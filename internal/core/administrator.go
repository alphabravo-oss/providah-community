package core

import (
	"context"
	"github.com/pquerna/otp/totp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
)

// SeedAdministrator is available only to the explicitly enabled local maintenance command.
// It never changes passwords or repeats a privilege grant after its first application.
func (s *Service) SeedAdministrator(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.transaction(ctx, func(q *database.Queries) error {
		if e := q.LockSetup(ctx); e != nil {
			return e
		}
		u, e := q.UserByEmail(ctx, email)
		if e != nil {
			return e
		}
		n, e := q.SeedGlobalAdmin(ctx, email)
		if e != nil || n == 0 {
			return e
		}
		if e = q.DeleteUserSessions(ctx, u.ID); e != nil {
			return e
		}
		return accountAudit(ctx, q, u.ID, u.Email, "account.global_admin_seeded")
	})
}

func (s *Service) SetAccountMFA(ctx context.Context, r *connect.Request[pb.SetAccountMFARequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	p := actor(ctx)
	e := s.accountChange(ctx, r.Msg.Password, r.Msg.Code, func(q *database.Queries) error {
		u, e := q.LockRecoveryUser(ctx, p.UserID)
		if e != nil {
			return e
		}
		if !r.Msg.Enabled {
			required, e := q.MFARequiredForUser(ctx, p.UserID)
			if e != nil {
				return e
			}
			if required {
				return conflict("An administrator requires MFA for this account's organization access.")
			}
		}
		if u.MfaEnabled == r.Msg.Enabled {
			return nil
		}
		if r.Msg.Enabled {
			seed, e := s.open(u.TotpCiphertext)
			if e != nil {
				return e
			}
			if !checkTOTP(r.Msg.Code, seed) {
				return invalid("Enter a current code from your enrolled authenticator.")
			}
			n, e := q.ConsumeTOTP(ctx, database.ConsumeTOTPParams{ID: p.UserID, LastTotpStep: time.Now().Unix() / 30})
			if e != nil {
				return e
			}
			if n != 1 {
				return invalid("Wait for a new authenticator code.")
			}
		}
		if e = q.SetAccountMFA(ctx, database.SetAccountMFAParams{ID: p.UserID, MfaEnabled: r.Msg.Enabled}); e != nil {
			return e
		}
		if e = q.DeleteUserSessions(ctx, p.UserID); e != nil {
			return e
		}
		action := "account.mfa_disabled"
		if r.Msg.Enabled {
			action = "account.mfa_enabled"
		}
		return accountAudit(ctx, q, p.UserID, p.Email, action)
	})
	if e != nil {
		return nil, e
	}
	out := connect.NewResponse(&pb.AccessMutationResponse{})
	s.setCookie(out.Header(), "providah_access", "", -1)
	s.setCookie(out.Header(), "providah_refresh", "", -1)
	return out, nil
}

// CreateOrganization grants no new account authority: only existing global
// administrators can create one, and ordinary membership is invited separately.
func (s *Service) CreateOrganization(ctx context.Context, r *connect.Request[pb.CreateOrganizationRequest]) (*connect.Response[pb.Organization], error) {
	name := strings.TrimSpace(r.Msg.Name)
	if name == "" || len(name) > 120 || strings.ContainsAny(name, "\r\n\t") {
		return nil, invalid("Enter an organization name of 1–120 bytes.")
	}
	p := actor(ctx)
	id := randomID()
	e := s.accountChange(ctx, r.Msg.Password, r.Msg.Code, func(q *database.Queries) error {
		u, e := q.LockRecoveryUser(ctx, p.UserID)
		if e != nil {
			return e
		}
		if !u.GlobalAdmin {
			return denied()
		}
		if e = q.CreateOrganization(ctx, database.CreateOrganizationParams{ID: id, Name: name}); e != nil {
			return e
		}
		if e = q.CreateBuiltinRoles(ctx, id); e != nil {
			return e
		}
		return audit(ctx, q, id, p.Email, "organization.created", id, map[string]any{"name": name})
	})
	if e != nil {
		return nil, e
	}
	permissions, e := s.q.Permissions(ctx, database.PermissionsParams{OrgID: id, UserID: p.UserID})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.Organization{Id: id, Name: name, Permissions: permissions}), nil
}

func (s *Service) BeginMFAEnrollment(ctx context.Context, r *connect.Request[pb.BeginMFAEnrollmentRequest]) (*connect.Response[pb.BeginSetupResponse], error) {
	var out *pb.BeginSetupResponse
	e := s.accountChange(ctx, r.Msg.Password, "", func(q *database.Queries) error {
		p := actor(ctx)
		u, e := q.LockRecoveryUser(ctx, p.UserID)
		if e != nil {
			return e
		}
		if u.MfaEnabled {
			return conflict("MFA is already enabled.")
		}
		key, e := totp.Generate(totp.GenerateOpts{Issuer: "Providah", AccountName: p.Email})
		if e != nil {
			return e
		}
		cipher, e := s.seal(key.Secret())
		if e != nil {
			return e
		}
		if e = q.SetDisabledMFASeed(ctx, database.SetDisabledMFASeedParams{ID: p.UserID, TotpCiphertext: cipher}); e != nil {
			return e
		}
		if e = accountAudit(ctx, q, p.UserID, p.Email, "account.mfa_enrollment_started"); e != nil {
			return e
		}
		out = &pb.BeginSetupResponse{Secret: key.Secret(), Uri: key.URL()}
		return nil
	})
	return connect.NewResponse(out), e
}
