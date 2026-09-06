-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION prevent_audit_changes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit events are append-only';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION prevent_audit_changes();
-- +goose Down
DROP TRIGGER audit_append_only ON audit_events;
DROP FUNCTION prevent_audit_changes();
