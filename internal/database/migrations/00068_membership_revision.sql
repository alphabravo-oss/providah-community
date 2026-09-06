-- +goose Up
ALTER TABLE memberships ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0);
-- +goose StatementBegin
CREATE FUNCTION increment_membership_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.revision := OLD.revision + 1;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER membership_revision BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION increment_membership_revision();

-- +goose Down
DROP TRIGGER membership_revision ON memberships;
DROP FUNCTION increment_membership_revision();
ALTER TABLE memberships DROP COLUMN revision;
