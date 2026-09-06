-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster','network.network','network.firewall'));
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume') AND action='delete') OR (resource_kind IN ('network.network','network.firewall') AND action='delete' AND provider IN ('digitalocean','hetzner')) OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>''));
CREATE UNIQUE INDEX operation_network_busy ON operations(org_id,connection_id,resource_kind,native_id) WHERE resource_kind IN ('network.network','network.firewall') AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
-- +goose Down
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster'));
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume') AND action='delete') OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>''));
DROP INDEX operation_network_busy;
