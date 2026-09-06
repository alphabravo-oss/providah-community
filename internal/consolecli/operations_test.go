package consolecli

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"errors"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f fixture@example.test"

type operationService struct {
	service
	calls  int
	method string
	fail   bool
}

func (s *operationService) result(method string, r connect.AnyRequest) (*connect.Response[pb.OperationResponse], error) {
	s.calls++
	s.method = method
	m := r.Any().(proto.Message).ProtoReflect()
	if m.Get(m.Descriptor().Fields().ByName("organization_id")).String() != "org" || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("operation authority/context lost")
	}
	if s.fail {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("private-password"))
	}
	return connect.NewResponse(&pb.OperationResponse{Operation: &pb.Operation{Id: "op", Action: "resize", ExpectedSize: "cx23", TargetSize: "cx33", Status: pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL}}), nil
}
func (s *operationService) RequestOperation(_ context.Context, r *connect.Request[pb.RequestOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	if r.Msg.IdempotencyKey != "request-key-123456" || (r.Msg.Action != "tags" && r.Msg.TargetSize != "cx33") {
		s.t.Fatal("request key/input changed")
	}
	if r.Msg.Action == "tags" && (r.Msg.ExpectedTags.GetLabels()["env"] != "dev" || r.Msg.TargetTags == nil || len(r.Msg.TargetTags.Labels) != 0 || len(r.Msg.TargetTags.Names) != 0) {
		s.t.Fatal("complete expected/empty target tags lost")
	}
	return s.result("request-operation", r)
}
func (s *operationService) RequestServerCreation(_ context.Context, r *connect.Request[pb.RequestServerCreationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.result("request-server", r)
}
func (s *operationService) RequestSSHKeyCreation(_ context.Context, r *connect.Request[pb.RequestSSHKeyCreationRequest]) (*connect.Response[pb.OperationResponse], error) {
	if r.Msg.ConnectionId != "connection" || r.Msg.Region != "global" || r.Msg.IdempotencyKey != "request-key-123456" || r.Msg.Creation == nil || r.Msg.Creation.Name != "operator-key" || r.Msg.Creation.PublicKey != testPublicKey {
		s.t.Fatal("key input changed")
	}
	result, err := s.result("request-ssh-key", r)
	if err == nil {
		result.Msg.Operation.KeyCreation = r.Msg.Creation
	}
	return result, err
}
func (s *operationService) ReviewOperation(_ context.Context, r *connect.Request[pb.ReviewOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.result("review-operation", r)
}
func (s *operationService) CancelOperation(_ context.Context, r *connect.Request[pb.CancelOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.result("cancel-operation", r)
}
func (s *operationService) ReconcileOperation(_ context.Context, r *connect.Request[pb.ReconcileOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.result("reconcile-operation", r)
}
func (s *operationService) ResolveOperation(_ context.Context, r *connect.Request[pb.ResolveOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.result("resolve-operation", r)
}
func (s *operationService) PreviewDeletion(_ context.Context, r *connect.Request[pb.PreviewDeletionRequest]) (*connect.Response[pb.PreviewDeletionResponse], error) {
	s.calls++
	s.method = "preview-deletion"
	return connect.NewResponse(&pb.PreviewDeletionResponse{Impact: "Delete exact target\nKeep snapshot", Digest: "digest"}), nil
}
func TestOperationCLI(t *testing.T) {
	for _, tc := range []struct{ command, input string }{
		{"request-operation", `{"resourceId":"resource","action":"resize","expectedStatus":"off","expectedSize":"cx23","targetSize":"cx33","reason":"CPU change","idempotencyKey":"request-key-123456"}`},
		{"request-operation", `{"resourceId":"resource","action":"tags","expectedStatus":"off","expectedTags":{"labels":{"env":"dev"}},"targetTags":{},"reason":"Clear labels","idempotencyKey":"request-key-123456"}`},
		{"request-ssh-key", `{"connectionId":"connection","region":"global","creation":{"name":"operator-key","publicKey":"` + testPublicKey + `"},"reason":"Reviewed key","idempotencyKey":"request-key-123456"}`},
		{"request-server", `{"connectionId":"connection","region":"fsn1","creation":{"name":"server","image":"11","size":"cx23","sshKey":"12"},"reason":"New capacity","idempotencyKey":"request-key-123456"}`},
		{"review-operation", `{"id":"op","approve":false,"reason":"Decline change"}`},
		{"resolve-operation", `{"id":"op","reason":"Verified external provider state"}`},
		{"cancel-operation", ""}, {"reconcile-operation", ""}, {"preview-deletion", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			s := &operationService{service: service{t: t}}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			args := []string{tc.command, "--url", server.URL, "--org", "org", "--auth-stdin"}
			if tc.input != "" {
				file := filepath.Join(t.TempDir(), "input.json")
				if e := os.WriteFile(file, []byte(tc.input), 0600); e != nil {
					t.Fatal(e)
				}
				args = append(args, "--input", file)
			} else {
				args = append(args, "--id", "op")
			}
			var out, diagnostics bytes.Buffer
			code := Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
			if code != 0 || s.calls != 1 || s.method != tc.command || s.logouts != 1 {
				t.Fatalf("operation route/cleanup failed: %d %s", code, diagnostics.String())
			}
			if tc.command == "preview-deletion" {
				if !strings.Contains(out.String(), "digest") || !strings.Contains(out.String(), `\nKeep snapshot`) {
					t.Fatal("preview missing")
				}
			} else if !strings.Contains(out.String(), "cx33") {
				t.Fatal("missing operation review fields")
			}
			if tc.command == "request-ssh-key" && !strings.Contains(out.String(), testPublicKey) {
				t.Fatal("public key omitted from review")
			}
			if tc.command == "request-operation" || tc.command == "request-ssh-key" {
				s.fail = true
				s.calls = 0
				out.Reset()
				diagnostics.Reset()
				code = Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
				if code != 1 || s.calls != 1 || !strings.Contains(diagnostics.String(), "may have been recorded") || strings.Contains(diagnostics.String(), "private-password") {
					t.Fatal("mutation retry or ambiguous outcome hidden")
				}
			}
		})
	}
	for _, input := range []string{`{"organizationId":"other"}`, `{"id":"op","reason":"Missing explicit approval"}`, `{"id":"op","approve":null,"reason":"Missing decision"}`, `{"id":"op","approve":true,"reason":"review","credential":"secret"}`, strings.Repeat("x", 65537)} {
		file := filepath.Join(t.TempDir(), "bad.json")
		_ = os.WriteFile(file, []byte(input), 0600)
		var out bytes.Buffer
		code := Run(context.Background(), []string{"review-operation", "--url", "http://127.0.0.1:1", "--org", "org", "--input", file, "--auth-stdin"}, strings.NewReader(loginInput), &out, &out)
		if code != 2 || !strings.Contains(out.String(), "No request sent") {
			t.Fatal("invalid operation input reached login")
		}
	}
}

func TestSSHKeyInputBeforeLogin(t *testing.T) {
	for _, creation := range []string{`null`, `{}`, `{"name":"operator-key","publicKey":"-----BEGIN OPENSSH PRIVATE KEY-----"}`, `{"name":"operator-key","publicKey":"private material\n` + testPublicKey + `"}`} {
		file := filepath.Join(t.TempDir(), "key.json")
		input := `{"connectionId":"connection","region":"global","creation":` + creation + `,"reason":"Import reviewed key","idempotencyKey":"request-key-123456"}`
		if err := os.WriteFile(file, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		code := Run(context.Background(), []string{"request-ssh-key", "--url", "http://127.0.0.1:1", "--org", "org", "--input", file, "--auth-stdin"}, strings.NewReader(loginInput), &out, &out)
		if code != 2 || !strings.Contains(out.String(), "No request sent") {
			t.Fatal("invalid public key reached login")
		}
	}
}

func TestTagInputPresence(t *testing.T) {
	for _, fields := range []string{
		`"action":"tags"`,
		`"action":"tags","expectedTags":{}`,
		`"action":"tags","expectedTags":{},"targetTags":null`,
		`"action":"tags","targetTags":{}`,
		`"action":"start","expectedTags":{},"targetTags":{}`,
	} {
		file := filepath.Join(t.TempDir(), "input.json")
		input := `{"resourceId":"resource","expectedStatus":"off","reason":"Test","idempotencyKey":"request-key-123456",` + fields + `}`
		if err := os.WriteFile(file, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		code := Run(context.Background(), []string{"request-operation", "--url", "http://127.0.0.1:1", "--org", "org", "--input", file, "--auth-stdin"}, strings.NewReader(loginInput), &out, &out)
		if code != 2 || !strings.Contains(out.String(), "No request sent") {
			t.Fatal("invalid tags reached login", out.String())
		}
	}
}
