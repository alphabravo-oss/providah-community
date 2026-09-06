// runtime-sign creates an offline publisher approval. It never runs or pulls an image.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"os"
	"strings"
	"time"
)

func run() error {
	keyPath := flag.String("private-key", "", "PKCS8 Ed25519 PEM file")
	keyID := flag.String("key-id", "", "deployment trust-root identifier")
	image := flag.String("image-id", "", "immutable Docker sha256 image ID")
	clouds := flag.String("providers", "", "comma-separated provider IDs")
	expires := flag.String("expires", "", "approval expiry in RFC3339")
	flag.Parse()
	if flag.NArg() != 0 || *keyPath == "" {
		return errors.New("supply --private-key, --key-id, --image-id, --providers and --expires")
	}
	data, e := os.ReadFile(*keyPath)
	if e != nil {
		return errors.New("could not read signing key")
	}
	block, rest := pem.Decode(data)
	if len(data) > 16384 || block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return errors.New("invalid PKCS8 signing key")
	}
	parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return errors.New("invalid PKCS8 signing key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return errors.New("signing key must be Ed25519")
	}
	expiry, e := time.Parse(time.RFC3339, *expires)
	if e != nil {
		return errors.New("expiry must be RFC3339")
	}
	signed, e := provider.SignRuntimeRelease(key, *keyID, provider.RuntimeRelease{Version: 1, Image: *image, Providers: strings.Split(*clouds, ","), ExpiresAt: expiry}, time.Now())
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode([]provider.SignedRuntimeRelease{signed})
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
