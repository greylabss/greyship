-- +goose Up
-- Scale to zero (F-31–F-33, F-80): idle instances pause and keep their memory; the router wakes them.
ALTER TABLE instances DROP CONSTRAINT instances_state_check;
ALTER TABLE instances ADD CONSTRAINT instances_state_check
    CHECK (state IN ('starting', 'running', 'paused', 'failed', 'stopping', 'stopped', 'exited'));
ALTER TABLE instances ADD COLUMN paused_at timestamptz;

ALTER TABLE services ADD COLUMN concurrency int NOT NULL DEFAULT 100 CHECK (concurrency > 0);
ALTER TABLE services ADD COLUMN max_instances int NOT NULL DEFAULT 2 CHECK (max_instances > 0);
ALTER TABLE services ADD COLUMN grace_seconds int NOT NULL DEFAULT 30 CHECK (grace_seconds >= 0);
ALTER TABLE services ADD COLUMN idle_seconds int NOT NULL DEFAULT 86400 CHECK (idle_seconds > 0);

DROP INDEX instances_node_live;
CREATE INDEX instances_node_live ON instances (node_id) WHERE state IN ('starting', 'running', 'paused', 'stopping');

-- +goose Down
DROP INDEX instances_node_live;
CREATE INDEX instances_node_live ON instances (node_id) WHERE state IN ('starting', 'running', 'stopping');
ALTER TABLE services DROP COLUMN idle_seconds;
ALTER TABLE services DROP COLUMN grace_seconds;
ALTER TABLE services DROP COLUMN max_instances;
ALTER TABLE services DROP COLUMN concurrency;
ALTER TABLE instances DROP COLUMN paused_at;
ALTER TABLE instances DROP CONSTRAINT instances_state_check;
ALTER TABLE instances ADD CONSTRAINT instances_state_check
    CHECK (state IN ('starting', 'running', 'failed', 'stopping', 'stopped', 'exited'));
