-- +goose Up
ALTER TABLE organizations ADD COLUMN approval_actions text[] NOT NULL DEFAULT ARRAY['create','shutdown','restart','resize','delete','snapshot','tags'];
ALTER TABLE organizations ADD CONSTRAINT approval_actions_valid CHECK (approval_actions <@ ARRAY['create','start','shutdown','restart','resize','delete','snapshot','tags']::text[]);
-- +goose Down
ALTER TABLE organizations DROP COLUMN approval_actions;
