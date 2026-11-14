-- +goose Up
-- An input changed while a processing job ran: the projector queues a checks-only job once that one ends, so
-- processing jobs of a session never overlap.
ALTER TABLE catalog_sessions ADD COLUMN recheck boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE catalog_sessions DROP COLUMN recheck;
