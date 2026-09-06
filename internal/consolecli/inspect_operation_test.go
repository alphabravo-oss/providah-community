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

type inspectService struct {
	service
	mode  string
	calls int
}

func (s *inspectService) GetOperation(_ context.Context, r *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	s.calls++
	if r.Msg.Id != "op" || r.Msg.OrganizationId != "org" || r.Header().Get("Origin") != s.origin || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("operation scope/session lost")
	}
	if s.mode == "denied" || s.mode == "revoked" && s.calls > 1 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("private detail"))
	}
	status := pb.OperationStatus_OPERATION_STATUS_QUEUED
	if s.mode == "complete" && s.calls > 1 {
		status = pb.OperationStatus_OPERATION_STATUS_SUCCEEDED
	}
	if s.mode == "uncertain" {
		status = pb.OperationStatus_OPERATION_STATUS_UNCERTAIN
	}
	if s.mode == "failed" {
		status = pb.OperationStatus_OPERATION_STATUS_FAILED
	}
	result := &pb.Operation{Id: "op", Status: status}
	if s.mode == "wrong" {
		result.Id = "another"
	}
	return connect.NewResponse(&pb.OperationResponse{Operation: result}), nil
}
func TestInspectOperation(t *testing.T) {
	for _, mode := range []string{"lookup", "complete", "uncertain", "failed", "denied", "revoked", "timeout", "wrong"} {
		t.Run(mode, func(t *testing.T) {
			s := &inspectService{service: service{t: t}, mode: mode}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			args := []string{"operation", "--url", server.URL, "--org", "org", "--id", "op", "--auth-stdin", "--json"}
			if mode != "lookup" {
				duration := "5s"
				if mode == "timeout" {
					duration = "50ms"
				}
				args = append(args, "--wait", duration)
			}
			var out, diagnostics bytes.Buffer
			got := Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
			want := 0
			if mode == "denied" || mode == "revoked" {
				want = 4
			}
			if mode == "timeout" || mode == "wrong" {
				want = 1
			}
			if got != want {
				t.Fatal("unexpected exit", got, diagnostics.String())
			}
			calls := 1
			if mode == "complete" || mode == "revoked" {
				calls = 2
			}
			if s.calls != calls || s.logins != 1 || s.logouts != 1 {
				t.Fatal("read/retry/session count", s.calls, s.logins, s.logouts)
			}
			if want != 0 && out.Len() != 0 {
				t.Fatal("stale result after failed wait")
			}
			if mode == "complete" && !strings.Contains(out.String(), "SUCCEEDED") {
				t.Fatal("missing final result")
			}
			if strings.Contains(out.String()+diagnostics.String(), "private") {
				t.Fatal("private error or credential leak")
			}
		})
	}
	for _, flags := range [][]string{{"--wait", "-1s", "--id", "op"}, {"--wait", "6m", "--id", "op"}, {"--wait", "1s"}, {"--all", "--id", "op"}} {
		var output bytes.Buffer
		args := append([]string{"operation", "--url", "http://localhost:1", "--org", "org", "--auth-stdin"}, flags...)
		if Run(context.Background(), args, strings.NewReader(loginInput), &output, &output) != 2 {
			t.Fatal("invalid flags accepted")
		}
	}
}
