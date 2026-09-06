-- +goose Up
CREATE TABLE inventory_views (
 id text PRIMARY KEY, org_id text NOT NULL REFERENCES organizations(id), user_id text NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80), spec jsonb NOT NULL,
 revision bigint NOT NULL DEFAULT 1, updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,user_id,name)
);
-- +goose Down
DROP TABLE inventory_views;
