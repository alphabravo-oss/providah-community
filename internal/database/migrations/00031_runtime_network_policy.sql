-- +goose Up
ALTER TABLE automation_versions ADD COLUMN runtime_policy text NOT NULL DEFAULT '' CHECK(runtime_policy='' OR runtime_policy ~ '^[a-f0-9]{64}$');
ALTER TABLE automation_versions ADD COLUMN dependency_hosts text[] NOT NULL DEFAULT '{}';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM automation_versions WHERE runtime_policy<>'') THEN RAISE EXCEPTION 'Pinned runtime policy history must be preserved; rollback refused'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE automation_versions DROP COLUMN dependency_hosts;
ALTER TABLE automation_versions DROP COLUMN runtime_policy;
