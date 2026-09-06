-- +goose Up
ALTER TABLE organizations ADD COLUMN maintenance_revision bigint NOT NULL DEFAULT 0;
ALTER TABLE operations ADD COLUMN maintenance_revision bigint NOT NULL DEFAULT 0, ADD COLUMN maintenance_exception_reason text NOT NULL DEFAULT '', ADD COLUMN maintenance_expires_at timestamptz;
ALTER TABLE schedules ADD COLUMN maintenance_revision bigint NOT NULL DEFAULT 0;
CREATE TABLE maintenance_policies (
 id text PRIMARY KEY,org_id text NOT NULL REFERENCES organizations(id),name text NOT NULL,spec jsonb NOT NULL,duration_minutes int NOT NULL CHECK(duration_minutes BETWEEN 1 AND 1440),created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(org_id,id)
);
CREATE TABLE maintenance_connections (
 org_id text NOT NULL,policy_id text NOT NULL,connection_id text NOT NULL,
 PRIMARY KEY(policy_id,connection_id),FOREIGN KEY(org_id,policy_id) REFERENCES maintenance_policies(org_id,id) ON DELETE CASCADE,FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id)
);
UPDATE roles SET permissions=permissions||ARRAY['maintenance.read','maintenance.manage','maintenance.override'] WHERE builtin AND id='administrator';
UPDATE roles SET permissions=permissions||ARRAY['maintenance.read'] WHERE builtin AND id IN ('operator','connection-manager','auditor');
UPDATE roles SET permissions=permissions||ARRAY['maintenance.read','maintenance.override'] WHERE builtin AND id='approver';
-- +goose Down
DROP TABLE maintenance_connections,maintenance_policies;
ALTER TABLE schedules DROP COLUMN maintenance_revision;
ALTER TABLE operations DROP COLUMN maintenance_revision,DROP COLUMN maintenance_exception_reason,DROP COLUMN maintenance_expires_at;
ALTER TABLE organizations DROP COLUMN maintenance_revision;
UPDATE roles SET permissions=array_remove(array_remove(array_remove(permissions,'maintenance.read'),'maintenance.manage'),'maintenance.override') WHERE builtin;
