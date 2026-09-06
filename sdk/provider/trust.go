package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"slices"
	"time"
)

const runtimeApprovalDomain = "providah-runtime-approval-v1\n"

// RuntimeRelease authorizes immutable local Docker image IDs, not mutable OCI tags.
type RuntimeRelease struct {
	Version   int       `json:"version"`
	Image     string    `json:"image_id"`
	Providers []string  `json:"providers"`
	ExpiresAt time.Time `json:"expires_at"`
}
type SignedRuntimeRelease struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type trustedRuntimeRelease struct {
	RuntimeRelease
	KeyID string
}
type RuntimeTrust struct{ releases []trustedRuntimeRelease }

func decodeTrustJSON(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 65536 {
		return errors.New("invalid runtime trust document size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return errors.New("invalid runtime trust document")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing runtime trust data")
	}
	return nil
}
func (r RuntimeRelease) validate(now time.Time) error {
	if r.Version != 1 || !imageID.MatchString(r.Image) || len(r.Providers) == 0 || len(r.Providers) > 3 || !r.ExpiresAt.After(now) {
		return errors.New("invalid or expired runtime release")
	}
	seen := map[string]bool{}
	for _, p := range r.Providers {
		if !slices.Contains([]string{"aws", "digitalocean", "hetzner"}, p) || seen[p] {
			return errors.New("invalid runtime release provider")
		}
		seen[p] = true
	}
	return nil
}

// ParseRuntimeTrust separates deployment-owned trust roots from publisher-supplied approvals.
func ParseRuntimeTrust(keysJSON, releasesJSON []byte, now time.Time) (*RuntimeTrust, error) {
	var encoded map[string]string
	var releases []SignedRuntimeRelease
	if e := decodeTrustJSON(keysJSON, &encoded); e != nil {
		return nil, e
	}
	if e := decodeTrustJSON(releasesJSON, &releases); e != nil {
		return nil, e
	}
	if len(encoded) < 1 || len(encoded) > 16 || len(releases) < 1 || len(releases) > 32 {
		return nil, errors.New("runtime trust requires 1–16 keys and 1–32 approvals")
	}
	keys := map[string]ed25519.PublicKey{}
	for id, value := range encoded {
		block, rest := pem.Decode([]byte(value))
		if id == "" || len(id) > 80 || block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("invalid runtime publisher key")
		}
		parsed, e := x509.ParsePKIXPublicKey(block.Bytes)
		if e != nil {
			return nil, errors.New("invalid runtime publisher key")
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("runtime publisher key must be Ed25519")
		}
		keys[id] = key
	}
	trust := &RuntimeTrust{}
	for _, signed := range releases {
		key, ok := keys[signed.KeyID]
		if !ok {
			return nil, errors.New("untrusted runtime publisher")
		}
		payload, e := base64.StdEncoding.DecodeString(signed.Payload)
		if e != nil || len(payload) > 4096 {
			return nil, errors.New("invalid signed runtime payload")
		}
		signature, e := base64.StdEncoding.DecodeString(signed.Signature)
		if e != nil || !ed25519.Verify(key, append([]byte(runtimeApprovalDomain), payload...), signature) {
			return nil, errors.New("invalid runtime publisher signature")
		}
		var release RuntimeRelease
		if e := decodeTrustJSON(payload, &release); e != nil {
			return nil, e
		}
		if e := release.validate(now); e != nil {
			return nil, e
		}
		trust.releases = append(trust.releases, trustedRuntimeRelease{RuntimeRelease: release, KeyID: signed.KeyID})
	}
	return trust, nil
}

// Allows is evaluated before the isolated --describe probe. Nil preserves the explicit deployment-approved development mode.
func (t *RuntimeTrust) Allows(image, cloud string, now time.Time) bool {
	if t == nil {
		return true
	}
	for _, r := range t.releases {
		if r.Image == image && r.ExpiresAt.After(now) && slices.Contains(r.Providers, cloud) {
			return true
		}
	}
	return false
}
func SignRuntimeRelease(key ed25519.PrivateKey, keyID string, release RuntimeRelease, now time.Time) (SignedRuntimeRelease, error) {
	if len(key) != ed25519.PrivateKeySize || keyID == "" || len(keyID) > 80 {
		return SignedRuntimeRelease{}, errors.New("invalid runtime signing key")
	}
	if e := release.validate(now); e != nil {
		return SignedRuntimeRelease{}, e
	}
	payload, e := json.Marshal(release)
	if e != nil {
		return SignedRuntimeRelease{}, e
	}
	return SignedRuntimeRelease{KeyID: keyID, Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(runtimeApprovalDomain), payload...)))}, nil
}

// Approval returns launcher-verified provenance, never provider-supplied claims.
func (t *RuntimeTrust) Approval(image, cloud string, admittedAt time.Time) (string, string) {
	if t != nil {
		for _, r := range t.releases {
			if r.Image == image && r.ExpiresAt.After(admittedAt) && slices.Contains(r.Providers, cloud) {
				return r.KeyID, r.ExpiresAt.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	return "", ""
}
