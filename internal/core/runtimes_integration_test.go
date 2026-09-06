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

func exerciseRuntimes(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	a, b := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	savedCatalog, savedCall := s.cfg.ProviderRuntimes, s.cfg.ProviderCall
	defer func() {
		s.cfg.ProviderRuntimes = savedCatalog
		s.cfg.ProviderCall = savedCall
		_, _ = s.pool.Exec(ctx, "UPDATE provider_modules SET runtime_id='',revision=revision+1 WHERE org_id=$1", org)
	}()
	catalog := []provider.Runtime{{Provider: "hetzner", Image: a, Version: "1", SDKVersion: "1", Protocol: provider.Protocol}, {Provider: "hetzner", Image: b, Version: "2", SDKVersion: "2", Protocol: provider.Protocol}}
	s.cfg.ProviderRuntimes = catalog
	current := func() database.GetProviderModuleRow {
		row, e := s.q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: org, Provider: "hetzner"})
		check(e)
		return row
	}
	selectRuntime := func(id string) {
		t.Helper()
		_, e := owner.SetProviderRuntime(ctx, connect.NewRequest(&pb.SetProviderRuntimeRequest{OrganizationId: org, Provider: "hetzner", RuntimeId: id, ExpectedRevision: current().Revision, Confirmation: "hetzner"}))
		check(e)
	}
	request := func() *pb.Operation {
		t.Helper()
		_, e := s.pool.Exec(ctx, "UPDATE resources SET status='off',observed_at=now() WHERE id=$1", resource)
		check(e)
		out, e := owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "start", ExpectedStatus: "off", Reason: "Runtime lifecycle test", IdempotencyKey: randomID()}))
		check(e)
		return out.Msg.Operation
	}
	run := func() { t.Helper(); job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	calls := []provider.Request{}
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		calls = append(calls, r)
		outcome := "accepted"
		if r.Power.Phase == "observe" {
			outcome = "succeeded"
		}
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: outcome, ActionID: "42", Status: "running"}}, nil
	}
	selectRuntime(a)
	old := request()
	epoch := current().Revision
	selectRuntime(b)
	run()
	row, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: old.Id})
	check(err)
	if row.Status != "canceled" || row.RuntimeID != a || len(calls) != 0 {
		t.Fatal("old queued runtime executed after update")
	}
	active := request()
	run()
	if len(calls) != 1 || calls[0].RuntimeID != b {
		t.Fatal("new runtime not selected")
	}
	selectRuntime(a)
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", active.Id)
	check(err)
	run()
	if len(calls) != 2 || calls[1].RuntimeID != b || calls[1].Power.Phase != "observe" {
		t.Fatal("rollback switched submitted operation runtime")
	}
	_, err = owner.SetProviderRuntime(ctx, connect.NewRequest(&pb.SetProviderRuntimeRequest{OrganizationId: org, Provider: "hetzner", RuntimeId: b, ExpectedRevision: epoch, Confirmation: "hetzner"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale runtime review accepted", err)
	}
	_, err = owner.SetProviderRuntime(ctx, connect.NewRequest(&pb.SetProviderRuntimeRequest{OrganizationId: org, Provider: "aws", RuntimeId: b, ExpectedRevision: 1, Confirmation: "aws"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("cross-provider runtime accepted", err)
	}
	missing := request()
	run()
	selectRuntime(b)
	s.cfg.ProviderRuntimes = catalog[1:]
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", missing.Id)
	check(err)
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: missing.Id})
	check(err)
	if row.Status != "uncertain" || len(calls) != 3 {
		t.Fatal("missing runtime silently substituted")
	}
	s.cfg.ProviderRuntimes = catalog
	_, err = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", missing.Id)
	check(err)
	_, err = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: missing.Id}))
	check(err)
	run()
	if len(calls) != 4 || calls[3].RuntimeID != a || calls[3].Power.Phase != "observe" {
		t.Fatal("restored runtime not used for read-only recovery")
	}
}
