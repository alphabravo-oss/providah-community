-- +goose Up
CREATE TABLE state_ownership_scans (
 state_id text PRIMARY KEY REFERENCES automation_states(id),
 status text NOT NULL CHECK(status IN ('complete','partial','invalid')),
 unsupported integer NOT NULL DEFAULT 0,
 scanned_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE resource_ownership_claims (
 org_id text NOT NULL,
 connection_id text NOT NULL,
 project_id text NOT NULL,
 provider text NOT NULL,
 kind text NOT NULL,
 region text NOT NULL,
 native_id text NOT NULL,
 first_state_id text NOT NULL REFERENCES automation_states(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(org_id,connection_id,project_id,kind,region,native_id),
 FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id),
 FOREIGN KEY(org_id,project_id) REFERENCES automation_projects(org_id,id)
);
CREATE INDEX resource_ownership_lookup ON resource_ownership_claims(org_id,connection_id,kind,native_id);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM resource_ownership_claims) THEN RAISE EXCEPTION 'Ownership protections must be preserved'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE resource_ownership_claims,state_ownership_scans;
