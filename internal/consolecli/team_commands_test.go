package consolecli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type teamMutationService struct{ scheduleMutationService }

func (s *teamMutationService) SaveTeam(_ context.Context, r *connect.Request[pb.SaveTeamRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) DeleteTeam(_ context.Context, r *connect.Request[pb.DeleteTeamRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}

func (s *teamMutationService) SaveRole(_ context.Context, r *connect.Request[pb.SaveRoleRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) UpdateMember(_ context.Context, r *connect.Request[pb.UpdateMemberRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) SetNotificationSubscriptions(_ context.Context, r *connect.Request[pb.SetNotificationSubscriptionsRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) SetNotificationGrouping(_ context.Context, r *connect.Request[pb.SetNotificationGroupingRequest]) (*connect.Response[pb.NotificationGrouping], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.NotificationGrouping{}), nil
}
func (s *teamMutationService) DeleteNotificationDestination(_ context.Context, r *connect.Request[pb.DeleteNotificationDestinationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) SetNotificationDestinationEnabled(_ context.Context, r *connect.Request[pb.SetNotificationDestinationEnabledRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) SaveNotificationDestination(_ context.Context, r *connect.Request[pb.SaveNotificationDestinationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) SetOrganizationSmtp(_ context.Context, r *connect.Request[pb.SetOrganizationSmtpRequest]) (*connect.Response[pb.OrganizationSmtp], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.OrganizationSmtp{}), nil
}
func (s *teamMutationService) SendNotificationTest(_ context.Context, r *connect.Request[pb.SendNotificationTestRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) VerifyNotificationDestination(_ context.Context, r *connect.Request[pb.VerifyNotificationDestinationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) RedeliverNotification(_ context.Context, r *connect.Request[pb.RedeliverNotificationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func (s *teamMutationService) CreateAutomationProject(_ context.Context, r *connect.Request[pb.CreateAutomationProjectRequest]) (*connect.Response[pb.AutomationProject], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AutomationProject{}), nil
}
func (s *teamMutationService) RequestAutomationValidation(_ context.Context, r *connect.Request[pb.RequestAutomationValidationRequest]) (*connect.Response[pb.AutomationValidation], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AutomationValidation{}), nil
}
func (s *teamMutationService) CancelAutomationValidation(_ context.Context, r *connect.Request[pb.CancelAutomationValidationRequest]) (*connect.Response[pb.AutomationValidation], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AutomationValidation{}), nil
}
func (s *teamMutationService) ImportAutomationSource(_ context.Context, r *connect.Request[pb.ImportAutomationSourceRequest]) (*connect.Response[pb.AutomationSource], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AutomationSource{}), nil
}
func (s *teamMutationService) PublishAutomationVersion(_ context.Context, r *connect.Request[pb.PublishAutomationVersionRequest]) (*connect.Response[pb.AutomationVersion], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AutomationVersion{}), nil
}
func (s *teamMutationService) SetAutomationVersionStatus(_ context.Context, r *connect.Request[pb.SetAutomationVersionStatusRequest]) (*connect.Response[pb.AutomationVersion], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AutomationVersion{}), nil
}
func (s *teamMutationService) PublishServerTemplate(_ context.Context, r *connect.Request[pb.PublishServerTemplateRequest]) (*connect.Response[pb.ServerTemplate], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.ServerTemplate{Id: "template", Creation: r.Msg.Creation}), nil
}
func (s *teamMutationService) SetServerTemplateStatus(_ context.Context, r *connect.Request[pb.SetServerTemplateStatusRequest]) (*connect.Response[pb.ServerTemplate], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.ServerTemplate{Id: "template", Status: r.Msg.Status}), nil
}
func TestTeamMutationCommands(t *testing.T) {
	for _, tc := range []struct {
		command, input string
		message        proto.Message
	}{
		{"publish-server-template", `{"name":"Web","connectionId":"connection","region":"fsn1","creation":{"image":"ubuntu","size":"cx23","sshKey":"123"}}`, &pb.PublishServerTemplateRequest{}},
		{"set-server-template-status", `{"id":"template","status":"retired"}`, &pb.SetServerTemplateStatusRequest{}},
		{"set-server-template-status", `{"id":"template","status":"revoked"}`, &pb.SetServerTemplateStatusRequest{}},
		{"import-automation-source", `{"name":"Source","runtime":"opentofu","entrypoint":"main.tf"}`, &pb.ImportAutomationSourceRequest{}},
		{"publish-automation-version", `{"name":"Version","sourceId":"source","runtimeImage":"digest","connectionId":"connection","region":"global","reviewNote":"Reviewed","expectedVersion":1}`, &pb.PublishAutomationVersionRequest{}},
		{"set-automation-version-status", `{"id":"version","status":"retired"}`, &pb.SetAutomationVersionStatusRequest{}},
		{"set-automation-version-status", `{"id":"version","status":"revoked"}`, &pb.SetAutomationVersionStatusRequest{}},
		{"create-automation-project", `{"name":"Project","versionId":"version","inputsJson":"{}"}`, &pb.CreateAutomationProjectRequest{}},
		{"request-validation", `{"versionId":"version"}`, &pb.RequestAutomationValidationRequest{}},
		{"request-validation", `{"versionId":"version","projectId":"project"}`, &pb.RequestAutomationValidationRequest{}},
		{"cancel-validation", `{"id":"validation"}`, &pb.CancelAutomationValidationRequest{}},
		{"save-destination", `{"name":"Hook","kind":"webhook","endpoint":"https://example.com/hook","signingSecret":"private-test-secret"}`, &pb.SaveNotificationDestinationRequest{}},
		{"save-destination", `{"id":"destination","expectedRevision":"2","name":"Mail","kind":"email","endpoint":"ops@example.com","usePlatformSmtp":true}`, &pb.SaveNotificationDestinationRequest{}},
		{"save-destination", `{"name":"Mail","kind":"email","endpoint":"ops@example.com","useOrganizationSmtp":true,"organizationSmtpRevision":"2"}`, &pb.SaveNotificationDestinationRequest{}},
		{"set-organization-smtp", `{"expectedRevision":"2","remove":false,"smtp":{"host":"smtp.example.com","port":587,"sender":"ops@example.com","password":"private-test-secret"}}`, &pb.SetOrganizationSmtpRequest{}},
		{"set-organization-smtp", `{"expectedRevision":"2","remove":true}`, &pb.SetOrganizationSmtpRequest{}},
		{"send-notification-test", `{"id":"destination","verification":true}`, &pb.SendNotificationTestRequest{}},
		{"send-notification-test", `{"id":"destination","verification":false}`, &pb.SendNotificationTestRequest{}},
		{"verify-destination", `{"id":"destination","code":"private-test-secret","senderCode":"sender-code"}`, &pb.VerifyNotificationDestinationRequest{}},
		{"redeliver-notification", `{"id":"delivery"}`, &pb.RedeliverNotificationRequest{}},
		{"set-destination-enabled", `{"id":"destination","expectedRevision":"2","enabled":false}`, &pb.SetNotificationDestinationEnabledRequest{}},
		{"set-destination-enabled", `{"id":"destination","expectedRevision":"2","enabled":true}`, &pb.SetNotificationDestinationEnabledRequest{}},
		{"set-notification-subscriptions", `{"id":"destination","expectedRevision":"2","eventTypes":["operation.failed"]}`, &pb.SetNotificationSubscriptionsRequest{}},
		{"set-notification-subscriptions", `{"id":"destination","expectedRevision":"2","event_types":[]}`, &pb.SetNotificationSubscriptionsRequest{}},
		{"set-notification-grouping", `{"seconds":0,"expectedRevision":"2"}`, &pb.SetNotificationGroupingRequest{}},
		{"set-notification-grouping", `{"seconds":300,"expectedRevision":"2"}`, &pb.SetNotificationGroupingRequest{}},
		{"delete-destination", `{"id":"destination","confirmation":"Production mail","expectedRevision":"2"}`, &pb.DeleteNotificationDestinationRequest{}},
		{"save-role", `{"name":"Reader","permissions":["resources.read"]}`, &pb.SaveRoleRequest{}},
		{"save-role", `{"id":"role","expectedRevision":"1","name":"Reader","permissions":["resources.read","connections.read"]}`, &pb.SaveRoleRequest{}},
		{"update-member", `{"userId":"user","roleId":"role","active":false,"expectedRevision":"1"}`, &pb.UpdateMemberRequest{}},
		{"update-member", `{"user_id":"user","role_id":"role","active":true,"expected_revision":"1"}`, &pb.UpdateMemberRequest{}},

		{"save-team", `{"name":"Ops","roleId":"role","active":true,"userIds":["user"]}`, &pb.SaveTeamRequest{}},
		{"save-team", `{"id":"team","expectedRevision":"2","name":"Ops","roleId":"role","active":false,"user_ids":[]}`, &pb.SaveTeamRequest{}},
		{"delete-team", `{"id":"team","expectedRevision":"2"}`, &pb.DeleteTeamRequest{}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			if err := protojson.Unmarshal([]byte(strings.Replace(tc.input, "{", `{"organizationId":"org",`, 1)), tc.message); err != nil {
				t.Fatal(err)
			}
			archivePath := ""
			if request, ok := tc.message.(*pb.ImportAutomationSourceRequest); ok {
				archivePath = filepath.Join(t.TempDir(), "source.zip")
				request.Archive = []byte(strings.Repeat("archive-byte", 10000))
				if err := os.WriteFile(archivePath, request.Archive, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := &teamMutationService{scheduleMutationService{service: service{t: t}, want: tc.message}}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			for _, failure := range []connect.Code{0, connect.CodeUnavailable, connect.CodePermissionDenied, connect.CodeFailedPrecondition} {
				s.failure = failure
				s.calls = 0
				logouts := s.logouts
				var out, diagnostics bytes.Buffer
				args := []string{tc.command, "--url", server.URL, "--org", "org", "--auth-stdin", "--input", file, "--json"}
				if archivePath != "" {
					args = append(args, "--archive", archivePath)
				}
				code := Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
				want := map[connect.Code]int{0: 0, connect.CodeUnavailable: 1, connect.CodePermissionDenied: 4, connect.CodeFailedPrecondition: 5}[failure]
				if code != want || s.calls != 1 || s.logouts != logouts+1 {
					t.Fatal("dispatch, retry or cleanup", code, diagnostics.String())
				}
				if strings.Contains(out.String()+diagnostics.String(), "private-test-secret") {
					t.Fatal("input secret leaked")
				}
				if failure != 0 && (out.Len() != 0 || strings.Contains(diagnostics.String(), "private error")) {
					t.Fatal("unsafe output")
				}
				if (failure == connect.CodeUnavailable) != strings.Contains(diagnostics.String(), "may have been recorded") {
					t.Fatal("ambiguity guidance")
				}
			}
		})
	}
	for _, tc := range []struct{ command, input string }{
		{"publish-server-template", `{"name":"Web","connectionId":"connection","region":"fsn1"}`},
		{"set-server-template-status", `{"id":"template","status":"published"}`},
		{"import-automation-source", `{"name":"Source","runtime":"opentofu","entrypoint":"main.tf","archive":"eA=="}`},
		{"import-automation-source", `{"name":"Source","runtime":"unsupported","entrypoint":"main.tf"}`},
		{"publish-automation-version", `{"name":"Version"}`},
		{"set-automation-version-status", `{"id":"version","status":"published"}`},
		{"create-automation-project", `{"name":"Project","versionId":"version"}`},
		{"create-automation-project", `{"name":"Project","versionId":"version","inputsJson":"null"}`},
		{"create-automation-project", `{"name":"Project","versionId":"version","inputsJson":"[]"}`},
		{"request-validation", `{"projectId":"project"}`},
		{"cancel-validation", `{}`},
		{"save-destination", `{"name":"Hook","kind":"webhook","endpoint":"https://example.com"}`},
		{"save-destination", `{"id":"d","name":"Hook","kind":"webhook","endpoint":"https://example.com","signingSecret":"secret"}`},
		{"save-destination", `{"name":"Mail","kind":"email","endpoint":"ops@example.com","usePlatformSmtp":true,"useOrganizationSmtp":true}`},
		{"set-organization-smtp", `{"expectedRevision":"2","remove":null}`},
		{"set-organization-smtp", `{"expectedRevision":"2","remove":false}`},
		{"set-organization-smtp", `{"expectedRevision":"2","remove":true,"smtp":{}}`},
		{"send-notification-test", `{"id":"destination"}`},
		{"send-notification-test", `{"id":"destination","verification":null}`},
		{"verify-destination", `{"id":"destination"}`},
		{"redeliver-notification", `{}`},
		{"set-destination-enabled", `{"id":"destination","expectedRevision":"2"}`},
		{"set-destination-enabled", `{"id":"destination","expectedRevision":"2","enabled":null}`},
		{"set-destination-enabled", `{"id":"destination","enabled":false}`},
		{"set-notification-subscriptions", `{"id":"destination","expectedRevision":"2"}`},
		{"set-notification-subscriptions", `{"id":"destination","expectedRevision":"2","eventTypes":null}`},
		{"set-notification-subscriptions", `{"id":"destination","eventTypes":[]}`},
		{"set-notification-grouping", `{"expectedRevision":"2"}`},
		{"set-notification-grouping", `{"seconds":null,"expectedRevision":"2"}`},
		{"set-notification-grouping", `{"seconds":1,"expectedRevision":"2"}`},
		{"set-notification-grouping", `{"seconds":300}`},
		{"delete-destination", `{"id":"destination","expectedRevision":"2"}`},
		{"delete-destination", `{"id":"destination","confirmation":"Production mail"}`},
		{"save-role", `{"name":"Empty","permissions":[]}`},
		{"save-role", `{"name":"Reader","permissions":["resources.read"],"organizationId":"other"}`},
		{"update-member", `{"userId":"user","roleId":"role"}`},
		{"update-member", `{"userId":"user","roleId":"role","active":null}`},
		{"update-member", `{"userId":"user","active":false}`},

		{"save-team", `{"name":"Ops","roleId":"role","userIds":[]}`},
		{"save-team", `{"name":"Ops","roleId":"role","active":false}`},
		{"save-team", `{"name":"Ops","roleId":"role","active":false,"userIds":null}`},
		{"save-team", `{"id":"team","name":"Ops","roleId":"role","active":false,"userIds":[]}`},
		{"delete-team", `{"id":"team"}`},
	} {
		file := filepath.Join(t.TempDir(), "input.json")
		if e := os.WriteFile(file, []byte(tc.input), 0600); e != nil {
			t.Fatal(e)
		}
		var out, diagnostics bytes.Buffer
		if code := Run(context.Background(), []string{tc.command, "--url", "http://127.0.0.1:1", "--org", "org", "--auth-stdin", "--input", file}, strings.NewReader(loginInput), &out, &diagnostics); code != 2 {
			t.Fatal(tc, code, diagnostics.String())
		}
	}
}

func (s *teamMutationService) ListTeams(_ context.Context, r *connect.Request[pb.ListTeamsRequest]) (*connect.Response[pb.ListTeamsResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.ListTeamsResponse{Teams: []*pb.Team{{Id: "team", Name: "Ops", RoleId: "role", Active: true, Revision: 2, UserIds: []string{"user"}}}}), nil
}
func (s *teamMutationService) ListAccess(_ context.Context, r *connect.Request[pb.ListAccessRequest]) (*connect.Response[pb.ListAccessResponse], error) {
	if e := s.check(r); e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.ListAccessResponse{Roles: []*pb.Role{{Id: "role", Name: "Ops", Permissions: []string{"resources.read"}}}, Members: []*pb.Member{{UserId: "user", Email: "member@example.com", RoleId: "role", Active: true}}, Invitations: []*pb.Invitation{{Id: "invite", Email: "pending@example.com"}}, PermissionCatalog: []string{"resources.read", "unsafe\nline"}}), nil
}
func TestTeamAccessReads(t *testing.T) {
	for _, command := range []string{"teams", "access"} {
		s := &teamMutationService{scheduleMutationService{service: service{t: t}}}
		if command == "teams" {
			s.want = &pb.ListTeamsRequest{OrganizationId: "org"}
		} else {
			s.want = &pb.ListAccessRequest{OrganizationId: "org"}
		}
		path, h := providahv1connect.NewConsoleServiceHandler(s)
		mux := http.NewServeMux()
		mux.Handle("/api"+path, http.StripPrefix("/api", h))
		server := httptest.NewServer(mux)
		defer server.Close()
		s.origin = server.URL
		for _, jsonOutput := range []bool{false, true} {
			for _, failure := range []connect.Code{0, connect.CodePermissionDenied} {
				s.failure = failure
				s.calls = 0
				logouts := s.logouts
				var out, diagnostics bytes.Buffer
				args := []string{command, "--url", server.URL, "--org", "org", "--auth-stdin"}
				if jsonOutput {
					args = append(args, "--json")
				}
				code := Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
				want := 0
				if failure != 0 {
					want = 4
				}
				if code != want || s.calls != 1 || s.logouts != logouts+1 {
					t.Fatal(code, diagnostics.String())
				}
				if failure != 0 {
					if out.Len() != 0 {
						t.Fatal("partial result")
					}
					continue
				}
				fields := []string{"role", "user", "2"}
				if command == "access" {
					fields = []string{"roles", "members", "invitations", "permission", "member@example.com", "pending@example.com", "resources.read"}
				}
				for _, field := range fields {
					if !strings.Contains(out.String(), field) {
						t.Fatal("missing field", field, out.String())
					}
				}
				if strings.Contains(out.String(), "unsafe\nline") {
					t.Fatal("unescaped output")
				}
			}
		}
	}
}

func TestPrivateNotificationInput(t *testing.T) {
	for _, message := range []proto.Message{&pb.CreateAutomationProjectRequest{}, &pb.SaveNotificationDestinationRequest{}, &pb.SetOrganizationSmtpRequest{}, &pb.VerifyNotificationDestinationRequest{}} {
		file := filepath.Join(t.TempDir(), "input.json")
		if err := os.WriteFile(file, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := commandInput(file, "org", message); err == nil || !strings.Contains(err.Error(), "bounded regular file") {
			t.Fatal("nonprivate secret input accepted", err)
		}
	}
}

func TestArchiveFileBounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(file, []byte("archive"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := boundedInputFile(file, 4<<20, true); err == nil {
		t.Fatal("nonprivate archive accepted")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, (4<<20)+1); err != nil {
		t.Fatal(err)
	}
	if _, err := boundedInputFile(file, 4<<20, true); err == nil {
		t.Fatal("oversized archive accepted")
	}
}
