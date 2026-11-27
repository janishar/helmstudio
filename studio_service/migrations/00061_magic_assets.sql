-- +goose Up
-- Magic assets and the storyboard (API 1.83, plan 39 phase E; contracts/README.md "Magic: assets and the
-- storyboard"): a run that makes an asset's views (Make views) is kept against the asset, not a block (`asset`, with
-- `block` ''), so the projector adds its take to the asset's sheet named by `label` (the view); a board frame's run
-- keeps the style it was drawn in as `label` (the frame dialog's Sketch · 2, Rough · 1).
ALTER TABLE magic_runs ADD COLUMN asset text, ADD COLUMN label text;
CREATE INDEX magic_runs_asset ON magic_runs (board_id, asset, created_at) WHERE asset IS NOT NULL;

-- +goose Down
DROP INDEX magic_runs_asset;
ALTER TABLE magic_runs DROP COLUMN asset, DROP COLUMN label;
