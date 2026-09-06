//go:build integration

package core

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func exerciseTelemetry(t *testing.T, s *Service, ctx context.Context) {
	previousBudget := s.cfg.DatabaseBudgetBytes
	s.cfg.DatabaseBudgetBytes = 1
	defer func() { s.cfg.DatabaseBudgetBytes = previousBudget }()
	storage, err := s.databaseStorage(ctx)
	if err != nil || storage.Status != "critical" || storage.UsedBytes < storage.AuditBytes {
		t.Fatal("database size or budget check failed", storage, err)
	}
	s.refreshTelemetry(ctx)
	scrape := func() string {
		r := httptest.NewRecorder()
		s.MetricsHandler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
		if r.Code != 200 {
			t.Fatal("scrape failed")
		}
		return r.Body.String()
	}
	body := scrape()
	for _, want := range []string{`providah_database_storage_bytes{kind="budget"} 1`, `providah_database_storage_bytes{kind="audit"}`, `providah_database_storage_snapshot_timestamp_seconds`} {
		if !strings.Contains(body, want) {
			t.Fatal("storage metric missing", want)
		}
	}

	if strings.Contains(body, "providah_work_snapshot_timestamp_seconds 0") || !strings.Contains(body, `providah_work_items{kind="scan",status="queued"}`) || !strings.Contains(body, `providah_database_connections{state="max"}`) {
		t.Fatal("snapshot or pool metrics missing")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	s.refreshTelemetry(canceled)
	body = scrape()
	if !strings.Contains(body, "providah_database_storage_snapshot_failures_total 1") || !strings.Contains(body, `providah_database_storage_bytes{kind="budget"} 1`) || !strings.Contains(body, "providah_work_snapshot_failures_total 1") || strings.Contains(body, "providah_work_snapshot_timestamp_seconds 0") {
		t.Fatal("failed snapshot discarded last good state")
	}
	for _, secret := range []string{"owner@example.com", "vault-test-token", "hetzner-test-secret-never-return"} {
		if strings.Contains(body, secret) {
			t.Fatal("sensitive metadata in metrics")
		}
	}
}
