-- +goose Up
ALTER TABLE operations ADD COLUMN trace_parent text NOT NULL DEFAULT '' CHECK (length(trace_parent)<=55);
ALTER TABLE scan_jobs ADD COLUMN trace_parent text NOT NULL DEFAULT '' CHECK (length(trace_parent)<=55);
-- +goose Down
ALTER TABLE operations DROP COLUMN trace_parent;
ALTER TABLE scan_jobs DROP COLUMN trace_parent;
