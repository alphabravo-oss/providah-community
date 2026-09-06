-- +goose Up
-- User changes advance this version; scheduler clock/occurrence updates do not.
ALTER TABLE schedules ADD COLUMN write_revision bigint NOT NULL DEFAULT 1;
-- +goose Down
ALTER TABLE schedules DROP COLUMN write_revision;
