package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/hex"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"strings"
)

func (s *Service) ListAccountSessions(ctx context.Context, r *connect.Request[pb.ListAccountSessionsRequest]) (*connect.Response[pb.ListAccountSessionsResponse], error) {
	p := actor(ctx)
	if len(r.Msg.PageToken) > 1024 {
		return nil, invalid("Invalid page token.")
	}
	before, e := parseCursor(r.Msg.PageToken, p.UserID, "account-sessions")
	if e != nil {
		return nil, e
	}
	if before != "" {
		if decoded, e := hex.DecodeString(before); e != nil || len(decoded) != 32 {
			return nil, invalid("Invalid page token.")
		}
	}
	if before == "" {
		before = strings.Repeat("f", 65)
	}
	rows, e := s.q.ListAccountSessions(ctx, database.ListAccountSessionsParams{UserID: p.UserID, ID: before})
	if e != nil {
		return nil, e
	}
	result := &pb.ListAccountSessionsResponse{}
	if len(rows) > 100 {
		rows = rows[:100]
		result.NextPageToken = cursor(p.UserID, "account-sessions", rows[99].ID)
	}
	for _, row := range rows {
		mfa := ""
		if row.MfaAt.Valid && row.MfaAt.Time.Unix() > 0 {
			mfa = stamp(row.MfaAt)
		}
		result.Sessions = append(result.Sessions, &pb.AccountSession{Id: row.ID, ExpiresAt: stamp(row.ExpiresAt), MfaAt: mfa, Current: row.ID == p.ID, Oidc: row.OidcID.Valid})
	}
	return connect.NewResponse(result), nil
}
func (s *Service) RevokeAccountSession(ctx context.Context, r *connect.Request[pb.RevokeAccountSessionRequest]) (*connect.Response[pb.RevokeAccountSessionResponse], error) {
	decoded, decodeErr := hex.DecodeString(r.Msg.Id)
	if decodeErr != nil || len(decoded) != 32 {
		return nil, invalid("Choose a session from your account.")
	}
	p := actor(ctx)
	e := s.accountChange(ctx, r.Msg.Password, r.Msg.Code, func(q *database.Queries) error {
		count, e := q.DeleteAccountSession(ctx, database.DeleteAccountSessionParams{UserID: p.UserID, ID: r.Msg.Id})
		if e != nil {
			return e
		}
		if count != 1 {
			return conflict("Session is unavailable. Reload your sessions.")
		}
		return accountAudit(ctx, q, p.UserID, p.Email, "account.session_revoked")
	})
	if e != nil {
		return nil, e
	}
	result := connect.NewResponse(&pb.RevokeAccountSessionResponse{SignedOut: r.Msg.Id == p.ID})
	if result.Msg.SignedOut {
		s.setCookie(result.Header(), "providah_access", "", -1)
		s.setCookie(result.Header(), "providah_refresh", "", -1)
	}
	return result, nil
}
