-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload) VALUES ($1, $2) RETURNING *;

-- name: NotifyJobs :exec
-- Delivered when the surrounding transaction commits, so workers never wake for a job they can't see yet.
SELECT pg_notify('jobs', sqlc.arg(kind)::text);

-- name: ClaimJob :one
-- A job whose group already runs group_limit jobs (with live leases) waits its turn; higher priority goes first.
UPDATE jobs SET status = 'running', attempts = attempts + 1, leased_by = sqlc.arg(worker)::text,
    lease_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.kind = sqlc.arg(kind)::text
      AND ((j.status = 'queued' AND j.run_after <= now()) OR (j.status = 'running' AND j.lease_until < now()))
      AND (j.group_key IS NULL OR (
            SELECT count(*) FROM jobs r
            WHERE r.kind = j.kind AND r.group_key = j.group_key AND r.status = 'running'
              AND r.lease_until >= now() AND r.id <> j.id) < j.group_limit)
    ORDER BY j.priority DESC, j.run_after, j.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: LockClaims :exec
-- Claims of one kind take turns, so two workers can't both see a group as free and start two of its jobs.
SELECT pg_advisory_xact_lock(hashtext('claim:' || sqlc.arg(kind)::text));

-- name: EnqueueGroupedJob :one
INSERT INTO jobs (kind, payload, group_key, group_limit, priority) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: BuildsAhead :one
-- How many builds are queued or running before this deployment's.
SELECT count(*) FROM jobs a
WHERE a.kind = 'build.requested' AND a.status IN ('queued', 'running')
  AND a.id < (SELECT max(b.id) FROM jobs b WHERE b.kind = 'build.requested' AND b.payload->>'deployment_id' = sqlc.arg(deployment_id)::text);

-- name: CompleteJob :one
UPDATE jobs SET status = 'done', finished_at = now(), lease_until = NULL
WHERE id = sqlc.arg(id) AND leased_by = sqlc.arg(worker)::text AND status = 'running'
RETURNING *;

-- name: FailJob :one
-- Retries back off 10s per attempt, at most 5 minutes; the last attempt fails the job for good.
UPDATE jobs SET
    status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
    finished_at = CASE WHEN attempts >= max_attempts THEN now() END,
    run_after = now() + make_interval(secs => least(10 * attempts, 300)),
    last_error = sqlc.arg(last_error)::text,
    lease_until = NULL
WHERE id = sqlc.arg(id) AND leased_by = sqlc.arg(worker)::text AND status = 'running'
RETURNING *;

-- name: GiveUpJob :exec
-- A job whose lease ran out on its last attempt: its worker died every time, so it is not run again.
UPDATE jobs SET status = 'failed', finished_at = now(), lease_until = NULL,
    last_error = 'lease expired on the last attempt (the worker stopped without finishing)'
WHERE id = sqlc.arg(id) AND leased_by = sqlc.arg(worker)::text AND status = 'running';

-- name: ExtendJobLease :one
UPDATE jobs SET lease_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
WHERE id = sqlc.arg(id) AND leased_by = sqlc.arg(worker)::text AND status = 'running'
RETURNING id;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;
