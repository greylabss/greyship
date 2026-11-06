-- name: AppendAppLogs :copyfrom
INSERT INTO app_logs (project_id, service_id, instance_id, stream, line, ts) VALUES ($1, $2, $3, $4, $5, $6);

-- name: AppLogsAfter :many
-- Lines after id, oldest first; service filters to one service when not null.
SELECT l.id, l.ts, l.stream, l.line, l.instance_id, s.name AS service_name
FROM app_logs l JOIN services s ON s.id = l.service_id
WHERE l.project_id = $1 AND l.id > $2 AND (sqlc.narg(service)::text IS NULL OR s.name = sqlc.narg(service)::text)
ORDER BY l.id
LIMIT $3;

-- name: AppLogsTail :many
-- The last lines, newest first (the caller reverses them).
SELECT l.id, l.ts, l.stream, l.line, l.instance_id, s.name AS service_name
FROM app_logs l JOIN services s ON s.id = l.service_id
WHERE l.project_id = $1 AND (sqlc.narg(service)::text IS NULL OR s.name = sqlc.narg(service)::text)
ORDER BY l.id DESC
LIMIT $2;

-- name: DeleteOldAppLogs :execrows
DELETE FROM app_logs WHERE ts < now() - make_interval(secs => sqlc.arg(keep_seconds)::float8);
