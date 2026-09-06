-- +goose Up
ALTER TABLE operations ADD COLUMN expected_tags jsonb, ADD COLUMN target_tags jsonb;
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create','resize','snapshot','tags'));
ALTER TABLE operations ADD CONSTRAINT operation_tag_input CHECK ((action='tags' AND resource_kind='compute.server' AND expected_tags IS NOT NULL AND target_tags IS NOT NULL AND jsonb_typeof(expected_tags)='object' AND jsonb_typeof(target_tags)='object') OR (action<>'tags' AND expected_tags IS NULL AND target_tags IS NULL));
-- +goose StatementBegin
CREATE FUNCTION protect_operation_tags() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.expected_tags IS DISTINCT FROM OLD.expected_tags OR NEW.target_tags IS DISTINCT FROM OLD.target_tags THEN
  RAISE EXCEPTION 'operation tag input is immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER operation_tags_immutable BEFORE UPDATE ON operations FOR EACH ROW EXECUTE FUNCTION protect_operation_tags();
-- +goose Down
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create','resize','snapshot'));
DROP TRIGGER operation_tags_immutable ON operations;
DROP FUNCTION protect_operation_tags();
ALTER TABLE operations DROP CONSTRAINT operation_tag_input, DROP COLUMN expected_tags, DROP COLUMN target_tags;
