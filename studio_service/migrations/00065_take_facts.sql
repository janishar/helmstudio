-- +goose Up
-- Take facts (contracts/README.md "Take facts", API 1.104; plan 42): a video or audio take's ffmpeg facts and a video's
-- frame grid {facts, key, width, height, bytes, at} (key null for audio; null until made), and the facts job's tries.
ALTER TABLE takes ADD COLUMN facts jsonb, ADD COLUMN facts_tries smallint NOT NULL DEFAULT 0;
CREATE INDEX takes_facts_todo ON takes (created_at) WHERE facts IS NULL AND deleted_at IS NULL AND ready
	AND kind IN ('video', 'audio');

-- +goose Down
DROP INDEX takes_facts_todo;
ALTER TABLE takes DROP COLUMN facts_tries, DROP COLUMN facts;
