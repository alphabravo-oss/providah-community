-- +goose Up
ALTER TABLE resources ADD COLUMN tag_metadata jsonb CHECK(tag_metadata IS NULL OR jsonb_typeof(tag_metadata)='object');
-- +goose Down
ALTER TABLE resources DROP COLUMN tag_metadata;
