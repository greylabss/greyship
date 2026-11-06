-- +goose Up
-- Public repositories connected by URL, without the GitHub app (decisions.md 043): no installation, and updates
-- only on demand.
ALTER TABLE project_repos ALTER COLUMN installation_id DROP NOT NULL;
-- A commit whose deploy failed may be deployed again (after fixing its env vars, say); one that is live or on its
-- way may not.
DROP INDEX deployments_github_commit;
CREATE UNIQUE INDEX deployments_github_commit ON deployments (project_id, commit_sha) WHERE source = 'github' AND status <> 'failed';

-- +goose Down
DROP INDEX deployments_github_commit;
CREATE UNIQUE INDEX deployments_github_commit ON deployments (project_id, commit_sha) WHERE source = 'github';
DELETE FROM project_repos WHERE installation_id IS NULL;
ALTER TABLE project_repos ALTER COLUMN installation_id SET NOT NULL;
