-- +goose Up
CREATE TABLE provider_modules (
 org_id text NOT NULL REFERENCES organizations(id),provider text NOT NULL CHECK(provider IN ('aws','digitalocean','hetzner')),
 enabled boolean NOT NULL DEFAULT true,revision bigint NOT NULL DEFAULT 1,PRIMARY KEY(org_id,provider)
);
CREATE TRIGGER provider_module_revision AFTER INSERT OR UPDATE ON provider_modules FOR EACH ROW EXECUTE FUNCTION bump_organization_revision();
ALTER TABLE scan_jobs ADD COLUMN module_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE operations ADD COLUMN module_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE schedule_targets ADD COLUMN module_revision bigint NOT NULL DEFAULT 1;
UPDATE roles SET permissions=permissions||ARRAY['modules.read'] WHERE builtin;
UPDATE roles SET permissions=permissions||ARRAY['modules.manage'] WHERE builtin AND id='administrator';
-- +goose Down
ALTER TABLE schedule_targets DROP COLUMN module_revision;
ALTER TABLE operations DROP COLUMN module_revision;
ALTER TABLE scan_jobs DROP COLUMN module_revision;
DROP TABLE provider_modules;
UPDATE roles SET permissions=array_remove(array_remove(permissions,'modules.read'),'modules.manage') WHERE builtin;
