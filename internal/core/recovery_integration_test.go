//go:build integration

package core

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

func exerciseRecovery(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, ctx context.Context, org string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// The preceding invitation flows share loopback's authentication budget.
	_, resetErr := s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key LIKE 'auth-peer:%'")
	check(resetErr)
	user, err := s.q.UserByEmail(ctx, "owner@example.com")
	check(err)
	seed, err := s.open(user.TotpCiphertext)
	check(err)
	fresh := func() string {
		t.Helper()
		_, e := s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE id=$1", user.ID)
		check(e)
		// Leave room for password verification before the current TOTP step expires.
		if remaining := time.Until(time.Now().Truncate(30 * time.Second).Add(30 * time.Second)); remaining < time.Second {
			time.Sleep(remaining)
		}
		code, e := integrationTOTP(seed)
		check(e)
		return code
	}
	lastCode := ""
	generate := func(c providahv1connect.ConsoleServiceClient) []string {
		t.Helper()
		lastCode = fresh()
		out, e := c.GenerateRecoveryCodes(ctx, connect.NewRequest(&pb.GenerateRecoveryCodesRequest{Password: "test-password-long", Code: lastCode}))
		check(e)
		return out.Msg.Codes
	}
	codes := generate(owner)
	if len(codes) != 10 {
		t.Fatal("expected ten codes")
	}
	var count int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM recovery_codes WHERE user_id=$1 AND octet_length(verifier)=32", user.ID).Scan(&count))
	if count != 10 {
		t.Fatal("verifiers not persisted")
	}
	status, err := owner.GetAccountSecurity(ctx, connect.NewRequest(&pb.GetAccountSecurityRequest{}))
	check(err)
	if status.Msg.RemainingCodes != 10 || len(status.Msg.Events) != 1 {
		t.Fatal("account status missing")
	}
	if strings.Contains(status.Msg.String(), codes[0]) {
		t.Fatal("plaintext returned by read API")
	}
	// Regeneration requires a new TOTP, even with a recently verified session.
	used := lastCode
	_, err = owner.GenerateRecoveryCodes(ctx, connect.NewRequest(&pb.GenerateRecoveryCodesRequest{Password: "test-password-long", Code: used}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("TOTP replay permitted", err)
	}
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	newClient := func() providahv1connect.ConsoleServiceClient {
		jar, _ := cookiejar.New(nil)
		return providahv1connect.NewConsoleServiceClient(&http.Client{Jar: jar}, server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Origin", s.cfg.Origin)
				return next(ctx, r)
			}
		})))
	}
	recovered := newClient()
	login := func(c providahv1connect.ConsoleServiceClient, code, password string) error {
		_, e := c.Login(ctx, connect.NewRequest(&pb.LoginRequest{Email: user.Email, Password: password, RecoveryCode: code}))
		return e
	}
	if connect.CodeOf(login(recovered, codes[0], "wrong-password-long")) != connect.CodeUnauthenticated {
		t.Fatal("password not required")
	}
	check(login(recovered, codes[0], "test-password-long"))
	if _, err = owner.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("older session survived recovery")
	}
	if e := login(newClient(), codes[0], "test-password-long"); connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("recovery replay accepted", e)
	}
	_, err = recovered.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "must not create", Provider: "hetzner", Credential: "test-credential"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("recovery session silently granted recent MFA", err)
	}
	_, err = recovered.Refresh(ctx, connect.NewRequest(&pb.RefreshRequest{}))
	check(err)
	_, err = recovered.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "must not create", Provider: "hetzner", Credential: "test-credential"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("refresh elevated recovery session", err)
	}
	// Failed audit persistence rolls back code consumption and session revocation.
	_, err = s.pool.Exec(ctx, "ALTER TABLE account_events ADD CONSTRAINT reject_recovery CHECK(action<>'account.recovery_code_used') NOT VALID")
	check(err)
	if login(newClient(), codes[1], "test-password-long") == nil {
		t.Fatal("login committed without audit")
	}
	_, err = recovered.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	check(err)
	_, err = s.pool.Exec(ctx, "ALTER TABLE account_events DROP CONSTRAINT reject_recovery")
	check(err)
	check(login(recovered, codes[1], "test-password-long"))
	// Concurrent submissions can consume a verifier only once.
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- login(newClient(), codes[2], "test-password-long") }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if connect.CodeOf(e) != connect.CodeUnauthenticated {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent recovery replay", successes)
	}
	// Restore an ordinary login and replace every remaining verifier.
	_, err = owner.Login(ctx, connect.NewRequest(&pb.LoginRequest{Email: user.Email, Password: "test-password-long", Code: fresh()}))
	check(err)
	replacement := generate(owner)
	if replacement[0] == codes[0] {
		t.Fatal("codes reused")
	}
	if connect.CodeOf(login(newClient(), codes[3], "test-password-long")) != connect.CodeUnauthenticated {
		t.Fatal("old generation still accepted")
	}
	if _, err = s.pool.Exec(ctx, "UPDATE account_events SET action='changed' WHERE user_id=$1", user.ID); err == nil {
		t.Fatal("account audit was mutable")
	}
}
