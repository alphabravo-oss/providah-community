//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"net/http"
	"net/http/httptest"
	"testing"
)

func exerciseInventoryViews(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := randomID()
	_, err := s.pool.Exec(ctx, "INSERT INTO organizations(id,name) VALUES($1,'View isolation')", org)
	check(err)
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	makeClient := func() (providahv1connect.ConsoleServiceClient, string) {
		user, session := randomID(), randomID()
		cipher, e := s.seal("JBSWY3DPEHPK3PXP")
		check(e)
		_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext) VALUES($1,$2,'fake',$3)", user, user+"@example.test", cipher)
		check(e)
		_, e = s.pool.Exec(ctx, "INSERT INTO memberships(org_id,user_id,permissions) VALUES($1,$2,ARRAY['resources.read'])", org, user)
		check(e)
		_, e = s.pool.Exec(ctx, "INSERT INTO sessions(id,user_id,refresh_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", session, user, []byte(randomID()))
		check(e)
		h := http.Header{}
		check(s.issue(h, session, "fake"))
		access := ""
		for _, c := range (&http.Response{Header: h}).Cookies() {
			if c.Name == "providah_access" {
				access = c.Name + "=" + c.Value
			}
		}
		client := providahv1connect.NewConsoleServiceClient(server.Client(), server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Origin", s.cfg.Origin)
				r.Header().Set("Cookie", access)
				return next(ctx, r)
			}
		})))
		return client, user
	}
	a, user := makeClient()
	b, otherUser := makeClient()
	spec := &pb.InventoryViewSpec{ConnectionId: randomID(), Region: "fsn1", Status: "running", TagKey: "env", TagValue: "production", Search: "private-search", Provider: "hetzner", Kind: "compute.server", SortBy: "name", Descending: true, HiddenColumns: []string{"region"}}
	saved, err := a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Name: "Production", Spec: spec}))
	check(err)
	v := saved.Msg
	if v.Spec.ConnectionId != spec.ConnectionId || v.Spec.Region != "fsn1" || v.Spec.Status != "running" || v.Revision != 1 || v.Spec.Search != spec.Search || v.Spec.TagKey != "env" || v.Spec.TagValue != "production" {
		t.Fatal("saved view did not round trip")
	}
	list, err := b.ListInventoryViews(ctx, connect.NewRequest(&pb.ListInventoryViewsRequest{OrganizationId: org}))
	check(err)
	if len(list.Msg.Views) != 0 {
		t.Fatal("another user saw private views")
	}
	for _, id := range []string{v.Id, randomID()} {
		_, e := b.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Id: id, ExpectedRevision: 1, Name: "Guess", Spec: spec}))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("foreign/missing view update was not uniformly rejected")
		}
		_, e = b.DeleteInventoryView(ctx, connect.NewRequest(&pb.DeleteInventoryViewRequest{OrganizationId: org, Id: id, ExpectedRevision: 1}))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("foreign/missing deletion was not uniformly rejected")
		}
	}
	_, err = a.ListInventoryViews(ctx, connect.NewRequest(&pb.ListInventoryViewsRequest{OrganizationId: randomID()}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-organization view access allowed")
	}
	_, err = a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Name: "Production", Spec: spec}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("duplicate name accepted")
	}
	updated, err := a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Id: v.Id, ExpectedRevision: v.Revision, Name: "Renamed", Spec: spec}))
	check(err)
	if updated.Msg.Revision != 2 {
		t.Fatal("revision did not advance")
	}
	_, err = a.DeleteInventoryView(ctx, connect.NewRequest(&pb.DeleteInventoryViewRequest{OrganizationId: org, Id: v.Id, ExpectedRevision: 1}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale deletion accepted")
	}
	_, err = a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Id: v.Id, ExpectedRevision: 1, Name: "Stale", Spec: spec}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale update accepted")
	}
	for _, bad := range []*pb.InventoryViewSpec{nil, {Descending: true}, {SortBy: "unknown"}, {HiddenColumns: []string{"name", "provider", "kind", "region", "status", "observedAt"}}, {HiddenColumns: []string{"region", "region"}}} {
		_, e := a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Name: "Invalid", Spec: bad}))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("invalid view accepted")
		}
	}

	combined, e := a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Name: "Combined", Spec: &pb.InventoryViewSpec{TagConditions: []*pb.TagCondition{{Key: "env", Value: "production"}, {Name: "backend"}}, TagMatchAny: true}}))
	check(e)
	if len(combined.Msg.Spec.TagConditions) != 2 || !combined.Msg.Spec.TagMatchAny {
		t.Fatal("combined view did not persist")
	}
	_, e = a.DeleteInventoryView(ctx, connect.NewRequest(&pb.DeleteInventoryViewRequest{OrganizationId: org, Id: combined.Msg.Id, ExpectedRevision: combined.Msg.Revision}))
	check(e)
	_, err = s.pool.Exec(ctx, "INSERT INTO inventory_views(id,org_id,user_id,name,spec) SELECT repeat(md5($1||n),2),$2,$1,'Extra '||n,'{}'::jsonb FROM generate_series(1,49)n", user, org)
	check(err)
	_, err = a.SaveInventoryView(ctx, connect.NewRequest(&pb.SaveInventoryViewRequest{OrganizationId: org, Name: "Over limit", Spec: spec}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("view limit not enforced")
	}
	_, err = a.DeleteInventoryView(ctx, connect.NewRequest(&pb.DeleteInventoryViewRequest{OrganizationId: org, Id: v.Id, ExpectedRevision: 2}))
	check(err)
	var leaked int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE org_id=$1 AND details::text LIKE '%private-search%'", org).Scan(&leaked))
	if leaked != 0 {
		t.Fatal("private view filters leaked through audit")
	}

	dashboardSpec := &pb.DashboardSpec{Widgets: []*pb.DashboardWidget{{Title: "Production", Filters: spec}}}
	dashboard, e := a.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Name: "My dashboard", Spec: dashboardSpec}))
	check(e)
	if dashboard.Msg.Revision != 1 || dashboard.Msg.Spec.Widgets[0].Filters.TagKey != "env" {
		t.Fatal("dashboard filters did not round trip")
	}
	dashboards, e := b.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
	check(e)
	if len(dashboards.Msg.Dashboards) != 0 {
		t.Fatal("private dashboard leaked")
	}
	_, e = b.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Id: dashboard.Msg.Id, ExpectedRevision: 1, Name: "Stolen", Spec: dashboardSpec}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("foreign dashboard update allowed")
	}
	_, e = b.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: dashboard.Msg.Id, ExpectedRevision: 1}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("foreign dashboard delete allowed")
	}
	_, e = a.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: randomID()}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign organization dashboard read allowed")
	}
	updatedDashboard, e := a.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Id: dashboard.Msg.Id, ExpectedRevision: 1, Name: "Updated", Spec: dashboardSpec}))
	check(e)
	if updatedDashboard.Msg.Revision != 2 {
		t.Fatal("dashboard revision did not advance")
	}
	_, e = a.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: dashboard.Msg.Id, ExpectedRevision: 1}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale dashboard delete allowed")
	}
	_, e = a.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Name: "Bad", Spec: &pb.DashboardSpec{Widgets: []*pb.DashboardWidget{{Title: "Bad", Filters: &pb.InventoryViewSpec{TagValue: "invalid"}}}}}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid dashboard filter allowed")
	}
	_, e = a.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: dashboard.Msg.Id, ExpectedRevision: 2}))
	check(e)

	_, e = a.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Name: "Shared", Spec: dashboardSpec, Shared: true}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("sharing allowed without permission")
	}
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET permissions=array_append(permissions,'dashboards.manage') WHERE org_id=$1 AND user_id=$2", org, user)
	check(e)
	shared, e := a.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Name: "Shared", Spec: dashboardSpec, Shared: true}))
	check(e)
	if !shared.Msg.Shared || !shared.Msg.Owned {
		t.Fatal("incorrect shared owner metadata")
	}
	visible, e := b.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
	check(e)
	if len(visible.Msg.Dashboards) != 1 || visible.Msg.Dashboards[0].Owned || !visible.Msg.Dashboards[0].Shared {
		t.Fatal("shared visibility incorrect")
	}
	_, e = b.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Id: shared.Msg.Id, ExpectedRevision: 1, Name: "Unauthorized", Spec: dashboardSpec, Shared: true}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("reader edited shared dashboard")
	}
	_, e = b.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: shared.Msg.Id, ExpectedRevision: 1}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("reader deleted shared dashboard")
	}
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET permissions=array_append(permissions,'dashboards.manage') WHERE org_id=$1 AND user_id=$2", org, otherUser)
	check(e)
	_, e = b.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Name: "Shared", Spec: dashboardSpec, Shared: true}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("duplicate organization dashboard name allowed")
	}
	sharedUpdate, e := b.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Id: shared.Msg.Id, ExpectedRevision: 1, Name: "Team board", Spec: dashboardSpec, Shared: true}))
	check(e)
	if sharedUpdate.Msg.Owned || sharedUpdate.Msg.Revision != 2 {
		t.Fatal("shared edit changed ownership")
	}
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET permissions=array_remove(permissions,'dashboards.manage') WHERE org_id=$1 AND user_id=$2", org, user)
	check(e)
	_, e = a.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Id: shared.Msg.Id, ExpectedRevision: 2, Name: "Owner override", Spec: dashboardSpec}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("revoked owner bypassed shared permission")
	}
	_, e = b.SaveDashboard(ctx, connect.NewRequest(&pb.SaveDashboardRequest{OrganizationId: org, Id: shared.Msg.Id, ExpectedRevision: 2, Name: "Private again", Spec: dashboardSpec}))
	check(e)
	visible, e = b.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
	check(e)
	if len(visible.Msg.Dashboards) != 0 {
		t.Fatal("unpublished dashboard remained visible to manager")
	}
	_, e = a.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: shared.Msg.Id, ExpectedRevision: 3}))
	check(e)
	teamManager, teamManagerID := makeClient()
	_, err = s.pool.Exec(ctx, "UPDATE memberships SET permissions=array_append(permissions,'dashboards.manage') WHERE org_id=$1 AND user_id=$2", org, teamManagerID)
	check(err)
	exerciseTeamDashboards(t, s, ctx, org, user, a, b, teamManager)
	_, err = s.pool.Exec(ctx, "UPDATE memberships SET active=false WHERE org_id=$1 AND user_id=$2", org, user)
	check(err)
	_, err = a.ListInventoryViews(ctx, connect.NewRequest(&pb.ListInventoryViewsRequest{OrganizationId: org}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("revoked membership retained views")
	}
	_, err = a.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("revoked membership retained dashboards")
	}

	_, err = a.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: org, GroupBy: "status", Filters: &pb.InventoryViewSpec{}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("revoked membership retained summary access")
	}

	_, err = s.pool.Exec(ctx, "UPDATE memberships SET permissions=ARRAY['operations.read'] WHERE org_id=$1 AND user_id=$2", org, otherUser)
	check(err)
	_, err = b.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: org}))
	check(err)
	_, err = b.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: org, ResourceFilters: &pb.InventoryViewSpec{}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("activity used inventory without resource access")
	}

	_, err = a.ListResourceScopes(ctx, connect.NewRequest(&pb.ListResourceScopesRequest{OrganizationId: org}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("revoked membership retained scope choices")
	}

}
