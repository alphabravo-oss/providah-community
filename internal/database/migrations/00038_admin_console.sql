-- +goose Up
UPDATE roles SET permissions=array_append(permissions,'admin.access'),revision=revision+1
WHERE NOT ('admin.access'=ANY(permissions)) AND permissions && ARRAY['connections.manage','modules.manage','members.read','members.manage','roles.manage','identity.manage','audit.read','notifications.manage','maintenance.manage','audit.export.manage'];
UPDATE memberships SET permissions=array_append(permissions,'admin.access')
WHERE role_id IS NULL AND NOT ('admin.access'=ANY(permissions)) AND permissions && ARRAY['connections.manage','modules.manage','members.read','members.manage','roles.manage','identity.manage','audit.read','notifications.manage','maintenance.manage','audit.export.manage'];
-- +goose Down
UPDATE roles SET permissions=array_remove(permissions,'admin.access'),revision=revision+1;
UPDATE memberships SET permissions=array_remove(permissions,'admin.access');
