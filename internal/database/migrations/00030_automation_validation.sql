-- +goose Up
ALTER TABLE automation_versions ADD UNIQUE(org_id,id);
CREATE TABLE automation_validations (
 id text PRIMARY KEY,
 org_id text NOT NULL,
 version_id text NOT NULL,
 requester_id text NOT NULL REFERENCES users(id),
 requester_email text NOT NULL,
 requester_oidc_id uuid,
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed','canceled')),
 detail text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz,
 FOREIGN KEY(org_id,version_id) REFERENCES automation_versions(org_id,id)
);
CREATE UNIQUE INDEX automation_validation_active ON automation_validations(org_id,version_id) WHERE status IN ('queued','running');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_validations) THEN RAISE EXCEPTION 'Validation history must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE automation_validations;
ALTER TABLE automation_versions DROP CONSTRAINT automation_versions_org_id_id_key;
