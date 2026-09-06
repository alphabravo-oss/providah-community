-- name: ConnectionForScan :one
SELECT c.* FROM connections c JOIN organizations o ON o.id=c.org_id WHERE c.org_id=$1 AND c.id=$2 AND c.deleted_at IS NULL AND o.active FOR UPDATE OF c;
-- name: QueueScan :one
INSERT INTO scan_jobs(id,org_id,connection_id,revision,requester_id,actor,module_revision,runtime_id,trace_parent)
SELECT sqlc.arg(id),c.org_id,c.id,c.revision,sqlc.arg(requester_id),sqlc.arg(actor),sqlc.arg(module_revision),sqlc.arg(runtime_id),sqlc.arg(trace_parent) FROM connections c JOIN organizations o ON o.id=c.org_id WHERE c.org_id=sqlc.arg(org_id) AND c.id=sqlc.arg(connection_id) AND c.enabled AND c.deleted_at IS NULL AND o.active
ON CONFLICT (org_id,connection_id) WHERE status IN ('queued','running') DO NOTHING RETURNING id;
-- name: ActiveScan :one
SELECT id FROM scan_jobs WHERE org_id=$1 AND connection_id=$2 AND status IN ('queued','running');
-- name: MarkScanQueued :exec
UPDATE connections SET scan_status='queued',scan_error='' WHERE org_id=$1 AND id=$2 AND enabled AND deleted_at IS NULL;
-- name: DueConnections :many
SELECT c.org_id,c.id FROM connections c JOIN organizations o ON o.id=c.org_id WHERE c.enabled AND c.deleted_at IS NULL AND o.active AND coalesce((SELECT enabled FROM provider_modules WHERE org_id=c.org_id AND provider=c.provider),true) AND c.next_scan_at<=now() AND NOT EXISTS(SELECT 1 FROM scan_jobs j WHERE j.org_id=c.org_id AND j.connection_id=c.id AND j.status IN ('queued','running')) ORDER BY c.next_scan_at LIMIT 20;
-- name: ClaimScan :one
UPDATE scan_jobs SET status='running',lease_until=now()+interval '3 minutes' WHERE id=(SELECT id FROM scan_jobs WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *;
-- name: LockRunningScan :one
SELECT * FROM scan_jobs WHERE id=$1 AND status='running' AND lease_until>now() FOR UPDATE;
-- name: MarkScanRunning :exec
UPDATE connections SET scan_status='running' WHERE org_id=$1 AND id=$2 AND revision=$3 AND enabled AND deleted_at IS NULL;
-- name: FinishScanJob :exec
UPDATE scan_jobs SET status=$2,error=$3,finished_at=now() WHERE id=$1 AND status='running';
-- name: RecordScanSuccess :exec
UPDATE connections SET scan_status='succeeded',scan_error='',last_scan_at=now(),next_scan_at=now()+interval '5 minutes' WHERE org_id=$1 AND id=$2 AND revision=$3 AND enabled AND deleted_at IS NULL;
-- name: RecordScanFailure :exec
UPDATE connections SET scan_status='failed',scan_error=$4,next_scan_at=now()+interval '5 minutes' WHERE org_id=$1 AND id=$2 AND revision=$3 AND deleted_at IS NULL;
-- name: ExpiredScans :many
SELECT id,org_id,connection_id,revision FROM scan_jobs WHERE status='running' AND lease_until<=now() LIMIT 100;
-- name: ExpireScan :execrows
UPDATE scan_jobs SET status='failed',error='worker_interrupted',finished_at=now() WHERE id=$1 AND status='running' AND lease_until<=now();
-- name: UpsertResource :exec
INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,public_ip,private_ip,size,observed_at,provider_identity,tag_metadata,catalog)
VALUES($1,$2,$3,$4,$5,$6,sqlc.arg(kind),$7,$8,$9,$10,$11,now(),sqlc.arg(provider_identity),sqlc.arg(tag_metadata),sqlc.arg(catalog))
ON CONFLICT(org_id,connection_id,kind,region,native_id) DO UPDATE SET catalog=excluded.catalog,tag_metadata=excluded.tag_metadata,name=excluded.name,status=excluded.status,public_ip=excluded.public_ip,private_ip=excluded.private_ip,size=excluded.size,provider_identity=excluded.provider_identity,observed_at=now(),deleted_at=NULL;
-- name: RetireMissingResources :exec
UPDATE resources SET deleted_at=now() WHERE org_id=$1 AND connection_id=$2 AND kind=ANY(sqlc.arg(covered_kinds)::text[]) AND deleted_at IS NULL AND NOT(id=ANY(sqlc.arg(present_ids)::text[]));
-- name: GetResource :one
SELECT r.id,r.connection_id,r.native_id,r.name,r.provider,r.kind,r.region,r.status,r.observed_at,r.public_ip,r.private_ip,r.size,r.provider_identity,r.tag_metadata,c.enabled AS connection_enabled,c.name AS connection_name FROM resources r JOIN connections c ON c.org_id=r.org_id AND c.id=r.connection_id WHERE r.org_id=$1 AND r.id=$2 AND r.deleted_at IS NULL AND c.deleted_at IS NULL;

-- name: ScanOvertakenByOperation :one
SELECT EXISTS(SELECT 1 FROM operations WHERE org_id=$1 AND connection_id=$2 AND updated_at>=sqlc.arg(scan_started) AND status IN ('dispatching','observing','uncertain','succeeded','failed','resolved'));
-- name: RescheduleOvertakenScan :exec
UPDATE connections SET scan_status='queued',scan_error='',next_scan_at=now() WHERE org_id=$1 AND id=$2;
