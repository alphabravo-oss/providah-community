-- +goose Up
ALTER TABLE automation_sources ADD COLUMN storage text NOT NULL DEFAULT 'database' CHECK(storage IN ('database','s3'));
COMMENT ON COLUMN automation_sources.ciphertext IS 'Encrypted source ZIP for database storage; encrypted object reference and per-object age identity for s3 storage.';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_sources WHERE storage='s3') THEN RAISE EXCEPTION 'Object-backed sources must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE automation_sources DROP COLUMN storage;
