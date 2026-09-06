-- name: ListResourceScopes :many
SELECT 'connection'::text AS kind,c.id::text AS value,c.name::text AS label FROM connections c WHERE c.org_id=sqlc.arg(org_id) AND EXISTS(SELECT 1 FROM resources r WHERE r.org_id=c.org_id AND r.connection_id=c.id AND r.deleted_at IS NULL AND NOT r.catalog)
UNION ALL
SELECT DISTINCT 'region'::text,region::text,region::text FROM resources WHERE org_id=sqlc.arg(org_id) AND deleted_at IS NULL AND NOT catalog AND region<>''
UNION ALL
SELECT DISTINCT 'status'::text,status::text,status::text FROM resources WHERE org_id=sqlc.arg(org_id) AND deleted_at IS NULL AND NOT catalog AND status<>''
ORDER BY 1,3,2 LIMIT 1001;
