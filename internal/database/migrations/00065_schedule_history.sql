-- +goose Up
CREATE INDEX schedule_occurrence_history ON schedule_occurrences(org_id,schedule_id,id DESC);
-- +goose Down
DROP INDEX schedule_occurrence_history;
