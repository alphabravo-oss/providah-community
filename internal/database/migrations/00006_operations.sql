-- +goose Up
ALTER TABLE resources ADD CONSTRAINT resources_org_id_key UNIQUE(org_id,id);
CREATE TABLE operations (
 id text PRIMARY KEY,org_id text NOT NULL REFERENCES organizations(id),resource_id text NOT NULL,connection_id text NOT NULL,
 connection_revision bigint NOT NULL,provider text NOT NULL,native_id text NOT NULL,region text NOT NULL,resource_name text NOT NULL,
 action text NOT NULL CHECK(action IN ('start','shutdown','restart')),expected_status text NOT NULL,reason text NOT NULL,
 requester_id text NOT NULL REFERENCES users(id),requester_email text NOT NULL,approver_id text REFERENCES users(id),
 policy_version text NOT NULL DEFAULT 'power-v1',status text NOT NULL CHECK(status IN ('awaiting_approval','queued','dispatching','observing','succeeded','failed','uncertain','rejected','canceled','expired','resolved')),
 idempotency_key text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 approval_expires_at timestamptz,expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours',lease_until timestamptz,next_attempt_at timestamptz NOT NULL DEFAULT now(),
 observation_deadline timestamptz,provider_action_id text NOT NULL DEFAULT '',observed_status text NOT NULL DEFAULT '',detail text NOT NULL DEFAULT '',
 FOREIGN KEY(org_id,resource_id) REFERENCES resources(org_id,id),FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id),UNIQUE(org_id,requester_id,idempotency_key)
);
CREATE UNIQUE INDEX operation_resource_busy ON operations(org_id,resource_id) WHERE status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
CREATE INDEX operations_claim ON operations(next_attempt_at) WHERE status IN ('queued','observing');
CREATE INDEX operations_org ON operations(org_id,created_at DESC);
UPDATE roles SET permissions=permissions||ARRAY['operations.read','operations.request','operations.approve'] WHERE id='administrator' AND builtin;
INSERT INTO roles(org_id,id,name,permissions,builtin)
SELECT id,'operator','Operator',ARRAY['connections.read','resources.read','operations.read','operations.request'],true FROM organizations;
INSERT INTO roles(org_id,id,name,permissions,builtin)
SELECT id,'approver','Approver',ARRAY['resources.read','operations.read','operations.approve'],true FROM organizations;
-- +goose Down
DELETE FROM roles WHERE id IN ('operator','approver') AND builtin;
UPDATE roles SET permissions=array_remove(array_remove(array_remove(permissions,'operations.read'),'operations.request'),'operations.approve') WHERE id='administrator' AND builtin;
DROP TABLE operations;
ALTER TABLE resources DROP CONSTRAINT resources_org_id_key;
