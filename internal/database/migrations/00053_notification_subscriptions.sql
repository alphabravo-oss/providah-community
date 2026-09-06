-- +goose Up
ALTER TABLE notification_destinations ADD COLUMN event_types text[] NOT NULL DEFAULT '{}';
ALTER TABLE notification_destinations ADD CONSTRAINT notification_event_types_bound CHECK (cardinality(event_types) <= 64);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM notification_destinations WHERE cardinality(event_types)>0) THEN
 RAISE EXCEPTION 'Reset custom notification subscriptions before rollback';
END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE notification_destinations DROP COLUMN event_types;
