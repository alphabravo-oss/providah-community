package consolecli

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type pagesService struct {
	service
	mode string
}

func (s *pagesService) ListResources(_ context.Context, r *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	s.reads++
	want := ""
	if s.reads > 1 {
		want = fmt.Sprintf("page-%d", s.reads)
	}
	if r.Msg.PageToken != want || r.Msg.OrganizationId != "org" || r.Msg.Kind != "compute.server" || r.Msg.Search != "needle" || !strings.Contains(r.Header().Get("Cookie"), "private-session") {
		s.t.Fatal("pagination lost scope or session")
	}
	if s.mode == "denied" && s.reads == 2 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("access revoked"))
	}
	next := ""
	if s.reads < 3 {
		next = fmt.Sprintf("page-%d", s.reads+1)
	}
	if s.mode == "cycle" && s.reads == 2 {
		next = "page-2"
	}
	rows := []*pb.Resource{{Id: fmt.Sprintf("r%d", s.reads), Name: "node", Kind: "compute.server"}}
	if s.mode == "limit" {
		rows = make([]*pb.Resource, 100001)
		for i := range rows {
			rows[i] = &pb.Resource{Id: "row"}
		}
	}
	if s.mode == "empty page" && s.reads == 1 {
		rows = nil
	}
	return connect.NewResponse(&pb.ListResourcesResponse{Resources: rows, NextPageToken: next}), nil
}
func TestAllPages(t *testing.T) {
	for _, mode := range []string{"complete", "empty page", "denied", "cycle", "limit"} {
		t.Run(mode, func(t *testing.T) {
			s := &pagesService{service: service{t: t}, mode: mode}
			path, h := providahv1connect.NewConsoleServiceHandler(s)
			mux := http.NewServeMux()
			mux.Handle("/api"+path, http.StripPrefix("/api", h))
			server := httptest.NewServer(mux)
			defer server.Close()
			s.origin = server.URL
			var out, diagnostics bytes.Buffer
			code := Run(context.Background(), []string{"resources", "--url", server.URL, "--org", "org", "--kind", "compute.server", "--search", "needle", "--all", "--json", "--auth-stdin"}, strings.NewReader(loginInput), &out, &diagnostics)
			want := 0
			if mode == "denied" {
				want = 4
			}
			if mode == "cycle" || mode == "limit" {
				want = 6
			}
			if code != want || s.logouts != 1 || s.logins != 1 {
				t.Fatalf("result/session: %d %s", code, diagnostics.String())
			}
			if want != 0 {
				reads := 2
				if mode == "limit" {
					reads = 1
				}
				if out.Len() != 0 || s.reads != reads {
					t.Fatal("partial result published or pagination retried")
				}
				return
			}
			var result struct {
				Resources []struct {
					ID string `json:"id"`
				} `json:"resources"`
				Next string `json:"nextPageToken"`
			}
			if json.Unmarshal(out.Bytes(), &result) != nil {
				t.Fatal("not aggregate JSON")
			}
			count := 3
			if mode == "empty page" {
				count = 2
			}
			if len(result.Resources) != count || result.Next != "" || s.reads != 3 {
				t.Fatal("incomplete aggregate or stale cursor")
			}
		})
	}
}
