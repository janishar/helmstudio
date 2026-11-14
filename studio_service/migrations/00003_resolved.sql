-- +goose Up
-- What the runner chose for a run (a random seed): evt.run.status succeeded data.resolved, and each output's seed.
ALTER TABLE runs ADD COLUMN resolved jsonb;
ALTER TABLE run_outputs ADD COLUMN seed bigint;

-- +goose Down
ALTER TABLE run_outputs DROP COLUMN seed;
ALTER TABLE runs DROP COLUMN resolved;
