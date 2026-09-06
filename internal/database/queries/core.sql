-- name: UserCount :one
SELECT count(*) FROM users;
-- name: LockSetup :exec
SELECT pg_advisory_xact_lock(74193820);
-- name: SetPendingSetup :exec
INSERT INTO setup_pending(id,ciphertext,expires_at) VALUES(true,$1,now()+interval '10 minutes') ON CONFLICT(id) DO UPDATE SET ciphertext=excluded.ciphertext,expires_at=excluded.expires_at;
-- name: PendingSetup :one
SELECT ciphertext FROM setup_pending WHERE expires_at>now();
-- name: ClearSetup :exec
DELETE FROM setup_pending;
-- name: CreateUser :exec
INSERT INTO users(id,email,password_hash,totp_ciphertext,last_totp_step) VALUES($1,$2,$3,$4,$5);
-- name: UserByEmail :one
SELECT * FROM users WHERE email=$1 AND active=true;
-- name: ConsumeTOTP :execrows
UPDATE users SET last_totp_step=$2 WHERE id=$1 AND active=true AND last_totp_step<$2;
-- name: CreateOrganization :exec
INSERT INTO organizations(id,name) VALUES($1,$2);
-- name: AddMembership :exec
INSERT INTO memberships(org_id,user_id,permissions) VALUES($1,$2,$3);
-- name: OrganizationsForUser :many
SELECT o.id,o.name,m.permissions FROM organizations o JOIN effective_memberships m ON m.org_id=o.id WHERE m.user_id=$1 ORDER BY o.name,o.id;
-- name: Permissions :one
SELECT permissions FROM effective_memberships WHERE org_id=$1 AND user_id=$2;
-- name: RateLimit :one
INSERT INTO auth_limits(key,window_at,attempts) VALUES($1,now(),1) ON CONFLICT(key) DO UPDATE SET window_at=CASE WHEN auth_limits.window_at<now()-interval '1 minute' THEN now() ELSE auth_limits.window_at END,attempts=CASE WHEN auth_limits.window_at<now()-interval '1 minute' THEN 1 ELSE auth_limits.attempts+1 END RETURNING attempts;
-- name: CreateSession :exec
INSERT INTO sessions(id,user_id,refresh_hash,expires_at) VALUES($1,$2,$3,now()+interval '30 days');
-- name: GetSession :one
SELECT s.id,s.user_id,s.mfa_at,s.oidc_id,u.email,u.mfa_enabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.expires_at>now() AND u.active;
-- name: SessionByRefresh :one
SELECT s.id,s.user_id,s.mfa_at,s.oidc_id,u.email,u.mfa_enabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.refresh_hash=$1 AND s.expires_at>now() AND u.active FOR UPDATE OF s;
-- name: RotateSession :exec
UPDATE sessions SET refresh_hash=$2 WHERE id=$1;
-- name: DeleteSession :exec
DELETE FROM sessions WHERE id=$1;
-- name: ListConnections :many
SELECT id,org_id,name,provider,region,enabled,created_at,scan_status,scan_error,last_scan_at,credential_source FROM connections WHERE org_id=$1 AND deleted_at IS NULL ORDER BY created_at,id LIMIT 1000;
-- name: CreateConnection :one
INSERT INTO connections(id,org_id,name,provider,region,key_id,ciphertext,credential_source) VALUES($1,$2,$3,$4,$5,$6,$7,coalesce(nullif(sqlc.arg(credential_source)::text,''),'builtin')) RETURNING id,org_id,name,provider,region,enabled,created_at,scan_status,scan_error,last_scan_at,credential_source;
-- name: SetConnectionEnabled :one
UPDATE connections SET enabled=$3,revision=revision+1,scan_status='never',scan_error='',next_scan_at=now() WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL RETURNING id,org_id,name,provider,region,enabled,created_at,scan_status,scan_error,last_scan_at,credential_source;
-- name: RotateCredential :one
UPDATE connections SET ciphertext=$3,key_id=$4,credential_source=coalesce(nullif(sqlc.arg(credential_source)::text,''),'builtin'),revision=revision+1,scan_status='never',scan_error='',next_scan_at=now() WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL RETURNING id,org_id,name,provider,region,enabled,created_at,scan_status,scan_error,last_scan_at,credential_source;
-- ponytail: filtered result sorting uses PostgreSQL; add measured sort indexes if the 100k-resource load target needs them.
-- name: ListResources :many
WITH filtered AS (
SELECT id,connection_id,native_id,name,provider,kind,region,status,observed_at,public_ip,private_ip,size,tag_metadata,
(CASE sqlc.arg(sort_by)::text WHEN 'name' THEN name WHEN 'provider' THEN provider WHEN 'kind' THEN kind WHEN 'region' THEN region WHEN 'status' THEN status ELSE id END COLLATE "C")::text AS sort_value
FROM filtered_inventory(sqlc.arg(org_id)::text,sqlc.arg(search)::text,sqlc.arg(provider)::text,sqlc.arg(kind)::text,sqlc.arg(connection_id)::text,sqlc.arg(tag_key)::text,sqlc.arg(tag_value)::text,sqlc.arg(tag_exists)::boolean,sqlc.arg(tag_name)::text,sqlc.arg(tag_conditions)::jsonb,sqlc.arg(tag_match_any)::boolean,sqlc.arg(filter_region)::text,sqlc.arg(filter_status)::text)
)
SELECT * FROM filtered
WHERE sqlc.arg(after_id)::text='' OR
(NOT sqlc.arg(descending)::boolean AND (sort_value,id)>(sqlc.arg(after_value)::text COLLATE "C",sqlc.arg(after_id)::text)) OR
(sqlc.arg(descending)::boolean AND (sort_value,id)<(sqlc.arg(after_value)::text COLLATE "C",sqlc.arg(after_id)::text))
ORDER BY CASE WHEN NOT sqlc.arg(descending)::boolean THEN sort_value END ASC,
CASE WHEN sqlc.arg(descending)::boolean THEN sort_value END DESC,
CASE WHEN NOT sqlc.arg(descending)::boolean THEN id END ASC,
CASE WHEN sqlc.arg(descending)::boolean THEN id END DESC LIMIT sqlc.arg(page_size)::integer;
-- name: AddAudit :exec
WITH locked AS MATERIALIZED (SELECT organizations.id FROM organizations WHERE organizations.id=sqlc.arg(org_id) FOR UPDATE)
INSERT INTO audit_events(org_id,actor,action,target,details) SELECT locked.id,sqlc.arg(actor),sqlc.arg(action),sqlc.arg(target),sqlc.arg(details) FROM locked;
-- name: ListAudit :many
SELECT * FROM audit_events WHERE org_id=$1 AND id<$2
AND (sqlc.arg(actor)::text='' OR actor=sqlc.arg(actor))
AND (sqlc.arg(action)::text='' OR action=sqlc.arg(action))
AND (sqlc.arg(target)::text='' OR target=sqlc.arg(target))
AND (sqlc.narg(occurred_from)::timestamptz IS NULL OR occurred_at>=sqlc.narg(occurred_from))
AND (sqlc.narg(occurred_before)::timestamptz IS NULL OR occurred_at<sqlc.narg(occurred_before))
ORDER BY id DESC LIMIT 101;

-- name: GetConnectionMetadata :one
SELECT id,org_id,name,provider,region,enabled,created_at,scan_status,scan_error,last_scan_at,credential_source FROM connections WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL;
