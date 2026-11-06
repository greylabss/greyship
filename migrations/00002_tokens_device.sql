-- +goose Up
-- API tokens for the CLI, CI and scripts (F-3). Only the hash is stored; prefix is shown so people can tell tokens apart.
CREATE TABLE tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          text NOT NULL,
    token_hash    bytea NOT NULL UNIQUE,
    prefix        text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz
);
CREATE INDEX tokens_user ON tokens (user_id);

-- Device flow for `grey login` (F-2, RFC 8628). The CLI holds device_code; the person types or confirms user_code.
CREATE TABLE device_codes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash  bytea NOT NULL UNIQUE,
    user_code         text NOT NULL UNIQUE,
    client_name       text NOT NULL,
    ip                text NOT NULL,
    status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    user_id           uuid REFERENCES users (id) ON DELETE CASCADE,
    team_id           uuid REFERENCES teams (id) ON DELETE CASCADE,
    last_polled_at    timestamptz,
    expires_at        timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE device_codes;
DROP TABLE tokens;
