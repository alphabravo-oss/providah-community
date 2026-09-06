-- +goose Up
ALTER TABLE organizations ADD COLUMN revision bigint NOT NULL DEFAULT 0;
-- +goose StatementBegin
CREATE FUNCTION bump_organization_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE organizations SET revision=revision+1 WHERE id=NEW.org_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_revision AFTER INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION bump_organization_revision();
CREATE TRIGGER connection_revision AFTER INSERT OR UPDATE ON connections FOR EACH ROW EXECUTE FUNCTION bump_organization_revision();
-- +goose Down
DROP TRIGGER connection_revision ON connections;
DROP TRIGGER audit_revision ON audit_events;
DROP FUNCTION bump_organization_revision();
ALTER TABLE organizations DROP COLUMN revision;
