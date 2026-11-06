-- +goose Up
-- Every public web service gets <service>-<project>.<apps domain> (F-50). Labels are unique across all
-- users (F-50a) and valid DNS labels of at most 63 characters (F-50b). Once assigned, a service keeps it.
ALTER TABLE services ADD COLUMN subdomain text UNIQUE
    CHECK (subdomain ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$');

-- +goose Down
ALTER TABLE services DROP COLUMN subdomain;
