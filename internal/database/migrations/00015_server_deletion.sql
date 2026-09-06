-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart','delete'));
ALTER TABLE operations ADD COLUMN deletion_impact text NOT NULL DEFAULT '' CHECK(octet_length(deletion_impact)<=16384);
UPDATE roles SET permissions=permissions||ARRAY['operations.delete'] WHERE id='administrator' AND builtin;
-- +goose Down
-- Refuse rollback while deletion history exists rather than losing audit-linked operations.
ALTER TABLE operations DROP CONSTRAINT operations_action_check;
ALTER TABLE operations ADD CONSTRAINT operations_action_check CHECK(action IN ('start','shutdown','restart'));
ALTER TABLE operations DROP COLUMN deletion_impact;
UPDATE roles SET permissions=array_remove(permissions,'operations.delete') WHERE builtin;
