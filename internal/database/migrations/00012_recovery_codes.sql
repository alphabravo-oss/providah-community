-- +goose Up
CREATE TABLE recovery_codes (
 user_id text NOT NULL REFERENCES users(id),verifier bytea NOT NULL CHECK(octet_length(verifier)=32),created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,verifier)
);
CREATE TABLE account_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,user_id text NOT NULL REFERENCES users(id),action text NOT NULL,occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_event_history ON account_events(user_id,id DESC);
CREATE TRIGGER account_event_append_only BEFORE UPDATE OR DELETE ON account_events FOR EACH ROW EXECUTE FUNCTION prevent_audit_changes();
-- +goose Down
DROP TABLE account_events,recovery_codes;
