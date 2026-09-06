package core

import (
	"filippo.io/age"
	"strings"
	"testing"
)

func TestSecurityBoundaries(t *testing.T) {
	t.Run("encryption", func(t *testing.T) {
		t.Parallel()
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		s := &Service{identity: id}
		plain := "do-secret-token-12345"
		cipher, err := s.seal(plain)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(cipher), plain) {
			t.Fatal("secret in ciphertext")
		}
		got, err := s.open(cipher)
		if err != nil || got != plain {
			t.Fatalf("round trip: %q %v", got, err)
		}
		cipher[len(cipher)-1] ^= 1
		if _, err = s.open(cipher); err == nil {
			t.Fatal("tamper accepted")
		}
	})
	for _, c := range []struct {
		name, org, filter string
		bad               bool
	}{{"matching", "org-a", "filter", false}, {"other org", "org-b", "filter", true}, {"other query", "org-a", "changed", true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			id, err := parseCursor(cursor("org-a", "filter", "id"), c.org, c.filter)
			if (err != nil) != c.bad {
				t.Fatalf("got %q %v", id, err)
			}
		})
	}
	for _, c := range []struct {
		provider, credential string
		valid                bool
	}{{"hetzner", "test-token", true}, {"aws", `{"access_key_id":"key","secret_access_key":"secret"}`, true}, {"aws", `{"access_key_id":"key"}`, false}, {"unknown", "test-token", false}} {
		t.Run(c.provider+c.credential, func(t *testing.T) {
			t.Parallel()
			if (validateCredential(c.provider, c.credential) == nil) != c.valid {
				t.Fatal("unexpected credential validation")
			}
		})
	}
}
