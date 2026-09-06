-- +goose Up
ALTER TABLE notification_destinations ADD COLUMN deleted_at timestamptz;
ALTER TABLE notification_destinations ALTER COLUMN ciphertext DROP NOT NULL;
ALTER TABLE notification_destinations ADD CONSTRAINT notification_removed_secrets CHECK (
 (deleted_at IS NULL AND ciphertext IS NOT NULL) OR
 (deleted_at IS NOT NULL AND ciphertext IS NULL AND NOT enabled AND NOT verified)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
IF EXISTS(SELECT 1 FROM notification_destinations WHERE deleted_at IS NOT NULL) THEN
 RAISE EXCEPTION 'Removed notification destinations prevent rollback';
END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE notification_destinations DROP CONSTRAINT notification_removed_secrets;
ALTER TABLE notification_destinations ALTER COLUMN ciphertext SET NOT NULL;
ALTER TABLE notification_destinations DROP COLUMN deleted_at;
