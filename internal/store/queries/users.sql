-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: CreateUser :one
INSERT INTO users (email, name, avatar_url) VALUES ($1, $2, $3) RETURNING *;

-- name: FillUserProfile :exec
UPDATE users
SET name = CASE WHEN name = '' THEN sqlc.arg(name) ELSE name END,
    avatar_url = CASE WHEN avatar_url = '' THEN sqlc.arg(avatar_url) ELSE avatar_url END
WHERE id = sqlc.arg(id);

-- name: TeamSlugTaken :one
SELECT EXISTS (SELECT 1 FROM teams WHERE slug = $1);

-- name: CreateTeam :one
-- An empty plan means the default, free.
INSERT INTO teams (slug, name, plan) VALUES (sqlc.arg(slug), sqlc.arg(name), COALESCE(NULLIF(sqlc.arg(plan)::text, ''), 'free')) RETURNING *;

-- name: UserCount :one
SELECT count(*) FROM users;

-- name: MakeOwnerIfFirst :one
-- The first account on an instance owns it. Returns whether this call made the owner.
UPDATE users u SET is_owner = true
WHERE u.id = $1 AND NOT EXISTS (SELECT 1 FROM users o WHERE o.is_owner)
RETURNING true;

-- name: MakeOwnerByEmail :exec
UPDATE users SET is_owner = true WHERE email = $1;

-- name: AddMember :exec
INSERT INTO memberships (team_id, user_id, role) VALUES ($1, $2, $3);

-- name: ListUserTeams :many
SELECT t.*, m.role FROM teams t JOIN memberships m ON m.team_id = t.id
WHERE m.user_id = $1 ORDER BY t.created_at;

-- name: GetIdentity :one
SELECT * FROM oauth_identities WHERE provider = $1 AND subject = $2;

-- name: CreateIdentity :exec
INSERT INTO oauth_identities (provider, subject, user_id, email) VALUES ($1, $2, $3, $4);

-- name: MarkBetaTester :exec
UPDATE users SET beta_tester = true WHERE id = $1;
