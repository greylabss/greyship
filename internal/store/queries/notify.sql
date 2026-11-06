-- name: DeploymentEvent :one
-- What a deploy notification says, and who started it. A push to GitHub has no starter; the person who connected
-- GitHub for the team gets it instead.
SELECT d.id, d.status, d.error, d.commit_sha, d.source, d.created_by, d.created_at, d.finished_at, d.rollback_of,
       p.id AS project_id, p.slug AS project_slug, t.id AS team_id, t.slug AS team_slug,
       coalesce((SELECT gi.created_by::text FROM project_repos r JOIN github_installations gi ON gi.id = r.installation_id
        WHERE r.project_id = p.id LIMIT 1), '')::text AS github_user
FROM deployments d JOIN projects p ON p.id = d.project_id JOIN teams t ON t.id = p.team_id
WHERE d.id = $1;

-- name: ProjectEvent :one
SELECT p.id, p.slug, t.id AS team_id, t.slug AS team_slug,
       coalesce((SELECT r.full_name FROM project_repos r WHERE r.project_id = p.id LIMIT 1), '')::text AS repo
FROM projects p JOIN teams t ON t.id = p.team_id
WHERE p.id = $1;

-- name: MemberEmail :one
-- The recipient's address, only while they still belong to the team.
SELECT u.email::text FROM users u JOIN memberships m ON m.user_id = u.id
WHERE u.id = sqlc.arg(user_id) AND m.team_id = sqlc.arg(team_id);

-- name: NotificationSetting :one
SELECT coalesce((SELECT s.email FROM notification_settings s WHERE s.user_id = sqlc.arg(user_id) AND s.kind = sqlc.arg(kind)), true)::bool AS email,
       coalesce((SELECT s.push FROM notification_settings s WHERE s.user_id = sqlc.arg(user_id) AND s.kind = sqlc.arg(kind)), true)::bool AS push;

-- name: CreateNotification :one
-- Repeating the same event (a retried job) returns the row already made, so nothing is sent twice.
INSERT INTO notifications (user_id, team_id, kind, subject_id, project_id, deployment_id, title, body, link)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (user_id, kind, subject_id) DO UPDATE SET kind = EXCLUDED.kind
RETURNING *;

-- name: MarkNotificationEmailed :exec
UPDATE notifications SET emailed_at = now() WHERE id = $1;

-- name: MarkNotificationPushed :exec
UPDATE notifications SET pushed_at = now() WHERE id = $1;

-- name: ListNotifications :many
SELECT * FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL;

-- name: MarkNotificationsRead :exec
UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL;

-- name: ListNotificationSettings :many
SELECT * FROM notification_settings WHERE user_id = $1;

-- name: SetNotificationSetting :exec
INSERT INTO notification_settings (user_id, kind, email, push) VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, kind) DO UPDATE SET email = EXCLUDED.email, push = EXCLUDED.push;

-- name: AddPushSubscription :one
-- A browser re-subscribing (or a new person signing in on it) takes the endpoint over.
INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (endpoint) DO UPDATE SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent
RETURNING *;

-- name: ListPushSubscriptions :many
SELECT * FROM push_subscriptions WHERE user_id = $1 ORDER BY created_at;

-- name: DeletePushSubscription :execrows
DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2;

-- name: DeletePushSubscriptionByEndpoint :exec
DELETE FROM push_subscriptions WHERE endpoint = $1;
