-- +goose Up
CREATE TABLE oidc_links (
 issuer text NOT NULL CHECK(octet_length(issuer)<=2048),subject text NOT NULL CHECK(octet_length(subject)<=512),
 user_id text NOT NULL REFERENCES users(id),created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(issuer,subject),UNIQUE(user_id,issuer)
);
CREATE TABLE oidc_flows (
 id text PRIMARY KEY,cookie_hash bytea NOT NULL CHECK(octet_length(cookie_hash)=32),provider_key text NOT NULL,
 user_id text REFERENCES users(id),session_id text REFERENCES sessions(id) ON DELETE CASCADE,
 mode text NOT NULL CHECK(mode IN ('login','link','verified')),subject text NOT NULL DEFAULT '',
 claimed boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '5 minutes'
);
CREATE INDEX oidc_flow_expiry ON oidc_flows(expires_at);
-- +goose Down
DROP TABLE oidc_flows,oidc_links;
