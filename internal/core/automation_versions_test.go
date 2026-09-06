package core

import (
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"strings"
	"testing"
)

func TestAutomationRuntimeAllowlist(t *testing.T) {
	valid := `[{"runtime":"opentofu","image":"sha256:` + strings.Repeat("a", 64) + `","version":"1.10.0"}]`
	if rows, e := automation.ParseRuntimes(valid); e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	for _, raw := range []string{`{}`, valid + `{}`, strings.Replace(valid, "sha256:"+strings.Repeat("a", 64), "image:latest", 1), strings.Replace(valid, "opentofu", "shell", 1), strings.Replace(valid, `"version":`, `"unknown":"value","version":`, 1), `[` + valid[1:len(valid)-1] + `,` + valid[1:len(valid)-1] + `]`} {
		if _, e := automation.ParseRuntimes(raw); e == nil {
			t.Fatal("invalid runtime policy accepted", raw)
		}
	}
}
