package vaultstore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPinnedKV(t *testing.T) {
	t.Setenv("BAO_ADDR", "http://untrusted.invalid")
	t.Setenv("VAULT_ADDR", "http://untrusted.invalid")
	t.Setenv("BAO_TOKEN", "untrusted-token")
	t.Setenv("BAO_NAMESPACE", "untrusted")
	org := strings.Repeat("a", 64)
	calls, status := 0, 200
	version, destroyed, deleted, value := 7, false, "", any("provider-test-token")
	store, err := New(Config{Address: "https://vault.invalid:8200", Mount: "secret", Namespace: "tenant", Token: "installation-test-token", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://vault.invalid:8200/v1/secret/data/providah/"+org+"/hetzner/production?version=7" || r.Header.Get("X-Vault-Token") != "installation-test-token" || r.Header.Get("X-Vault-Namespace") != "tenant" {
			t.Fatal("wrong vault scope or auth")
		}
		body, _ := json.Marshal(map[string]any{"data": map[string]any{"data": map[string]any{"credential": value}, "metadata": map[string]any{"version": version, "destroyed": destroyed, "deletion_time": deleted}}})
		if status != 200 {
			body = []byte(`{"errors":["installation-test-token provider-test-token"]}`)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://attacker.invalid/"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := Parse(store.Bind(Reference{Path: "production", Key: "credential", Version: 7}))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, e := store.Resolve(context.Background(), org, "hetzner", *ref)
		if e != nil || got != "provider-test-token" {
			t.Fatal("resolve failed", e)
		}
	}
	if calls != 2 {
		t.Fatal("secret cached")
	}
	reject := func() {
		t.Helper()
		got, e := store.Resolve(context.Background(), org, "hetzner", *ref)
		if got != "" || e == nil || e.Error() != "external credential unavailable" {
			t.Fatal("unsafe external response")
		}
	}
	version = 8
	reject()
	version = 7
	destroyed = true
	reject()
	destroyed = false
	deleted = "2026-01-01T00:00:00Z"
	reject()
	deleted = ""
	for _, v := range []any{nil, 17, "short", Prefix + `{}`, strings.Repeat("x", 16385)} {
		value = v
		reject()
	}
	value = "provider-test-token"
	for _, code := range []int{307, 403, 404, 429, 500} {
		status = code
		before := calls
		reject()
		if calls != before+1 {
			t.Fatal("redirect or retry occurred")
		}
	}
	status = 200
	before := calls
	ref.Store = strings.Repeat("b", 64)
	reject()
	ref.Store = store.id
	if calls != before {
		t.Fatal("rebound store reached network")
	}
	org = "../../other"
	reject()
	if calls != before {
		t.Fatal("invalid organization reached network")
	}
}

func TestReferenceAndConfiguration(t *testing.T) {
	for _, raw := range []string{`{}`, `{"path":"../outside","key":"credential","version":1}`, `{"path":"a/%2e%2e/b","key":"credential","version":1}`, `{"path":"a","key":"credential","version":0}`, `{"path":"a","key":"credential","version":1,"unknown":true}`, `{"path":"a","key":"credential","version":1} {}`} {
		if _, err := Parse(Prefix + raw); err == nil {
			t.Fatal("unsafe reference accepted")
		}
	}
	if ref, err := Parse("provider-test-token"); err != nil || ref != nil {
		t.Fatal("builtin credential parsed as reference")
	}
	for _, addr := range []string{"http://vault.invalid", "https://user:pass@vault.invalid", "https://vault.invalid/path", "https://vault.invalid/?token=secret"} {
		if _, err := New(Config{Address: addr, Mount: "secret", Token: "test-token"}); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	var store *Store
	if _, err := store.Resolve(context.Background(), strings.Repeat("a", 64), "hetzner", Reference{}); err == nil {
		t.Fatal("unconfigured vault accepted")
	}
}
