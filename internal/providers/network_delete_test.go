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

func TestNetworkDeletionSDKs(t *testing.T) {
	for _, cloud := range []string{"digitalocean", "hetzner"} {
		for _, kind := range []string{"network.network", "network.firewall"} {
			t.Run(cloud+kind, func(t *testing.T) {
				id, region, status := "123", "global", "present"
				if cloud == "digitalocean" {
					id = "12345678-1234-1234-1234-123456789abc"
					if kind == "network.network" {
						region = "nyc3"
					} else {
						status = "succeeded"
					}
				}
				writes := 0
				attached, protected, peered, tagged, changed, missing, ambiguous, next := false, false, false, false, false, false, false, false
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Fatal("explicit SDK credential missing")
					}
					body, code := "", 200
					if cloud == "digitalocean" && kind == "network.network" {
						switch r.URL.Path {
						case "/v2/vpcs/" + id:
							body = fmt.Sprintf(`{"vpc":{"id":%q,"name":"unused","region":"nyc3","ip_range":"10.0.0.0/16","default":%t}}`, id, protected)
						case "/v2/vpcs/" + id + "/members":
							body = `{"members":[]}`
							if attached {
								body = `{"members":[{"urn":"do:droplet:1"}]}`
							}
							if next {
								body = `{"members":[],"links":{"pages":{"next":"https://api.digitalocean.com/v2/vpcs/other/members?page=2"}}}`
							}
						case "/v2/vpcs/" + id + "/peerings":
							body = `{"vpc_peerings":[]}`
							if peered {
								body = `{"vpc_peerings":[{"id":"peer"}]}`
							}
						default:
							t.Fatal("unexpected VPC SDK endpoint", r.URL.Path)
						}
					} else if cloud == "digitalocean" {
						if r.URL.Path != "/v2/firewalls/"+id {
							t.Fatal("unexpected firewall endpoint")
						}
						body = fmt.Sprintf(`{"firewall":{"id":%q,"name":"unused","status":"succeeded","inbound_rules":[{"protocol":"tcp","ports":"22","sources":{"addresses":["192.0.2.0/24"]}}],"outbound_rules":[],"droplet_ids":[%s],"tags":[%s],"pending_changes":[]}}`, id, map[bool]string{true: "456"}[attached], map[bool]string{true: `"managed"`}[tagged])
					} else if kind == "network.network" {
						if r.URL.Path != "/v1/networks/123" {
							t.Fatal("unexpected network endpoint")
						}
						body = fmt.Sprintf(`{"network":{"id":123,"name":"unused","ip_range":"10.0.0.0/16","servers":[%s],"load_balancers":[],"subnets":[],"routes":[],"protection":{"delete":%t},"expose_routes_to_vswitch":%t}}`, map[bool]string{true: "456"}[attached], protected, tagged)
					} else {
						if r.URL.Path != "/v1/firewalls/123" {
							t.Fatal("unexpected firewall endpoint")
						}
						target := ""
						if attached {
							target = `{"type":"server","server":{"id":456}}`
						}
						if tagged {
							target = `{"type":"label_selector","label_selector":{"selector":"role=web"}}`
						}
						body = fmt.Sprintf(`{"firewall":{"id":123,"name":"unused","rules":[{"direction":"in","protocol":"tcp","port":"22","source_ips":["192.0.2.0/24"]}],"applied_to":[%s]}}`, target)
					}
					if changed {
						body = strings.ReplaceAll(body, "unused", "renamed")
					}
					if r.Method == "DELETE" {
						writes++
						code = 204
						body = ""
						if ambiguous {
							code = 500
							body = `{"error":{"code":"server_error","message":"sensitive"}}`
						}
					}
					if missing {
						code = 404
						body = `{"error":{"code":"not_found","message":"gone"}}`
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				req := provider.Request{Version: provider.Protocol, Provider: cloud, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Region: region, Credential: "test-token", Power: &provider.PowerRequest{ResourceKind: kind, OperationID: strings.Repeat("c", 64), Action: "delete", Phase: "preview", NativeID: id, ExpectedStatus: status}}
				run := func() *provider.PowerResult {
					out := Power(context.Background(), req, client)
					if out.Validate() != nil || out.Power == nil {
						t.Fatal("invalid network result")
					}
					return out.Power
				}
				preview := run()
				if preview.Outcome != "preview" || writes != 0 {
					t.Fatal("network preview failed", preview)
				}
				req.Power.Phase = "submit"
				req.Power.DeletionImpact = preview.DeletionImpact
				attached = true
				if run().Outcome != "failed" || writes != 0 {
					t.Fatal("attached resource deleted")
				}
				attached = false
				if kind == "network.network" {
					protected = true
					if run().Outcome != "failed" {
						t.Fatal("protected/default network deleted")
					}
					protected = false
				}
				if cloud == "digitalocean" && kind == "network.network" {
					peered = true
					if run().Outcome != "failed" {
						t.Fatal("peered VPC deleted")
					}
					peered = false
					next = true
					if run().Outcome != "failed" {
						t.Fatal("incomplete member page accepted")
					}
					next = false
				} else {
					tagged = true
					if run().Outcome != "failed" {
						t.Fatal("selector or vSwitch network deleted")
					}
					tagged = false
				}
				changed = true
				if run().Outcome != "failed" {
					t.Fatal("changed review accepted")
				}
				changed = false
				if writes != 0 {
					t.Fatal("preflight performed a mutation")
				}
				if run().Outcome != "accepted" || writes != 1 {
					t.Fatal("delete not submitted once")
				}
				ambiguous = true
				if run().Outcome != "uncertain" || writes != 2 {
					t.Fatal("ambiguous delete retried")
				}
				ambiguous = false
				req.Power.Phase = "observe"
				if run().Outcome != "accepted" || writes != 2 {
					t.Fatal("presence treated as completion")
				}
				missing = true
				if run().Outcome != "succeeded" || writes != 2 {
					t.Fatal("absence not observed")
				}
			})
		}
	}
}
