-- +goose Up
-- Server builds on Fly Machines and build minutes per plan (decisions.md 039).
CREATE TABLE build_machines (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fly_machine_id text NOT NULL UNIQUE,
    volume_id      text,
    size           text NOT NULL,
    team_id        uuid REFERENCES teams(id) ON DELETE CASCADE, -- a machine with a cache volume builds for one team only
    status         text NOT NULL DEFAULT 'stopped' CHECK (status IN ('stopped', 'busy')),
    last_used_at   timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX build_machines_pick ON build_machines (team_id, size, status);

-- Log lines and the final result posted by a build machine, waiting for the builder to read them.
CREATE TABLE build_reports (
    id            bigserial PRIMARY KEY,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    stream        text NOT NULL DEFAULT 'build',
    line          text NOT NULL DEFAULT '',
    result        jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX build_reports_deployment ON build_reports (deployment_id, id);

ALTER TABLE deployments ADD COLUMN runner text CHECK (runner IN ('gvisor', 'fly'));
ALTER TABLE deployments ADD COLUMN machine_size text;
ALTER TABLE deployments ADD COLUMN build_seconds int;

ALTER TABLE jobs ADD COLUMN group_limit int NOT NULL DEFAULT 1 CHECK (group_limit > 0);
ALTER TABLE jobs ADD COLUMN priority int NOT NULL DEFAULT 0;

CREATE TABLE build_usage_notices (
    team_id   uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    month     date NOT NULL,
    threshold int NOT NULL,
    sent_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, month, threshold)
);

-- +goose Down
DROP TABLE build_usage_notices;
ALTER TABLE jobs DROP COLUMN priority;
ALTER TABLE jobs DROP COLUMN group_limit;
ALTER TABLE deployments DROP COLUMN build_seconds;
ALTER TABLE deployments DROP COLUMN machine_size;
ALTER TABLE deployments DROP COLUMN runner;
DROP TABLE build_reports;
DROP TABLE build_machines;
