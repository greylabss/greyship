-- +goose Up
-- Environment variables (F-40–F-48), encrypted at rest (F-41). project_keys holds each project's data key,
-- encrypted by the master key; env_vars holds values encrypted by that data key. service_id NULL is a main
-- (shared) variable; a service id is that service's override.
CREATE TABLE project_keys (
    project_id   uuid PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    wrapped_key  bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE env_vars (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    service_id   uuid REFERENCES services (id) ON DELETE CASCADE,
    env          text NOT NULL CHECK (env IN ('production', 'preview')),
    key          text NOT NULL CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,255}$'),
    ciphertext   bytea NOT NULL,
    value_hash   text NOT NULL CHECK (value_hash ~ '^[0-9a-f]{64}$'),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX env_vars_main ON env_vars (project_id, env, key) WHERE service_id IS NULL;
CREATE UNIQUE INDEX env_vars_override ON env_vars (service_id, env, key) WHERE service_id IS NOT NULL;

CREATE TABLE env_excludes (
    service_id  uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    env         text NOT NULL,
    key         text NOT NULL,
    PRIMARY KEY (service_id, env, key)
);

-- +goose Down
DROP TABLE env_excludes;
DROP TABLE env_vars;
DROP TABLE project_keys;
