-- Read-only queries for the operator's admin view (decisions.md 048). They see every team's data, so only
-- internal/admin calls them, behind the admin check.

-- name: AdminCounts :one
SELECT
    (SELECT count(*) FROM users)::bigint AS users,
    (SELECT count(*) FROM teams)::bigint AS teams,
    (SELECT count(*) FROM projects)::bigint AS projects,
    (SELECT count(*) FROM deployments WHERE created_at > now() - interval '24 hours')::bigint AS deploys_24h,
    (SELECT count(*) FROM deployments WHERE created_at > now() - interval '24 hours' AND status = 'failed')::bigint AS failed_24h,
    (SELECT count(*) FROM deployments WHERE status IN ('uploading', 'queued', 'building', 'built', 'releasing'))::bigint AS in_progress,
    (SELECT count(*) FROM instances WHERE state IN ('starting', 'running'))::bigint AS instances_live;

-- name: AdminNodes :many
SELECT n.id, n.name, n.region, n.state, n.total_mb, n.last_heartbeat,
       extract(epoch FROM now() - n.last_heartbeat)::int AS heartbeat_age,
       coalesce((SELECT sum(i.memory_mb) FROM instances i WHERE i.node_id = n.id AND i.state IN ('starting', 'running', 'stopping')), 0)::bigint AS used_mb,
       (SELECT count(*) FROM instances i WHERE i.node_id = n.id AND i.state IN ('starting', 'running'))::bigint AS instances
FROM nodes n ORDER BY n.name;

-- name: AdminJobStats :many
SELECT kind, status, count(*)::bigint AS n FROM jobs
WHERE status IN ('queued', 'running') OR finished_at > now() - interval '24 hours'
GROUP BY kind, status ORDER BY kind, status;

-- name: AdminFailedJobs :many
SELECT id, kind, payload::text AS payload, attempts, max_attempts, last_error, created_at, finished_at FROM jobs
WHERE status = 'failed' ORDER BY id DESC LIMIT $1;

-- name: AdminDeployments :many
-- Newest first, optionally only one status, optionally ids starting with a prefix or a project or team slug.
SELECT d.id, d.status, d.error, d.source, d.build_mode, d.runner, d.commit_sha, d.branch, d.rollback_of,
       d.created_at, d.finished_at, d.build_seconds,
       p.id AS project_id, p.slug AS project_slug, t.slug AS team_slug, coalesce(u.email::text, '')::text AS created_by_email
FROM deployments d
JOIN projects p ON p.id = d.project_id
JOIN teams t ON t.id = p.team_id
LEFT JOIN users u ON u.id = d.created_by
WHERE (sqlc.arg(status)::text = '' OR d.status = sqlc.arg(status)::text)
  AND (sqlc.arg(q)::text = '' OR d.id::text LIKE sqlc.arg(q)::text || '%' OR p.slug = sqlc.arg(q)::text OR t.slug = sqlc.arg(q)::text)
ORDER BY d.created_at DESC
LIMIT sqlc.arg(lim);

-- name: AdminDeployment :one
SELECT d.*, p.slug AS project_slug, p.name AS project_name, t.id AS team_id, t.slug AS team_slug, t.plan AS team_plan,
       coalesce(u.email::text, '')::text AS created_by_email,
       coalesce((SELECT r.full_name FROM project_repos r WHERE r.project_id = p.id LIMIT 1), '')::text AS repo
FROM deployments d
JOIN projects p ON p.id = d.project_id
JOIN teams t ON t.id = p.team_id
LEFT JOIN users u ON u.id = d.created_by
WHERE d.id = $1;

-- name: AdminDeploymentByPrefix :many
SELECT d.id FROM deployments d WHERE d.id::text LIKE sqlc.arg(prefix)::text || '%' ORDER BY d.created_at DESC LIMIT 5;

-- name: AdminDeploymentInstances :many
SELECT i.id, i.state, i.addr, i.error, i.memory_mb, i.started_at, i.stopped_at, i.created_at,
       s.name AS service_name, s.type AS service_type, n.name AS node_name
FROM instances i JOIN services s ON s.id = i.service_id JOIN nodes n ON n.id = i.node_id
WHERE i.deployment_id = $1 ORDER BY s.name, i.created_at;

-- name: AdminDeploymentJobs :many
SELECT id, kind, status, attempts, max_attempts, last_error, created_at, finished_at FROM jobs
WHERE payload->>'deployment_id' = sqlc.arg(id)::text OR payload->>'subject' = sqlc.arg(id)::text
ORDER BY id;

-- name: AdminBuildLog :many
SELECT seq, stream, line, ts FROM build_logs WHERE deployment_id = $1 ORDER BY seq LIMIT 5000;

-- name: AdminDeploymentAppLogs :many
-- What the deployment's own instances printed, newest last.
SELECT * FROM (
    SELECT l.id, l.ts, l.stream, l.line, l.instance_id, s.name AS service_name
    FROM app_logs l JOIN services s ON s.id = l.service_id
    WHERE l.instance_id IN (SELECT i.id FROM instances i WHERE i.deployment_id = $1)
    ORDER BY l.id DESC LIMIT 300
) x ORDER BY id;

-- name: AdminUsers :many
SELECT u.id, u.email::text AS email, u.name, u.is_owner, (u.password_hash <> '')::bool AS has_password, u.beta_tester, u.created_at,
       coalesce(string_agg(DISTINCT t.slug, ', '), '')::text AS teams,
       (SELECT count(*) FROM projects p JOIN memberships m2 ON m2.team_id = p.team_id WHERE m2.user_id = u.id)::bigint AS projects,
       ld.last_deploy
FROM users u
LEFT JOIN memberships m ON m.user_id = u.id
LEFT JOIN teams t ON t.id = m.team_id
LEFT JOIN (SELECT created_by, max(created_at) AS last_deploy FROM deployments GROUP BY created_by) ld ON ld.created_by = u.id
GROUP BY u.id, ld.last_deploy ORDER BY u.created_at DESC LIMIT $1;
