-- +goose Up
-- Sign-in is email and password only, and there are no plans (decisions.md 053). Emailed codes, Google and GitHub
-- sign-in, the beta badge and the plan column go. An instance's admin limits what a user's apps may use instead:
-- NULL means no limit.
DROP TABLE login_codes;
DROP TABLE oauth_identities;
ALTER TABLE users DROP COLUMN beta_tester;
ALTER TABLE teams DROP COLUMN plan;
ALTER TABLE teams
    ADD COLUMN max_memory_mb integer CHECK (max_memory_mb > 0),
    ADD COLUMN max_cpu_millis integer CHECK (max_cpu_millis > 0),
    ADD COLUMN max_concurrent_builds integer CHECK (max_concurrent_builds > 0);

-- +goose Down
ALTER TABLE teams DROP COLUMN max_concurrent_builds, DROP COLUMN max_cpu_millis, DROP COLUMN max_memory_mb;
ALTER TABLE teams ADD COLUMN plan text NOT NULL DEFAULT 'free';
ALTER TABLE users ADD COLUMN beta_tester boolean NOT NULL DEFAULT false;
CREATE TABLE oauth_identities (
    provider    text NOT NULL CHECK (provider IN ('google', 'github')),
    subject     text NOT NULL,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email       citext NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);
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
