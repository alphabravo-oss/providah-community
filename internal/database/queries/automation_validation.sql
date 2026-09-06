-- name: AutomationValidationSource :one
SELECT v.*,s.ciphertext,s.storage,s.entrypoint,s.sha256,s.input_schema FROM automation_versions v JOIN automation_sources s ON s.org_id=v.org_id AND s.id=v.source_id WHERE v.org_id=$1 AND v.id=$2 FOR SHARE OF v,s;
-- name: QueueAutomationValidation :one
INSERT INTO automation_validations(id,org_id,version_id,requester_id,requester_email,requester_oidc_id,project_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(org_id,version_id,project_id) WHERE status IN ('queued','running') DO NOTHING RETURNING *;
-- name: ActiveAutomationValidation :one
SELECT * FROM automation_validations WHERE org_id=$1 AND version_id=$2 AND project_id=$3 AND status IN ('queued','running');
-- name: ListAutomationValidations :many
SELECT * FROM automation_validations WHERE org_id=$1 AND (sqlc.arg(status)::text='' OR status=sqlc.arg(status)) AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(before_at)::timestamptz,sqlc.arg(before_id)::text)) ORDER BY created_at DESC,id DESC LIMIT 101;
-- name: ClaimAutomationValidation :one
UPDATE automation_validations SET status='running',lease_until=now()+interval '3 minutes',updated_at=now() WHERE id=(SELECT id FROM automation_validations WHERE status='queued' AND created_at>now()-interval '30 minutes' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *;
-- name: LockAutomationValidation :one
SELECT * FROM automation_validations WHERE id=$1 AND status='running' AND lease_until>now() FOR UPDATE;
-- name: FinishAutomationValidation :exec
UPDATE automation_validations SET status=$2,detail=$3,updated_at=now() WHERE id=$1 AND status='running' AND lease_until>now();
-- name: ExpireAutomationValidations :many
UPDATE automation_validations SET status='failed',detail='Validation expired or was interrupted. Request a new check.',updated_at=now() WHERE (status='running' AND lease_until<=now()) OR (status='queued' AND created_at<=now()-interval '30 minutes') RETURNING *;
-- name: CancelAutomationValidation :one
UPDATE automation_validations SET status='canceled',detail='Canceled by an authorized publisher.',updated_at=now()
WHERE org_id=$1 AND id=$2 AND status IN ('queued','running') RETURNING *;
-- name: AutomationValidationRunning :one
SELECT EXISTS(SELECT 1 FROM automation_validations WHERE id=$1 AND status='running' AND lease_until>now())::boolean;

-- name: GetAutomationValidation :one
SELECT * FROM automation_validations WHERE org_id=$1 AND id=$2;
