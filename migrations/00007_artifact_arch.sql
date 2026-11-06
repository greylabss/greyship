-- +goose Up
-- Binaries are built for the CPU of the nodes that run them: amd64 in production, the VM's own on a dev Mac.
ALTER TABLE artifacts ADD COLUMN goarch text NOT NULL DEFAULT 'amd64' CHECK (goarch IN ('amd64', 'arm64'));
ALTER TABLE artifacts ALTER COLUMN goarch DROP DEFAULT;

-- +goose Down
ALTER TABLE artifacts DROP COLUMN goarch;
