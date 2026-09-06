package core

import (
	"strings"
	"testing"
)

func TestOIDCReturnTo(t *testing.T) {
	for _, raw := range []string{"https://evil.test/app", "//evil.test/app", "javascript:alert(1)", "/api/oidc/callback", "/app/../api", "/app/./resources", "/app/%2e%2e/api", "/app/%5Cevil", "/app/%0Aevil", "/app\\evil", "/app\n", "/app?x=" + strings.Repeat("é", 500)} {
		if _, e := oidcReturnTo(raw); e == nil {
			t.Fatalf("unsafe return accepted: %q", raw)
		}
	}
	for raw, want := range map[string]string{"": "/", "/": "/", "/app/resources?view=v&org=o#detail": "/app/resources?org=o&view=v#detail", "/admin/access?org=o": "/admin/access?org=o", "/?oidc=failed": "/"} {
		got, e := oidcReturnTo(raw)
		if e != nil || got != want {
			t.Fatalf("return %q: %q %v", raw, got, e)
		}
	}
}
