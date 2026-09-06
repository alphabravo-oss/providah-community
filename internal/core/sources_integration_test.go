//go:build integration

package core

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

func exerciseSources(t *testing.T, s *Service, owner, viewer providahv1connect.ConsoleServiceClient, ctx context.Context, org, connection string) {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, e := z.CreateHeader(&zip.FileHeader{Name: "main.tf", Method: zip.Store})
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write(bytes.Repeat([]byte("# source\n"), 12000))
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	request := &pb.ImportAutomationSourceRequest{OrganizationId: org, Name: "Immutable source", Runtime: "opentofu", Entrypoint: "main.tf", Archive: b.Bytes()}
	if _, e = viewer.ImportAutomationSource(ctx, connect.NewRequest(request)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("nonpublisher imported source", e)
	}
	result, e := owner.ImportAutomationSource(ctx, connect.NewRequest(request))
	if e != nil {
		t.Fatal(e)
	}
	exact, e := owner.GetAutomationSource(ctx, connect.NewRequest(&pb.GetAutomationSourceRequest{OrganizationId: org, Id: result.Msg.Id}))
	if e != nil || exact.Msg.Sha256 != result.Msg.Sha256 || exact.Msg.Entrypoint != "main.tf" {
		t.Fatal("exact source lookup failed", e)
	}
	for _, target := range []struct {
		org, id string
		code    connect.Code
	}{{org, "invalid", connect.CodeInvalidArgument}, {org, randomID(), connect.CodePermissionDenied}, {randomID(), result.Msg.Id, connect.CodePermissionDenied}} {
		_, e = owner.GetAutomationSource(ctx, connect.NewRequest(&pb.GetAutomationSourceRequest{OrganizationId: target.org, Id: target.id}))
		if connect.CodeOf(e) != target.code {
			t.Fatal("source scope validation failed", e)
		}
	}
	request.Name = "Changed name"
	again, e := owner.ImportAutomationSource(ctx, connect.NewRequest(request))
	if e != nil || again.Msg.Id != result.Msg.Id || again.Msg.Name != "Immutable source" {
		t.Fatal("source retry mutated version", e)
	}
	var encrypted []byte
	if e = s.pool.QueryRow(ctx, "SELECT ciphertext FROM automation_sources WHERE id=$1", result.Msg.Id).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	var storage string
	if e = s.pool.QueryRow(ctx, "SELECT storage FROM automation_sources WHERE id=$1", result.Msg.Id).Scan(&storage); e != nil {
		t.Fatal(e)
	}
	if s.cfg.Artifacts != nil && (storage != "s3" || len(encrypted) > 4096) {
		t.Fatal("source body remained in database")
	}
	plain, e := s.openArtifact(ctx, "sources/"+org+"/"+result.Msg.Id, storage, encrypted, 4<<20)
	if e != nil || plain != b.String() || bytes.Contains(encrypted, []byte("terraform {}")) {
		t.Fatal("source was not preserved encrypted", e)
	}
	listed, e := owner.ListAutomationSources(ctx, connect.NewRequest(&pb.ListAutomationSourcesRequest{OrganizationId: org}))
	if e != nil || len(listed.Msg.Sources) != 1 || listed.Msg.Sources[0].Sha256 != result.Msg.Sha256 {
		t.Fatal("catalog mismatch", e)
	}
	request.OrganizationId = "other-tenant"
	if _, e = owner.ImportAutomationSource(ctx, connect.NewRequest(request)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("cross tenant import accepted", e)
	}
	request.OrganizationId = org
	request.Entrypoint = "../main.tf"
	if _, e = owner.ImportAutomationSource(ctx, connect.NewRequest(request)); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("unsafe entrypoint accepted", e)
	}
	var count int
	if e = s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='automation.source_imported'", org).Scan(&count); e != nil || count != 1 {
		t.Fatal("missing or duplicate import audit", count, e)
	}

	saved := s.automationRuntimes
	defer func() { s.automationRuntimes = saved }()
	image := "sha256:" + strings.Repeat("a", 64)
	s.automationRuntimes = []automation.Runtime{{Runtime: "opentofu", Image: image, Version: "1.10.0"}}
	publication := &pb.PublishAutomationVersionRequest{OrganizationId: org, Name: "Reviewed automation", SourceId: result.Msg.Id, RuntimeImage: image, ConnectionId: connection, Region: "fsn1", ReviewNote: "Reviewed source and dependencies in pinned image"}
	if _, e = viewer.PublishAutomationVersion(ctx, connect.NewRequest(publication)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("nonpublisher published", e)
	}
	publication.RuntimeImage = "image:latest"
	if _, e = owner.PublishAutomationVersion(ctx, connect.NewRequest(publication)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("unapproved image accepted", e)
	}
	publication.RuntimeImage = image
	version, e := owner.PublishAutomationVersion(ctx, connect.NewRequest(publication))
	if e != nil {
		t.Fatal(e)
	}
	direct, e := owner.GetAutomationVersion(ctx, connect.NewRequest(&pb.GetAutomationVersionRequest{OrganizationId: org, Id: version.Msg.Id}))
	if e != nil || direct.Msg.Id != version.Msg.Id || direct.Msg.RuntimeImage != image {
		t.Fatal("direct version read failed", e)
	}
	for _, target := range []*pb.GetAutomationVersionRequest{{OrganizationId: randomID(), Id: version.Msg.Id}, {OrganizationId: org, Id: randomID()}} {
		_, e = owner.GetAutomationVersion(ctx, connect.NewRequest(target))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("version scope leaked", e)
		}
	}
	_, e = owner.GetAutomationVersion(ctx, connect.NewRequest(&pb.GetAutomationVersionRequest{OrganizationId: org, Id: "invalid"}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid version ID accepted")
	}
	repeated, e := owner.PublishAutomationVersion(ctx, connect.NewRequest(publication))
	if e != nil || version.Msg.Id != repeated.Msg.Id {
		t.Fatal("publication retry changed version", e)
	}
	var revision int64
	if e = s.pool.QueryRow(ctx, "SELECT revision FROM connections WHERE id=$1", connection).Scan(&revision); e != nil || revision != version.Msg.ConnectionRevision {
		t.Fatal("connection revision not pinned", e)
	}
	publication.ReviewNote = "Changed review"
	if _, e = owner.PublishAutomationVersion(ctx, connect.NewRequest(publication)); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale publication accepted", e)
	}
	publication.ExpectedVersion = 1
	next, e := owner.PublishAutomationVersion(ctx, connect.NewRequest(publication))
	if e != nil || next.Msg.Version != 2 {
		t.Fatal("new publication failed", e)
	}
	exerciseAutomationValidation(t, s, owner, viewer, ctx, org, next.Msg.Id)
	exerciseManagedState(t, s, owner, viewer, ctx, org, next.Msg.Id)
	exerciseProjectInputs(t, s, owner, ctx, org, connection, image)
	publication.ExpectedVersion = 2
	publication.SourceId = "other-tenant-source"
	if _, e = owner.PublishAutomationVersion(ctx, connect.NewRequest(publication)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign source published", e)
	}
	for _, status := range []string{"retired", "revoked"} {
		updated, e := owner.SetAutomationVersionStatus(ctx, connect.NewRequest(&pb.SetAutomationVersionStatusRequest{OrganizationId: org, Id: version.Msg.Id, Status: status}))
		if e != nil || updated.Msg.Status != status {
			t.Fatal("version lifecycle failed", e)
		}
	}
	if _, e = owner.SetAutomationVersionStatus(ctx, connect.NewRequest(&pb.SetAutomationVersionStatusRequest{OrganizationId: org, Id: version.Msg.Id, Status: "published"})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("revoked version resurrected", e)
	}

	s.automationRuntimes[0].DependencyHosts = []string{"example.com"}
	changed, e := owner.ListAutomationVersions(ctx, connect.NewRequest(&pb.ListAutomationVersionsRequest{OrganizationId: org}))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range changed.Msg.Versions {
		if v.RuntimeAvailable {
			t.Fatal("network authority expanded without republication")
		}
	}
	s.automationRuntimes = nil
	versions, e := owner.ListAutomationVersions(ctx, connect.NewRequest(&pb.ListAutomationVersionsRequest{OrganizationId: org}))
	if e != nil || len(versions.Msg.Versions) != 3 {
		t.Fatal("catalog failed", e)
	}
	for _, v := range versions.Msg.Versions {
		if v.RuntimeAvailable {
			t.Fatal("removed image still available")
		}
	}
	exerciseRealAutomation(t, s, owner, ctx, org, connection)
}
