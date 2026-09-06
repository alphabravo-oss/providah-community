package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/auditstore"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/jackc/pgx/v5"
)

var exportDetailKeys = []string{"runtime", "previous_runtime", "name", "provider", "resources", "action", "status", "reason", "decision", "detail", "provider_action_id", "observed_status", "role", "active", "email", "permissions", "revision", "identity", "enabled", "identity_enabled", "scheduled_for", "outcome", "operation", "local_time", "maintenance_exception", "maintenance_revision", "job_id", "servers", "approval", "connections", "approval_effect", "attempt", "response_code"}

func exportDetails(raw []byte) map[string]any {
	var source map[string]any
	_ = json.Unmarshal(raw, &source)
	safe := map[string]any{}
	for key, value := range source {
		if !slices.Contains(exportDetailKeys, key) {
			continue
		}
		switch v := value.(type) {
		case string, bool, float64, nil:
			safe[key] = value
		case []any:
			valid := true
			for _, item := range v {
				if _, ok := item.(string); !ok {
					valid = false
					break
				}
			}
			if valid {
				safe[key] = value
			}
		}
	}
	return safe
}
func auditArchive(org string, rows []database.AuditEvent, probe string) ([]byte, error) {
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	first, last := int64(0), int64(0)
	if len(rows) > 0 {
		first, last = rows[0].ID, rows[len(rows)-1].ID
	}
	if err := encoder.Encode(map[string]any{"record_type": "manifest", "format": "providah.audit.v1", "organization_id": org, "first_event_id": strconv.FormatInt(first, 10), "last_event_id": strconv.FormatInt(last, 10), "event_count": len(rows), "probe_id": probe}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.OrgID != org {
			return nil, errors.New("Audit batch organization mismatch.")
		}
		if err := encoder.Encode(map[string]any{"record_type": "event", "event_id": strconv.FormatInt(row.ID, 10), "organization_id": row.OrgID, "occurred_at": stamp(row.OccurredAt), "actor": row.Actor, "action": row.Action, "target": row.Target, "details": exportDetails(row.Details)}); err != nil {
			return nil, err
		}
		if raw.Len() > auditstore.MaxBatchBytes {
			return nil, errors.New("Audit batch exceeds its size limit.")
		}
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() > auditstore.MaxBatchBytes {
		return nil, errors.New("Compressed audit batch exceeds its size limit.")
	}
	return compressed.Bytes(), nil
}
func createExportBatch(ctx context.Context, q *database.Queries, c database.AuditExport, rows []database.AuditEvent, test bool) error {
	id := newOperationID()
	kind, probe := "audit", ""
	if test {
		kind, probe = "test", id
	}
	payload, err := auditArchive(c.OrgID, rows, probe)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	checksum := hex.EncodeToString(sum[:])
	first, last := int64(0), int64(0)
	if len(rows) > 0 {
		first, last = rows[0].ID, rows[len(rows)-1].ID
	}
	filename := fmt.Sprintf("%020d-%020d-%s.jsonl.gz", first, last, checksum)
	if test {
		filename = "checks/" + id + "-" + checksum + ".jsonl.gz"
	}
	return q.CreateAuditExportBatch(ctx, database.CreateAuditExportBatchParams{ID: id, OrgID: c.OrgID, Revision: c.Revision, Kind: kind, FirstID: first, LastID: last, EventCount: int32(len(rows)), Payload: payload, Checksum: checksum, ObjectKey: "providah-audit/" + c.OrgID + "/" + filename}) // #nosec G115 -- Export batches are bounded before encoding and persistence.
}
func canManageExport(ctx context.Context, q *database.Queries, org string) bool {
	p, err := q.Permissions(ctx, database.PermissionsParams{OrgID: org, UserID: actor(ctx).UserID})
	return err == nil && slices.Contains(p, "audit.read") && slices.Contains(p, "audit.export.manage")
}
func (s *Service) GetAuditExport(ctx context.Context, req *connect.Request[pb.GetAuditExportRequest]) (*connect.Response[pb.GetAuditExportResponse], error) {
	org := req.Msg.OrganizationId
	out := &pb.GetAuditExportResponse{Cursor: "0"}
	config, err := s.q.GetAuditExport(ctx, org)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		out = &pb.GetAuditExportResponse{Configured: true, Endpoint: config.Endpoint, Region: config.Region, Bucket: config.Bucket, Enabled: config.Enabled, Verified: config.Verified, Cursor: strconv.FormatInt(config.CursorID, 10), Detail: config.Detail}
	}
	backlog, err := s.q.AuditExportBacklog(ctx, database.AuditExportBacklogParams{OrgID: org, ID: config.CursorID})
	if err != nil {
		return nil, err
	}
	out.PendingEvents, out.OldestPendingAt = backlog.Events, stamp(backlog.Oldest)
	return connect.NewResponse(out), nil
}
func (s *Service) SaveAuditExport(ctx context.Context, req *connect.Request[pb.SaveAuditExportRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	target := auditstore.Target{Endpoint: r.Endpoint, Region: r.Region, Bucket: r.Bucket, AccessKey: r.AccessKey, SecretKey: r.SecretKey, SessionToken: r.SessionToken}
	if err := auditstore.Validate(target); err != nil {
		return nil, invalid(err.Error())
	}
	raw, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	cipher, err := s.seal(string(raw))
	if err != nil {
		return nil, err
	}
	err = s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, r.OrganizationId); err != nil {
			return denied()
		}
		if !canManageExport(ctx, q, r.OrganizationId) {
			return denied()
		}
		if err := q.SaveAuditExport(ctx, database.SaveAuditExportParams{OrgID: r.OrganizationId, Endpoint: r.Endpoint, Region: r.Region, Bucket: r.Bucket, Ciphertext: cipher}); err != nil {
			return err
		}
		if err := q.CancelAuditExportBatches(ctx, r.OrganizationId); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "audit.export_configured", r.OrganizationId, map[string]any{"status": "paused; verification and retained-history backfill required"})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) TestAuditExport(ctx context.Context, req *connect.Request[pb.TestAuditExportRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	org := req.Msg.OrganizationId
	if err := s.limit(ctx, "audit-export-test:"+org); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, org); err != nil {
			return denied()
		}
		if !canManageExport(ctx, q, org) {
			return denied()
		}
		config, err := q.GetAuditExport(ctx, org)
		if err != nil {
			return conflict("Configure export storage first.")
		}
		active, err := q.AuditExportActive(ctx, org)
		if err != nil {
			return err
		}
		if active || config.Enabled {
			return conflict("Pause export and wait for current work before testing storage.")
		}
		if err = createExportBatch(ctx, q, config, nil, true); err != nil {
			return err
		}
		return audit(ctx, q, org, actor(ctx).Email, "audit.export_test_requested", org, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) SetAuditExportEnabled(ctx context.Context, req *connect.Request[pb.SetAuditExportEnabledRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	org := req.Msg.OrganizationId
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, org); err != nil {
			return denied()
		}
		if !canManageExport(ctx, q, org) {
			return denied()
		}
		config, err := q.GetAuditExport(ctx, org)
		if err != nil {
			return conflict("Configure export storage first.")
		}
		if req.Msg.Enabled && !config.Verified {
			return conflict("Run and pass a storage probe before enabling export.")
		}
		if !req.Msg.Enabled {
			if err = q.CancelAuditExportBatches(ctx, org); err != nil {
				return err
			}
		}
		if err = q.SetAuditExportEnabled(ctx, database.SetAuditExportEnabledParams{OrgID: org, Enabled: req.Msg.Enabled}); err != nil {
			return err
		}
		return audit(ctx, q, org, actor(ctx).Email, "audit.export_state_changed", org, map[string]any{"enabled": req.Msg.Enabled})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) RetryAuditExport(ctx context.Context, req *connect.Request[pb.RetryAuditExportRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	org := req.Msg.OrganizationId
	if err := s.limit(ctx, "audit-export-retry:"+org); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, org); err != nil {
			return denied()
		}
		if !canManageExport(ctx, q, org) {
			return denied()
		}
		if err := q.RetryAuditExport(ctx, org); err != nil {
			return err
		}
		return audit(ctx, q, org, actor(ctx).Email, "audit.export_retry_requested", org, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) ListAuditExportBatches(ctx context.Context, req *connect.Request[pb.ListAuditExportBatchesRequest]) (*connect.Response[pb.ListAuditExportBatchesResponse], error) {
	r := req.Msg
	after, err := parseCursor(r.PageToken, r.OrganizationId, "audit-export")
	if err != nil {
		return nil, err
	}
	if after == "" {
		after = "~"
	}
	rows, err := s.q.ListAuditExportBatches(ctx, database.ListAuditExportBatchesParams{OrgID: r.OrganizationId, ID: after})
	if err != nil {
		return nil, err
	}
	out := &pb.ListAuditExportBatchesResponse{}
	if len(rows) > 100 {
		out.NextPageToken = cursor(r.OrganizationId, "audit-export", rows[99].ID)
		rows = rows[:100]
	}
	for _, b := range rows {
		out.Batches = append(out.Batches, &pb.AuditExportBatch{Id: b.ID, Kind: b.Kind, FirstId: strconv.FormatInt(b.FirstID, 10), LastId: strconv.FormatInt(b.LastID, 10), EventCount: b.EventCount, Checksum: b.Checksum, ObjectKey: b.ObjectKey, Status: b.Status, Attempts: b.Attempts, Detail: b.Detail, UpdatedAt: stamp(b.UpdatedAt)})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) prepareAuditExport(ctx context.Context) error {
	org, err := s.q.NextAuditExport(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, org); err != nil {
			return err
		}
		config, err := q.GetAuditExport(ctx, org)
		if err != nil {
			return err
		}
		active, err := q.AuditExportActive(ctx, org)
		if err != nil {
			return err
		}
		if active || !config.Enabled || !config.Verified {
			return nil
		}
		rows, err := q.AuditExportRows(ctx, database.AuditExportRowsParams{OrgID: org, ID: config.CursorID})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return q.DelayAuditExport(ctx, org)
		}
		if err = createExportBatch(ctx, q, config, rows, false); err != nil {
			return err
		}
		backlog, err := q.AuditExportBacklog(ctx, database.AuditExportBacklogParams{OrgID: org, ID: config.CursorID})
		if err != nil {
			return err
		}
		if backlog.Events > 10000 || (backlog.Oldest.Valid && time.Since(backlog.Oldest.Time) > 24*time.Hour) {
			if err = audit(ctx, q, org, "system:audit-export", "audit.export_backlog", org, map[string]any{"status": "backlog exceeds 10,000 events or 24 hours"}); err != nil {
				return err
			}
		}
		return q.BumpAuditExportVisibility(ctx, org)
	})
}

// One bounded batch per worker turn; leases make additional workers safe.
func (s *Service) StartAuditExport(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.prepareAuditExport(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error().Msg("audit export preparation failed")
			}
			if err := s.auditExportTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error().Msg("audit export worker failed")
			}
		}
	}
}
func (s *Service) auditExportTick(ctx context.Context) error {
	job, err := s.q.ClaimAuditExportBatch(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var request auditstore.Request
	ready := false
	err = s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, job.OrgID); err != nil {
			return err
		}
		live, err := q.LockAuditExportBatch(ctx, database.LockAuditExportBatchParams{OrgID: job.OrgID, ID: job.ID})
		if err != nil {
			return err
		}
		config, err := q.GetAuditExport(ctx, job.OrgID)
		if err != nil {
			return err
		}
		if live.Status != "running" || live.Attempts != job.Attempts || !live.LeaseUntil.Time.Equal(job.LeaseUntil.Time) || time.Now().After(live.LeaseUntil.Time) {
			return nil
		}
		if config.Revision != job.Revision || (job.Kind == "audit" && (!config.Enabled || !config.Verified)) {
			return q.FinishAuditExportBatch(ctx, database.FinishAuditExportBatchParams{OrgID: job.OrgID, ID: job.ID, Status: "canceled", Detail: "Export authorization or configuration changed.", NextAttempt: dbTime(time.Now())})
		}
		raw, err := s.open(config.Ciphertext)
		if err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &request.Target); err != nil {
			return err
		}
		request.Origin, request.Key, request.Checksum, request.Payload = s.cfg.Origin, job.ObjectKey, job.Checksum, job.Payload
		request.First, request.Last = strconv.FormatInt(job.FirstID, 10), strconv.FormatInt(job.LastID, 10)
		ready = true
		return q.BumpAuditExportVisibility(ctx, job.OrgID)
	})
	if err != nil || !ready {
		return err
	}
	write := s.cfg.AuditExportWrite
	if write == nil {
		write = auditstore.Write
	}
	sendCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	writeErr := write(sendCtx, request)
	cancel()
	return s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAuditScope(ctx, job.OrgID); err != nil {
			return err
		}
		live, err := q.LockAuditExportBatch(ctx, database.LockAuditExportBatchParams{OrgID: job.OrgID, ID: job.ID})
		if err != nil {
			return err
		}
		config, err := q.GetAuditExport(ctx, job.OrgID)
		if err != nil {
			return err
		}
		if live.Status != "running" || live.Attempts != job.Attempts || !live.LeaseUntil.Time.Equal(job.LeaseUntil.Time) || time.Now().After(live.LeaseUntil.Time) || config.Revision != job.Revision {
			return nil
		}
		status, detail := "succeeded", "Object stored and checksum verified."
		delay := time.Minute * time.Duration(1<<min(job.Attempts-1, 6))
		if delay > time.Hour {
			delay = time.Hour
		}
		if writeErr != nil {
			status = "pending"
			detail = "Storage request or checksum verification failed; retry scheduled."
		}
		if err = q.FinishAuditExportBatch(ctx, database.FinishAuditExportBatchParams{OrgID: job.OrgID, ID: job.ID, Status: status, Detail: detail, NextAttempt: dbTime(time.Now().Add(delay))}); err != nil {
			return err
		}
		if writeErr != nil {
			if err = q.FailAuditExport(ctx, job.OrgID); err != nil {
				return err
			}
			return audit(ctx, q, job.OrgID, "system:audit-export", "audit.export_failed", job.OrgID, map[string]any{"job_id": job.ID, "attempt": job.Attempts})
		}
		if job.Kind == "test" {
			return q.VerifyAuditExport(ctx, job.OrgID)
		}
		// No success audit event: exporting the exporter indefinitely would prevent
		// an idle organization from ever draining its backlog. Batch history is durable.
		return q.AdvanceAuditExport(ctx, database.AdvanceAuditExportParams{OrgID: job.OrgID, CursorID: job.LastID})
	})
}
