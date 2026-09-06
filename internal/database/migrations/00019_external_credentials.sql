-- +goose Up
ALTER TABLE connections ADD COLUMN credential_source text NOT NULL DEFAULT 'builtin' CHECK (credential_source IN ('builtin','vault_kv2'));
-- +goose Down
ALTER TABLE connections DROP COLUMN credential_source;
