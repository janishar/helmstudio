-- +goose Up
-- Media in the chat (API 1.65, alpha.2 items 7, 8): the images attached to a user entry, in order.
ALTER TABLE assistant_entries ADD COLUMN takes uuid[];

-- +goose Down
ALTER TABLE assistant_entries DROP COLUMN takes;
