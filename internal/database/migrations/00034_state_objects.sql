-- +goose Up
ALTER TABLE automation_states ADD COLUMN storage text NOT NULL DEFAULT 'database' CHECK(storage IN ('database','s3'));
COMMENT ON COLUMN automation_states.ciphertext IS 'Encrypted state bytes for database storage; encrypted object reference and per-object age identity for s3 storage.';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_states WHERE storage='s3') THEN RAISE EXCEPTION 'Object-backed state must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE automation_states DROP COLUMN storage;
