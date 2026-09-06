-- +goose Up
CREATE TABLE roles (
 org_id text NOT NULL REFERENCES organizations(id), id text NOT NULL, name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 permissions text[] NOT NULL, builtin boolean NOT NULL DEFAULT false, revision bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(org_id,id), UNIQUE(org_id,name)
);
INSERT INTO roles(org_id,id,name,permissions,builtin)
SELECT o.id,r.id,r.name,r.permissions,true FROM organizations o CROSS JOIN (VALUES
 ('administrator','Administrator',ARRAY['connections.read','connections.manage','resources.read','audit.read','members.read','members.manage','roles.manage']),
 ('viewer','Viewer',ARRAY['connections.read','resources.read']),
 ('connection-manager','Connection manager',ARRAY['connections.read','connections.manage','resources.read']),
 ('auditor','Auditor',ARRAY['audit.read'])
) AS r(id,name,permissions);
ALTER TABLE memberships ADD COLUMN role_id text, ADD COLUMN active boolean NOT NULL DEFAULT true,
 ADD CONSTRAINT membership_role FOREIGN KEY(org_id,role_id) REFERENCES roles(org_id,id);
UPDATE memberships m SET role_id='administrator' FROM users u WHERE u.id=m.user_id AND EXISTS(SELECT 1 FROM audit_events a WHERE a.org_id=m.org_id AND a.actor=u.email AND a.action='installation.initialized');
CREATE TABLE invitations (
 id text PRIMARY KEY, org_id text NOT NULL REFERENCES organizations(id), email text NOT NULL, role_id text NOT NULL, role_revision bigint NOT NULL,
 inviter_id text NOT NULL REFERENCES users(id), token_hash bytea NOT NULL UNIQUE,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL DEFAULT now()+interval '48 hours',
 revoked_at timestamptz, accepted_at timestamptz, totp_ciphertext bytea,
 FOREIGN KEY(org_id,role_id) REFERENCES roles(org_id,id)
);
CREATE INDEX invitations_org ON invitations(org_id,created_at);
-- +goose Down
DROP TABLE invitations;
ALTER TABLE memberships DROP CONSTRAINT membership_role, DROP COLUMN role_id, DROP COLUMN active;
DROP TABLE roles;
