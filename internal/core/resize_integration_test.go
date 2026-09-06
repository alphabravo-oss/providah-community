//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"google.golang.org/protobuf/proto"
	"testing"
)

func exerciseResize(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource string) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	old := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = old }()
	setSize := func(size string) {
		_, e := s.pool.Exec(ctx, "UPDATE resources SET size=$2,status='off',observed_at=now() WHERE id=$1", resource, size)
		check(e)
	}
	setSize("cx23")
	submissions := 0
	reported := ""
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.Validate() != nil || r.Power.Action != "resize" || r.Power.ExpectedSize != "cx23" || r.Power.TargetSize != "cx33" {
			t.Fatal("resize lost reviewed input")
		}
		if r.Power.Phase == "submit" {
			submissions++
			return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "accepted", Status: "off", ActionID: "42"}}, nil
		}
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "succeeded", Status: "off", Size: reported}}, nil
	}
	request := &pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "resize", ExpectedStatus: "off", ExpectedSize: "cx23", TargetSize: "cx33", Reason: "Reviewed CPU and RAM change", IdempotencyKey: randomID()}
	created, e := owner.RequestOperation(ctx, connect.NewRequest(request))
	check(e)
	if created.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL || created.Msg.Operation.ExpectedSize != "cx23" || created.Msg.Operation.TargetSize != "cx33" {
		t.Fatal("missing immutable resize review")
	}
	same, e := owner.RequestOperation(ctx, connect.NewRequest(request))
	check(e)
	if same.Msg.Operation.Id != created.Msg.Operation.Id {
		t.Fatal("lost idempotency")
	}
	changed := proto.Clone(request).(*pb.RequestOperationRequest)
	changed.TargetSize = "cx43"
	_, e = owner.RequestOperation(ctx, connect.NewRequest(changed))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("request key reused for changed type", e)
	}
	review := &pb.ReviewOperationRequest{OrganizationId: org, Id: created.Msg.Operation.Id, Approve: true, Reason: "Reviewed exact sizes and charges"}
	_, e = owner.ReviewOperation(ctx, connect.NewRequest(review))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("self approval allowed", e)
	}
	_, e = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(e)
	run := func() { job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	state := func(id, want string) {
		row, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
		check(e)
		if row.Status != want {
			t.Fatalf("got %s want %s: %s", row.Status, want, row.Detail)
		}
	}
	run()
	state(review.Id, "observing")
	_, e = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", review.Id)
	check(e)
	run()
	state(review.Id, "uncertain") // A provider success without the target type is insufficient.
	reported = "cx33"
	_, e = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", review.Id)
	check(e)
	_, e = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: review.Id}))
	check(e)
	run()
	state(review.Id, "succeeded")
	if submissions != 1 {
		t.Fatal("resize was resubmitted during reconciliation")
	}
	request.IdempotencyKey = randomID()
	created, e = owner.RequestOperation(ctx, connect.NewRequest(request))
	check(e)
	review.Id = created.Msg.Operation.Id
	_, e = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(e)
	setSize("cx43")
	run()
	state(review.Id, "canceled")
	if submissions != 1 {
		t.Fatal("changed size reached provider")
	}
	request.IdempotencyKey = randomID()
	_, e = owner.RequestOperation(ctx, connect.NewRequest(request))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale source requested", e)
	}
	setSize("cx23")
	request.TargetSize = "cx23"
	_, e = owner.RequestOperation(ctx, connect.NewRequest(request))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("same type accepted", e)
	}
	legacy := provider.Runtime{Provider: "hetzner"}
	if legacy.SupportsResourceAction("compute.server", "resize") {
		t.Fatal("legacy runtime accepted resize")
	}
}
