-- +goose Up
-- Retain deleted team IDs: losing the last team must never broaden a dashboard's audience.
ALTER TABLE dashboards ADD COLUMN team_ids text[] NOT NULL DEFAULT '{}' CHECK(cardinality(team_ids)<=20 AND (NOT shared OR cardinality(team_ids)=0));
DROP INDEX dashboards_shared_name;
CREATE UNIQUE INDEX dashboards_shared_name ON dashboards(org_id,name) WHERE shared OR cardinality(team_ids)>0;
-- +goose Down
-- Refuse to silently discard team audiences during rollback.
ALTER TABLE dashboards ADD CONSTRAINT dashboards_no_team_audiences CHECK(cardinality(team_ids)=0);
DROP INDEX dashboards_shared_name;
CREATE UNIQUE INDEX dashboards_shared_name ON dashboards(org_id,name) WHERE shared;
ALTER TABLE dashboards DROP COLUMN team_ids;
