-- +goose Up
-- The release command (F-29, decision 012) runs as a one-off instance, so it is recorded as a service of type release.
ALTER TABLE services DROP CONSTRAINT services_type_check;
ALTER TABLE services ADD CONSTRAINT services_type_check CHECK (type IN ('web', 'worker', 'cron', 'release'));

-- +goose Down
DELETE FROM services WHERE type = 'release';
ALTER TABLE services DROP CONSTRAINT services_type_check;
ALTER TABLE services ADD CONSTRAINT services_type_check CHECK (type IN ('web', 'worker', 'cron'));
