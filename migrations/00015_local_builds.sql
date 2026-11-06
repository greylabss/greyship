-- +goose Up
-- Local builds (decisions.md 034): grey deploy may compile on the developer's machine and upload binaries.
-- Those deployments have no source tarball; their binaries are checked, then released like any other.
ALTER TABLE deployments ADD COLUMN build_mode text NOT NULL DEFAULT 'remote' CHECK (build_mode IN ('remote', 'local'));
ALTER TABLE deployments ALTER COLUMN source_key DROP NOT NULL;
ALTER TABLE deployments ALTER COLUMN source_size DROP NOT NULL;
ALTER TABLE deployments ALTER COLUMN source_sha256 DROP NOT NULL;
ALTER TABLE deployments ADD CONSTRAINT deployments_build_source_check
    CHECK (build_mode = 'local' OR (source_key IS NOT NULL AND source_size IS NOT NULL AND source_sha256 IS NOT NULL));

-- A local build's binaries, as uploaded, before the builder checks them.
CREATE TABLE uploaded_binaries (
    deployment_id  uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    service_name   text NOT NULL,
    service_path   text NOT NULL,
    service_type   text NOT NULL CHECK (service_type IN ('web', 'worker', 'release')),
    sha256         text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes     bigint NOT NULL CHECK (size_bytes > 0),
    gz_bytes       bigint NOT NULL CHECK (gz_bytes > 0),
    object_key     text NOT NULL,
    PRIMARY KEY (deployment_id, service_name)
);

-- +goose Down
DROP TABLE uploaded_binaries;
DELETE FROM deployments WHERE build_mode = 'local';
ALTER TABLE deployments DROP CONSTRAINT deployments_build_source_check;
ALTER TABLE deployments ALTER COLUMN source_sha256 SET NOT NULL;
ALTER TABLE deployments ALTER COLUMN source_size SET NOT NULL;
ALTER TABLE deployments ALTER COLUMN source_key SET NOT NULL;
ALTER TABLE deployments DROP COLUMN build_mode;
