-- name: ListTeams :many
SELECT t.*,ARRAY(SELECT tm.user_id FROM team_members tm WHERE tm.org_id=t.org_id AND tm.team_id=t.id ORDER BY tm.user_id)::text[] AS user_ids FROM teams t WHERE t.org_id=$1 ORDER BY t.name,t.id LIMIT 101;
-- name: GetTeam :one
SELECT * FROM teams WHERE org_id=$1 AND id=$2;
-- name: CreateTeam :exec
INSERT INTO teams(org_id,id,name,role_id,active) VALUES($1,$2,$3,$4,$5);
-- name: UpdateTeam :execrows
UPDATE teams SET name=$3,role_id=$4,active=$5,revision=revision+1 WHERE org_id=$1 AND id=$2 AND revision=$6;
-- name: ReplaceTeamMembers :exec
DELETE FROM team_members WHERE org_id=$1 AND team_id=$2;
-- name: AddTeamMember :execrows
INSERT INTO team_members(org_id,team_id,user_id) SELECT sqlc.arg(org_id),sqlc.arg(team_id),m.user_id FROM memberships m WHERE m.org_id=sqlc.arg(org_id) AND m.user_id=sqlc.arg(user_id);
-- name: DeleteTeam :execrows
DELETE FROM teams WHERE org_id=$1 AND id=$2 AND revision=$3;
