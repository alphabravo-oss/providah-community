//go:build integration

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func exerciseModules(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource, connection string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	set := func(enabled bool) {
		t.Helper()
		_, e := owner.SetProviderModule(ctx, connect.NewRequest(&pb.SetProviderModuleRequest{OrganizationId: org, Provider: "hetzner", Enabled: enabled, Confirmation: "hetzner"}))
		check(e)
	}
	_, err := approver.SetProviderModule(ctx, connect.NewRequest(&pb.SetProviderModuleRequest{OrganizationId: org, Provider: "hetzner", Confirmation: "hetzner"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("non-administrator module change allowed", err)
	}
	// Clear prior fixture work; real module changes never rewrite prior outcomes.
	_, err = s.pool.Exec(ctx, "UPDATE operations SET status='canceled' WHERE org_id=$1 AND status IN ('queued','awaiting_approval')", org)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET enabled=false,next_due=NULL WHERE org_id=$1", org)
	check(err)
	setState := func() {
		_, e := s.pool.Exec(ctx, "UPDATE resources SET status='off',observed_at=now() WHERE id=$1", resource)
		check(e)
	}
	setState()
	request := func() *pb.Operation {
		t.Helper()
		out, e := owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "start", ExpectedStatus: "off", Reason: "Module test", IdempotencyKey: randomID()}))
		check(e)
		return out.Msg.Operation
	}
	stale := request()
	scanID, err := s.queueScan(ctx, org, connection, "", "test")
	check(err)
	scan, err := s.q.ClaimScan(ctx)
	check(err)
	if scan.ID != scanID {
		t.Fatal("unexpected scan fixture")
	}
	scheduled, err := owner.SaveSchedule(ctx, connect.NewRequest(&pb.SaveScheduleRequest{OrganizationId: org, Name: "Module schedule", ResourceIds: []string{resource}, Spec: &pb.ScheduleSpec{Timezone: "UTC", Action: "start", Cron: "* * * * *"}}))
	check(err)
	_, err = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: scheduled.Msg.Schedule.Id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, scheduled.Msg.Schedule.Id)}))
	check(err)
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous }()
	calls := 0
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		calls++
		if r.Power == nil {
			t.Fatal("unexpected discovery call")
		}
		outcome := "accepted"
		if r.Power.Phase == "observe" {
			outcome = "succeeded"
		}
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: outcome, ActionID: "42", Status: "running"}}, nil
	}
	set(false)
	if _, err = s.queueScan(ctx, org, connection, "", "test"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("disabled discovery queued", err)
	}
	_, err = owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "start", ExpectedStatus: "off", Reason: "Module test", IdempotencyKey: randomID()}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("disabled mutation requested", err)
	}
	preview, err := owner.PreviewSchedule(ctx, connect.NewRequest(&pb.PreviewScheduleRequest{OrganizationId: org, ResourceIds: []string{resource}, Spec: &pb.ScheduleSpec{Timezone: "UTC", Action: "start", Cron: "* * * * *"}}))
	check(err)
	if preview.Msg.Occurrences[0].SkippedReason == "" {
		t.Fatal("disabled module not shown in preview")
	}
	set(true)
	// Turning it back on cannot authorize work captured under the old revision.
	check(s.runScan(ctx, scan))
	if calls != 0 {
		t.Fatal("old scan reached worker")
	}
	var scanStatus string
	check(s.pool.QueryRow(ctx, "SELECT status FROM scan_jobs WHERE id=$1", scan.ID).Scan(&scanStatus))
	if scanStatus != "canceled" {
		t.Fatal("old scan not canceled")
	}
	run := func() { job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	run()
	old, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: stale.Id})
	check(err)
	if old.Status != "canceled" || calls != 0 {
		t.Fatal("old operation revived")
	}
	_, err = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: scheduled.Msg.Schedule.Id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, scheduled.Msg.Schedule.Id)}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("old schedule approved without new target review", err)
	}
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET next_due=$2 WHERE id=$1", scheduled.Msg.Schedule.Id, time.Now().Add(-time.Second))
	check(err)
	check(s.scheduleTick(ctx, time.Now()))
	var outcome string
	check(s.pool.QueryRow(ctx, "SELECT outcome FROM schedule_occurrences WHERE schedule_id=$1 ORDER BY id DESC LIMIT 1", scheduled.Msg.Schedule.Id).Scan(&outcome))
	if outcome != "skipped" {
		t.Fatal("old schedule replayed")
	}
	// A new submission may finish observation while its module is disabled.
	setState()
	active := request()
	run()
	if calls != 1 {
		t.Fatal("fresh submission failed")
	}
	set(false)
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", active.Id)
	check(err)
	run()
	done, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: active.Id})
	check(err)
	if done.Status != "succeeded" || calls != 2 {
		t.Fatal("module disable stopped minimal observation", done.Status)
	}
	detail, err := owner.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: resource}))
	check(err)
	if detail.Msg.ProviderEnabled || !detail.Msg.ConnectionEnabled {
		t.Fatal("module and connection state were conflated")
	}
	list, err := owner.ListProviderModules(ctx, connect.NewRequest(&pb.ListProviderModulesRequest{OrganizationId: org}))
	check(err)
	if len(list.Msg.Modules) != 3 {
		t.Fatal("module catalog incomplete")
	}
	for _, m := range list.Msg.Modules {
		if m.Provider != "hetzner" && !m.Enabled {
			t.Fatal("unrelated provider disabled")
		}
	}
	previousRuntimes := s.cfg.ProviderRuntimes
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: "aws", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: provider.Protocol, PublisherKeyID: "test-publisher", ApprovalExpiresAt: "2027-01-01T00:00:00Z"}}
	list, err = owner.ListProviderModules(ctx, connect.NewRequest(&pb.ListProviderModulesRequest{OrganizationId: org}))
	s.cfg.ProviderRuntimes = previousRuntimes
	check(err)
	for _, m := range list.Msg.Modules {
		if m.Provider == "aws" && (m.PublisherKeyId != "test-publisher" || m.ApprovalExpiresAt != "2027-01-01T00:00:00Z" || len(m.Runtimes) != 1 || m.Runtimes[0].PublisherKeyId != "test-publisher") {
			t.Fatal("runtime trust metadata lost at API")
		}
	}

	set(true)
}
