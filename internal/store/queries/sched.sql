-- name: UpsertNode :one
INSERT INTO nodes (name, region, total_mb) VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET region = EXCLUDED.region, total_mb = EXCLUDED.total_mb, state = 'ready', last_heartbeat = now()
RETURNING *;

-- name: HeartbeatNode :exec
UPDATE nodes SET last_heartbeat = now() WHERE id = $1;

-- name: ListNodeLoad :many
-- Nodes that can take work, with the memory their live instances hold.
SELECT n.id, n.name, n.total_mb,
       COALESCE((SELECT sum(i.memory_mb) FROM instances i
                 WHERE i.node_id = n.id AND i.state IN ('starting', 'running', 'paused', 'stopping')), 0)::int AS used_mb
FROM nodes n
WHERE n.state = 'ready' AND n.last_heartbeat > now() - make_interval(secs => sqlc.arg(fresh_seconds)::float8)
ORDER BY n.name;

-- name: UpsertService :one
INSERT INTO services (project_id, name, path, type) VALUES ($1, $2, $3, $4)
ON CONFLICT (project_id, name) DO UPDATE SET path = EXCLUDED.path, type = EXCLUDED.type
RETURNING *;

-- name: SetArtifactService :exec
UPDATE artifacts SET service_id = $2 WHERE id = $1;

-- name: CreateInstance :one
INSERT INTO instances (service_id, artifact_id, deployment_id, node_id, memory_mb)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInstance :one
SELECT * FROM instances WHERE id = $1;

-- name: InstanceDetail :one
-- Everything the agent needs to start an instance.
SELECT i.*, a.object_key, a.sha256, s.name AS service_name, s.type AS service_type, s.cpu_millis, s.project_id,
       d.files_key, d.files_sha256
FROM instances i JOIN artifacts a ON a.id = i.artifact_id JOIN services s ON s.id = i.service_id
JOIN deployments d ON d.id = i.deployment_id
WHERE i.id = $1;

-- name: ListDeploymentInstances :many
SELECT i.*, s.name AS service_name, n.name AS node_name
FROM instances i JOIN services s ON s.id = i.service_id JOIN nodes n ON n.id = i.node_id
WHERE i.deployment_id = $1
ORDER BY s.name, i.created_at;

-- name: MarkInstanceRunning :exec
UPDATE instances SET state = 'running', addr = $2, started_at = now(), error = NULL
WHERE id = $1 AND state = 'starting';

-- name: MarkInstanceFailed :exec
UPDATE instances SET state = 'failed', error = $2, stopped_at = now()
WHERE id = $1 AND state = 'starting';

-- name: MarkInstanceExited :exec
UPDATE instances SET state = 'exited', error = $2, stopped_at = now()
WHERE id = $1 AND state IN ('starting', 'running');

-- name: MarkInstanceStopping :exec
UPDATE instances SET state = 'stopping' WHERE id = $1 AND state IN ('starting', 'running', 'paused');

-- name: MarkInstanceStopped :exec
UPDATE instances SET state = 'stopped', stopped_at = now() WHERE id = $1 AND state NOT IN ('failed', 'exited', 'stopped');

-- name: StopNodeInstances :exec
-- An agent that restarts owns nothing it can't see: its node's live instances are written off.
UPDATE instances SET state = 'stopped', stopped_at = now(), error = 'node agent restarted'
WHERE node_id = $1 AND state IN ('starting', 'running', 'paused', 'stopping');

-- name: StartRelease :one
UPDATE deployments SET status = 'releasing' WHERE id = $1 AND status IN ('built', 'releasing') RETURNING *;

-- name: FailRelease :exec
UPDATE deployments SET status = 'failed', error = $2, finished_at = now() WHERE id = $1 AND status = 'releasing';

-- name: MarkDeploymentReady :exec
UPDATE deployments SET status = 'ready', finished_at = now() WHERE id = $1 AND status = 'releasing';

-- name: MarkDeploymentSuperseded :exec
UPDATE deployments SET status = 'superseded' WHERE id = $1 AND status = 'ready';

-- name: GetRelease :one
SELECT * FROM releases WHERE project_id = $1 AND env = $2;

-- name: SetRelease :exec
INSERT INTO releases (project_id, env, deployment_id) VALUES ($1, $2, $3)
ON CONFLICT (project_id, env) DO UPDATE SET deployment_id = EXCLUDED.deployment_id, updated_at = now();

-- name: LiveInstancesOf :many
SELECT * FROM instances WHERE deployment_id = $1 AND state IN ('starting', 'running', 'paused');

-- name: MaxBuildLogSeq :one
SELECT COALESCE(max(seq), 0)::int FROM build_logs WHERE deployment_id = $1;

-- name: SetServiceSubdomain :one
UPDATE services SET subdomain = $2 WHERE id = $1 AND subdomain IS NULL RETURNING *;

-- name: NotifyRoutes :exec
-- Routers reload their table at commit.
SELECT pg_notify('routes', '');

-- name: RoutingTable :many
-- What every router serves: each public web service's subdomain, its scaling settings, and the running or
-- paused instances of the live deployment.
SELECT s.id AS service_id, s.subdomain::text AS subdomain, s.concurrency, s.max_instances, s.grace_seconds,
       i.id AS instance_id, i.addr, i.node_id, i.state AS instance_state
FROM services s
JOIN releases r ON r.project_id = s.project_id AND r.env = 'production'
LEFT JOIN instances i ON i.service_id = s.id AND i.deployment_id = r.deployment_id
     AND i.state IN ('running', 'paused') AND i.addr IS NOT NULL
WHERE s.type = 'web' AND s.subdomain IS NOT NULL
ORDER BY s.subdomain, i.created_at;

-- name: ServiceURLs :many
-- The public web services a deployment has, for printing their URLs.
SELECT DISTINCT s.name, s.subdomain::text AS subdomain
FROM artifacts a JOIN services s ON s.id = a.service_id
WHERE a.deployment_id = $1 AND s.type = 'web' AND s.subdomain IS NOT NULL
ORDER BY s.name;

-- name: GetService :one
SELECT * FROM services WHERE id = $1;

-- name: MarkInstancePaused :execrows
UPDATE instances SET state = 'paused', paused_at = now() WHERE id = $1 AND state = 'running';

-- name: MarkInstanceResumed :execrows
UPDATE instances SET state = 'running', paused_at = NULL WHERE id = $1 AND state = 'paused';

-- name: LiveServiceInstances :many
-- A service's instances in its live deployment that are up or on their way up.
SELECT i.* FROM instances i
JOIN services s ON s.id = i.service_id
JOIN releases r ON r.project_id = s.project_id AND r.env = 'production' AND r.deployment_id = i.deployment_id
WHERE i.service_id = $1 AND i.state IN ('starting', 'running', 'paused');

-- name: LiveServiceArtifact :one
-- The live deployment's build of a service: what a new instance of it runs.
SELECT a.*, d.project_id FROM artifacts a
JOIN deployments d ON d.id = a.deployment_id
JOIN releases r ON r.project_id = d.project_id AND r.env = 'production' AND r.deployment_id = d.id
WHERE a.service_id = $1;

-- name: IdlePausedInstances :many
-- Paused instances past their service's idle timeout: they are stopped to free their memory (F-31).
SELECT i.* FROM instances i JOIN services s ON s.id = i.service_id
WHERE i.state = 'paused' AND i.paused_at < now() - make_interval(secs => s.idle_seconds);

-- name: LiveServicesOfProject :many
-- The web and worker services of a project's live deployment, with their build.
SELECT s.*, a.id AS artifact_id, d.id AS deployment_id
FROM services s
JOIN releases r ON r.project_id = s.project_id AND r.env = 'production'
JOIN deployments d ON d.id = r.deployment_id
JOIN artifacts a ON a.deployment_id = d.id AND a.service_id = s.id
WHERE s.project_id = $1 AND s.type IN ('web', 'worker')
ORDER BY s.name;

-- name: LiveServiceStates :many
-- A project's services in its live deployment, with how many instances are in each state, for the dashboard.
SELECT s.id, s.name, s.path, s.type, s.subdomain, s.memory_mb, s.max_instances, s.concurrency, s.grace_seconds,
       d.id AS deployment_id, d.finished_at AS released_at,
       count(i.id) FILTER (WHERE i.state = 'running')::int AS running,
       count(i.id) FILTER (WHERE i.state = 'paused')::int AS paused,
       count(i.id) FILTER (WHERE i.state = 'starting')::int AS starting
FROM services s
JOIN releases r ON r.project_id = s.project_id AND r.env = 'production'
JOIN deployments d ON d.id = r.deployment_id
JOIN artifacts a ON a.deployment_id = d.id AND a.service_id = s.id
LEFT JOIN instances i ON i.service_id = s.id AND i.deployment_id = d.id
WHERE s.project_id = $1 AND s.type <> 'release'
GROUP BY s.id, d.id
ORDER BY s.name;
