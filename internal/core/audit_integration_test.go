//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func exerciseAuditFilters(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other string) {
	t.Helper()
	action := "filter.check." + randomID()
	actor, target := "actor%_'\n", "target%_'\n"
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_events(org_id,actor,action,target,occurred_at) SELECT $1,$2,$3,$4,'2026-01-01T00:00:00Z'::timestamptz+n*interval '1 second' FROM generate_series(0,206)n`, org, actor, action, target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_events(org_id,actor,action,target,occurred_at) VALUES($1,$2,$3,$4,'2026-01-01T00:00:01Z')`, other, actor, action, target)
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.ListAuditRequest{OrganizationId: org, Actor: actor, Action: action, Target: target, OccurredFrom: "2026-01-01T00:00:01Z", OccurredBefore: "2026-01-01T00:03:26Z"}
	seen := map[string]bool{}
	firstToken := ""
	for {
		res, e := client.ListAudit(ctx, connect.NewRequest(req))
		if e != nil {
			t.Fatal(e)
		}
		for _, event := range res.Msg.Events {
			if seen[event.Id] || event.Actor != actor || event.Target != target || event.Action != action || (event.OccurredAt == "2026-01-01T00:00:00Z" || event.OccurredAt == "2026-01-01T00:03:26Z") {
				t.Fatal("incorrect audit filter or duplicate page")
			}
			seen[event.Id] = true
		}
		if firstToken == "" {
			firstToken = res.Msg.NextPageToken
		}
		req.PageToken = res.Msg.NextPageToken
		if req.PageToken == "" {
			break
		}
	}
	if len(seen) != 205 || firstToken == "" {
		t.Fatalf("expected 205 filtered rows, got %d", len(seen))
	}
	for _, change := range []func(*pb.ListAuditRequest){
		func(r *pb.ListAuditRequest) { r.Actor = "other" }, func(r *pb.ListAuditRequest) { r.Action = "other" }, func(r *pb.ListAuditRequest) { r.Target = "other" }, func(r *pb.ListAuditRequest) { r.OccurredFrom = "2026-01-01T00:00:02Z" }, func(r *pb.ListAuditRequest) { r.OccurredBefore = "2026-01-01T00:03:25Z" },
	} {
		r := proto.Clone(req).(*pb.ListAuditRequest)
		r.PageToken = firstToken
		change(r)
		_, e := client.ListAudit(ctx, connect.NewRequest(r))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("cursor accepted different filters", e)
		}
	}
	for _, value := range []string{"tomorrow", "2026-01-01T00:00:00", "2026-01-01T00:03:26Z", "2027-01-01T00:00:00Z"} {
		r := proto.Clone(req).(*pb.ListAuditRequest)
		r.OccurredFrom = value
		_, e := client.ListAudit(ctx, connect.NewRequest(r))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("invalid range accepted", e)
		}
	}
	for _, change := range []func(*pb.ListAuditRequest){func(r *pb.ListAuditRequest) { r.Actor = "%" }, func(r *pb.ListAuditRequest) { r.Target = "%" }, func(r *pb.ListAuditRequest) { r.Action = "%" }} {
		r := proto.Clone(req).(*pb.ListAuditRequest)
		change(r)
		res, e := client.ListAudit(ctx, connect.NewRequest(r))
		if e != nil || len(res.Msg.Events) != 0 {
			t.Fatal("exact filter treated as wildcard", e)
		}
	}
	req.Actor = strings.Repeat("x", 321)
	_, err = client.ListAudit(ctx, connect.NewRequest(req))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unbounded filter accepted", err)
	}
	req.Actor = actor
	req.OrganizationId = other
	_, err = client.ListAudit(ctx, connect.NewRequest(req))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-tenant audit allowed", err)
	}
}
