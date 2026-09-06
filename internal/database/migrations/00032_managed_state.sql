-- +goose Up
CREATE TABLE automation_projects (
 id text PRIMARY KEY,
 org_id text NOT NULL REFERENCES organizations(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 version_id text NOT NULL,
 state_id text NOT NULL DEFAULT '',
 lineage text NOT NULL DEFAULT '',
 serial bigint NOT NULL DEFAULT -1 CHECK(serial >= -1),
 sha256 text NOT NULL DEFAULT '',
 state_bytes integer NOT NULL DEFAULT 0,
 lock_id text NOT NULL DEFAULT '',
 lock_session_id text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,id), UNIQUE(org_id,name),
 FOREIGN KEY(org_id,version_id) REFERENCES automation_versions(org_id,id),
 CHECK((lock_id='')=(lock_session_id='')),
 CHECK((serial=-1 AND state_id='' AND lineage='' AND sha256='' AND state_bytes=0) OR
       (serial>=0 AND state_id<>'' AND lineage<>'' AND sha256 ~ '^[a-f0-9]{64}$' AND state_bytes>0))
);
CREATE TABLE automation_states (
 id text PRIMARY KEY,
 org_id text NOT NULL,
 project_id text NOT NULL,
 lineage text NOT NULL,
 serial bigint NOT NULL CHECK(serial>=0),
 sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 state_bytes integer NOT NULL CHECK(state_bytes BETWEEN 1 AND 33554432),
 ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(project_id,serial),
 FOREIGN KEY(org_id,project_id) REFERENCES automation_projects(org_id,id)
);
CREATE TABLE automation_state_sessions (
 id text PRIMARY KEY,
 org_id text NOT NULL,
 project_id text NOT NULL,
 requester_id text NOT NULL REFERENCES users(id),
 requester_oidc_id uuid,
 secret_hash bytea NOT NULL CHECK(octet_length(secret_hash)=32),
 writable boolean NOT NULL,
 expires_at timestamptz NOT NULL,
 revoked boolean NOT NULL DEFAULT false,
 FOREIGN KEY(org_id,project_id) REFERENCES automation_projects(org_id,id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_projects) THEN RAISE EXCEPTION 'Managed project/state history must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE automation_state_sessions;
DROP TABLE automation_states;
DROP TABLE automation_projects;
