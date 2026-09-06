-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster','network.network','network.firewall','access.ssh_key'));
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume','access.ssh_key') AND action='delete') OR (resource_kind IN ('network.network','network.firewall') AND action='delete' AND provider IN ('digitalocean','hetzner')) OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>''));
CREATE UNIQUE INDEX operation_ssh_key_busy ON operations(org_id,connection_id,native_id,(CASE WHEN provider='aws' THEN region ELSE '' END)) WHERE resource_kind='access.ssh_key' AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain');
-- +goose Down
ALTER TABLE operations DROP CONSTRAINT operations_resource_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_resource_kind_check CHECK(resource_kind IN ('compute.server','storage.snapshot','storage.volume','database.instance','database.cluster','network.network','network.firewall'));
ALTER TABLE operations DROP CONSTRAINT operation_kind_action;
ALTER TABLE operations ADD CONSTRAINT operation_kind_action CHECK(resource_kind='compute.server' OR (resource_kind IN ('storage.snapshot','storage.volume') AND action='delete') OR (resource_kind IN ('network.network','network.firewall') AND action='delete' AND provider IN ('digitalocean','hetzner')) OR (resource_kind IN ('database.instance','database.cluster') AND provider='aws' AND action IN ('start','shutdown') AND provider_identity<>''));
DROP INDEX operation_ssh_key_busy;
