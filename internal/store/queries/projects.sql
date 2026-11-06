-- name: GetMembershipByTeamID :one
SELECT t.*, m.role FROM teams t JOIN memberships m ON m.team_id = t.id
WHERE m.user_id = $1 AND t.id = $2;

-- name: CreateProject :one
INSERT INTO projects (team_id, name, slug) VALUES ($1, $2, $3)
ON CONFLICT (team_id, slug) DO NOTHING
RETURNING *;

-- name: ListProjects :many
SELECT * FROM projects WHERE team_id = $1 ORDER BY created_at DESC;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE team_id = $1 AND slug = $2;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- name: RenameProject :one
UPDATE projects SET name = $2, slug = $3 WHERE id = $1 RETURNING *;
