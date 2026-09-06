-- +goose Up
ALTER TABLE operations ADD COLUMN resource_kind text NOT NULL DEFAULT 'compute.server' CHECK(resource_kind IN ('compute.server','storage.snapshot'));
ALTER TABLE operations ADD CONSTRAINT snapshot_delete_only CHECK(resource_kind='compute.server' OR action='delete');
CREATE UNIQUE INDEX operation_snapshot_busy ON operations(org_id,connection_id,native_id) WHERE resource_kind='storage.snapshot' AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
-- +goose Down
DROP INDEX operation_snapshot_busy;
ALTER TABLE operations DROP CONSTRAINT snapshot_delete_only;
ALTER TABLE operations DROP COLUMN resource_kind;
