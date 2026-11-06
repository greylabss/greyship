-- +goose Up
-- The instance owner (decisions.md 050): the first account on an instance, or the one GREYSHIP_OWNER_EMAIL names.
-- Owners see the admin view. Replaces the admin email that was written into the code.
ALTER TABLE users ADD COLUMN is_owner boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN is_owner;
