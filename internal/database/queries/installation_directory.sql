-- name: InstallationUsers :many
SELECT id,email,active,global_admin,mfa_enabled,admin_revision FROM users
WHERE id>sqlc.arg(after_id) AND (sqlc.arg(search)::text='' OR strpos(lower(email),lower(sqlc.arg(search)))>0)
AND (sqlc.arg(state)::text='' OR active=(sqlc.arg(state)='active')) ORDER BY id LIMIT 101;
-- name: InstallationOrganizations :many
SELECT id,name,active,admin_revision FROM organizations
WHERE id>sqlc.arg(after_id) AND (sqlc.arg(search)::text='' OR strpos(lower(name),lower(sqlc.arg(search)))>0)
AND (sqlc.arg(state)::text='' OR active=(sqlc.arg(state)='active')) ORDER BY id LIMIT 101;

-- name: UpdateInstallationUser :execrows
UPDATE users SET active=sqlc.arg(active),global_admin=sqlc.arg(global_admin),admin_revision=admin_revision+1
WHERE id=sqlc.arg(id) AND admin_revision=sqlc.arg(expected_revision);
-- name: AddInstallationUserEvent :exec
INSERT INTO installation_events(actor_id,target_id,action,details)
VALUES(sqlc.arg(actor_id),sqlc.arg(target_id),'user.access_changed',jsonb_build_object('active',sqlc.arg(active)::boolean,'global_admin',sqlc.arg(global_admin)::boolean));

-- name: ListInstallationAudit :many
SELECT e.id,''::text AS org_id,''::text AS organization_name,e.actor_email AS actor,e.action,coalesce(e.target_id,'')::text AS target,e.details,e.occurred_at
FROM installation_events e
WHERE e.id<sqlc.arg(before_id)
AND (sqlc.arg(actor)::text='' OR e.actor_email=sqlc.arg(actor))
AND (sqlc.arg(action)::text='' OR e.action=sqlc.arg(action))
AND (sqlc.arg(target)::text='' OR e.target_id=sqlc.arg(target))
AND (sqlc.narg(occurred_from)::timestamptz IS NULL OR e.occurred_at>=sqlc.narg(occurred_from))
AND (sqlc.narg(occurred_before)::timestamptz IS NULL OR e.occurred_at<sqlc.narg(occurred_before))

ORDER BY e.id DESC LIMIT 101;
-- name: ListGlobalOrganizationAudit :many
SELECT e.id,e.org_id,o.name AS organization_name,e.actor,e.action,e.target,e.details,e.occurred_at
FROM audit_events e JOIN organizations o ON o.id=e.org_id
WHERE e.id<sqlc.arg(before_id)
AND (sqlc.arg(actor)::text='' OR e.actor=sqlc.arg(actor))
AND (sqlc.arg(action)::text='' OR e.action=sqlc.arg(action))
AND (sqlc.arg(target)::text='' OR e.target=sqlc.arg(target))
AND (sqlc.narg(occurred_from)::timestamptz IS NULL OR e.occurred_at>=sqlc.narg(occurred_from))
AND (sqlc.narg(occurred_before)::timestamptz IS NULL OR e.occurred_at<sqlc.narg(occurred_before))
AND (sqlc.arg(org_id)::text='' OR e.org_id=sqlc.arg(org_id))
ORDER BY e.id DESC LIMIT 101;

-- name: UpdateInstallationOrganization :execrows
UPDATE organizations SET active=sqlc.arg(active),admin_revision=admin_revision+1
WHERE id=sqlc.arg(id) AND admin_revision=sqlc.arg(expected_revision);

-- name: AddInstallationPolicyEvent :exec
INSERT INTO installation_events(actor_id,target_id,action,details)
VALUES(sqlc.arg(actor_id),NULL,sqlc.arg(action),jsonb_build_object('required',sqlc.arg(required)::boolean,'revision',sqlc.arg(revision)::bigint));
