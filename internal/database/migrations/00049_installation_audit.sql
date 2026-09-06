-- +goose Up
ALTER TABLE installation_events ADD COLUMN actor_email text NOT NULL DEFAULT '';
UPDATE installation_events e SET actor_email=u.email FROM users u WHERE u.id=e.actor_id;
ALTER TABLE installation_events ALTER COLUMN target_id DROP NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION installation_actor_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.actor_email := (SELECT email FROM users WHERE id=NEW.actor_id);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER installation_actor_snapshot BEFORE INSERT ON installation_events FOR EACH ROW EXECUTE FUNCTION installation_actor_snapshot();
CREATE TRIGGER installation_append_only BEFORE UPDATE OR DELETE ON installation_events FOR EACH ROW EXECUTE FUNCTION prevent_audit_changes();
CREATE TRIGGER installation_no_truncate BEFORE TRUNCATE ON installation_events FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_changes();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON audit_events FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_changes();
-- +goose Down
DROP TRIGGER audit_no_truncate ON audit_events;
DROP TRIGGER installation_no_truncate ON installation_events;
DROP TRIGGER installation_append_only ON installation_events;
DROP TRIGGER installation_actor_snapshot ON installation_events;
DROP FUNCTION installation_actor_snapshot();
ALTER TABLE installation_events DROP COLUMN actor_email;
-- Refuse to discard installation-wide policy history when rolling back.
ALTER TABLE installation_events ALTER COLUMN target_id SET NOT NULL;
