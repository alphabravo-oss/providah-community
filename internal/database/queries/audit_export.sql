-- name: GetAuditExport :one
SELECT * FROM audit_exports WHERE org_id=$1;
-- name: SaveAuditExport :exec
INSERT INTO audit_exports(org_id,endpoint,region,bucket,ciphertext) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(org_id) DO UPDATE SET endpoint=excluded.endpoint,region=excluded.region,bucket=excluded.bucket,ciphertext=excluded.ciphertext,revision=audit_exports.revision+1,enabled=false,verified=false,cursor_id=0,next_batch_at=now(),detail='',updated_at=now();
-- name: SetAuditExportEnabled :exec
UPDATE audit_exports SET enabled=$2,next_batch_at=now(),updated_at=now() WHERE org_id=$1;
-- name: CancelAuditExportBatches :exec
UPDATE audit_export_batches SET status='canceled',payload=''::bytea,detail='Export configuration changed or was paused.',updated_at=now() WHERE org_id=$1 AND status IN ('pending','running');
-- name: AuditExportActive :one
SELECT EXISTS(SELECT 1 FROM audit_export_batches WHERE org_id=$1 AND status IN ('pending','running'));
-- name: NextAuditExport :one
SELECT e.org_id FROM audit_exports e WHERE e.enabled AND e.verified AND e.next_batch_at<=now() AND NOT EXISTS(SELECT 1 FROM audit_export_batches b WHERE b.org_id=e.org_id AND b.status IN ('pending','running')) ORDER BY e.next_batch_at,e.org_id LIMIT 1;
-- name: DelayAuditExport :exec
UPDATE audit_exports SET next_batch_at=now()+interval '30 seconds' WHERE org_id=$1;
-- name: AuditExportRows :many
SELECT * FROM audit_events WHERE org_id=$1 AND id>$2 ORDER BY id LIMIT 100;
-- name: AuditExportBacklog :one
SELECT count(*)::bigint AS events,min(occurred_at)::timestamptz AS oldest FROM audit_events WHERE org_id=$1 AND id>$2;
-- name: CreateAuditExportBatch :exec
INSERT INTO audit_export_batches(id,org_id,revision,kind,first_id,last_id,event_count,payload,checksum,object_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);
-- name: ClaimAuditExportBatch :one
UPDATE audit_export_batches SET status='running',attempts=attempts+1,lease_until=now()+interval '60 seconds',updated_at=now()
WHERE id=(SELECT id FROM audit_export_batches WHERE (status='pending' AND next_attempt<=now()) OR (status='running' AND lease_until<now()) ORDER BY next_attempt,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *;
-- name: LockAuditExportBatch :one
SELECT * FROM audit_export_batches WHERE org_id=$1 AND id=$2 FOR UPDATE;
-- name: FinishAuditExportBatch :exec
UPDATE audit_export_batches SET status=$3,payload=CASE WHEN $3 IN ('succeeded','canceled') THEN ''::bytea ELSE payload END,detail=$4,next_attempt=$5,lease_until=NULL,updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: VerifyAuditExport :exec
UPDATE audit_exports SET verified=true,detail='Storage probe verified. Enable continuous export when ready.',updated_at=now() WHERE org_id=$1;
-- name: AdvanceAuditExport :exec
UPDATE audit_exports SET cursor_id=$2,next_batch_at=now(),detail='Last batch stored and verified.',updated_at=now() WHERE org_id=$1;
-- name: FailAuditExport :exec
UPDATE audit_exports SET detail='Export failed. Local audit is retained; retry is scheduled.',updated_at=now() WHERE org_id=$1;
-- name: ListAuditExportBatches :many
SELECT id,kind,first_id,last_id,event_count,checksum,object_key,status,attempts,detail,updated_at FROM audit_export_batches WHERE org_id=$1 AND id<$2 ORDER BY id DESC LIMIT 101;
-- name: RetryAuditExport :exec
UPDATE audit_export_batches SET next_attempt=now() WHERE org_id=$1 AND status='pending';

-- name: LockAuditScope :one
SELECT id FROM organizations WHERE id=$1 FOR UPDATE;
-- name: BumpAuditExportVisibility :exec
UPDATE organizations SET revision=revision+1 WHERE id=$1;
