//go:build integration

package core

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

func exerciseOIDC(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, client *http.Client, base string, ctx context.Context, secret string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	old := s.oidc
	defer func() { s.oidc = old }()
	_, e0 := s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key LIKE 'mfa:%'")
	check(e0)
	var fixture *fakeIdentity
	s.oidc, fixture = oidcFixture(t, s.cfg)
	callbackClient := *client
	callbackClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	fresh := func() string {
		// Leave enough validity for password hashing and the authenticated request.
		until := time.Until(time.Now().Truncate(30 * time.Second).Add(30 * time.Second))
		if until < time.Second {
			time.Sleep(until + 10*time.Millisecond)
		}
		_, e := s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE email='owner@example.com'")
		check(e)
		value, e := integrationTOTP(secret)
		check(e)
		return value
	}
	clearRate := func() {
		_, e := s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key LIKE 'auth-peer:%' OR key LIKE 'login:%'")
		check(e)
	}
	callback := func(state string) string {
		response, e := callbackClient.Get(base + "/api/oidc/callback?state=" + url.QueryEscape(state) + "&code=test-code")
		check(e)
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusSeeOther || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("unsafe callback response %d", response.StatusCode)
		}
		location := response.Header.Get("Location")
		if !strings.HasPrefix(location, s.cfg.Origin+"/?oidc=") {
			t.Fatal("unbound callback destination")
		}
		return strings.TrimPrefix(location, s.cfg.Origin+"/?oidc=")
	}
	returnTo := "/app/resources?org=deep-link-org&view=saved#details"
	begin := func() string {
		clearRate()
		r, e := owner.BeginOIDCLogin(ctx, connect.NewRequest(&pb.BeginOIDCLoginRequest{ReturnTo: returnTo}))
		check(e)
		return fixture.begin(t, r.Msg.Url)
	}
	// An email match and administrator group claim must never create an identity link or membership.
	state := begin()
	if callback(state) != "failed" {
		t.Fatal("email auto-link accepted")
	}
	var links int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM oidc_links").Scan(&links))
	if links != 0 {
		t.Fatal("unverified auto-link stored")
	}
	clearRate()
	linked, e := owner.BeginOIDCLink(ctx, connect.NewRequest(&pb.OIDCAccountRequest{Password: "test-password-long", Code: fresh()}))
	check(e)
	state = fixture.begin(t, linked.Msg.Url)
	if callback(state) != "linked" {
		t.Fatal("explicit identity linking failed")
	}
	status, e := owner.GetOIDCStatus(ctx, connect.NewRequest(&pb.GetOIDCStatusRequest{}))
	check(e)
	if !status.Msg.Linked {
		t.Fatal("linked identity missing")
	}
	before := fixture.exchanges
	if callback(state) != "failed" || fixture.exchanges != before {
		t.Fatal("callback replay reached token exchange")
	}
	for _, failure := range []string{"nonce", "issuer", "audience", "expired", "azp", "multiple-audience", "old-issuance", "signature"} {
		fixture.invalid = failure
		state = begin()
		if callback(state) != "failed" {
			t.Fatalf("invalid %s accepted", failure)
		}
	}
	fixture.invalid = ""
	begin()
	before = fixture.exchanges
	if callback(strings.Repeat("0", 64)) != "failed" || fixture.exchanges != before {
		t.Fatal("wrong state reached IdP")
	}
	state = begin()
	_, e = s.pool.Exec(ctx, "UPDATE oidc_flows SET expires_at=now()-interval '1 second' WHERE id=$1", hex.EncodeToString(hash(state)))
	check(e)
	before = fixture.exchanges
	if callback(state) != "failed" || fixture.exchanges != before {
		t.Fatal("expired state reached IdP")
	}
	state = begin()
	// A different browser cannot claim this flow even if it knows the state.
	request, _ := http.NewRequest("GET", base+"/api/oidc/callback?state="+state+"&code=test-code", nil)
	outsider := &http.Client{CheckRedirect: callbackClient.CheckRedirect}
	response, e := outsider.Do(request)
	check(e)
	_ = response.Body.Close()
	if !strings.HasSuffix(response.Header.Get("Location"), "failed") {
		t.Fatal("missing browser binding accepted")
	}
	var sessionsBefore int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&sessionsBefore))
	if callback(state) != "verify" {
		t.Fatal("valid OIDC proof failed")
	}

	challenge, e := owner.GetOIDCStatus(ctx, connect.NewRequest(&pb.GetOIDCStatusRequest{}))
	check(e)
	if !challenge.Msg.LoginVerified || !challenge.Msg.LoginMfaEnabled || challenge.Msg.LoginReturnTo != returnTo {
		t.Fatal("verified login challenge lost MFA or return destination")
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET mfa_enabled=false WHERE email='owner@example.com'")
	check(e)
	challenge, e = owner.GetOIDCStatus(ctx, connect.NewRequest(&pb.GetOIDCStatusRequest{}))
	check(e)
	if !challenge.Msg.LoginVerified || challenge.Msg.LoginMfaEnabled {
		t.Fatal("optional MFA challenge incorrect")
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET mfa_enabled=true WHERE email='owner@example.com'")
	check(e)
	var sessionsAfter int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&sessionsAfter))
	if sessionsBefore != sessionsAfter {
		t.Fatal("OIDC proof bypassed local MFA")
	}
	_, e = owner.CompleteOIDCLogin(ctx, connect.NewRequest(&pb.CompleteOIDCLoginRequest{Code: "xxxxxx"}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("invalid TOTP accepted")
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET active=false WHERE email='owner@example.com'")
	check(e)
	_, e = owner.CompleteOIDCLogin(ctx, connect.NewRequest(&pb.CompleteOIDCLoginRequest{Code: fresh()}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("disabled user completed OIDC")
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET active=true WHERE email='owner@example.com'")
	check(e)
	signed, e := owner.CompleteOIDCLogin(ctx, connect.NewRequest(&pb.CompleteOIDCLoginRequest{Code: fresh()}))
	check(e)
	if signed.Msg.ReturnTo != returnTo || signed.Msg.Email != "owner@example.com" || len(signed.Msg.Organizations) != 1 {
		t.Fatal("OIDC changed identity or memberships")
	}
	_, e = owner.CompleteOIDCLogin(ctx, connect.NewRequest(&pb.CompleteOIDCLoginRequest{Code: fresh()}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("verified proof reused")
	}
	exerciseIdentityPolicy(t, s, owner, base, ctx)
	// Unlinking removes authority for a previously verified login and revokes sessions.
	state = begin()
	if callback(state) != "verify" {
		t.Fatal("pending login failed")
	}
	cookieURL, _ := url.Parse(base + "/api/")
	var savedFlow *http.Cookie
	for _, c := range client.Jar.Cookies(cookieURL) {
		if c.Name == "providah_oidc" {
			savedFlow = c
		}
	}
	if savedFlow == nil {
		t.Fatal("no verified browser binding")
	}
	_, e = owner.UnlinkOIDC(ctx, connect.NewRequest(&pb.OIDCAccountRequest{Password: "test-password-long", Code: fresh()}))
	check(e)
	_, e = owner.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("unlink kept session")
	}
	client.Jar.SetCookies(cookieURL, []*http.Cookie{savedFlow})
	_, e = owner.CompleteOIDCLogin(ctx, connect.NewRequest(&pb.CompleteOIDCLoginRequest{Code: fresh()}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("unlinked identity login was not rejected correctly", e)
	}
	clearRate()
	_, e = owner.Login(ctx, connect.NewRequest(&pb.LoginRequest{Email: "owner@example.com", Password: "test-password-long", Code: fresh()}))
	check(e)
	clearRate()
	pending, e := owner.BeginOIDCLink(ctx, connect.NewRequest(&pb.OIDCAccountRequest{Password: "test-password-long", Code: fresh()}))
	check(e)
	state = fixture.begin(t, pending.Msg.Url)
	_, e = s.pool.Exec(ctx, "DELETE FROM sessions WHERE id=(SELECT session_id FROM oidc_flows WHERE id=$1)", hex.EncodeToString(hash(state)))
	check(e)
	if callback(state) != "failed" {
		t.Fatal("revoked source session linked an identity")
	}
	clearRate()
	_, e = owner.Login(ctx, connect.NewRequest(&pb.LoginRequest{Email: "owner@example.com", Password: "test-password-long", Code: fresh()}))
	check(e)

}
