-- +goose Up
-- Steer while it answers (API 1.59; board Assistant frame 8): a message sent while a turn works is a user entry with
-- steer waiting | read | next, and `next` holds the command it starts when the turn ends before reading it.
ALTER TABLE assistant_entries
    ADD COLUMN steer text CHECK (steer IN ('waiting', 'read', 'next')),
    ADD COLUMN next  jsonb;

-- +goose Down
ALTER TABLE assistant_entries DROP COLUMN steer, DROP COLUMN next;
