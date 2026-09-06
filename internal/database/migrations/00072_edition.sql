-- +goose Up
CREATE TABLE installation_edition (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  edition text NOT NULL CHECK (edition IN ('community','enterprise'))
);
INSERT INTO installation_edition VALUES (true, 'community');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM installation_edition WHERE edition='enterprise') THEN
    RAISE EXCEPTION 'Restore a pre-upgrade backup instead of dropping Enterprise compatibility state';
  END IF;
END $$;
-- +goose StatementEnd
DROP TABLE installation_edition;
