package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Signed previews freeze a bounded target set without another queue or temporary table.
// Every target uses the existing durable operation and its own stable idempotency key.
type powerReview struct {
	jwt.RegisteredClaims
	Organization        string `json:"org"`
	Resource            string `json:"resource"`
	Action              string `json:"action"`
	Status              string `json:"status"`
	Runtime             string `json:"runtime"`
	ConnectionRevision  int64  `json:"connection_revision"`
	ModuleRevision      int64  `json:"module_revision"`
	MaintenanceRevision int64  `json:"maintenance_revision"`
	WindowEnd           int64  `json:"window_end"`
}

func (s *Service) PreviewBulkPower(ctx context.Context, req *connect.Request[pb.PreviewBulkPowerRequest]) (*connect.Response[pb.PreviewBulkPowerResponse], error) {
	r := req.Msg
	if len(r.ResourceIds) == 0 || len(r.ResourceIds) > 50 || !slices.Contains([]string{"start", "shutdown", "restart"}, r.Action) {
		return nil, invalid("Choose one power action and between one and fifty exact targets.")
	}
	if !powerPermission(ctx, s.q, r.OrganizationId, actor(ctx).UserID, "operations.request") {
		return nil, denied()
	}
	seen := map[string]bool{}
	for _, id := range r.ResourceIds {
		if id == "" || len(id) > 64 || seen[id] {
			return nil, invalid("Choose distinct resource IDs.")
		}
		seen[id] = true
	}
	out := &pb.PreviewBulkPowerResponse{}
	for _, id := range r.ResourceIds {
		target, err := s.previewPowerTarget(ctx, r.OrganizationId, id, r.Action)
		if err != nil {
			return nil, err
		}
		out.Targets = append(out.Targets, target)
	}
	return connect.NewResponse(out), nil
}
func (s *Service) previewPowerTarget(ctx context.Context, org, id, action string) (*pb.BulkPowerTarget, error) {
	out := &pb.BulkPowerTarget{Action: action, ResourceId: id, Eligibility: "inaccessible", Detail: "Target is unavailable or inaccessible."}
	resource, err := s.q.GetResource(ctx, database.GetResourceParams{OrgID: org, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Name, out.NativeId, out.Provider, out.Region, out.Status = resource.Name, resource.NativeID, resource.Provider, resource.Region, resource.Status
	out.Eligibility = "ineligible"
	if resource.Kind != "compute.server" {
		out.Eligibility = "unsupported"
		out.Detail = "Power actions require a server."
		return out, nil
	}
	c, err := s.q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: org, ID: resource.ConnectionID})
	if err != nil {
		return nil, err
	}
	enabled, err := moduleAllowed(ctx, s.q, org, c.Provider, 0)
	if err != nil {
		return nil, err
	}
	if !enabled || !c.Enabled || c.DeletedAt.Valid {
		out.Detail = "Connection or provider module is disabled."
		return out, nil
	}
	if !provider.PowerAllowed(action, resource.Status) || time.Since(resource.ObservedAt.Time) > 6*time.Minute {
		out.Detail = "Server state is ineligible or inventory needs refreshing."
		return out, nil
	}
	runtime, revision, err := s.runtimeFor(ctx, s.q, org, c.Provider, 0)
	if err != nil {
		return nil, err
	}
	if !s.runtimeCapabilities(c.Provider, runtime).SupportsAction(action) {
		out.Eligibility = "unsupported"
		out.Detail = "Selected provider runtime does not support this action."
		return out, nil
	}
	maintenance, restriction, end, err := maintenanceCheck(ctx, s.q, org, c.ID, time.Now())
	if err != nil {
		return nil, err
	}
	if restriction != "" {
		out.Detail = restriction
		return out, nil
	}
	busy, err := s.q.ResourceOperationBusy(ctx, database.ResourceOperationBusyParams{OrgID: org, ResourceID: pgtype.Text{String: id, Valid: true}})
	if err != nil {
		return nil, err
	}
	if busy {
		out.Detail = "Pending or uncertain work must finish or be resolved first."
		return out, nil
	}
	if s.cfg.ProviderCall == nil {
		out.Detail = "Provider execution is unavailable."
		return out, nil
	}
	proof := powerReview{RegisteredClaims: jwt.RegisteredClaims{Issuer: "providah", Audience: jwt.ClaimStrings{"providah-bulk-power"}, Subject: actor(ctx).UserID, ID: randomID(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute))}, Organization: org, Resource: id, Action: action, Status: resource.Status, Runtime: runtime, ConnectionRevision: c.Revision, ModuleRevision: revision, MaintenanceRevision: maintenance, WindowEnd: end.Unix()}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, proof).SignedString([]byte(s.cfg.SessionKey))
	if err != nil {
		return nil, err
	}
	out.Eligibility, out.Detail, out.ReviewToken = "eligible", "Ready for an independent operation request.", token
	return out, nil
}
func (s *Service) RequestBulkPower(ctx context.Context, req *connect.Request[pb.RequestBulkPowerRequest]) (*connect.Response[pb.RequestBulkPowerResponse], error) {
	r := req.Msg
	r.Reason = strings.TrimSpace(r.Reason)
	if len(r.ReviewTokens) == 0 || len(r.ReviewTokens) > 50 || len(r.Reason) < 3 || len(r.Reason) > 500 {
		return nil, invalid("Provide one to fifty reviewed targets and a reason in 3–500 characters.")
	}
	proofs := make([]*powerReview, 0, len(r.ReviewTokens))
	seen := map[string]bool{}
	// Validate the whole envelope before persisting any intent; eligibility is per target.
	for _, raw := range r.ReviewTokens {
		if len(raw) > 4096 {
			return nil, invalid("Invalid power review.")
		}
		proof := &powerReview{}
		_, err := jwt.ParseWithClaims(raw, proof, func(*jwt.Token) (any, error) { return []byte(s.cfg.SessionKey), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("providah"), jwt.WithAudience("providah-bulk-power"), jwt.WithExpirationRequired())
		if err != nil || proof.Organization != r.OrganizationId || proof.Subject != actor(ctx).UserID || seen[proof.Resource] || !slices.Contains([]string{"start", "shutdown", "restart"}, proof.Action) {
			return nil, invalid("Power review is invalid, expired, duplicated, or belongs to another account.")
		}
		seen[proof.Resource] = true
		proofs = append(proofs, proof)
	}
	out := &pb.RequestBulkPowerResponse{}
	// ponytail: at most fifty short, sequential transactions; provider execution stays asynchronous.
	for _, proof := range proofs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		operation, err := s.requestOperation(ctx, &pb.RequestOperationRequest{OrganizationId: r.OrganizationId, ResourceId: proof.Resource, Action: proof.Action, ExpectedStatus: proof.Status, Reason: r.Reason, IdempotencyKey: proof.ID}, proof)
		item := &pb.BulkPowerResult{ResourceId: proof.Resource}
		if err != nil {
			var ce *connect.Error
			if errors.As(err, &ce) {
				item.Error = ce.Message()
			} else {
				item.Error = "Request could not be recorded. Retry the same reviewed request."
			}
		} else {
			item.Operation = operation.Msg.Operation
		}
		out.Results = append(out.Results, item)
	}
	return connect.NewResponse(out), nil
}
