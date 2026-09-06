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

func TestDigitalOceanAppsAndProjects(t *testing.T) {
	for _, kind := range []string{"organization.project", "application.app"} {
		for _, mode := range []string{"success", "empty", "late-error", "invalid", "cycle", "duplicate", "filtered", "pending"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					collection := "apps"
					if kind == "organization.project" {
						collection = "projects"
					}
					if r.Method != "GET" || r.URL.Path != "/v2/"+collection || r.Header.Get("Authorization") != "Bearer fake-token" || r.URL.Query().Get("per_page") != "200" {
						t.Fatal("unexpected SDK request")
					}
					page := r.URL.Query().Get("page")
					id := "first"
					if page == "2" && mode != "duplicate" {
						id = "second"
					}
					entry := fmt.Sprintf(`{"id":%q,"name":"Team project","environment":"Production","is_default":true,"description":"private-description"}`, id)
					if kind == "application.app" {
						entry = fmt.Sprintf(`{"id":%q,"spec":{"name":"web-app","envs":[{"key":"TOKEN","value":"private-token"}],"services":[{}],"workers":[{}]},"region":{"slug":"nyc"},"active_deployment":{"phase":"ACTIVE","spec":{"envs":[{"value":"private-deployment-token"}]}}}`, id)
						if mode == "pending" {
							entry = strings.TrimSuffix(entry, "}") + `,"in_progress_deployment":{"phase":"BUILDING"}}`
						}
					}
					if mode == "invalid" {
						entry = `{"id":"broken"}`
					}
					body := fmt.Sprintf(`{"%s":[%s]`, collection, entry)
					if page != "2" || mode == "cycle" {
						body += fmt.Sprintf(`,"links":{"pages":{"next":"https://api.digitalocean.com/v2/%s?page=2"}}`, collection)
					}
					body += "}"
					code := 200
					if mode == "late-error" && page == "2" {
						code = 403
						body = `{"message":"private-provider-error"}`
					}
					if mode == "empty" {
						body = fmt.Sprintf(`{"%s":[]}`, collection)
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "digitalocean", Credential: "fake-token", InventoryKinds: []string{kind}}
				if mode == "filtered" {
					req.Region = "ams3"
				}
				out := Discover(context.Background(), req, client)
				switch mode {
				case "success", "pending", "filtered", "empty":
					want := 2
					if mode == "empty" || mode == "filtered" && kind == "application.app" {
						want = 0
					}
					if !out.Complete || out.Validate() != nil || len(out.Resources) != want {
						t.Fatal("invalid inventory", out)
					}
					if mode == "pending" && kind == "application.app" && out.Resources[0].Status != "building" {
						t.Fatal("active deployment masked pending deployment")
					}
				default:
					if out.Complete || len(out.Resources) != 0 || out.Error == "" {
						t.Fatal("partial or invalid snapshot accepted", out)
					}
				}
				raw, _ := json.Marshal(out)
				if strings.Contains(string(raw), "private-") {
					t.Fatal("specification or error secret leaked")
				}
			})
		}
	}
}
