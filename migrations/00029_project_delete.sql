-- +goose Up
-- A deleted project vanishes at once (lists, routing, pushes skip it) and is removed for good by the project.delete
-- job once its instances have stopped (decisions.md 056).
ALTER TABLE projects ADD COLUMN deleting_at timestamptz;

-- +goose Down
ALTER TABLE projects DROP COLUMN deleting_at;
