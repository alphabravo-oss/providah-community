-- name: ListAutomationVersions :many
SELECT * FROM automation_versions WHERE org_id=$1 ORDER BY name,version DESC LIMIT 201;
-- name: PublishAutomationVersion :one
INSERT INTO automation_versions(id,org_id,name,version,source_id,runtime_image,runtime_version,connection_id,connection_revision,region,review_note,runtime,runtime_policy,dependency_hosts)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING *;
-- name: SetAutomationVersionStatus :one
UPDATE automation_versions SET status=$3 WHERE org_id=$1 AND id=$2 AND (status='published' OR (status='retired' AND $3='revoked')) RETURNING *;

-- name: GetAutomationVersion :one
SELECT * FROM automation_versions WHERE org_id=$1 AND id=$2;
