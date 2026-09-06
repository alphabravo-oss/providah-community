-- +goose Up
CREATE TABLE organizations (id text PRIMARY KEY, name text NOT NULL CHECK(length(name) BETWEEN 1 AND 120), active boolean NOT NULL DEFAULT true);
CREATE TABLE users (id text PRIMARY KEY, email text NOT NULL UNIQUE, password_hash bytea NOT NULL, totp_ciphertext bytea NOT NULL, last_totp_step bigint NOT NULL DEFAULT 0, active boolean NOT NULL DEFAULT true);
CREATE TABLE memberships (org_id text NOT NULL REFERENCES organizations(id), user_id text NOT NULL REFERENCES users(id), permissions text[] NOT NULL, PRIMARY KEY(org_id,user_id));
CREATE TABLE sessions (id text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), refresh_hash bytea NOT NULL UNIQUE, expires_at timestamptz NOT NULL, mfa_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE setup_pending (id boolean PRIMARY KEY DEFAULT true CHECK(id), ciphertext bytea NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE auth_limits (key text PRIMARY KEY, window_at timestamptz NOT NULL, attempts integer NOT NULL);
CREATE TABLE connections (id text PRIMARY KEY, org_id text NOT NULL REFERENCES organizations(id), name text NOT NULL CHECK(length(name) BETWEEN 1 AND 120), provider text NOT NULL CHECK(provider IN ('aws','digitalocean','hetzner')), region text NOT NULL DEFAULT '', enabled boolean NOT NULL DEFAULT true, key_id text NOT NULL, algorithm text NOT NULL DEFAULT 'age-x25519', ciphertext bytea NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz, UNIQUE(org_id,id));
CREATE INDEX connections_org ON connections(org_id,created_at,id) WHERE deleted_at IS NULL;
CREATE TABLE resources (id text PRIMARY KEY, org_id text NOT NULL, connection_id text NOT NULL, native_id text NOT NULL, name text NOT NULL, provider text NOT NULL, kind text NOT NULL, region text NOT NULL, status text NOT NULL, observed_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz, FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id), UNIQUE(org_id,connection_id,kind,region,native_id));
CREATE INDEX resources_org ON resources(org_id,name,id) WHERE deleted_at IS NULL;
CREATE TABLE audit_events (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, org_id text NOT NULL REFERENCES organizations(id), actor text NOT NULL, action text NOT NULL, target text NOT NULL, details jsonb NOT NULL DEFAULT '{}', occurred_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX audit_org ON audit_events(org_id,id DESC);
-- +goose Down
DROP TABLE audit_events, resources, connections, auth_limits, setup_pending, sessions, memberships, users, organizations;
