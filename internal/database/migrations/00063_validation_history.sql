-- +goose Up
CREATE INDEX automation_validation_history ON automation_validations(org_id,created_at DESC,id DESC);
CREATE INDEX automation_validation_status_history ON automation_validations(org_id,status,created_at DESC,id DESC);
-- +goose Down
DROP INDEX automation_validation_status_history;
DROP INDEX automation_validation_history;
