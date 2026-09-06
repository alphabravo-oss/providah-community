-- +goose Up
ALTER TABLE automation_sources ADD COLUMN input_schema jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(input_schema)='array');
ALTER TABLE automation_projects ADD COLUMN inputs_ciphertext bytea;
ALTER TABLE automation_projects ADD COLUMN inputs_hash text NOT NULL DEFAULT '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a' CHECK(inputs_hash ~ '^[a-f0-9]{64}$');
ALTER TABLE automation_validations ADD COLUMN project_id text NOT NULL DEFAULT '';
DROP INDEX automation_validation_active;
CREATE UNIQUE INDEX automation_validation_active ON automation_validations(org_id,version_id,project_id) WHERE status IN ('queued','running');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_projects WHERE inputs_ciphertext IS NOT NULL) OR EXISTS(SELECT 1 FROM automation_sources WHERE input_schema<>'[]') OR EXISTS(SELECT 1 FROM automation_validations WHERE project_id<>'') THEN RAISE EXCEPTION 'Pinned automation inputs must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
DROP INDEX automation_validation_active;
ALTER TABLE automation_validations DROP COLUMN project_id;
CREATE UNIQUE INDEX automation_validation_active ON automation_validations(org_id,version_id) WHERE status IN ('queued','running');
ALTER TABLE automation_projects DROP COLUMN inputs_ciphertext, DROP COLUMN inputs_hash;
ALTER TABLE automation_sources DROP COLUMN input_schema;
