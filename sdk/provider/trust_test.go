package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestRuntimePublisherTrust(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	keys, _ := json.Marshal(map[string]string{"publisher": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	release := RuntimeRelease{Version: 1, Image: "sha256:" + strings.Repeat("a", 64), Providers: []string{"aws"}, ExpiresAt: now.Add(time.Hour)}
	signed, e := SignRuntimeRelease(key, "publisher", release, now)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal([]SignedRuntimeRelease{signed})
	trust, e := ParseRuntimeTrust(keys, raw, now)
	keyID, expiry := trust.Approval(release.Image, "aws", now)
	if keyID != "publisher" || expiry != release.ExpiresAt.Format(time.RFC3339Nano) {
		t.Fatal("approval provenance lost")
	}
	if e != nil || !trust.Allows(release.Image, "aws", now) || trust.Allows(release.Image, "hetzner", now) || trust.Allows("sha256:"+strings.Repeat("b", 64), "aws", now) || trust.Allows(release.Image, "aws", release.ExpiresAt) {
		t.Fatal("incorrect admission", e)
	}
	if _, e = ParseRuntimeTrust(keys, raw, release.ExpiresAt); e == nil {
		t.Fatal("expired approval accepted")
	}
	for _, mode := range []string{"payload", "signature", "publisher", "version", "provider", "extra", "trailing", "oversize", "domain"} {
		candidate := signed
		switch mode {
		case "payload":
			candidate.Payload = base64.StdEncoding.EncodeToString([]byte(`{}`))
		case "signature":
			candidate.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
		case "publisher":
			candidate.KeyID = "stranger"
		case "version", "provider", "extra", "domain":
			body, _ := base64.StdEncoding.DecodeString(candidate.Payload)
			if mode == "version" {
				body = []byte(strings.Replace(string(body), `"version":1`, `"version":2`, 1))
			}
			if mode == "provider" {
				body = []byte(strings.Replace(string(body), `"aws"`, `"unknown"`, 1))
			}
			if mode == "extra" {
				body = append([]byte(`{"unrecognized":true,`), body[1:]...)
			}
			message := append([]byte(runtimeApprovalDomain), body...)
			if mode == "domain" {
				message = body
			}
			candidate.Payload = base64.StdEncoding.EncodeToString(body)
			candidate.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, message))
		}
		altered, _ := json.Marshal([]SignedRuntimeRelease{candidate})
		if mode == "trailing" {
			altered = append(altered, []byte(` {}`)...)
		}
		if mode == "oversize" {
			altered = []byte(strings.Repeat(" ", 65537))
		}
		if _, e := ParseRuntimeTrust(keys, altered, now); e == nil {
			t.Fatal("untrusted release admitted", mode)
		}
	}
	if _, e := ParseRuntimeTrust([]byte(`{}`), raw, now); e == nil {
		t.Fatal("empty trust roots accepted")
	}
}
