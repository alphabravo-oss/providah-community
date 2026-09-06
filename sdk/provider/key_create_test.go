package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
)

func TestPublicKeyCreationContract(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	key := string(ssh.MarshalAuthorizedKey(parsed))
	c := SSHKeyCreate{Name: "test-key", PublicKey: key}
	if c.Validate() != nil || !PublicKeysEqual(key, strings.TrimSpace(key)+" comment") {
		t.Fatal("valid public key rejected")
	}
	for _, raw := range []string{"-----BEGIN OPENSSH PRIVATE KEY-----", "-----BEGIN OPENSSH PRIVATE KEY-----\nprivate material\n" + key, "invalid line\n" + key, key + key, "command=\"false\" " + key, "ssh-dss invalid", strings.Repeat("x", 16385)} {
		c.PublicKey = raw
		if c.Validate() == nil {
			t.Fatal("unsafe public key accepted")
		}
	}
	c.PublicKey = key
	p := PowerRequest{ResourceKind: "access.ssh_key", Action: "create", Phase: "submit", OperationID: strings.Repeat("a", 64), KeyCreate: &c}
	for _, cloud := range []string{"aws", "hetzner", "digitalocean"} {
		if err := p.Validate(cloud); err != nil {
			t.Fatal(err)
		}
	}
	p.Action = "delete"
	if p.Validate("hetzner") == nil {
		t.Fatal("key input accepted on delete")
	}
}
