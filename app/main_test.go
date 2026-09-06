package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeySettings(t *testing.T) {
	t.Setenv("AGE_IDENTITY", "")
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGE_IDENTITY_FILE", path)
	raw, err := readKeySetting("AGE_IDENTITY")
	if err != nil || raw != "test-key\n" {
		t.Fatal("file injection failed")
	}
	t.Setenv("AGE_IDENTITY", "other-key")
	if _, err = readKeySetting("AGE_IDENTITY"); err == nil {
		t.Fatal("conflicting key sources accepted")
	}
	t.Setenv("AGE_IDENTITY", "")
	if err = os.WriteFile(path, []byte(strings.Repeat("x", 16385)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readKeySetting("AGE_IDENTITY"); err == nil {
		t.Fatal("unbounded key input accepted")
	}
}
