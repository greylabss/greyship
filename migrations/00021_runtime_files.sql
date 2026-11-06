-- +goose Up
-- The files bundle: repo files listed in greyship.toml's files setting, shipped next to every service's binary.
ALTER TABLE deployments
    ADD COLUMN files_key text,
    ADD COLUMN files_sha256 text,
    ADD COLUMN files_size bigint;

-- +goose Down
ALTER TABLE deployments DROP COLUMN files_key, DROP COLUMN files_sha256, DROP COLUMN files_size;
