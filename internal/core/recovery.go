package core

import (
	"context"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"golang.org/x/crypto/bcrypt"
)

func recoveryVerifier(user, code string) []byte {
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if len(code) != 32 {
		return nil
	}
	if _, err := hex.DecodeString(code); err != nil {
		return nil
	}
	return hash("providah-recovery-v1:" + user + ":" + code)
}

func accountAudit(ctx context.Context, q *database.Queries, user, email, action string) error {
	if err := q.AddAccountEvent(ctx, database.AddAccountEventParams{UserID: user, Action: action}); err != nil {
		return err
	}
	orgs, err := q.OrganizationsForUser(ctx, user)
	if err != nil {
		return err
	}
	slices.SortFunc(orgs, func(a, b database.OrganizationsForUserRow) int { return strings.Compare(a.ID, b.ID) })
	for _, org := range orgs {
		if err = audit(ctx, q, org.ID, email, action, user, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetAccountSecurity(ctx context.Context, _ *connect.Request[pb.GetAccountSecurityRequest]) (*connect.Response[pb.GetAccountSecurityResponse], error) {
	user := actor(ctx).UserID
	count, err := s.q.RecoveryCodeCount(ctx, user)
	if err != nil {
		return nil, err
	}
	events, err := s.q.ListAccountEvents(ctx, user)
	if err != nil {
		return nil, err
	}
	out := &pb.GetAccountSecurityResponse{RemainingCodes: count}
	for _, event := range events {
		out.Events = append(out.Events, &pb.AccountEvent{Id: strconv.FormatInt(event.ID, 10), Action: event.Action, OccurredAt: stamp(event.OccurredAt)})
	}
	return connect.NewResponse(out), nil
}

func (s *Service) GenerateRecoveryCodes(ctx context.Context, req *connect.Request[pb.GenerateRecoveryCodesRequest]) (*connect.Response[pb.GenerateRecoveryCodesResponse], error) {
	p := actor(ctx)
	codes := make([]string, 10)
	for i := range codes {
		raw := randomID()[:32]
		codes[i] = raw[:8] + "-" + raw[8:16] + "-" + raw[16:24] + "-" + raw[24:]
	}
	err := s.accountChange(ctx, req.Msg.Password, req.Msg.Code, func(q *database.Queries) error {
		if err := q.ClearRecoveryCodes(ctx, p.UserID); err != nil {
			return err
		}
		for _, code := range codes {
			if err := q.AddRecoveryCode(ctx, database.AddRecoveryCodeParams{UserID: p.UserID, Verifier: recoveryVerifier(p.UserID, code)}); err != nil {
				return err
			}
		}
		if err := func() error {
			if p.MFADisabled {
				return nil
			}
			return q.VerifySessionMFA(ctx, p.ID)
		}(); err != nil {
			return err
		}
		return accountAudit(ctx, q, p.UserID, p.Email, "account.recovery_codes_replaced")
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GenerateRecoveryCodesResponse{Codes: codes}), nil
}

// accountChange shares fresh password/TOTP verification and the user lock across sensitive account changes.
func (s *Service) accountChange(ctx context.Context, password, code string, apply func(*database.Queries) error) error {
	p := actor(ctx)
	if err := s.limit(ctx, "mfa:"+p.UserID); err != nil {
		return err
	}
	if len(password) > 72 || len(code) > 6 {
		return invalid("Enter your password and a fresh authenticator code.")
	}
	// Validate the expensive password hash before taking the user lock, then fence
	// against credential changes under that lock before consuming a fresh TOTP.
	user, err := s.q.UserByEmail(ctx, p.Email)
	if err != nil {
		return unauthenticated()
	}
	if bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)) != nil {
		return unauthenticated()
	}
	return s.transaction(ctx, func(q *database.Queries) error {
		locked, err := q.LockRecoveryUser(ctx, p.UserID)
		if err != nil {
			return unauthenticated()
		}
		if !slices.Equal(user.PasswordHash, locked.PasswordHash) {
			return unauthenticated()
		}
		// The session can have been revoked while the password check ran.
		if _, err = q.GetSession(ctx, p.ID); err != nil {
			return unauthenticated()
		}
		if !locked.MfaEnabled {
			return apply(q)
		}
		seed, err := s.open(locked.TotpCiphertext)
		if err != nil {
			return err
		}
		if !checkTOTP(code, seed) {
			return invalid("Authenticator code is invalid or expired.")
		}
		n, err := q.ConsumeTOTP(ctx, database.ConsumeTOTPParams{ID: p.UserID, LastTotpStep: time.Now().Unix() / 30})
		if err != nil {
			return err
		}
		if n != 1 {
			return invalid("Authenticator code was already used. Wait for the next code.")
		}
		return apply(q)
	})
}

func (s *Service) ChangePassword(ctx context.Context, req *connect.Request[pb.ChangePasswordRequest]) (*connect.Response[pb.ChangePasswordResponse], error) {
	if len(req.Msg.NewPassword) < 12 || len(req.Msg.NewPassword) > 72 || req.Msg.NewPassword == req.Msg.Password {
		return nil, invalid("Use a different password between 12 and 72 bytes.")
	}
	p := actor(ctx)
	err := s.accountChange(ctx, req.Msg.Password, req.Msg.Code, func(q *database.Queries) error {
		password, err := bcrypt.GenerateFromPassword([]byte(req.Msg.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if err = q.ChangePassword(ctx, database.ChangePasswordParams{ID: p.UserID, PasswordHash: password}); err != nil {
			return err
		}
		if err = q.DeleteUserSessions(ctx, p.UserID); err != nil {
			return err
		}
		return accountAudit(ctx, q, p.UserID, p.Email, "account.password_changed")
	})
	if err != nil {
		return nil, err
	}
	r := connect.NewResponse(&pb.ChangePasswordResponse{})
	s.setCookie(r.Header(), "providah_access", "", -1)
	s.setCookie(r.Header(), "providah_refresh", "", -1)
	return r, nil
}
