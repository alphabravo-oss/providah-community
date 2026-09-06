-- +goose Up
ALTER TABLE installation_events ALTER COLUMN actor_id DROP NOT NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION installation_actor_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.actor_email := CASE WHEN NEW.actor_id IS NULL THEN 'system:runtime-admission' ELSE (SELECT email FROM users WHERE id=NEW.actor_id) END;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE INDEX installation_runtime_catalog_latest ON installation_events(id DESC) WHERE action='runtime.catalog_admitted';
-- +goose Down
-- Refuse rollback while system admission history exists.
ALTER TABLE installation_events ALTER COLUMN actor_id SET NOT NULL;
DROP INDEX installation_runtime_catalog_latest;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION installation_actor_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.actor_email := (SELECT email FROM users WHERE id=NEW.actor_id);
 RETURN NEW;
END $$;
-- +goose StatementEnd
