-- name: ListAutomationSources :many
SELECT id,name,runtime,entrypoint,sha256,files,archive_bytes,expanded_bytes,input_schema FROM automation_sources WHERE org_id=$1 ORDER BY created_at DESC,id LIMIT 51;
-- name: ImportAutomationSource :one
INSERT INTO automation_sources(id,org_id,name,runtime,entrypoint,sha256,files,archive_bytes,expanded_bytes,ciphertext,input_schema,storage)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(org_id,runtime,entrypoint,sha256) DO UPDATE SET sha256=automation_sources.sha256
RETURNING id;
-- name: VerifyAutomationSource :one
SELECT * FROM automation_sources WHERE id>$1 ORDER BY id LIMIT 1;
-- name: RecoveryArtifact :one
SELECT kind,id,org_id,object_key::text,ciphertext FROM (
 SELECT 'sources'::text AS kind,id,org_id,'sources/'||org_id||'/'||id AS object_key,ciphertext FROM automation_sources WHERE storage='s3'
 UNION ALL
 SELECT 'states'::text AS kind,id,org_id,'states/'||org_id||'/'||project_id||'/'||id AS object_key,ciphertext FROM automation_states WHERE storage='s3'
) objects WHERE (kind,id)>(sqlc.arg(after_kind)::text,sqlc.arg(after_id)::text) ORDER BY kind,id LIMIT 1;
-- name: RebindSourceArtifact :exec
UPDATE automation_sources SET ciphertext=$2 WHERE id=$1 AND storage='s3';
-- name: RebindStateArtifact :exec
UPDATE automation_states SET ciphertext=$2 WHERE id=$1 AND storage='s3';

-- name: GetAutomationSourceMetadata :one
SELECT id,name,runtime,entrypoint,sha256,files,archive_bytes,expanded_bytes,input_schema FROM automation_sources WHERE org_id=$1 AND id=$2;
