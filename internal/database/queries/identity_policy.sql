-- name: IdentityAllows :one
SELECT identity_allows(sqlc.arg(org_id)::text,sqlc.arg(user_id)::text,sqlc.narg(oidc_id)::uuid)::boolean;
-- name: SetSessionIdentity :exec
UPDATE sessions SET oidc_id=$2 WHERE id=$1;
-- name: OrganizationsForSession :many
SELECT o.id,o.name,m.permissions,mfa_required(o.id)::boolean AS mfa_required,identity_allows(o.id,m.user_id,sqlc.narg(oidc_id)::uuid)::boolean AS allowed FROM organizations o JOIN effective_memberships m ON m.org_id=o.id WHERE m.user_id=sqlc.arg(user_id) ORDER BY o.name,o.id;
-- name: GetIdentityPolicy :one
SELECT o.id AS org_id,coalesce(p.enabled,false)::boolean AS enabled,coalesce(p.issuer,'')::text AS issuer,coalesce(p.recovery_users,'{}')::text[] AS recovery_users,coalesce(p.revision,0)::bigint AS revision FROM organizations o LEFT JOIN identity_policies p ON p.org_id=o.id WHERE o.id=$1;
-- name: SaveIdentityPolicy :exec
INSERT INTO identity_policies(org_id,enabled,issuer,recovery_users) VALUES($1,$2,$3,$4) ON CONFLICT(org_id) DO UPDATE SET enabled=excluded.enabled,issuer=excluded.issuer,recovery_users=excluded.recovery_users,revision=identity_policies.revision+1;
-- name: CountEligibleRecoveryUsers :one
SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND m.user_id=ANY(sqlc.arg(users)::text[]) AND m.active AND u.active AND m.role_id='administrator';
-- name: IdentityRecoveryMissing :one
SELECT EXISTS(SELECT 1 FROM identity_policies p WHERE p.org_id=$1 AND p.enabled AND NOT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=p.org_id AND m.user_id=ANY(p.recovery_users) AND m.active AND u.active AND m.role_id='administrator'));
-- name: StampOperationIdentity :exec
UPDATE operations SET requester_oidc_id=$3,approver_oidc_id=$4 WHERE org_id=$1 AND id=$2;
-- name: StampScheduleIdentity :exec
UPDATE schedules SET editor_oidc_id=$3,approver_oidc_id=NULL WHERE org_id=$1 AND id=$2;
