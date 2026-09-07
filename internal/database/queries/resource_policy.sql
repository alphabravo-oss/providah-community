-- name: GetResourcePolicy :one
SELECT approval_actions,creation_enabled,resource_policy_revision AS revision FROM organizations WHERE id=$1;
-- name: SaveResourcePolicy :execrows
UPDATE organizations SET creation_enabled=$2,approval_actions=sqlc.arg(approval_actions)::text[],resource_policy_revision=resource_policy_revision+1 WHERE id=$1 AND resource_policy_revision=$3;
