-- +goose Up
-- Video takes (contracts/README.md "Video", API 1.11): a video take's file is the MP4 (takes.key), and video holds what
-- the runner's evt.run.output reported: {duration_s, fps, frames, has_audio, poster: {key, width, height, bytes}}. The
-- poster (under the same owner prefix) is the take's thumbnail; the sweep deletes it with the MP4. Null for an image.
ALTER TABLE takes ADD COLUMN video jsonb;
ALTER TABLE run_outputs ADD COLUMN video jsonb;

-- +goose Down
ALTER TABLE run_outputs DROP COLUMN video;
ALTER TABLE takes DROP COLUMN video;
