-- +goose Up
ALTER TABLE organizations ADD COLUMN notification_group_seconds integer NOT NULL DEFAULT 900 CHECK(notification_group_seconds IN (0,60,300,900,3600));
ALTER TABLE organizations ADD COLUMN notification_group_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE notification_events ALTER COLUMN bucket DROP NOT NULL;
ALTER TABLE notification_events ADD COLUMN grouping_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE notification_events DROP CONSTRAINT notification_events_org_id_type_target_bucket_key;
ALTER TABLE notification_events ADD UNIQUE(org_id,type,target,grouping_revision,bucket);
-- +goose Down
-- Refuse rollback when ungrouped or policy-separated event history cannot fit the old constraint.
ALTER TABLE notification_events ALTER COLUMN bucket SET NOT NULL;
ALTER TABLE notification_events ADD UNIQUE(org_id,type,target,bucket);
ALTER TABLE notification_events DROP COLUMN grouping_revision;
ALTER TABLE organizations DROP COLUMN notification_group_revision;
ALTER TABLE organizations DROP COLUMN notification_group_seconds;
