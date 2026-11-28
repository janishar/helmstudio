-- +goose Up
-- Magic dialogue blocks that run (API 1.89, plan 39 gaps; contracts/README.md "Magic: dialogue, Run on and Loop"):
-- each turn of a dialogue is its own run, kept with its turn (its index in the request's `turns`).
ALTER TABLE magic_runs ADD COLUMN turn int;

-- +goose Down
ALTER TABLE magic_runs DROP COLUMN turn;
