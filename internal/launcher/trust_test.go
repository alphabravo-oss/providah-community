package launcher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runtimeTestTrust(t *testing.T, image string, clouds []string) *provider.RuntimeTrust {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	roots, _ := json.Marshal(map[string]string{"test": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))})
	signed, e := provider.SignRuntimeRelease(key, "test", provider.RuntimeRelease{Version: 1, Image: image, Providers: clouds, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	releases, _ := json.Marshal([]provider.SignedRuntimeRelease{signed})
	dir := t.TempDir()
	kp, rp := filepath.Join(dir, "keys.json"), filepath.Join(dir, "releases.json")
	if e = os.WriteFile(kp, roots, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(rp, releases, 0600); e != nil {
		t.Fatal(e)
	}
	trust, e := ReadRuntimeTrust(kp, rp)
	if e != nil {
		t.Fatal(e)
	}
	return trust
}
func TestRuntimeTrustBeforeProbe(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	image := "sha256:" + strings.Repeat("a", 64)
	// This fixture permits image inspection and records any attempt to run code.
	script := "#!/bin/sh\nif [ \"$1\" = image ]; then echo " + image + "; exit 0; fi\ntouch '" + marker + "'\nexit 1\n"
	if e := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	for _, trust := range []*provider.RuntimeTrust{runtimeTestTrust(t, "sha256:"+strings.Repeat("b", 64), []string{"aws"}), runtimeTestTrust(t, image, []string{"hetzner"})} {
		_, e := LoadRuntimes(context.Background(), `{"aws":["local:test"]}`, "", trust)
		if e == nil || !strings.Contains(e.Error(), "trusted publisher approval") {
			t.Fatal("unapproved image admitted", e)
		}
		if _, e = os.Stat(marker); !os.IsNotExist(e) {
			t.Fatal("unapproved runtime executed")
		}
	}
	if _, e := ReadRuntimeTrust("missing", ""); e == nil {
		t.Fatal("partial trust configuration accepted")
	}
	if _, e := ReadRuntimeTrust(dir, dir); e == nil {
		t.Fatal("directory accepted as trust input")
	}
}
func TestSignedProviderContainer(t *testing.T) {
	ref := os.Getenv("TEST_PROVIDER_IMAGE")
	if ref == "" {
		t.Skip("set TEST_PROVIDER_IMAGE for signed container admission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	image, e := ResolveImage(ctx, ref)
	if e != nil {
		t.Fatal(e)
	}
	trust := runtimeTestTrust(t, image, []string{"aws", "digitalocean", "hetzner"})
	catalog, e := LoadRuntimes(ctx, "", ref, trust)
	if e != nil || len(catalog) != 3 {
		t.Fatal("signed container admission failed", e)
	}
	for _, r := range catalog {
		if r.PublisherKeyID != "test" || r.ApprovalExpiresAt == "" {
			t.Fatal("signed container provenance missing")
		}
	}
}

func TestDescriptionCannotClaimPublisherTrust(t *testing.T) {
	image := "sha256:" + strings.Repeat("a", 64)
	entry := provider.Runtime{Provider: "aws", Image: image, Protocol: provider.Protocol, Version: "1", SDKVersion: "1", PublisherKeyID: "forged", ApprovalExpiresAt: "not-a-date"}
	raw, _ := json.Marshal([]provider.Runtime{entry})
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\nimage) echo '" + image + "';;\nrun) echo '" + string(raw) + "';;\nesac\n"
	if e := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	for _, signed := range []bool{false, true} {
		var trust *provider.RuntimeTrust
		if signed {
			trust = runtimeTestTrust(t, image, []string{"aws"})
		}
		entries, e := LoadRuntimes(context.Background(), `{"aws":["local:test"]}`, "", trust)
		if e != nil || len(entries) != 1 {
			t.Fatal("admission failed", e)
		}
		if signed {
			if entries[0].PublisherKeyID != "test" || entries[0].ApprovalExpiresAt == "" {
				t.Fatal("verified provenance missing")
			}
		} else if entries[0].PublisherKeyID != "" || entries[0].ApprovalExpiresAt != "" {
			t.Fatal("runtime forged publisher verification")
		}
	}
}
