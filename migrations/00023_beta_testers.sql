-- +goose Up
-- People from the private-beta list (decisions.md 049): shown with a beta tester badge, for good.
ALTER TABLE users ADD COLUMN beta_tester boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN beta_tester;
