//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"testing"
)

func exerciseTeamDashboards(t *testing.T, s *Service, ctx context.Context, org, user string, reader, manager, otherManager providahv1connect.ConsoleServiceClient) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	exec := func(sql string, args ...any) { t.Helper(); _, e := s.pool.Exec(ctx, sql, args...); check(e) }
	team, foreignOrg, foreignTeam := randomID(), randomID(), randomID()
	exec("INSERT INTO organizations(id,name) VALUES($1,'Other audience')", foreignOrg)
	for _, scope := range []struct{ org, id string }{{org, team}, {foreignOrg, foreignTeam}} {
		exec("INSERT INTO roles(org_id,id,name,permissions) VALUES($1,'dashboard-team','Dashboard team','{}')", scope.org)
		exec("INSERT INTO teams(org_id,id,name,role_id) VALUES($1,$2,'Operations','dashboard-team')", scope.org, scope.id)
	}
	req := &pb.SaveDashboardRequest{OrganizationId: org, Name: "Team audience", Spec: &pb.DashboardSpec{}, TeamIds: []string{team}}
	_, e := reader.SaveDashboard(ctx, connect.NewRequest(req))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("reader shared dashboard", e)
	}
	_, e = reader.ListDashboardTeams(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("reader enumerated audience directory", e)
	}
	choices, e := manager.ListDashboardTeams(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
	check(e)
	if len(choices.Msg.Teams) != 1 || choices.Msg.Teams[0].Id != team {
		t.Fatal("team choices escaped organization")
	}
	req.TeamIds = []string{foreignTeam}
	_, e = manager.SaveDashboard(ctx, connect.NewRequest(req))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("foreign team accepted", e)
	}
	req.TeamIds = []string{team}
	req.Shared = true
	_, e = manager.SaveDashboard(ctx, connect.NewRequest(req))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("ambiguous audience accepted", e)
	}
	req.Shared = false
	created, e := manager.SaveDashboard(ctx, connect.NewRequest(req))
	check(e)
	req.Id, req.ExpectedRevision = created.Msg.Id, created.Msg.Revision
	if created.Msg.Shared || len(created.Msg.TeamIds) != 1 {
		t.Fatal("team audience metadata lost")
	}
	visible := func(client providahv1connect.ConsoleServiceClient, want bool) {
		t.Helper()
		rows, e := client.ListDashboards(ctx, connect.NewRequest(&pb.ListDashboardsRequest{OrganizationId: org}))
		check(e)
		found := false
		for _, d := range rows.Msg.Dashboards {
			if d.Id == req.Id {
				found = true
			}
		}
		if found != want {
			t.Fatalf("team audience visible=%v, want %v", found, want)
		}
	}
	visible(reader, false)
	visible(manager, true)
	visible(otherManager, true)
	exec("INSERT INTO team_members(org_id,team_id,user_id) VALUES($1,$2,$3)", org, team, user)
	visible(reader, true)
	_, e = reader.SaveDashboard(ctx, connect.NewRequest(req))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("team reader edited dashboard", e)
	}
	_, e = reader.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: req.Id, ExpectedRevision: 1}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("team reader deleted dashboard", e)
	}
	exec("UPDATE teams SET active=false WHERE org_id=$1 AND id=$2", org, team)
	visible(reader, false)
	exec("UPDATE teams SET active=true WHERE org_id=$1 AND id=$2", org, team)
	visible(reader, true)
	exec("DELETE FROM team_members WHERE org_id=$1 AND team_id=$2", org, team)
	visible(reader, false)
	exec("INSERT INTO team_members(org_id,team_id,user_id) VALUES($1,$2,$3)", org, team, user)
	visible(reader, true)
	exec("DELETE FROM team_members WHERE org_id=$1 AND team_id=$2", org, team)
	exec("DELETE FROM teams WHERE org_id=$1 AND id=$2", org, team)
	visible(reader, false)
	visible(manager, true)
	visible(otherManager, true)
	var shared bool
	var ids []string
	check(s.pool.QueryRow(ctx, "SELECT shared,team_ids FROM dashboards WHERE id=$1", req.Id).Scan(&shared, &ids))
	if shared || len(ids) != 1 || ids[0] != team {
		t.Fatal("deleted team broadened audience")
	}
	_, e = manager.SaveDashboard(ctx, connect.NewRequest(req))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("deleted audience retained on save", e)
	}
	req.TeamIds = nil
	updated, e := otherManager.SaveDashboard(ctx, connect.NewRequest(req))
	check(e)
	if updated.Msg.Owned || updated.Msg.Shared || len(updated.Msg.TeamIds) != 0 || updated.Msg.Revision != 2 {
		t.Fatal("explicit unshare failed")
	}
	visible(reader, false)
	visible(otherManager, false)
	_, e = manager.SaveDashboard(ctx, connect.NewRequest(req))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale dashboard update accepted", e)
	}
	_, e = manager.DeleteDashboard(ctx, connect.NewRequest(&pb.DeleteDashboardRequest{OrganizationId: org, Id: req.Id, ExpectedRevision: 2}))
	check(e)
}
