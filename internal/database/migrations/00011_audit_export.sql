-- +goose Up
CREATE TABLE audit_exports (
 org_id text PRIMARY KEY REFERENCES organizations(id),endpoint text NOT NULL,region text NOT NULL,bucket text NOT NULL,ciphertext bytea NOT NULL,
 revision bigint NOT NULL DEFAULT 1,enabled boolean NOT NULL DEFAULT false,verified boolean NOT NULL DEFAULT false,cursor_id bigint NOT NULL DEFAULT 0,
 next_batch_at timestamptz NOT NULL DEFAULT now(),detail text NOT NULL DEFAULT '',updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE audit_export_batches (
 id text PRIMARY KEY,org_id text NOT NULL REFERENCES audit_exports(org_id),revision bigint NOT NULL,kind text NOT NULL CHECK(kind IN ('test','audit')),
 first_id bigint NOT NULL,last_id bigint NOT NULL,event_count int NOT NULL,payload bytea NOT NULL,checksum text NOT NULL,object_key text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','succeeded','canceled')),attempts int NOT NULL DEFAULT 0,
 next_attempt timestamptz NOT NULL DEFAULT now(),lease_until timestamptz,detail text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(org_id,id)
);
CREATE UNIQUE INDEX audit_export_active ON audit_export_batches(org_id) WHERE status IN ('pending','running');
CREATE INDEX audit_export_due ON audit_export_batches(next_attempt) WHERE status IN ('pending','running');
CREATE INDEX audit_export_history ON audit_export_batches(org_id,id DESC);
CREATE TRIGGER audit_export_revision AFTER INSERT OR UPDATE OF enabled,verified,cursor_id,detail,ciphertext ON audit_exports FOR EACH ROW EXECUTE FUNCTION bump_organization_revision();
UPDATE roles SET permissions=permissions||ARRAY['audit.export.manage'] WHERE builtin AND id='administrator';
-- +goose Down
DROP TABLE audit_export_batches,audit_exports;
UPDATE roles SET permissions=array_remove(permissions,'audit.export.manage') WHERE builtin;
