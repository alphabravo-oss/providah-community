-- +goose Up
ALTER TABLE state_ownership_scans ADD COLUMN scanner_version integer NOT NULL DEFAULT 1 CHECK(scanner_version > 0);
-- +goose Down
ALTER TABLE state_ownership_scans DROP COLUMN scanner_version;
