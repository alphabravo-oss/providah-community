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

type scopeMutationService struct{ scheduleMutationService }

func (s *scopeMutationService) SaveInventoryView(_ context.Context, r *connect.Request[pb.SaveInventoryViewRequest]) (*connect.Response[pb.InventoryView], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.InventoryView{}), nil
}

func (s *scopeMutationService) DeleteInventoryView(_ context.Context, r *connect.Request[pb.DeleteInventoryViewRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}

func (s *scopeMutationService) SaveDashboard(_ context.Context, r *connect.Request[pb.SaveDashboardRequest]) (*connect.Response[pb.Dashboard], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Dashboard{}), nil
}

func (s *scopeMutationService) DeleteDashboard(_ context.Context, r *connect.Request[pb.DeleteDashboardRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}

func TestScopeMutationCommands(t *testing.T) {
	for _, tc := range []struct {
		command, input string
		message        proto.Message
	}{
		{"save-view", `{"name":"Production","spec":{"tagKey":"env","tagValue":"production"}}`, &pb.SaveInventoryViewRequest{}},
		{"save-view", `{"id":"view","name":"Production","expectedRevision":"2","spec":{}}`, &pb.SaveInventoryViewRequest{}},
		{"delete-view", `{"id":"view","expectedRevision":"2"}`, &pb.DeleteInventoryViewRequest{}},
		{"save-dashboard", `{"name":"Production","shared":false,"teamIds":[],"spec":{"widgets":[{"title":"Servers","filters":{"kind":"compute.server"}}]}}`, &pb.SaveDashboardRequest{}},
		{"save-dashboard", `{"id":"dashboard","expected_revision":"2","name":"Production","shared":false,"team_ids":["team"],"spec":{}}`, &pb.SaveDashboardRequest{}},
		{"delete-dashboard", `{"id":"dashboard","expectedRevision":"2"}`, &pb.DeleteDashboardRequest{}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			if err := protojson.Unmarshal([]byte(strings.Replace(tc.input, "{", `{"organizationId":"org",`, 1)), tc.message); err != nil {
				t.Fatal(err)
			}
			s := &scopeMutationService{scheduleMutationService{service: service{t: t}, want: tc.message}}
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
				code := Run(context.Background(), []string{tc.command, "--url", server.URL, "--org", "org", "--auth-stdin", "--input", file, "--json"}, strings.NewReader(loginInput), &out, &diagnostics)
				want := map[connect.Code]int{0: 0, connect.CodeUnavailable: 1, connect.CodePermissionDenied: 4, connect.CodeFailedPrecondition: 5}[failure]
				if code != want || s.calls != 1 || s.logouts != logouts+1 {
					t.Fatal("dispatch, retry or cleanup", code, diagnostics.String())
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
		{"save-view", `{"name":"x"}`},
		{"save-view", `{"name":"x","spec":{},"id":"v"}`},
		{"save-view", `{"name":"x","spec":{},"expectedRevision":1}`},
		{"save-view", `{"name":"x","spec":{},"organizationId":"other"}`},
		{"save-dashboard", `{"name":"x","spec":{},"teamIds":[]}`},
		{"save-dashboard", `{"name":"x","spec":{},"shared":false}`},
		{"save-dashboard", `{"name":"x","spec":{},"shared":false,"teamIds":null}`},
		{"delete-view", `{"id":"v"}`},
		{"delete-dashboard", `{"expectedRevision":1}`},
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
