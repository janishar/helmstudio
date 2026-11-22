-- +goose Up
-- Thumbnails (contracts/README.md "Thumbnails", API 1.48; plan 22-thumbnails section 26): a take's WebP thumbnail
-- {key, width, height, bytes} (null until made, or when it would save nothing), the thumbs job's tries (at most 3),
-- and a thumbs media job's takes in input order (media_jobs.kind gains thumbs).
ALTER TABLE takes ADD COLUMN thumb jsonb, ADD COLUMN thumb_tries smallint NOT NULL DEFAULT 0;
ALTER TABLE media_jobs ADD COLUMN take_ids uuid[];
CREATE INDEX takes_thumb_todo ON takes (created_at) WHERE thumb IS NULL AND deleted_at IS NULL AND ready;

-- +goose Down
DROP INDEX takes_thumb_todo;
ALTER TABLE media_jobs DROP COLUMN take_ids;
ALTER TABLE takes DROP COLUMN thumb_tries, DROP COLUMN thumb;
