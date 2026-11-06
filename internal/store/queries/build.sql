-- name: StartBuild :one
-- Only a queued deployment, or one whose earlier build attempt died mid-way, starts building.
UPDATE deployments SET status = 'building', started_at = now(), error = NULL
WHERE id = $1 AND status IN ('queued', 'building')
RETURNING *;

-- name: FinishBuild :one
UPDATE deployments SET status = 'built'
WHERE id = $1 AND status = 'building'
RETURNING *;

-- name: FailBuild :one
UPDATE deployments SET status = 'failed', error = $2, finished_at = now()
WHERE id = $1 AND status = 'building'
RETURNING *;

-- name: DeleteArtifacts :exec
-- A retried build starts from nothing: artifacts and log lines from an earlier attempt go.
DELETE FROM artifacts WHERE deployment_id = $1;

-- name: DeleteBuildLogs :exec
DELETE FROM build_logs WHERE deployment_id = $1;

-- name: CreateArtifact :one
INSERT INTO artifacts (deployment_id, service_name, service_path, service_type, object_key, sha256, size_bytes, go_version, goarch)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListArtifacts :many
SELECT * FROM artifacts WHERE deployment_id = $1 ORDER BY service_name;

-- name: AppendBuildLog :copyfrom
INSERT INTO build_logs (deployment_id, seq, stream, line, ts) VALUES ($1, $2, $3, $4, $5);

-- name: NotifyBuildLog :exec
SELECT pg_notify('build_logs', sqlc.arg(deployment_id)::text);

-- name: BuildLogsAfter :many
SELECT * FROM build_logs WHERE deployment_id = $1 AND seq > $2 ORDER BY seq LIMIT $3;

-- name: StartVerify :one
-- A local build's check; like StartBuild, a repeat of an unfinished check starts again.
UPDATE deployments SET status = 'building', started_at = now(), error = NULL
WHERE id = $1 AND status IN ('queued', 'building') AND build_mode = 'local'
RETURNING *;

-- name: ProjectArtifactBySHA :one
-- A binary this project already has, checked when it was first uploaded: same bytes (sha256) and CPU.
SELECT a.* FROM artifacts a JOIN deployments d ON d.id = a.deployment_id
WHERE d.project_id = sqlc.arg(project_id) AND a.sha256 = sqlc.arg(sha256) AND a.goarch = sqlc.arg(goarch)
ORDER BY a.created_at DESC
LIMIT 1;
