//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
)

func exerciseKeyCreation(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, connection string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	oldCall, oldRuntimes := s.cfg.ProviderCall, s.cfg.ProviderRuntimes
	defer func() { s.cfg.ProviderCall = oldCall; s.cfg.ProviderRuntimes = oldRuntimes }()
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: "hetzner", Image: "sha256:" + strings.Repeat("e", 64), CapabilitiesVersion: 1, InventoryKinds: []string{"access.ssh_key"}, Actions: []string{"create"}, SSHKeyCreate: true}}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	key, err := ssh.NewPublicKey(public)
	check(err)
	raw := string(ssh.MarshalAuthorizedKey(key))
	calls := 0
	mode, native := "", "87654"
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.Validate() != nil || r.Power.KeyCreate == nil || r.Power.KeyCreate.PublicKey != raw || r.Power.Create != nil || r.Power.ResourceKind != "access.ssh_key" {
			t.Fatal("wrong key creation request")
		}
		result := &provider.PowerResult{Outcome: "succeeded", NativeID: native, Status: "present"}
		if r.Power.Phase == "submit" {
			calls++
			if mode == "lost" {
				return provider.Response{}, errors.New("lost response")
			}
		} else if mode == "wrong" {
			result.Status = "running"
		}
		return provider.Response{Version: provider.Protocol, Power: result}, nil
	}
	input := &pb.RequestSSHKeyCreationRequest{OrganizationId: org, ConnectionId: connection, Region: "global", Creation: &pb.SSHKeyCreate{Name: "imported-key", PublicKey: raw}, Reason: "Import reviewed public key", IdempotencyKey: randomID()}
	if _, err = approver.RequestSSHKeyCreation(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("request accepted without request authority", err)
	}
	input.Creation.PublicKey = "private material\n" + raw
	if _, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("mixed private material accepted", err)
	}
	input.Creation.PublicKey = raw
	input.Region = "fsn1"
	if _, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("regional Hetzner key accepted", err)
	}
	input.Region = "global"
	var oldRegion string
	check(s.pool.QueryRow(ctx, "SELECT region FROM connections WHERE id=$1", connection).Scan(&oldRegion))
	_, err = s.pool.Exec(ctx, "UPDATE connections SET region='fsn1' WHERE id=$1", connection)
	check(err)
	defer func() {
		_, e := s.pool.Exec(ctx, "UPDATE connections SET region=$2 WHERE id=$1", connection, oldRegion)
		check(e)
	}()
	created, err := owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input))
	check(err)
	if created.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL || created.Msg.Operation.KeyCreation == nil || created.Msg.Operation.Creation != nil || created.Msg.Operation.ResourceKind != "access.ssh_key" {
		t.Fatal("incorrect key review")
	}
	same, err := owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input))
	check(err)
	if same.Msg.Operation.Id != created.Msg.Operation.Id {
		t.Fatal("key idempotency lost")
	}
	input.Creation.Name = "changed-key"
	if _, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("changed input accepted", err)
	}
	input.Creation.Name = "imported-key"
	id := created.Msg.Operation.Id
	if _, err = s.pool.Exec(ctx, "UPDATE operations SET creation='{}'::jsonb WHERE id=$1", id); err == nil {
		t.Fatal("key input mutable")
	}
	review := &pb.ReviewOperationRequest{OrganizationId: org, Id: id, Approve: true, Reason: "Reviewed exact public key"}
	if _, err = owner.ReviewOperation(ctx, connect.NewRequest(review)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("self approval accepted", err)
	}
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	run := func() { job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	run()
	operation, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	check(err)
	if operation.Status != "observing" || operation.NativeID != native || operation.ResourceID.Valid || calls != 1 {
		t.Fatal("submission was treated as observed inventory")
	}
	mode = "wrong"
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", id)
	check(err)
	run()
	operation, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	check(err)
	if operation.Status != "uncertain" || operation.ResourceID.Valid || calls != 1 {
		t.Fatal("invalid observed key state accepted")
	}
	reconcile := func() {
		_, e := s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", id)
		check(e)
		_, e = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: id}))
		check(e)
		run()
	}
	mode = ""
	reconcile()
	operation, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	check(err)
	if operation.Status != "succeeded" || calls != 1 || !operation.ResourceID.Valid {
		t.Fatal("key creation did not observe once", operation.Status, calls)
	}
	resource, err := s.q.GetResource(ctx, database.GetResourceParams{OrgID: org, ID: operation.ResourceID.String})
	check(err)
	if resource.Kind != "access.ssh_key" || resource.Name != "imported-key-"+id || resource.NativeID != "87654" {
		t.Fatal("incorrect key inventory")
	}
	mode, native = "lost", "87655"
	input.Creation.Name, input.IdempotencyKey = "lost-key", randomID()
	created, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input))
	check(err)
	id, review.Id = created.Msg.Operation.Id, created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	run()
	operation, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	check(err)
	if operation.Status != "uncertain" || operation.ResourceID.Valid || calls != 2 {
		t.Fatal("lost response did not remain uncertain")
	}
	input.IdempotencyKey = randomID()
	if _, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("uncertain key allowed another import", err)
	}
	mode = ""
	reconcile()
	operation, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	check(err)
	if operation.Status != "succeeded" || operation.NativeID != native || !operation.ResourceID.Valid || calls != 2 {
		t.Fatal("lost key was not recovered by reads only")
	}
	input.Creation.Name, input.IdempotencyKey = "unsupported-key", randomID()
	created, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input))
	check(err)
	id, review.Id = created.Msg.Operation.Id, created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	s.cfg.ProviderRuntimes[0].SSHKeyCreate = false
	run()
	operation, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	check(err)
	if operation.Status != "canceled" || calls != 2 {
		t.Fatal("unsupported key runtime dispatched")
	}
	input.IdempotencyKey = randomID()
	if _, err = owner.RequestSSHKeyCreation(ctx, connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("unsupported key runtime accepted request", err)
	}
}
