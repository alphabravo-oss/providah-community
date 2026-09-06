-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster','network.network','network.firewall','access.ssh_key','database.snapshot','database.cluster_snapshot'));
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume','access.ssh_key') AND action='delete') OR (resource_kind IN ('network.network','network.firewall') AND action='delete' AND provider IN ('digitalocean','hetzner')) OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>'') OR (resource_kind='network.firewall' AND provider='aws' AND action='delete') OR (resource_kind IN ('database.snapshot','database.cluster_snapshot') AND provider='aws' AND action='delete'));
CREATE UNIQUE INDEX operation_database_snapshot_busy ON operations(org_id,connection_id,resource_kind,region,native_id) WHERE resource_kind IN ('database.snapshot','database.cluster_snapshot') AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
-- +goose Down
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster','network.network','network.firewall','access.ssh_key'));
DROP INDEX operation_database_snapshot_busy;
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume','access.ssh_key') AND action='delete') OR (resource_kind IN ('network.network','network.firewall') AND action='delete' AND provider IN ('digitalocean','hetzner')) OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>'') OR (resource_kind='network.firewall' AND provider='aws' AND action='delete'));
