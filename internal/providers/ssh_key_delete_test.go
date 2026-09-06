package providers

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSSHKeyDeletionSDKs(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			id, region, credential := "123", "global", "test-token"
			if cloud == "aws" {
				id, region, credential = "key-12345678", "us-east-1", `{"access_key_id":"test","secret_access_key":"test"}` // gitleaks:allow -- synthetic cloud identifier/credential fixture
			}
			writes := 0
			changed, missing, denied, ambiguous, wrong := false, false, false, false, false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				code, body := 200, ""
				deleting := r.Method == "DELETE"
				if cloud == "aws" {
					if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
						t.Fatal("missing signed credential")
					}
					if e := r.ParseForm(); e != nil {
						t.Fatal(e)
					}
					deleting = r.Form.Get("Action") == "DeleteKeyPair"
					if deleting {
						if r.Form.Get("KeyPairId") != id || r.Form.Get("KeyName") != "" {
							t.Fatal("delete must use immutable ID")
						}
						body = "<DeleteKeyPairResponse><return>true</return></DeleteKeyPairResponse>"
					} else {
						if r.Form.Get("Action") != "DescribeKeyPairs" || r.Form.Get("Filter.1.Name") != "key-pair-id" || r.Form.Get("Filter.1.Value.1") != id {
							t.Fatal("wrong exact ID filter", r.Form)
						}
						body = "<DescribeKeyPairsResponse><keySet><item><keyPairId>" + id + "</keyPairId><keyName>unused</keyName><keyFingerprint>fingerprint</keyFingerprint><publicKey>PUBLIC-SENTINEL</publicKey></item></keySet></DescribeKeyPairsResponse>"
						if missing {
							body = "<DescribeKeyPairsResponse><keySet/></DescribeKeyPairsResponse>"
						}
					}
				} else {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Fatal("missing explicit credential")
					}
					path := "/v2/account/keys/123"
					if cloud == "hetzner" {
						path = "/v1/ssh_keys/123"
					}
					if r.URL.Path != path {
						t.Fatal("wrong endpoint", r.URL.Path)
					}
					body = `{"ssh_key":{"id":123,"name":"unused","fingerprint":"fingerprint","public_key":"PUBLIC-SENTINEL"}}`
					if deleting {
						code, body = 204, ""
					}
					if missing {
						code, body = 404, `{"error":{"code":"not_found","message":"gone"}}`
					}
				}
				if changed {
					body = strings.ReplaceAll(body, "fingerprint", "changed")
				}
				if wrong {
					body = strings.ReplaceAll(body, id, map[bool]string{true: "key-87654321", false: "456"}[cloud == "aws"])
				}
				if deleting {
					writes++
					if ambiguous {
						code = 500
					}
				}
				if denied {
					code = 403
				}
				return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Version: provider.Protocol, Provider: cloud, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Region: region, Credential: credential, Power: &provider.PowerRequest{ResourceKind: "access.ssh_key", OperationID: strings.Repeat("c", 64), Action: "delete", Phase: "preview", NativeID: id, ExpectedStatus: "present"}}
			run := func() *provider.PowerResult {
				out := Power(context.Background(), req, client)
				if out.Validate() != nil || out.Power == nil {
					t.Fatal("invalid response", fmt.Sprint(out))
				}
				return out.Power
			}
			preview := run()
			if preview.Outcome != "preview" || writes != 0 || !strings.Contains(preview.DeletionImpact, "does not revoke existing server access") || strings.Contains(preview.DeletionImpact, "PUBLIC-SENTINEL") {
				t.Fatal("unsafe preview", preview)
			}
			req.Power.Phase = "submit"
			req.Power.DeletionImpact = preview.DeletionImpact
			for _, flag := range []*bool{&changed, &wrong, &denied, &missing} {
				*flag = true
				if run().Outcome != "failed" || writes != 0 {
					t.Fatal("unsafe submit")
				}
				*flag = false
			}
			if run().Outcome != "accepted" || writes != 1 {
				t.Fatal("delete not submitted once")
			}
			ambiguous = true
			if run().Outcome != "uncertain" || writes != 2 {
				t.Fatal("ambiguous mutation retried")
			}
			ambiguous = false
			req.Power.Phase = "observe"
			if run().Outcome != "accepted" {
				t.Fatal("presence treated as completion")
			}
			denied = true
			if run().Outcome == "succeeded" {
				t.Fatal("permission error treated as absence")
			}
			denied = false
			missing = true
			if run().Outcome != "succeeded" || writes != 2 {
				t.Fatal("absence not confirmed")
			}
		})
	}
}
