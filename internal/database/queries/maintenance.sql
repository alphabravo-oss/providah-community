-- name: ListMaintenancePolicies :many
SELECT p.*,ARRAY(SELECT c.connection_id FROM maintenance_connections c WHERE c.policy_id=p.id ORDER BY c.connection_id)::text[] AS connection_ids FROM maintenance_policies p WHERE p.org_id=$1 ORDER BY p.created_at,p.id;
-- name: GetMaintenanceRevision :one
SELECT maintenance_revision FROM organizations WHERE id=$1;
-- name: CreateMaintenancePolicy :exec
INSERT INTO maintenance_policies(id,org_id,name,spec,duration_minutes) VALUES($1,$2,$3,$4,$5);
-- name: UpdateMaintenancePolicy :execrows
UPDATE maintenance_policies SET name=$3,spec=$4,duration_minutes=$5 WHERE org_id=$1 AND id=$2;
-- name: ClearMaintenanceConnections :exec
DELETE FROM maintenance_connections WHERE org_id=$1 AND policy_id=$2;
-- name: AddMaintenanceConnection :exec
INSERT INTO maintenance_connections(org_id,policy_id,connection_id) VALUES($1,$2,$3);
-- name: DeleteMaintenancePolicy :execrows
DELETE FROM maintenance_policies WHERE org_id=$1 AND id=$2 AND name=$3;
-- name: BumpMaintenanceRevision :exec
UPDATE organizations SET maintenance_revision=maintenance_revision+1 WHERE id=$1;
