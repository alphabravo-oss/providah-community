//go:build integration

package core

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/internal/launcher"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

// Opt-in test uses real restricted CLI containers and the ordinary publication/queue path.
func exerciseRealAutomation(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, ctx context.Context, org, connection string) {
	path := os.Getenv("TEST_AUTOMATION_CATALOG")
	if path == "" {
		return
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	runtimes, e := automation.ParseRuntimes(string(raw))
	if e != nil || len(runtimes) == 0 {
		t.Fatal("invalid test runtimes", e)
	}
	worker, e := launcher.NewRuntimes([]provider.Runtime{{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Version: "test", SDKVersion: "test", Protocol: provider.Protocol}})
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.ConfigureAutomation(ctx, string(raw), ""); e != nil {
		t.Fatal(e)
	}
	previous, previousCall := s.automationRuntimes, s.cfg.AutomationCall
	defer func() { s.automationRuntimes = previous; s.cfg.AutomationCall = previousCall }()
	s.automationRuntimes = runtimes
	s.cfg.AutomationCall = func(ctx context.Context, r automation.Request) (automation.Result, error) {
		body, e := json.Marshal(r)
		if e != nil {
			return automation.Result{}, e
		}
		response := httptest.NewRecorder()
		worker.ServeHTTP(response, httptest.NewRequest("POST", "/automation/validate", bytes.NewReader(body)).WithContext(ctx))
		if response.Code != 200 {
			return automation.Result{}, fmt.Errorf("worker status %d", response.Code)
		}
		var result automation.Result
		e = json.Unmarshal(response.Body.Bytes(), &result)
		return result, e
	}
	// Earlier cases intentionally exhaust this test user's validation rate limit.
	if _, e = s.pool.Exec(ctx, "UPDATE auth_limits SET window_at=now()-interval '2 minutes' WHERE key IN (SELECT 'automation-validation:'||requester_id FROM automation_validations WHERE org_id=$1)", org); e != nil {
		t.Fatal(e)
	}
	for _, runtime := range runtimes {
		entry, good, bad := "main.tf", "terraform {}\noutput \"ok\" { value = 1 }\n", "invalid {{{"
		if runtime.Runtime == "ansible" {
			entry, good, bad = "play.yml", "- hosts: all\n  tasks: []\n", "- hosts: ["
		}
		for _, tc := range []struct{ source, status string }{{good, "succeeded"}, {bad, "failed"}} {
			var archive bytes.Buffer
			z := zip.NewWriter(&archive)
			f, e := z.Create(entry)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write([]byte(tc.source)); e != nil {
				t.Fatal(e)
			}
			if e = z.Close(); e != nil {
				t.Fatal(e)
			}
			name := "Container " + runtime.Runtime + " " + tc.status
			source, e := owner.ImportAutomationSource(ctx, connect.NewRequest(&pb.ImportAutomationSourceRequest{OrganizationId: org, Name: name, Runtime: runtime.Runtime, Entrypoint: entry, Archive: archive.Bytes()}))
			if e != nil {
				t.Fatal(e)
			}
			version, e := owner.PublishAutomationVersion(ctx, connect.NewRequest(&pb.PublishAutomationVersionRequest{OrganizationId: org, Name: name, SourceId: source.Msg.Id, ConnectionId: connection, Region: "fsn1", RuntimeImage: runtime.Image, ReviewNote: "Offline fixture with no provider dependencies or cloud execution"}))
			if e != nil {
				t.Fatal(e)
			}
			queued, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(&pb.RequestAutomationValidationRequest{OrganizationId: org, VersionId: version.Msg.Id}))
			if e != nil {
				t.Fatal(e)
			}
			job, e := s.q.ClaimAutomationValidation(ctx)
			if e != nil || job.ID != queued.Msg.Id {
				t.Fatal("wrong validation job", e)
			}
			if e = s.runAutomationValidation(ctx, job); e != nil {
				t.Fatal(e)
			}
			var status string
			if e = s.pool.QueryRow(ctx, "SELECT status FROM automation_validations WHERE org_id=$1 AND id=$2", org, job.ID).Scan(&status); e != nil || status != tc.status {
				t.Fatal("real validation outcome", runtime.Runtime, status, e)
			}
		}
	}
}
