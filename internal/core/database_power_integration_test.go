//go:build integration

package core

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func exerciseDatabasePower(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org string) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous; s.cfg.RuntimePolicy = nil }()
	connection, e := owner.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "Database power fixture", Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"fake","secret_access_key":"fake-secret"}`}))
	check(e)
	cid := connection.Msg.Connection.Id
	for _, kind := range []string{"database.instance", "database.cluster"} {
		rid := randomID()
		native := "database-one"
		identity := "db-IMMUTABLE"
		set := func(status, identity string) {
			check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: rid, OrgID: org, ConnectionID: cid, Provider: "aws", Kind: kind, NativeID: native, Name: "Database power fixture", Region: "us-east-1", Status: status, ProviderIdentity: identity}))
		}
		set("stopped", identity)
		reads, submissions := 0, 0
		lost := false
		observed := "available"
		s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
			if r.Validate() != nil || r.ConnectionID != cid || r.Power.ResourceKind != kind || r.Power.NativeID != native || r.Power.ExpectedIdentity != identity {
				t.Fatal("database request lost its reviewed identity")
			}
			if r.Power.Phase == "submit" {
				submissions++
				if lost {
					return provider.Response{}, errors.New("lost submission")
				}
				return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "accepted", Status: r.Power.ExpectedStatus}}, nil
			}
			reads++
			return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "succeeded", Status: observed}}, nil
		}
		request := func(action, status, key string) *pb.Operation {
			out, e := owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: rid, Action: action, ExpectedStatus: status, Reason: "Database operation fixture", IdempotencyKey: key}))
			check(e)
			if out.Msg.Operation.ResourceKind != kind {
				t.Fatal("API lost database operation kind")
			}
			return out.Msg.Operation
		}
		state := func(id, want string) {
			o, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
			check(e)
			if o.Status != want || o.ProviderIdentity != identity {
				t.Fatalf("database state=%s identity=%s, want %s: %s", o.Status, o.ProviderIdentity, want, o.Detail)
			}
		}
		run := func(id string) {
			job, e := s.q.ClaimOperation(ctx)
			check(e)
			if job.ID != id {
				t.Fatal("unexpected queued operation")
			}
			check(s.runOperation(ctx, job))
		}
		_, deniedErr := owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: rid, Action: "start", ExpectedStatus: "stopped", Reason: "Community boundary", IdempotencyKey: randomID()}))
		if connect.CodeOf(deniedErr) != connect.CodeFailedPrecondition {
			t.Fatal("unsupported database action accepted", deniedErr)
		}
		// Exercise the shared lifecycle contract with an explicitly composed extended runtime.
		s.cfg.RuntimePolicy = func(r provider.Runtime) provider.Runtime { return r }
		key := randomID()
		start := request("start", "stopped", key)
		state(start.Id, "queued")
		if request("start", "stopped", key).Id != start.Id {
			t.Fatal("database idempotency lost")
		}
		run(start.Id)
		state(start.Id, "observing")
		_, e = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", start.Id)
		check(e)
		run(start.Id)
		state(start.Id, "succeeded")
		if submissions != 1 || reads != 1 {
			t.Fatal("startup did not separate submission and observation")
		}
		set("available", identity)
		stop := request("shutdown", "available", randomID())
		state(stop.Id, "awaiting_approval")
		review := &pb.ReviewOperationRequest{OrganizationId: org, Id: stop.Id, Approve: true, Reason: "Reviewed outage and automatic restart"}
		_, e = owner.ReviewOperation(ctx, connect.NewRequest(review))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("database self-approval allowed")
		}
		_, e = approver.ReviewOperation(ctx, connect.NewRequest(review))
		check(e)
		set("available", "db-REPLACEMENT")
		run(stop.Id)
		state(stop.Id, "canceled")
		if submissions != 1 {
			t.Fatal("replacement database reached dispatch")
		}
		set("available", identity)
		stop = request("shutdown", "available", randomID())
		review.Id = stop.Id
		_, e = approver.ReviewOperation(ctx, connect.NewRequest(review))
		check(e)
		lost = true
		run(stop.Id)
		state(stop.Id, "uncertain")
		reconcile := func() {
			_, e := s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", stop.Id)
			check(e)
			_, e = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: stop.Id}))
			check(e)
			run(stop.Id)
		}
		lost = false
		observed = "available"
		reconcile()
		state(stop.Id, "uncertain") // A claimed success in the wrong state is not completion.
		observed = "stopped"
		reconcile()
		state(stop.Id, "succeeded")
		if submissions != 2 || reads != 3 {
			t.Fatal("reconciliation resubmitted a database mutation")
		}
		set("stopped", identity)
		pending := request("start", "stopped", randomID())
		s.cfg.RuntimePolicy = provider.CommunityRuntime // Capability authorization changed after queueing.
		run(pending.Id)
		state(pending.Id, "canceled")
		if submissions != 2 {
			t.Fatal("revoked capability reached provider submission")
		}
		s.cfg.RuntimePolicy = nil
		set("stopped", "")
		detail, e := owner.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: rid}))
		check(e)
		if len(detail.Msg.AvailableActions) != 0 {
			t.Fatal("legacy inventory without identity exposed mutations")
		}
		_, e = owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: rid, Action: "start", ExpectedStatus: "stopped", Reason: "Refresh required", IdempotencyKey: randomID()}))
		if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("missing immutable identity accepted")
		}
	}
	_, e = owner.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: cid, Enabled: false}))
	check(e)
	_, e = s.pool.Exec(ctx, "UPDATE resources SET deleted_at=now() WHERE connection_id=$1", cid)
	check(e)
}
