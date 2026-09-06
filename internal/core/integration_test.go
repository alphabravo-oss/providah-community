//go:build integration

package core

import (
	"context"
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"filippo.io/age"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

func TestConsoleIntegration(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("run make test-integration")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "test_" + randomID()[:16]
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)") }()
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	pool, err := database.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	identity, _ := age.GenerateX25519Identity()
	cfg := Config{Origin: "http://console.test", BootstrapToken: randomID(), SessionKey: randomID(), AgeIdentity: identity.String()}
	var destroyArtifacts func()
	if os.Getenv("TEST_RUSTFS") == "1" {
		cfg.Artifacts, destroyArtifacts = testArtifacts(t)
	}
	svc, err := New(pool, cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	exerciseRuntimeAudit(t, svc, ctx)
	server := httptest.NewServer(svc.Handler(""))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{Jar: jar}
	client := providahv1connect.NewConsoleServiceClient(httpClient, server.URL+"/api", connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Origin", cfg.Origin)
			return next(ctx, req)
		}
	})))
	enrollment, err := client.BeginSetup(ctx, connect.NewRequest(&pb.BeginSetupRequest{Token: cfg.BootstrapToken}))
	if err != nil {
		t.Fatal(err)
	}
	code, _ := integrationTOTP(enrollment.Msg.Secret)
	session, err := client.FinishSetup(ctx, connect.NewRequest(&pb.FinishSetupRequest{Token: cfg.BootstrapToken, Email: "owner@example.com", Password: "test-password-long", Code: code, OrganizationName: "Test organization"}))
	if err != nil {
		t.Fatal(err)
	}
	org := session.Msg.Organizations[0].Id
	if _, err = client.BeginSetup(ctx, connect.NewRequest(&pb.BeginSetupRequest{Token: cfg.BootstrapToken})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("setup not sealed: %v", err)
	}
	secret := "hetzner-test-secret-never-return"
	created, err := client.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Provider: "hetzner", Name: "Production", Credential: secret}))
	if err != nil {
		t.Fatal(err)
	}
	connections, err := client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	if err != nil || len(connections.Msg.Connections) != 1 {
		t.Fatalf("list: %v", err)
	}
	data, _ := json.Marshal(connections.Msg)
	if strings.Contains(string(data), secret) {
		t.Fatal("credential leaked")
	}
	detail, detailErr := client.GetConnection(ctx, connect.NewRequest(&pb.GetConnectionRequest{OrganizationId: org, Id: created.Msg.Connection.Id}))
	if detailErr != nil || detail.Msg.Connection.Name != "Production" {
		t.Fatal("connection detail", detailErr)
	}
	detailJSON, _ := json.Marshal(detail.Msg)
	if strings.Contains(string(detailJSON), secret) || strings.Contains(string(detailJSON), "ciphertext") {
		t.Fatal("connection detail leaked credential")
	}
	for _, request := range []*pb.GetConnectionRequest{{OrganizationId: org, Id: randomID()}, {OrganizationId: randomID(), Id: created.Msg.Connection.Id}} {
		if _, e := client.GetConnection(ctx, connect.NewRequest(request)); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("foreign/missing connection allowed", e)
		}
	}
	if _, e := client.GetConnection(ctx, connect.NewRequest(&pb.GetConnectionRequest{OrganizationId: org, Id: "invalid"})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid connection ID", e)
	}
	var stored []byte
	if err = pool.QueryRow(ctx, "SELECT ciphertext FROM connections WHERE id=$1", created.Msg.Connection.Id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), secret) {
		t.Fatal("plaintext in database")
	}
	events, err := client.ListAudit(ctx, connect.NewRequest(&pb.ListAuditRequest{OrganizationId: org}))
	if err != nil || len(events.Msg.Events) != 2 {
		t.Fatalf("audit not atomic: %v", err)
	}
	data, _ = json.Marshal(events.Msg)
	if strings.Contains(string(data), secret) {
		t.Fatal("secret in audit")
	}
	// Audit immutability and failure atomicity are verified against PostgreSQL.
	if _, err = pool.Exec(ctx, "UPDATE audit_events SET action='changed' WHERE org_id=$1", org); err == nil {
		t.Fatal("audit event was mutable")
	}
	if _, err = pool.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT reject_creation CHECK(action <> 'connection.created') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Provider: "hetzner", Name: "Must rollback", Credential: secret})); err == nil {
		t.Fatal("mutation succeeded without required audit")
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM connections WHERE org_id=$1", org).Scan(&count); err != nil || count != 1 {
		t.Fatalf("mutation was not rolled back: %d %v", count, err)
	}
	if _, err = pool.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT reject_creation"); err != nil {
		t.Fatal(err)
	}
	other := randomID()
	if err = svc.q.CreateOrganization(ctx, database.CreateOrganizationParams{ID: other, Name: "Other tenant"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: other})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("cross-tenant list: %v", err)
	}
	if _, err = client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: other, Id: created.Msg.Connection.Id})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("cross-tenant write: %v", err)
	}
	if _, err = client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: created.Msg.Connection.Id, Enabled: false})); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Refresh(ctx, connect.NewRequest(&pb.RefreshRequest{})); err != nil {
		t.Fatal(err)
	}
	exerciseDiscovery(t, svc, client, ctx, org, other, created.Msg.Connection.Id)
	exerciseRDSInventory(t, svc, client, ctx, org, other)
	exerciseAccess(t, svc, client, ctx, org)
	exerciseNotifications(t, svc, client, ctx, org, other)
	exerciseAuditExport(t, svc, client, ctx, org, other)
	exerciseAuditFilters(t, svc, client, ctx, org, other)
	exerciseRecovery(t, svc, client, ctx, org)
	exerciseOIDC(t, svc, client, httpClient, server.URL, ctx, enrollment.Msg.Secret)
	exerciseExternalCredentials(t, svc, client, ctx, org)
	exerciseMetrics(t, svc, client, ctx, org)
	exerciseBulkPower(t, svc, client, ctx, org)
	exerciseInventoryViews(t, svc, ctx)
	exerciseTelemetry(t, svc, ctx)
	exerciseCLI(t, svc, ctx)
	exerciseAccountSessions(t, svc, ctx)
	exerciseGlobalAdministrator(t, svc, ctx)
	// A read-only grant is checked again on the very next request, not at token expiry.
	if _, err = pool.Exec(ctx, "UPDATE memberships SET role_id=NULL,permissions=ARRAY['connections.read'] WHERE org_id=$1", org); err != nil {
		t.Fatal(err)
	}
	if _, err = client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: created.Msg.Connection.Id, Enabled: true})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("revoked write still allowed: %v", err)
	}
	if _, err = client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org})); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE users SET active=false"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.GetSession(ctx, connect.NewRequest(&pb.GetSessionRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("disabled session still usable: %v", err)
	}
	request, _ := http.NewRequest("POST", server.URL+"/api/providah.v1.ConsoleService/SetupStatus", strings.NewReader("{}"))
	request.Header.Set("Origin", "http://evil.test")
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("bad origin accepted: %d", response.StatusCode)
	}
	exerciseArtifactCheck(t, svc, ctx)
	svc = exerciseEncryptionRotation(t, svc, ctx)
	exerciseArtifactCheck(t, svc, ctx)
	exerciseDatabaseBackup(t, svc, ctx, destroyArtifacts)
}

// Avoid submitting a valid test code across the strict production 30-second boundary.
func integrationTOTP(secret string) (string, error) {
	now := time.Now()
	remaining := 30*time.Second - time.Duration(now.UnixNano()%(30*int64(time.Second)))
	if remaining < 3*time.Second {
		time.Sleep(remaining + 10*time.Millisecond)
	}
	return totp.GenerateCode(secret, time.Now())
}
