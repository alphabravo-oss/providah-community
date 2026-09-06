-- +goose Up
ALTER TABLE oidc_links ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE;
ALTER TABLE sessions ADD COLUMN oidc_id uuid REFERENCES oidc_links(id) ON DELETE SET NULL;
ALTER TABLE operations ADD COLUMN requester_oidc_id uuid,ADD COLUMN approver_oidc_id uuid;
ALTER TABLE schedules ADD COLUMN editor_oidc_id uuid,ADD COLUMN approver_oidc_id uuid;
CREATE TABLE identity_policies (
 org_id text PRIMARY KEY REFERENCES organizations(id),enabled boolean NOT NULL DEFAULT false,issuer text NOT NULL DEFAULT '',
 recovery_users text[] NOT NULL DEFAULT '{}',revision bigint NOT NULL DEFAULT 1,
 CHECK(cardinality(recovery_users)<=20),CHECK(NOT enabled OR (issuer<>'' AND cardinality(recovery_users)>0))
);
-- +goose StatementBegin
CREATE FUNCTION identity_allows(org text,uid text,login uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM identity_policies p WHERE p.org_id=org AND p.enabled
 AND NOT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=org AND m.user_id=uid AND m.active AND u.active AND m.role_id='administrator' AND uid=ANY(p.recovery_users))
 AND NOT EXISTS(SELECT 1 FROM oidc_links l WHERE l.id=login AND l.user_id=uid AND l.issuer=p.issuer))
$$;
-- +goose StatementEnd
UPDATE roles SET permissions=permissions||ARRAY['identity.manage'] WHERE id='administrator' AND builtin;
-- +goose Down
DROP FUNCTION identity_allows;
DROP TABLE identity_policies;
ALTER TABLE schedules DROP COLUMN editor_oidc_id,DROP COLUMN approver_oidc_id;
ALTER TABLE operations DROP COLUMN requester_oidc_id,DROP COLUMN approver_oidc_id;
ALTER TABLE sessions DROP COLUMN oidc_id;
ALTER TABLE oidc_links DROP COLUMN id;
UPDATE roles SET permissions=array_remove(permissions,'identity.manage') WHERE builtin;
