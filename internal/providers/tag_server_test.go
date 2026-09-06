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

func TestServerTagAdapter(t *testing.T) {
	for _, cloud := range []string{"aws", "hetzner", "digitalocean"} {
		for _, mode := range []string{"submit", "changed", "observe-equal", "observe-pending", "read-denied", "write-failed", "partial-failed", "noop"} {
			t.Run(cloud+"/"+mode, func(t *testing.T) {
				before, after := &provider.Tags{Labels: map[string]string{"old": "value"}}, &provider.Tags{Labels: map[string]string{"new": "value"}}
				id, status, credential := "123", "running", "fake-token"
				if cloud == "aws" {
					id = "i-12345678"
					credential = `{"access_key_id":"fake","secret_access_key":"fake-secret"}`
				}
				if cloud == "digitalocean" {
					status = "active"
					before = &provider.Tags{Names: []string{"old"}}
					after = &provider.Tags{Names: []string{"new"}}
				}
				observed := "old"
				if mode == "observe-equal" {
					observed = "new"
				}
				if mode == "changed" {
					observed = "other"
				}
				if mode == "noop" {
					after = before
				}
				phase := "submit"
				if strings.HasPrefix(mode, "observe-") {
					phase = "observe"
				}
				writes := 0
				reads := 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					action := ""
					if cloud == "aws" {
						if e := r.ParseForm(); e != nil {
							t.Fatal(e)
						}
						action = r.Form.Get("Action")
					}
					read := r.Method == "GET" || action == "DescribeInstances"
					body := ""
					code := 200
					if read {
						reads++
						switch cloud {
						case "aws":
							body = fmt.Sprintf(`<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>%s</instanceId><instanceState><name>running</name></instanceState><tagSet><item><key>%s</key><value>value</value></item></tagSet></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, id, observed)
						case "hetzner":
							body = fmt.Sprintf(`{"server":{"id":123,"status":"running","labels":{%q:"value"}}}`, observed)
						case "digitalocean":
							body = fmt.Sprintf(`{"droplet":{"id":123,"status":"active","tags":[%q]}}`, observed)
						}
						if mode == "read-denied" {
							code = 403
							body = `{"error":{"code":"forbidden"}}`
							if cloud == "aws" {
								body = `<Response><Errors><Error><Code>UnauthorizedOperation</Code></Error></Errors></Response>`
							}
						}
					} else {
						writes++
						if writes > 3 {
							t.Fatal("mutation retried")
						}
						switch cloud {
						case "aws":
							if action != "CreateTags" && action != "DeleteTags" {
								t.Fatal(action)
							}
							if r.Form.Get("ResourceId.1") != id || r.Form.Get("Tag.1.Value") != "value" {
								t.Fatal("wrong mutation target", r.Form)
							}
							body = "<" + action + "Response/>"
						case "hetzner":
							if r.Method != "PUT" || !strings.HasSuffix(r.URL.Path, "/servers/123") {
								t.Fatal(r.URL)
							}
							raw, _ := io.ReadAll(r.Body)
							if !strings.Contains(string(raw), `"new":"value"`) || strings.Contains(string(raw), `"name"`) {
								t.Fatal("wrong label replacement", string(raw))
							}
							body = `{"server":{"id":123,"status":"running","labels":{"new":"value"}}}`
						case "digitalocean":
							if r.URL.Path == "/v2/tags" {
								code = 201
								body = `{"tag":{"name":"new"}}`
							} else {
								if r.URL.Path != "/v2/tags/new/resources" && r.URL.Path != "/v2/tags/old/resources" {
									t.Fatal(r.URL)
								}
								raw, _ := io.ReadAll(r.Body)
								if !strings.Contains(string(raw), `"resource_id":"123"`) {
									t.Fatal("wrong droplet")
								}
								code = 204
							}
						}
						if mode == "write-failed" || mode == "partial-failed" && (writes == 2 || cloud == "hetzner") {
							return nil, fmt.Errorf("private-write-error")
						}
					}
					return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				out := Power(context.Background(), provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: cloud, Region: "us-east-1", Credential: credential, Power: &provider.PowerRequest{OperationID: strings.Repeat("c", 64), Phase: phase, Action: "tags", NativeID: id, ExpectedStatus: status, ExpectedTags: before, TargetTags: after}}, client)
				if out.Power == nil || out.Validate() != nil {
					t.Fatal(out)
				}
				want := "accepted"
				wantWrites := 0
				switch mode {
				case "submit":
					wantWrites = 2
					if cloud == "hetzner" {
						wantWrites = 1
					}
					if cloud == "digitalocean" {
						wantWrites = 3
					}
				case "changed", "read-denied":
					want = "failed"
				case "partial-failed":
					want = "uncertain"
					wantWrites = 2
					if cloud == "hetzner" {
						wantWrites = 1
					}
				case "write-failed":
					want = "uncertain"
					wantWrites = 1
				case "observe-equal", "noop":
					want = "succeeded"
				}
				if out.Power.Outcome != want || writes != wantWrites || reads != 1 {
					t.Fatal("incorrect outcome or dispatch", out.Power, writes, reads)
				}
				if want == "succeeded" && !provider.TagsEqual(out.Power.Tags, after) {
					t.Fatal("missing observed tags")
				}
			})
		}
	}
}
