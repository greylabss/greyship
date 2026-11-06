-- name: AddInstallation :exec
-- The person who installed it proved on GitHub that they can access it; it belongs to their team from now on.
INSERT INTO github_installations (id, team_id, account_login, created_by) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET team_id = EXCLUDED.team_id, account_login = EXCLUDED.account_login, created_by = EXCLUDED.created_by;

-- name: TeamInstallations :many
SELECT * FROM github_installations WHERE team_id = $1 ORDER BY created_at;

-- name: GetInstallation :one
SELECT * FROM github_installations WHERE id = $1;

-- name: ConnectRepo :one
INSERT INTO project_repos (project_id, installation_id, repo_id, full_name, branch) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (project_id) DO UPDATE SET installation_id = EXCLUDED.installation_id, repo_id = EXCLUDED.repo_id,
    full_name = EXCLUDED.full_name, branch = EXCLUDED.branch
RETURNING *;

-- name: ProjectRepo :one
SELECT * FROM project_repos WHERE project_id = $1;

-- name: ProjectsForPush :many
-- The projects a push deploys: those connected to this repo and branch.
SELECT r.*, p.team_id FROM project_repos r JOIN projects p ON p.id = r.project_id
WHERE r.repo_id = $1 AND r.branch = $2 AND r.installation_id IS NOT NULL;

-- name: CreateGithubDeployment :one
-- Returns no row when this commit already has a deployment in the project (a repeated delivery).
INSERT INTO deployments (id, project_id, source, commit_sha, branch, created_by)
VALUES ($1, $2, 'github', $3, $4, $5)
ON CONFLICT (project_id, commit_sha) WHERE source = 'github' AND status <> 'failed' DO NOTHING
RETURNING *;

-- name: SetFetchedSource :one
UPDATE deployments SET source_key = $2, source_size = $3, source_sha256 = $4
WHERE id = $1 AND status = 'uploading' AND source = 'github'
RETURNING *;

-- name: FailFetch :exec
UPDATE deployments SET status = 'failed', error = $2, finished_at = now() WHERE id = $1 AND status = 'uploading';

-- name: GithubStatusesDue :many
-- GitHub deployments whose commit status on GitHub is behind their state. superseded was reported as success.
SELECT d.id, d.commit_sha, d.status, d.error, r.full_name, r.installation_id, p.slug AS project_slug, t.slug AS team_slug,
       CASE d.status WHEN 'ready' THEN 'success' WHEN 'failed' THEN 'failure' WHEN 'superseded' THEN 'success' ELSE 'pending' END::text AS state
FROM deployments d
JOIN project_repos r ON r.project_id = d.project_id
JOIN projects p ON p.id = d.project_id
JOIN teams t ON t.id = p.team_id
WHERE d.source = 'github' AND d.commit_sha IS NOT NULL AND r.installation_id IS NOT NULL AND d.created_at > now() - interval '1 day'
  AND coalesce(d.github_reported, '') <> CASE d.status WHEN 'ready' THEN 'success' WHEN 'failed' THEN 'failure' WHEN 'superseded' THEN 'success' ELSE 'pending' END
LIMIT 50;

-- name: MarkGithubReported :exec
UPDATE deployments SET github_reported = $2 WHERE id = $1;
