-- +goose Up
-- Building (F-23, F-24): a deployment is built into one artifact per service, and its build log is kept.
ALTER TABLE deployments DROP CONSTRAINT deployments_status_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_status_check
    CHECK (status IN ('uploading', 'queued', 'building', 'built', 'releasing', 'ready', 'failed', 'superseded'));
ALTER TABLE deployments ADD COLUMN error text;
ALTER TABLE deployments ADD COLUMN started_at timestamptz;

-- Services are named here until the services table exists (step 10); then service_id joins them.
CREATE TABLE artifacts (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    service_name  text NOT NULL,
    service_path  text NOT NULL,
    service_type  text NOT NULL CHECK (service_type IN ('web', 'worker', 'cron', 'release')),
    object_key    text NOT NULL,
    sha256        text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes    bigint NOT NULL,
    go_version    text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (deployment_id, service_name)
);

CREATE TABLE build_logs (
    deployment_id uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    seq           int NOT NULL,
    stream        text NOT NULL CHECK (stream IN ('system', 'build')),
    line          text NOT NULL,
    ts            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (deployment_id, seq)
);

-- +goose Down
DROP TABLE build_logs;
DROP TABLE artifacts;
ALTER TABLE deployments DROP COLUMN started_at;
ALTER TABLE deployments DROP COLUMN error;
ALTER TABLE deployments DROP CONSTRAINT deployments_status_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_status_check
    CHECK (status IN ('uploading', 'queued', 'building', 'releasing', 'ready', 'failed', 'superseded'));
