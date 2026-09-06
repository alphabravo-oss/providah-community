-- +goose Up
CREATE TABLE automation_sources (
 id text PRIMARY KEY,
 org_id text NOT NULL REFERENCES organizations(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 runtime text NOT NULL CHECK(runtime IN ('terraform','opentofu','ansible')),
 entrypoint text NOT NULL,
 sha256 text NOT NULL CHECK(length(sha256)=64),
 files text[] NOT NULL,
 archive_bytes integer NOT NULL CHECK(archive_bytes BETWEEN 1 AND 4194304),
 expanded_bytes bigint NOT NULL CHECK(expanded_bytes BETWEEN 0 AND 16777216),
 ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,runtime,entrypoint,sha256)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_sources) THEN RAISE EXCEPTION 'Imported source history must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE automation_sources;
