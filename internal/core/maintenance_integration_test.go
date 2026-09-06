//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"strings"
	"testing"
	"time"
)

func exerciseMaintenance(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource, connection, approverID string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE org_id=$1 AND resource_id=$2 AND status='observing'", org, resource)
	check(err)
	check(s.operationTick(ctx))
	// The preceding test removed a schedule after submission. Resolve its
	// deliberately uncertain fixture through independent review first.
	previous, err := s.q.ListOperations(ctx, database.ListOperationsParams{OrgID: org, ID: "~"})
	check(err)
	for _, o := range previous {
		if o.ResourceID.String == resource && o.Status == "uncertain" {
			_, err = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", o.ID)
			check(err)
			_, err = approver.ResolveOperation(ctx, connect.NewRequest(&pb.ResolveOperationRequest{OrganizationId: org, Id: o.ID, Reason: "Verified the fixture provider outcome independently"}))
			check(err)
		}
	}

	_, err = s.pool.Exec(ctx, "UPDATE resources SET status='off',observed_at=now() WHERE org_id=$1 AND id=$2", org, resource)
	check(err)
	policy := &pb.SaveMaintenancePolicyRequest{OrganizationId: org, Name: "Restricted maintenance", Timezone: "UTC", Cron: "* * * * *", DurationMinutes: 60, Exceptions: []string{time.Now().UTC().Format("2006-01-02")}, ConnectionIds: []string{connection}}
	_, err = owner.SaveMaintenancePolicy(ctx, connect.NewRequest(policy))
	check(err)
	list, err := owner.ListMaintenancePolicies(ctx, connect.NewRequest(&pb.ListMaintenancePoliciesRequest{OrganizationId: org}))
	check(err)
	policy.Id = list.Msg.Policies[0].Id
	if list.Msg.Policies[0].OpenNow {
		t.Fatal("exception date not closed")
	}
	request := &pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "start", ExpectedStatus: "off", Reason: "Maintenance test", IdempotencyKey: randomID()}
	_, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("closed maintenance allowed operation", err)
	}
	request.MaintenanceExceptionReason = "Emergency recovery with separately reviewed scope"
	result, err := owner.RequestOperation(ctx, connect.NewRequest(request))
	check(err)
	operation := result.Msg.Operation
	if operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL {
		t.Fatal("exception startup bypassed independent review")
	}
	_, err = owner.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: operation.Id, Approve: true, Reason: "Own exception"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("self exception approved", err)
	}
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Name: "Reviewer without exceptions", Permissions: []string{"resources.read", "operations.read", "operations.approve", "maintenance.read"}}))
	check(err)
	access, err := owner.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	check(err)
	noExceptionRole := ""
	for _, r := range access.Msg.Roles {
		if r.Name == "Reviewer without exceptions" {
			noExceptionRole = r.Id
		}
	}
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: noExceptionRole, Active: true}))
	check(err)
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: operation.Id, Approve: true, Reason: "Normal review alone is insufficient"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("normal approval bypassed exception permission", err)
	}
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "approver", Active: true}))
	check(err)
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: operation.Id, Approve: true, Reason: "Independent emergency review"}))
	check(err)
	check(s.operationTick(ctx))
	row, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: operation.Id})
	check(err)
	if row.Status != "observing" {
		t.Fatal("approved exception failed to dispatch", row.Status, row.Detail)
	}
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", row.ID)
	check(err)
	check(s.operationTick(ctx))
	// A new policy revision cancels an earlier request instead of silently widening approval.
	policy.Exceptions = nil
	_, err = owner.SaveMaintenancePolicy(ctx, connect.NewRequest(policy))
	check(err)
	request.IdempotencyKey = randomID()
	request.MaintenanceExceptionReason = ""
	result, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	check(err)
	_, err = owner.SaveMaintenancePolicy(ctx, connect.NewRequest(policy))
	check(err)
	check(s.operationTick(ctx))
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: result.Msg.Operation.Id})
	check(err)
	if row.Status != "canceled" || !strings.Contains(row.Detail, "policy changed") {
		t.Fatal("policy edit did not cancel stale approval", row.Status, row.Detail)
	}
	request.IdempotencyKey = randomID()
	result, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE operations SET maintenance_expires_at=now()-interval '1 second' WHERE id=$1", result.Msg.Operation.Id)
	check(err)
	check(s.operationTick(ctx))
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: result.Msg.Operation.Id})
	check(err)
	if row.Status != "canceled" || !strings.Contains(row.Detail, "window ended") {
		t.Fatal("work escaped its original window", row.Status, row.Detail)
	}
	// Schedules bind approval to the current maintenance policy and never gain an exception.
	scheduled, err := owner.SaveSchedule(ctx, connect.NewRequest(&pb.SaveScheduleRequest{OrganizationId: org, Name: "Maintenance schedule", ResourceIds: []string{resource}, Spec: &pb.ScheduleSpec{Action: "start", Timezone: "UTC", Cron: "* * * * *"}}))
	check(err)
	_, err = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: scheduled.Msg.Schedule.Id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, scheduled.Msg.Schedule.Id)}))
	check(err)
	policy.Exceptions = []string{time.Now().UTC().Format("2006-01-02")}
	_, err = owner.SaveMaintenancePolicy(ctx, connect.NewRequest(policy))
	check(err)
	schedules, err := owner.ListSchedules(ctx, connect.NewRequest(&pb.ListSchedulesRequest{OrganizationId: org}))
	check(err)
	for _, schedule := range schedules.Msg.Schedules {
		if schedule.Id == scheduled.Msg.Schedule.Id && schedule.Approved {
			t.Fatal("policy edit retained standing approval")
		}
	}
	_, err = approver.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: scheduled.Msg.Schedule.Id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, scheduled.Msg.Schedule.Id)}))
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET next_due=now()-interval '1 second' WHERE id=$1", scheduled.Msg.Schedule.Id)
	check(err)
	check(s.scheduleTick(ctx, time.Now()))
	history, err := owner.ListScheduleOccurrences(ctx, connect.NewRequest(&pb.ListScheduleOccurrencesRequest{OrganizationId: org}))
	check(err)
	if history.Msg.Occurrences[0].Outcome != "skipped" || !strings.Contains(history.Msg.Occurrences[0].Detail, "Maintenance window closed") {
		t.Fatal("schedule bypassed closed window", history.Msg.Occurrences[0])
	}
	// Exception authority is rechecked at dispatch even when the normal window is open.
	policy.Exceptions = nil
	_, err = owner.SaveMaintenancePolicy(ctx, connect.NewRequest(policy))
	check(err)
	request.IdempotencyKey = randomID()
	request.MaintenanceExceptionReason = "Independent maintenance exception for recovery"
	result, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	check(err)
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(&pb.ReviewOperationRequest{OrganizationId: org, Id: result.Msg.Operation.Id, Approve: true, Reason: "Independent recovery review"}))
	check(err)
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: noExceptionRole, Active: true}))
	check(err)
	check(s.operationTick(ctx))
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: result.Msg.Operation.Id})
	check(err)
	if row.Status != "canceled" {
		t.Fatal("revoked exception authority dispatched", row)
	}
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: "approver", Active: true}))
	check(err)
	_, err = owner.DeleteMaintenancePolicy(ctx, connect.NewRequest(&pb.DeleteMaintenancePolicyRequest{OrganizationId: org, Id: policy.Id, Confirmation: "wrong"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("delete bypassed confirmation", err)
	}
	_, err = owner.DeleteMaintenancePolicy(ctx, connect.NewRequest(&pb.DeleteMaintenancePolicyRequest{OrganizationId: org, Id: policy.Id, Confirmation: policy.Name}))
	check(err)
}
