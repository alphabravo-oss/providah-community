//go:build integration

package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/internal/vaultstore"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func exerciseExternalCredentials(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org string) {
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	previous, call := s.vault, s.cfg.ProviderCall
	defer func() { s.vault = previous; s.cfg.ProviderCall = call }()
	raw := vaultstore.Encode(vaultstore.Reference{Path: "production", Key: "credential", Version: 2})
	_, err := client.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "Unavailable vault", Provider: "hetzner", Credential: raw}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("unconfigured reference accepted", err)
	}
	outage, reads, calls := false, 0, 0
	s.vault, err = vaultstore.New(vaultstore.Config{Address: "https://vault.invalid", Mount: "secret", Token: "vault-test-token", HTTPClient: &http.Client{Transport: oidcTestTransport(func(r *http.Request) (*http.Response, error) {
		reads++
		if r.URL.Path != "/v1/secret/data/providah/"+org+"/hetzner/production" || r.URL.Query().Get("version") != "2" {
			t.Fatal("incorrect vault scope")
		}
		status, body := 200, `{"data":{"data":{"credential":"resolved-provider-test-token"},"metadata":{"version":2,"destroyed":false,"deletion_time":""}}}`
		if outage {
			status = 503
			body = `{"errors":["vault-test-token"]}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}})
	check(err)
	created, err := client.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: "External test", Provider: "hetzner", Credential: raw}))
	check(err)
	id := created.Msg.Connection.Id
	if created.Msg.Connection.CredentialSource != "vault_kv2" || reads != 0 {
		t.Fatal("reference metadata or lazy resolution failed")
	}
	var cipher []byte
	check(s.pool.QueryRow(ctx, "SELECT ciphertext FROM connections WHERE id=$1", id).Scan(&cipher))
	stored, err := s.open(cipher)
	check(err)
	ref, err := vaultstore.Parse(stored)
	check(err)
	if ref == nil || ref.Store == "" || ref.Version != 2 || strings.Contains(stored, "resolved-provider-test-token") {
		t.Fatal("resolved material stored")
	}
	listed, err := client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	check(err)
	payload, _ := json.Marshal(listed.Msg)
	if !listed.Msg.ExternalSecretsEnabled || strings.Contains(string(payload), "vault-test-token") || strings.Contains(string(payload), "vault-kv2:") {
		t.Fatal("reference or value exposed")
	}
	resource := randomID()
	check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: resource, OrgID: org, ConnectionID: id, Provider: "hetzner", Kind: "compute.server", NativeID: "8123", Name: "External metric server", Region: "fsn1", Status: "running"}))
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		calls++
		if r.Credential != "resolved-provider-test-token" || r.ConnectionID != id || r.Metrics == nil {
			t.Fatal("worker credential boundary failed")
		}
		return provider.Response{Version: provider.Protocol, Metrics: &provider.MetricsResult{}}, nil
	}
	request := &pb.GetResourceMetricsRequest{OrganizationId: org, ResourceId: resource, Hours: 1}
	for range 2 {
		_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
		check(err)
	}
	if reads != 2 || calls != 2 {
		t.Fatal("vault value cached or worker bypassed")
	}
	outage = true
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if err == nil || calls != 2 || strings.Contains(err.Error(), "vault-test-token") {
		t.Fatal("outage reused secret or exposed error")
	}
	_, err = client.RotateCredential(ctx, connect.NewRequest(&pb.RotateCredentialRequest{OrganizationId: org, Id: id, Credential: "replacement-builtin-test-token"}))
	check(err)
	var revision int64
	var source string
	check(s.pool.QueryRow(ctx, "SELECT revision,credential_source FROM connections WHERE id=$1", id).Scan(&revision, &source))
	if revision != 2 || source != "builtin" {
		t.Fatal("credential rotation did not fence queued work")
	}
	// Leave no additional enabled candidate for later fixtures.
	_, err = client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: id, Enabled: false}))
	check(err)
}
