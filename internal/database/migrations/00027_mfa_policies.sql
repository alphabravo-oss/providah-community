-- +goose Up
CREATE TABLE mfa_policies(scope text PRIMARY KEY,required boolean NOT NULL DEFAULT false,revision bigint NOT NULL DEFAULT 1);
INSERT INTO mfa_policies(scope) VALUES('');
-- +goose StatementBegin
CREATE FUNCTION mfa_required(org text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM mfa_policies WHERE scope IN ('',org) AND required)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity_allows(org text,uid text,login uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT (EXISTS(SELECT 1 FROM users WHERE id=uid AND active AND global_admin) OR NOT EXISTS(SELECT 1 FROM identity_policies p WHERE p.org_id=org AND p.enabled
 AND NOT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=org AND m.user_id=uid AND m.active AND u.active AND m.role_id='administrator' AND uid=ANY(p.recovery_users))
 AND NOT EXISTS(SELECT 1 FROM oidc_links l WHERE l.id=login AND l.user_id=uid AND l.issuer=p.issuer))) AND (NOT mfa_required(org) OR EXISTS(SELECT 1 FROM users WHERE id=uid AND active AND mfa_enabled))
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM mfa_policies WHERE required) THEN RAISE EXCEPTION 'Disable required MFA policies before rollback'; END IF; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity_allows(org text,uid text,login uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM users WHERE id=uid AND active AND global_admin) OR NOT EXISTS(SELECT 1 FROM identity_policies p WHERE p.org_id=org AND p.enabled
 AND NOT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=org AND m.user_id=uid AND m.active AND u.active AND m.role_id='administrator' AND uid=ANY(p.recovery_users))
 AND NOT EXISTS(SELECT 1 FROM oidc_links l WHERE l.id=login AND l.user_id=uid AND l.issuer=p.issuer))
$$;
-- +goose StatementEnd
DROP FUNCTION mfa_required(text);
DROP TABLE mfa_policies;
