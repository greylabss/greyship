-- name: CreateToken :one
INSERT INTO tokens (team_id, user_id, name, token_hash, prefix) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetTokenByHash :one
SELECT * FROM tokens WHERE token_hash = $1;

-- name: TouchToken :exec
UPDATE tokens SET last_used_at = $2 WHERE id = $1;

-- name: ListTokens :many
SELECT t.*, tm.slug AS team_slug FROM tokens t JOIN teams tm ON tm.id = t.team_id
WHERE t.user_id = $1 ORDER BY t.created_at DESC;

-- name: DeleteToken :execrows
DELETE FROM tokens WHERE id = $1 AND user_id = $2;

-- name: GetMembership :one
SELECT t.*, m.role FROM teams t JOIN memberships m ON m.team_id = t.id
WHERE m.user_id = $1 AND t.slug = $2;

-- name: GetTeamByID :one
SELECT * FROM teams WHERE id = $1;

-- name: CreateDeviceCode :exec
INSERT INTO device_codes (device_code_hash, user_code, client_name, ip, expires_at) VALUES ($1, $2, $3, $4, $5);

-- name: GetDeviceByUserCode :one
SELECT * FROM device_codes WHERE user_code = $1 AND expires_at > $2;

-- name: GetDeviceByHash :one
SELECT * FROM device_codes WHERE device_code_hash = $1 FOR UPDATE;

-- name: SetDevicePolled :exec
UPDATE device_codes SET last_polled_at = $2 WHERE id = $1;

-- name: DecideDevice :execrows
UPDATE device_codes SET status = $2, user_id = $3, team_id = $4
WHERE user_code = $1 AND status = 'pending' AND expires_at > $5;

-- name: ConsumeDevice :exec
UPDATE device_codes SET status = 'consumed' WHERE id = $1;
