-- +goose Up
-- Stop (API 1.59.1): the turn the user stopped; its done step takes the turn's unread steers as read, and no next
-- turn starts from them.
ALTER TABLE assistant_threads ADD COLUMN stopped_turn int;

-- +goose Down
ALTER TABLE assistant_threads DROP COLUMN stopped_turn;
