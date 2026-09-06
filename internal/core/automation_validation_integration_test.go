//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"testing"
	"time"
)

func exerciseAutomationValidation(t *testing.T, s *Service, owner, viewer providahv1connect.ConsoleServiceClient, ctx context.Context, org, version string) {
	t.Helper()
	saved := s.cfg.AutomationCall
	defer func() { s.cfg.AutomationCall = saved }()
	calls := 0
	s.cfg.AutomationCall = func(_ context.Context, r automation.Request) (automation.Result, error) {
		calls++
		if e := r.Validate(); e != nil {
			t.Fatal(e)
		}
		return automation.Result{Status: "succeeded"}, nil
	}
	req := &pb.RequestAutomationValidationRequest{OrganizationId: org, VersionId: version}
	if _, e := viewer.RequestAutomationValidation(ctx, connect.NewRequest(req)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("nonpublisher requested code execution", e)
	}
	first, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	again, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil || first.Msg.Id != again.Msg.Id {
		t.Fatal("duplicate active validation", e)
	}
	run := func() {
		t.Helper()
		job, e := s.q.ClaimAutomationValidation(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.runAutomationValidation(ctx, job); e != nil {
			t.Fatal(e)
		}
	}
	run()
	var status string
	if e = s.pool.QueryRow(ctx, "SELECT status FROM automation_validations WHERE id=$1", first.Msg.Id).Scan(&status); e != nil || status != "succeeded" || calls != 1 {
		t.Fatal("validation did not succeed", status, calls, e)
	}
	// Cancellation is scoped, preserves history, and prevents queued dispatch.
	cancelJob, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	cancelRequest := &pb.CancelAutomationValidationRequest{OrganizationId: org, Id: cancelJob.Msg.Id}
	if _, e = viewer.CancelAutomationValidation(ctx, connect.NewRequest(cancelRequest)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("viewer canceled validation", e)
	}
	if _, e = owner.CancelAutomationValidation(ctx, connect.NewRequest(&pb.CancelAutomationValidationRequest{OrganizationId: randomID(), Id: cancelJob.Msg.Id})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign cancellation", e)
	}
	if _, e = owner.CancelAutomationValidation(ctx, connect.NewRequest(cancelRequest)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.q.ClaimAutomationValidation(ctx); e == nil {
		t.Fatal("canceled check dispatched")
	}
	if _, e = owner.CancelAutomationValidation(ctx, connect.NewRequest(cancelRequest)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("terminal check changed", e)
	}
	// A running worker observes cancellation and cannot publish a late success.
	active, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	originalCall := s.cfg.AutomationCall
	s.cfg.AutomationCall = func(run context.Context, _ automation.Request) (automation.Result, error) {
		close(started)
		<-run.Done()
		return automation.Result{Status: "succeeded"}, nil
	}
	job, e := s.q.ClaimAutomationValidation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	go func() { finished <- s.runAutomationValidation(ctx, job) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	if _, e = owner.CancelAutomationValidation(ctx, connect.NewRequest(&pb.CancelAutomationValidationRequest{OrganizationId: org, Id: active.Msg.Id})); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-finished:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("running validation did not stop")
	}
	s.cfg.AutomationCall = originalCall
	if e = s.pool.QueryRow(ctx, "SELECT status FROM automation_validations WHERE id=$1", active.Msg.Id).Scan(&status); e != nil || status != "canceled" {
		t.Fatal("late worker replaced canceled result", status, e)
	}
	// A damaged encrypted body or object envelope must never reach execution.
	var sourceID string
	var original []byte
	if e = s.pool.QueryRow(ctx, "SELECT s.id,s.ciphertext FROM automation_sources s JOIN automation_versions v ON v.source_id=s.id AND v.org_id=s.org_id WHERE v.id=$1", version).Scan(&sourceID, &original); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE automation_sources SET ciphertext=$2 WHERE id=$1", sourceID, []byte("corrupt")); e != nil {
		t.Fatal(e)
	}
	damaged, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	run()
	if e = s.pool.QueryRow(ctx, "SELECT status FROM automation_validations WHERE id=$1", damaged.Msg.Id).Scan(&status); e != nil || status != "failed" || calls != 1 {
		t.Fatal("corrupt source reached runner", status, calls, e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE automation_sources SET ciphertext=$2 WHERE id=$1", sourceID, original); e != nil {
		t.Fatal(e)
	}
	queued, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.pool.Exec(ctx, "UPDATE automation_versions SET status='revoked' WHERE id=$1", version)
	if e != nil {
		t.Fatal(e)
	}
	run()
	if e = s.pool.QueryRow(ctx, "SELECT status FROM automation_validations WHERE id=$1", queued.Msg.Id).Scan(&status); e != nil || status != "canceled" || calls != 1 {
		t.Fatal("revoked version reached runner", status, calls, e)
	}
	// Restore only this fixture so the caller can exercise its lifecycle independently.
	_, e = s.pool.Exec(ctx, "UPDATE automation_versions SET status='published' WHERE id=$1", version)
	if e != nil {
		t.Fatal(e)
	}
	interrupted, e := owner.RequestAutomationValidation(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.pool.Exec(ctx, "UPDATE automation_validations SET status='running',lease_until=now()-interval '1 second' WHERE id=$1", interrupted.Msg.Id)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.automationValidationTick(ctx); e != nil {
		t.Fatal(e)
	}
	rows, e := s.q.ListAutomationValidations(ctx, database.ListAutomationValidationsParams{OrgID: org})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range rows {
		if r.ID == interrupted.Msg.Id {
			found = r.Status == "failed"
		}
	}
	if !found || calls != 1 {
		t.Fatal("expired validation was retried or not failed")
	}

	detail, e := owner.GetAutomationValidation(ctx, connect.NewRequest(&pb.GetAutomationValidationRequest{OrganizationId: org, Id: interrupted.Msg.Id}))
	if e != nil || detail.Msg.Status != "failed" || detail.Msg.VersionId != version {
		t.Fatal("exact validation read failed", e)
	}
	for _, input := range []*pb.GetAutomationValidationRequest{{OrganizationId: org, Id: randomID()}, {OrganizationId: randomID(), Id: interrupted.Msg.Id}} {
		if _, e = owner.GetAutomationValidation(ctx, connect.NewRequest(input)); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("foreign validation read allowed", e)
		}
	}
	if _, e = owner.GetAutomationValidation(ctx, connect.NewRequest(&pb.GetAutomationValidationRequest{OrganizationId: org, Id: "bad"})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("malformed validation ID accepted", e)
	}
	exerciseValidationHistory(t, s, owner, ctx, org, interrupted.Msg.Id)
	// Stale workers cannot overwrite an already expired result.
	if e = s.runAutomationValidation(ctx, database.AutomationValidation{ID: interrupted.Msg.Id, OrgID: org, VersionID: version}); e != nil {
		t.Fatal(e)
	}
}

func exerciseValidationHistory(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, ctx context.Context, org, source string) {
	t.Helper()
	_, err := s.pool.Exec(ctx, `INSERT INTO automation_validations(id,org_id,version_id,requester_id,requester_email,status,created_at)
 SELECT repeat(md5(v.id||n::text),2),v.org_id,v.version_id,v.requester_id,v.requester_email,CASE WHEN n%2=0 THEN 'succeeded' ELSE 'failed' END,'2020-01-01T00:00:00Z'::timestamptz
 FROM automation_validations v CROSS JOIN generate_series(1,205) n WHERE v.id=$1`, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"", "failed", "succeeded"} {
		var expected int
		if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM automation_validations WHERE org_id=$1 AND ($2='' OR status=$2)", org, status).Scan(&expected); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		token := ""
		firstToken := ""
		for page := 0; page < 10; page++ {
			out, err := owner.ListAutomationValidations(ctx, connect.NewRequest(&pb.ListAutomationValidationsRequest{OrganizationId: org, Status: status, PageToken: token}))
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Msg.Validations) > 100 {
				t.Fatal("unbounded validation page")
			}
			for _, row := range out.Msg.Validations {
				if seen[row.Id] || (status != "" && row.Status != status) {
					t.Fatal("duplicate or wrongly filtered validation")
				}
				seen[row.Id] = true
			}
			token = out.Msg.NextPageToken
			if firstToken == "" {
				firstToken = token
			}
			if token == "" {
				break
			}
		}
		if len(seen) != expected || firstToken == "" {
			t.Fatal("validation history incomplete", status, len(seen), expected)
		}
		if status == "failed" {
			_, err = owner.ListAutomationValidations(ctx, connect.NewRequest(&pb.ListAutomationValidationsRequest{OrganizationId: org, Status: "succeeded", PageToken: firstToken}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatal("cursor crossed status filter", err)
			}
		}
	}
	for _, token := range []string{"invalid", cursor(org, "automation-validations:", "bad|"+source), cursor(randomID(), "automation-validations:", "2020-01-01T00:00:00Z|"+source)} {
		_, err = owner.ListAutomationValidations(ctx, connect.NewRequest(&pb.ListAutomationValidationsRequest{OrganizationId: org, PageToken: token}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid history cursor accepted", err)
		}
	}
}
