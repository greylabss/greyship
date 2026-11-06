-- +goose Up
-- What apps print (F-60), kept for the plan's retention (F-61; 1 day on the free plan). Stage 0 keeps logs in
-- Postgres (architecture §6); the scheduler deletes expired lines.
CREATE TABLE app_logs (
    id           bigserial PRIMARY KEY,
    project_id   uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    service_id   uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    instance_id  uuid NOT NULL,
    stream       text NOT NULL CHECK (stream IN ('stdout', 'stderr', 'system')),
    line         text NOT NULL,
    ts           timestamptz NOT NULL
);

CREATE INDEX app_logs_project ON app_logs (project_id, id);
CREATE INDEX app_logs_ts ON app_logs (ts);

-- +goose Down
DROP TABLE app_logs;
