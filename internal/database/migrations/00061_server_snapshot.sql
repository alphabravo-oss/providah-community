-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create','resize','snapshot'));
-- +goose Down
-- Refuse rollback while snapshot operation history exists.
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete','create','resize'));
