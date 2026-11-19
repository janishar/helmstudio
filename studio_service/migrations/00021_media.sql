-- +goose Up
-- Media inputs (contracts/README.md "Media inputs", API 1.19): an audio take's length as the client declared it at
-- upload ({duration_s}), and how a video made from a media take lines up with it (evt.run.output source_map:
-- {take_id, offset_s, new_in_s, new_out_s}). Both null otherwise.
ALTER TABLE takes ADD COLUMN audio jsonb, ADD COLUMN source_map jsonb;
-- A run's largest media facts {mp, frames} (a video source's size and frames, an extend's added frames, an audio
-- part's frames): its memory plan holds only up to planned_at (contracts/README.md, Media inputs: memory).
ALTER TABLE runs ADD COLUMN media_at jsonb;

-- +goose Down
ALTER TABLE runs DROP COLUMN media_at;
ALTER TABLE takes DROP COLUMN source_map, DROP COLUMN audio;
