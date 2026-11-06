-- +goose Up
-- Notifications (decisions.md 046): one row per person per event, shown in the dashboard bell and sent by email
-- and browser push. emailed_at and pushed_at let a retried job finish what it didn't send without repeating it.
CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('deploy.succeeded', 'deploy.failed', 'project.created')),
    subject_id uuid NOT NULL, -- the deployment, or the project for project.created
    project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
    deployment_id uuid REFERENCES deployments(id) ON DELETE CASCADE,
    title text NOT NULL,
    body text NOT NULL,
    link text NOT NULL, -- dashboard path, e.g. /acme/shop/deployments/<id>
    read_at timestamptz,
    emailed_at timestamptz,
    pushed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind, subject_id)
);
CREATE INDEX notifications_user ON notifications (user_id, created_at DESC);

CREATE TABLE push_subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint text NOT NULL UNIQUE,
    p256dh text NOT NULL,
    auth text NOT NULL,
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);

-- No row means on.
CREATE TABLE notification_settings (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('deploy.succeeded', 'deploy.failed', 'project.created')),
    email boolean NOT NULL DEFAULT true,
    push boolean NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, kind)
);

-- +goose Down
DROP TABLE notification_settings;
DROP TABLE push_subscriptions;
DROP TABLE notifications;
