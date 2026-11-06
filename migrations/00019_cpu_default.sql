-- +goose Up
-- Each instance may use up to one CPU (a cap, not a reservation: an idle instance uses none, and billing follows
-- running time). A quarter of a CPU made busy apps slow (docs/test-runs/2026-11-04-testapi.md).
ALTER TABLE services ALTER COLUMN cpu_millis SET DEFAULT 1000;
UPDATE services SET cpu_millis = 1000 WHERE cpu_millis = 250;

-- +goose Down
UPDATE services SET cpu_millis = 250 WHERE cpu_millis = 1000;
ALTER TABLE services ALTER COLUMN cpu_millis SET DEFAULT 250;
