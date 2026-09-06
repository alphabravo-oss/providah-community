//go:build integration

package core

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5/pgtype"
)

func exerciseIdentityPolicy(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, base string, ctx context.Context) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	user, err := s.q.UserByEmail(ctx, "owner@example.com")
	check(err)
	organizations, err := s.q.OrganizationsForUser(ctx, user.ID)
	check(err)
	org := organizations[0].ID
	original, err := s.q.UserOIDCLink(ctx, database.UserOIDCLinkParams{UserID: user.ID, Issuer: s.oidc.config.Issuer})
	check(err)
	recoveryID := randomID()
	_, err = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext,last_totp_step) SELECT $1,'sso-recovery@example.test',password_hash,totp_ciphertext,0 FROM users WHERE id=$2", recoveryID, user.ID)
	check(err)
	check(s.q.JoinMembership(ctx, database.JoinMembershipParams{OrgID: org, UserID: recoveryID, RoleID: pgtype.Text{String: "administrator", Valid: true}}))
	localClient := func(uid string) (providahv1connect.ConsoleServiceClient, *http.Client) {
		sid, refresh := randomID(), randomID()
		check(s.q.CreateSession(ctx, database.CreateSessionParams{ID: sid, UserID: uid, RefreshHash: hash(refresh)}))
		h := http.Header{}
		check(s.issue(h, sid, refresh))
		response := &http.Response{Header: h}
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse(base + "/api/")
		jar.SetCookies(u, response.Cookies())
		httpClient := &http.Client{Jar: jar, Timeout: 6 * time.Second}
		return providahv1connect.NewConsoleServiceClient(httpClient, base+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Origin", s.cfg.Origin)
				return next(ctx, r)
			}
		}))), httpClient
	}
	local, localHTTP := localClient(user.ID)
	recovery, _ := localClient(recoveryID)
	request := &pb.SaveIdentityPolicyRequest{OrganizationId: org, Enabled: true, RecoveryUsers: []string{recoveryID}, Reason: "Require the verified organization identity provider"}
	_, err = local.SaveIdentityPolicy(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("local sign-in enabled mandatory SSO", err)
	}
	var connection string
	check(s.pool.QueryRow(ctx, "SELECT id FROM connections WHERE org_id=$1 AND provider='hetzner' AND enabled LIMIT 1", org).Scan(&connection))
	resource := randomID()
	check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: resource, OrgID: org, ConnectionID: connection, Provider: "hetzner", Kind: "compute.server", NativeID: "6789", Name: "SSO policy test server", Region: "fsn1", Status: "off"}))
	prior := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = prior }()
	calls := 0
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		calls++
		return provider.Response{Version: provider.Protocol, Power: &provider.PowerResult{Outcome: "succeeded", Status: "running"}}, nil
	}
	operation := func(client providahv1connect.ConsoleServiceClient) *pb.Operation {
		out, e := client.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource, Action: "start", ExpectedStatus: "off", Reason: "Validate the recorded sign-in identity", IdempotencyKey: randomID()}))
		check(e)
		return out.Msg.Operation
	}
	queued := operation(local)
	// Isolate this schedule from older clock fixtures in the same disposable database.
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET next_due=NULL,enabled=false")
	check(err)
	scheduled, err := local.SaveSchedule(ctx, connect.NewRequest(&pb.SaveScheduleRequest{OrganizationId: org, Name: "SSO standing approval", ResourceIds: []string{resource}, Spec: &pb.ScheduleSpec{Action: "start", Timezone: "UTC", RunAt: time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04")}}))
	check(err)
	_, err = recovery.ApproveSchedule(ctx, connect.NewRequest(&pb.ApproveScheduleRequest{OrganizationId: org, Id: scheduled.Msg.Schedule.Id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, scheduled.Msg.Schedule.Id)}))
	check(err)
	streamRequest, _ := http.NewRequest("GET", base+"/api/events?organization_id="+org, nil)
	streamRequest.Header.Set("Origin", s.cfg.Origin)
	stream, err := localHTTP.Do(streamRequest)
	check(err)
	if stream.StatusCode != 200 {
		t.Fatal("local stream unavailable before policy")
	}
	streamResult := make(chan string, 1)
	go func() { body, _ := io.ReadAll(stream.Body); _ = stream.Body.Close(); streamResult <- string(body) }()
	request.RecoveryUsers = nil
	_, err = owner.SaveIdentityPolicy(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("SSO allowed without recovery")
	}
	request.RecoveryUsers = []string{recoveryID}
	saved, err := owner.SaveIdentityPolicy(ctx, connect.NewRequest(request))
	check(err)
	if !saved.Msg.Enabled || saved.Msg.Issuer != s.oidc.config.Issuer {
		t.Fatal("identity policy not saved")
	}
	_, err = owner.SaveIdentityPolicy(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale policy update accepted")
	}
	_, err = local.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("local API bypassed mandatory SSO")
	}
	session, err := local.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	check(err)
	if !session.Msg.Organizations[0].SsoRequired || len(session.Msg.Organizations[0].Permissions) != 0 {
		t.Fatal("session advertised inaccessible organization permissions")
	}
	_, err = recovery.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	check(err)
	_, err = owner.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	check(err)
	select {
	case body := <-streamResult:
		if !strings.Contains(body, "event: revoked") {
			t.Fatal("SSO did not revoke open SSE", body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("SSO stream revocation exceeded bound")
	}
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: recoveryID, ExpectedRevision: membershipRevision(t, s, ctx, org, recoveryID), RoleId: "viewer", Active: true}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("removed the last recovery administrator", err)
	}
	run := func(id string) {
		job, e := s.q.ClaimOperation(ctx)
		check(e)
		if job.ID != id {
			t.Fatal("unexpected queued operation")
		}
		check(s.runOperation(ctx, job))
	}
	run(queued.Id)
	result, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: queued.Id})
	check(err)
	if result.Status != "canceled" || !strings.Contains(result.Detail, "SSO policy") || calls != 0 {
		t.Fatal("queued local action bypassed SSO", result.Detail)
	}
	_, err = s.pool.Exec(ctx, "UPDATE schedules SET next_due=now()-interval '1 second' WHERE id=$1", scheduled.Msg.Schedule.Id)
	check(err)
	check(s.scheduleTick(ctx, time.Now()))
	var detail string
	check(s.pool.QueryRow(ctx, "SELECT detail FROM schedule_occurrences WHERE schedule_id=$1 ORDER BY scheduled_for DESC LIMIT 1", scheduled.Msg.Schedule.Id).Scan(&detail))
	if !strings.Contains(detail, "SSO policy") {
		t.Fatal("standing schedule bypassed SSO", detail)
	}
	_, err = owner.DeleteSchedule(ctx, connect.NewRequest(&pb.DeleteScheduleRequest{OrganizationId: org, Id: scheduled.Msg.Schedule.Id, ExpectedRevision: scheduleTestRevision(t, s, ctx, org, scheduled.Msg.Schedule.Id), Confirmation: "SSO standing approval"}))
	check(err)
	allowed := operation(owner)
	stamped, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: allowed.Id})
	check(err)
	if stamped.RequesterOidcID != original.ID {
		t.Fatal("request identity not preserved")
	}
	run(allowed.Id)
	if calls != 1 {
		t.Fatal("valid OIDC request did not execute")
	}
	// Removing and recreating an identical issuer/subject must not revive an old request identity.
	_, err = s.pool.Exec(ctx, "UPDATE resources SET status='off' WHERE id=$1", resource)
	check(err)
	oldProof := operation(owner)
	check(s.q.UnlinkOIDC(ctx, database.UnlinkOIDCParams{UserID: user.ID, Issuer: s.oidc.config.Issuer}))
	check(s.q.LinkOIDC(ctx, database.LinkOIDCParams{UserID: user.ID, Issuer: original.Issuer, Subject: original.Subject}))
	run(oldProof.Id)
	result, err = s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: oldProof.Id})
	check(err)
	if result.Status != "canceled" || calls != 1 {
		t.Fatal("unlink/relink revived an old proof")
	}
	request.Enabled = false
	request.ExpectedRevision = saved.Msg.Revision
	request.Reason = "Restore local access through the designated recovery administrator"
	_, err = recovery.SaveIdentityPolicy(ctx, connect.NewRequest(request))
	check(err)
	_, err = local.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	check(err)
}
