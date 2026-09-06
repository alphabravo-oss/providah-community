-- +goose Up
ALTER TABLE dashboards ADD COLUMN shared boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX dashboards_shared_name ON dashboards(org_id,name) WHERE shared;
UPDATE roles SET permissions=array_append(permissions,'dashboards.manage'),revision=revision+1 WHERE builtin AND id='administrator' AND NOT ('dashboards.manage'=ANY(permissions));
-- +goose Down
UPDATE roles SET permissions=array_remove(permissions,'dashboards.manage'),revision=revision+1 WHERE 'dashboards.manage'=ANY(permissions);
UPDATE memberships SET permissions=array_remove(permissions,'dashboards.manage') WHERE 'dashboards.manage'=ANY(permissions);
DROP INDEX dashboards_shared_name;
ALTER TABLE dashboards DROP COLUMN shared;
