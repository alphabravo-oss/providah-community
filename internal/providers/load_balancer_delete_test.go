package providers

import (
	"context"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLoadBalancerDeletion(t *testing.T) {
	for _, cloud := range []string{"digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			id, region, path, host := "12345678-1234-1234-1234-123456789abc", "nyc3", "/v2/load_balancers/", "api.digitalocean.com"
			status := "active"
			fixture := `{"load_balancer":{"id":"12345678-1234-1234-1234-123456789abc","name":"edge","region":{"slug":"nyc3"},"status":"active","created_at":"2026-09-01T00:00:00Z","droplet_ids":[789],"tag":"production","tags":["api"],"forwarding_rules":[{"entry_protocol":"https","entry_port":443,"target_port":8080,"certificate_id":"certificate-id"}]}}`
			if cloud == "hetzner" {
				id, region, path, host, status = "123", "fsn1", "/v1/load_balancers/", "api.hetzner.cloud", "present"
				fixture = `{"load_balancer":{"id":123,"name":"edge","created":"2026-09-01T00:00:00Z","location":{"name":"fsn1"},"protection":{"delete":false},"services":[{"protocol":"https","listen_port":443,"destination_port":8080,"http":{"certificates":[1234]},"health_check":{"http":{"response":"secret-health-match"}}}],"targets":[{"type":"label_selector","label_selector":{"selector":"env=production"},"targets":[{"type":"server","server":{"id":789}}]}]}}`
			}
			mode := ""
			writes := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != host || r.URL.Path != path+id || r.Header.Get("Authorization") != "Bearer fake-token" {
					t.Fatal("wrong target or credentials", r.URL)
				}
				body, code := fixture, 200
				if r.Method == "DELETE" {
					writes++
					code, body = 204, ""
					if mode == "lost" {
						code = 500
					}
					if mode == "bad-ack" {
						code = 200
					}
				} else if r.Method != "GET" {
					t.Fatal("unexpected mutation", r.Method)
				}
				switch mode {
				case "changed":
					body = strings.ReplaceAll(body, "789", "987")
				case "service":
					body = strings.ReplaceAll(body, "8080", "9090")
				case "created":
					body = strings.ReplaceAll(body, "2026-09-01", "2026-09-02")
				case "protected":
					body = strings.ReplaceAll(body, `"delete":false`, `"delete":true`)
				case "wrong-region":
					body = strings.ReplaceAll(body, region, "elsewhere")
				case "missing":
					code, body = 404, `{"error":{"code":"not_found","message":"missing"},"id":"not_found","message":"missing"}`
				case "denied":
					code, body = 403, `{"error":{"code":"forbidden","message":"denied"},"id":"forbidden","message":"denied"}`
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			req := provider.Request{Provider: cloud, Region: region, Credential: "fake-token", Power: &provider.PowerRequest{ResourceKind: "network.load_balancer", OperationID: strings.Repeat("a", 64), Phase: "preview", Action: "delete", NativeID: id, ExpectedStatus: status}}
			run := func() *provider.PowerResult {
				if e := req.Power.Validate(cloud); e != nil {
					t.Fatal(e)
				}
				return deleteLoadBalancer(context.Background(), req, client)
			}
			preview := run()
			if preview.Outcome != "preview" || writes != 0 || !strings.Contains(preview.DeletionImpact, "789") || !strings.Contains(preview.DeletionImpact, "443") || strings.Contains(preview.DeletionImpact, "secret-health-match") {
				t.Fatal("incorrect review", preview)
			}
			req.Power.Phase, req.Power.DeletionImpact = "submit", preview.DeletionImpact
			modes := []string{"changed", "service", "created", "wrong-region", "missing", "denied"}
			if cloud == "hetzner" {
				modes = append(modes, "protected")
			}
			for _, v := range modes {
				mode = v
				if run().Outcome != "failed" || writes != 0 {
					t.Fatal("unsafe submit", v)
				}
			}
			mode = ""
			if run().Outcome != "accepted" || writes != 1 {
				t.Fatal("valid deletion failed")
			}
			for _, v := range []string{"lost", "bad-ack"} {
				mode = v
				before := writes
				if run().Outcome != "uncertain" || writes != before+1 {
					t.Fatal("ambiguous write retried or accepted")
				}
			}
			if cloud == "digitalocean" {
				fixture = strings.ReplaceAll(fixture, `"region":{"slug":"nyc3"}`, `"type":"GLOBAL","target_load_balancer_ids":["child-lb"],"domains":[{"name":"example.test","certificate_id":"global-cert"}]`)
				req.Region = "global"
				req.Power.Phase = "preview"
				mode = ""
				global := run()
				if global.Outcome != "preview" || !strings.Contains(global.DeletionImpact, "child-lb") || !strings.Contains(global.DeletionImpact, "example.test") {
					t.Fatal("global routing missing", global)
				}
				req.Power.Phase = "submit"
				req.Power.DeletionImpact = global.DeletionImpact
				if run().Outcome != "accepted" {
					t.Fatal("global delete failed")
				}
			}
			req.Power.Phase = "observe"
			mode = ""
			if run().Outcome != "accepted" {
				t.Fatal("present resource marked absent")
			}
			mode = "missing"
			if run().Outcome != "succeeded" {
				t.Fatal("absence not confirmed")
			}
			mode = "denied"
			if run().Outcome == "succeeded" {
				t.Fatal("denial treated as absence")
			}
		})
	}
}
