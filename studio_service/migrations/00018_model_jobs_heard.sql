-- +goose Up
-- M5a fix: when studio last heard of a released model job (its release, then every evt.model.*): a job silent for
-- JobSilence fails and its command is purged, so it never blocks its host's queue.
ALTER TABLE model_jobs ADD COLUMN heard_at timestamptz;

-- +goose Down
ALTER TABLE model_jobs DROP COLUMN heard_at;
