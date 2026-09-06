-- name: CreateBuiltinRoles :exec
INSERT INTO roles(org_id,id,name,permissions,builtin)
SELECT sqlc.arg(org_id),r.id,r.name,r.permissions||ARRAY['modules.read','templates.read']||CASE WHEN r.id IN ('administrator','connection-manager','auditor') THEN ARRAY['admin.access'] ELSE ARRAY[]::text[] END||CASE WHEN r.id='administrator' THEN ARRAY['modules.manage','templates.publish','dashboards.manage'] ELSE ARRAY[]::text[] END,true FROM (VALUES
 ('administrator','Administrator',ARRAY['connections.read','connections.manage','resources.read','audit.read','members.read','members.manage','roles.manage','operations.read','operations.request','operations.delete','operations.create','operations.approve','identity.manage','schedules.read','schedules.manage','notifications.read','notifications.manage','maintenance.read','maintenance.manage','maintenance.override','audit.export.manage']),
 ('viewer','Viewer',ARRAY['connections.read','resources.read']),
 ('connection-manager','Connection manager',ARRAY['connections.read','connections.manage','resources.read','maintenance.read']),
 ('auditor','Auditor',ARRAY['audit.read','notifications.read','maintenance.read']),
 ('operator','Operator',ARRAY['connections.read','resources.read','operations.read','operations.request','schedules.read','schedules.manage','notifications.read','maintenance.read']),
 ('approver','Approver',ARRAY['resources.read','operations.read','operations.approve','schedules.read','notifications.read','maintenance.read','maintenance.override'])
) AS r(id,name,permissions);
-- name: LockAccess :one
SELECT id FROM organizations WHERE id=$1 AND active FOR UPDATE;
-- name: ListRoles :many
SELECT * FROM roles WHERE org_id=$1 ORDER BY builtin DESC,name;
-- name: GetRole :one
SELECT * FROM roles WHERE org_id=$1 AND id=$2;
-- name: CreateRole :exec
INSERT INTO roles(org_id,id,name,permissions) VALUES($1,$2,$3,$4);
-- name: UpdateRole :execrows
UPDATE roles SET name=$3,permissions=$4,revision=revision+1 WHERE org_id=$1 AND id=$2 AND NOT builtin AND revision=$5;
-- name: ListMembers :many
SELECT m.user_id,u.email,m.active,m.revision,u.active AS user_active,coalesce(m.role_id,'')::text AS role_id,coalesce(r.name,'Custom legacy grant')::text AS role_name,coalesce(em.permissions,r.permissions,m.permissions)::text[] AS permissions
FROM memberships m JOIN users u ON u.id=m.user_id LEFT JOIN roles r ON r.org_id=m.org_id AND r.id=m.role_id LEFT JOIN effective_memberships em ON em.org_id=m.org_id AND em.user_id=m.user_id WHERE m.org_id=$1 ORDER BY u.email LIMIT 1001;
-- name: SetMember :execrows
UPDATE memberships SET role_id=$3,active=$4 WHERE org_id=$1 AND user_id=$2 AND revision=$5;
-- name: JoinMembership :exec
INSERT INTO memberships(org_id,user_id,permissions,role_id) VALUES($1,$2,'{}',$3);
-- name: AdministratorCount :one
SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND m.role_id='administrator' AND m.active AND u.active;
-- name: HasMembership :one
SELECT EXISTS(SELECT 1 FROM memberships WHERE org_id=$1 AND user_id=$2);
-- name: CreateInvitation :exec
INSERT INTO invitations(id,org_id,email,role_id,role_revision,inviter_id,token_hash) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: ListInvitations :many
SELECT i.id,i.email,i.role_id,r.name AS role_name,i.expires_at FROM invitations i JOIN roles r ON r.org_id=i.org_id AND r.id=i.role_id WHERE i.org_id=$1 AND i.revoked_at IS NULL AND i.accepted_at IS NULL AND i.expires_at>now() ORDER BY i.created_at DESC LIMIT 1001;
-- name: InvitationByToken :one
SELECT * FROM invitations WHERE token_hash=$1 AND revoked_at IS NULL AND accepted_at IS NULL AND expires_at>now();
-- name: EnrollInvitation :exec
UPDATE invitations SET totp_ciphertext=$2 WHERE id=$1;
-- name: AcceptInvitation :execrows
UPDATE invitations SET accepted_at=now(),totp_ciphertext=NULL WHERE id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now();
-- name: RevokeInvitation :execrows
UPDATE invitations SET revoked_at=now(),totp_ciphertext=NULL WHERE org_id=$1 AND id=$2 AND accepted_at IS NULL AND revoked_at IS NULL;
-- name: UserExists :one
SELECT EXISTS(SELECT 1 FROM users WHERE email=$1);
-- name: VerifySessionMFA :exec
UPDATE sessions SET mfa_at=now() WHERE id=$1;
-- name: EventRevision :one
SELECT o.revision FROM organizations o JOIN effective_memberships m ON m.org_id=o.id WHERE o.id=$1 AND m.user_id=$2;
