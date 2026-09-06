-- name: CreateSchedule :exec
INSERT INTO schedules(id,org_id,name,editor_id,editor_email,identity_id,spec,next_due,next_action,next_local,next_skip) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);
-- name: LockSchedule :one
SELECT * FROM schedules WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE;
-- name: GetSchedule :one
SELECT * FROM schedules WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL;
-- name: ListSchedules :many
SELECT * FROM schedules WHERE org_id=$1 AND deleted_at IS NULL ORDER BY name,id LIMIT 1001;
-- name: ListScheduleTargets :many
SELECT t.*,r.name,r.native_id,r.provider,r.region,r.status,r.kind,r.observed_at,r.deleted_at FROM schedule_targets t JOIN resources r ON r.org_id=t.org_id AND r.id=t.resource_id WHERE t.org_id=$1 AND t.schedule_id=$2 ORDER BY t.connection_id,t.resource_id;
-- name: ClearScheduleTargets :exec
DELETE FROM schedule_targets WHERE org_id=$1 AND schedule_id=$2;
-- name: AddScheduleTarget :exec
INSERT INTO schedule_targets(org_id,schedule_id,resource_id,connection_id,connection_revision,module_revision) VALUES($1,$2,$3,$4,$5,coalesce((SELECT m.revision FROM provider_modules m JOIN connections c ON c.org_id=m.org_id AND c.provider=m.provider WHERE c.org_id=$1 AND c.id=$4),1));
-- name: UpdateSchedule :exec
UPDATE schedules SET write_revision=write_revision+1,clock_version=2,name=$3,editor_id=$4,editor_email=$5,spec=$6,revision=revision+1,approved_revision=NULL,approver_id=NULL,approved_at=NULL,next_due=$7,next_action=$8,next_local=$9,next_skip=$10,updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: SetScheduleEnabled :exec
UPDATE schedules SET write_revision=write_revision+1,enabled=$3,identity_enabled=$4,updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: ApproveSchedule :exec
UPDATE schedules SET write_revision=write_revision+1,approved_revision=revision,approver_id=$3,maintenance_revision=$4,approver_oidc_id=sqlc.narg(oidc_id)::uuid,approved_at=now(),updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: ClaimDueSchedule :one
SELECT * FROM schedules WHERE deleted_at IS NULL AND (next_due<=now() OR (clock_version<2 AND next_due IS NOT NULL)) ORDER BY next_due,id FOR UPDATE SKIP LOCKED LIMIT 1;
-- name: AdvanceSchedule :exec
UPDATE schedules SET clock_version=2,next_due=$3,next_action=$4,next_local=$5,next_skip=$6,last_outcome=$7,last_detail=$8,updated_at=now() WHERE org_id=$1 AND id=$2;
-- name: AddScheduleOccurrence :exec
INSERT INTO schedule_occurrences(org_id,schedule_id,revision,resource_id,scheduled_for,action,outcome,detail,operation_id,local_time) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING;
-- name: ListScheduleOccurrences :many
SELECT x.*,coalesce(o.status,'')::text AS operation_status,r.name AS resource_name FROM schedule_occurrences x JOIN resources r ON r.org_id=x.org_id AND r.id=x.resource_id LEFT JOIN operations o ON o.org_id=x.org_id AND o.id=x.operation_id WHERE x.org_id=$1 AND x.id<$2 AND (sqlc.arg(schedule_id)::text='' OR x.schedule_id=sqlc.arg(schedule_id)) AND (sqlc.arg(outcome)::text='' OR x.outcome=sqlc.arg(outcome)) ORDER BY x.id DESC LIMIT 101;
-- name: ResourceOperationBusy :one
SELECT EXISTS(SELECT 1 FROM operations WHERE org_id=$1 AND resource_id=$2 AND status IN ('awaiting_approval','queued','dispatching','observing','uncertain'));
-- name: BindScheduledOperation :exec
UPDATE operations SET schedule_id=$3,schedule_revision=$4,automation_identity_id=$5,scheduled_for=$6,approver_id=$7,approval_expires_at=$8,expires_at=$8 WHERE org_id=$1 AND id=$2;
-- name: ScheduleAuthorizesOperation :one
SELECT EXISTS(SELECT 1 FROM schedules s JOIN schedule_targets t ON t.org_id=s.org_id AND t.schedule_id=s.id WHERE s.org_id=$1 AND s.id=$2 AND s.revision=$3 AND s.identity_id=$4 AND t.resource_id=$5 AND t.connection_revision=$6 AND s.deleted_at IS NULL AND s.enabled AND s.identity_enabled AND s.approved_revision=s.revision AND s.maintenance_revision=(SELECT maintenance_revision FROM organizations WHERE id=s.org_id) AND identity_allows(s.org_id,s.editor_id,s.editor_oidc_id) AND identity_allows(s.org_id,s.approver_id,s.approver_oidc_id) AND s.policy_version='power-v1' AND (s.spec->>'action'=sqlc.arg(action)::text OR (s.spec->>'action'='window' AND sqlc.arg(action)::text IN ('start','shutdown'))));

-- name: DeleteSchedule :exec
UPDATE schedules SET write_revision=write_revision+1,enabled=false,identity_enabled=false,deleted_at=now(),updated_at=now() WHERE org_id=$1 AND id=$2;

-- name: RefreshScheduleClock :exec
UPDATE schedules SET clock_version=2,next_due=$3,next_action=$4,next_local=$5,next_skip=$6,updated_at=now() WHERE org_id=$1 AND id=$2;
