package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDigitalOceanNodePools(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "denied", "invalid", "duplicate", "cycle", "filtered"} {
		t.Run(mode, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fake-token" || r.URL.Query().Get("per_page") != "200" {
					t.Fatal("unexpected SDK request")
				}
				body := `{"kubernetes_clusters":[{"id":"cluster","name":"Production","region":"nyc3","status":{"state":"running"},"endpoint":"private-endpoint"}]}`
				code := 200
				if strings.HasSuffix(r.URL.Path, "/node_pools") {
					if r.URL.Path != "/v2/kubernetes/clusters/cluster/node_pools" {
						t.Fatal("wrong cluster")
					}
					id := "pool1"
					page := r.URL.Query().Get("page")
					if page == "2" && mode != "duplicate" {
						id = "pool2"
					}
					entry := fmt.Sprintf(`{"id":%q,"name":"workers","size":"s-2vcpu-4gb","count":2,"auto_scale":true,"min_nodes":1,"max_nodes":4,"tags":["production"],"labels":{"team":"ops"},"nodes":[{"name":"private-node"}]}`, id)
					if mode == "invalid" {
						entry = strings.Replace(entry, `"count":2`, `"count":-1`, 1)
					}
					body = `{"node_pools":[` + entry + `]`
					if page != "2" || mode == "cycle" {
						body += `,"links":{"pages":{"next":"https://api.digitalocean.com/v2/kubernetes/clusters/cluster/node_pools?page=2"}}`
					}
					body += "}"
					if mode == "empty" {
						body = `{"node_pools":[]}`
					}
					if mode == "denied" && page == "2" {
						code = 403
						body = `{"message":"private-error"}`
					}
				} else if r.URL.Path != "/v2/kubernetes/clusters" {
					t.Fatal("unexpected endpoint", r.URL)
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "digitalocean", Credential: "fake-token", InventoryKinds: []string{"kubernetes.node_group"}}
			if mode == "filtered" {
				req.Region = "ams3"
			}
			out := Discover(context.Background(), req, client)
			if mode == "complete" || mode == "empty" || mode == "filtered" {
				want := 2
				if mode != "complete" {
					want = 0
				}
				if !out.Complete || out.Validate() != nil || len(out.Resources) != want {
					t.Fatal(out)
				}
				if want > 0 && (out.Resources[0].Tags.Labels["team"] != "ops" || out.Resources[0].Name != "Production / workers" || !strings.Contains(out.Resources[0].Size, "min 1 / max 4")) {
					t.Fatal("projection", out)
				}
			} else if out.Complete || len(out.Resources) != 0 || out.Error == "" {
				t.Fatal("partial inventory", out)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "private-") {
				t.Fatal("private metadata leaked")
			}
		})
	}
}
