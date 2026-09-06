package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"
)

type OIDCConfig struct{ Issuer, ClientID, ClientSecret string }
type oidcClient struct {
	mu       sync.Mutex
	config   OIDCConfig
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	http     *http.Client
	key      string
}

type boundedResponseTransport struct{ http.RoundTripper }

func (t boundedResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	_ = response.Body.Close()
	if err != nil || len(body) > 1<<20 {
		return nil, errors.New("HTTP response exceeded its bound or could not be read")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
func newOIDCClient(cfg Config) (*oidcClient, error) {
	c := *cfg.OIDC
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && (cfg.SecureCookies || u.Scheme != "http")) || notification.ValidateHTTPS(c.Issuer) != nil || len(c.Issuer) > 2048 || c.ClientID == "" || len(c.ClientID) > 512 || len(c.ClientSecret) < 8 || len(c.ClientSecret) > 16384 {
		return nil, errors.New("invalid OIDC issuer, client, or callback configuration")
	}
	client := cfg.OIDCHTTPClient
	if client == nil {
		client, err = notification.PublicHTTPSClient(cfg.Origin)
		if err != nil {
			return nil, err
		}
	}
	bounded := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	bounded.Transport = boundedResponseTransport{transport}
	bounded.Timeout = 15 * time.Second
	client = &bounded
	return &oidcClient{config: c, oauth: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: cfg.Origin + "/api/oidc/callback", Scopes: []string{oidc.ScopeOpenID}}, http: client, key: hex.EncodeToString(hash(c.Issuer + "\n" + c.ClientID))}, nil
}

// Discover on demand so an identity-provider outage cannot prevent local sign-in or application startup.
func (c *oidcClient) initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.verifier != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, c.http), 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, c.config.Issuer)
	if err != nil {
		return errors.New("OIDC discovery failed")
	}
	endpoint := p.Endpoint()
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if p.Claims(&metadata) != nil || notification.ValidateHTTPS(endpoint.AuthURL) != nil || notification.ValidateHTTPS(endpoint.TokenURL) != nil || notification.ValidateHTTPS(metadata.JWKS) != nil {
		return errors.New("OIDC endpoints must use HTTPS")
	}
	c.oauth.Endpoint = endpoint
	c.verifier = p.Verifier(&oidc.Config{ClientID: c.config.ClientID, SupportedSigningAlgs: []string{"RS256", "ES256", "PS256"}})
	return nil
}

// Domain-separated HMAC keeps short-lived PKCE material out of storage. A session-key change invalidates outstanding flows.
func (s *Service) oidcProof(purpose, state string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.SessionKey))
	mac.Write([]byte("providah:oidc:v1:" + purpose + ":" + state))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) oidcCookie(h http.Header, value string, age int) {
	h.Add("Set-Cookie", (&http.Cookie{Name: "providah_oidc", Value: value, Path: "/api/", MaxAge: age, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode}).String())
}
func oidcBrowser(h http.Header) (string, string, error) {
	parts := strings.Split(cookie(h, "providah_oidc"), ".")
	if len(parts) != 2 || len(parts[0]) != 64 || len(parts[1]) != 64 {
		return "", "", unauthenticated()
	}
	for _, p := range parts {
		if _, err := hex.DecodeString(p); err != nil {
			return "", "", unauthenticated()
		}
	}
	return parts[0], parts[1], nil
}
func oidcReturnTo(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if len(raw) > 2048 || strings.Contains(raw, "\\") || strings.ContainsFunc(raw, unicode.IsControl) {
		return "", invalid("Choose a local console return path.")
	}
	u, e := url.Parse(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" || u.RawPath != "" || strings.Contains(u.Path, "\\") || strings.ContainsFunc(u.Path, unicode.IsControl) || path.Clean(u.Path) != u.Path || (u.Path != "/" && u.Path != "/app" && u.Path != "/admin" && !strings.HasPrefix(u.Path, "/app/") && !strings.HasPrefix(u.Path, "/admin/")) {
		return "", invalid("Choose a local console return path.")
	}
	q := u.Query()
	q.Del("oidc")
	u.RawQuery = q.Encode()
	result := u.String()
	if len(result) > 2048 {
		return "", invalid("Choose a shorter console return path.")
	}
	return result, nil
}
func (s *Service) GetOIDCStatus(ctx context.Context, req *connect.Request[pb.GetOIDCStatusRequest]) (*connect.Response[pb.GetOIDCStatusResponse], error) {
	out := &pb.GetOIDCStatusResponse{}
	if s.oidc != nil {
		out.Enabled = true
		out.Issuer = s.oidc.config.Issuer
		if state, browser, e := oidcBrowser(req.Header()); e == nil {
			if flow, e := s.q.GetVerifiedOIDCFlow(ctx, database.GetVerifiedOIDCFlowParams{ID: hex.EncodeToString(hash(state)), CookieHash: hash(browser), ProviderKey: s.oidc.key}); e == nil {
				if user, e := s.q.OIDCLinkedUser(ctx, database.OIDCLinkedUserParams{Issuer: out.Issuer, Subject: flow.Subject}); e == nil && user.ID == flow.UserID.String {
					out.LoginVerified = true
					out.LoginMfaEnabled = user.MfaEnabled
					out.LoginReturnTo = flow.ReturnTo
				}
			}
		}

		if p, e := s.authenticate(ctx, req.Header()); e == nil {
			_, e = s.q.UserOIDCLink(ctx, database.UserOIDCLinkParams{UserID: p.UserID, Issuer: out.Issuer})
			out.Linked = e == nil
		}
	}
	return connect.NewResponse(out), nil
}
func (s *Service) beginOIDC(ctx context.Context, mode, password, code, returnTo string) (*connect.Response[pb.OIDCRedirectResponse], error) {
	destination, e := oidcReturnTo(returnTo)
	if e != nil {
		return nil, e
	}
	if s.oidc == nil {
		return nil, conflict("OIDC sign-in is not configured.")
	}
	if err := s.oidc.initialize(ctx); err != nil {
		return nil, conflict("Organization sign-in is temporarily unavailable. Local sign-in remains available.")
	}
	state, browser := randomID(), randomID()
	create := func(q *database.Queries) error {
		if err := q.ClearExpiredOIDCFlows(ctx); err != nil {
			return err
		}
		p := actor(ctx)
		return q.NewOIDCFlow(ctx, database.NewOIDCFlowParams{ID: hex.EncodeToString(hash(state)), CookieHash: hash(browser), ProviderKey: s.oidc.key, Mode: mode, ReturnTo: destination, UserID: pgtype.Text{String: p.UserID, Valid: mode == "link"}, SessionID: pgtype.Text{String: p.ID, Valid: mode == "link"}})
	}
	var err error
	if mode == "link" {
		err = s.accountChange(ctx, password, code, create)
	} else {
		err = create(s.q)
	}
	if err != nil {
		return nil, err
	}
	out := connect.NewResponse(&pb.OIDCRedirectResponse{Url: s.oidc.oauth.AuthCodeURL(state, oidc.Nonce(s.oidcProof("nonce", state)), oauth2.S256ChallengeOption(s.oidcProof("pkce", state)), oauth2.SetAuthURLParam("prompt", "login"))})
	s.oidcCookie(out.Header(), state+"."+browser, 300)
	return out, nil
}
func (s *Service) BeginOIDCLogin(ctx context.Context, req *connect.Request[pb.BeginOIDCLoginRequest]) (*connect.Response[pb.OIDCRedirectResponse], error) {
	return s.beginOIDC(ctx, "login", "", "", req.Msg.ReturnTo)
}
func (s *Service) BeginOIDCLink(ctx context.Context, req *connect.Request[pb.OIDCAccountRequest]) (*connect.Response[pb.OIDCRedirectResponse], error) {
	return s.beginOIDC(ctx, "link", req.Msg.Password, req.Msg.Code, "/")
}
func (s *Service) oidcCallback(w http.ResponseWriter, r *http.Request) {
	result := "failed"
	if s.oidc != nil {
		if mode, err := s.finishOIDCCallback(r); err == nil {
			result = mode
		}
	}
	if result != "verify" {
		s.oidcCookie(w.Header(), "", -1)
	}
	http.Redirect(w, r, s.cfg.Origin+"/?oidc="+result, http.StatusSeeOther)
}
func (s *Service) finishOIDCCallback(r *http.Request) (string, error) {
	state, browser, err := oidcBrowser(r.Header)
	values := r.URL.Query()
	if err != nil || len(values["state"]) != 1 || !equal(state, values.Get("state")) || len(values["code"]) != 1 || values.Get("code") == "" || len(values.Get("code")) > 4096 || values.Get("error") != "" {
		return "", unauthenticated()
	}
	if err = s.oidc.initialize(r.Context()); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(r.Context(), s.oidc.http), 20*time.Second)
	defer cancel()
	flow, err := s.q.ClaimOIDCFlow(ctx, database.ClaimOIDCFlowParams{ID: hex.EncodeToString(hash(state)), CookieHash: hash(browser), ProviderKey: s.oidc.key})
	if err != nil {
		return "", unauthenticated()
	}
	token, err := s.oidc.oauth.Exchange(ctx, values.Get("code"), oauth2.VerifierOption(s.oidcProof("pkce", state)))
	if err != nil {
		return "", unauthenticated()
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 32768 {
		return "", unauthenticated()
	}
	id, err := s.oidc.verifier.Verify(ctx, raw)
	if err != nil || !equal(id.Nonce, s.oidcProof("nonce", state)) || !time.Now().Before(flow.ExpiresAt.Time) || id.Subject == "" || len(id.Subject) > 512 || id.IssuedAt.Before(flow.CreatedAt.Time.Add(-time.Minute)) || id.IssuedAt.After(time.Now().Add(time.Minute)) {
		return "", unauthenticated()
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if id.Claims(&claims) != nil || (len(id.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != s.oidc.config.ClientID || id.AccessTokenHash != "" && id.VerifyAccessToken(token.AccessToken) != nil {
		return "", unauthenticated()
	}
	if flow.Mode == "login" {
		user, err := s.q.OIDCLinkedUser(ctx, database.OIDCLinkedUserParams{Issuer: id.Issuer, Subject: id.Subject})
		if err != nil {
			return "", unauthenticated()
		}
		err = s.q.VerifyOIDCFlow(ctx, database.VerifyOIDCFlowParams{ID: flow.ID, UserID: pgtype.Text{String: user.ID, Valid: true}, Subject: id.Subject})
		return "verify", err
	}
	err = s.transaction(ctx, func(q *database.Queries) error {
		user, err := q.LockRecoveryUser(ctx, flow.UserID.String)
		if err != nil {
			return unauthenticated()
		}
		session, err := q.GetSession(ctx, flow.SessionID.String)
		if err != nil || session.UserID != user.ID {
			return unauthenticated()
		}
		if err = q.LinkOIDC(ctx, database.LinkOIDCParams{Issuer: id.Issuer, Subject: id.Subject, UserID: user.ID}); err != nil {
			return denied()
		}
		if err = q.DeleteOIDCFlow(ctx, flow.ID); err != nil {
			return err
		}
		return accountAudit(ctx, q, user.ID, user.Email, "account.oidc_linked")
	})
	return "linked", err
}
func (s *Service) CompleteOIDCLogin(ctx context.Context, req *connect.Request[pb.CompleteOIDCLoginRequest]) (*connect.Response[pb.SessionResponse], error) {
	if s.oidc == nil || len(req.Msg.Code) > 6 {
		return nil, unauthenticated()
	}
	state, browser, err := oidcBrowser(req.Header())
	if err != nil {
		return nil, err
	}
	flow, err := s.q.GetVerifiedOIDCFlow(ctx, database.GetVerifiedOIDCFlowParams{ID: hex.EncodeToString(hash(state)), CookieHash: hash(browser), ProviderKey: s.oidc.key})
	if err != nil {
		return nil, unauthenticated()
	}
	if err = s.limit(ctx, "mfa:"+flow.UserID.String); err != nil {
		return nil, err
	}
	sid, refresh := randomID(), randomID()
	identity := pgtype.UUID{}
	email := ""
	err = s.transaction(ctx, func(q *database.Queries) error {
		user, err := q.LockRecoveryUser(ctx, flow.UserID.String)
		if err != nil {
			return unauthenticated()
		}
		current, err := q.LockOIDCFlow(ctx, database.LockOIDCFlowParams{ID: flow.ID, CookieHash: hash(browser), ProviderKey: s.oidc.key})
		if err != nil || current.UserID != flow.UserID {
			return unauthenticated()
		}
		linked, err := q.OIDCLinkedUser(ctx, database.OIDCLinkedUserParams{Issuer: s.oidc.config.Issuer, Subject: current.Subject})
		if err != nil || linked.ID != user.ID {
			return unauthenticated()
		}
		if user.MfaEnabled {
			secret, err := s.open(user.TotpCiphertext)
			if err != nil {
				return err
			}
			if !checkTOTP(req.Msg.Code, secret) {
				return unauthenticated()
			}
			n, err := q.ConsumeTOTP(ctx, database.ConsumeTOTPParams{ID: user.ID, LastTotpStep: time.Now().Unix() / 30})
			if err != nil {
				return err
			}
			if n != 1 {
				return unauthenticated()
			}
		}
		if err = q.CreateSession(ctx, database.CreateSessionParams{ID: sid, UserID: user.ID, RefreshHash: hash(refresh)}); err != nil {
			return err
		}
		link, err := q.UserOIDCLink(ctx, database.UserOIDCLinkParams{UserID: user.ID, Issuer: s.oidc.config.Issuer})
		if err != nil {
			return err
		}
		identity = link.ID
		if !user.MfaEnabled {
			if e := q.RestrictRecoverySession(ctx, sid); e != nil {
				return e
			}
		}
		if err = q.SetSessionIdentity(ctx, database.SetSessionIdentityParams{ID: sid, OidcID: identity}); err != nil {
			return err
		}
		if err = q.DeleteOIDCFlow(ctx, flow.ID); err != nil {
			return err
		}
		email = user.Email
		return accountAudit(ctx, q, user.ID, user.Email, "account.oidc_signed_in")
	})
	if err != nil {
		return nil, err
	}
	out, err := s.sessionResponse(ctx, flow.UserID.String, email, identity)
	if err != nil {
		return nil, err
	}
	out.Msg.ReturnTo = flow.ReturnTo
	s.oidcCookie(out.Header(), "", -1)
	return out, s.issue(out.Header(), sid, refresh)
}
func (s *Service) UnlinkOIDC(ctx context.Context, req *connect.Request[pb.OIDCAccountRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if s.oidc == nil {
		return nil, conflict("OIDC is not configured.")
	}
	p := actor(ctx)
	err := s.accountChange(ctx, req.Msg.Password, req.Msg.Code, func(q *database.Queries) error {
		if err := q.UnlinkOIDC(ctx, database.UnlinkOIDCParams{UserID: p.UserID, Issuer: s.oidc.config.Issuer}); err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, p.UserID); err != nil {
			return err
		}
		return accountAudit(ctx, q, p.UserID, p.Email, "account.oidc_unlinked")
	})
	if err != nil {
		return nil, err
	}
	out := connect.NewResponse(&pb.AccessMutationResponse{})
	s.setCookie(out.Header(), "providah_access", "", -1)
	s.setCookie(out.Header(), "providah_refresh", "", -1)
	s.oidcCookie(out.Header(), "", -1)
	return out, nil
}
