//go:build integration

package core

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"filippo.io/age"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/artifactstore"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/internal/tfstate"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5/pgtype"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func exerciseManagedState(t *testing.T, s *Service, owner, viewer providahv1connect.ConsoleServiceClient, ctx context.Context, org, version string) {
	t.Helper()
	var unavailableStore *artifactstore.Store
	if s.cfg.Artifacts != nil {
		var e error
		unavailableStore, e = artifactstore.New(artifactstore.Config{Endpoint: "http://127.0.0.1:19000", Region: "us-east-1", Bucket: "providah-missing-" + randomID()[:32], AccessKey: "unused", SecretKey: "unused"}, true)
		if e != nil {
			t.Fatal(e)
		}
	}
	req := &pb.CreateAutomationProjectRequest{OrganizationId: org, Name: "State integration", VersionId: version}
	if _, e := viewer.CreateAutomationProject(ctx, connect.NewRequest(req)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("nonpublisher created project", e)
	}
	created, e := owner.CreateAutomationProject(ctx, connect.NewRequest(req))
	if e != nil {
		t.Fatal(e)
	}
	direct, e := owner.GetAutomationProject(ctx, connect.NewRequest(&pb.GetAutomationProjectRequest{OrganizationId: org, Id: created.Msg.Id}))
	if e != nil || direct.Msg.Id != created.Msg.Id || direct.Msg.Serial != -1 || direct.Msg.Ownership == nil || direct.Msg.Ownership.Status != "no_state" {
		t.Fatal("direct project metadata read failed", e)
	}
	for _, target := range []*pb.GetAutomationProjectRequest{{OrganizationId: randomID(), Id: created.Msg.Id}, {OrganizationId: org, Id: randomID()}} {
		_, e = owner.GetAutomationProject(ctx, connect.NewRequest(target))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("project identity leaked", e)
		}
	}
	_, e = owner.GetAutomationProject(ctx, connect.NewRequest(&pb.GetAutomationProjectRequest{OrganizationId: org, Id: "invalid"}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid project ID accepted")
	}
	again, e := owner.CreateAutomationProject(ctx, connect.NewRequest(req))
	if e != nil || created.Msg.Id != again.Msg.Id {
		t.Fatal("project retry changed identity", e)
	}
	req.VersionId = randomID()
	if _, e = owner.CreateAutomationProject(ctx, connect.NewRequest(req)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("name conflict changed binding", e)
	}
	req.OrganizationId = randomID()
	if _, e = owner.CreateAutomationProject(ctx, connect.NewRequest(req)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("cross tenant project created", e)
	}
	var p principal
	if e = s.pool.QueryRow(ctx, "SELECT requester_id FROM automation_validations WHERE version_id=$1 LIMIT 1", version).Scan(&p.UserID); e != nil {
		t.Fatal(e)
	}
	project := created.Msg.Id
	id, secret, e := s.newStateSession(ctx, org, project, p, true)
	if e != nil {
		t.Fatal(e)
	}
	other, otherSecret, e := s.newStateSession(ctx, org, project, p, true)
	if e != nil {
		t.Fatal(e)
	}
	read, readSecret, e := s.newStateSession(ctx, org, project, p, false)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	request := func(method, project, user, password, query string, raw []byte, want int) []byte {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, method, server.URL+statePath+project+query, bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		if user != "" {
			r.SetBasicAuth(user, password)
		} else {
			r.Header.Set("Cookie", "providah_session=not-a-capability")
		}
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		b, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != want {
			t.Fatalf("state %s got %d, want %d", method, response.StatusCode, want)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("raw state cache allowed")
		}
		return b
	}
	request("GET", project, "", "", "", nil, 401)
	request("GET", project, id, randomID(), "", nil, 401)
	request("GET", randomID(), id, secret, "", nil, 401)
	request("GET", project, id, secret, "", nil, 404)
	request("DELETE", project, id, secret, "", nil, 405)
	browserRequest, _ := http.NewRequestWithContext(ctx, "GET", server.URL+statePath+project, nil)
	browserRequest.SetBasicAuth(id, secret)
	browserRequest.Header.Set("Origin", s.cfg.Origin)
	browserResponse, e := http.DefaultClient.Do(browserRequest)
	if e != nil {
		t.Fatal(e)
	}
	_ = browserResponse.Body.Close()
	if browserResponse.StatusCode != 403 {
		t.Fatal("browser basic credentials accepted")
	}

	lockID := "00112233-4455-6677-8899-aabbccddeeff"
	lock := []byte(`{"ID":"` + lockID + `","Who":"never-store-this-secret"}`)
	request("LOCK", project, read, readSecret, "", lock, 403)
	request("LOCK", project, id, secret, "", lock, 200)
	request("LOCK", project, id, secret, "", lock, 200)
	held := request("LOCK", project, other, otherSecret, "", lock, 423)
	if bytes.Contains(held, []byte("never-store")) {
		t.Fatal("lock conflict leaked lock info")
	}
	request("UNLOCK", project, other, otherSecret, "", lock, 409)
	state := func(serial int, lineage string) []byte {
		return []byte(fmt.Sprintf(`{"version":4,"serial":%d,"lineage":%q,"outputs":{"secret":{"value":%q,"type":"string","sensitive":true}}}`, serial, lineage, strings.Repeat("protected-state-value", 5000)))
	}
	raw := bytes.Replace(state(0, lockID), []byte(`"outputs":`), []byte(`"resources":[{"mode":"managed","type":"hcloud_server","provider":"provider[\"registry.terraform.io/hetznercloud/hcloud\"]","instances":[{"attributes":{"id":"887766"}}]},{"mode":"managed","type":"hcloud_volume","provider":"provider[\"registry.terraform.io/hetznercloud/hcloud\"]","instances":[{"attributes":{"id":887766}}]},{"mode":"managed","type":"hcloud_firewall","provider":"provider[\"registry.terraform.io/hetznercloud/hcloud\"]","instances":[{"attributes":{"id":"887766"}}]},{"mode":"managed","type":"unsupported_resource","provider":"unknown","instances":[{"attributes":{"id":"unknown"}}]}],"outputs":`), 1)
	request("POST", project, id, secret, "", raw, 409)
	request("POST", project, id, secret, "?ID="+lockID, raw, 200)
	request("POST", project, id, secret, "?ID="+lockID, raw, 200)
	request("POST", project, read, readSecret, "?ID="+lockID, raw, 403)
	request("POST", project, other, otherSecret, "?ID="+lockID, raw, 409)
	request("POST", project, id, secret, "?ID="+lockID, []byte(`{}`), 400)
	request("POST", project, id, secret, "?ID="+lockID, make([]byte, tfstate.MaxBytes+1), 413)
	request("POST", project, id, secret, "?ID="+lockID, state(1, "10112233-4455-6677-8899-aabbccddeeff"), 409)
	request("POST", project, id, secret, "?ID="+lockID, append(raw, ' '), 409)
	if unavailableStore != nil {
		cfg := s.cfg
		cfg.Artifacts = unavailableStore
		failed, e := New(s.pool, cfg, s.log)
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.NewRequestWithContext(ctx, "POST", server.URL+statePath+project+"?ID="+lockID, bytes.NewReader(state(1, lockID)))
		if e != nil {
			t.Fatal(e)
		}
		r.SetBasicAuth(id, secret)
		out := httptest.NewRecorder()
		failed.automationState(out, r)
		if out.Code != 500 {
			t.Fatal("failed artifact write was acknowledged", out.Code)
		}
		var serial int64
		if e = s.pool.QueryRow(ctx, "SELECT serial FROM automation_projects WHERE id=$1", project).Scan(&serial); e != nil || serial != 0 {
			t.Fatal("failed artifact write advanced state", e)
		}
	}
	scope, e := s.q.OwnershipProjectScope(ctx, database.OwnershipProjectScopeParams{OrgID: org, ID: project})
	if e != nil {
		t.Fatal(e)
	}
	exerciseAWSOwnership(t, s, ctx, org, project, scope.ConnectionID, scope.Region)
	resourceID := randomID()
	if e = s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: resourceID, OrgID: org, ConnectionID: scope.ConnectionID, Provider: "hetzner", Kind: "compute.server", NativeID: "887766", Name: "State protected server", Region: "fsn1", Status: "off", Size: "cx23"}); e != nil {
		t.Fatal(e)
	}
	assertProtection := func() {
		t.Helper()
		owners, err := s.q.ResourceOwnership(ctx, database.ResourceOwnershipParams{OrgID: org, ConnectionID: scope.ConnectionID, Kind: "compute.server", NativeID: "887766", Region: "fsn1"})
		if err != nil || len(owners) != 1 || owners[0] != project {
			t.Fatal("state ownership missing", owners, err)
		}
		projectDetail, err := owner.GetAutomationProject(ctx, connect.NewRequest(&pb.GetAutomationProjectRequest{OrganizationId: org, Id: project}))
		if err != nil || projectDetail.Msg.Ownership == nil || projectDetail.Msg.Ownership.ScannerVersion != 11 || projectDetail.Msg.Ownership.ProtectedReferences != 3 || projectDetail.Msg.Ownership.IncompleteVersions != 1 {
			t.Fatal("ownership summary mismatch", err)
		}
		if projectDetail.Msg.Serial == 0 && (projectDetail.Msg.Ownership.Status != "partial" || projectDetail.Msg.Ownership.Unsupported != 1) {
			t.Fatal("partial current scan hidden")
		}
		if projectDetail.Msg.Serial == 1 && (projectDetail.Msg.Ownership.Status != "complete" || projectDetail.Msg.Ownership.Unsupported != 0) {
			t.Fatal("historical coverage confused with current scan")
		}
		listed, err := owner.ListProjectOwnership(ctx, connect.NewRequest(&pb.ListProjectOwnershipRequest{OrganizationId: org, ProjectId: project}))
		if err != nil || len(listed.Msg.References) != 3 {
			t.Fatal("ownership list mismatch", err)
		}
		found := false
		for _, ref := range listed.Msg.References {
			if ref.Kind == "compute.server" && ref.ResourceId == resourceID {
				found = true
			}
		}
		if !found {
			t.Fatal("missing inventory link")
		}
		for _, action := range []string{"delete", "resize", "future-edit"} {
			if err = ownershipGuard(ctx, s.q, org, scope.ConnectionID, "compute.server", "887766", "fsn1", action); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("direct edit unprotected", action, err)
			}
		}
		for _, action := range []string{"start", "shutdown", "restart", "snapshot"} {
			if err = ownershipGuard(ctx, s.q, org, scope.ConnectionID, "compute.server", "887766", "fsn1", action); err != nil {
				t.Fatal("power denied", err)
			}
		}
		for _, ids := range [][2]string{{randomID(), scope.ConnectionID}, {org, randomID()}} {
			if err = ownershipGuard(ctx, s.q, ids[0], ids[1], "compute.server", "887766", "fsn1", "delete"); err != nil {
				t.Fatal("claim crossed account scope", err)
			}
		}
		detail, err := owner.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: resourceID}))
		if err != nil || len(detail.Msg.OwnershipProjectIds) != 1 || slices.Contains(detail.Msg.AvailableActions, "delete") || slices.Contains(detail.Msg.AvailableActions, "resize") {
			t.Fatal("resource details protection failed", err)
		}
		if _, err = owner.PreviewDeletion(ctx, connect.NewRequest(&pb.PreviewDeletionRequest{OrganizationId: org, ResourceId: resourceID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("delete preview unprotected", err)
		}
		previous := s.cfg.ProviderCall
		s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
			t.Fatal("protected operation called provider")
			return provider.Response{}, nil
		}
		defer func() { s.cfg.ProviderCall = previous }()
		_, err = owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resourceID, Action: "resize", ExpectedStatus: "off", ExpectedSize: "cx23", TargetSize: "cx33", Reason: "Must remain protected", IdempotencyKey: randomID()}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("resize API unprotected", err)
		}
	}
	assertProtection()
	for _, kind := range []string{"storage.volume", "network.firewall"} {
		id := randomID()
		if e = s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: scope.ConnectionID, Provider: "hetzner", Kind: kind, NativeID: "887766", Name: "Protected infrastructure", Region: "fsn1", Status: "present"}); e != nil {
			t.Fatal(e)
		}
		if _, e = owner.PreviewDeletion(ctx, connect.NewRequest(&pb.PreviewDeletionRequest{OrganizationId: org, ResourceId: id})); connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("non-server deletion unprotected", kind, e)
		}
	}
	raw = state(1, lockID)
	request("POST", project, id, secret, "?ID="+lockID, raw, 200)
	assertProtection() // Removing a reference from later state must not unlock the resource.
	// Simulate an upgrade: retained states exist but their ownership index is absent.
	if _, e = s.pool.Exec(ctx, "DELETE FROM resource_ownership_claims WHERE project_id=$1", project); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE state_ownership_scans SET scanner_version=10 WHERE state_id IN (SELECT id FROM automation_states WHERE project_id=$1)", project); e != nil {
		t.Fatal(e)
	}
	previousCall := s.cfg.ProviderCall
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		t.Fatal("late ownership claim reached provider")
		return provider.Response{}, nil
	}
	defer func() { s.cfg.ProviderCall = previousCall }()
	queued, e := owner.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resourceID, Action: "resize", ExpectedStatus: "off", ExpectedSize: "cx23", TargetSize: "cx33", Reason: "Queued before ownership was known", IdempotencyKey: randomID()}))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IndexStateOwnership(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.IndexStateOwnership(ctx); e != nil {
		t.Fatal(e)
	}
	assertProtection()
	for _, kind := range []string{"storage.volume", "network.firewall"} {
		if e = ownershipGuard(ctx, s.q, org, scope.ConnectionID, kind, "887766", "fsn1", "delete"); connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("upgrade failed to index infrastructure", kind, e)
		}
	}
	for _, target := range []*pb.ListProjectOwnershipRequest{{OrganizationId: randomID(), ProjectId: project}, {OrganizationId: org, ProjectId: randomID()}} {
		if _, e = owner.ListProjectOwnership(ctx, connect.NewRequest(target)); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("foreign ownership scope accepted", e)
		}
	}
	if _, e = owner.ListProjectOwnership(ctx, connect.NewRequest(&pb.ListProjectOwnershipRequest{OrganizationId: org, ProjectId: project, PageToken: cursor(org, "project-ownership:"+randomID(), "key")})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("foreign ownership cursor accepted", e)
	}
	// Exercise bounded paging with additional references in this isolated database.
	if _, e = s.pool.Exec(ctx, `INSERT INTO resource_ownership_claims(org_id,connection_id,project_id,provider,kind,region,native_id,first_state_id) SELECT org_id,connection_id,project_id,provider,kind,region,(900000+n)::text,first_state_id FROM resource_ownership_claims CROSS JOIN generate_series(1,103) AS n WHERE project_id=$1 AND kind='storage.volume'`, project); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	token := ""
	for {
		page, err := owner.ListProjectOwnership(ctx, connect.NewRequest(&pb.ListProjectOwnershipRequest{OrganizationId: org, ProjectId: project, PageToken: token}))
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Msg.References) > 100 {
			t.Fatal("ownership page unbounded")
		}
		for _, ref := range page.Msg.References {
			key := ref.Kind + "/" + ref.Region + "/" + ref.NativeId
			if seen[key] {
				t.Fatal("duplicate ownership page")
			}
			seen[key] = true
		}
		token = page.Msg.NextPageToken
		if token == "" {
			break
		}
	}
	if len(seen) != 106 {
		t.Fatal("ownership paging lost references", len(seen))
	}
	conflictProject, e := owner.CreateAutomationProject(ctx, connect.NewRequest(&pb.CreateAutomationProjectRequest{OrganizationId: org, Name: "Conflicting state references", VersionId: version}))
	if e != nil {
		t.Fatal(e)
	}
	conflictSession, conflictSecret, e := s.newStateSession(ctx, org, conflictProject.Msg.Id, p, true)
	if e != nil {
		t.Fatal(e)
	}
	request("LOCK", conflictProject.Msg.Id, conflictSession, conflictSecret, "", lock, 200)
	conflictRaw := []byte(`{"version":4,"serial":0,"lineage":"00112233-4455-6677-8899-aabbccddeeff","resources":[{"mode":"managed","type":"hcloud_volume","provider":"provider[\"registry.terraform.io/hetznercloud/hcloud\"]","instances":[{"attributes":{"id":"887766"}}]}]}`)
	request("POST", conflictProject.Msg.Id, conflictSession, conflictSecret, "?ID="+lockID, conflictRaw, 200)
	request("UNLOCK", conflictProject.Msg.Id, conflictSession, conflictSecret, "", lock, 200)
	conflicts, e := owner.ListProjectOwnership(ctx, connect.NewRequest(&pb.ListProjectOwnershipRequest{OrganizationId: org, ProjectId: project}))
	if e != nil {
		t.Fatal(e)
	}
	foundConflict := false
	for _, ref := range conflicts.Msg.References {
		if ref.Kind == "storage.volume" && ref.NativeId == "887766" && ref.Conflicting {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatal("conflicting project reference hidden")
	}
	withoutResourceAccess := context.WithValue(ctx, principalKey{}, principal{UserID: randomID()})
	if _, e = s.ListProjectOwnership(withoutResourceAccess, connect.NewRequest(&pb.ListProjectOwnershipRequest{OrganizationId: org, ProjectId: project})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("resource authority not required", e)
	}
	// Supply a worker lease to exercise dispatch rechecking a newly indexed claim.
	if _, e = s.pool.Exec(ctx, "UPDATE operations SET status='dispatching',lease_until=now()+interval '1 minute' WHERE id=$1", queued.Msg.Operation.Id); e != nil {
		t.Fatal(e)
	}
	job, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: queued.Msg.Operation.Id})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.runOperation(ctx, job); e != nil {
		t.Fatal(e)
	}
	stopped, e := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: job.ID})
	if e != nil || stopped.Status != "canceled" || !strings.Contains(stopped.Detail, "Managed IaC state protects") {
		t.Fatal("late claim did not cancel dispatch", stopped.Status, stopped.Detail, e)
	}
	request("POST", project, id, secret, "?ID="+lockID, state(0, lockID), 409)
	if got := request("GET", project, read, readSecret, "", nil, 200); !bytes.Equal(got, raw) {
		t.Fatal("state bytes changed")
	}
	var cipher []byte
	var count int
	if e = s.pool.QueryRow(ctx, "SELECT count(*) FROM automation_states WHERE project_id=$1", project).Scan(&count); e != nil || count != 2 {
		t.Fatal("state versions not immutable/idempotent", count, e)
	}
	if e = s.pool.QueryRow(ctx, "SELECT ciphertext FROM automation_states WHERE project_id=$1 AND serial=1", project).Scan(&cipher); e != nil || bytes.Contains(cipher, []byte("protected-state-value")) {
		t.Fatal("state not encrypted", e)
	}
	if s.cfg.Artifacts != nil {
		var stateID, storage string
		if e = s.pool.QueryRow(ctx, "SELECT id,storage FROM automation_states WHERE project_id=$1 AND serial=1", project).Scan(&stateID, &storage); e != nil || storage != "s3" || len(cipher) > 4096 {
			t.Fatal("state body remained in database", e)
		}
		key := "states/" + org + "/" + project + "/" + stateID
		envelope, e := s.open(cipher)
		if e != nil {
			t.Fatal(e)
		}
		next, e := age.GenerateX25519Identity()
		if e != nil {
			t.Fatal(e)
		}
		cfg := s.cfg
		cfg.AgeIdentity = next.String()
		rotated, e := New(s.pool, cfg, s.log)
		if e != nil {
			t.Fatal(e)
		}
		rewrapped, e := rotated.seal(envelope)
		if e != nil {
			t.Fatal(e)
		}
		plain, e := rotated.openArtifact(ctx, key, storage, rewrapped, tfstate.MaxBytes)
		if e != nil || plain != string(raw) {
			t.Fatal("rewrapped envelope lost object access", e)
		}
		if _, e = rotated.openArtifact(ctx, key+"0", storage, rewrapped, tfstate.MaxBytes); e == nil {
			t.Fatal("cross-record envelope accepted")
		}
		cfg.Artifacts = nil
		unavailable, e := New(s.pool, cfg, s.log)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = unavailable.openArtifact(ctx, key, storage, rewrapped, tfstate.MaxBytes); e == nil {
			t.Fatal("missing store accepted")
		}
		legacy, e := unavailable.seal(string(raw))
		if e != nil {
			t.Fatal(e)
		}
		plain, e = rotated.openArtifact(ctx, key, "database", legacy, tfstate.MaxBytes)
		if e != nil || plain != string(raw) {
			t.Fatal("legacy state compatibility lost", e)
		}
	}
	history, e := owner.ListAutomationStates(ctx, connect.NewRequest(&pb.ListAutomationStatesRequest{OrganizationId: org, ProjectId: project}))
	if e != nil || len(history.Msg.States) != 2 || history.Msg.States[0].Serial != 1 {
		t.Fatal("state metadata history", e)
	}
	encoded, _ := json.Marshal(history.Msg)
	if bytes.Contains(encoded, []byte("protected-state-value")) {
		t.Fatal("human metadata leaked state")
	}
	before, e := owner.ListAutomationStates(ctx, connect.NewRequest(&pb.ListAutomationStatesRequest{OrganizationId: org, ProjectId: project, BeforeSerial: "1"}))
	if e != nil || len(before.Msg.States) != 1 || before.Msg.States[0].Serial != 0 {
		t.Fatal("state cursor mismatch", e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE automation_state_sessions SET revoked=true WHERE id=$1", read); e != nil {
		t.Fatal(e)
	}
	request("GET", project, read, readSecret, "", nil, 401)
	var originalRole pgtype.Text
	var originalPermissions []string
	if e = s.pool.QueryRow(ctx, "SELECT role_id,permissions FROM memberships WHERE org_id=$1 AND user_id=$2", org, p.UserID).Scan(&originalRole, &originalPermissions); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE memberships SET role_id=NULL,permissions=ARRAY[]::text[] WHERE org_id=$1 AND user_id=$2", org, p.UserID); e != nil {
		t.Fatal(e)
	}
	request("GET", project, id, secret, "", nil, 403)
	if _, _, e = s.newStateSession(ctx, org, project, p, true); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("revoked publisher issued capability", e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE memberships SET role_id=$3,permissions=$4 WHERE org_id=$1 AND user_id=$2", org, p.UserID, originalRole, originalPermissions); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE users SET active=false WHERE id=$1", p.UserID); e != nil {
		t.Fatal(e)
	}
	request("GET", project, id, secret, "", nil, 403)
	if _, e = s.pool.Exec(ctx, "UPDATE users SET active=true WHERE id=$1", p.UserID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE automation_states SET ciphertext=$2 WHERE project_id=$1 AND serial=1", project, []byte("corrupt")); e != nil {
		t.Fatal(e)
	}
	request("GET", project, id, secret, "", nil, 500)
	if _, e = s.pool.Exec(ctx, "UPDATE automation_states SET ciphertext=$2 WHERE project_id=$1 AND serial=1", project, cipher); e != nil {
		t.Fatal(e)
	}
	var leaked int
	if e = s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE details::text LIKE '%never-store-this-secret%' OR details::text LIKE '%protected-state-value%'").Scan(&leaked); e != nil || leaked != 0 {
		t.Fatal("state secrets leaked to audit", e)
	}
	request("UNLOCK", project, id, secret, "", lock, 200)
	request("UNLOCK", project, id, secret, "", lock, 200)
	request("LOCK", project, other, otherSecret, "", lock, 200)
	if _, e = s.pool.Exec(ctx, "UPDATE automation_state_sessions SET expires_at=now()-interval '1 second' WHERE id=$1", other); e != nil {
		t.Fatal(e)
	}
	request("UNLOCK", project, other, otherSecret, "", lock, 401)
	request("LOCK", project, id, secret, "", lock, 423) // Expiry never steals an uncertain writer's lock.
	// Real CLIs run only this trusted, provider-free fixture, with an empty environment.
	for _, binary := range filepath.SplitList(os.Getenv("TEST_STATE_CLIS")) {
		if binary == "" {
			continue
		}
		created, e := owner.CreateAutomationProject(ctx, connect.NewRequest(&pb.CreateAutomationProjectRequest{OrganizationId: org, Name: "CLI " + filepath.Base(binary), VersionId: version}))
		if e != nil {
			t.Fatal(e)
		}
		user, password, e := s.newStateSession(ctx, org, created.Msg.Id, p, true)
		if e != nil {
			t.Fatal(e)
		}
		dir := t.TempDir()
		config := `terraform {
 backend "http" {}
}
variable "value" { type = string }
output "fixture" { value = var.value }
`
		if e = os.WriteFile(filepath.Join(dir, "main.tf"), []byte(config), 0600); e != nil {
			t.Fatal(e)
		}
		address := server.URL + statePath + created.Msg.Id
		run := func(args ...string) {
			t.Helper()
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(runCtx, binary, args...)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TF_IN_AUTOMATION=1", "TF_INPUT=0", "TF_HTTP_ADDRESS=" + address, "TF_HTTP_LOCK_ADDRESS=" + address, "TF_HTTP_UNLOCK_ADDRESS=" + address, "TF_HTTP_USERNAME=" + user, "TF_HTTP_PASSWORD=" + password, "TF_HTTP_RETRY_MAX=0"}
			if out, e := cmd.CombinedOutput(); e != nil {
				detail := strings.ReplaceAll(strings.ReplaceAll(string(out), password, "[redacted]"), user, "[redacted]")
				t.Fatalf("%s state integration %s: %v: %.16000s", filepath.Base(binary), args[0], e, detail)
			}
		}
		run("init", "-input=false", "-no-color")
		run("plan", "-input=false", "-no-color", "-var=value=first", "-out=approved.plan")
		run("apply", "-input=false", "-no-color", "approved.plan")
		first := request("GET", created.Msg.Id, user, password, "", nil, 200)
		a, e := tfstate.Parse(first)
		if e != nil {
			t.Fatal(e)
		}
		run("plan", "-input=false", "-no-color", "-var=value=second", "-out=approved.plan")
		run("apply", "-input=false", "-no-color", "approved.plan")
		last := request("GET", created.Msg.Id, user, password, "", nil, 200)
		b, e := tfstate.Parse(last)
		if e != nil || a.Lineage != b.Lineage || b.Serial <= a.Serial {
			t.Fatal("real CLI did not preserve lineage and advance serial", e)
		}
		var held string
		if e = s.pool.QueryRow(ctx, "SELECT lock_id FROM automation_projects WHERE id=$1", created.Msg.Id).Scan(&held); e != nil || held != "" {
			t.Fatal("CLI left backend locked", e)
		}
		t.Logf("%s: real HTTP backend plan/apply and state version advance passed", filepath.Base(binary))
	}
}

// Use a rolled-back fixture scope: no real connection or provider call is involved.
func exerciseAWSOwnership(t *testing.T, s *Service, ctx context.Context, org, project, connection, region string) {
	exerciseOwnershipMappings(t, s, ctx, org, project, connection, region, "aws")
	exerciseOwnershipMappings(t, s, ctx, org, project, connection, region, "digitalocean")
	exerciseOwnershipMappings(t, s, ctx, org, project, connection, region, "hetzner")
}

func exerciseOwnershipMappings(t *testing.T, s *Service, ctx context.Context, org, project, connection, region, cloud string) {
	t.Helper()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "UPDATE connections SET provider=$2 WHERE id=$1", connection, cloud); err != nil {
		t.Fatal(err)
	}
	var stateID string
	if err = tx.QueryRow(ctx, "SELECT state_id FROM automation_projects WHERE id=$1", project).Scan(&stateID); err != nil {
		t.Fatal(err)
	}
	q := s.q.WithTx(tx)
	cases := []struct{ typ, kind, field, id string }{
		{"aws_s3_bucket", "storage.bucket", "id", "bucket-0"},
		{"aws_s3_bucket_policy", "storage.bucket", "bucket", "bucket-1"},
		{"aws_s3_bucket_versioning", "storage.bucket", "bucket", "bucket-2"},
		{"aws_s3_bucket_lifecycle_configuration", "storage.bucket", "bucket", "bucket-3"},

		{"aws_placement_group", "compute.placement_group", "placement_group_id", "pg-1234567890abcdef0"},
		{"aws_elb", "network.load_balancer", "id", "classic-production"},
		{"aws_eks_cluster", "kubernetes.cluster", "id", "production"},
		{"aws_eks_node_group", "kubernetes.node_group", "id", "production:workers"},
		{"aws_route_table", "network.route_table", "id", "rtb-1234567890abcdef0"},
		{"aws_internet_gateway", "network.internet_gateway", "id", "igw-1234567890abcdef0"},
		{"aws_nat_gateway", "network.nat_gateway", "id", "nat-1234567890abcdef0"},
		{"aws_ami", "compute.image", "id", "ami-1234567890abcdef0"},
		{"aws_ami_copy", "compute.image", "id", "ami-1234567890abcdef1"},
		{"aws_ami_from_instance", "compute.image", "id", "ami-1234567890abcdef2"},
		{"aws_db_instance", "database.instance", "identifier", "production-db"},
		{"aws_rds_cluster_instance", "database.instance", "identifier", "production-member"},
		{"aws_rds_cluster", "database.cluster", "cluster_identifier", "production-cluster"},
	}
	source := "hashicorp/aws"
	if cloud == "digitalocean" {
		source = "digitalocean/digitalocean"
		cases = []struct{ typ, kind, field, id string }{
			{"digitalocean_database_cluster", "database.cluster", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
			{"digitalocean_kubernetes_cluster", "kubernetes.cluster", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
			{"digitalocean_kubernetes_node_pool", "kubernetes.node_group", "id", "506f78a4-e098-11e5-ad9f-000f53306ae2"},
			{"digitalocean_app", "application.app", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
			{"digitalocean_project", "organization.project", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		}
	}
	if cloud == "hetzner" {
		source = "hetznercloud/hcloud"
		cases = []struct{ typ, kind, field, id string }{{"hcloud_placement_group", "compute.placement_group", "id", "445566"}}
	}

	resources := []any{}
	for _, tc := range cases {
		resources = append(resources, map[string]any{"mode": "managed", "type": tc.typ, "provider": `provider["registry.terraform.io/` + source + `"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": "db-INTERNAL-NOT-INVENTORY", tc.field: tc.id, "node_pool": []any{map[string]any{"id": "506f78a4-e098-11e5-ad9f-000f53306ae2"}}}}}})
	}
	raw, err := json.Marshal(map[string]any{"version": 4, "serial": 1, "lineage": "00112233-4455-6677-8899-aabbccddeeff", "resources": resources})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.indexOwnership(ctx, q, database.AutomationState{ID: stateID, OrgID: org, ProjectID: project}, raw); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		for _, action := range []string{"delete", "resize", "tags"} {
			if err = ownershipGuard(ctx, q, org, connection, tc.kind, tc.id, region, action); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("AWS state reference unprotected", tc.typ, action, err)
			}
			if err = ownershipGuard(ctx, q, org, connection, tc.kind, tc.id, "different-region", action); (cloud == "aws" && err != nil) || (cloud != "aws" && connect.CodeOf(err) != connect.CodeFailedPrecondition) {
				t.Fatal("Incorrect ownership region boundary", err)
			}
		}
	}
	if cloud == "aws" {
		// The same native ID can exist in separate AWS regions. Explicit state region
		// wins for new claims, while old conservative claims remain protected.
		for _, item := range resources {
			item.(map[string]any)["instances"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["region"] = "us-west-2"
		}
		const freshID = "i-9876543210abcdef0"
		resources = append(resources, map[string]any{"mode": "managed", "type": "aws_instance", "provider": `provider["registry.terraform.io/hashicorp/aws"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": freshID, "region": "us-west-2"}}}})
		raw, err = json.Marshal(map[string]any{"version": 4, "serial": 1, "lineage": "00112233-4455-6677-8899-aabbccddeeff", "resources": resources})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.indexOwnership(ctx, q, database.AutomationState{ID: stateID, OrgID: org, ProjectID: project}, raw); err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			for _, protectedRegion := range []string{region, "us-west-2"} {
				if err = ownershipGuard(ctx, q, org, connection, tc.kind, tc.id, protectedRegion, "delete"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatal("regional/retained claim lost", tc.typ, protectedRegion, err)
				}
			}
		}
		if err = ownershipGuard(ctx, q, org, connection, "compute.server", freshID, "us-west-2", "delete"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("explicit region unprotected", err)
		}
		if err = ownershipGuard(ctx, q, org, connection, "compute.server", freshID, "us-east-2", "delete"); err != nil {
			t.Fatal("explicit region claimed unrelated region", err)
		}
	}
	if cloud == "aws" {
		for _, typ := range []string{"aws_lb", "aws_alb"} {
			id := "arn:aws:elasticloadbalancing:us-west-2:123456789012:loadbalancer/app/" + typ + "/0123456789abcdef"
			// AWS names do not contain underscores.
			id = strings.ReplaceAll(id, "aws_", "aws-")
			raw, err := json.Marshal(map[string]any{"version": 4, "serial": 1, "lineage": "00112233-4455-6677-8899-aabbccddeeff", "resources": []any{map[string]any{"mode": "managed", "type": typ, "provider": `provider["registry.terraform.io/hashicorp/aws"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": id}}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.indexOwnership(ctx, q, database.AutomationState{ID: stateID, OrgID: org, ProjectID: project}, raw); err != nil {
				t.Fatal(err)
			}
			if err = ownershipGuard(ctx, q, org, connection, "network.load_balancer", id, "us-west-2", "delete"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("ARN region unprotected", err)
			}
			if err = ownershipGuard(ctx, q, org, connection, "network.load_balancer", id, region, "delete"); err != nil {
				t.Fatal("ARN incorrectly claimed project region", err)
			}
		}
	}
	summary, err := q.ProjectOwnershipSummary(ctx, database.ProjectOwnershipSummaryParams{OrgID: org, ID: project})
	if err != nil || summary.Status != "complete" || summary.ScannerVersion != 11 {
		t.Fatal("AWS scan incomplete", summary, err)
	}
}
