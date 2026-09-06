//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"testing"
	"time"
)

func exerciseScheduleSlots(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	spec := &pb.ScheduleSpec{Action: "start", Timezone: "America/New_York", Cron: "0 2,3 * * *"}
	saved, err := owner.SaveSchedule(ctx, connect.NewRequest(&pb.SaveScheduleRequest{OrganizationId: org, Name: "DST slot regression", ResourceIds: []string{resource}, Spec: spec}))
	check(err)
	id := saved.Msg.Schedule.Id
	// Keep earlier wall-clock schedules from winning this single-claim fixture at a minute boundary.
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET next_due=NULL WHERE id<>$1", id)
	check(err)
	_, err = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, id)}))
	check(err)
	// Repair future cursors from the previous clock algorithm without revoking
	// standing approval or replaying already overdue work.
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET clock_version=1,next_due=now()+interval '40 days',next_local='old cursor' WHERE id=$1", id)
	check(err)
	check(s.scheduleTick(ctx, time.Now()))
	row, err := s.q.GetSchedule(ctx, database.GetScheduleParams{OrgID: org, ID: id})
	check(err)
	if row.ClockVersion != 2 || row.NextLocal == "old cursor" || !row.ApprovedRevision.Valid {
		t.Fatal("existing future cursor was not repaired", row.NextLocal)
	}
	before, _ := time.Parse(time.RFC3339, "2026-03-08T06:59:00Z")
	first, err := nextSchedule(spec, before)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET next_due=$2,next_local=$3,next_action=$4,next_skip=$5 WHERE id=$1", id, first.Due, first.Local, first.Action, first.Skip)
	check(err)
	check(s.scheduleTick(ctx, first.Due.Add(500*time.Millisecond)))
	row, err = s.q.GetSchedule(ctx, database.GetScheduleParams{OrgID: org, ID: id})
	check(err)
	if !row.NextDue.Time.Equal(first.Due) || row.NextSkip != "" || row.NextLocal == first.Local {
		t.Fatal("skipped slot consumed real slot", row.NextLocal, row.NextSkip)
	}
	check(s.scheduleTick(ctx, first.Due.Add(time.Second)))
	history, err := owner.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: org}))
	check(err)
	var slots []*pb.ScheduleOccurrence
	for _, o := range history.Msg.Occurrences {
		if o.ScheduleId == id {
			slots = append(slots, o)
		}
	}
	if len(slots) != 2 || slots[0].Outcome != "queued" || slots[1].Outcome != "skipped" || slots[0].ScheduledFor != slots[1].ScheduledFor || slots[0].LocalTime == slots[1].LocalTime {
		t.Fatal("distinct UTC-colliding slots were lost", slots)
	}
}
