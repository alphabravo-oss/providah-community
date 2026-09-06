-- +goose Up
ALTER TABLE provider_modules ADD COLUMN runtime_id text NOT NULL DEFAULT '';
ALTER TABLE scan_jobs ADD COLUMN runtime_id text NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN runtime_id text NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE operations DROP COLUMN runtime_id;
ALTER TABLE scan_jobs DROP COLUMN runtime_id;
ALTER TABLE provider_modules DROP COLUMN runtime_id;
