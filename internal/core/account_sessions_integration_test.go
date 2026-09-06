//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exerciseAccountSessions(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	password, seed := "sessions-fixture-password", "JBSWY3DPEHPK3PXP"
	cipher, e := s.seal(seed)
	check(e)
	hashed, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	check(e)
	user, foreign := randomID(), randomID()
	for _, id := range []string{user, foreign} {
		_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext) VALUES($1,$2,$3,$4)", id, id+"@example.test", hashed, cipher)
		check(e)
	}
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	client := func(uid string) (providahv1connect.ConsoleServiceClient, string) {
		session, refresh := randomID(), randomID()
		_, e = s.pool.Exec(ctx, "INSERT INTO sessions(id,user_id,refresh_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", session, uid, hash(refresh))
		check(e)
		h := http.Header{}
		check(s.issue(h, session, refresh))
		cookies := []string{}
		for _, cookie := range (&http.Response{Header: h}).Cookies() {
			cookies = append(cookies, cookie.Name+"="+cookie.Value)
		}
		return providahv1connect.NewConsoleServiceClient(server.Client(), server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Origin", s.cfg.Origin)
				r.Header().Set("Cookie", strings.Join(cookies, "; "))
				return next(ctx, r)
			}
		}))), session
	}
	a, current := client(user)
	b, target := client(user)
	_, other := client(foreign)
	_, e = s.pool.Exec(ctx, "INSERT INTO sessions(id,user_id,refresh_hash,expires_at) SELECT md5($1||n::text)||md5(n::text||$1),$1,decode(md5(n::text),'hex'),now()+interval '1 hour' FROM generate_series(1,101)n", user)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO sessions(id,user_id,refresh_hash,expires_at) VALUES($1,$2,$3,now()-interval '1 hour')", randomID(), user, hash(randomID()))
	check(e)
	seen := map[string]bool{}
	page := ""
	first := ""
	currentCount := 0
	for {
		res, e := a.ListAccountSessions(ctx, connect.NewRequest(&pb.ListAccountSessionsRequest{PageToken: page}))
		check(e)
		for _, session := range res.Msg.Sessions {
			if session.Id == other || seen[session.Id] {
				t.Fatal("session ownership/pagination failed")
			}
			seen[session.Id] = true
			if session.Current {
				currentCount++
				if session.Id != current {
					t.Fatal("wrong current session")
				}
			}
		}
		page = res.Msg.NextPageToken
		if first == "" {
			first = page
		}
		if page == "" {
			break
		}
	}
	if len(seen) != 103 || currentCount != 1 || !seen[target] {
		t.Fatal("session listing missing active sessions or included expired rows")
	}
	foreignClient, _ := client(foreign)
	_, e = foreignClient.ListAccountSessions(ctx, connect.NewRequest(&pb.ListAccountSessionsRequest{PageToken: first}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("cross-user cursor accepted", e)
	}
	code := func() string {
		_, e = s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE id=$1", user)
		check(e)
		value, e := integrationTOTP(seed)
		check(e)
		return value
	}
	revoke := func(id, pass, proof string) error {
		_, e := a.RevokeAccountSession(ctx, connect.NewRequest(&pb.RevokeAccountSessionRequest{Id: id, Password: pass, Code: proof}))
		return e
	}
	if connect.CodeOf(revoke(target, "wrong-password", code())) != connect.CodeUnauthenticated {
		t.Fatal("password not required")
	}
	for _, id := range []string{other, randomID()} {
		if connect.CodeOf(revoke(id, password, code())) != connect.CodeFailedPrecondition {
			t.Fatal("foreign/missing session not uniformly unavailable")
		}
	}
	_, e = s.pool.Exec(ctx, "ALTER TABLE account_events ADD CONSTRAINT reject_session_revoke CHECK(action<>'account.session_revoked') NOT VALID")
	check(e)
	proof := code()
	if revoke(target, password, proof) == nil {
		t.Fatal("revocation committed without audit")
	}
	_, e = b.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	check(e)
	_, e = s.pool.Exec(ctx, "ALTER TABLE account_events DROP CONSTRAINT reject_session_revoke")
	check(e)
	check(revoke(target, password, proof)) // Failed transaction did not consume the code.
	_, e = b.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("revoked session remained usable")
	}
	if connect.CodeOf(revoke(current, password, proof)) != connect.CodeInvalidArgument {
		t.Fatal("authenticator replay accepted")
	}
	res, e := a.RevokeAccountSession(ctx, connect.NewRequest(&pb.RevokeAccountSessionRequest{Id: current, Password: password, Code: code()}))
	check(e)
	if !res.Msg.SignedOut || !strings.Contains(res.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("current session did not clear browser cookies")
	}
	_, e = a.ListAccountSessions(ctx, connect.NewRequest(&pb.ListAccountSessionsRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("current session survived revocation")
	}
	var count int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM account_events WHERE user_id=$1 AND action='account.session_revoked'", user).Scan(&count))
	if count != 2 {
		t.Fatal("incorrect account audit count")
	}
}
