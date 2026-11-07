-- name: GetMembershipByTeamID :one
SELECT t.*, m.role FROM teams t JOIN memberships m ON m.team_id = t.id
WHERE m.user_id = $1 AND t.id = $2;

-- name: CreateProject :one
INSERT INTO projects (team_id, name, slug) VALUES ($1, $2, $3)
ON CONFLICT (team_id, slug) DO NOTHING
RETURNING *;

-- name: ListProjects :many
SELECT * FROM projects WHERE team_id = $1 AND deleting_at IS NULL ORDER BY created_at DESC;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE team_id = $1 AND slug = $2 AND deleting_at IS NULL;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 AND deleting_at IS NULL;

-- name: RenameProject :one
UPDATE projects SET name = $2, slug = $3 WHERE id = $1 RETURNING *;

-- name: MarkProjectDeleting :execrows
UPDATE projects SET deleting_at = now() WHERE id = $1 AND deleting_at IS NULL;

-- name: GetDeletingProject :one
SELECT * FROM projects WHERE id = $1 AND deleting_at IS NOT NULL;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1 AND deleting_at IS NOT NULL;

-- name: ProjectLiveInstances :many
-- Instances of a project that still hold a container: up, on their way up or down, or paused.
SELECT i.* FROM instances i JOIN services s ON s.id = i.service_id
WHERE s.project_id = $1 AND i.state IN ('starting', 'running', 'paused', 'stopping');

-- name: ProjectObjectKeys :many
-- Every stored object a project owns: sources, files bundles, built binaries and uploaded binaries.
SELECT d.source_key::text AS key FROM deployments d WHERE d.project_id = $1 AND d.source_key IS NOT NULL
UNION SELECT d.files_key::text FROM deployments d WHERE d.project_id = $1 AND d.files_key IS NOT NULL
UNION SELECT a.object_key::text FROM artifacts a JOIN deployments d ON d.id = a.deployment_id WHERE d.project_id = $1
UNION SELECT u.object_key::text FROM uploaded_binaries u JOIN deployments d ON d.id = u.deployment_id WHERE d.project_id = $1;
