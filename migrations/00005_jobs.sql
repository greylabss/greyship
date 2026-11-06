-- +goose Up
-- Work queue for Stage 0 (decisions.md 005): workers claim rows with FOR UPDATE SKIP LOCKED and hold a lease.
-- A lease that runs out (the worker crashed) makes the job claimable again. Delivery is at least once.
CREATE TABLE jobs (
    id            bigserial PRIMARY KEY,
    kind          text NOT NULL,
    payload       jsonb NOT NULL DEFAULT '{}',
    status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts      int NOT NULL DEFAULT 0,
    max_attempts  int NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    run_after     timestamptz NOT NULL DEFAULT now(),
    leased_by     text,
    lease_until   timestamptz,
    last_error    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);

CREATE INDEX jobs_claimable ON jobs (kind, run_after) WHERE status IN ('queued', 'running');

-- +goose Down
DROP TABLE jobs;
