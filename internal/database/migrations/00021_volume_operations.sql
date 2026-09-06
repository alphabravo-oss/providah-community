-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume'));
CREATE UNIQUE INDEX operation_volume_busy ON operations(org_id,connection_id,native_id) WHERE resource_kind='storage.volume' AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
-- +goose Down
DROP INDEX operation_volume_busy;
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot'));
