-- name: UnindexedOwnershipState :one
SELECT s.* FROM automation_states s LEFT JOIN state_ownership_scans scan ON scan.state_id=s.id WHERE scan.state_id IS NULL OR scan.scanner_version < 11 ORDER BY s.created_at,s.id LIMIT 1;
-- name: OwnershipProjectScope :one
SELECT c.provider,v.connection_id,v.region FROM automation_projects p JOIN automation_versions v ON v.org_id=p.org_id AND v.id=p.version_id JOIN connections c ON c.org_id=v.org_id AND c.id=v.connection_id WHERE p.org_id=$1 AND p.id=$2;
-- name: InsertOwnershipClaims :exec
INSERT INTO resource_ownership_claims(org_id,connection_id,project_id,provider,kind,region,native_id,first_state_id)
SELECT sqlc.arg(org_id),sqlc.arg(connection_id),sqlc.arg(project_id),sqlc.arg(provider),r.kind,r.region,r.native_id,sqlc.arg(state_id)
FROM jsonb_to_recordset(sqlc.arg(refs)::jsonb) AS r(kind text,native_id text,region text)
ON CONFLICT DO NOTHING;
-- name: RecordOwnershipScan :exec
INSERT INTO state_ownership_scans(state_id,status,unsupported,scanner_version) VALUES($1,$2,$3,11) ON CONFLICT(state_id) DO UPDATE SET status=EXCLUDED.status,unsupported=EXCLUDED.unsupported,scanner_version=EXCLUDED.scanner_version,scanned_at=now();
-- name: ResourceOwnership :many
SELECT DISTINCT project_id FROM resource_ownership_claims WHERE org_id=$1 AND connection_id=$2 AND kind=$3 AND native_id=$4 AND (provider<>'aws' OR region=$5) ORDER BY project_id;

-- name: ProjectOwnershipSummary :one
SELECT COALESCE(scan.status,CASE WHEN p.state_id='' THEN 'no_state' ELSE 'pending' END)::text AS status,
 COALESCE(scan.unsupported,0)::integer AS unsupported, COALESCE(scan.scanner_version,0)::integer AS scanner_version,
 scan.scanned_at,
 (SELECT count(*) FROM resource_ownership_claims c WHERE c.org_id=p.org_id AND c.project_id=p.id)::bigint AS protected_references,
 (SELECT count(*) FROM automation_states s LEFT JOIN state_ownership_scans x ON x.state_id=s.id WHERE s.org_id=p.org_id AND s.project_id=p.id AND (x.state_id IS NULL OR x.status<>'complete' OR x.scanner_version<11))::bigint AS incomplete_versions
FROM automation_projects p LEFT JOIN state_ownership_scans scan ON scan.state_id=p.state_id WHERE p.org_id=$1 AND p.id=$2;

-- name: ListProjectOwnership :many
SELECT c.*, (c.kind||'/'||c.region||'/'||c.native_id)::text AS page_key,
 COALESCE((SELECT r.id FROM resources r WHERE r.org_id=c.org_id AND r.connection_id=c.connection_id AND r.kind=c.kind AND r.native_id=c.native_id AND (c.provider<>'aws' OR r.region=c.region) AND r.deleted_at IS NULL ORDER BY r.region,r.id LIMIT 1),'')::text AS resource_id,
 EXISTS(SELECT 1 FROM resource_ownership_claims other WHERE other.org_id=c.org_id AND other.connection_id=c.connection_id AND other.kind=c.kind AND other.native_id=c.native_id AND (c.provider<>'aws' OR other.region=c.region) AND other.project_id<>c.project_id) AS conflicting
FROM resource_ownership_claims c WHERE c.org_id=$1 AND c.project_id=$2 AND (c.kind||'/'||c.region||'/'||c.native_id)>sqlc.arg(after_key)::text ORDER BY page_key LIMIT 101;
