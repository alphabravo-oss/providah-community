-- name: GetMFAPolicy :one
SELECT coalesce((SELECT required FROM mfa_policies p WHERE p.scope=sqlc.arg(scope)),false)::boolean AS required,
coalesce((SELECT revision FROM mfa_policies p WHERE p.scope=sqlc.arg(scope)),0)::bigint AS revision,
coalesce((SELECT required FROM mfa_policies p WHERE p.scope=''),false)::boolean AS global_required;
-- name: SaveMFAPolicy :exec
INSERT INTO mfa_policies(scope,required) VALUES($1,$2) ON CONFLICT(scope) DO UPDATE SET required=excluded.required,revision=mfa_policies.revision+1;
-- name: MFARequiredForUser :one
SELECT EXISTS(SELECT 1 FROM mfa_policies p WHERE required AND (scope='' OR EXISTS(SELECT 1 FROM effective_memberships m WHERE m.user_id=$1 AND m.org_id=p.scope)))::boolean;
-- name: LockMFAPolicies :exec
SELECT scope FROM mfa_policies WHERE scope='' FOR UPDATE;
