-- +goose Up
ALTER TABLE connections ADD COLUMN revision bigint NOT NULL DEFAULT 1,
 ADD COLUMN scan_status text NOT NULL DEFAULT 'never' CHECK(scan_status IN ('never','queued','running','succeeded','failed')),
 ADD COLUMN scan_error text NOT NULL DEFAULT '', ADD COLUMN last_scan_at timestamptz,
 ADD COLUMN next_scan_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE resources ADD COLUMN public_ip text NOT NULL DEFAULT '', ADD COLUMN private_ip text NOT NULL DEFAULT '', ADD COLUMN size text NOT NULL DEFAULT '';
CREATE TABLE scan_jobs (
 id text PRIMARY KEY, org_id text NOT NULL, connection_id text NOT NULL, revision bigint NOT NULL,
 requester_id text NOT NULL DEFAULT '', actor text NOT NULL,
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed','canceled')),
 created_at timestamptz NOT NULL DEFAULT now(), lease_until timestamptz, finished_at timestamptz,
 error text NOT NULL DEFAULT '',
 FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id)
);
CREATE UNIQUE INDEX scan_jobs_active ON scan_jobs(org_id,connection_id) WHERE status IN ('queued','running');
CREATE INDEX scan_jobs_claim ON scan_jobs(created_at) WHERE status='queued';
-- +goose Down
DROP TABLE scan_jobs;
ALTER TABLE resources DROP COLUMN public_ip,DROP COLUMN private_ip,DROP COLUMN size;
ALTER TABLE connections DROP COLUMN revision,DROP COLUMN scan_status,DROP COLUMN scan_error,DROP COLUMN last_scan_at,DROP COLUMN next_scan_at;
