-- +goose Up
ALTER TABLE organizations ADD COLUMN admin_revision bigint NOT NULL DEFAULT 1;
-- +goose Down
ALTER TABLE organizations DROP COLUMN admin_revision;
