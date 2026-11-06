-- +goose Up
-- A project is one repo's worth of services (F-10). The slug is its name in URLs and default domains (F-10a).
CREATE TABLE projects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    name        text NOT NULL,
    slug        text NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,39}$'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, slug)
);

-- +goose Down
DROP TABLE projects;
