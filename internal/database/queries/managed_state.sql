-- name: ListAutomationProjects :many
SELECT * FROM automation_projects WHERE org_id=$1 ORDER BY name,id LIMIT 201;
-- name: CreateAutomationProject :one
INSERT INTO automation_projects(id,org_id,name,version_id,inputs_ciphertext,inputs_hash) VALUES($1,$2,$3,$4,$5,$6) RETURNING *;
-- name: LockAutomationProject :one
SELECT * FROM automation_projects WHERE org_id=$1 AND id=$2 FOR UPDATE;
-- name: ListAutomationStates :many
SELECT id,lineage,serial,sha256,state_bytes,created_at FROM automation_states WHERE org_id=$1 AND project_id=$2 AND (sqlc.arg(before_serial)::bigint<0 OR serial<sqlc.arg(before_serial)::bigint) ORDER BY serial DESC LIMIT 101;
-- name: CurrentAutomationState :one
SELECT s.* FROM automation_states s JOIN automation_projects p ON p.state_id=s.id AND p.org_id=s.org_id AND p.id=s.project_id WHERE p.org_id=$1 AND p.id=$2;
-- name: InsertAutomationState :exec
INSERT INTO automation_states(id,org_id,project_id,lineage,serial,sha256,state_bytes,ciphertext,storage) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);
-- name: AdvanceAutomationState :exec
UPDATE automation_projects SET state_id=$3,lineage=$4,serial=$5,sha256=$6,state_bytes=$7 WHERE org_id=$1 AND id=$2;
-- name: SetAutomationStateLock :exec
UPDATE automation_projects SET lock_id=$3,lock_session_id=$4 WHERE org_id=$1 AND id=$2;
-- name: CreateAutomationStateSession :exec
INSERT INTO automation_state_sessions(id,org_id,project_id,requester_id,requester_oidc_id,secret_hash,writable,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '15 minutes');
-- name: AutomationStateSession :one
SELECT * FROM automation_state_sessions WHERE id=$1 AND project_id=$2 AND expires_at>now() AND NOT revoked;
-- name: VerifyAutomationState :one
SELECT * FROM automation_states WHERE id>$1 ORDER BY id LIMIT 1;
-- name: InvalidAutomationStatePointer :one
SELECT EXISTS(SELECT 1 FROM automation_projects p LEFT JOIN automation_states s ON s.id=p.state_id AND s.org_id=p.org_id AND s.project_id=p.id WHERE (p.state_id='' AND EXISTS(SELECT 1 FROM automation_states history WHERE history.project_id=p.id)) OR (p.state_id<>'' AND (s.id IS NULL OR s.serial<>p.serial OR s.lineage<>p.lineage OR s.sha256<>p.sha256 OR s.state_bytes<>p.state_bytes OR EXISTS(SELECT 1 FROM automation_states newer WHERE newer.project_id=p.id AND newer.serial>p.serial))))::boolean;

-- name: GetAutomationProject :one
SELECT * FROM automation_projects WHERE org_id=$1 AND id=$2;
