-- +goose Up
-- The scheduler (architecture §2.3) and node agents (§2.4): services, the nodes that run them, the instances
-- on those nodes, and which deployment is live. Only the columns in use so far; the rest arrive with their features.
CREATE TABLE services (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        text NOT NULL,
    path        text NOT NULL,
    type        text NOT NULL CHECK (type IN ('web', 'worker', 'cron')),
    memory_mb   int NOT NULL DEFAULT 128 CHECK (memory_mb > 0),
    cpu_millis  int NOT NULL DEFAULT 250 CHECK (cpu_millis > 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

ALTER TABLE artifacts ADD COLUMN service_id uuid REFERENCES services (id) ON DELETE SET NULL;

CREATE TABLE nodes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL UNIQUE,
    region          text NOT NULL,
    total_mb        int NOT NULL CHECK (total_mb > 0),
    state           text NOT NULL DEFAULT 'ready' CHECK (state IN ('ready', 'draining')),
    last_heartbeat  timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE instances (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id     uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    artifact_id    uuid NOT NULL REFERENCES artifacts (id) ON DELETE CASCADE,
    deployment_id  uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    node_id        uuid NOT NULL REFERENCES nodes (id),
    memory_mb      int NOT NULL,
    state          text NOT NULL DEFAULT 'starting'
                   CHECK (state IN ('starting', 'running', 'failed', 'stopping', 'stopped', 'exited')),
    addr           text,
    error          text,
    started_at     timestamptz,
    stopped_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX instances_node_live ON instances (node_id) WHERE state IN ('starting', 'running', 'stopping');
CREATE INDEX instances_deployment ON instances (deployment_id);

CREATE TABLE releases (
    project_id     uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    env            text NOT NULL,
    deployment_id  uuid NOT NULL REFERENCES deployments (id),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, env)
);

-- +goose Down
DROP TABLE releases;
DROP TABLE instances;
DROP TABLE nodes;
ALTER TABLE artifacts DROP COLUMN service_id;
DROP TABLE services;
