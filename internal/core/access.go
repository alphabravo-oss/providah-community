package core

import (
	"context"
	"encoding/hex"
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

var permissionCatalog = []string{"dashboards.manage", "admin.access", "templates.read", "templates.publish", "identity.manage", "modules.read", "modules.manage", "connections.read", "connections.manage", "resources.read", "audit.read", "members.read", "members.manage", "roles.manage", "operations.read", "operations.request", "operations.delete", "operations.create", "operations.approve", "schedules.read", "schedules.manage", "notifications.read", "notifications.manage", "maintenance.read", "maintenance.manage", "maintenance.override", "audit.export.manage"}

func subset(want, have []string) bool {
	for _, p := range want {
		if !slices.Contains(have, p) {
			return false
		}
	}
	return true
}
func (s *Service) accessLock(ctx context.Context, q *database.Queries, org, permission string) ([]string, error) {
	if _, err := q.LockAccess(ctx, org); err != nil {
		return nil, denied()
	}
	p, err := q.Permissions(ctx, database.PermissionsParams{OrgID: org, UserID: actor(ctx).UserID})
	if err != nil || !slices.Contains(p, permission) {
		return nil, denied()
	}
	return p, nil
}
func (s *Service) ListAccess(ctx context.Context, req *connect.Request[pb.ListAccessRequest]) (*connect.Response[pb.ListAccessResponse], error) {
	out := &pb.ListAccessResponse{PermissionCatalog: permissionCatalog}
	roles, err := s.q.ListRoles(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		out.Roles = append(out.Roles, &pb.Role{Id: r.ID, Name: r.Name, Permissions: r.Permissions, Builtin: r.Builtin, Revision: r.Revision})
	}
	members, err := s.q.ListMembers(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	invites, err := s.q.ListInvitations(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	if len(members) > 1000 || len(invites) > 1000 {
		return nil, conflict("Access directory exceeds the current 1,000-entry limit.")
	}
	for _, m := range members {
		out.Members = append(out.Members, &pb.Member{UserId: m.UserID, Email: m.Email, RoleId: m.RoleID, RoleName: m.RoleName, Permissions: m.Permissions, Active: m.Active && m.UserActive, Revision: m.Revision})
	}
	for _, i := range invites {
		out.Invitations = append(out.Invitations, &pb.Invitation{Id: i.ID, Email: i.Email, RoleId: i.RoleID, RoleName: i.RoleName, ExpiresAt: stamp(i.ExpiresAt)})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveRole(ctx context.Context, req *connect.Request[pb.SaveRoleRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	name := strings.TrimSpace(r.Name)
	if (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && r.ExpectedRevision < 1) {
		return nil, invalid("Reload the role before saving its current revision.")
	}
	if len(name) == 0 || len(name) > 80 || len(r.Permissions) == 0 || len(r.Permissions) > len(permissionCatalog) || !subset(r.Permissions, permissionCatalog) {
		return nil, invalid("Choose a role name and known permissions.")
	}
	perms := slices.Clone(r.Permissions)
	slices.Sort(perms)
	perms = slices.Compact(perms)
	err := s.transaction(ctx, func(q *database.Queries) error {
		own, err := s.accessLock(ctx, q, r.OrganizationId, "roles.manage")
		if err != nil {
			return err
		}
		if !subset(perms, own) {
			return denied()
		}
		id := r.Id
		if id == "" {
			id = randomID()
			err = q.CreateRole(ctx, database.CreateRoleParams{OrgID: r.OrganizationId, ID: id, Name: name, Permissions: perms})
		} else {
			old, e := q.GetRole(ctx, database.GetRoleParams{OrgID: r.OrganizationId, ID: id})
			if e != nil || !subset(old.Permissions, own) {
				return denied()
			}
			if old.Builtin {
				return conflict("Built-in roles cannot be edited. Create a custom role.")
			}
			var changed int64
			changed, err = q.UpdateRole(ctx, database.UpdateRoleParams{OrgID: r.OrganizationId, ID: id, Name: name, Permissions: perms, Revision: r.ExpectedRevision})
			if err == nil && changed != 1 {
				return conflict("Role changed. Close and reopen the editor before saving.")
			}
		}
		if err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "role.saved", id, map[string]any{"name": name, "permissions": perms})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) UpdateMember(ctx context.Context, req *connect.Request[pb.UpdateMemberRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if r.ExpectedRevision < 1 {
		return nil, invalid("Reload the membership before saving its current revision.")
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		own, err := s.accessLock(ctx, q, r.OrganizationId, "members.manage")
		if err != nil {
			return err
		}
		role, err := q.GetRole(ctx, database.GetRoleParams{OrgID: r.OrganizationId, ID: r.RoleId})
		if err != nil || !subset(role.Permissions, own) {
			return denied()
		}
		// A delegated manager may neither grant nor remove authority beyond their own.
		old, err := q.Permissions(ctx, database.PermissionsParams{OrgID: r.OrganizationId, UserID: r.UserId})
		if err == nil && !subset(old, own) {
			return denied()
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		n, err := q.SetMember(ctx, database.SetMemberParams{OrgID: r.OrganizationId, UserID: r.UserId, RoleID: pgtype.Text{String: r.RoleId, Valid: true}, Active: r.Active, Revision: r.ExpectedRevision})
		if err != nil {
			return err
		}
		if n != 1 {
			return conflict("Membership changed or is unavailable. Close and reopen the editor before saving.")
		}
		count, err := q.AdministratorCount(ctx, r.OrganizationId)
		if err != nil {
			return err
		}
		if count == 0 {
			return conflict("Keep at least one active administrator.")
		}
		missing, err := q.IdentityRecoveryMissing(ctx, r.OrganizationId)
		if err != nil {
			return err
		}
		if missing {
			return conflict("Keep an active designated recovery administrator before removing this membership or role.")
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "member.updated", r.UserId, map[string]any{"role": r.RoleId, "active": r.Active})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) CreateInvitation(ctx context.Context, req *connect.Request[pb.CreateInvitationRequest]) (*connect.Response[pb.CreateInvitationResponse], error) {
	r := req.Msg
	email := strings.ToLower(strings.TrimSpace(r.Email))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 {
		return nil, invalid("Enter a valid email address.")
	}
	token, id := randomID(), randomID()
	err = s.transaction(ctx, func(q *database.Queries) error {
		own, err := s.accessLock(ctx, q, r.OrganizationId, "members.manage")
		if err != nil {
			return err
		}
		role, err := q.GetRole(ctx, database.GetRoleParams{OrgID: r.OrganizationId, ID: r.RoleId})
		if err != nil || !subset(role.Permissions, own) {
			return denied()
		}
		if err = q.CreateInvitation(ctx, database.CreateInvitationParams{ID: id, OrgID: r.OrganizationId, Email: email, RoleID: role.ID, RoleRevision: role.Revision, InviterID: actor(ctx).UserID, TokenHash: hash(token)}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "invitation.created", id, map[string]any{"email": email, "role": role.ID})
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.CreateInvitationResponse{Link: s.cfg.Origin + "/invite#" + token}), nil
}
func (s *Service) RevokeInvitation(ctx context.Context, req *connect.Request[pb.RevokeInvitationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := s.accessLock(ctx, q, r.OrganizationId, "members.manage"); err != nil {
			return err
		}
		n, err := q.RevokeInvitation(ctx, database.RevokeInvitationParams{OrgID: r.OrganizationId, ID: r.Id})
		if err != nil {
			return err
		}
		if n != 1 {
			return denied()
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "invitation.revoked", r.Id, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

// Lock organization first, then re-read the token to serialize acceptance, revocation and role edits.
func (s *Service) invitation(ctx context.Context, q *database.Queries, token string) (database.Invitation, error) {
	bad := conflict("Invitation is invalid, expired, revoked, or its access has changed. Request a new invitation.")
	if len(token) != 64 {
		return database.Invitation{}, bad
	}
	i, err := q.InvitationByToken(ctx, hash(token))
	if err != nil {
		return i, bad
	}
	if _, err = q.LockAccess(ctx, i.OrgID); err != nil {
		return i, bad
	}
	i, err = q.InvitationByToken(ctx, hash(token))
	if err != nil {
		return i, bad
	}
	role, err := q.GetRole(ctx, database.GetRoleParams{OrgID: i.OrgID, ID: i.RoleID})
	if err != nil || role.Revision != i.RoleRevision {
		return i, bad
	}
	own, err := q.Permissions(ctx, database.PermissionsParams{OrgID: i.OrgID, UserID: i.InviterID})
	if err != nil || !slices.Contains(own, "members.manage") || !subset(role.Permissions, own) {
		return i, bad
	}
	return i, nil
}
func (s *Service) BeginInvitation(ctx context.Context, req *connect.Request[pb.BeginInvitationRequest]) (*connect.Response[pb.BeginInvitationResponse], error) {
	if err := s.limit(ctx, "invite:"+hex.EncodeToString(hash(req.Msg.Token))); err != nil {
		return nil, err
	}
	out := &pb.BeginInvitationResponse{}
	err := s.transaction(ctx, func(q *database.Queries) error {
		i, err := s.invitation(ctx, q, req.Msg.Token)
		if err != nil {
			return err
		}
		out.Email = i.Email
		out.RequiresSignIn, err = q.UserExists(ctx, i.Email)
		if err != nil || out.RequiresSignIn {
			return err
		}
		// Reuse this invitation's pending seed; opening another tab must not invalidate enrollment.
		if len(i.TotpCiphertext) > 0 {
			out.Secret, err = s.open(i.TotpCiphertext)
			return err
		}
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "Providah", AccountName: i.Email})
		if err != nil {
			return err
		}
		out.Secret, out.Uri = key.Secret(), key.URL()
		cipher, err := s.seal(key.Secret())
		if err != nil {
			return err
		}
		return q.EnrollInvitation(ctx, database.EnrollInvitationParams{ID: i.ID, TotpCiphertext: cipher})
	})
	return connect.NewResponse(out), err
}
func (s *Service) AcceptInvitation(ctx context.Context, req *connect.Request[pb.AcceptInvitationRequest]) (*connect.Response[pb.SessionResponse], error) {
	if err := s.limit(ctx, "invite:"+hex.EncodeToString(hash(req.Msg.Token))); err != nil {
		return nil, err
	}
	uid, email, sid, refresh := "", "", "", ""
	err := s.transaction(ctx, func(q *database.Queries) error {
		i, err := s.invitation(ctx, q, req.Msg.Token)
		if err != nil {
			return err
		}
		email = i.Email
		exists, err := q.UserExists(ctx, email)
		if err != nil {
			return err
		}
		if exists {
			p, err := s.authenticate(ctx, req.Header())
			if err != nil || p.Email != email {
				return unauthenticated()
			}
			if !p.MFADisabled && time.Since(p.MFAAt) > 5*time.Minute {
				return conflict("Verify MFA before accepting this invitation.")
			}
			uid = p.UserID
		} else {
			if len(req.Msg.Password) < 12 || len(req.Msg.Password) > 72 {
				return invalid("Use a password between 12 and 72 bytes.")
			}
			seed, err := s.open(i.TotpCiphertext)
			if err != nil || !checkTOTP(req.Msg.Code, seed) {
				return invalid("Begin enrollment and enter a current authenticator code.")
			}
			password, err := bcrypt.GenerateFromPassword([]byte(req.Msg.Password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			uid, sid, refresh = randomID(), randomID(), randomID()
			if err = q.CreateUser(ctx, database.CreateUserParams{ID: uid, Email: email, PasswordHash: password, TotpCiphertext: i.TotpCiphertext, LastTotpStep: time.Now().Unix() / 30}); err != nil {
				return err
			}
			if err = q.CreateSession(ctx, database.CreateSessionParams{ID: sid, UserID: uid, RefreshHash: hash(refresh)}); err != nil {
				return err
			}
		}
		member, err := q.HasMembership(ctx, database.HasMembershipParams{OrgID: i.OrgID, UserID: uid})
		if err != nil {
			return err
		}
		if member {
			return conflict("Membership already exists. Ask an administrator to update it.")
		}
		if err = q.JoinMembership(ctx, database.JoinMembershipParams{OrgID: i.OrgID, UserID: uid, RoleID: pgtype.Text{String: i.RoleID, Valid: true}}); err != nil {
			return err
		}
		n, err := q.AcceptInvitation(ctx, i.ID)
		if err != nil {
			return err
		}
		if n != 1 {
			return conflict("Invitation is no longer available.")
		}
		return audit(ctx, q, i.OrgID, email, "invitation.accepted", i.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	identity := pgtype.UUID{}
	if p, e := s.authenticate(ctx, req.Header()); e == nil && p.UserID == uid {
		identity = p.OIDC
	}
	out, err := s.sessionResponse(ctx, uid, email, identity)
	if err == nil && sid != "" {
		err = s.issue(out.Header(), sid, refresh)
	}
	return out, err
}
func (s *Service) VerifyMfa(ctx context.Context, req *connect.Request[pb.VerifyMfaRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	p := actor(ctx)
	if err := s.limit(ctx, "mfa:"+p.UserID); err != nil {
		return nil, err
	}
	u, err := s.q.UserByEmail(ctx, p.Email)
	if err != nil {
		return nil, unauthenticated()
	}
	seed, err := s.open(u.TotpCiphertext)
	if err != nil {
		return nil, err
	}
	if !checkTOTP(req.Msg.Code, seed) {
		return nil, invalid("Authenticator code is invalid or expired.")
	}
	err = s.transaction(ctx, func(q *database.Queries) error {
		n, err := q.ConsumeTOTP(ctx, database.ConsumeTOTPParams{ID: p.UserID, LastTotpStep: time.Now().Unix() / 30})
		if err != nil {
			return err
		}
		if n != 1 {
			return invalid("Authenticator code was already used. Wait for the next code.")
		}
		if err = q.VerifySessionMFA(ctx, p.ID); err != nil {
			return err
		}
		orgs, err := q.OrganizationsForUser(ctx, p.UserID)
		if err != nil {
			return err
		}
		for _, org := range orgs {
			if err = audit(ctx, q, org.ID, p.Email, "session.mfa_verified", p.ID, nil); err != nil {
				return err
			}
		}
		return nil
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
