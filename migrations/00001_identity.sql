-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       citext NOT NULL UNIQUE,
    name        text NOT NULL DEFAULT '',
    avatar_url  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE teams (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug             text NOT NULL UNIQUE,
    name             text NOT NULL,
    plan             text NOT NULL DEFAULT 'free',
    spend_cap_cents  integer NOT NULL DEFAULT 1000,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    team_id  uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id  uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role     text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE oauth_identities (
    provider    text NOT NULL CHECK (provider IN ('google', 'github')),
    subject     text NOT NULL,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email       citext NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

-- Only the SHA-256 of a code is stored; the code itself exists only in the email.
CREATE TABLE login_codes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email        citext NOT NULL,
    code_hash    bytea NOT NULL,
    attempts     integer NOT NULL DEFAULT 0,
    ip           text NOT NULL,
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_codes_email_created ON login_codes (email, created_at DESC);
CREATE INDEX login_codes_ip_created ON login_codes (ip, created_at DESC);

CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    user_agent    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

-- +goose Down
DROP TABLE sessions;
DROP TABLE login_codes;
DROP TABLE oauth_identities;
DROP TABLE memberships;
DROP TABLE teams;
DROP TABLE users;
