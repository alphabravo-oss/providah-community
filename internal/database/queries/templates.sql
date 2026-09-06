-- name: ListServerTemplates :many
SELECT * FROM server_templates WHERE org_id=$1 ORDER BY name,version DESC LIMIT 201;
-- name: GetServerTemplate :one
SELECT * FROM server_templates WHERE org_id=$1 AND id=$2 FOR SHARE;
-- name: PublishServerTemplate :one
INSERT INTO server_templates(id,org_id,connection_id,name,version,region,creation)
SELECT sqlc.arg(id),sqlc.arg(org_id),sqlc.arg(connection_id),sqlc.arg(name),coalesce(max(version),0)+1,sqlc.arg(region),sqlc.arg(creation)
FROM server_templates WHERE org_id=sqlc.arg(org_id) AND name=sqlc.arg(name)
RETURNING *;
-- name: SetServerTemplateStatus :one
UPDATE server_templates SET status=$3 WHERE org_id=$1 AND id=$2 AND (status='published' OR (status='retired' AND $3='revoked')) RETURNING *;
-- name: BindOperationTemplate :exec
UPDATE operations SET template_id=$3 WHERE org_id=$1 AND id=$2;
