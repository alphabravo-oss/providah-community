-- +goose Up
ALTER TABLE automation_sources ADD UNIQUE(org_id,id);
CREATE TABLE automation_versions (
 id text PRIMARY KEY,
 org_id text NOT NULL REFERENCES organizations(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 version integer NOT NULL CHECK(version>0),
 source_id text NOT NULL,
 runtime_image text NOT NULL CHECK(runtime_image ~ '^sha256:[a-f0-9]{64}$'),
 runtime_version text NOT NULL,
 runtime text NOT NULL CHECK(runtime IN ('terraform','opentofu','ansible')),
 connection_id text NOT NULL,
 connection_revision bigint NOT NULL,
 region text NOT NULL,
 review_note text NOT NULL CHECK(length(review_note) BETWEEN 3 AND 1000),
 status text NOT NULL DEFAULT 'published' CHECK(status IN ('published','retired','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,name,version),
 FOREIGN KEY(org_id,source_id) REFERENCES automation_sources(org_id,id),
 FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_versions) THEN RAISE EXCEPTION 'Published automation history must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE automation_versions;
ALTER TABLE automation_sources DROP CONSTRAINT automation_sources_org_id_id_key;
