//go:build integration

package core

import (
	"context"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"strings"
	"sync"
	"testing"
)

func exerciseRuntimeAudit(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	original := s.cfg.ProviderRuntimes
	defer func() { s.cfg.ProviderRuntimes = original }()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	count := func(want int) {
		t.Helper()
		var got int
		check(s.pool.QueryRow(ctx, `SELECT count(*) FROM installation_events WHERE action='runtime.catalog_admitted' AND actor_id IS NULL AND actor_email='system:runtime-admission'`).Scan(&got))
		if got != want {
			t.Fatalf("catalog audit count %d want %d", got, want)
		}
	}
	// Works before any user exists, and concurrent identical startups deduplicate.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.AuditRuntimeCatalog(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		check(err)
	}
	count(1)
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: "aws", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: provider.Protocol}}
	check(s.AuditRuntimeCatalog(ctx))
	check(s.AuditRuntimeCatalog(ctx))
	count(2)
	s.cfg.ProviderRuntimes[0].PublisherKeyID = "publisher"
	s.cfg.ProviderRuntimes[0].ApprovalExpiresAt = "2099-01-01T00:00:00Z"
	check(s.AuditRuntimeCatalog(ctx))
	count(3)
	var signer string
	check(s.pool.QueryRow(ctx, `SELECT details->'runtimes'->0->>'publisher_key_id' FROM installation_events WHERE action='runtime.catalog_admitted' ORDER BY id DESC LIMIT 1`).Scan(&signer))
	if signer != "publisher" {
		t.Fatal("missing publisher evidence")
	}
	s.cfg.ProviderRuntimes = nil
	check(s.AuditRuntimeCatalog(ctx))
	count(4)
	if _, err := s.pool.Exec(ctx, `DELETE FROM installation_events WHERE action='runtime.catalog_admitted'`); err == nil {
		t.Fatal("catalog audit deletion allowed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s.AuditRuntimeCatalog(canceled) == nil {
		t.Fatal("audit failure hidden")
	}
}
