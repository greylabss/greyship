-- name: CreateLoginCode :exec
INSERT INTO login_codes (email, code_hash, ip, expires_at) VALUES ($1, $2, $3, $4);

-- name: CountCodesForEmailSince :one
SELECT count(*) FROM login_codes WHERE email = $1 AND created_at > $2;

-- name: CountCodesForIPSince :one
SELECT count(*) FROM login_codes WHERE ip = $1 AND created_at > $2;

-- name: LatestLoginCode :one
SELECT * FROM login_codes
WHERE email = $1 AND consumed_at IS NULL AND expires_at > $2
ORDER BY created_at DESC LIMIT 1
FOR UPDATE;

-- name: RecordCodeAttempt :one
UPDATE login_codes SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts;

-- name: ConsumeLoginCode :exec
UPDATE login_codes SET consumed_at = $2 WHERE id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (user_id, token_hash, user_agent, expires_at) VALUES ($1, $2, $3, $4);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = $1 AND expires_at > $2;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;
