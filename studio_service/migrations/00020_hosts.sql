-- +goose Up
-- M6, several hosts: the host attempt 1 was sent to when it stopped responding (Run.lost_host): attempt 2 goes to
-- another eligible host when there is one.
ALTER TABLE runs ADD COLUMN lost_host text;

-- +goose Down
ALTER TABLE runs DROP COLUMN lost_host;
