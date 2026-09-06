package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/jackc/pgx/v5/pgtype"
	"slices"
)

func (s *Service) mfaPolicyAccess(ctx context.Context, q *database.Queries, org string) error {
	p := actor(ctx)
	u, e := q.UserByEmail(ctx, p.Email)
	if e != nil || u.ID != p.UserID {
		return unauthenticated()
	}
	if org == "" {
		if !u.GlobalAdmin {
			return denied()
		}
		return nil
	}
	permissions, e := q.Permissions(ctx, database.PermissionsParams{OrgID: org, UserID: p.UserID})
	if e != nil || !slices.Contains(permissions, "identity.manage") || !identityAllowed(ctx, q, org, p.UserID, p.OIDC) {
		return denied()
	}
	return nil
}
func (s *Service) GetMFAPolicy(ctx context.Context, r *connect.Request[pb.GetMFAPolicyRequest]) (*connect.Response[pb.MFAPolicy], error) {
	if e := s.mfaPolicyAccess(ctx, s.q, r.Msg.OrganizationId); e != nil {
		return nil, e
	}
	row, e := s.q.GetMFAPolicy(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.MFAPolicy{Required: row.Required, GlobalRequired: row.GlobalRequired, Revision: row.Revision}), nil
}
func (s *Service) SaveMFAPolicy(ctx context.Context, r *connect.Request[pb.SaveMFAPolicyRequest]) (*connect.Response[pb.MFAPolicy], error) {
	v := r.Msg
	e := s.accountChange(ctx, v.Password, v.Code, func(q *database.Queries) error {
		// ponytail: serialize rare policy changes on the installation row.
		if e := q.LockMFAPolicies(ctx); e != nil {
			return e
		}
		if e := s.mfaPolicyAccess(ctx, q, v.OrganizationId); e != nil {
			return e
		}
		row, e := q.GetMFAPolicy(ctx, v.OrganizationId)
		if e != nil {
			return e
		}
		if row.Revision != v.ExpectedRevision {
			return conflict("MFA policy changed. Reload before saving.")
		}
		u, e := q.UserByEmail(ctx, actor(ctx).Email)
		if e != nil {
			return e
		}
		if (v.Required || row.GlobalRequired) && !u.MfaEnabled {
			return conflict("Enable MFA on your own account before requiring or administering required MFA.")
		}
		if e = q.SaveMFAPolicy(ctx, database.SaveMFAPolicyParams{Scope: v.OrganizationId, Required: v.Required}); e != nil {
			return e
		}
		action := "identity.mfa_optional"
		if v.Required {
			action = "identity.mfa_required"
		}
		if v.OrganizationId == "" {
			if e = q.AddInstallationPolicyEvent(ctx, database.AddInstallationPolicyEventParams{ActorID: pgtype.Text{String: u.ID, Valid: true}, Action: action + ".global", Required: v.Required, Revision: row.Revision + 1}); e != nil {
				return e
			}
			return accountAudit(ctx, q, u.ID, u.Email, action+".global")
		}
		return audit(ctx, q, v.OrganizationId, u.Email, action, v.OrganizationId, nil)
	})
	if e != nil {
		return nil, e
	}
	return s.GetMFAPolicy(ctx, connect.NewRequest(&pb.GetMFAPolicyRequest{OrganizationId: v.OrganizationId}))
}
