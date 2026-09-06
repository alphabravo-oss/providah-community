-- +goose Up
ALTER TABLE operations ADD COLUMN expected_size text NOT NULL DEFAULT '', ADD COLUMN target_size text NOT NULL DEFAULT '';
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create','resize'));
ALTER TABLE operations ADD CONSTRAINT operations_resize_input CHECK((action='resize' AND resource_kind='compute.server' AND expected_size ~ '^[a-z][a-z0-9.-]{0,63}$' AND target_size ~ '^[a-z][a-z0-9.-]{0,63}$' AND expected_size<>target_size) OR (action<>'resize' AND expected_size='' AND target_size=''));
-- +goose Down
-- Refuse rollback while resize history exists.
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create'));
ALTER TABLE operations DROP CONSTRAINT operations_resize_input;
ALTER TABLE operations DROP COLUMN expected_size, DROP COLUMN target_size;
