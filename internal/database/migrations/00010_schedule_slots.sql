-- +goose Up
ALTER TABLE schedules ADD COLUMN clock_version smallint NOT NULL DEFAULT 1;
ALTER TABLE schedules ALTER COLUMN clock_version SET DEFAULT 2;
ALTER TABLE schedule_occurrences ADD COLUMN local_time text NOT NULL DEFAULT '';
ALTER TABLE schedule_occurrences DROP CONSTRAINT schedule_occurrences_schedule_id_revision_resource_id_sched_key;
ALTER TABLE schedule_occurrences ADD CONSTRAINT schedule_occurrence_slot UNIQUE(schedule_id,revision,resource_id,scheduled_for,action,local_time);
-- +goose Down
ALTER TABLE schedules DROP COLUMN clock_version;
-- Refuse a lossy rollback once distinct local slots share a UTC instant.
ALTER TABLE schedule_occurrences ADD CONSTRAINT schedule_occurrences_schedule_id_revision_resource_id_sched_key UNIQUE(schedule_id,revision,resource_id,scheduled_for,action);
ALTER TABLE schedule_occurrences DROP CONSTRAINT schedule_occurrence_slot,DROP COLUMN local_time;
