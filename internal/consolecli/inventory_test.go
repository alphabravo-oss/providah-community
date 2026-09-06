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
	"google.golang.org/protobuf/proto"
)

const inventoryFilterJSON = `{"provider":"aws","kind":"compute.server","connectionId":"connection","region":"us-east-1","status":"running","search":"web","sortBy":"name","descending":true,"tagKey":"env","tagValue":"production","tagExists":true,"tagName":"critical","tagConditions":[{"key":"team","value":"platform"}],"tagMatchAny":true}`

func expectedFilters() *pb.InventoryViewSpec {
	return &pb.InventoryViewSpec{Provider: "aws", Kind: "compute.server", ConnectionId: "connection", Region: "us-east-1", Status: "running", Search: "web", SortBy: "name", Descending: true, TagKey: "env", TagValue: "production", TagExists: true, TagName: "critical", TagConditions: []*pb.TagCondition{{Key: "team", Value: "platform"}}, TagMatchAny: true}
}

type inventoryService struct {
	service
	denied         bool
	defaultSummary bool
}

func (s *inventoryService) ListResources(_ context.Context, r *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	s.reads++
	f := expectedFilters()
	page := ""
	if s.reads == 2 {
		page = "next"
	}
	expected := &pb.ListResourcesRequest{OrganizationId: "org", Provider: f.Provider, Kind: f.Kind, ConnectionId: f.ConnectionId, Region: f.Region, Status: f.Status, Search: f.Search, SortBy: f.SortBy, Descending: f.Descending, TagKey: f.TagKey, TagValue: f.TagValue, TagExists: f.TagExists, TagName: f.TagName, TagConditions: f.TagConditions, TagMatchAny: f.TagMatchAny, PageSize: 200, PageToken: page}
	if !proto.Equal(r.Msg, expected) {
		s.t.Fatal("filter changed across pages", r.Msg)
	}
	if s.denied && s.reads == 2 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("private"))
	}
	next := "next"
	if s.reads == 2 {
		next = ""
	}
	return connect.NewResponse(&pb.ListResourcesResponse{Resources: []*pb.Resource{{Id: page + "resource"}}, NextPageToken: next}), nil
}
func (s *inventoryService) GetResourceSummary(_ context.Context, r *connect.Request[pb.GetResourceSummaryRequest]) (*connect.Response[pb.GetResourceSummaryResponse], error) {
	s.reads++
	f, group := expectedFilters(), "status"
	if s.defaultSummary {
		f, group = &pb.InventoryViewSpec{}, "provider"
	}
	if r.Msg.OrganizationId != "org" || r.Msg.GroupBy != group || !proto.Equal(r.Msg.Filters, f) {
		s.t.Fatal("summary scope lost")
	}
	return connect.NewResponse(&pb.GetResourceSummaryResponse{Groups: []*pb.ResourceCount{{Label: "running\nunsafe", Total: 2}}, Total: 2}), nil
}
func (s *inventoryService) ListOperations(_ context.Context, r *connect.Request[pb.ListOperationsRequest]) (*connect.Response[pb.ListOperationsResponse], error) {
	s.reads++
	if r.Msg.OrganizationId != "org" || !proto.Equal(r.Msg.ResourceFilters, expectedFilters()) {
		s.t.Fatal("operation scope lost")
	}
	return connect.NewResponse(&pb.ListOperationsResponse{}), nil
}
func (s *inventoryService) ListInventoryViews(_ context.Context, r *connect.Request[pb.ListInventoryViewsRequest]) (*connect.Response[pb.ListInventoryViewsResponse], error) {
	s.reads++
	if r.Msg.OrganizationId != "org" {
		s.t.Fatal("view scope lost")
	}
	return connect.NewResponse(&pb.ListInventoryViewsResponse{Views: []*pb.InventoryView{{Name: "Production", Spec: expectedFilters()}}}), nil
}
func (s *inventoryService) ListDashboards(_ context.Context, r *connect.Request[pb.ListDashboardsRequest]) (*connect.Response[pb.ListDashboardsResponse], error) {
	s.reads++
	if r.Msg.OrganizationId != "org" {
		s.t.Fatal("dashboard scope lost")
	}
	return connect.NewResponse(&pb.ListDashboardsResponse{Dashboards: []*pb.Dashboard{{Name: "Production", Spec: &pb.DashboardSpec{Widgets: []*pb.DashboardWidget{{Filters: expectedFilters()}}}}}}), nil
}
func TestInventoryScopeCommands(t *testing.T) {
	file := filepath.Join(t.TempDir(), "filters.json")
	if err := os.WriteFile(file, []byte(inventoryFilterJSON), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"resources", "denied", "default-summary", "resource-summary", "operations", "views", "dashboards"} {
		t.Run(command, func(t *testing.T) {
			s := &inventoryService{service: service{t: t}, denied: command == "denied", defaultSummary: command == "default-summary"}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			cmd := command
			if s.defaultSummary {
				cmd = "resource-summary"
			}
			if cmd == "denied" {
				cmd = "resources"
			}
			args := []string{cmd, "--url", server.URL, "--org", "org", "--auth-stdin"}
			if cmd != "views" && cmd != "dashboards" && !s.defaultSummary {
				args = append(args, "--filters", file)
			}
			if cmd == "resources" || cmd == "operations" {
				args = append(args, "--all")
			}
			if cmd == "resource-summary" {
				if !s.defaultSummary {
					args = append(args, "--group-by", "status")
				}
			} else {
				args = append(args, "--json")
			}
			var out, diagnostics bytes.Buffer
			code := Run(context.Background(), args, strings.NewReader(loginInput), &out, &diagnostics)
			want := 0
			if s.denied {
				want = 4
			}
			if code != want || s.logins != 1 || s.logouts != 1 {
				t.Fatal(code, diagnostics.String())
			}
			if s.denied && out.Len() != 0 {
				t.Fatal("partial result leaked")
			}
			if cmd == "resources" && s.reads != 2 {
				t.Fatal("missing second page")
			}
			if cmd == "resource-summary" && (!strings.Contains(out.String(), "total") || strings.Contains(out.String(), "running\nunsafe")) {
				t.Fatal("summary table missing or unsafe")
			}
			if (cmd == "views" || cmd == "dashboards") && !strings.Contains(out.String(), "tagConditions") {
				t.Fatal("nested filters omitted")
			}
		})
	}
	for _, tc := range []struct {
		body  string
		flags []string
	}{
		{`{"unknown":true}`, nil}, {`{"kind":"a","kind":"b"}`, nil}, {`{}`, []string{"--provider", "aws"}}, {strings.Repeat(" ", 65537), nil},
	} {
		if err := os.WriteFile(file, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"resources", "--url", "http://localhost:1", "--org", "org", "--auth-stdin", "--filters", file}, tc.flags...)
		var out bytes.Buffer
		if Run(context.Background(), args, strings.NewReader(loginInput), &out, &out) != 2 {
			t.Fatal("invalid filters reached login")
		}
	}
}
