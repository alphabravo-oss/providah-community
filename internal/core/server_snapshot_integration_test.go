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
)

func exerciseServerSnapshot(t *testing.T, s *Service, owner, reviewer providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource, reviewerID string) {
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous }()
	_, e := s.pool.Exec(ctx, "UPDATE resources SET status='off',observed_at=now() WHERE id=$1", resource)
	check(e)
	submissions := 0
	observed := "789"
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.Validate() != nil || r.Power.Action != "snapshot" || r.Power.NativeID != "234" {
			t.Fatal("wrong snapshot request")
		}
		result := &provider.PowerResult{Outcome: "succeeded", ActionID: observed, Status: "snapshot_available"}
		if r.Power.Phase == "submit" {
			submissions++
			result = &provider.PowerResult{Outcome: "accepted", ActionID: "456", Status: "snapshot_pending"}
		}
		return provider.Response{Version: provider.Protocol, Power: result}, nil
	}
	request := &pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "snapshot", ExpectedStatus: "off", Reason: "Create reviewed recovery image", IdempotencyKey: randomID()}
	first, e := owner.RequestOperation(ctx, connect.NewRequest(request))
	check(e)
	if first.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL {
		t.Fatal("snapshot skipped approval")
	}
	review := &pb.ReviewOperationRequest{OrganizationId: org, Id: first.Msg.Operation.Id, Approve: true, Reason: "Reviewed storage cost and scope"}
	if _, e = owner.ReviewOperation(ctx, connect.NewRequest(review)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("self approved snapshot", e)
	}
	if _, e = reviewer.ReviewOperation(ctx, connect.NewRequest(review)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("snapshot approved without creation grant", e)
	}
	var role string
	check(s.pool.QueryRow(ctx, "SELECT id FROM roles WHERE org_id=$1 AND name='Creation reviewer'", org).Scan(&role))
	setRole := func(role string) {
		_, e := owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: reviewerID, ExpectedRevision: membershipRevision(t, s, ctx, org, reviewerID), RoleId: role, Active: true}))
		check(e)
	}
	setRole(role)
	_, e = reviewer.ReviewOperation(ctx, connect.NewRequest(review))
	check(e)
	run := func() { job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	state := func(want string) {
		row, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
		check(e)
		if row.Status != want {
			t.Fatal("snapshot status", row.Status, row.Detail)
		}
	}
	run()
	state("observing")
	_, e = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", review.Id)
	check(e)
	run()
	state("uncertain")
	observed = "456"
	_, e = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", review.Id)
	check(e)
	_, e = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: review.Id}))
	check(e)
	run()
	state("succeeded")
	if submissions != 1 {
		t.Fatal("snapshot resubmitted")
	}
	request.IdempotencyKey = randomID()
	next, e := owner.RequestOperation(ctx, connect.NewRequest(request))
	check(e)
	review.Id = next.Msg.Operation.Id
	_, e = reviewer.ReviewOperation(ctx, connect.NewRequest(review))
	check(e)
	setRole("approver")
	run()
	state("canceled")
	if submissions != 1 {
		t.Fatal("revoked creation authority reached provider")
	}
}
