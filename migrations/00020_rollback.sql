-- +goose Up
-- Redeploys and rollbacks are new deployments made from an existing one's binaries (spec F-27).
ALTER TABLE deployments ADD COLUMN rollback_of uuid REFERENCES deployments(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE deployments DROP COLUMN rollback_of;
