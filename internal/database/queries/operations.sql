-- name: GetOperation :one
SELECT * FROM operations WHERE org_id=$1 AND id=$2;
-- name: LockOperation :one
SELECT * FROM operations WHERE org_id=$1 AND id=$2 FOR UPDATE;
-- name: OperationByKey :one
SELECT * FROM operations WHERE org_id=$1 AND requester_id=$2 AND idempotency_key=$3;
-- name: CreateOperation :one
INSERT INTO operations(id,org_id,resource_id,connection_id,connection_revision,provider,native_id,region,resource_name,action,expected_status,reason,requester_id,requester_email,status,idempotency_key,maintenance_revision,maintenance_exception_reason,maintenance_expires_at,module_revision,runtime_id,deletion_impact,resource_kind,expected_size,target_size,provider_identity,expected_tags,target_tags,trace_parent)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,coalesce(nullif(sqlc.arg(resource_kind)::text,''),'compute.server'),sqlc.arg(expected_size),sqlc.arg(target_size),sqlc.arg(provider_identity),sqlc.narg(expected_tags),sqlc.narg(target_tags),sqlc.arg(trace_parent)) RETURNING *;
-- name: ListOperations :many
SELECT * FROM operations WHERE org_id=sqlc.arg(org_id) AND id<sqlc.arg(id)
AND (NOT sqlc.arg(filter_resources)::boolean OR resource_id IN (SELECT id FROM filtered_inventory(sqlc.arg(org_id)::text,sqlc.arg(search)::text,sqlc.arg(provider)::text,sqlc.arg(kind)::text,sqlc.arg(connection_id)::text,sqlc.arg(tag_key)::text,sqlc.arg(tag_value)::text,sqlc.arg(tag_exists)::boolean,sqlc.arg(tag_name)::text,sqlc.arg(tag_conditions)::jsonb,sqlc.arg(tag_match_any)::boolean,sqlc.arg(filter_region)::text,sqlc.arg(filter_status)::text)))
ORDER BY id DESC LIMIT 101;
-- name: ReviewOperation :exec
UPDATE operations SET status=$3,approver_id=$4,approval_expires_at=now()+interval '1 hour',updated_at=now(),detail=$5,approver_oidc_id=sqlc.narg(oidc_id)::uuid WHERE org_id=$1 AND id=$2;
-- name: ClaimOperation :one
UPDATE operations SET lease_until=now()+interval '3 minutes',updated_at=now(),status=CASE WHEN status='queued' THEN 'dispatching' ELSE status END
WHERE id=(SELECT id FROM operations WHERE status IN ('queued','observing') AND (lease_until IS NULL OR lease_until<=now()) AND next_attempt_at<=now() ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *;
-- name: SaveOperationOutcome :exec
UPDATE operations SET status=$3,provider_action_id=$4,observed_status=$5,detail=$6,updated_at=now(),next_attempt_at=now()+interval '5 seconds',
 observation_deadline=coalesce(observation_deadline,now()+interval '15 minutes'),lease_until=CASE WHEN $3='observing' THEN NULL ELSE lease_until END
WHERE org_id=$1 AND id=$2;
-- name: ExpiredDispatches :many
SELECT * FROM operations WHERE status='dispatching' AND lease_until<=now() LIMIT 100;
-- name: ExpiredPendingOperations :many
SELECT * FROM operations WHERE status IN ('awaiting_approval','queued') AND (expires_at<=now() OR approval_expires_at<=now()) LIMIT 100;
-- name: RefreshAfterOperation :exec
UPDATE connections SET next_scan_at=now() WHERE org_id=$1 AND id=$2 AND enabled AND deleted_at IS NULL;
-- name: LockOperationConnection :one
SELECT * FROM connections WHERE org_id=$1 AND id=$2 FOR UPDATE;
-- name: RecheckOperation :exec
UPDATE operations SET status='observing',lease_until=NULL,next_attempt_at=now(),observation_deadline=now()+interval '15 minutes',updated_at=now(),detail='Checking provider state; no action will be resubmitted.' WHERE org_id=$1 AND id=$2;

-- name: MarkServerDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND id=$2 AND kind='compute.server';

-- name: SaveCreationInput :exec
UPDATE operations SET creation=$3 WHERE org_id=$1 AND id=$2;
-- name: BindCreatedServer :exec
UPDATE operations SET native_id=$3,resource_id=$4 WHERE org_id=$1 AND id=$2 AND action='create' AND (native_id='' OR native_id=$3);

-- name: UpsertCreatedServer :one
INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size)
VALUES($1,$2,$3,$4,$5,$6,'compute.server',$7,$8,$9)
ON CONFLICT(org_id,connection_id,kind,region,native_id) DO UPDATE SET status=excluded.status,observed_at=now(),deleted_at=NULL
RETURNING id;

-- name: MarkSnapshotDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind IN ('storage.snapshot','compute.image');

-- name: MarkVolumeDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind='storage.volume';
-- name: MarkNetworkDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind=$4 AND kind IN ('network.network','network.firewall','network.load_balancer') AND (provider<>'aws' OR region=$5);
-- name: MarkSSHKeyDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind='access.ssh_key' AND (provider<>'aws' OR region=$4);

-- name: OperationOverviewCounts :one
SELECT count(*) FILTER (WHERE status='awaiting_approval') AS pending,
 count(*) FILTER (WHERE status IN ('queued','dispatching','observing')) AS active,
 count(*) FILTER (WHERE status='failed') AS failed,
 count(*) FILTER (WHERE status='uncertain') AS uncertain
FROM operations WHERE org_id=$1;
-- name: ListOverviewOperations :many
SELECT * FROM operations WHERE org_id=$1 AND status=ANY(sqlc.arg(statuses)::text[]) ORDER BY updated_at DESC,id DESC LIMIT 10;

-- name: MarkDatabaseSnapshotDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind=$4 AND region=$5 AND kind IN ('database.snapshot','database.cluster_snapshot');

-- name: MarkImageDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND region=$4 AND kind='compute.image';

-- name: MarkCloudProjectDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind='organization.project';

-- name: UpdateObservedServerTags :exec
UPDATE resources SET tag_metadata=$5 WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND region=$4 AND kind='compute.server' AND deleted_at IS NULL;

-- name: MarkPlacementGroupDeleted :exec
UPDATE resources SET deleted_at=now(),status='deleted',observed_at=now() WHERE org_id=$1 AND connection_id=$2 AND native_id=$3 AND kind='compute.placement_group' AND (provider<>'aws' OR region=$4);

-- name: UpsertCreatedSSHKey :one
INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status)
VALUES($1,$2,$3,$4,$5,$6,'access.ssh_key',$7,$8)
ON CONFLICT(org_id,connection_id,kind,region,native_id) DO UPDATE SET status=excluded.status,observed_at=now(),deleted_at=NULL
RETURNING id;
