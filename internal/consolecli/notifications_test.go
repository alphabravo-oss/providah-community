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
	"google.golang.org/protobuf/proto"
)

type notificationService struct {
	service
	mode string
}

func (s *notificationService) check(r connect.AnyRequest) {
	s.reads++
	m := r.Any().(proto.Message).ProtoReflect()
	if m.Get(m.Descriptor().Fields().ByName("organization_id")).String() != "org" || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("notification scope/session lost")
	}
	if f := m.Descriptor().Fields().ByName("id"); f != nil && m.Get(f).String() != "record" {
		s.t.Fatal("notification identity lost")
	}
}
func (s *notificationService) ListNotificationDestinations(_ context.Context, r *connect.Request[pb.ListNotificationDestinationsRequest]) (*connect.Response[pb.ListNotificationDestinationsResponse], error) {
	s.check(r)
	return connect.NewResponse(&pb.ListNotificationDestinationsResponse{Destinations: []*pb.NotificationDestination{{Id: "record", Name: "Mail", Endpoint: "ops@example.test", Verified: true, EventTypes: []string{"operation.failed"}}}}), nil
}
func (s *notificationService) GetNotificationDestination(_ context.Context, r *connect.Request[pb.GetNotificationRecordRequest]) (*connect.Response[pb.NotificationDestination], error) {
	s.check(r)
	return connect.NewResponse(&pb.NotificationDestination{Id: "record", Name: "Mail", Verified: true}), nil
}
func (s *notificationService) GetNotificationDelivery(_ context.Context, r *connect.Request[pb.GetNotificationRecordRequest]) (*connect.Response[pb.NotificationDelivery], error) {
	s.check(r)
	return connect.NewResponse(&pb.NotificationDelivery{Id: "record", Status: "failed", Detail: "safe\ntext"}), nil
}
func (s *notificationService) ListNotificationAttempts(_ context.Context, r *connect.Request[pb.ListNotificationAttemptsRequest]) (*connect.Response[pb.ListNotificationAttemptsResponse], error) {
	s.check(r)
	return connect.NewResponse(&pb.ListNotificationAttemptsResponse{Attempts: []*pb.NotificationAttempt{{Id: "record", Number: 1, Status: "failed", ResponseCode: 503, Detail: "safe\ntext"}}}), nil
}
func (s *notificationService) ListNotificationDeliveries(_ context.Context, r *connect.Request[pb.ListNotificationDeliveriesRequest]) (*connect.Response[pb.ListNotificationDeliveriesResponse], error) {
	s.check(r)
	expected := ""
	if s.reads > 1 {
		expected = "next"
	}
	if r.Msg.PageToken != expected {
		s.t.Fatal("delivery cursor lost")
	}
	if s.mode == "denied" && s.reads == 2 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("private detail"))
	}
	next := ""
	if s.reads == 1 || s.mode == "cycle" {
		next = "next"
	}
	return connect.NewResponse(&pb.ListNotificationDeliveriesResponse{Deliveries: []*pb.NotificationDelivery{{Id: "record", Status: "failed", Attempts: 1, Detail: "safe\ntext"}}, NextPageToken: next}), nil
}
func (s *notificationService) GetNotificationGrouping(_ context.Context, r *connect.Request[pb.ListNotificationDestinationsRequest]) (*connect.Response[pb.NotificationGrouping], error) {
	s.check(r)
	return connect.NewResponse(&pb.NotificationGrouping{Seconds: 300, Revision: 2}), nil
}
func (s *notificationService) GetOrganizationSmtp(_ context.Context, r *connect.Request[pb.ListNotificationDestinationsRequest]) (*connect.Response[pb.OrganizationSmtp], error) {
	s.check(r)
	return connect.NewResponse(&pb.OrganizationSmtp{Configured: true, Revision: 300}), nil
}
func TestNotificationDiagnostics(t *testing.T) {
	for _, command := range []string{"destinations", "destination", "deliveries", "delivery", "delivery-attempts", "notification-grouping", "organization-smtp"} {
		for _, mode := range []string{"table", "json", "denied", "cycle"} {
			if command != "deliveries" && (mode == "denied" || mode == "cycle") {
				continue
			}
			t.Run(command+"/"+mode, func(t *testing.T) {
				s := &notificationService{service: service{t: t}, mode: mode}
				path, h := providahv1connect.NewConsoleServiceHandler(s)
				mux := http.NewServeMux()
				mux.Handle("/api"+path, http.StripPrefix("/api", h))
				server := httptest.NewServer(mux)
				defer server.Close()
				s.origin = server.URL
				args := []string{command, "--url", server.URL, "--org", "org", "--auth-stdin"}
				if command == "deliveries" {
					args = append(args, "--all")
				} else if command != "destinations" && command != "notification-grouping" && command != "organization-smtp" {
					args = append(args, "--id", "record")
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
				if code != want || s.logins != 1 || s.logouts != 1 {
					t.Fatal("result/session", code, diagnostics.String())
				}
				if want != 0 {
					if out.Len() != 0 || s.reads != 2 {
						t.Fatal("partial output or unexpected retry")
					}
					return
				}
				expected := "record"
				if command == "notification-grouping" || command == "organization-smtp" {
					expected = "300"
				}
				if !strings.Contains(out.String(), expected) || strings.Contains(out.String(), "safe\ntext") {
					t.Fatal("missing record or unsafe table")
				}
				if command == "deliveries" && (s.reads != 2 || strings.Count(out.String(), "record") != 2 || strings.Contains(out.String(), "nextPageToken")) {
					t.Fatal("incomplete pagination")
				}
				if command == "delivery-attempts" && !strings.Contains(out.String(), "503") {
					t.Fatal("missing response status")
				}
				if command == "destinations" && !strings.Contains(out.String(), "ops@example.test") {
					t.Fatal("missing destination endpoint")
				}
			})
		}
	}
	for _, args := range [][]string{{"destination"}, {"delivery"}, {"delivery-attempts"}, {"destinations", "--all"}, {"delivery", "--id", "record", "--all"}} {
		var out bytes.Buffer
		args = append(args, "--url", "http://localhost:1", "--org", "org", "--auth-stdin")
		if Run(context.Background(), args, strings.NewReader(loginInput), &out, &out) != 2 {
			t.Fatal("invalid notification flags accepted")
		}
	}
}
