-- +goose Up
-- Fairness: jobs with the same group (a team, for builds) run one at a time, so one team's burst of deploys
-- waits behind its own running build instead of in front of everyone else.
ALTER TABLE jobs ADD COLUMN group_key text;
CREATE INDEX jobs_running_group ON jobs (kind, group_key) WHERE status = 'running';

-- +goose Down
DROP INDEX jobs_running_group;
ALTER TABLE jobs DROP COLUMN group_key;
