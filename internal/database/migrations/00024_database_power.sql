-- +goose Up
ALTER TABLE resources ADD COLUMN provider_identity text NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN provider_identity text NOT NULL DEFAULT '';
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster'));
ALTER TABLE operations DROP CONSTRAINT snapshot_delete_only;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume') AND action='delete') OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>''));
-- +goose Down
-- Refuse rollback while database operation history exists.
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume'));
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT snapshot_delete_only CHECK(resource_kind='compute.server' OR action='delete');
ALTER TABLE operations DROP COLUMN provider_identity;
ALTER TABLE resources DROP COLUMN provider_identity;
