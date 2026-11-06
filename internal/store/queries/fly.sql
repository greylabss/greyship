-- name: PickBuildMachine :one
-- A stopped machine of this size for this team, taken for one build. Concurrent builders skip each other's picks.
UPDATE build_machines AS b SET status = 'busy', last_used_at = now()
WHERE b.id = (
    SELECT m.id FROM build_machines m
    WHERE m.team_id = sqlc.arg(team_id)::uuid AND m.size = sqlc.arg(size)::text AND m.status = 'stopped'
    ORDER BY m.last_used_at DESC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING b.*;

-- name: CountBuildMachines :one
SELECT count(*) FROM build_machines;

-- name: AddBuildMachine :one
INSERT INTO build_machines (fly_machine_id, volume_id, size, team_id, status)
VALUES ($1, $2, $3, $4, 'busy') RETURNING *;

-- name: ReleaseBuildMachine :exec
UPDATE build_machines SET status = 'stopped', last_used_at = now() WHERE id = $1;

-- name: RemoveBuildMachine :exec
DELETE FROM build_machines WHERE id = $1;

-- name: StaleBuildMachines :many
-- Stopped machines whose team hasn't built for a while: their volume costs money, so they go (decisions.md 039).
SELECT * FROM build_machines WHERE status = 'stopped' AND last_used_at < now() - make_interval(days => sqlc.arg(days)::int);

-- name: AddBuildReport :exec
INSERT INTO build_reports (deployment_id, stream, line, result) VALUES ($1, $2, $3, $4);

-- name: BuildReportsAfter :many
SELECT * FROM build_reports WHERE deployment_id = $1 AND id > $2 ORDER BY id LIMIT 500;

-- name: DeleteBuildReports :exec
DELETE FROM build_reports WHERE deployment_id = $1;

-- name: RecordBuildRun :exec
UPDATE deployments SET runner = $2, machine_size = $3, build_seconds = $4 WHERE id = $1;

-- name: TeamBuildSeconds :one
-- Server build time a team used since the start of the month (spec §7); local builds have no build_seconds.
SELECT coalesce(sum(d.build_seconds), 0)::bigint FROM deployments d
JOIN projects p ON p.id = d.project_id
WHERE p.team_id = $1 AND d.created_at >= date_trunc('month', now());

-- name: TeamPlan :one
SELECT plan FROM teams WHERE id = $1;
