package providers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSSHKeyImport(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	parsed, _ := ssh.NewPublicKey(public)
	key := string(ssh.MarshalAuthorizedKey(parsed))
	op := strings.Repeat("a", 64)
	name := "test-key-" + op
	for _, cloud := range []string{"aws", "hetzner", "digitalocean"} {
		t.Run(cloud, func(t *testing.T) {
			mode, writes := "", 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, code, content := "", 200, "application/json"
				write := false
				observedName, observedKey, observedID := name, key, "123"
				if cloud == "aws" {
					observedID = "key-1234567890abcdef0"
				}
				if mode == "wrong" {
					observedName = "another-request"
				}
				if mode == "wrong_key" {
					observedKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
				}
				if mode == "wrong_id" {
					observedID = "124"
					if cloud == "aws" {
						observedID = "key-1234567890abcdef1"
					}
				}
				if cloud == "aws" {
					content = "text/xml"
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					action := r.Form.Get("Action")
					if !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
						t.Fatal("unsigned request")
					}
					switch action {
					case "ImportKeyPair":
						write = true
						if r.Form.Get("KeyName") != name || r.Form.Get("PublicKeyMaterial") != base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(key))) {
							t.Fatal("import input changed")
						}
						body = fmt.Sprintf(`<keyPairId>key-1234567890abcdef0</keyPairId><keyName>%s</keyName>`, name)
					case "DescribeKeyPairs":
						if r.Form.Get("KeyName.1") != name || r.Form.Get("IncludePublicKey") != "true" {
							t.Fatal("observation scope lost")
						}
						body = fmt.Sprintf(`<keySet><item><keyPairId>%s</keyPairId><keyName>%s</keyName><publicKey>%s</publicKey></item></keySet>`, observedID, observedName, observedKey)
					default:
						t.Fatal("unexpected API", action)
					}
					if mode == "error" {
						code = 500
						body = `<Errors><Error><Code>InternalError</Code><Message>private</Message></Error></Errors>`
					}
					body = "<" + action + "Response>" + body + "</" + action + "Response>"
				} else {
					if r.Header.Get("Authorization") != "Bearer token" {
						t.Fatal("missing auth")
					}
					if r.Method == "POST" {
						write = true
						var input struct {
							Name      string `json:"name"`
							PublicKey string `json:"public_key"`
						}
						if json.NewDecoder(r.Body).Decode(&input) != nil || input.Name != name || input.PublicKey != key {
							t.Fatal("public key input changed")
						}
						code = 201
					} else if r.Method != "GET" {
						t.Fatal("unexpected method")
					}
					if cloud == "hetzner" {
						if r.URL.Path != "/v1/ssh_keys" {
							t.Fatal("wrong Hetzner endpoint")
						}
						if !write && r.URL.Query().Get("name") != name {
							t.Fatal("name scope lost")
						}
					} else if !strings.HasPrefix(r.URL.Path, "/v2/account/keys") {
						t.Fatal("wrong DO endpoint")
					}
					record := fmt.Sprintf(`{"id":%s,"name":%q,"public_key":%q}`, observedID, observedName, observedKey)
					if cloud == "hetzner" && !write {
						body = `{"ssh_keys":[` + record + `],"meta":{"pagination":{"next_page":null}}}`
					} else {
						body = `{"ssh_key":` + record + `}`
					}
					if mode == "error" {
						code = 500
						body = `{"error":{"code":"server_error","message":"private"},"message":"private"}`
					}
				}
				if write {
					writes++
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {content}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Provider: cloud, Region: "global", Credential: "token", Power: &provider.PowerRequest{Action: "create", ResourceKind: "access.ssh_key", Phase: "submit", OperationID: op, KeyCreate: &provider.SSHKeyCreate{Name: "test-key", PublicKey: key}}}
			if cloud == "aws" {
				req.Region = "us-east-1"
				req.Credential = `{"access_key_id":"fake","secret_access_key":"fake-secret"}`
			}
			run := func() *provider.PowerResult {
				v := createSSHKey(context.Background(), req, client)
				if err := v.Validate(); err != nil {
					t.Fatal(err)
				}
				return v
			}
			if got := run(); got.Outcome != "accepted" || writes != 1 {
				t.Fatal("import failed", got)
			}
			mode = "error"
			if got := run(); got.Outcome != "uncertain" || writes != 2 {
				t.Fatal("lost write retried", got)
			}
			req.Power.Phase = "observe"
			req.Power.NativeID = "123"
			if cloud == "aws" {
				req.Power.NativeID = "key-1234567890abcdef0"
			}
			for _, m := range []string{"", "wrong", "wrong_key", "wrong_id", "error"} {
				mode = m
				want := "succeeded"
				if strings.HasPrefix(m, "wrong") {
					want = "uncertain"
				}
				if m == "error" {
					want = "accepted"
				}
				if got := run(); got.Outcome != want || writes != 2 {
					t.Fatal("unsafe observation", m, got)
				}
			}
		})
	}
}
