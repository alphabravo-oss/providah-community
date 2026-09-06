package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"time"
)

func (s *Service) deletionImpact(ctx context.Context, q *database.Queries, c database.Connection, r database.GetResourceRow, runtime string) (string, error) {
	if err := ownershipGuard(ctx, q, c.OrgID, c.ID, r.Kind, r.NativeID, r.Region, "delete"); err != nil {
		return "", err
	}
	if !s.runtimeCapabilities(c.Provider, runtime).SupportsResourceAction(r.Kind, "delete") {
		return "", conflict("Selected runtime does not support deletion.")
	}
	if !c.Enabled || c.DeletedAt.Valid || !provider.ResourceActionAllowed(r.Kind, "delete", r.Status) || !powerPermission(ctx, q, c.OrgID, actor(ctx).UserID, "operations.delete") {
		return "", denied()
	}
	if s.cfg.ProviderCall == nil {
		return "", conflict("Provider execution is not configured.")
	}
	raw, err := s.open(c.Ciphertext)
	if err != nil {
		return "", err
	}
	raw, err = s.providerCredential(ctx, c.Provider, raw, r.Region, c.OrgID, c.ID)
	if err != nil {
		return "", conflict("Could not authenticate to inspect deletion impact.")
	}
	// ponytail: bounded read-only preview holds the connection lock; split and recheck revisions if contention grows.
	callCtx, cancel := context.WithTimeout(ctx, 140*time.Second)
	defer cancel()
	kind := ""
	if r.Kind != "compute.server" {
		kind = r.Kind
	}
	out, err := s.callProvider(callCtx, provider.Request{Version: provider.Protocol, RuntimeID: runtime, OrganizationID: c.OrgID, ConnectionID: c.ID, Provider: c.Provider, Region: r.Region, Credential: raw, Power: &provider.PowerRequest{ResourceKind: kind, OperationID: randomID(), Phase: "preview", Action: "delete", NativeID: r.NativeID, ExpectedStatus: r.Status}})
	if err != nil || out.Validate() != nil || out.Power == nil || out.Power.Outcome != "preview" || out.Power.Status != r.Status {
		return "", conflict("Could not verify deletion impact. Refresh the resource and check provider permissions or protection settings.")
	}
	if !powerPermission(ctx, q, c.OrgID, actor(ctx).UserID, "operations.delete") || !powerPermission(ctx, q, c.OrgID, actor(ctx).UserID, "operations.request") {
		return "", denied()
	}
	return out.Power.DeletionImpact, nil
}
func (s *Service) PreviewDeletion(ctx context.Context, req *connect.Request[pb.PreviewDeletionRequest]) (*connect.Response[pb.PreviewDeletionResponse], error) {
	impact := ""
	err := s.transaction(ctx, func(q *database.Queries) error {
		r, err := q.GetResource(ctx, database.GetResourceParams{OrgID: req.Msg.OrganizationId, ID: req.Msg.ResourceId})
		if err != nil {
			return denied()
		}
		c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: req.Msg.OrganizationId, ID: r.ConnectionID})
		if err != nil {
			return err
		}
		r, err = q.GetResource(ctx, database.GetResourceParams{OrgID: req.Msg.OrganizationId, ID: req.Msg.ResourceId})
		if err != nil {
			return denied()
		}
		if err = requireModule(ctx, q, c.OrgID, c.Provider, 0); err != nil {
			return err
		}
		runtime, _, err := s.runtimeFor(ctx, q, c.OrgID, c.Provider, 0)
		if err != nil {
			return err
		}
		impact, err = s.deletionImpact(ctx, q, c, r, runtime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PreviewDeletionResponse{Impact: impact, Digest: provider.ImpactDigest(impact)}), nil
}
