package consolecli

import (
	"bytes"
	"context"
	"errors"
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

type scheduleMutationService struct {
	service
	calls   int
	failure connect.Code
	want    proto.Message
}

func (s *scheduleMutationService) check(r connect.AnyRequest) error {
	s.calls++
	if !proto.Equal(r.Any().(proto.Message), s.want) || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("schedule request scope/revision/input changed", r.Any())
	}
	if s.failure != 0 {
		return connect.NewError(s.failure, errors.New("private error detail"))
	}
	return nil
}
func (s *scheduleMutationService) PreviewSchedule(_ context.Context, r *connect.Request[pb.PreviewScheduleRequest]) (*connect.Response[pb.PreviewScheduleResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PreviewScheduleResponse{Occurrences: []*pb.SchedulePreview{{Due: "2030-01-01T00:00:00Z", Local: "2030-01-01 00:00", Action: "shutdown", SkippedReason: "reason\nsecond line", MaintenanceRestrictions: "window required"}}}), nil
}
func (s *scheduleMutationService) SaveSchedule(_ context.Context, r *connect.Request[pb.SaveScheduleRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return scheduleMutationResult(), nil
}
func (s *scheduleMutationService) ApproveSchedule(_ context.Context, r *connect.Request[pb.ApproveScheduleRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return scheduleMutationResult(), nil
}
func (s *scheduleMutationService) SetScheduleEnabled(_ context.Context, r *connect.Request[pb.SetScheduleEnabledRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return scheduleMutationResult(), nil
}
func (s *scheduleMutationService) DeleteSchedule(_ context.Context, r *connect.Request[pb.DeleteScheduleRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	if err := s.check(r); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}
func scheduleMutationResult() *connect.Response[pb.ScheduleResponse] {
	return connect.NewResponse(&pb.ScheduleResponse{Schedule: &pb.Schedule{Id: "schedule", Name: "Night shift", Revision: 8, Enabled: false, IdentityEnabled: true}})
}
func TestScheduleMutationCommands(t *testing.T) {
	for _, tc := range []struct {
		command, input string
		message        proto.Message
	}{
		{"preview-schedule", `{"spec":{"timezone":"UTC","action":"shutdown","cron":"0 20 * * *"},"resourceIds":["resource"]}`, &pb.PreviewScheduleRequest{}},
		{"save-schedule", `{"name":"Night shift","spec":{"timezone":"UTC","action":"shutdown","cron":"0 20 * * *"},"resourceIds":["resource"]}`, &pb.SaveScheduleRequest{}},
		{"save-schedule", `{"id":"schedule","expectedRevision":"7","name":"Night shift","spec":{"timezone":"UTC","action":"shutdown","cron":"0 20 * * *"},"resourceIds":["resource"]}`, &pb.SaveScheduleRequest{}},
		{"approve-schedule", `{"id":"schedule","expectedRevision":"7"}`, &pb.ApproveScheduleRequest{}},
		{"set-schedule-enabled", `{"id":"schedule","expectedRevision":"7","enabled":false,"identityEnabled":true}`, &pb.SetScheduleEnabledRequest{}},
		{"set-schedule-enabled", `{"id":"schedule","expected_revision":"7","enabled":true,"identity_enabled":false}`, &pb.SetScheduleEnabledRequest{}},
		{"delete-schedule", `{"id":"schedule","expectedRevision":"7","confirmation":"Night shift"}`, &pb.DeleteScheduleRequest{}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			if err := protojson.Unmarshal([]byte(strings.Replace(tc.input, "{", `{"organizationId":"org",`, 1)), tc.message); err != nil {
				t.Fatal(err)
			}
			s := &scheduleMutationService{service: service{t: t}, want: tc.message}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			for _, failure := range []connect.Code{0, connect.CodeUnavailable, connect.CodePermissionDenied, connect.CodeFailedPrecondition} {
				s.failure = failure
				s.calls = 0
				previousLogouts := s.logouts
				var out, diagnostics bytes.Buffer
				code := Run(context.Background(), []string{tc.command, "--url", server.URL, "--org", "org", "--auth-stdin", "--input", file}, strings.NewReader(loginInput), &out, &diagnostics)
				want := 0
				switch failure {
				case connect.CodeUnavailable:
					want = 1
				case connect.CodePermissionDenied:
					want = 4
				case connect.CodeFailedPrecondition:
					want = 5
				}
				if code != want || s.calls != 1 || s.logouts != previousLogouts+1 {
					t.Fatal("retry or cleanup failure", code, diagnostics.String())
				}
				if failure != 0 && (out.Len() != 0 || strings.Contains(diagnostics.String(), "private error")) {
					t.Fatal("unsafe output")
				}
				if failure == connect.CodeUnavailable && strings.Contains(diagnostics.String(), "may have been recorded") != (tc.command != "preview-schedule") {
					t.Fatal("ambiguous outcome handling")
				}
				if failure == 0 && tc.command == "preview-schedule" && (!strings.Contains(out.String(), "window required") || strings.Contains(out.String(), "reason\nsecond line")) {
					t.Fatal("preview missing or unescaped")
				}
				if failure == 0 && tc.command != "delete-schedule" && tc.command != "preview-schedule" && (!strings.Contains(out.String(), "revision") || !strings.Contains(out.String(), "identity_enabled")) {
					t.Fatal("review fields missing")
				}
			}
		})
	}
	for _, tc := range []struct{ command, input string }{
		{"preview-schedule", `{}`}, {"save-schedule", `{"name":"x","spec":{}}`},
		{"save-schedule", `{"name":"x","spec":{},"resourceIds":["r"],"id":"s"}`},
		{"save-schedule", `{"name":"x","spec":{},"resourceIds":["r"],"expectedRevision":2}`},
		{"approve-schedule", `{"id":"s"}`}, {"approve-schedule", `{"id":"s","expectedRevision":1,"organizationId":"other"}`},
		{"set-schedule-enabled", `{"id":"s","expectedRevision":1,"enabled":false}`},
		{"set-schedule-enabled", `{"id":"s","expectedRevision":1,"enabled":null,"identityEnabled":false}`},
		{"delete-schedule", `{"id":"s","expectedRevision":1}`},
		{"delete-schedule", `{"id":"s","expectedRevision":1,"confirmation":"x","unknown":true}`},
	} {
		file := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if Run(context.Background(), []string{tc.command, "--url", "http://localhost:1", "--org", "org", "--auth-stdin", "--input", file}, strings.NewReader(loginInput), &out, &out) != 2 || !strings.Contains(out.String(), "No request sent") {
			t.Fatal("invalid schedule input reached login")
		}
	}
}
