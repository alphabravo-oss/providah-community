-- +goose Up
ALTER TABLE users ADD COLUMN global_admin boolean NOT NULL DEFAULT false, ADD COLUMN mfa_enabled boolean NOT NULL DEFAULT true, ADD COLUMN admin_seeded_at timestamptz;
-- +goose StatementBegin
CREATE VIEW effective_memberships AS
SELECT o.id AS org_id,u.id AS user_id,CASE WHEN u.global_admin THEN coalesce(a.permissions,(SELECT permissions FROM roles WHERE id='administrator' AND builtin ORDER BY org_id LIMIT 1),'{}') ELSE coalesce(r.permissions,m.permissions) END::text[] AS permissions
FROM organizations o CROSS JOIN users u
LEFT JOIN roles a ON a.org_id=o.id AND a.id='administrator' AND a.builtin
LEFT JOIN memberships m ON m.org_id=o.id AND m.user_id=u.id
LEFT JOIN roles r ON r.org_id=o.id AND r.id=m.role_id
WHERE o.active AND u.active AND (u.global_admin OR m.active);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity_allows(org text,uid text,login uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM users WHERE id=uid AND active AND global_admin) OR NOT EXISTS(SELECT 1 FROM identity_policies p WHERE p.org_id=org AND p.enabled
 AND NOT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=org AND m.user_id=uid AND m.active AND u.active AND m.role_id='administrator' AND uid=ANY(p.recovery_users))
 AND NOT EXISTS(SELECT 1 FROM oidc_links l WHERE l.id=login AND l.user_id=uid AND l.issuer=p.issuer))
$$;
-- +goose StatementEnd
-- +goose Down
-- Refuse rollback while installation administrators or optional-MFA accounts exist.
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM users WHERE global_admin OR NOT mfa_enabled) THEN RAISE EXCEPTION 'Resolve global administrator and optional MFA accounts before rollback'; END IF; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity_allows(org text,uid text,login uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM identity_policies p WHERE p.org_id=org AND p.enabled
 AND NOT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=org AND m.user_id=uid AND m.active AND u.active AND m.role_id='administrator' AND uid=ANY(p.recovery_users))
 AND NOT EXISTS(SELECT 1 FROM oidc_links l WHERE l.id=login AND l.user_id=uid AND l.issuer=p.issuer))
$$;
-- +goose StatementEnd
DROP VIEW effective_memberships;
ALTER TABLE users DROP COLUMN global_admin,DROP COLUMN mfa_enabled,DROP COLUMN admin_seeded_at;
