-- name: ListDashboards :many
SELECT d.* FROM dashboards d WHERE d.org_id=$1 AND (d.user_id=$2 OR d.shared OR (cardinality(d.team_ids)>0 AND (
 EXISTS(SELECT 1 FROM effective_memberships m WHERE m.org_id=d.org_id AND m.user_id=$2 AND 'dashboards.manage'=ANY(m.permissions))
 OR EXISTS(SELECT 1 FROM teams t JOIN team_members tm ON tm.org_id=t.org_id AND tm.team_id=t.id WHERE t.org_id=d.org_id AND t.id=ANY(d.team_ids) AND t.active AND tm.user_id=$2)
))) ORDER BY d.name,d.id LIMIT 100;
-- name: CountDashboards :one
SELECT count(*) FROM dashboards WHERE org_id=$1 AND user_id=$2;
-- name: CountSharedDashboards :one
SELECT count(*) FROM dashboards WHERE org_id=$1 AND (shared OR cardinality(team_ids)>0);
-- name: GetOwnedOrSharedDashboard :one
SELECT * FROM dashboards WHERE org_id=$1 AND id=$2 AND (user_id=$3 OR shared OR cardinality(team_ids)>0);
-- name: CreateDashboard :one
INSERT INTO dashboards(id,org_id,user_id,name,spec,shared,team_ids) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *;
-- name: UpdateDashboard :one
UPDATE dashboards SET name=$4,spec=$5,revision=revision+1,updated_at=now(),shared=sqlc.arg(shared),team_ids=sqlc.arg(team_ids)
WHERE id=$1 AND org_id=$2 AND (user_id=$3 OR ((shared OR cardinality(team_ids)>0) AND sqlc.arg(manage_shared)::boolean)) AND revision=$6 RETURNING *;
-- name: DeleteDashboard :execrows
DELETE FROM dashboards WHERE id=$1 AND org_id=$2 AND (user_id=$3 OR ((shared OR cardinality(team_ids)>0) AND sqlc.arg(manage_shared)::boolean)) AND revision=$4;

-- name: ListDashboardTeams :many
SELECT id,name,active FROM teams WHERE org_id=$1 ORDER BY name,id LIMIT 101;
