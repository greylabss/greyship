-- name: GetProjectKey :one
SELECT wrapped_key FROM project_keys WHERE project_id = $1;

-- name: CreateProjectKey :one
-- Two writers racing to create the key both end up with the stored one.
INSERT INTO project_keys (project_id, wrapped_key) VALUES ($1, $2)
ON CONFLICT (project_id) DO UPDATE SET project_id = EXCLUDED.project_id
RETURNING wrapped_key;

-- name: ListMainEnv :many
SELECT key, value_hash, updated_at FROM env_vars
WHERE project_id = $1 AND env = $2 AND service_id IS NULL
ORDER BY key;

-- name: ListMainEnvValues :many
SELECT key, ciphertext FROM env_vars
WHERE project_id = $1 AND env = $2 AND service_id IS NULL
ORDER BY key;

-- name: SetMainEnv :exec
INSERT INTO env_vars (project_id, env, key, ciphertext, value_hash) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (project_id, env, key) WHERE service_id IS NULL
DO UPDATE SET ciphertext = EXCLUDED.ciphertext, value_hash = EXCLUDED.value_hash, updated_at = now();

-- name: DeleteMainEnv :execrows
DELETE FROM env_vars WHERE project_id = $1 AND env = $2 AND key = $3 AND service_id IS NULL;

-- name: CountMainEnv :one
SELECT count(*) FROM env_vars WHERE project_id = $1 AND env = $2 AND service_id IS NULL;

-- name: EffectiveEnv :many
-- What one service gets (architecture §4.4): main variables minus its exclusions, then its overrides on top.
-- Overrides of other services are never selected (F-46).
SELECT key, ciphertext, service_id IS NOT NULL AS override FROM env_vars v
WHERE v.project_id = $1 AND v.env = $2
  AND (v.service_id = sqlc.arg(service_id)::uuid
       OR (v.service_id IS NULL AND NOT EXISTS (
            SELECT 1 FROM env_excludes x WHERE x.service_id = sqlc.arg(service_id)::uuid AND x.env = v.env AND x.key = v.key)))
ORDER BY override, key;
