-- +goose Up
-- Audio crossfades by longer cues (API 1.106, plan 41; contracts/README.md "Audio fades, crossfades and ducking"): a
-- music or sound block with a crossfade after it runs that many seconds past its block, kept with the run.
ALTER TABLE magic_runs ADD COLUMN tail real;

-- +goose Down
ALTER TABLE magic_runs DROP COLUMN tail;
