//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"testing"
)

func exerciseOperations(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, approverID string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "approver", Active: true}))
	check(err)
	connections, err := owner.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	check(err)
	connection := connections.Msg.Connections[0]
	_, err = owner.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: connection.Id, Enabled: true}))
	check(err)
	resourceID := randomID()
	setState := func(state string) {
		check(s.q.UpsertResource(ctx, database.UpsertResourceParams{Kind: "compute.server", ID: resourceID, OrgID: org, ConnectionID: connection.Id, NativeID: "234", Name: "Action test server", Provider: "hetzner", Region: "fsn1", Status: state, Size: "cx23"}))
	}
	setState("off")
	calls, submissions := 0, 0
	ambiguous := false
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous }()
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		calls++
		if r.ConnectionID != connection.Id || r.OrganizationID != org || r.Power == nil || r.Power.NativeID != "234" {
			t.Fatal("wrong operation scope")
		}
		if r.Power.Phase == "submit" {
			submissions++
			if ambiguous {
				return provider.Response{}, errors.New("sensitive lost response")
			}
			return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "accepted", ActionID: "42", Status: r.Power.ExpectedStatus}}, nil
		}
		status := "running"
		if r.Power.Action == "shutdown" {
			status = "off"
		}
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "succeeded", ActionID: "42", Status: status}}, nil
	}
	request := func(action, state, key string) *pb.Operation {
		r, err := owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resourceID, Action: action, ExpectedStatus: state, Reason: "Test operational change", IdempotencyKey: key}))
		check(err)
		return r.Msg.Operation
	}
	run := func() { job, err := s.q.ClaimOperation(ctx); check(err); check(s.runOperation(ctx, job)) }
	status := func(id, want string) {
		row, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
		check(err)
		if row.Status != want {
			t.Fatalf("operation status got %s want %s (%s)", row.Status, want, row.Detail)
		}
	}
	approve := func(id string) {
		_, err := approver.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: id, Approve: true, Reason: "Approved exact target"}))
		check(err)
	}
	key := randomID()
	start := request("start", "off", key)
	baseline, e := owner.GetOperationsOverview(ctx, connect.NewRequest(&pb.GetOperationsOverviewRequest{OrganizationId: org}))
	check(e)
	prefix := randomID()
	_, e = s.pool.Exec(ctx, `INSERT INTO resources SELECT (jsonb_populate_record(NULL::resources,to_jsonb(r)||jsonb_build_object('id',md5($2::text||'-resource-'||n::text)||md5(n::text||$2::text),'native_id',(900000+n)::text,'name','Overview fixture'))).* FROM resources r CROSS JOIN generate_series(1,16) n WHERE r.id=$1`, resourceID, prefix)
	check(e)
	_, e = s.pool.Exec(ctx, `INSERT INTO operations SELECT (jsonb_populate_record(NULL::operations,to_jsonb(o)||jsonb_build_object('id',md5($2::text||n::text)||md5(n::text||$2::text),'resource_id',md5($2::text||'-resource-'||n::text)||md5(n::text||$2::text),'idempotency_key',$2::text||n::text,'created_at',now()-interval '30 days','updated_at',now()-interval '30 days','status',CASE WHEN n<=12 THEN 'awaiting_approval' WHEN n=14 THEN 'uncertain' WHEN n=15 THEN 'queued' ELSE 'failed' END))).* FROM operations o CROSS JOIN generate_series(1,16) n WHERE o.id=$1`, start.Id, prefix)
	check(e)
	summary, e := owner.GetOperationsOverview(ctx, connect.NewRequest(&pb.GetOperationsOverviewRequest{OrganizationId: org}))
	check(e)
	if summary.Msg.Pending != baseline.Msg.Pending+12 || summary.Msg.Active != baseline.Msg.Active+1 || summary.Msg.Failed != baseline.Msg.Failed+2 || summary.Msg.Uncertain != baseline.Msg.Uncertain+1 || len(summary.Msg.Approvals) != 10 {
		t.Fatal("overview totals truncated to visible rows", summary.Msg)
	}
	for _, op := range summary.Msg.Approvals {
		if op.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL {
			t.Fatal("wrong approval category")
		}
	}
	for _, op := range summary.Msg.Problems {
		if op.Status != pb.OperationStatus_OPERATION_STATUS_FAILED && op.Status != pb.OperationStatus_OPERATION_STATUS_UNCERTAIN {
			t.Fatal("wrong problem category")
		}
	}
	_, e = owner.GetOperationsOverview(ctx, connect.NewRequest(&pb.GetOperationsOverviewRequest{OrganizationId: randomID()}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign overview readable", e)
	}
	_, e = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "viewer", Active: true}))
	check(e)
	_, e = approver.GetOperationsOverview(ctx, connect.NewRequest(&pb.GetOperationsOverviewRequest{OrganizationId: org}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("overview bypassed operations.read", e)
	}
	_, e = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "approver", Active: true}))
	check(e)
	_, e = s.pool.Exec(ctx, "DELETE FROM operations WHERE org_id=$1 AND left(idempotency_key,64)=$2", org, prefix)
	check(e)
	_, e = s.pool.Exec(ctx, "DELETE FROM resources WHERE org_id=$1 AND id IN (SELECT md5($2::text||'-resource-'||n::text)||md5(n::text||$2::text) FROM generate_series(1,16) n)", org, prefix)
	check(e)

	_, err = s.pool.Exec(ctx, `UPDATE resources SET tag_metadata='{"labels":{"env":"production"}}'::jsonb WHERE id=$1`, resourceID)
	check(err)
	for _, tc := range []struct {
		value string
		want  int
	}{{"production", 1}, {"Production", 0}} {
		activity, e := owner.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: org, ResourceFilters: &pb.InventoryViewSpec{TagKey: "env", TagValue: tc.value, Search: "Action test server"}}))
		check(e)
		if len(activity.Msg.Operations) != tc.want {
			t.Fatal("activity filter mismatch")
		}
		if tc.want == 1 && activity.Msg.Operations[0].Id != start.Id {
			t.Fatal("activity returned wrong operation")
		}
	}
	_, err = owner.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: org, ResourceFilters: &pb.InventoryViewSpec{}, PageToken: cursor(org, "operations", start.Id)}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unfiltered cursor reused for activity")
	}
	_, err = owner.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: org, ResourceFilters: &pb.InventoryViewSpec{TagValue: "no key"}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid activity filter accepted")
	}
	_, err = owner.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: randomID(), ResourceFilters: &pb.InventoryViewSpec{}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-organization activity allowed")
	}

	activity, e := owner.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{OrganizationId: org, ResourceFilters: &pb.InventoryViewSpec{ConnectionId: connection.Id, Region: "fsn1", Status: "off", TagConditions: []*pb.TagCondition{{Key: "env", Value: "production"}, {Key: "env", Exists: true}}}}))
	check(e)
	if len(activity.Msg.Operations) != 1 || activity.Msg.Operations[0].Id != start.Id {
		t.Fatal("combined activity filter failed")
	}
	if request("start", "off", key).Id != start.Id {
		t.Fatal("idempotency duplicated intent")
	}
	run()
	status(start.Id, "observing")
	if submissions != 1 {
		t.Fatal("bad submission count")
	}
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", start.Id)
	check(err)
	run()
	status(start.Id, "succeeded")
	if submissions != 1 {
		t.Fatal("observation resubmitted action")
	}
	setState("running")
	shutdown := request("shutdown", "running", randomID())
	status(shutdown.Id, "awaiting_approval")
	read, e := owner.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{OrganizationId: org, Id: shutdown.Id}))
	check(e)
	if read.Msg.Operation.Id != shutdown.Id || read.Msg.Operation.CanReview {
		t.Fatal("wrong direct operation review authority")
	}
	read, e = approver.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{OrganizationId: org, Id: shutdown.Id}))
	check(e)
	if !read.Msg.Operation.CanReview {
		t.Fatal("independent reviewer missing")
	}
	for _, target := range []*pb.GetOperationRequest{{OrganizationId: randomID(), Id: shutdown.Id}, {OrganizationId: org, Id: randomID()}} {
		_, e = owner.GetOperation(ctx, connect.NewRequest(target))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("operation identity leaked outside scope", e)
		}
	}
	_, e = owner.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{OrganizationId: org, Id: "invalid"}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid operation ID accepted")
	}
	_, e = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "viewer", Active: true}))
	check(e)
	_, e = approver.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{OrganizationId: org, Id: shutdown.Id}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("revoked operation read retained")
	}
	_, e = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "approver", Active: true}))
	check(e)

	_, err = owner.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: shutdown.Id, Approve: true, Reason: "Self approval attempt"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("self approval accepted")
	}
	approve(shutdown.Id)
	_, err = owner.RotateCredential(ctx, connect.NewRequest(&pb.RotateCredentialRequest{OrganizationId: org, Id: connection.Id, Credential: "changed-test-token"}))
	check(err)
	before := calls
	run()
	status(shutdown.Id, "canceled")
	if calls != before {
		t.Fatal("changed credential reached provider")
	}
	shutdown = request("shutdown", "running", randomID())
	approve(shutdown.Id)
	ambiguous = true
	run()
	status(shutdown.Id, "uncertain")
	_, err = owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resourceID, Action: "restart", ExpectedStatus: "running", Reason: "Conflicting work", IdempotencyKey: randomID()}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("uncertain work did not fence resource")
	}
	_, err = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: shutdown.Id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("live worker lease ignored")
	}
	_, err = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", shutdown.Id)
	check(err)
	_, err = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: shutdown.Id}))
	check(err)
	before = submissions
	run()
	status(shutdown.Id, "succeeded")
	if submissions != before {
		t.Fatal("reconciliation resubmitted mutation")
	}
	ambiguous = false
	// Lost worker leases stay uncertain and are never reclaimed for dispatch.
	restart := request("restart", "running", randomID())
	approve(restart.Id)
	job, err := s.q.ClaimOperation(ctx)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", job.ID)
	check(err)
	before = calls
	check(s.operationTick(ctx))
	status(restart.Id, "uncertain")
	if calls != before {
		t.Fatal("expired dispatch was repeated")
	}
	_, err = owner.ResolveOperation(ctx, connect.NewRequest(&pb.ResolveOperationRequest{OrganizationId: org, Id: restart.Id, Reason: "Verified externally by requester"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("requester resolved own uncertain action")
	}
	_, err = approver.ResolveOperation(ctx, connect.NewRequest(&pb.ResolveOperationRequest{OrganizationId: org, Id: restart.Id, Reason: "Verified provider action and running server independently"}))
	check(err)
	status(restart.Id, "resolved")
	// Approval revocation is checked at dispatch, not just at review time.
	restart = request("restart", "running", randomID())
	approve(restart.Id)
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "viewer", Active: true}))
	check(err)
	before = calls
	run()
	status(restart.Id, "canceled")
	if calls != before {
		t.Fatal("revoked approver authority reached provider")
	}
	// A requester can cancel pending work without claiming an already submitted action stopped.
	restart = request("restart", "running", randomID())
	_, err = owner.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: restart.Id}))
	check(err)
	status(restart.Id, "canceled")
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "approver", Active: true}))
	check(err)
	setState("off")

	// Approval modes keep existing requests pending and recheck queued work at dispatch.
	policy, err := owner.GetResourcePolicy(ctx, connect.NewRequest(&pb.GetResourcePolicyRequest{OrganizationId: org}))
	check(err)
	originalActions := policy.Msg.ApprovalActions
	saveApprovals := func(actions []string) {
		t.Helper()
		current, e := owner.GetResourcePolicy(ctx, connect.NewRequest(&pb.GetResourcePolicyRequest{OrganizationId: org}))
		check(e)
		_, e = owner.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: current.Msg.CreationEnabled, ApprovalActions: actions, ExpectedRevision: current.Msg.Revision, Reason: "Exercise action approval policy"}))
		check(e)
	}
	for _, actions := range [][]string{{"unknown"}, {"restart", "restart"}} {
		_, e := owner.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: true, ApprovalActions: actions, ExpectedRevision: policy.Msg.Revision, Reason: "Invalid approval policy"}))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatal("invalid approval actions accepted", e)
		}
	}
	_, err = approver.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: true, ExpectedRevision: policy.Msg.Revision, Reason: "Unauthorized bypass"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("non-admin changed approvals", err)
	}
	setState("running")
	pending := request("restart", "running", randomID())
	saveApprovals(nil)
	status(pending.Id, "awaiting_approval")
	_, err = owner.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: pending.Id}))
	check(err)
	direct := request("shutdown", "running", randomID())
	status(direct.Id, "queued")
	run()
	status(direct.Id, "observing")
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", direct.Id)
	check(err)
	run()
	status(direct.Id, "succeeded")
	setState("running")
	direct = request("restart", "running", randomID())
	saveApprovals([]string{"restart"})
	before = calls
	run()
	status(direct.Id, "canceled")
	if calls != before {
		t.Fatal("tightened approval policy reached provider")
	}
	pending = request("restart", "running", randomID())
	status(pending.Id, "awaiting_approval")
	_, err = owner.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: pending.Id}))
	check(err)
	direct = request("shutdown", "running", randomID())
	status(direct.Id, "queued")
	_, err = owner.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: direct.Id}))
	check(err)
	saveApprovals([]string{"create", "start", "shutdown", "restart", "resize", "delete", "snapshot", "tags"})
	setState("off")
	pending = request("start", "off", randomID())
	status(pending.Id, "awaiting_approval")
	_, err = owner.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: pending.Id}))
	check(err)
	saveApprovals(originalActions)
	exerciseTagOperations(t, s, owner, approver, ctx, org, resourceID)
	exerciseResize(t, s, owner, approver, ctx, org, resourceID)
	exerciseDatabasePower(t, s, owner, approver, ctx, org)
	exerciseSources(t, s, owner, approver, ctx, org, connection.Id)
	exerciseCreation(t, s, owner, approver, ctx, org, connection.Id, approverID)
	exerciseServerSnapshot(t, s, owner, approver, ctx, org, resourceID, approverID)
	exerciseDeletion(t, s, owner, approver, ctx, org, resourceID, approverID)
	setState("off")
	exerciseSchedules(t, s, owner, approver, ctx, org, resourceID)
	exerciseMaintenance(t, s, owner, approver, ctx, org, resourceID, connection.Id, approverID)
	exerciseScheduleSlots(t, s, owner, approver, ctx, org, resourceID)
	exerciseModules(t, s, owner, approver, ctx, org, resourceID, connection.Id)
	exerciseRuntimes(t, s, owner, ctx, org, resourceID)

}
