-- +goose Up
CREATE TABLE notification_smtp (
 org_id text PRIMARY KEY REFERENCES organizations(id),
 ciphertext bytea,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM notification_smtp WHERE ciphertext IS NOT NULL) THEN
  RAISE EXCEPTION 'Remove organization SMTP profiles before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE notification_smtp;
