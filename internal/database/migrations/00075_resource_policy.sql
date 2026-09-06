-- +goose Up
ALTER TABLE organizations ADD COLUMN creation_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE organizations ADD COLUMN resource_policy_revision bigint NOT NULL DEFAULT 1;
-- +goose Down
ALTER TABLE organizations DROP COLUMN resource_policy_revision;
ALTER TABLE organizations DROP COLUMN creation_enabled;
