-- +goose Up
ALTER TABLE operations ALTER COLUMN resource_id DROP NOT NULL;
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create'));
ALTER TABLE operations ADD COLUMN creation jsonb NOT NULL DEFAULT '{}' CHECK(octet_length(creation::text)<=4096);
ALTER TABLE operations ADD CONSTRAINT operations_creation_target CHECK(resource_id IS NOT NULL OR action='create');
UPDATE roles SET permissions=permissions||ARRAY['operations.create'] WHERE id='administrator' AND builtin;
CREATE UNIQUE INDEX operation_creation_busy ON operations(org_id,connection_id,region,resource_name) WHERE action='create' AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
CREATE INDEX operations_connection_updated ON operations(org_id,connection_id,updated_at);
-- +goose Down
-- Refuse rollback while creation history exists.
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete'));
DROP INDEX operation_creation_busy;
DROP INDEX operations_connection_updated;
ALTER TABLE operations DROP CONSTRAINT operations_creation_target;
ALTER TABLE operations ALTER COLUMN resource_id SET NOT NULL;
ALTER TABLE operations DROP COLUMN creation;
UPDATE roles SET permissions=array_remove(permissions,'operations.create') WHERE builtin;
