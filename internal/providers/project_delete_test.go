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

func TestCloudProjectDeletion(t *testing.T) {
	const id = "506f78a4-e098-11e5-ad9f-000f53306ae1"
	mode, writes := "", 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			t.Fatal("missing authorization")
		}
		body, code := "", 200
		switch r.URL.Path {
		case "/v2/projects/" + id:
			if r.Method == "DELETE" {
				writes++
				code = 204
				if mode == "bad-ack" {
					code = 200
				}
				if mode == "lost" {
					code = 500
					body = `{"message":"private failure"}`
				}
			} else {
				if r.Method != "GET" {
					t.Fatal("unexpected mutation")
				}
				name := "Empty project"
				if mode == "changed" {
					name = "Changed project"
				}
				projectID := id
				if mode == "wrong" {
					projectID = "wrong"
				}
				body = fmt.Sprintf(`{"project":{"id":%q,"name":%q,"is_default":%t,"created_at":"2026-01-01","updated_at":"2026-01-02"}}`, projectID, name, mode == "default")
				if mode == "missing" {
					code = 404
					body = `{"message":"not found"}`
				}
				if mode == "denied" {
					code = 403
					body = `{"message":"private error"}`
				}
			}
		case "/v2/projects/default":
			projectID := "another-project"
			if mode == "default-changed" {
				projectID = id
			}
			body = fmt.Sprintf(`{"project":{"id":%q}}`, projectID)
		case "/v2/projects/" + id + "/resources":
			if r.Method != "GET" || r.URL.Query().Get("per_page") != "200" {
				t.Fatal("unexpected resources request")
			}
			body = `{"resources":[],"meta":{"total":0}}`
			if mode == "occupied" {
				body = `{"resources":[{"urn":"do:droplet:1"}]}`
			}
			if mode == "more" {
				body = `{"resources":[],"links":{"pages":{"next":"https://api.digitalocean.com/v2/projects/x/resources?page=2"}}}`
			}
			if mode == "count" {
				body = `{"resources":[],"meta":{"total":1}}`
			}
			if mode == "resources-denied" {
				code = 403
				body = `{"message":"private error"}`
			}
		default:
			t.Fatal("unexpected provider endpoint", r.URL.Path)
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	req := provider.Request{Provider: "digitalocean", Region: "global", Credential: "fake-token", Power: &provider.PowerRequest{ResourceKind: "organization.project", NativeID: id, Action: "delete", Phase: "preview", ExpectedStatus: "present", OperationID: strings.Repeat("a", 64)}}
	run := func() *provider.PowerResult {
		if err := req.Power.Validate("digitalocean"); err != nil {
			t.Fatal(err)
		}
		return deleteCloudProject(context.Background(), req, client)
	}
	preview := run()
	if preview.Outcome != "preview" || writes != 0 || !strings.Contains(preview.DeletionImpact, "Empty project") {
		t.Fatal("missing review", preview)
	}
	req.Power.Phase, req.Power.DeletionImpact = "submit", preview.DeletionImpact
	for _, m := range []string{"changed", "wrong", "default", "default-changed", "missing", "denied", "occupied", "more", "count", "resources-denied"} {
		mode = m
		if got := run(); got.Outcome != "failed" || writes != 0 {
			t.Fatal("unsafe deletion", m, got)
		}
	}
	for _, m := range []string{"", "lost", "bad-ack"} {
		mode = m
		before := writes
		want := "accepted"
		if m != "" {
			want = "uncertain"
		}
		if got := run(); got.Outcome != want || writes != before+1 {
			t.Fatal("write retried or misreported", m, got)
		}
	}
	req.Power.Phase = "observe"
	for _, m := range []string{"", "missing", "denied"} {
		mode = m
		before := writes
		want := "accepted"
		if m == "missing" {
			want = "succeeded"
		}
		if got := run(); got.Outcome != want || writes != before {
			t.Fatal("unsafe observation", m, got)
		}
	}
}
