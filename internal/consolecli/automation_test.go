package consolecli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

type automationService struct{ notificationService }

func (s *automationService) ListAutomationProjects(_ context.Context, r *connect.Request[pb.ListAutomationProjectsRequest]) (*connect.Response[pb.ListAutomationProjectsResponse], error) {
	s.check(r)
	return connect.NewResponse(&pb.ListAutomationProjectsResponse{Projects: []*pb.AutomationProject{{Id: "record", Name: "Project", Locked: true}}}), nil
}
func (s *automationService) GetAutomationProject(_ context.Context, r *connect.Request[pb.GetAutomationProjectRequest]) (*connect.Response[pb.AutomationProject], error) {
	s.check(r)
	return connect.NewResponse(&pb.AutomationProject{Id: "record", Name: "Project", Ownership: &pb.ProjectOwnershipSummary{Status: "partial", Unsupported: 3, ProtectedReferences: 8}}), nil
}
func (s *automationService) GetAutomationValidation(_ context.Context, r *connect.Request[pb.GetAutomationValidationRequest]) (*connect.Response[pb.AutomationValidation], error) {
	s.check(r)
	return connect.NewResponse(&pb.AutomationValidation{Id: "record", Status: "failed", Detail: "safe\ntext"}), nil
}
func (s *automationService) ListAutomationValidations(_ context.Context, r *connect.Request[pb.ListAutomationValidationsRequest]) (*connect.Response[pb.ListAutomationValidationsResponse], error) {
	s.check(r)
	if r.Msg.Status != "failed" {
		s.t.Fatal("status filter lost")
	}
	return connect.NewResponse(&pb.ListAutomationValidationsResponse{Validations: []*pb.AutomationValidation{{Id: "record", Status: "failed"}}}), nil
}
func (s *automationService) ListProjectOwnership(_ context.Context, r *connect.Request[pb.ListProjectOwnershipRequest]) (*connect.Response[pb.ListProjectOwnershipResponse], error) {
	s.check(r)
	if r.Msg.ProjectId != "record" {
		s.t.Fatal("ownership project lost")
	}
	return connect.NewResponse(&pb.ListProjectOwnershipResponse{References: []*pb.ProjectOwnershipReference{{ResourceId: "record", NativeId: "i-12345678", FirstStateId: "state", Kind: "compute.server", Conflicting: true}}}), nil
}
func (s *automationService) ListAutomationStates(_ context.Context, r *connect.Request[pb.ListAutomationStatesRequest]) (*connect.Response[pb.ListAutomationStatesResponse], error) {
	s.check(r)
	expected := ""
	if s.reads > 1 {
		expected = "10"
	}
	if r.Msg.ProjectId != "record" || r.Msg.BeforeSerial != expected {
		s.t.Fatal("state scope/cursor lost")
	}
	if s.mode == "denied" && s.reads == 2 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("private detail"))
	}
	next := ""
	if s.reads == 1 || s.mode == "cycle" {
		next = "10"
	}
	return connect.NewResponse(&pb.ListAutomationStatesResponse{States: []*pb.AutomationState{{Id: "record", Serial: int64(11 - s.reads), Sha256: "digest"}}, NextBeforeSerial: next}), nil
}
func (s *automationService) ListAutomationVersions(_ context.Context, r *connect.Request[pb.ListAutomationVersionsRequest]) (*connect.Response[pb.ListAutomationVersionsResponse], error) {
	s.check(r)
	return connect.NewResponse(&pb.ListAutomationVersionsResponse{Versions: []*pb.AutomationVersion{{Id: "record", Name: "Version", Runtime: "opentofu", RuntimeAvailable: true}}}), nil
}
func (s *automationService) GetAutomationVersion(_ context.Context, r *connect.Request[pb.GetAutomationVersionRequest]) (*connect.Response[pb.AutomationVersion], error) {
	s.check(r)
	return connect.NewResponse(&pb.AutomationVersion{Id: "record", Name: "Version", Runtime: "opentofu", RuntimeAvailable: true}), nil
}
func (s *automationService) ListAutomationSources(_ context.Context, r *connect.Request[pb.ListAutomationSourcesRequest]) (*connect.Response[pb.ListAutomationSourcesResponse], error) {
	s.check(r)
	return connect.NewResponse(&pb.ListAutomationSourcesResponse{Sources: []*pb.AutomationSource{{Id: "record", Name: "Source", Runtime: "opentofu"}}}), nil
}
func (s *automationService) GetAutomationSource(_ context.Context, r *connect.Request[pb.GetAutomationSourceRequest]) (*connect.Response[pb.AutomationSource], error) {
	s.check(r)
	return connect.NewResponse(&pb.AutomationSource{Id: "record", Name: "Source", Runtime: "opentofu", Inputs: []*pb.AutomationInput{{Name: "count", Type: "number"}}}), nil
}
func (s *automationService) ListServerTemplates(_ context.Context, r *connect.Request[pb.ListServerTemplatesRequest]) (*connect.Response[pb.ListServerTemplatesResponse], error) {
	s.check(r)
	return connect.NewResponse(&pb.ListServerTemplatesResponse{Templates: []*pb.ServerTemplate{{Id: "record", Name: "Web", Version: 2}}}), nil
}
func (s *automationService) GetServerTemplate(_ context.Context, r *connect.Request[pb.GetServerTemplateRequest]) (*connect.Response[pb.ServerTemplate], error) {
	s.check(r)
	return connect.NewResponse(&pb.ServerTemplate{Id: "record", Name: "Web", Version: 2, Creation: &pb.ServerCreate{Image: "ubuntu", Size: "cx23", SshKey: "123", Network: "safe\ntext"}}), nil
}
func TestAutomationDiagnostics(t *testing.T) {
	for _, command := range []string{"automation-projects", "automation-project", "project-ownership", "project-states", "validations", "validation", "automation-versions", "automation-version", "automation-sources", "automation-source", "server-templates", "server-template"} {
		for _, mode := range []string{"table", "json", "denied", "cycle"} {
			if command != "project-states" && (mode == "denied" || mode == "cycle") {
				continue
			}
			t.Run(command+"/"+mode, func(t *testing.T) {
				s := &automationService{notificationService{service: service{t: t}, mode: mode}}
				path, h := providahv1connect.NewConsoleServiceHandler(s)
				mux := http.NewServeMux()
				mux.Handle("/api"+path, http.StripPrefix("/api", h))
				server := httptest.NewServer(mux)
				defer server.Close()
				s.origin = server.URL
				args := []string{command, "--url", server.URL, "--org", "org", "--auth-stdin"}
				if command != "automation-projects" && command != "validations" && command != "automation-versions" && command != "automation-sources" && command != "server-templates" {
					args = append(args, "--id", "record")
				}
				if command == "validations" {
					args = append(args, "--status", "failed", "--all")
				}
				if command == "project-states" || command == "project-ownership" {
					args = append(args, "--all")
				}
				if mode == "json" {
					args = append(args, "--json")
				}
				var out, diagnostics bytes.Buffer
				code := Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
				want := 0
				if mode == "denied" {
					want = 4
				}
				if mode == "cycle" {
					want = 6
				}
				if code != want || s.logouts != 1 {
					t.Fatal("result/cleanup", code, diagnostics.String())
				}
				if want != 0 {
					if out.Len() != 0 || s.reads != 2 {
						t.Fatal("partial output or retry")
					}
					return
				}
				if !strings.Contains(out.String(), "record") || strings.Contains(out.String(), "safe\ntext") {
					t.Fatal("missing identity or unescaped output")
				}
				if command == "automation-project" && (!strings.Contains(out.String(), "partial") || !strings.Contains(out.String(), "Project")) {
					t.Fatal("project or ownership summary missing")
				}
				if command == "project-states" && (s.reads != 2 || strings.Count(out.String(), "record") != 2 || strings.Contains(out.String(), "nextBeforeSerial")) {
					t.Fatal("state pagination incomplete")
				}
				if command == "server-template" && (!strings.Contains(out.String(), "Web") || !strings.Contains(out.String(), "ubuntu") || !strings.Contains(out.String(), "cx23")) {
					t.Fatal("template identity/configuration missing")
				}
				if command == "project-ownership" && !strings.Contains(out.String(), "i-12345678") {
					t.Fatal("native ownership identity missing")
				}
			})
		}
	}
}
