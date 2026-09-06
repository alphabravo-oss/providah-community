//go:build integration

package core

import (
	"context"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

func exerciseGlobalAdministrator(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	user, org := randomID(), randomID()
	email := user + "@example.test"
	password := "global-fixture-password"
	seed := "JBSWY3DPEHPK3PXP"
	cipher, e := s.seal(seed)
	check(e)
	hashed, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext) VALUES($1,$2,$3,$4)", user, email, hashed, cipher)
	check(e)
	check(s.q.CreateOrganization(ctx, database.CreateOrganizationParams{ID: org, Name: "Global admin foreign organization"}))
	check(s.q.CreateBuiltinRoles(ctx, org))
	server := httptest.NewServer(s.Handler(""))
	defer server.Close()
	httpClient := server.Client()
	httpClient.Jar, _ = cookiejar.New(nil)
	client := providahv1connect.NewConsoleServiceClient(httpClient, server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Origin", s.cfg.Origin)
			return next(ctx, r)
		}
	})))
	login := func(code string) (*connect.Response[pb.SessionResponse], error) {
		return client.Login(ctx, connect.NewRequest(&pb.LoginRequest{Email: email, Password: password, Code: code}))
	}
	prompt, e := login("")
	check(e)
	if !prompt.Msg.MfaRequired || len(prompt.Header().Values("Set-Cookie")) != 0 || len(prompt.Msg.Organizations) != 0 {
		t.Fatal("MFA prompt issued authority")
	}
	_, e = client.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("ordinary account bypassed MFA")
	}
	check(s.SeedAdministrator(ctx, email))
	check(s.SeedAdministrator(ctx, email))
	session, e := login("")
	check(e)
	if !session.Msg.GlobalAdmin || session.Msg.MfaEnabled {
		t.Fatal("seeded authority or optional MFA missing")
	}
	found := false
	for _, o := range session.Msg.Organizations {
		if o.Id == org {
			found = true
		}
	}
	if !found {
		t.Fatal("global admin cannot see organization without membership")
	}
	// Global metadata includes disabled entries, with stable filter-bound pagination.
	prefix := randomID()
	_, e = s.pool.Exec(ctx, "INSERT INTO organizations(id,name,active) SELECT $1 || lpad(n::text,3,'0'),$1 || n::text,false FROM generate_series(1,101) n", prefix)
	check(e)
	directory, e := client.ListInstallationDirectory(ctx, connect.NewRequest(&pb.ListInstallationDirectoryRequest{Kind: "organizations", Search: prefix, State: "disabled"}))
	check(e)
	if len(directory.Msg.Organizations) != 100 || directory.Msg.NextPageToken == "" || directory.Msg.Organizations[0].Active {
		t.Fatal("disabled organization directory pagination missing")
	}
	next, e := client.ListInstallationDirectory(ctx, connect.NewRequest(&pb.ListInstallationDirectoryRequest{Kind: "organizations", Search: prefix, State: "disabled", PageToken: directory.Msg.NextPageToken}))
	check(e)
	if len(next.Msg.Organizations) != 1 || next.Msg.NextPageToken != "" || next.Msg.Organizations[0].Id <= directory.Msg.Organizations[99].Id {
		t.Fatal("directory continuation incorrect")
	}
	_, e = client.ListInstallationDirectory(ctx, connect.NewRequest(&pb.ListInstallationDirectoryRequest{Kind: "users", PageToken: directory.Msg.NextPageToken}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("directory cursor crossed filters")
	}
	users, e := client.ListInstallationDirectory(ctx, connect.NewRequest(&pb.ListInstallationDirectoryRequest{Kind: "users", Search: email, State: "active"}))
	check(e)
	if len(users.Msg.Users) != 1 || users.Msg.Users[0].Id != user || !users.Msg.Users[0].GlobalAdmin || users.Msg.Users[0].MfaEnabled {
		t.Fatal("installation user metadata incorrect")
	}
	// Installation access writes require fresh proof, prevent self-lockout,
	// compare revisions, revoke target sessions and leave an attributed audit.
	target := randomID()
	_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext,mfa_enabled) VALUES($1,$2,$3,$4,false)", target, target+"@example.test", hashed, cipher)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO sessions(id,user_id,refresh_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 day')", randomID(), target, hash(randomID()))
	check(e)
	change := &pb.SetInstallationUserRequest{Id: target, ExpectedRevision: 1, Password: "wrong"}
	_, e = client.SetInstallationUser(ctx, connect.NewRequest(change))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("installation write accepted wrong password")
	}
	change.Password = password
	_, e = client.SetInstallationUser(ctx, connect.NewRequest(change))
	check(e)
	var active, targetGlobal bool
	var revision int64
	var sessionCount, eventCount int
	check(s.pool.QueryRow(ctx, "SELECT active,global_admin,admin_revision FROM users WHERE id=$1", target).Scan(&active, &targetGlobal, &revision))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", target).Scan(&sessionCount))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM installation_events WHERE actor_id=$1 AND target_id=$2 AND details->>'active'='false'", user, target).Scan(&eventCount))
	if active || targetGlobal || revision != 2 || sessionCount != 0 || eventCount != 1 {
		t.Fatal("user disable, revision, session revocation or audit failed")
	}
	_, e = client.SetInstallationUser(ctx, connect.NewRequest(change))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale installation user edit accepted", e)
	}
	change.ExpectedRevision = 2
	change.Active = true
	change.GlobalAdmin = true
	_, e = client.SetInstallationUser(ctx, connect.NewRequest(change))
	check(e)
	check(s.pool.QueryRow(ctx, "SELECT active,global_admin,admin_revision FROM users WHERE id=$1", target).Scan(&active, &targetGlobal, &revision))
	if !active || !targetGlobal || revision != 3 {
		t.Fatal("user reactivation/global grant failed")
	}
	_, e = s.pool.Exec(ctx, "INSERT INTO installation_events(actor_id,target_id,action,details) SELECT $1,$2,'fixture.global','{}'::jsonb FROM generate_series(1,101)", user, target)
	check(e)
	audits, e := client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "installation", Actor: email, Action: "fixture.global", Target: target}))
	check(e)
	if len(audits.Msg.Events) != 100 || audits.Msg.NextPageToken == "" || audits.Msg.Events[0].Actor != email {
		t.Fatal("global audit pagination/filter failed")
	}
	older, e := client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "installation", Actor: email, Action: "fixture.global", Target: target, PageToken: audits.Msg.NextPageToken}))
	check(e)
	if len(older.Msg.Events) != 1 || older.Msg.NextPageToken != "" {
		t.Fatal("global audit continuation failed")
	}
	_, e = client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "organizations", PageToken: audits.Msg.NextPageToken}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("global audit cursor crossed source")
	}
	_, e = s.pool.Exec(ctx, "INSERT INTO audit_events(org_id,actor,action,target) VALUES($1,$2,'fixture.disabled',$3)", org, email, target)
	check(e)
	orgAudit, e := client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "organizations", OrganizationId: org, Target: target}))
	check(e)
	if len(orgAudit.Msg.Events) != 1 || orgAudit.Msg.Events[0].OrganizationId != org || orgAudit.Msg.Events[0].OrganizationName == "" {
		t.Fatal("cross-organization audit unavailable")
	}
	for _, statement := range []string{"UPDATE installation_events SET action='tampered' WHERE actor_id=$1", "DELETE FROM installation_events WHERE actor_id=$1"} {
		if _, err := s.pool.Exec(ctx, statement, user); err == nil {
			t.Fatal("installation audit is mutable")
		}
	}
	for _, table := range []string{"installation_events", "audit_events"} {
		if _, err := s.pool.Exec(ctx, "TRUNCATE "+table+" CASCADE"); err == nil {
			t.Fatal("audit truncation permitted")
		}
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET email=$2 WHERE id=$1", user, "renamed-"+email)
	check(e)
	snapshot, e := client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "installation", Actor: email, Action: "fixture.global", Target: target}))
	check(e)
	if len(snapshot.Msg.Events) != 100 || snapshot.Msg.Events[0].Actor != email {
		t.Fatal("audit actor changed with current email")
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET email=$2 WHERE id=$1", user, email)
	check(e)
	change.Id = user
	change.ExpectedRevision = 1
	_, e = client.SetInstallationUser(ctx, connect.NewRequest(change))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("self installation grant change accepted")
	}
	_, e = s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key=$1", "mfa:"+user)
	check(e)
	_, e = client.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "Global administration fixture", Provider: "hetzner", Credential: "fake-global-credential"}))
	check(e)
	health, e := client.GetInstallationHealth(ctx, connect.NewRequest(&pb.GetInstallationHealthRequest{}))
	check(e)
	if health.Msg.ObservedAt == "" || health.Msg.DatabaseConnections < 1 || health.Msg.DatabaseCapacity < 1 || health.Msg.Storage == nil || health.Msg.Storage.UsedBytes < 1 || health.Msg.Storage.AuditBytes < 1 || health.Msg.Storage.Status != "unconfigured" {
		t.Fatal("installation health snapshot missing")
	}
	var expectedOperations, reportedOperations int64
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM operations WHERE status IN ('awaiting_approval','queued','dispatching','observing','uncertain')").Scan(&expectedOperations))
	for _, v := range health.Msg.Work {
		if v.Count < 1 || v.OldestSeconds < 0 {
			t.Fatal("invalid workload aggregate")
		}
		if v.Kind == "operation" {
			reportedOperations += v.Count
		}
	}
	if reportedOperations != expectedOperations {
		t.Fatal("global operation health count incorrect")
	}
	teamUser := randomID()
	_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext,mfa_enabled) VALUES($1,$2,$3,$4,false)", teamUser, teamUser+"@example.test", hashed, cipher)
	check(e)
	check(s.q.JoinMembership(ctx, database.JoinMembershipParams{OrgID: org, UserID: teamUser, RoleID: pgtype.Text{String: "viewer", Valid: true}}))
	teamRequest := &pb.SaveTeamRequest{OrganizationId: org, Name: "Operators", RoleId: "operator", Active: true, UserIds: []string{teamUser}}
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	check(e)
	teams, e := client.ListTeams(ctx, connect.NewRequest(&pb.ListTeamsRequest{OrganizationId: org}))
	check(e)
	if len(teams.Msg.Teams) != 1 || len(teams.Msg.Teams[0].UserIds) != 1 {
		t.Fatal("team metadata missing")
	}
	teamRequest.Id = teams.Msg.Teams[0].Id
	teamRequest.ExpectedRevision = 1
	if !powerPermission(ctx, s.q, org, teamUser, "operations.request") {
		t.Fatal("team role did not grant operations")
	}
	teamRequest.Active = false
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	check(e)
	if powerPermission(ctx, s.q, org, teamUser, "operations.request") {
		t.Fatal("disabled team retained grant")
	}
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale team edit accepted")
	}
	teamRequest.ExpectedRevision = 2
	teamRequest.Active = true
	teamRequest.UserIds = []string{target}
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("foreign member joined team")
	}
	teamRequest.UserIds = []string{teamUser}
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	check(e)
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET active=false WHERE org_id=$1 AND user_id=$2", org, teamUser)
	check(e)
	if powerPermission(ctx, s.q, org, teamUser, "operations.request") {
		t.Fatal("team bypassed disabled membership")
	}
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET active=true WHERE org_id=$1 AND user_id=$2", org, teamUser)
	check(e)
	if !powerPermission(ctx, s.q, org, teamUser, "operations.request") {
		t.Fatal("team grant did not restore with membership")
	}
	orgChange := &pb.SetInstallationOrganizationRequest{Id: org, ExpectedRevision: 1, Password: password}
	_, e = client.SetInstallationOrganization(ctx, connect.NewRequest(orgChange))
	check(e)
	_, e = client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("disabled organization retained global cloud access")
	}
	disabledSession, e := client.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	check(e)
	for _, o := range disabledSession.Msg.Organizations {
		if o.Id == org {
			t.Fatal("disabled organization retained session scope")
		}
	}
	_, e = client.SetInstallationOrganization(ctx, connect.NewRequest(orgChange))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale organization state accepted")
	}
	orgEvents, e := client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "organizations", OrganizationId: org, Action: "organization.state_changed"}))
	check(e)
	if len(orgEvents.Msg.Events) != 1 {
		t.Fatal("disabled organization audit missing")
	}
	orgChange.Active = true
	orgChange.ExpectedRevision = 2
	_, e = client.SetInstallationOrganization(ctx, connect.NewRequest(orgChange))
	check(e)
	_, e = client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	check(e)
	_, e = s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key=$1", "mfa:"+user)
	check(e)
	if _, e = s.q.EventRevision(ctx, database.EventRevisionParams{ID: org, UserID: user}); e != nil {
		t.Fatal("global admin SSE authorization missing", e)
	}
	if !identityAllowed(ctx, s.q, org, user, actor(ctx).OIDC) {
		t.Fatal("global identity not authorized")
	}

	_, e = client.CreateOrganization(ctx, connect.NewRequest(&pb.CreateOrganizationRequest{Name: "Denied wrong proof", Password: "wrong-password"}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("organization creation accepted wrong proof")
	}
	_, e = client.CreateOrganization(ctx, connect.NewRequest(&pb.CreateOrganizationRequest{Name: " ", Password: password}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("organization creation accepted blank name")
	}
	created, e := client.CreateOrganization(ctx, connect.NewRequest(&pb.CreateOrganizationRequest{Name: "  New customer  ", Password: password}))
	check(e)
	if created.Msg.Name != "New customer" || len(created.Msg.Permissions) == 0 {
		t.Fatal("organization defaults missing")
	}
	var roles, events, members int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM roles WHERE org_id=$1", created.Msg.Id).Scan(&roles))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='organization.created'", created.Msg.Id).Scan(&events))
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM memberships WHERE org_id=$1", created.Msg.Id).Scan(&members))
	if roles == 0 || events != 1 || members != 0 {
		t.Fatalf("organization initialization: roles=%d events=%d memberships=%d", roles, events, members)
	}
	_, e = client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: created.Msg.Id}))
	check(e)
	// Removing global authority is effective on the very next request, even with an existing session.
	_, e = s.pool.Exec(ctx, "UPDATE users SET global_admin=false WHERE id=$1", user)
	check(e)
	_, e = client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("global role revocation not enforced")
	}
	_, e = client.CreateOrganization(ctx, connect.NewRequest(&pb.CreateOrganizationRequest{Name: "Denied ordinary user", Password: password}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("ordinary account created organization")
	}
	_, e = client.ListInstallationDirectory(ctx, connect.NewRequest(&pb.ListInstallationDirectoryRequest{Kind: "users"}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("revoked global grant retained installation directory")
	}
	check(s.SeedAdministrator(ctx, email))
	u, e := s.q.UserByEmail(ctx, email)
	check(e)
	if u.GlobalAdmin {
		t.Fatal("seed reapplied a revoked global grant")
	}
	// Delegation uses the same server-scoped session directory and grant ceiling.
	role := randomID()
	check(s.q.CreateRole(ctx, database.CreateRoleParams{OrgID: org, ID: role, Name: "Scoped admin reader", Permissions: []string{"admin.access", "members.read", "roles.manage"}}))
	check(s.q.JoinMembership(ctx, database.JoinMembershipParams{OrgID: org, UserID: user, RoleID: pgtype.Text{String: role, Valid: true}}))
	scoped, e := client.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	check(e)
	_, e = client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "installation"}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin read global audit")
	}
	_, e = client.GetInstallationHealth(ctx, connect.NewRequest(&pb.GetInstallationHealthRequest{}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin read global health")
	}
	_, e = client.DeleteTeam(ctx, connect.NewRequest(&pb.DeleteTeamRequest{OrganizationId: org, Id: teamRequest.Id, ExpectedRevision: 3}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("read-only delegate deleted team")
	}
	teamRequest.ExpectedRevision = 3
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("read-only delegated admin edited team")
	}
	_, e = s.pool.Exec(ctx, "UPDATE roles SET permissions=permissions||ARRAY['members.manage'] WHERE org_id=$1 AND id=$2", org, role)
	check(e)
	_, e = client.SaveTeam(ctx, connect.NewRequest(teamRequest))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("team manager exceeded own grant ceiling")
	}
	orgChange.ExpectedRevision = 3
	_, e = client.SetInstallationOrganization(ctx, connect.NewRequest(orgChange))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin changed organization state")
	}
	change.Id = target
	change.ExpectedRevision = 3
	_, e = client.SetInstallationUser(ctx, connect.NewRequest(change))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin changed installation grant", e)
	}
	if scoped.Msg.GlobalAdmin || len(scoped.Msg.Organizations) != 1 || scoped.Msg.Organizations[0].Id != org {
		t.Fatal("delegated directory leaked scope")
	}
	_, e = client.ListInstallationDirectory(ctx, connect.NewRequest(&pb.ListInstallationDirectoryRequest{Kind: "organizations"}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin accessed installation directory")
	}
	_, e = client.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	check(e)
	_, e = client.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: created.Msg.Id}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin accessed foreign directory")
	}
	_, e = client.SaveRole(ctx, connect.NewRequest(&pb.SaveRoleRequest{OrganizationId: org, Name: "Escalation", Permissions: []string{"admin.access", "connections.manage"}}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("admin entry permission escalated cloud authority")
	}
	_, e = s.pool.Exec(ctx, "UPDATE memberships SET active=false WHERE org_id=$1 AND user_id=$2", org, user)
	check(e)
	_, e = client.ListAccess(ctx, connect.NewRequest(&pb.ListAccessRequest{OrganizationId: org}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("delegated admin revocation not enforced")
	}
	_, e = s.pool.Exec(ctx, "DELETE FROM memberships WHERE org_id=$1 AND user_id=$2", org, user)
	check(e)
	_, e = s.pool.Exec(ctx, "UPDATE users SET global_admin=true,last_totp_step=0 WHERE id=$1", user)
	check(e)
	_, e = client.DeleteTeam(ctx, connect.NewRequest(&pb.DeleteTeamRequest{OrganizationId: created.Msg.Id, Id: teamRequest.Id, ExpectedRevision: 3}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("foreign scope deleted team")
	}
	_, e = client.DeleteTeam(ctx, connect.NewRequest(&pb.DeleteTeamRequest{OrganizationId: org, Id: teamRequest.Id, ExpectedRevision: 2}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale team delete accepted")
	}
	if !powerPermission(ctx, s.q, org, teamUser, "operations.request") {
		t.Fatal("failed team deletion changed grants")
	}
	_, e = client.DeleteTeam(ctx, connect.NewRequest(&pb.DeleteTeamRequest{OrganizationId: org, Id: teamRequest.Id, ExpectedRevision: 3}))
	check(e)
	if powerPermission(ctx, s.q, org, teamUser, "operations.request") || !powerPermission(ctx, s.q, org, teamUser, "resources.read") {
		t.Fatal("team deletion did not preserve only direct grants")
	}
	var deletedAudit int
	check(s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='team.deleted' AND target=$2", org, teamRequest.Id).Scan(&deletedAudit))
	if deletedAudit != 1 {
		t.Fatal("team deletion audit missing")
	}
	code, e := integrationTOTP(seed)
	check(e)
	_, e = client.SetAccountMFA(ctx, connect.NewRequest(&pb.SetAccountMFARequest{Enabled: true, Password: password, Code: code}))
	check(e)
	_, e = client.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatal("MFA policy change retained old session")
	}
	prompt, e = login("")
	check(e)
	if !prompt.Msg.MfaRequired {
		t.Fatal("reenabled MFA ignored")
	}
	_, e = s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE id=$1", user)
	check(e)
	code, e = integrationTOTP(seed)
	check(e)
	_, e = login(code)
	check(e)
	_, e = s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE id=$1", user)
	check(e)
	code, e = integrationTOTP(seed)
	check(e)
	_, e = client.SetAccountMFA(ctx, connect.NewRequest(&pb.SetAccountMFARequest{Password: password, Code: code}))
	check(e)
	_, e = login("")
	check(e)

	// Policy defaults remain optional; enrollment is password-protected and works
	// for the initially password-only administrator without exposing a saved seed.
	_, e = s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key=$1", "mfa:"+user)
	check(e)
	enrollment, e := client.BeginMFAEnrollment(ctx, connect.NewRequest(&pb.BeginMFAEnrollmentRequest{Password: password}))
	check(e)
	seed = enrollment.Msg.Secret
	if seed == "" {
		t.Fatal("missing MFA enrollment key")
	}
	fresh := func() string {
		t.Helper()
		_, e := s.pool.Exec(ctx, "UPDATE users SET last_totp_step=0 WHERE id=$1", user)
		check(e)
		_, e = s.pool.Exec(ctx, "DELETE FROM auth_limits WHERE key=$1", "mfa:"+user)
		check(e)
		code, e := integrationTOTP(seed)
		check(e)
		return code
	}
	_, e = client.SetAccountMFA(ctx, connect.NewRequest(&pb.SetAccountMFARequest{Enabled: true, Password: password, Code: fresh()}))
	check(e)
	_, e = login(fresh())
	check(e)
	viewer := randomID()
	_, e = s.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash,totp_ciphertext,mfa_enabled) VALUES($1,$2,$3,$4,false)", viewer, viewer+"@example.test", hashed, cipher)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO memberships(org_id,user_id,role_id,permissions) VALUES($1,$2,'viewer','{}')", org, viewer)
	check(e)
	setPolicy := func(scope string, required bool) {
		t.Helper()
		old, e := client.GetMFAPolicy(ctx, connect.NewRequest(&pb.GetMFAPolicyRequest{OrganizationId: scope}))
		check(e)
		_, e = client.SaveMFAPolicy(ctx, connect.NewRequest(&pb.SaveMFAPolicyRequest{OrganizationId: scope, Required: required, ExpectedRevision: old.Msg.Revision, Password: password, Code: fresh()}))
		check(e)
	}
	if !identityAllowed(ctx, s.q, org, viewer, actor(ctx).OIDC) {
		t.Fatal("optional MFA blocked user")
	}
	setPolicy(org, true)

	viewerHTTP := *server.Client()
	viewerHTTP.Jar, _ = cookiejar.New(nil)
	viewerClient := providahv1connect.NewConsoleServiceClient(&viewerHTTP, server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Origin", s.cfg.Origin)
			return next(ctx, r)
		}
	})))
	viewerSession, e := viewerClient.Login(ctx, connect.NewRequest(&pb.LoginRequest{Email: viewer + "@example.test", Password: password}))
	check(e)
	if len(viewerSession.Msg.Organizations) != 1 || !viewerSession.Msg.Organizations[0].MfaRequired || viewerSession.Msg.Organizations[0].SsoRequired {
		t.Fatal("MFA enrollment gate not represented in session")
	}
	_, e = viewerClient.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("password-only session bypassed organization MFA")
	}
	_, e = viewerClient.GetMFAPolicy(ctx, connect.NewRequest(&pb.GetMFAPolicyRequest{}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("ordinary user read global policy")
	}
	if identityAllowed(ctx, s.q, org, viewer, actor(ctx).OIDC) {
		t.Fatal("organization MFA requirement bypassed")
	}
	_, e = client.SetAccountMFA(ctx, connect.NewRequest(&pb.SetAccountMFARequest{Password: password, Code: fresh()}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("required MFA could be disabled")
	}
	setPolicy(org, false)
	if !identityAllowed(ctx, s.q, org, viewer, actor(ctx).OIDC) {
		t.Fatal("optional MFA did not restore access")
	}

	// Organization administrators control their own policy but not installation policy.
	_, e = s.pool.Exec(ctx, "UPDATE users SET global_admin=false WHERE id=$1", user)
	check(e)
	_, e = s.pool.Exec(ctx, "INSERT INTO memberships(org_id,user_id,role_id,permissions) VALUES($1,$2,'administrator','{}')", org, user)
	check(e)
	_, e = client.GetMFAPolicy(ctx, connect.NewRequest(&pb.GetMFAPolicyRequest{}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("organization admin managed global MFA")
	}
	setPolicy(org, true)
	setPolicy(org, false)
	_, e = s.pool.Exec(ctx, "UPDATE users SET global_admin=true WHERE id=$1", user)
	check(e)
	setPolicy("", true)
	if identityAllowed(ctx, s.q, org, viewer, actor(ctx).OIDC) {
		t.Fatal("optional organization weakened global MFA")
	}
	global, e := client.GetMFAPolicy(ctx, connect.NewRequest(&pb.GetMFAPolicyRequest{OrganizationId: org}))
	check(e)
	if !global.Msg.GlobalRequired || global.Msg.Required {
		t.Fatal("MFA inheritance flags incorrect")
	}
	_, e = client.SaveMFAPolicy(ctx, connect.NewRequest(&pb.SaveMFAPolicyRequest{ExpectedRevision: 0, Password: password, Code: fresh()}))
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("stale MFA policy accepted")
	}
	setPolicy("", false)
	policyEvents, e := client.ListInstallationAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{Source: "installation", Actor: email, Action: "identity.mfa_optional.global"}))
	check(e)
	if len(policyEvents.Msg.Events) != 1 || policyEvents.Msg.Events[0].Target != "" {
		t.Fatal("global policy installation audit missing")
	}

	if !identityAllowed(ctx, s.q, org, viewer, actor(ctx).OIDC) {
		t.Fatal("global optional policy did not restore access")
	}
	_, e = client.Logout(ctx, connect.NewRequest(&pb.LogoutRequest{}))
	check(e)
}
