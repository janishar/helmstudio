-- +goose Up
-- Optional add-ons (contracts/README.md "Optional add-ons", API 1.36): a download or delete job of a model's add-on
-- names it here, with variant null.
ALTER TABLE model_jobs ADD COLUMN addon text;

-- +goose Down
ALTER TABLE model_jobs DROP COLUMN addon;
