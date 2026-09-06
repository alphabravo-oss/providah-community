package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func projectProto(p database.AutomationProject) *pb.AutomationProject {
	return &pb.AutomationProject{InputsHash: p.InputsHash, Id: p.ID, Name: p.Name, VersionId: p.VersionID, Lineage: p.Lineage, Serial: p.Serial, Sha256: p.Sha256, StateBytes: p.StateBytes, Locked: p.LockID != "", CreatedAt: p.CreatedAt.Time.Format(time.RFC3339)}
}
func (s *Service) GetAutomationProject(ctx context.Context, r *connect.Request[pb.GetAutomationProjectRequest]) (*connect.Response[pb.AutomationProject], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Msg.Id) {
		return nil, invalid("Invalid project ID.")
	}
	row, e := s.q.GetAutomationProject(ctx, database.GetAutomationProjectParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, denied()
	}
	if e != nil {
		return nil, e
	}
	summary, e := s.q.ProjectOwnershipSummary(ctx, database.ProjectOwnershipSummaryParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if e != nil {
		return nil, e
	}
	out := projectProto(row)
	out.Ownership = &pb.ProjectOwnershipSummary{Status: summary.Status, Unsupported: summary.Unsupported, ScannerVersion: summary.ScannerVersion, ScannedAt: stamp(summary.ScannedAt), ProtectedReferences: summary.ProtectedReferences, IncompleteVersions: summary.IncompleteVersions}
	return connect.NewResponse(out), nil
}
func (s *Service) ListAutomationProjects(ctx context.Context, r *connect.Request[pb.ListAutomationProjectsRequest]) (*connect.Response[pb.ListAutomationProjectsResponse], error) {
	rows, e := s.q.ListAutomationProjects(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	out := &pb.ListAutomationProjectsResponse{}
	for _, row := range rows {
		out.Projects = append(out.Projects, projectProto(row))
	}
	return connect.NewResponse(out), nil
}
func (s *Service) CreateAutomationProject(ctx context.Context, r *connect.Request[pb.CreateAutomationProjectRequest]) (*connect.Response[pb.AutomationProject], error) {
	v := r.Msg
	name := strings.TrimSpace(v.Name)
	if name == "" || len(name) > 80 {
		return nil, invalid("Choose a project name of at most 80 characters.")
	}
	var out *pb.AutomationProject
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, v.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, v.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		rows, e := q.ListAutomationProjects(ctx, v.OrganizationId)
		if e != nil {
			return e
		}
		for _, p := range rows {
			if p.Name == name && p.VersionID != v.VersionId {
				return conflict("This project name already has a different source or input binding.")
			}
		}
		source, e := q.AutomationValidationSource(ctx, database.AutomationValidationSourceParams{OrgID: v.OrganizationId, ID: v.VersionId})
		if e != nil {
			return denied()
		}
		var fields []inputspec.Field
		if json.Unmarshal(source.InputSchema, &fields) != nil {
			return conflict("Input schema is unavailable.")
		}
		values, e := inputspec.Values(fields, []byte(v.InputsJson))
		if e != nil {
			return invalid(e.Error())
		}
		inputHash := hex.EncodeToString(hash(string(values)))
		for _, p := range rows {
			if p.Name == name {
				if p.VersionID != v.VersionId || p.InputsHash != inputHash {
					return conflict("This project name already has a different source or input binding.")
				}
				out = projectProto(p)
				return nil
			}
		}
		if len(rows) >= 200 {
			return conflict("The organization is limited to 200 automation projects.")
		}
		version, e := s.validationSource(ctx, q, database.AutomationValidation{OrgID: v.OrganizationId, VersionID: v.VersionId, RequesterID: actor(ctx).UserID, RequesterOidcID: actor(ctx).OIDC})
		if e != nil {
			return e
		}
		if version.Runtime != "opentofu" && version.Runtime != "terraform" {
			return invalid("Managed state projects require Terraform or OpenTofu.")
		}
		ciphertext, e := s.seal(string(values))
		if e != nil {
			return e
		}
		p, e := q.CreateAutomationProject(ctx, database.CreateAutomationProjectParams{InputsCiphertext: ciphertext, InputsHash: inputHash, ID: randomID(), OrgID: v.OrganizationId, Name: name, VersionID: v.VersionId})
		if e != nil {
			return e
		}
		out = projectProto(p)
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "automation.project_created", p.ID, map[string]any{"version_id": v.VersionId, "inputs_hash": inputHash})
	})
	return connect.NewResponse(out), e
}
func (s *Service) ListAutomationStates(ctx context.Context, r *connect.Request[pb.ListAutomationStatesRequest]) (*connect.Response[pb.ListAutomationStatesResponse], error) {
	before := int64(-1)
	if r.Msg.BeforeSerial != "" {
		var e error
		before, e = strconv.ParseInt(r.Msg.BeforeSerial, 10, 64)
		if e != nil || before < 0 {
			return nil, invalid("Invalid state history cursor.")
		}
	}
	rows, e := s.q.ListAutomationStates(ctx, database.ListAutomationStatesParams{OrgID: r.Msg.OrganizationId, ProjectID: r.Msg.ProjectId, BeforeSerial: before})
	if e != nil {
		return nil, e
	}
	out := &pb.ListAutomationStatesResponse{}
	if len(rows) > 100 {
		out.NextBeforeSerial = strconv.FormatInt(rows[99].Serial, 10)
		rows = rows[:100]
	}
	for _, v := range rows {
		out.States = append(out.States, &pb.AutomationState{Id: v.ID, Lineage: v.Lineage, Serial: v.Serial, Sha256: v.Sha256, StateBytes: v.StateBytes, CreatedAt: v.CreatedAt.Time.Format(time.RFC3339)})
	}
	return connect.NewResponse(out), nil
}
