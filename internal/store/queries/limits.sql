-- name: TeamMaxConcurrentBuilds :one
SELECT max_concurrent_builds FROM teams WHERE id = $1;

-- name: ProjectTeamLimits :one
SELECT t.id, t.max_memory_mb, t.max_cpu_millis FROM teams t JOIN projects p ON p.team_id = t.id WHERE p.id = $1;

-- name: LockTeam :exec
-- Serialises limit checks of one team, so two instances starting at once can't both slip under the limit.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(team_id)::text, 0));

-- name: TeamResourceUse :one
-- What a team's apps hold now: instances that are up, on their way up, or paused (a paused one keeps its memory).
SELECT coalesce(sum(i.memory_mb), 0)::bigint AS memory_mb, coalesce(sum(s.cpu_millis), 0)::bigint AS cpu_millis
FROM instances i
JOIN services s ON s.id = i.service_id
JOIN projects p ON p.id = s.project_id
WHERE p.team_id = $1 AND i.state IN ('starting', 'running', 'paused');

-- name: UserOwnedTeam :one
SELECT t.* FROM teams t JOIN memberships m ON m.team_id = t.id
WHERE m.user_id = $1 AND m.role = 'owner' ORDER BY t.created_at LIMIT 1;

-- name: SetUserLimits :exec
UPDATE teams SET max_memory_mb = sqlc.narg('max_memory_mb'), max_cpu_millis = sqlc.narg('max_cpu_millis'), max_concurrent_builds = sqlc.narg('max_concurrent_builds')
WHERE id IN (SELECT m.team_id FROM memberships m WHERE m.user_id = sqlc.arg(user_id) AND m.role = 'owner');
