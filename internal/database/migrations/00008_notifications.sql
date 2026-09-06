-- +goose Up
CREATE TABLE notification_destinations (
 id text PRIMARY KEY,org_id text NOT NULL REFERENCES organizations(id),name text NOT NULL,kind text NOT NULL CHECK(kind IN ('webhook','email')),endpoint text NOT NULL,
 ciphertext bytea NOT NULL,revision bigint NOT NULL DEFAULT 1,enabled boolean NOT NULL DEFAULT true,verified boolean NOT NULL DEFAULT false,include_success boolean NOT NULL DEFAULT false,
 verification_hash bytea, sender_hash bytea,verification_expires timestamptz,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(org_id,id)
);
CREATE TABLE notification_events (
 id text PRIMARY KEY,org_id text NOT NULL REFERENCES organizations(id),type text NOT NULL,target text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),bucket bigint NOT NULL,
 UNIQUE(org_id,type,target,bucket),UNIQUE(org_id,id)
);
CREATE TABLE notification_deliveries (
 id text PRIMARY KEY,org_id text NOT NULL,event_id text NOT NULL,destination_id text NOT NULL,destination_revision bigint NOT NULL,
 verification_ciphertext bytea,status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','delivered','dead','canceled')),
 attempts int NOT NULL DEFAULT 0,retry_count int NOT NULL DEFAULT 0,next_attempt timestamptz NOT NULL DEFAULT now(),lease_until timestamptz,detail text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(org_id,event_id) REFERENCES notification_events(org_id,id),FOREIGN KEY(org_id,destination_id) REFERENCES notification_destinations(org_id,id),UNIQUE(event_id,destination_id),UNIQUE(org_id,id)
);
CREATE INDEX notification_pending ON notification_deliveries(next_attempt) WHERE status IN ('pending','sending');
CREATE INDEX notification_history ON notification_deliveries(org_id,created_at DESC,id);
CREATE TABLE notification_attempts (
 id text PRIMARY KEY,org_id text NOT NULL,delivery_id text NOT NULL,number int NOT NULL,status text NOT NULL DEFAULT 'sending',response_code int NOT NULL DEFAULT 0,detail text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(org_id,delivery_id) REFERENCES notification_deliveries(org_id,id),UNIQUE(delivery_id,number)
);
UPDATE roles SET permissions=permissions||ARRAY['notifications.read','notifications.manage'] WHERE builtin AND id='administrator';
UPDATE roles SET permissions=permissions||ARRAY['notifications.read'] WHERE builtin AND id IN ('operator','approver','auditor');
-- +goose Down
DROP TABLE notification_attempts,notification_deliveries,notification_events,notification_destinations;
UPDATE roles SET permissions=array_remove(array_remove(permissions,'notifications.read'),'notifications.manage') WHERE builtin;
