-- +goose Up
-- One attempt to ship a project's source (F-20). The tarball lives in object storage under source_key.
-- uploading: record made, waiting for the CLI's PUT. queued: upload confirmed, waiting for a builder.
CREATE TABLE deployments (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    source        text NOT NULL CHECK (source IN ('cli', 'dashboard', 'github')),
    status        text NOT NULL DEFAULT 'uploading'
                  CHECK (status IN ('uploading', 'queued', 'building', 'releasing', 'ready', 'failed', 'superseded')),
    env           text NOT NULL DEFAULT 'production',
    commit_sha    text CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
    source_key    text NOT NULL,
    source_size   bigint NOT NULL CHECK (source_size > 0),
    source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
    created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);

CREATE INDEX deployments_project_created ON deployments (project_id, created_at DESC);

-- +goose Down
DROP TABLE deployments;
