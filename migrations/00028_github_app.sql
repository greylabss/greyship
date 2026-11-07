-- +goose Up
-- Each team's own GitHub App, created from the dashboard with GitHub's app manifest flow (decisions.md 055). One per
-- team. The three secrets are sealed with the master key. Deleting an app removes its installations (and with them
-- the repositories connected through it).
CREATE TABLE github_apps (
    team_id            uuid PRIMARY KEY REFERENCES teams (id) ON DELETE CASCADE,
    app_id             bigint NOT NULL,
    slug               text NOT NULL,
    name               text NOT NULL,
    html_url           text NOT NULL,
    client_id          text NOT NULL,
    client_secret_enc  bytea NOT NULL,
    private_key_enc    bytea NOT NULL,
    webhook_secret_enc bytea NOT NULL,
    created_by         uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE github_apps;
