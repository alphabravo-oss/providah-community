-- +goose Up
CREATE TABLE teams (
 org_id text NOT NULL REFERENCES organizations(id),id text NOT NULL,name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 role_id text NOT NULL,active boolean NOT NULL DEFAULT true,revision bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(org_id,id),UNIQUE(org_id,name),FOREIGN KEY(org_id,role_id) REFERENCES roles(org_id,id)
);
CREATE TABLE team_members (
 org_id text NOT NULL,team_id text NOT NULL,user_id text NOT NULL,
 PRIMARY KEY(org_id,team_id,user_id),FOREIGN KEY(org_id,team_id) REFERENCES teams(org_id,id),
 FOREIGN KEY(org_id,user_id) REFERENCES memberships(org_id,user_id)
);
CREATE INDEX team_member_user ON team_members(org_id,user_id);
-- +goose StatementBegin
CREATE OR REPLACE VIEW effective_memberships AS
SELECT o.id AS org_id,u.id AS user_id,CASE WHEN u.global_admin THEN coalesce(a.permissions,(SELECT permissions FROM roles WHERE id='administrator' AND builtin ORDER BY org_id LIMIT 1),'{}') ELSE ARRAY(SELECT DISTINCT p FROM unnest(coalesce(r.permissions,m.permissions)||ARRAY(
 SELECT unnest(tr.permissions) FROM teams t JOIN team_members tm ON tm.org_id=t.org_id AND tm.team_id=t.id JOIN roles tr ON tr.org_id=t.org_id AND tr.id=t.role_id
 WHERE t.org_id=o.id AND tm.user_id=u.id AND t.active
 )) p ORDER BY p) END::text[] AS permissions
FROM organizations o CROSS JOIN users u
LEFT JOIN roles a ON a.org_id=o.id AND a.id='administrator' AND a.builtin
LEFT JOIN memberships m ON m.org_id=o.id AND m.user_id=u.id
LEFT JOIN roles r ON r.org_id=o.id AND r.id=m.role_id
WHERE o.active AND u.active AND (u.global_admin OR m.active);
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE VIEW effective_memberships AS
SELECT o.id AS org_id,u.id AS user_id,CASE WHEN u.global_admin THEN coalesce(a.permissions,(SELECT permissions FROM roles WHERE id='administrator' AND builtin ORDER BY org_id LIMIT 1),'{}') ELSE coalesce(r.permissions,m.permissions) END::text[] AS permissions
FROM organizations o CROSS JOIN users u
LEFT JOIN roles a ON a.org_id=o.id AND a.id='administrator' AND a.builtin
LEFT JOIN memberships m ON m.org_id=o.id AND m.user_id=u.id
LEFT JOIN roles r ON r.org_id=o.id AND r.id=m.role_id
WHERE o.active AND u.active AND (u.global_admin OR m.active);
-- +goose StatementEnd
DROP TABLE team_members,teams;
