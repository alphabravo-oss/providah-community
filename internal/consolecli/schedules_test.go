package consolecli

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type scheduleService struct {
	service
	mode string
}

func (s *scheduleService) GetSchedule(_ context.Context, r *connect.Request[pb.GetScheduleRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	s.reads++
	if r.Msg.OrganizationId != "org" || r.Msg.Id != "schedule" || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("exact schedule scope lost")
	}
	return connect.NewResponse(&pb.ScheduleResponse{Schedule: &pb.Schedule{Id: "schedule", Name: "Night shift", Approved: true, NextLocal: "2030-01-01 20:00"}}), nil
}
func (s *scheduleService) ListScheduleOccurrences(_ context.Context, r *connect.Request[pb.ListScheduleOccurrencesRequest]) (*connect.Response[pb.ListScheduleOccurrencesResponse], error) {
	s.reads++
	page := ""
	if s.reads > 1 {
		page = "next"
	}
	if r.Msg.OrganizationId != "org" || r.Msg.ScheduleId != "schedule" || r.Msg.Outcome != "queued" || r.Msg.PageToken != page || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("history filter/session lost")
	}
	if s.mode == "denied" && s.reads == 2 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("private detail"))
	}
	next := ""
	if s.reads == 1 || s.mode == "cycle" {
		next = "next"
	}
	id := "2"
	if s.reads > 1 {
		id = "1"
	}
	return connect.NewResponse(&pb.ListScheduleOccurrencesResponse{Occurrences: []*pb.ScheduleOccurrence{{Id: id, ScheduleId: "schedule", Outcome: "queued", OperationId: "operation", OperationStatus: "succeeded", Detail: "safe\ntext"}}, NextPageToken: next}), nil
}
func TestScheduleCommands(t *testing.T) {
	for _, mode := range []string{"schedule", "history", "table", "denied", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			s := &scheduleService{service: service{t: t}, mode: mode}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			command := "schedule-history"
			flags := []string{"--schedule", "schedule", "--outcome", "queued", "--all"}
			if mode == "schedule" {
				command = "schedule"
				flags = []string{"--id", "schedule"}
			}
			args := append([]string{command, "--url", server.URL, "--org", "org", "--auth-stdin"}, flags...)
			if mode != "table" {
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
			if code != want || s.logins != 1 || s.logouts != 1 {
				t.Fatal("result/session", code, diagnostics.String())
			}
			if want != 0 {
				if out.Len() != 0 || s.reads != 2 {
					t.Fatal("partial output or retry")
				}
				return
			}
			if mode == "history" {
				var result struct {
					Occurrences []json.RawMessage `json:"occurrences"`
					Next        string            `json:"nextPageToken"`
				}
				if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Occurrences) != 2 || result.Next != "" {
					t.Fatal("incomplete history")
				}
			}
			if mode == "table" && (!strings.Contains(out.String(), "operation_status") || !strings.Contains(out.String(), "succeeded") || strings.Contains(out.String(), "safe\ntext")) {
				t.Fatal("missing status or unsafe table")
			}
		})
	}
	for _, args := range [][]string{{"schedule"}, {"schedule", "--id", "schedule", "--all"}, {"schedule-history", "--outcome", "failed"}} {
		var output bytes.Buffer
		args = append(args, "--url", "http://localhost:1", "--org", "org", "--auth-stdin")
		if Run(context.Background(), args, strings.NewReader(loginInput), &output, &output) != 2 {
			t.Fatal("bad command flags accepted")
		}
	}
}
