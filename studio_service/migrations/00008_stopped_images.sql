-- +goose Up
-- The images stopped in a run that keeps going (POST .../images/stop): the projector re-broadcasts agents.cancel.image
-- for them when the job reports running, in case agents picked it up after the stop.
ALTER TABLE catalog_runs ADD COLUMN stopped_image_ids uuid[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE catalog_runs DROP COLUMN stopped_image_ids;
