-- +goose Up
-- Passwords for accounts the instance's admin creates (decisions.md 051). Empty means the account has none and
-- signs in with email codes (or Google or GitHub) as before. Hashes are PBKDF2-HMAC-SHA256 in a self-describing format.
ALTER TABLE users ADD COLUMN password_hash text NOT NULL DEFAULT '';

-- Failed password logins, for rate limiting by email and by address.
CREATE TABLE password_failures (
    id         bigserial PRIMARY KEY,
    email      citext NOT NULL,
    ip         text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_failures_email ON password_failures (email, created_at);
CREATE INDEX password_failures_ip ON password_failures (ip, created_at);

-- +goose Down
DROP TABLE password_failures;
ALTER TABLE users DROP COLUMN password_hash;
