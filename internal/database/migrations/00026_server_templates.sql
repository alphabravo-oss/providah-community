-- +goose Up
CREATE TABLE server_templates (
 id text PRIMARY KEY,
 org_id text NOT NULL REFERENCES organizations(id),
 connection_id text NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 version integer NOT NULL CHECK(version > 0),
 region text NOT NULL,
 creation jsonb NOT NULL,
 status text NOT NULL DEFAULT 'published' CHECK(status IN ('published','retired','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,name,version),
 UNIQUE(org_id,id),
 FOREIGN KEY(org_id,connection_id) REFERENCES connections(org_id,id)
);
ALTER TABLE operations ADD COLUMN template_id text;
ALTER TABLE operations ADD CONSTRAINT operation_template FOREIGN KEY(org_id,template_id) REFERENCES server_templates(org_id,id);
UPDATE roles SET permissions=permissions||ARRAY['templates.read'] WHERE builtin;
UPDATE roles SET permissions=permissions||ARRAY['templates.publish'] WHERE builtin AND id='administrator';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM server_templates) THEN RAISE EXCEPTION 'Template history must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE operations DROP COLUMN template_id;
DROP TABLE server_templates;
UPDATE roles SET permissions=array_remove(array_remove(permissions,'templates.read'),'templates.publish') WHERE builtin;
