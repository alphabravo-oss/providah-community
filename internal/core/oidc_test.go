package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

type oidcTestTransport func(*http.Request) (*http.Response, error)

func (f oidcTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeIdentity struct {
	nonce, challenge, subject, invalid string
	exchanges                          int
}

func oidcFixture(t *testing.T, cfg Config) (*oidcClient, *fakeIdentity) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdentity{subject: "stable-subject"}
	cfg.OIDC = &OIDCConfig{Issuer: "https://identity.test", ClientID: "providah-client", ClientSecret: "test-only-client-secret"}
	cfg.OIDCHTTPClient = &http.Client{Transport: oidcTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "identity.test" {
			t.Fatalf("unexpected host %s", r.URL.Host)
		}
		var body any
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			body = map[string]any{"issuer": cfg.OIDC.Issuer, "authorization_endpoint": cfg.OIDC.Issuer + "/authorize", "token_endpoint": cfg.OIDC.Issuer + "/token", "jwks_uri": cfg.OIDC.Issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}}
		case "/keys":
			body = jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}}
		case "/token":
			f.exchanges++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != cfg.Origin+"/api/oidc/callback" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != f.challenge {
				t.Fatal("invalid callback or PKCE verifier")
			}
			user, password, _ := r.BasicAuth()
			if user != cfg.OIDC.ClientID || password != cfg.OIDC.ClientSecret {
				t.Fatal("invalid client authentication")
			}
			claims := jwt.MapClaims{"iss": cfg.OIDC.Issuer, "sub": f.subject, "aud": cfg.OIDC.ClientID, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": f.nonce, "email": "owner@example.com", "email_verified": true, "groups": []string{"administrator"}}
			switch f.invalid {
			case "nonce":
				claims["nonce"] = "wrong"
			case "issuer":
				claims["iss"] = "https://other.test"
			case "audience":
				claims["aud"] = "other-client"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "azp":
				claims["azp"] = "other-client"
			case "multiple-audience":
				claims["aud"] = []string{cfg.OIDC.ClientID, "other-client"}
			case "old-issuance":
				claims["iat"] = time.Now().Add(-time.Hour).Unix()
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test-key"
			signed, e := token.SignedString(key)
			if e != nil {
				t.Fatal(e)
			}
			if f.invalid == "signature" {
				pieces := strings.Split(signed, ".")
				pieces[2] = "AAAA"
				signed = strings.Join(pieces, ".")
			}
			body = map[string]any{"access_token": "access-token-never-store", "token_type": "Bearer", "id_token": signed, "expires_in": 60}
		default:
			t.Fatalf("unexpected identity endpoint %s", r.URL.Path)
		}
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
	})}
	c, err := newOIDCClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, f
}
func (f *fakeIdentity) begin(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	f.nonce = q.Get("nonce")
	f.challenge = q.Get("code_challenge")
	if u.Host != "identity.test" || q.Get("code_challenge_method") != "S256" || f.nonce == "" || q.Get("scope") != "openid" || q.Get("response_type") != "code" {
		t.Fatal("incomplete OIDC request")
	}
	return q.Get("state")
}
func TestOIDCConfiguration(t *testing.T) {
	cfg := Config{Origin: "http://console.test", SessionKey: randomID()}
	client, _ := oidcFixture(t, cfg)
	if client.oauth.RedirectURL != "http://console.test/api/oidc/callback" {
		t.Fatal("unbound callback")
	}
	for _, issuer := range []string{"http://identity.test", "https://127.0.0.1", "https://identity.test?redirect=other", "https://user:secret@identity.test"} {
		cfg.OIDC = &OIDCConfig{Issuer: issuer, ClientID: "client", ClientSecret: "test-secret"}
		if _, err := newOIDCClient(cfg); err == nil {
			t.Fatal("untrusted OIDC configuration accepted")
		}
	}
	s := &Service{cfg: cfg}
	state := randomID()
	if s.oidcProof("nonce", state) == s.oidcProof("pkce", state) || s.oidcProof("pkce", state) == s.oidcProof("pkce", randomID()) {
		t.Fatal("proof derivation is not scoped")
	}
}

func TestOIDCStartupAndResponseBound(t *testing.T) {
	reader := strings.NewReader(strings.Repeat("x", 2<<20))
	calls := 0
	cfg := Config{Origin: "http://console.test", OIDC: &OIDCConfig{Issuer: "https://identity.test", ClientID: "client", ClientSecret: "test-secret"}, OIDCHTTPClient: &http.Client{Transport: oidcTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(reader), Header: http.Header{}, Request: r}, nil
	})}}
	client, err := newOIDCClient(cfg)
	if err != nil || calls != 0 {
		t.Fatal("application initialization contacted the IdP")
	}
	if client.initialize(context.Background()) == nil || calls != 1 || reader.Len() < (1<<20)-1 {
		t.Fatal("unbounded identity response")
	}
}
