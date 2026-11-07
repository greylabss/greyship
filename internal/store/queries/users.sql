-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: CreateUser :one
INSERT INTO users (email, name) VALUES ($1, $2) RETURNING *;

-- name: TeamSlugTaken :one
SELECT EXISTS (SELECT 1 FROM teams WHERE slug = $1);

-- name: CreateTeam :one
INSERT INTO teams (slug, name) VALUES ($1, $2) RETURNING *;

-- name: UserCount :one
SELECT count(*) FROM users;

-- name: AddMember :exec
INSERT INTO memberships (team_id, user_id, role) VALUES ($1, $2, $3);

-- name: ListUserTeams :many
SELECT t.*, m.role FROM teams t JOIN memberships m ON m.team_id = t.id
WHERE m.user_id = $1 ORDER BY t.created_at;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: SetOwner :exec
UPDATE users SET is_owner = $2 WHERE id = $1;

-- name: CountOwners :one
SELECT count(*) FROM users WHERE is_owner;

-- name: RecordPasswordFailure :exec
INSERT INTO password_failures (email, ip) VALUES ($1, $2);

-- name: CountPasswordFailuresForEmail :one
SELECT count(*) FROM password_failures WHERE email = $1 AND created_at > $2;

-- name: CountPasswordFailuresForIP :one
SELECT count(*) FROM password_failures WHERE ip = $1 AND created_at > $2;

-- name: ClearPasswordFailures :exec
DELETE FROM password_failures WHERE email = $1;
