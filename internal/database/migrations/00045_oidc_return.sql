-- +goose Up
ALTER TABLE oidc_flows ADD COLUMN return_to text NOT NULL DEFAULT '/' CHECK(octet_length(return_to)<=2048);
-- +goose Down
ALTER TABLE oidc_flows DROP COLUMN return_to;
