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

func exerciseCreation(t *testing.T, s *Service, owner, approver providahv1connect.ConsoleServiceClient, ctx context.Context, org, connection, approverID string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	previous := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = previous }()
	submissions := 0
	drop := false
	native := "12345"
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.Validate() != nil || r.ConnectionID != connection || r.OrganizationID != org || r.Power == nil || r.Power.Create == nil || r.Power.Create.Image != "11" || r.Power.Create.Network != "44" || r.Power.Action != "create" {
			t.Fatal("wrong creation authority/input")
		}
		result := &provider.PowerResult{Outcome: "succeeded", NativeID: native, Status: "running"}
		if r.Power.Phase == "submit" {
			submissions++
			if drop {
				return provider.Response{}, errors.New("lost submission")
			}
			result.Outcome = "accepted"
			result.Status = "initializing"
		}
		return provider.Response{Version: provider.Protocol, Power: result}, nil
	}
	request := &pb.RequestServerCreationRequest{OrganizationId: org, ConnectionId: connection, Region: "fsn1", Creation: &pb.ServerCreate{Name: "created-server", Image: "11", Size: "cx23", SshKey: "12", Network: "44"}, Reason: "Create a reviewed server", IdempotencyKey: randomID()}
	setCreation := func(enabled bool) {
		policy, e := owner.GetResourcePolicy(ctx, connect.NewRequest(&pb.GetResourcePolicyRequest{OrganizationId: org}))
		check(e)
		_, e = owner.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: enabled, ApprovalActions: policy.Msg.ApprovalActions, ExpectedRevision: policy.Msg.Revision, Reason: "Exercise organization resource policy"}))
		check(e)
	}
	setCreation(false)
	if _, e := owner.RequestServerCreation(ctx, connect.NewRequest(request)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("manage-existing policy allowed server creation", e)
	}
	if creationPermitted(ctx, s.q, org, "snapshot") || !creationPermitted(ctx, s.q, org, "delete") {
		t.Fatal("resource policy action boundary")
	}
	session, e := owner.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	check(e)
	for _, o := range session.Msg.Organizations {
		if o.Id == org {
			for _, permission := range o.Permissions {
				if permission == "operations.create" {
					t.Fatal("creation controls still advertised")
				}
			}
		}
	}
	if _, e := approver.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: true, ExpectedRevision: 2, Reason: "Unauthorized policy change"})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("non-admin changed resource policy", e)
	}
	setCreation(true)
	created, err := owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	if created.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_AWAITING_APPROVAL || created.Msg.Operation.ResourceId != "" || created.Msg.Operation.Creation == nil {
		t.Fatal("creation did not wait for approval without inventing a resource")
	}
	same, err := owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	if same.Msg.Operation.Id != created.Msg.Operation.Id {
		t.Fatal("idempotency lost")
	}
	request.Creation.Size = "cx33"
	_, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("changed creation reused a request key", err)
	}
	request.Creation.Size = "cx23"
	review := &pb.ReviewOperationRequest{OrganizationId: org, Id: created.Msg.Operation.Id, Approve: true, Reason: "Reviewed server and cloud charges"}
	_, err = owner.ReviewOperation(ctx, connect.NewRequest(review))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("self approval accepted")
	}
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("creation approved without creation permission")
	}
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Name: "Creation reviewer", Permissions: []string{"resources.read", "operations.read", "operations.approve", "operations.create"}}))
	check(err)
	var role string
	check(s.pool.QueryRow(ctx, "SELECT id FROM roles WHERE org_id=$1 AND name='Creation reviewer'", org).Scan(&role))
	setRole := func(role string) {
		_, e := owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: approverID, ExpectedRevision: membershipRevision(t, s, ctx, org, approverID), RoleId: role, Active: true}))
		check(e)
	}
	setRole(role)
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	run := func() { job, e := s.q.ClaimOperation(ctx); check(e); check(s.runOperation(ctx, job)) }
	pending, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(e)
	_, e = s.queueScan(ctx, org, connection, pending.RequesterID, "test")
	check(e)
	scan, e := s.q.ClaimScan(ctx)
	check(e)
	run()
	row, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "observing" || row.NativeID != native || !row.ResourceID.Valid || submissions != 1 {
		t.Fatalf("submission/binding failed: %+v", row)
	}
	setCreation(false) // Submitted work must still be observed after creation is disabled.
	_, err = s.pool.Exec(ctx, "UPDATE resources SET public_ip='203.0.113.10' WHERE id=$1", row.ResourceID.String)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE operations SET next_attempt_at=now() WHERE id=$1", row.ID)
	check(err)
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "succeeded" || submissions != 1 {
		t.Fatal("creation was resubmitted or not observed")
	}
	setCreation(true)
	resource, err := s.q.GetResource(ctx, database.GetResourceParams{OrgID: org, ID: row.ResourceID.String})
	check(err)
	if resource.NativeID != native || resource.Status != "running" || resource.PublicIp != "203.0.113.10" {
		t.Fatal("created inventory is wrong")
	}
	exerciseKeyCreation(t, s, owner, approver, ctx, org, connection)
	check(s.finishScan(ctx, scan, provider.Response{Version: provider.Protocol, Complete: true}, ""))
	var scanStatus string
	check(s.pool.QueryRow(ctx, "SELECT status FROM scan_jobs WHERE id=$1", scan.ID).Scan(&scanStatus))
	if scanStatus != "canceled" {
		t.Fatal("older scan overwrote a created server")
	}
	_, err = s.q.GetResource(ctx, database.GetResourceParams{OrgID: org, ID: row.ResourceID.String})
	check(err)
	// A lost response is fenced by connection/region/name and can only be reconciled by reads.
	drop = true
	native = "12346"
	request.Creation.Name = "lost-server"
	request.IdempotencyKey = randomID()
	created, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	review.Id = created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "uncertain" || row.ResourceID.Valid || submissions != 2 {
		t.Fatal("lost creation was not left uncertain")
	}
	request.IdempotencyKey = randomID()
	_, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("uncertain creation allowed duplicate", err)
	}
	_, err = s.pool.Exec(ctx, "UPDATE operations SET lease_until=now()-interval '1 second' WHERE id=$1", review.Id)
	check(err)
	_, err = owner.ReconcileOperation(ctx, connect.NewRequest(&pb.ReconcileOperationRequest{OrganizationId: org, Id: review.Id}))
	check(err)
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "succeeded" || row.NativeID != native || submissions != 2 {
		t.Fatal("lost creation did not reconcile without retry")
	}
	// Approval authority is rechecked immediately before dispatch.
	request.Creation.Name = "revoked-server"
	request.IdempotencyKey = randomID()
	created, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	review.Id = created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	setRole("approver")
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "canceled" || submissions != 2 {
		t.Fatal("revoked creation authority reached provider")
	}
	// Published templates pin all fields except the deployment's server name.
	publish := &pb.PublishServerTemplateRequest{OrganizationId: org, ConnectionId: connection, Name: "Web standard", Region: "fsn1", Creation: &pb.ServerCreate{Image: "11", Size: "cx23", SshKey: "12", Network: "44"}}
	_, err = approver.PublishServerTemplate(ctx, connect.NewRequest(publish))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("non-publisher published a template")
	}
	first, err := owner.PublishServerTemplate(ctx, connect.NewRequest(publish))
	check(err)
	exact, err := owner.GetServerTemplate(ctx, connect.NewRequest(&pb.GetServerTemplateRequest{OrganizationId: org, Id: first.Msg.Id}))
	check(err)
	if exact.Msg.Id != first.Msg.Id || exact.Msg.Creation.Size != "cx23" || exact.Msg.Creation.Network != "44" {
		t.Fatal("exact template lookup lost configuration")
	}
	for _, target := range []struct {
		org, id string
		code    connect.Code
	}{{org, "invalid", connect.CodeInvalidArgument}, {org, randomID(), connect.CodePermissionDenied}, {randomID(), first.Msg.Id, connect.CodePermissionDenied}} {
		_, e := owner.GetServerTemplate(ctx, connect.NewRequest(&pb.GetServerTemplateRequest{OrganizationId: target.org, Id: target.id}))
		if connect.CodeOf(e) != target.code {
			t.Fatal("template scope validation failed", e)
		}
	}
	second, err := owner.PublishServerTemplate(ctx, connect.NewRequest(publish))
	check(err)
	if first.Msg.Version != 1 || second.Msg.Version != 2 || first.Msg.Id == second.Msg.Id {
		t.Fatal("template versions are not immutable")
	}
	catalog, err := owner.ListServerTemplates(ctx, connect.NewRequest(&pb.ListServerTemplatesRequest{OrganizationId: org}))
	check(err)
	if len(catalog.Msg.Templates) != 2 {
		t.Fatal("template catalog missing versions")
	}
	publish.OrganizationId = randomID()
	if _, err = owner.PublishServerTemplate(ctx, connect.NewRequest(publish)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign template publication accepted")
	}
	setRole(role)
	templateCalls := 0
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		templateCalls++
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "failed", Error: "state_changed"}}, nil
	}
	request.TemplateId = first.Msg.Id
	request.Creation.Name = "template-revoked"
	request.IdempotencyKey = randomID()
	request.Creation.Size = "cx33"
	if _, err = owner.RequestServerCreation(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("template allowed configuration override")
	}
	request.Creation.Size = "cx23"
	created, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	review.Id = created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	_, err = owner.SetServerTemplateStatus(ctx, connect.NewRequest(&pb.SetServerTemplateStatusRequest{OrganizationId: org, Id: first.Msg.Id, Status: "revoked"}))
	check(err)
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "canceled" || templateCalls != 0 || row.TemplateID.String != first.Msg.Id {
		t.Fatal("revoked template reached provider")
	}
	request.TemplateId = second.Msg.Id
	request.Creation.Name = "template-retired"
	request.IdempotencyKey = randomID()
	created, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	review.Id = created.Msg.Operation.Id
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	_, err = owner.SetServerTemplateStatus(ctx, connect.NewRequest(&pb.SetServerTemplateStatusRequest{OrganizationId: org, Id: second.Msg.Id, Status: "retired"}))
	check(err)
	same, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	if same.Msg.Operation.Id != created.Msg.Operation.Id {
		t.Fatal("retirement lost idempotent binding")
	}
	request.IdempotencyKey = randomID()
	if _, err = owner.RequestServerCreation(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("retired template accepted new binding")
	}
	run()
	if templateCalls != 1 {
		t.Fatal("retirement canceled already approved binding")
	}
	_, err = owner.SetServerTemplateStatus(ctx, connect.NewRequest(&pb.SetServerTemplateStatusRequest{OrganizationId: org, Id: second.Msg.Id, Status: "revoked"}))
	check(err)
	request.TemplateId = ""
	request.Creation.Name = "policy-revoked"
	request.IdempotencyKey = randomID()
	created, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	review.Id = created.Msg.Operation.Id
	setCreation(false)
	if _, e := approver.ReviewOperation(ctx, connect.NewRequest(review)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("disabled creation was approved", e)
	}
	setCreation(true)
	_, err = approver.ReviewOperation(ctx, connect.NewRequest(review))
	check(err)
	setCreation(false)
	run()
	row, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: review.Id})
	check(err)
	if row.Status != "canceled" || templateCalls != 1 {
		t.Fatal("disabled queued creation reached provider")
	}
	setCreation(true)
	setRole("approver")

	// Confirmation-only creation must reach the provider without an approver identity.
	policy, e := owner.GetResourcePolicy(ctx, connect.NewRequest(&pb.GetResourcePolicyRequest{OrganizationId: org}))
	check(e)
	_, e = owner.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: true, ExpectedRevision: policy.Msg.Revision, Reason: "Test solo creation"}))
	check(e)
	request.Creation.Name = "solo-creation"
	request.IdempotencyKey = randomID()
	created, err = owner.RequestServerCreation(ctx, connect.NewRequest(request))
	check(err)
	if created.Msg.Operation.Status != pb.OperationStatus_OPERATION_STATUS_QUEUED {
		t.Fatal("solo creation required approval")
	}
	run()
	if templateCalls != 2 {
		t.Fatal("solo creation did not reach provider")
	}
	current, e := owner.GetResourcePolicy(ctx, connect.NewRequest(&pb.GetResourcePolicyRequest{OrganizationId: org}))
	check(e)
	_, e = owner.SaveResourcePolicy(ctx, connect.NewRequest(&pb.SaveResourcePolicyRequest{OrganizationId: org, CreationEnabled: true, ApprovalActions: policy.Msg.ApprovalActions, ExpectedRevision: current.Msg.Revision, Reason: "Restore creation approvals"}))
	check(e)

}
