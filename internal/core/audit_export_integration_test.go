//go:build integration

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/auditstore"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
)

func exerciseAuditExport(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	save := &pb.SaveAuditExportRequest{OrganizationId: org, Endpoint: "https://storage.example.com", Region: "us-east-1", Bucket: "audit-test", AccessKey: "test-access", SecretKey: "test-secret-never-return"}
	_, err := client.SaveAuditExport(ctx, connect.NewRequest(save))
	check(err)
	config := func() *pb.GetAuditExportResponse {
		t.Helper()
		out, e := client.GetAuditExport(ctx, connect.NewRequest(&pb.GetAuditExportRequest{OrganizationId: org}))
		check(e)
		return out.Msg
	}
	raw, err := json.Marshal(config())
	check(err)
	var ciphertext []byte
	check(s.pool.QueryRow(ctx, "SELECT ciphertext FROM audit_exports WHERE org_id=$1", org).Scan(&ciphertext))
	if strings.Contains(string(raw), save.SecretKey) || bytes.Contains(ciphertext, []byte(save.SecretKey)) {
		t.Fatal("storage credentials exposed")
	}
	_, err = client.GetAuditExport(ctx, connect.NewRequest(&pb.GetAuditExportRequest{OrganizationId: other}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-org read allowed", err)
	}
	_, err = client.TestAuditExport(ctx, connect.NewRequest(&pb.TestAuditExportRequest{OrganizationId: other}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("cross-org probe allowed", err)
	}
	enable := func(on bool) error {
		_, e := client.SetAuditExportEnabled(ctx, connect.NewRequest(&pb.SetAuditExportEnabledRequest{OrganizationId: org, Enabled: on}))
		return e
	}
	if connect.CodeOf(enable(true)) != connect.CodeFailedPrecondition {
		t.Fatal("unverified export enabled")
	}
	var received []auditstore.Request
	fail := false
	previous := s.cfg.AuditExportWrite
	defer func() { s.cfg.AuditExportWrite = previous }()
	s.cfg.AuditExportWrite = func(_ context.Context, r auditstore.Request) error {
		received = append(received, r)
		if r.Target.SecretKey != save.SecretKey || !strings.HasPrefix(r.Key, "providah-audit/"+org+"/") {
			t.Fatal("wrong credential or key scope")
		}
		if fail {
			return errors.New("test storage unavailable")
		}
		return nil
	}
	probe := func() {
		t.Helper()
		_, e := client.TestAuditExport(ctx, connect.NewRequest(&pb.TestAuditExportRequest{OrganizationId: org}))
		check(e)
		check(s.auditExportTick(ctx))
	}
	probe()
	if !config().Verified || config().Enabled || config().Cursor != "0" || len(received) != 1 {
		t.Fatal("invalid probe outcome", config())
	}
	check(enable(true))
	check(s.prepareAuditExport(ctx))
	fail = true
	check(s.auditExportTick(ctx))
	if config().Cursor != "0" {
		t.Fatal("failed export advanced cursor")
	}
	var key, checksum string
	var payload []byte
	check(s.pool.QueryRow(ctx, "SELECT object_key,checksum,payload FROM audit_export_batches WHERE org_id=$1 AND status='pending'", org).Scan(&key, &checksum, &payload))
	_, err = client.RetryAuditExport(ctx, connect.NewRequest(&pb.RetryAuditExportRequest{OrganizationId: org}))
	check(err)
	fail = false
	check(s.auditExportTick(ctx))
	if config().Cursor == "0" || len(received) != 3 || received[2].Key != key || received[2].Checksum != checksum || !bytes.Equal(received[2].Payload, payload) {
		t.Fatal("retry identity or commit failed")
	}
	var retained int
	check(s.pool.QueryRow(ctx, "SELECT coalesce(sum(octet_length(payload)),0) FROM audit_export_batches WHERE org_id=$1 AND status='succeeded'", org).Scan(&retained))
	if retained != 0 {
		t.Fatal("successful payload not released")
	}
	// A reclaimed lease reuses the exact object; it cannot silently skip the batch.
	check(s.prepareAuditExport(ctx))
	claimed, err := s.q.ClaimAuditExportBatch(ctx)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE audit_export_batches SET lease_until=now()-interval '1 second' WHERE id=$1", claimed.ID)
	check(err)
	check(s.auditExportTick(ctx))
	if received[len(received)-1].Key != claimed.ObjectKey {
		t.Fatal("lease replay changed identity")
	}
	// Export completion creates no new source events; the backlog drains.
	for i := 0; i < 5; i++ {
		check(s.prepareAuditExport(ctx))
		check(s.auditExportTick(ctx))
	}
	if config().PendingEvents != 0 {
		t.Fatal("export success feedback loop", config())
	}
	before := len(received)
	check(s.prepareAuditExport(ctx))
	check(s.auditExportTick(ctx))
	if len(received) != before {
		t.Fatal("idle exporter still writing")
	}
	// Pausing cancels pending work and keeps the committed cursor unchanged.
	check(s.transaction(ctx, func(q *database.Queries) error { return audit(ctx, q, org, "test", "export.fixture", "fixture", nil) }))
	_, err = s.pool.Exec(ctx, "UPDATE audit_exports SET next_batch_at=now() WHERE org_id=$1", org)
	check(err)
	check(s.prepareAuditExport(ctx))
	cursor := config().Cursor
	check(enable(false))
	check(s.auditExportTick(ctx))
	if len(received) != before || config().Cursor != cursor {
		t.Fatal("paused export dispatched")
	}
	// Replacing storage resets verification/cursor and fences outstanding probes.
	_, err = client.TestAuditExport(ctx, connect.NewRequest(&pb.TestAuditExportRequest{OrganizationId: org}))
	check(err)
	save.Bucket = "replacement-audit"
	_, err = client.SaveAuditExport(ctx, connect.NewRequest(save))
	check(err)
	check(s.auditExportTick(ctx))
	if len(received) != before || config().Verified || config().Enabled || config().Cursor != "0" {
		t.Fatal("replacement did not fence old work")
	}
	history, err := client.ListAuditExportBatches(ctx, connect.NewRequest(&pb.ListAuditExportBatchesRequest{OrganizationId: org}))
	check(err)
	if len(history.Msg.Batches) < 4 {
		t.Fatal("durable history missing")
	}
	exerciseAuditOrdering(t, s, ctx, org)
}

func exerciseAuditOrdering(t *testing.T, s *Service, parent context.Context, org string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := database.New(tx)
	if _, err = q.LockAuditScope(ctx, org); err != nil {
		t.Fatal(err)
	}
	// A competing writer waits before assigning its ID. Observe its actual lock
	// wait, then insert in the transaction holding the organization lock.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	pid := conn.Conn().PgConn().PID()
	done := make(chan error, 1)
	go func() {
		done <- database.New(conn).AddAudit(ctx, database.AddAuditParams{OrgID: org, Actor: "test", Action: "ordering.waiter", Target: "", Details: []byte(`{}`)})
	}()
	waiting := false
	for !waiting {
		if err = s.pool.QueryRow(ctx, "SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			select {
			case e := <-done:
				t.Fatal("writer did not wait", e)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	if err = q.AddAudit(ctx, database.AddAuditParams{OrgID: org, Actor: "test", Action: "ordering.holder", Target: "", Details: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var ordered bool
	if err = s.pool.QueryRow(ctx, "SELECT (SELECT id FROM audit_events WHERE org_id=$1 AND action='ordering.holder') < (SELECT id FROM audit_events WHERE org_id=$1 AND action='ordering.waiter')", org).Scan(&ordered); err != nil {
		t.Fatal(err)
	}
	if !ordered {
		t.Fatal("concurrent audit writer assigned ID before organization lock; export cursor could skip it")
	}
}
