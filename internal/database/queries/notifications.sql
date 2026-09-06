-- name: ListNotificationDestinations :many
SELECT * FROM notification_destinations WHERE org_id=$1 AND deleted_at IS NULL ORDER BY created_at,id LIMIT 101;
-- name: LockNotificationDestination :one
SELECT * FROM notification_destinations WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE;
-- name: GetNotificationDestination :one
SELECT * FROM notification_destinations WHERE org_id=$1 AND id=$2;
-- name: CreateNotificationDestination :exec
INSERT INTO notification_destinations(id,org_id,name,kind,endpoint,ciphertext,include_success) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: UpdateNotificationDestination :exec
UPDATE notification_destinations SET name=$3,endpoint=$4,ciphertext=$5,include_success=$6,revision=revision+1,verified=false,verification_hash=NULL,sender_hash=NULL,verification_expires=NULL WHERE org_id=$1 AND id=$2;
-- name: SetNotificationDestinationEnabled :exec
UPDATE notification_destinations SET enabled=$3,revision=revision+1 WHERE org_id=$1 AND id=$2;
-- name: SetNotificationChallenge :exec
UPDATE notification_destinations SET verification_hash=$3,sender_hash=$4,verification_expires=now()+interval '30 minutes',verified=false,revision=revision+1 WHERE org_id=$1 AND id=$2;
-- name: VerifyNotificationDestination :exec
UPDATE notification_destinations SET verified=true,verification_hash=NULL,sender_hash=NULL,verification_expires=NULL WHERE org_id=$1 AND id=$2;
-- name: EnqueueNotificationEvent :exec
WITH event AS (
 INSERT INTO notification_events(id,org_id,type,target,payload,bucket,grouping_revision)
 SELECT sqlc.arg(id),o.id,sqlc.arg(type),sqlc.arg(target),sqlc.arg(payload),
 CASE WHEN o.notification_group_seconds=0 THEN NULL ELSE floor(extract(epoch FROM statement_timestamp())/o.notification_group_seconds)::bigint END,o.notification_group_revision
 FROM organizations o WHERE o.id=sqlc.arg(org_id)
 ON CONFLICT(org_id,type,target,grouping_revision,bucket) DO NOTHING RETURNING *
)
INSERT INTO notification_deliveries(id,org_id,event_id,destination_id,destination_revision)
SELECT event.id||':'||d.id,event.org_id,event.id,d.id,d.revision FROM event JOIN notification_destinations d ON d.org_id=event.org_id WHERE d.enabled AND d.verified AND ((cardinality(d.event_types)=0 AND (NOT sqlc.arg(success)::boolean OR d.include_success)) OR event.type=ANY(d.event_types));
-- name: CreateDirectNotification :exec
WITH event AS (INSERT INTO notification_events(id,org_id,type,target,payload,bucket) VALUES($1,$2,$3,$4,$5,$6) RETURNING *)
INSERT INTO notification_deliveries(id,org_id,event_id,destination_id,destination_revision,verification_ciphertext)
SELECT event.id||':'||d.id,event.org_id,event.id,d.id,d.revision,sqlc.narg(verification_ciphertext)::bytea FROM event JOIN notification_destinations d ON d.org_id=event.org_id AND d.id=event.target;
-- name: ClaimNotificationDelivery :one
UPDATE notification_deliveries SET status='sending',attempts=attempts+1,retry_count=retry_count+1,lease_until=now()+interval '60 seconds',updated_at=now()
WHERE id=(SELECT id FROM notification_deliveries WHERE EXISTS(SELECT 1 FROM organizations o WHERE o.id=notification_deliveries.org_id AND o.active) AND ((status='pending' AND next_attempt<=now()) OR (status='sending' AND lease_until<now())) ORDER BY next_attempt,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *;
-- name: GetNotificationEvent :one
SELECT * FROM notification_events WHERE org_id=$1 AND id=$2;
-- name: AddNotificationAttempt :exec
INSERT INTO notification_attempts(id,org_id,delivery_id,number) VALUES($1,$2,$3,$4);
-- name: FinishNotificationAttempt :exec
UPDATE notification_attempts SET status=$3,response_code=$4,detail=$5 WHERE org_id=$1 AND id=$2;
-- name: LockNotificationDelivery :one
SELECT * FROM notification_deliveries WHERE org_id=$1 AND id=$2 FOR UPDATE;
-- name: FinishNotificationDelivery :exec
UPDATE notification_deliveries SET status=$3,detail=$4,next_attempt=$5,lease_until=NULL,updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: RedeliverNotification :exec
UPDATE notification_deliveries SET status='pending',retry_count=0,next_attempt=now(),lease_until=NULL,updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: ListNotificationDeliveries :many
SELECT d.*,e.type,n.name AS destination_name FROM notification_deliveries d JOIN notification_events e ON e.id=d.event_id JOIN notification_destinations n ON n.id=d.destination_id WHERE d.org_id=$1 AND d.id<$2 ORDER BY d.id DESC LIMIT 101;
-- name: ListNotificationAttempts :many
SELECT * FROM notification_attempts WHERE org_id=$1 AND delivery_id=$2 ORDER BY number DESC LIMIT 100;

-- name: MarkNotificationLostAttempts :exec
UPDATE notification_attempts SET status='uncertain',detail='Worker lease expired; delivery may have reached the destination.' WHERE org_id=$1 AND delivery_id=$2 AND status='sending';

-- name: SetNotificationSubscriptions :exec
UPDATE notification_destinations SET event_types=$3,revision=revision+1 WHERE org_id=$1 AND id=$2;

-- name: GetNotificationDestinationMetadata :one
SELECT id,name,kind,endpoint,enabled,verified,include_success,event_types,revision FROM notification_destinations WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL;
-- name: GetNotificationDeliveryMetadata :one
SELECT d.id,d.event_id,d.status,d.attempts,d.detail,d.updated_at,e.type,n.name AS destination_name FROM notification_deliveries d JOIN notification_events e ON e.org_id=d.org_id AND e.id=d.event_id JOIN notification_destinations n ON n.org_id=d.org_id AND n.id=d.destination_id WHERE d.org_id=$1 AND d.id=$2;

-- name: DeleteNotificationDestination :exec
UPDATE notification_destinations SET deleted_at=now(),enabled=false,verified=false,ciphertext=NULL,endpoint='',verification_hash=NULL,sender_hash=NULL,verification_expires=NULL,revision=revision+1 WHERE org_id=$1 AND id=$2;
-- name: CancelRemovedDestinationDeliveries :exec
UPDATE notification_deliveries SET status='canceled',detail='Destination removed.',lease_until=NULL,updated_at=now() WHERE org_id=$1 AND destination_id=$2 AND status='pending';
-- name: ClearDestinationChallenges :exec
UPDATE notification_deliveries SET verification_ciphertext=NULL WHERE org_id=$1 AND destination_id=$2 AND verification_ciphertext IS NOT NULL;

-- name: GetNotificationGrouping :one
SELECT notification_group_seconds,notification_group_revision FROM organizations WHERE id=$1;
-- name: SetNotificationGrouping :one
UPDATE organizations SET notification_group_seconds=$2,notification_group_revision=notification_group_revision+1 WHERE id=$1 AND notification_group_revision=$3 RETURNING notification_group_seconds,notification_group_revision;

-- name: GetOrganizationSMTPMetadata :one
SELECT (s.ciphertext IS NOT NULL)::boolean AS configured, COALESCE(s.revision,0)::bigint AS revision FROM organizations o LEFT JOIN notification_smtp s ON s.org_id=o.id WHERE o.id=$1;
-- name: GetOrganizationSMTP :one
SELECT * FROM notification_smtp WHERE org_id=$1;
-- name: SetOrganizationSMTP :one
INSERT INTO notification_smtp(org_id,ciphertext) VALUES($1,$2) ON CONFLICT(org_id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,revision=notification_smtp.revision+1 RETURNING revision;
