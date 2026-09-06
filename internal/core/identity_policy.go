package core

import (
	"context"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/jackc/pgx/v5/pgtype"
)

func identityAllowed(ctx context.Context, q *database.Queries, org, user string, login pgtype.UUID) bool {
	allowed, err := q.IdentityAllows(ctx, database.IdentityAllowsParams{OrgID: org, UserID: user, OidcID: login})
	return err == nil && allowed
}
func (s *Service) GetIdentityPolicy(ctx context.Context, req *connect.Request[pb.GetIdentityPolicyRequest]) (*connect.Response[pb.IdentityPolicyResponse], error) {
	p, err := s.q.GetIdentityPolicy(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	out := &pb.IdentityPolicyResponse{Enabled: p.Enabled, Issuer: p.Issuer, RecoveryUsers: p.RecoveryUsers, Revision: p.Revision}
	if s.oidc != nil {
		out.ConfiguredIssuer = s.oidc.config.Issuer
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveIdentityPolicy(ctx context.Context, req *connect.Request[pb.SaveIdentityPolicyRequest]) (*connect.Response[pb.IdentityPolicyResponse], error) {
	r := req.Msg
	reason := strings.TrimSpace(r.Reason)
	if len(reason) < 10 || len(reason) > 500 || len(r.RecoveryUsers) > 20 {
		return nil, invalid("Provide a reason in 10–500 characters and at most twenty recovery administrators.")
	}
	users := slices.Clone(r.RecoveryUsers)
	slices.Sort(users)
	users = slices.Compact(users)
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := s.accessLock(ctx, q, r.OrganizationId, "identity.manage"); err != nil {
			return err
		}
		// Recheck the policy after acquiring the same organization lock used by membership changes.
		p := actor(ctx)
		if !identityAllowed(ctx, q, r.OrganizationId, p.UserID, p.OIDC) {
			return denied()
		}
		current, err := q.GetIdentityPolicy(ctx, r.OrganizationId)
		if err != nil {
			return err
		}
		if current.Revision != r.ExpectedRevision {
			return conflict("Identity policy changed. Reload before saving.")
		}
		issuer := current.Issuer
		if r.Enabled {
			if s.oidc == nil || !p.OIDC.Valid {
				return conflict("Sign in through the configured OIDC provider before enabling SSO enforcement.")
			}
			linked, err := q.UserOIDCLink(ctx, database.UserOIDCLinkParams{UserID: p.UserID, Issuer: s.oidc.config.Issuer})
			if err != nil || linked.ID != p.OIDC {
				return denied()
			}
			issuer = s.oidc.config.Issuer
			eligible, err := q.CountEligibleRecoveryUsers(ctx, database.CountEligibleRecoveryUsersParams{OrgID: r.OrganizationId, Users: users})
			if err != nil {
				return err
			}
			if len(users) == 0 || eligible != int64(len(users)) {
				return invalid("Designate at least one active administrator for local recovery sign-in.")
			}
		} else {
			users = []string{}
		}
		if err = q.SaveIdentityPolicy(ctx, database.SaveIdentityPolicyParams{OrgID: r.OrganizationId, Enabled: r.Enabled, Issuer: issuer, RecoveryUsers: users}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, p.Email, "identity.policy_changed", r.OrganizationId, map[string]any{"enabled": r.Enabled, "issuer": issuer, "recovery_users": users, "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.GetIdentityPolicy(ctx, connect.NewRequest(&pb.GetIdentityPolicyRequest{OrganizationId: r.OrganizationId}))
}
