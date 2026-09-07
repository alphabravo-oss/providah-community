package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"slices"
	"strings"
)

func creationPermitted(ctx context.Context, q *database.Queries, org, action string) bool {
	if action != "create" && action != "snapshot" {
		return true
	}
	policy, err := q.GetResourcePolicy(ctx, org)
	return err == nil && policy.CreationEnabled
}
func (s *Service) GetResourcePolicy(ctx context.Context, r *connect.Request[pb.GetResourcePolicyRequest]) (*connect.Response[pb.ResourcePolicy], error) {
	row, err := s.q.GetResourcePolicy(ctx, r.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ResourcePolicy{ApprovalActions: row.ApprovalActions, CreationEnabled: row.CreationEnabled, Revision: row.Revision}), nil
}
func (s *Service) SaveResourcePolicy(ctx context.Context, r *connect.Request[pb.SaveResourcePolicyRequest]) (*connect.Response[pb.ResourcePolicy], error) {
	v := r.Msg
	reason := strings.TrimSpace(v.Reason)
	actions := []string{"create", "start", "shutdown", "restart", "resize", "delete", "snapshot", "tags"}
	for i, action := range v.ApprovalActions {
		if !slices.Contains(actions, action) || slices.Contains(v.ApprovalActions[:i], action) {
			return nil, invalid("Choose unique, supported approval actions.")
		}
	}
	if v.ApprovalActions == nil {
		v.ApprovalActions = []string{}
	}
	if len(reason) < 3 || len(reason) > 500 {
		return nil, invalid("Provide a policy change reason in 3–500 characters.")
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if !hasPermission(ctx, q, v.OrganizationId, actor(ctx).UserID, "roles.manage") {
			return denied()
		}
		n, err := q.SaveResourcePolicy(ctx, database.SaveResourcePolicyParams{ID: v.OrganizationId, ApprovalActions: v.ApprovalActions, CreationEnabled: v.CreationEnabled, ResourcePolicyRevision: v.ExpectedRevision})
		if err != nil {
			return err
		}
		if n != 1 {
			return conflict("Resource policy changed. Reload before saving.")
		}
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "organization.resource_policy", v.OrganizationId, map[string]any{"approval_actions": v.ApprovalActions, "creation_enabled": v.CreationEnabled, "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.GetResourcePolicy(ctx, connect.NewRequest(&pb.GetResourcePolicyRequest{OrganizationId: v.OrganizationId}))
}

// A failed policy read must never waive approval.
func approvalRequired(ctx context.Context, q *database.Queries, org, action string, maintenanceException bool) bool {
	policy, err := q.GetResourcePolicy(ctx, org)
	return err != nil || slices.Contains(policy.ApprovalActions, action) || maintenanceException && len(policy.ApprovalActions) > 0
}
