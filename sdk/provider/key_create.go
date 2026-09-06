package provider

import (
	"crypto/rsa"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SSHKeyCreate accepts one public key only; no private material or authorized_keys options.
type SSHKeyCreate struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

func (c SSHKeyCreate) Validate() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`).MatchString(c.Name) || len(c.PublicKey) > 16384 {
		return errors.New("invalid SSH key name or size")
	}
	if strings.ContainsAny(strings.TrimSpace(c.PublicKey), "\r\n") {
		return errors.New("supply a single public key line")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(c.PublicKey))
	if err != nil || len(options) != 0 || strings.TrimSpace(string(rest)) != "" {
		return errors.New("supply one OpenSSH public key without options")
	}
	switch key.Type() {
	case ssh.KeyAlgoED25519:
		return nil
	case ssh.KeyAlgoRSA:
		crypto, ok := key.(ssh.CryptoPublicKey)
		if ok {
			if rsaKey, ok := crypto.CryptoPublicKey().(*rsa.PublicKey); ok && rsaKey.N.BitLen() >= 2048 {
				return nil
			}
		}
	}
	return errors.New("use Ed25519 or RSA with at least 2048 bits")
}
func PublicKeysEqual(a, b string) bool {
	first, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(a))
	if err != nil || len(options) != 0 || strings.TrimSpace(string(rest)) != "" {
		return false
	}
	second, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(b))
	return err == nil && len(options) == 0 && strings.TrimSpace(string(rest)) == "" && string(first.Marshal()) == string(second.Marshal())
}
