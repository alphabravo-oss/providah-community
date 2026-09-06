//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"testing"
	"time"
)

func exerciseSchedules(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	request := &pb.SaveScheduleRequest{OrganizationId: org, Name: "Weekday startup", ResourceIds: []string{resource}, Spec: &pb.ScheduleSpec{Timezone: "UTC", Action: "start", Cron: "* * * * *"}}
	target, err := s.q.GetResource(ctx, database.GetResourceParams{OrgID: org, ID: resource})
	check(err)
	catalog := s.cfg.ProviderRuntimes
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: target.Provider, CapabilitiesVersion: 1, InventoryKinds: []string{"compute.server"}}}
	_, err = owner.SaveSchedule(ctx, connect.NewRequest(request))
	s.cfg.ProviderRuntimes = catalog
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("read-only runtime accepted a schedule", err)
	}
	saved, err := owner.SaveSchedule(ctx, connect.NewRequest(request))
	check(err)
	id := saved.Msg.Schedule.Id
	read, e := owner.GetSchedule(ctx, connect.NewRequest(&pb.GetScheduleRequest{OrganizationId: org, Id: id}))
	check(e)
	if read.Msg.Schedule.Id != id || read.Msg.Schedule.CanApprove {
		t.Fatal("wrong direct schedule authority")
	}
	read, e = approver.GetSchedule(ctx, connect.NewRequest(&pb.GetScheduleRequest{OrganizationId: org, Id: id}))
	check(e)
	if !read.Msg.Schedule.CanApprove {
		t.Fatal("independent schedule reviewer missing")
	}
	for _, target := range []*pb.GetScheduleRequest{{OrganizationId: randomID(), Id: id}, {OrganizationId: org, Id: randomID()}} {
		_, e = owner.GetSchedule(ctx, connect.NewRequest(target))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("schedule scope leaked", e)
		}
	}
	_, e = owner.GetSchedule(ctx, connect.NewRequest(&pb.GetScheduleRequest{OrganizationId: org, Id: "invalid"}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("malformed schedule ID accepted")
	}

	if saved.Msg.Schedule.Approved || saved.Msg.Schedule.IdentityId == "" {
		t.Fatal("schedule lacks independent identity or approval gate")
	}
	_, err = owner.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id)}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("self approval accepted", err)
	}
	// A second editor changes the configuration after the reviewer loaded revision 1.
	changed := &pb.SaveScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: saved.Msg.Schedule.Revision, Name: request.Name, Spec: request.Spec, ResourceIds: request.ResourceIds}
	newer, e := owner.SaveSchedule(ctx, connect.NewRequest(changed))
	check(e)
	if newer.Msg.Schedule.Revision != saved.Msg.Schedule.Revision+1 {
		t.Fatal("schedule write revision did not advance")
	}
	for _, stale := range []int64{0, saved.Msg.Schedule.Revision} {
		changed.ExpectedRevision = stale
		_, e = owner.SaveSchedule(ctx, connect.NewRequest(changed))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("stale edit accepted", e)
		}
		_, e = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: stale}))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("unreviewed revision approved", e)
		}
		_, e = owner.SetScheduleEnabled(ctx, connect.NewRequest(&pb.SetScheduleEnabledRequest{OrganizationId: org, Id: id, ExpectedRevision: stale, Enabled: false, IdentityEnabled: false}))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("stale toggle accepted", e)
		}
		_, e = owner.DeleteSchedule(ctx, connect.NewRequest(&pb.DeleteScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: stale, Confirmation: request.Name}))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("stale delete accepted", e)
		}
	}
	due := func(age time.Duration) {
		t.Helper()
		_, err := s.pool.Exec(ctx, "UPDATE schedules SET next_due=$2 WHERE id=$1", id, time.Now().Add(-age))
		check(err)
		check(s.scheduleTick(ctx, time.Now()))
	}
	latest := func() *pb.ScheduleOccurrence {
		t.Helper()
		history, err := owner.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: org}))
		check(err)
		if len(history.Msg.Occurrences) == 0 {
			t.Fatal("missing occurrence")
		}
		return history.Msg.Occurrences[0]
	}
	due(time.Second)
	if scheduleTestRevision(t, s, ctx, org, id) != newer.Msg.Schedule.Revision {
		t.Fatal("clock advancement invalidated human review")
	}
	if latest().Outcome != "skipped" {
		t.Fatal("unapproved schedule queued work")
	}
	_, err = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id)}))
	check(err)
	due(time.Second)
	queued := latest()
	if queued.Outcome != "queued" || queued.OperationId == "" {
		t.Fatal("approved occurrence did not queue", queued)
	}
	check(s.scheduleTick(ctx, time.Now()))
	// Crossing a real minute boundary may legitimately create the next slot.
	// Reprocessing must not duplicate the original scheduled slot.
	var copies int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM schedule_occurrences WHERE (schedule_id,resource_id,scheduled_for,action,local_time)=(SELECT schedule_id,resource_id,scheduled_for,action,local_time FROM schedule_occurrences WHERE id=$1)", queued.Id).Scan(&copies))
	if copies != 1 {
		t.Fatal("occurrence duplicated")
	}

	due(2 * time.Second)
	if latest().Outcome != "skipped" {
		t.Fatal("overlap was not skipped")
	}
	_, err = owner.SetScheduleEnabled(ctx, connect.NewRequest(&pb.SetScheduleEnabledRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id), Enabled: true, IdentityEnabled: false}))
	check(err)
	check(s.operationTick(ctx))
	var status string
	check(s.pool.QueryRow(ctx, "SELECT status FROM operations WHERE id=$1", queued.OperationId).Scan(&status))
	if status != "canceled" {
		t.Fatal("disabled identity allowed dispatch", status)
	}
	_, err = owner.SetScheduleEnabled(ctx, connect.NewRequest(&pb.SetScheduleEnabledRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id), Enabled: true, IdentityEnabled: true}))
	check(err)
	due(2 * time.Minute)
	if latest().Outcome != "skipped" {
		t.Fatal("missed run was replayed")
	}
	// An enabled, approved identity can traverse the existing provider pipeline.
	due(time.Second)
	queued = latest()
	if queued.Outcome != "queued" {
		t.Fatal("enabled identity did not queue", queued)
	}
	check(s.operationTick(ctx))
	check(s.pool.QueryRow(ctx, "SELECT status FROM operations WHERE id=$1", queued.OperationId).Scan(&status))
	if status != "observing" {
		t.Fatal("scheduled operation failed to dispatch", status)
	}
	request.Id = id
	request.ExpectedRevision = scheduleTestRevision(t, s, ctx, org, id)
	request.Name = "Changed schedule"
	saved, err = owner.SaveSchedule(ctx, connect.NewRequest(request))
	check(err)
	if saved.Msg.Schedule.Approved {
		t.Fatal("edit retained standing approval")
	}
	_, err = owner.DeleteSchedule(ctx, connect.NewRequest(&pb.DeleteScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id), Confirmation: "wrong"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("deletion skipped confirmation", err)
	}
	_, err = owner.DeleteSchedule(ctx, connect.NewRequest(&pb.DeleteScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id), Confirmation: request.Name}))
	check(err)
	_ = latest() // Historical evidence survives removal.
	_, err = s.pool.Exec(ctx, `INSERT INTO schedule_occurrences(org_id,schedule_id,revision,resource_id,scheduled_for,action,outcome,detail,operation_id,local_time) SELECT org_id,schedule_id,revision,resource_id,'2030-01-01'::timestamptz+n*interval '1 minute',action,'skipped','History fixture',NULL,'fixture-'||n FROM schedule_occurrences CROSS JOIN generate_series(1,205) n WHERE id=$1`, queued.Id)
	check(err)
	for _, outcome := range []string{"", "skipped", "queued"} {
		token := ""
		seen := map[string]bool{}
		for {
			result, e := owner.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: org, ScheduleId: id, Outcome: outcome, PageToken: token}))
			check(e)
			if len(result.Msg.Occurrences) > 100 {
				t.Fatal("unbounded history")
			}
			for _, row := range result.Msg.Occurrences {
				if row.ScheduleId != id || (outcome != "" && row.Outcome != outcome) || seen[row.Id] {
					t.Fatal("incorrect filtered history")
				}
				seen[row.Id] = true
			}
			token = result.Msg.NextPageToken
			if token == "" {
				break
			}
			_, e = owner.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: org, ScheduleId: randomID(), Outcome: outcome, PageToken: token}))
			if connect.CodeOf(e) != connect.CodeInvalidArgument {
				t.Fatal("cursor crossed schedule filter", e)
			}
		}
		var expected int
		check(s.pool.QueryRow(ctx, "SELECT count(*) FROM schedule_occurrences WHERE org_id=$1 AND schedule_id=$2 AND ($3='' OR outcome=$3)", org, id, outcome).Scan(&expected))
		if len(seen) != expected {
			t.Fatal("history page omitted records", len(seen), expected)
		}
	}
	_, err = owner.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: org, Outcome: "failed"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("operation status accepted as dispatch outcome")
	}

}

func scheduleTestRevision(t *testing.T, s *Service, ctx context.Context, org, id string) int64 {
	t.Helper()
	var revision int64
	if e := s.pool.QueryRow(ctx, "SELECT write_revision FROM schedules WHERE org_id=$1 AND id=$2", org, id).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	return revision
}
