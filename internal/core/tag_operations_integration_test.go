//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"strings"
	"testing"
)

func exerciseTagOperations(t *testing.T, s *Service, owner, reviewer providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource string) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	previous, catalog := s.cfg.ProviderCall, s.cfg.ProviderRuntimes
	defer func() { s.cfg.ProviderCall = previous; s.cfg.ProviderRuntimes = catalog }()
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Protocol: provider.Protocol, Version: "test", SDKVersion: "test", CapabilitiesVersion: 1, Actions: []string{"tags"}, InventoryKinds: []string{"compute.server"}}}
	_, e := s.pool.Exec(ctx, `UPDATE resources SET status='off',observed_at=now(),tag_metadata='{"labels":{"env":"old"}}' WHERE id=$1`, resource)
	check(e)
	submissions := 0
	correct := false
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.Validate() != nil || r.Power.Action != "tags" || r.Power.TargetTags.Labels["env"] != "new" || r.Power.ExpectedTags.Labels["env"] != "old" {
			t.Fatal("wrong persisted tag request", r.Power)
		}
		if r.Power.Phase == "submit" {
			submissions++
			return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "accepted"}}, nil
		}
		value := "wrong"
		if correct {
			value = "new"
		}
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "succeeded", Status: "off", Tags: &provider.Tags{Labels: map[string]string{"env": value}}}}, nil
	}
	input := &pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "tags", ExpectedStatus: "off", Reason: "Update reviewed environment", IdempotencyKey: randomID(), ExpectedTags: &pb.ResourceTags{Labels: map[string]string{"env": "old"}}, TargetTags: &pb.ResourceTags{Labels: map[string]string{"env": "new"}}}
	result, e := owner.RequestOperation(ctx, connect.NewRequest(input))
	check(e)
	id := result.Msg.Operation.Id
	if result.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL || result.Msg.Operation.TargetTags.Labels["env"] != "new" {
		t.Fatal("missing review", result.Msg)
	}
	repeat, e := owner.RequestOperation(ctx, connect.NewRequest(input))
	check(e)
	if repeat.Msg.Operation.Id != id {
		t.Fatal("idempotency lost")
	}
	input.TargetTags.Labels["env"] = "other"
	_, e = owner.RequestOperation(ctx, connect.NewRequest(input))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("changed key reused", e)
	}
	input.TargetTags.Labels["env"] = "new"
	_, e = s.pool.Exec(ctx, `UPDATE operations SET target_tags='{}' WHERE id=$1`, id)
	if e == nil {
		t.Fatal("review inputs mutable")
	}
	review := &pb.ReviewOperationRequest{OrganizationId: org, Id: id, Approve: true, Reason: "Reviewed target tags"}
	_, e = owner.ReviewOperation(ctx, connect.NewRequest(review))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("self approval", e)
	}
	_, e = reviewer.ReviewOperation(ctx, connect.NewRequest(review))
	check(e)
	run := func() { job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	state := func(want string) {
		o, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
		check(e)
		if o.Status != want {
			t.Fatal(o.Status, o.Detail)
		}
	}
	run()
	state("observing")
	_, e = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", id)
	check(e)
	run()
	state("uncertain")
	correct = true
	_, e = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", id)
	check(e)
	_, e = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: id}))
	check(e)
	run()
	state("succeeded")
	row, e := s.q.GetResource(ctx, database.GetResourceParams{OrgID: org, ID: resource})
	check(e)
	if storedTags(row.TagMetadata).Labels["env"] != "new" || submissions != 1 {
		t.Fatal("tags not published or mutation repeated")
	}
	input.IdempotencyKey = randomID()
	input.ExpectedTags.Labels["env"] = "new"
	input.TargetTags.Labels["env"] = "later"
	result, e = owner.RequestOperation(ctx, connect.NewRequest(input))
	check(e)
	id = result.Msg.Operation.Id
	review.Id = id
	_, e = reviewer.ReviewOperation(ctx, connect.NewRequest(review))
	check(e)
	_, e = s.pool.Exec(ctx, `UPDATE resources SET tag_metadata='{"labels":{"env":"external"}}' WHERE id=$1`, resource)
	check(e)
	run()
	state("canceled")
	if submissions != 1 {
		t.Fatal("changed inventory dispatched")
	}
}
