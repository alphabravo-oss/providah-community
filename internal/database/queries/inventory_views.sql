-- name: ListInventoryViews :many
SELECT * FROM inventory_views WHERE org_id=$1 AND user_id=$2 ORDER BY name,id LIMIT 50;
-- name: CountInventoryViews :one
SELECT count(*) FROM inventory_views WHERE org_id=$1 AND user_id=$2;
-- name: CreateInventoryView :one
INSERT INTO inventory_views(id,org_id,user_id,name,spec) VALUES($1,$2,$3,$4,$5) RETURNING *;
-- name: UpdateInventoryView :one
UPDATE inventory_views SET name=$4,spec=$5,revision=revision+1,updated_at=now()
WHERE id=$1 AND org_id=$2 AND user_id=$3 AND revision=$6 RETURNING *;
-- name: DeleteInventoryView :execrows
DELETE FROM inventory_views WHERE id=$1 AND org_id=$2 AND user_id=$3 AND revision=$4;
