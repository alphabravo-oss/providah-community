-- name: GetProviderModule :one
SELECT coalesce(m.enabled,true)::boolean AS enabled,coalesce(m.revision,1)::bigint AS revision,coalesce(m.runtime_id,'')::text AS runtime_id FROM (SELECT sqlc.arg(org_id)::text AS org_id,sqlc.arg(provider)::text AS provider) p LEFT JOIN provider_modules m ON m.org_id=p.org_id AND m.provider=p.provider;
-- name: SetProviderModule :exec
INSERT INTO provider_modules(org_id,provider,enabled,revision) VALUES($1,$2,$3,2)
ON CONFLICT(org_id,provider) DO UPDATE SET enabled=excluded.enabled,revision=provider_modules.revision+1 WHERE provider_modules.enabled IS DISTINCT FROM excluded.enabled;
-- name: ListProviderModules :many
SELECT p.provider,coalesce(m.enabled,true)::boolean AS enabled,coalesce(m.revision,1)::bigint AS revision,coalesce(m.runtime_id,'')::text AS runtime_id,
 (SELECT count(*) FROM connections c WHERE c.org_id=sqlc.arg(org_id) AND c.provider=p.provider AND c.deleted_at IS NULL)::bigint AS connections,
 (SELECT count(*) FROM operations o WHERE o.org_id=sqlc.arg(org_id) AND o.provider=p.provider AND o.status IN ('dispatching','observing','uncertain'))::bigint AS active_operations
FROM (SELECT 'aws'::text AS provider UNION ALL SELECT 'digitalocean'::text UNION ALL SELECT 'hetzner'::text) p LEFT JOIN provider_modules m ON m.org_id=sqlc.arg(org_id) AND m.provider=p.provider ORDER BY p.provider;
-- name: RecordModuleScanCanceled :exec
UPDATE connections SET scan_status='never',scan_error='Provider module changed; refresh after enabling it.' WHERE org_id=$1 AND id=$2 AND revision=$3;

-- name: SetProviderRuntime :exec
INSERT INTO provider_modules(org_id,provider,runtime_id,revision) VALUES($1,$2,$3,2)
ON CONFLICT(org_id,provider) DO UPDATE SET runtime_id=excluded.runtime_id,revision=provider_modules.revision+1 WHERE provider_modules.runtime_id IS DISTINCT FROM excluded.runtime_id;
