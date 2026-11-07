-- name: GetTeamGitHubApp :one
SELECT * FROM github_apps WHERE team_id = $1;

-- name: SaveTeamGitHubApp :exec
INSERT INTO github_apps (team_id, app_id, slug, name, html_url, client_id, client_secret_enc, private_key_enc, webhook_secret_enc, created_by)
VALUES (@team_id, @app_id, @slug, @name, @html_url, @client_id, @client_secret_enc, @private_key_enc, @webhook_secret_enc, @created_by)
ON CONFLICT (team_id) DO UPDATE SET
    app_id = EXCLUDED.app_id, slug = EXCLUDED.slug, name = EXCLUDED.name, html_url = EXCLUDED.html_url,
    client_id = EXCLUDED.client_id, client_secret_enc = EXCLUDED.client_secret_enc,
    private_key_enc = EXCLUDED.private_key_enc, webhook_secret_enc = EXCLUDED.webhook_secret_enc,
    created_by = EXCLUDED.created_by, created_at = now();

-- name: DeleteTeamGitHubApp :exec
DELETE FROM github_apps WHERE team_id = $1;

-- name: DeleteTeamInstallations :exec
DELETE FROM github_installations WHERE team_id = $1;
