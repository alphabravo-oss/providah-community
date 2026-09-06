package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func TestReservedIPv6Discovery(t *testing.T) {
	for _, mode := range []string{"success", "filtered", "empty", "late_error", "invalid_ip", "ipv4", "missing_region", "invalid_assignment", "duplicate", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v2/reserved_ipv6" || r.Header.Get("Authorization") != "Bearer fake-token" || r.URL.Query().Get("per_page") != "200" {
					t.Fatal("unexpected SDK request")
				}
				page := r.URL.Query().Get("page")
				ip, region, assignment := "2001:0db8:0000::1", "nyc3", `,"droplet":{"id":123}`
				if page == "2" {
					ip = "2001:db8::2"
					assignment = ""
				}
				if mode == "invalid_ip" {
					ip = "broken"
				}
				if mode == "ipv4" {
					ip = "192.0.2.1"
				}
				if mode == "missing_region" {
					region = ""
				}
				if mode == "invalid_assignment" {
					assignment = `,"droplet":{"id":0}`
				}
				if mode == "duplicate" {
					ip = "2001:db8::1"
				}
				body := fmt.Sprintf(`{"reserved_ipv6s":[{"ip":%q,"region_slug":%q%s}]`, ip, region, assignment)
				if page != "2" || mode == "cycle" {
					body += `,"links":{"pages":{"next":"https://api.digitalocean.com/v2/reserved_ipv6?page=2"}}`
				}
				body += "}"
				status := 200
				if mode == "late_error" && page == "2" {
					status = 403
					body = `{"message":"private provider error"}`
				}
				if mode == "empty" {
					body = `{"reserved_ipv6s":[]}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "digitalocean", Credential: "fake-token", InventoryKinds: []string{"network.reserved_ipv6"}}
			if mode == "filtered" {
				req.Region = "ams3"
			}
			out := Discover(context.Background(), req, client)
			switch mode {
			case "success":
				if out.Validate() != nil || len(out.Resources) != 2 || calls != 2 || out.Resources[0].NativeID != "2001:db8::1" || out.Resources[0].Status != "assigned" || out.Resources[1].Status != "unassigned" {
					t.Fatalf("incorrect IPv6 inventory: %+v", out)
				}
			case "filtered", "empty":
				if !out.Complete || len(out.Resources) != 0 {
					t.Fatalf("incorrect empty inventory: %+v", out)
				}
			default:
				if out.Complete || len(out.Resources) != 0 || out.Error == "" || strings.Contains(out.Error, "private provider") {
					t.Fatal("invalid or partial inventory accepted")
				}
			}
		})
	}
}
