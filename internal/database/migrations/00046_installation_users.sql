-- +goose Up
ALTER TABLE users ADD COLUMN admin_revision bigint NOT NULL DEFAULT 1;
CREATE TABLE installation_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id text NOT NULL REFERENCES users(id),
 target_id text NOT NULL REFERENCES users(id),
 action text NOT NULL,
 details jsonb NOT NULL,
 occurred_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down
DROP TABLE installation_events;
ALTER TABLE users DROP COLUMN admin_revision;
