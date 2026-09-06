package core

import (
	"encoding/json"
	"filippo.io/age"
	"testing"
)

func TestSecretEnvelope(t *testing.T) {
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := encryptSecret("secret", key.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptSecret(cipher, key)
	if err != nil || plain != "secret" {
		t.Fatal("round trip failed")
	}
	var envelope secretEnvelope
	if json.Unmarshal(cipher, &envelope) != nil {
		t.Fatal("missing metadata")
	}
	if _, err := decryptSecret(envelope.Ciphertext, key); err == nil {
		t.Fatal("runtime accepted raw old format")
	}
	for _, field := range []string{"key", "algorithm"} {
		altered := envelope
		if field == "key" {
			altered.KeyID = "unknown"
		} else {
			altered.Algorithm = "unknown"
		}
		raw, _ := json.Marshal(altered)
		if _, err := decryptSecret(raw, key); err == nil {
			t.Fatal("unknown metadata accepted")
		}
	}
	if _, err := decryptBounded(cipher, 2, key); err == nil {
		t.Fatal("size limit ignored")
	}
}
