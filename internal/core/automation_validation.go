package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"slices"
	"strings"
	"time"
)

func validationProto(row database.AutomationValidation) *pb.AutomationValidation {
	return &pb.AutomationValidation{ProjectId: row.ProjectID, Id: row.ID, VersionId: row.VersionID, Status: row.Status, Detail: row.Detail, CreatedAt: row.CreatedAt.Time.Format(time.RFC3339)}
}
func (s *Service) ListAutomationValidations(ctx context.Context, r *connect.Request[pb.ListAutomationValidationsRequest]) (*connect.Response[pb.ListAutomationValidationsResponse], error) {
	v := r.Msg
	if !slices.Contains([]string{"", "queued", "running", "succeeded", "failed", "canceled"}, v.Status) || len(v.PageToken) > 2048 {
		return nil, invalid("Choose a valid validation status or page.")
	}
	filter := "automation-validations:" + v.Status
	key, e := parseCursor(v.PageToken, v.OrganizationId, filter)
	if e != nil {
		return nil, e
	}
	var before pgtype.Timestamptz
	beforeID := ""
	if key != "" {
		stamp, id, ok := strings.Cut(key, "|")
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if !ok || err != nil || !notificationRecordID.MatchString(id) {
			return nil, invalid("Invalid validation page.")
		}
		before = pgtype.Timestamptz{Time: at, Valid: true}
		beforeID = id
	}
	rows, e := s.q.ListAutomationValidations(ctx, database.ListAutomationValidationsParams{OrgID: v.OrganizationId, Status: v.Status, BeforeAt: before, BeforeID: beforeID})
	if e != nil {
		return nil, e
	}
	out := &pb.ListAutomationValidationsResponse{RunnerConfigured: s.cfg.AutomationCall != nil}
	if len(rows) > 100 {
		last := rows[99]
		out.NextPageToken = cursor(v.OrganizationId, filter, last.CreatedAt.Time.UTC().Format(time.RFC3339Nano)+"|"+last.ID)
		rows = rows[:100]
	}
	for _, row := range rows {
		out.Validations = append(out.Validations, validationProto(row))
	}
	return connect.NewResponse(out), nil
}

func (s *Service) GetAutomationValidation(ctx context.Context, r *connect.Request[pb.GetAutomationValidationRequest]) (*connect.Response[pb.AutomationValidation], error) {
	if !notificationRecordID.MatchString(r.Msg.Id) {
		return nil, invalid("Invalid validation identifier.")
	}
	row, err := s.q.GetAutomationValidation(ctx, database.GetAutomationValidationParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(validationProto(row)), nil
}

type validationSource struct {
	database.AutomationValidationSourceRow
	Inputs json.RawMessage
}

func (s *Service) validationSource(ctx context.Context, q *database.Queries, job database.AutomationValidation) (validationSource, error) {
	var empty validationSource
	if !hasPermission(ctx, q, job.OrgID, job.RequesterID, "templates.publish") || !identityAllowed(ctx, q, job.OrgID, job.RequesterID, job.RequesterOidcID) {
		return empty, denied()
	}
	v, e := q.AutomationValidationSource(ctx, database.AutomationValidationSourceParams{OrgID: job.OrgID, ID: job.VersionID})
	if e != nil {
		return empty, e
	}
	allowed := false
	for _, r := range s.automationRuntimes {
		allowed = allowed || r.Matches(v.Runtime, v.RuntimeImage, v.RuntimeVersion, v.RuntimePolicy)
	}
	if v.Status != "published" || !allowed {
		return empty, conflict("Publish a version with an allowed runtime before validating.")
	}
	c, e := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: job.OrgID, ID: v.ConnectionID})
	if e != nil {
		return empty, e
	}
	if !c.Enabled || c.DeletedAt.Valid || c.Revision != v.ConnectionRevision {
		return empty, conflict("The bound connection changed. Publish a new version.")
	}
	out := validationSource{AutomationValidationSourceRow: v}
	if job.ProjectID != "" {
		project, e := q.LockAutomationProject(ctx, database.LockAutomationProjectParams{OrgID: job.OrgID, ID: job.ProjectID})
		if e != nil || project.VersionID != job.VersionID {
			return empty, denied()
		}
		values := "{}"
		if len(project.InputsCiphertext) > 0 {
			values, e = s.openBounded(project.InputsCiphertext, inputspec.MaxBytes)
			if e != nil {
				return empty, e
			}
		}
		var fields []inputspec.Field
		if json.Unmarshal(v.InputSchema, &fields) != nil {
			return empty, conflict("Input schema is unavailable.")
		}
		out.Inputs, e = inputspec.Values(fields, []byte(values))
		if e != nil || hex.EncodeToString(hash(string(out.Inputs))) != project.InputsHash {
			return empty, conflict("Project inputs failed integrity checks.")
		}
	}
	return out, nil
}
func (s *Service) RequestAutomationValidation(ctx context.Context, r *connect.Request[pb.RequestAutomationValidationRequest]) (*connect.Response[pb.AutomationValidation], error) {
	if s.cfg.AutomationCall == nil {
		return nil, conflict("The isolated validation runner is not configured.")
	}
	if e := s.limit(ctx, "automation-validation:"+actor(ctx).UserID); e != nil {
		return nil, e
	}
	var out database.AutomationValidation
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, r.Msg.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		job := database.AutomationValidation{ProjectID: r.Msg.ProjectId, OrgID: r.Msg.OrganizationId, VersionID: r.Msg.VersionId, RequesterID: actor(ctx).UserID, RequesterEmail: actor(ctx).Email, RequesterOidcID: actor(ctx).OIDC}
		if _, e := s.validationSource(ctx, q, job); e != nil {
			return e
		}
		var e error
		out, e = q.QueueAutomationValidation(ctx, database.QueueAutomationValidationParams{ProjectID: job.ProjectID, ID: randomID(), OrgID: job.OrgID, VersionID: job.VersionID, RequesterID: job.RequesterID, RequesterEmail: job.RequesterEmail, RequesterOidcID: job.RequesterOidcID})
		if errors.Is(e, pgx.ErrNoRows) {
			out, e = q.ActiveAutomationValidation(ctx, database.ActiveAutomationValidationParams{ProjectID: job.ProjectID, OrgID: job.OrgID, VersionID: job.VersionID})
			return e
		}
		if e != nil {
			return e
		}
		return audit(ctx, q, job.OrgID, job.RequesterEmail, "automation.validation_requested", out.ID, map[string]any{"version_id": job.VersionID})
	})
	return connect.NewResponse(validationProto(out)), e
}

func (s *Service) CancelAutomationValidation(ctx context.Context, req *connect.Request[pb.CancelAutomationValidationRequest]) (*connect.Response[pb.AutomationValidation], error) {
	if !stateID.MatchString(req.Msg.Id) {
		return nil, invalid("Invalid validation ID.")
	}
	var out database.AutomationValidation
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := s.accessLock(ctx, q, req.Msg.OrganizationId, "templates.publish"); err != nil {
			return err
		}
		var err error
		out, err = q.CancelAutomationValidation(ctx, database.CancelAutomationValidationParams{OrgID: req.Msg.OrganizationId, ID: req.Msg.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return conflict("Validation is unavailable or already finished. Reload its status.")
		}
		if err != nil {
			return err
		}
		return audit(ctx, q, out.OrgID, actor(ctx).Email, "automation.validation_canceled", out.ID, map[string]any{"version_id": out.VersionID})
	})
	return connect.NewResponse(validationProto(out)), err
}

// Stop the isolated request if another replica cancels the job or its lease expires.
func (s *Service) watchValidation(ctx context.Context, cancel context.CancelFunc, id string, done chan<- struct{}) {
	defer close(done)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			check, stop := context.WithTimeout(ctx, 2*time.Second)
			running, err := s.q.AutomationValidationRunning(check, id)
			stop()
			if err != nil || !running {
				cancel()
				return
			}
		}
	}
}

// ponytail: one validation per core replica; the shared launcher also bounds total container concurrency.
func (s *Service) StartAutomationValidation(ctx context.Context) {
	if s.cfg.AutomationCall == nil {
		return
	}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if e := s.automationValidationTick(ctx); e != nil && !errors.Is(e, context.Canceled) {
				s.log.Error().Msg("automation validation coordinator failed")
			}
		}
	}
}
func (s *Service) automationValidationTick(ctx context.Context) error {
	e := s.transaction(ctx, func(q *database.Queries) error {
		expired, e := q.ExpireAutomationValidations(ctx)
		if e != nil {
			return e
		}
		for _, j := range expired {
			if e = audit(ctx, q, j.OrgID, j.RequesterEmail, "automation.validation_failed", j.ID, nil); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	job, e := s.q.ClaimAutomationValidation(ctx)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	return s.runAutomationValidation(ctx, job)
}
func (s *Service) runAutomationValidation(ctx context.Context, job database.AutomationValidation) error {
	var source validationSource
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := q.LockAccess(ctx, job.OrgID); e != nil {
			return e
		}
		if _, e := q.LockAutomationValidation(ctx, job.ID); e != nil {
			return e
		}
		var e error
		source, e = s.validationSource(ctx, q, job)
		return e
	})
	status, detail := "canceled", "Version, connection, or requester authority changed."
	if e == nil {
		status, detail = "failed", "Validation failed or dependencies are unavailable. Review the source locally."
		raw, err := s.openArtifact(ctx, "sources/"+source.OrgID+"/"+source.SourceID, source.Storage, source.Ciphertext, sourcebundle.MaxArchive)
		if err == nil {
			request := automation.Request{Inputs: source.Inputs, RuntimePolicy: source.RuntimePolicy, Runtime: source.Runtime, Image: source.RuntimeImage, Entrypoint: source.Entrypoint, SHA256: source.Sha256, Archive: []byte(raw)}
			if request.Validate() == nil {
				run, cancel := context.WithTimeout(ctx, 135*time.Second)
				done := make(chan struct{})
				go s.watchValidation(run, cancel, job.ID, done)
				result, err := s.cfg.AutomationCall(run, request)
				cancel()
				<-done
				if err == nil && result.Status == "succeeded" {
					status, detail = "succeeded", "Source syntax and declared input constraints passed. This is not a plan or approval to execute."
				}
			}
		}
	}
	return s.transaction(ctx, func(q *database.Queries) error {
		if _, e := q.LockAccess(ctx, job.OrgID); e != nil {
			return e
		}
		if _, e := q.LockAutomationValidation(ctx, job.ID); errors.Is(e, pgx.ErrNoRows) {
			return nil
		} else if e != nil {
			return e
		}
		if _, e := s.validationSource(ctx, q, job); e != nil {
			status, detail = "canceled", "Version, connection, or requester authority changed."
		}
		if e := q.FinishAutomationValidation(ctx, database.FinishAutomationValidationParams{ID: job.ID, Status: status, Detail: detail}); e != nil {
			return e
		}
		return audit(ctx, q, job.OrgID, job.RequesterEmail, "automation.validation_"+status, job.ID, map[string]any{"version_id": job.VersionID})
	})
}
