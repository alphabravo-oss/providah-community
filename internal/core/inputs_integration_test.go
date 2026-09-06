//go:build integration

package core

import (
	"archive/zip"
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"testing"
)

func exerciseProjectInputs(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, ctx context.Context, org, connection, image string) {
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	for name, content := range map[string]string{"main.tf": "terraform {}", "providah.inputs.json": `{"version":1,"inputs":[{"name":"size","label":"Server size","type":"string","choices":["small","large"]},{"name":"count","label":"Node count","type":"integer","min":1,"max":5}]}`} {
		f, _ := z.Create(name)
		_, _ = f.Write([]byte(content))
	}
	_ = z.Close()
	source, e := owner.ImportAutomationSource(ctx, connect.NewRequest(&pb.ImportAutomationSourceRequest{OrganizationId: org, Name: "Parameterized source", Runtime: "opentofu", Entrypoint: "main.tf", Archive: archive.Bytes()}))
	if e != nil || len(source.Msg.Inputs) != 2 {
		t.Fatal("input schema not imported", e)
	}
	published, e := owner.PublishAutomationVersion(ctx, connect.NewRequest(&pb.PublishAutomationVersionRequest{OrganizationId: org, Name: "Parameterized version", SourceId: source.Msg.Id, RuntimeImage: image, ConnectionId: connection, Region: "fsn1", ReviewNote: "Reviewed sizes and count bounds"}))
	if e != nil {
		t.Fatal(e)
	}
	req := &pb.CreateAutomationProjectRequest{OrganizationId: org, Name: "Constrained project", VersionId: published.Msg.Id, InputsJson: `{"count":2,"size":"small"}`}
	project, e := owner.CreateAutomationProject(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	req.InputsJson = `{ "size":"small", "count":2 }`
	again, e := owner.CreateAutomationProject(ctx, connect.NewRequest(req))
	if e != nil || again.Msg.Id != project.Msg.Id {
		t.Fatal("canonical input retry changed project", e)
	}
	req.InputsJson = `{"count":2,"size":"large"}`
	if _, e = owner.CreateAutomationProject(ctx, connect.NewRequest(req)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("project inputs mutated", e)
	}
	req.Name = "Invalid inputs"
	req.InputsJson = `{"count":20,"size":"small"}`
	if _, e = owner.CreateAutomationProject(ctx, connect.NewRequest(req)); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("input bounds bypassed", e)
	}
	saved := s.cfg.AutomationCall
	defer func() { s.cfg.AutomationCall = saved }()
	calls := 0
	s.cfg.AutomationCall = func(_ context.Context, r automation.Request) (automation.Result, error) {
		calls++
		if e := r.Validate(); e != nil {
			t.Fatal(e)
		}
		if string(r.Inputs) != `{"count":2,"size":"small"}` {
			t.Fatal("pinned inputs not dispatched")
		}
		return automation.Result{Status: "succeeded"}, nil
	}
	queued, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(&pb.RequestAutomationValidationRequest{OrganizationId: org, VersionId: published.Msg.Id, ProjectId: project.Msg.Id}))
	if e != nil || queued.Msg.ProjectId != project.Msg.Id {
		t.Fatal("project validation not queued", e)
	}
	job, e := s.q.ClaimAutomationValidation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.runAutomationValidation(ctx, job); e != nil || calls != 1 {
		t.Fatal("project validation did not run", e)
	}
	if _, e = owner.RequestAutomationValidation(ctx, connect.NewRequest(&pb.RequestAutomationValidationRequest{OrganizationId: org, VersionId: published.Msg.Id, ProjectId: "foreign"})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign project queued", e)
	}
	encoded, _ := json.Marshal(project.Msg)
	if bytes.Contains(encoded, []byte("small")) {
		t.Fatal("project values exposed")
	}
}
