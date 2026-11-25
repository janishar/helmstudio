-- +goose Up
-- Auto mode (API 1.68, alpha.2 item 12): the chat's switch (starts off); safe steps run without asking.
ALTER TABLE assistant_threads ADD COLUMN auto boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE assistant_threads DROP COLUMN auto;
