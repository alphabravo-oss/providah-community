-- +goose Up
CREATE TABLE schedules (
 id text PRIMARY KEY,org_id text NOT NULL REFERENCES organizations(id),name text NOT NULL,editor_id text NOT NULL REFERENCES users(id),editor_email text NOT NULL,
 identity_id text NOT NULL UNIQUE,identity_enabled boolean NOT NULL DEFAULT true,enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1,spec jsonb NOT NULL,policy_version text NOT NULL DEFAULT 'power-v1',
 approved_revision bigint,approver_id text REFERENCES users(id),approved_at timestamptz,
 next_due timestamptz,next_action text NOT NULL DEFAULT '',next_local text NOT NULL DEFAULT '',next_skip text NOT NULL DEFAULT '',
 last_outcome text NOT NULL DEFAULT '',last_detail text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,UNIQUE(org_id,id)
);
CREATE TABLE schedule_targets (
 org_id text NOT NULL,schedule_id text NOT NULL,resource_id text NOT NULL,connection_id text NOT NULL,connection_revision bigint NOT NULL,
 PRIMARY KEY(schedule_id,resource_id),FOREIGN KEY(org_id,schedule_id) REFERENCES schedules(org_id,id),FOREIGN KEY(org_id,resource_id) REFERENCES resources(org_id,id),FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id)
);
CREATE TABLE schedule_occurrences (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,org_id text NOT NULL,schedule_id text NOT NULL,revision bigint NOT NULL,
 resource_id text NOT NULL,scheduled_for timestamptz NOT NULL,action text NOT NULL,outcome text NOT NULL,detail text NOT NULL,operation_id text REFERENCES operations(id),
 created_at timestamptz NOT NULL DEFAULT now(),FOREIGN KEY(org_id,schedule_id) REFERENCES schedules(org_id,id),UNIQUE(schedule_id,revision,resource_id,scheduled_for,action)
);
CREATE INDEX schedule_due ON schedules(next_due) WHERE deleted_at IS NULL;
CREATE INDEX schedule_occurrences_org ON schedule_occurrences(org_id,id DESC);
ALTER TABLE operations ADD COLUMN schedule_id text, ADD COLUMN schedule_revision bigint, ADD COLUMN automation_identity_id text, ADD COLUMN scheduled_for timestamptz,
 ADD CONSTRAINT operation_schedule FOREIGN KEY(org_id,schedule_id) REFERENCES schedules(org_id,id);
UPDATE roles SET permissions=permissions||ARRAY['schedules.read','schedules.manage'] WHERE id IN ('administrator','operator') AND builtin;
UPDATE roles SET permissions=permissions||ARRAY['schedules.read'] WHERE id='approver' AND builtin;
-- +goose Down
ALTER TABLE operations DROP CONSTRAINT operation_schedule,DROP COLUMN schedule_id,DROP COLUMN schedule_revision,DROP COLUMN automation_identity_id,DROP COLUMN scheduled_for;
DROP TABLE schedule_occurrences,schedule_targets,schedules;
UPDATE roles SET permissions=array_remove(array_remove(permissions,'schedules.read'),'schedules.manage') WHERE builtin;
