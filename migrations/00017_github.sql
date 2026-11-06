-- +goose Up
-- Deploys from GitHub (spec F-22, decisions.md 042).
CREATE TABLE github_installations (
    id            bigint PRIMARY KEY, -- GitHub's installation id
    team_id       uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    account_login text NOT NULL,
    created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX github_installations_team ON github_installations (team_id);

-- One connected repo per project for now.
CREATE TABLE project_repos (
    project_id      uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    installation_id bigint NOT NULL REFERENCES github_installations(id) ON DELETE CASCADE,
    repo_id         bigint NOT NULL,
    full_name       text NOT NULL,
    branch          text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX project_repos_push ON project_repos (repo_id, branch);

ALTER TABLE deployments ADD COLUMN branch text;
ALTER TABLE deployments ADD COLUMN github_reported text;
-- A push delivered twice deploys once.
CREATE UNIQUE INDEX deployments_github_commit ON deployments (project_id, commit_sha) WHERE source = 'github';
-- A GitHub deployment has no source until the builder has fetched it.
ALTER TABLE deployments DROP CONSTRAINT deployments_build_source_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_build_source_check
    CHECK (build_mode = 'local' OR (source = 'github' AND status IN ('uploading', 'failed'))
           OR (source_key IS NOT NULL AND source_size IS NOT NULL AND source_sha256 IS NOT NULL));

-- +goose Down
ALTER TABLE deployments DROP CONSTRAINT deployments_build_source_check;
DELETE FROM deployments WHERE source = 'github' AND source_key IS NULL;
ALTER TABLE deployments ADD CONSTRAINT deployments_build_source_check
    CHECK (build_mode = 'local' OR (source_key IS NOT NULL AND source_size IS NOT NULL AND source_sha256 IS NOT NULL));
DROP INDEX deployments_github_commit;
ALTER TABLE deployments DROP COLUMN github_reported;
ALTER TABLE deployments DROP COLUMN branch;
DROP TABLE project_repos;
DROP TABLE github_installations;
