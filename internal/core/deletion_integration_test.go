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
	"slices"
	"strings"
	"testing"
)

func exerciseDeletion(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource, approverID string) {
	exerciseResourceDeletion(t, s, owner, approver, ctx, org, resource, approverID, "compute.server")
	var hzConnection string
	if e := s.pool.QueryRow(ctx, "SELECT connection_id FROM resources WHERE id=$1", resource).Scan(&hzConnection); e != nil {
		t.Fatal(e)
	}
	hzLB := randomID()
	if e := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: hzLB, OrgID: org, ConnectionID: hzConnection, Provider: "hetzner", Kind: "network.load_balancer", NativeID: "4567", Name: "Routing service", Region: "fsn1", Status: "present"}); e != nil {
		t.Fatal(e)
	}
	exerciseResourceDeletion(t, s, owner, approver, ctx, org, hzLB, approverID, "network.load_balancer")

	created, err := owner.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "Snapshot deletion account", Provider: "digitalocean", Credential: "snapshot-test-token"}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := randomID()
	if err := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: snapshot, OrgID: org, ConnectionID: created.Msg.Connection.Id, Provider: "digitalocean", Kind: "storage.snapshot", NativeID: "456", Name: "Recovery snapshot", Region: "nyc3", Status: "present"}); err != nil {
		t.Fatal(err)
	}
	exerciseResourceDeletion(t, s, owner, approver, ctx, org, snapshot, approverID, "storage.snapshot")
	volume := randomID()
	if err := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: volume, OrgID: org, ConnectionID: created.Msg.Connection.Id, Provider: "digitalocean", Kind: "storage.volume", NativeID: "12345678-1234-1234-1234-123456789abc", Name: "Detached data", Region: "nyc3", Status: "available"}); err != nil {
		t.Fatal(err)
	}
	exerciseResourceDeletion(t, s, owner, approver, ctx, org, volume, approverID, "storage.volume")
	for _, kind := range []string{"organization.project", "network.network", "network.firewall", "access.ssh_key", "network.load_balancer"} {
		id := randomID()
		status, region := "present", "nyc3"
		native := "12345678-1234-1234-1234-123456789abc"
		if kind == "organization.project" {
			region = "global"
		}
		if kind == "network.load_balancer" {
			status = "active"
		}
		if kind == "access.ssh_key" {
			region, native = "global", "654"
		}
		if kind == "network.firewall" {
			status, region = "succeeded", "global"
		}
		if e := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: created.Msg.Connection.Id, Provider: "digitalocean", Kind: kind, NativeID: native, Name: "Unused " + kind, Region: region, Status: status}); e != nil {
			t.Fatal(e)
		}
		exerciseResourceDeletion(t, s, owner, approver, ctx, org, id, approverID, kind)
	}
	awsConnection, e := owner.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "Security group fixture", Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"fake","secret_access_key":"fake-secret"}`}))
	if e != nil {
		t.Fatal(e)
	}
	id := randomID()
	if e := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: awsConnection.Msg.Connection.Id, Provider: "aws", Kind: "network.firewall", NativeID: "sg-12345678", Name: "Unused security group", Region: "us-east-1", Status: "present"}); e != nil {
		t.Fatal(e)
	}
	exerciseResourceDeletion(t, s, owner, approver, ctx, org, id, approverID, "network.firewall")
	for _, native := range []string{"classic-edge", "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/edge/0123456789abcdef"} {
		id := randomID()
		status := "present"
		if strings.HasPrefix(native, "arn:") {
			status = "active"
		}
		if e := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: awsConnection.Msg.Connection.Id, Provider: "aws", Kind: "network.load_balancer", NativeID: native, Name: "AWS routing service", Region: "us-east-1", Status: status}); e != nil {
			t.Fatal(e)
		}
		exerciseResourceDeletion(t, s, owner, approver, ctx, org, id, approverID, "network.load_balancer")
	}
	for _, target := range []struct{ cloud, connection, native, region, status string }{{"aws", awsConnection.Msg.Connection.Id, "pg-1234567890abcdef0", "us-east-1", "available"}, {"hetzner", hzConnection, "879", "global", "present"}} {
		id := randomID()
		if e := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: target.connection, Provider: target.cloud, Kind: "compute.placement_group", NativeID: target.native, Name: "Empty placement", Region: target.region, Status: target.status}); e != nil {
			t.Fatal(e)
		}
		exerciseResourceDeletion(t, s, owner, approver, ctx, org, id, approverID, "compute.placement_group")
	}
	modules, e := owner.ListProviderModules(ctx, connect.NewRequest(&pb.ListProviderModulesRequest{OrganizationId: org}))
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range modules.Msg.Modules {
		if m.Provider == "aws" && (!slices.Contains(m.Capabilities, "compute.image.delete") || slices.Contains(m.Capabilities, "database.snapshot.delete") || slices.Contains(m.Capabilities, "database.cluster_snapshot.delete")) {
			t.Fatal("incorrect Community snapshot capabilities")
		}
	}
	for _, kind := range []string{"compute.image", "database.snapshot", "database.cluster_snapshot"} {
		id := randomID()
		native := "manual-recovery"
		if kind == "compute.image" {
			native = "ami-12345678"
		}
		if e := s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: awsConnection.Msg.Connection.Id, Provider: "aws", Kind: kind, NativeID: native, Name: "Manual recovery", Region: "us-east-1", Status: "available"}); e != nil {
			t.Fatal(e)
		}
		exerciseResourceDeletion(t, s, owner, approver, ctx, org, id, approverID, kind)
	}

}
func exerciseResourceDeletion(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, resource, approverID, kind string) {
	var status, native, cloud string
	if e := s.pool.QueryRow(ctx, "SELECT status,native_id,provider FROM resources WHERE org_id=$1 AND id=$2", org, resource).Scan(&status, &native, &cloud); e != nil {
		t.Fatal(e)
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !provider.CommunityAction(cloud, kind) {
		_, err := owner.PreviewDeletion(ctx, connect.NewRequest(&pb.PreviewDeletionRequest{OrganizationId: org, ResourceId: resource}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("Community accepted paid deletion preview", err)
		}
		return
	}
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous }()
	impact := "Delete " + kind + " " + native + ". Retain unrelated resources."
	submissions := 0
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		p := r.Power
		if kind != "compute.server" && p.ResourceKind != kind || kind == "compute.server" && p.ResourceKind != "" {
			t.Fatal("wrong worker resource kind")
		}
		out := &provider.PowerResult{Outcome: "preview", Status: status, DeletionImpact: impact}
		switch p.Phase {
		case "submit":
			submissions++
			if p.DeletionImpact != impact {
				t.Fatal("reviewed impact missing from worker request")
			}
			out = &provider.PowerResult{Outcome: "accepted", Status: status}
		case "observe":
			out = &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return provider.Response{Version: provider.Protocol, Power: out}, nil
	}
	preview, err := owner.PreviewDeletion(ctx, connect.NewRequest(&pb.PreviewDeletionRequest{OrganizationId: org, ResourceId: resource}))
	check(err)
	request := &pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "delete", ExpectedStatus: status, Reason: "Retire exact server and reviewed local disk", IdempotencyKey: randomID(), DeletionDigest: preview.Msg.Digest}
	_, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("missing confirmation accepted", err)
	}
	request.Confirmation = native
	impact += " Changed."
	_, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("changed impact accepted", err)
	}
	impact = preview.Msg.Impact
	created, err := owner.RequestOperation(ctx, connect.NewRequest(request))
	check(err)
	if created.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL || created.Msg.Operation.DeletionImpact != impact {
		t.Fatal("missing stored review")
	}
	if kind != "compute.server" {
		var connection string
		check(s.pool.QueryRow(ctx, "SELECT connection_id FROM resources WHERE id=$1", resource).Scan(&connection))
		alias := randomID()
		aliasRegion := "ams3"
		if cloud == "aws" {
			aliasRegion = "us-west-2"
		}
		check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: alias, OrgID: org, ConnectionID: connection, Provider: cloud, Kind: kind, NativeID: native, Name: "Regional snapshot alias", Region: aliasRegion, Status: status}))
		duplicate := proto.Clone(request).(*pb.RequestOperationRequest)
		duplicate.ResourceId = alias
		duplicate.IdempotencyKey = randomID()
		if cloud == "aws" {
			aliasPreview, e := owner.PreviewDeletion(ctx, connect.NewRequest(&pb.PreviewDeletionRequest{OrganizationId: org, ResourceId: alias}))
			check(e)
			duplicate.DeletionDigest = aliasPreview.Msg.Digest
		}
		_, e := owner.RequestOperation(ctx, connect.NewRequest(duplicate))
		if cloud == "aws" {
			if e != nil {
				t.Fatal("independent region blocked", e)
			}
			// Cancel the second-region request before exercising the original worker.
			var aliasOperation string
			check(s.pool.QueryRow(ctx, "SELECT id FROM operations WHERE resource_id=$1 AND status='awaiting_approval'", alias).Scan(&aliasOperation))
			_, e = owner.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{OrganizationId: org, Id: aliasOperation}))
			check(e)
		} else if connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("regional alias queued a duplicate delete", e)
		}
	}
	review := &pb.ReviewOperationRequest{OrganizationId: org, Id: created.Msg.Operation.Id, Approve: true, Reason: "Accept exact cascade and data loss"}
	_, err = owner.ReviewOperation(ctx, connect.NewRequest(review))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("self approved deletion")
	}
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("approved without delete permission")
	}
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Name: "Deletion reviewer " + cloud + kind + resource[:8], Permissions: []string{"resources.read", "operations.read", "operations.approve", "operations.delete"}}))
	check(err)
	var roleID string
	check(s.pool.QueryRow(ctx, "SELECT id FROM roles WHERE org_id=$1 AND name=$2", org, "Deletion reviewer "+cloud+kind+resource[:8]).Scan(&roleID))
	setRole := func(id string) {
		_, e := owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: id, Active: true}))
		check(e)
	}
	setRole(roleID)
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	setRole("approver")
	job, err := s.q.ClaimOperation(ctx)
	check(err)
	check(s.runOperation(ctx, job))
	row, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: created.Msg.Operation.Id})
	check(err)
	if row.Status != "canceled" || submissions != 0 {
		t.Fatal("revoked delete permission reached worker")
	}
	request.IdempotencyKey = randomID()
	created, err = owner.RequestOperation(ctx, connect.NewRequest(request))
	check(err)
	setRole(roleID)
	review.Id = created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	job, err = s.q.ClaimOperation(ctx)
	check(err)
	check(s.runOperation(ctx, job))
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: created.Msg.Operation.Id})
	check(err)
	if row.Status != "observing" || submissions != 1 {
		t.Fatal("delete did not await observation")
	}
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", created.Msg.Operation.Id)
	check(err)
	job, err = s.q.ClaimOperation(ctx)
	check(err)
	check(s.runOperation(ctx, job))
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: created.Msg.Operation.Id})
	check(err)
	if row.Status != "succeeded" || submissions != 1 {
		t.Fatal("deletion not confirmed or repeated")
	}
	_, err = owner.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: resource}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("deleted server remains active")
	}
	if kind != "compute.server" {
		var remaining int
		check(s.pool.QueryRow(ctx, "SELECT count(*) FROM resources WHERE org_id=$1 AND kind=$3 AND native_id=$2 AND deleted_at IS NULL", org, native, kind).Scan(&remaining))
		want := 0
		if cloud == "aws" {
			want = 1
		}
		if remaining != want {
			t.Fatal("regional snapshot aliases remained active")
		}
	}
	setRole("approver")
}
