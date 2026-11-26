-- +goose Up
-- Save with the video (API 1.79, plan 38 phase M): a mux media job keeps its video take beside source_take_id (the
-- audio take); one job per pair is answered again while it runs or its take is kept.
ALTER TABLE media_jobs ADD COLUMN video_take_id uuid;
CREATE INDEX media_jobs_mux ON media_jobs (source_take_id, video_take_id, created_at DESC) WHERE kind = 'mux';

-- +goose Down
DROP INDEX media_jobs_mux;
ALTER TABLE media_jobs DROP COLUMN video_take_id;
