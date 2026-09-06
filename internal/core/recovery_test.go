package core

import (
	"bytes"
	"strings"
	"testing"
)

func TestRecoveryVerifier(t *testing.T) {
	code := "01234567-89abcdef-01234567-89abcdef"
	first := recoveryVerifier("user", code)
	if len(first) != 32 || !bytes.Equal(first, recoveryVerifier("user", strings.ToUpper(code))) || !bytes.Equal(first, recoveryVerifier("user", strings.ReplaceAll(code, "-", ""))) {
		t.Fatal("verifier normalization failed")
	}
	if bytes.Equal(first, recoveryVerifier("other", code)) {
		t.Fatal("verifier lacks user scope")
	}
	for _, bad := range []string{"", "123456", strings.Repeat("z", 32), strings.Repeat("a", 33)} {
		if recoveryVerifier("user", bad) != nil {
			t.Fatal("malformed recovery code accepted")
		}
	}
}
