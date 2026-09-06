//go:build integration

package core

import (
	"bufio"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

func exerciseAccess(t *testing.T, s *Service, owner providahv1connect.ConsoleServiceClient, ctx context.Context, org string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	member := providahv1connect.NewConsoleServiceClient(&http.Client{Jar: jar}, server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Origin", s.cfg.Origin)
			return next(ctx, r)
		}
	})))
	_, err := owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Name: "Inventory team", Permissions: []string{"connections.read", "resources.read"}}))
	check(err)
	access, err := owner.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	check(err)
	var roleID string
	for _, r := range access.Msg.Roles {
		if r.Name == "Inventory team" {
			roleID = r.Id
		}
	}
	if roleID == "" {
		t.Fatal("role missing")
	}
	invite, err := owner.CreateInvitation(ctx, connect.NewRequest(&pb.CreateInvitationRequest{OrganizationId: org, Email: "member@example.com", RoleId: roleID}))
	check(err)
	token := strings.Split(invite.Msg.Link, "#")[1]
	enrolled, err := member.BeginInvitation(ctx, connect.NewRequest(&pb.BeginInvitationRequest{Token: token}))
	check(err)
	code, _ := integrationTOTP(enrolled.Msg.Secret)
	accepted, err := member.AcceptInvitation(ctx, connect.NewRequest(&pb.AcceptInvitationRequest{Token: token, Password: "member-password-long", Code: code}))
	check(err)
	if accepted.Msg.Email != "member@example.com" {
		t.Fatal("wrong invite identity")
	}
	_, err = member.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	check(err)
	_, err = member.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "Denied", Provider: "hetzner", Credential: "test-secret"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("viewer wrote: %v", err)
	}
	_, err = member.AcceptInvitation(ctx, connect.NewRequest(&pb.AcceptInvitationRequest{Token: token, Password: "member-password-long", Code: code}))
	if err == nil {
		t.Fatal("invitation replay accepted")
	}
	// Role changes affect existing sessions on the next request.
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Id: roleID, ExpectedRevision: 1, Name: "Inventory team", Permissions: []string{"connections.read", "resources.read", "audit.read"}}))
	check(err)
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Id: roleID, ExpectedRevision: 1, Name: "Stale role", Permissions: []string{"connections.read"}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale role update accepted", err)
	}
	current, err := owner.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	check(err)
	for _, role := range current.Msg.Roles {
		if role.Id == roleID && (role.Revision != 2 || role.Name != "Inventory team" || !slices.Contains(role.Permissions, "audit.read")) {
			t.Fatal("stale update changed role", role)
		}
	}

	_, err = member.ListAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{OrganizationId: org}))
	check(err)
	access, err = owner.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	check(err)
	var memberID, ownerID string
	for _, m := range access.Msg.Members {
		if m.Email == "member@example.com" {
			memberID = m.UserId
		} else {
			ownerID = m.UserId
		}
	}
	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	streamReq, _ := http.NewRequestWithContext(streamCtx, "GET", server.URL+"/api/events?organization_id="+org, nil)
	streamReq.Header.Set("Origin", s.cfg.Origin)
	stream, err := (&http.Client{Jar: jar}).Do(streamReq)
	check(err)
	defer func() { _ = stream.Body.Close() }()
	if stream.StatusCode != 200 || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE failed: %d", stream.StatusCode)
	}
	lines := bufio.NewReader(stream.Body)
	readEvent := func(want string) {
		t.Helper()
		for {
			line, err := lines.ReadString('\n')
			check(err)
			if strings.TrimSpace(line) == "event: "+want {
				return
			}
		}
	}
	readEvent("change")
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: memberID, ExpectedRevision: membershipRevision(t, s, ctx, org, memberID), RoleId: roleID, Active: false}))
	check(err)
	savedRevision := membershipRevision(t, s, ctx, org, memberID)
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: memberID, RoleId: roleID, Active: true, ExpectedRevision: savedRevision - 1}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale membership update accepted", err)
	}
	if membershipRevision(t, s, ctx, org, memberID) != savedRevision {
		t.Fatal("stale update changed revision")
	}

	_, err = member.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("disabled membership retained access")
	}
	readEvent("revoked")
	_ = stream.Body.Close()
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: ownerID, ExpectedRevision: membershipRevision(t, s, ctx, org, ownerID), RoleId: "viewer", Active: true}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("last admin demoted: %v", err)
	}
	// A scoped manager cannot escalate themselves to administrator.
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Id: roleID, ExpectedRevision: 2, Name: "Inventory team", Permissions: []string{"connections.read", "members.manage", "members.read"}}))
	check(err)
	_, err = owner.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: memberID, ExpectedRevision: membershipRevision(t, s, ctx, org, memberID), RoleId: roleID, Active: true}))
	check(err)
	_, err = member.UpdateMember(ctx, connect.NewRequest(&pb.UpdateMemberRequest{OrganizationId: org, UserId: memberID, ExpectedRevision: membershipRevision(t, s, ctx, org, memberID), RoleId: "administrator", Active: true}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("member self-escalated")
	}
	pending, err := owner.CreateInvitation(ctx, connect.NewRequest(&pb.CreateInvitationRequest{OrganizationId: org, Email: "pending@example.com", RoleId: "viewer"}))
	check(err)
	access, err = owner.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	check(err)
	_, err = owner.RevokeInvitation(ctx, connect.NewRequest(&pb.RevokeInvitationRequest{OrganizationId: org, Id: access.Msg.Invitations[0].Id}))
	check(err)
	_, err = member.BeginInvitation(ctx, connect.NewRequest(&pb.BeginInvitationRequest{Token: strings.Split(pending.Msg.Link, "#")[1]}))
	if err == nil {
		t.Fatal("revoked invitation accepted")
	}
	// MFA must be fresh for access changes, and a code cannot be replayed.
	_, err = s.pool.Exec(ctx, "UPDATE sessions SET mfa_at=now()-interval '6 minutes' WHERE user_id=$1", ownerID)
	check(err)
	_, err = owner.CreateInvitation(ctx, connect.NewRequest(&pb.CreateInvitationRequest{OrganizationId: org, Email: "fresh@example.com", RoleId: "viewer"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale MFA accepted for access change")
	}
	user, err := s.q.UserByEmail(ctx, "owner@example.com")
	check(err)
	seed, err := s.open(user.TotpCiphertext)
	check(err)
	// Model the last accepted code belonging to a previous period without a wall-clock wait.
	_, err = s.pool.Exec(ctx, "UPDATE users SET last_totp_step=$2 WHERE id=$1", ownerID, time.Now().Unix()/30-1)
	check(err)
	freshCode, _ := integrationTOTP(seed)
	_, err = owner.VerifyMfa(ctx, connect.NewRequest(&pb.VerifyMfaRequest{Code: freshCode}))
	check(err)
	_, err = owner.VerifyMfa(ctx, connect.NewRequest(&pb.VerifyMfaRequest{Code: freshCode}))
	if err == nil {
		t.Fatal("MFA code replay accepted")
	}
	changed, err := owner.CreateInvitation(ctx, connect.NewRequest(&pb.CreateInvitationRequest{OrganizationId: org, Email: "changed@example.com", RoleId: roleID}))
	check(err)
	_, err = owner.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Id: roleID, ExpectedRevision: 3, Name: "Inventory team", Permissions: []string{"connections.read"}}))
	check(err)
	_, err = member.BeginInvitation(ctx, connect.NewRequest(&pb.BeginInvitationRequest{Token: strings.Split(changed.Msg.Link, "#")[1]}))
	if err == nil {
		t.Fatal("role-changed invitation accepted")
	}
	// Token values never appear in the invitation or auth-limit tables.
	var leaked int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM auth_limits WHERE key LIKE '%' || $1 || '%'", token).Scan(&leaked))
	if leaked != 0 {
		t.Fatal("invitation token stored in plaintext")
	}
	exerciseOperations(t, s, owner, member, ctx, org, memberID)
}

func membershipRevision(t *testing.T, s *Service, ctx context.Context, org, user string) int64 {
	t.Helper()
	var revision int64
	if e := s.pool.QueryRow(ctx, "SELECT revision FROM memberships WHERE org_id=$1 AND user_id=$2", org, user).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	return revision
}
