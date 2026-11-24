-- +goose Up
-- The assistant's default caps per user (owner 2026-11-24, "Generous"; API 1.56): a saved row still holding the old
-- defaults exactly moves to the new ones; any customised value keeps the row as it is.
UPDATE assistant_settings
SET limits = '{"model_calls_per_day": 2000, "tokens_per_day": 20000000, "look_at_per_hour": 200, "messages_per_minute": 30}'
WHERE limits = '{"model_calls_per_day": 200, "tokens_per_day": 2000000, "look_at_per_hour": 20, "messages_per_minute": 6}'::jsonb;

-- +goose Down
UPDATE assistant_settings
SET limits = '{"model_calls_per_day": 200, "tokens_per_day": 2000000, "look_at_per_hour": 20, "messages_per_minute": 6}'
WHERE limits = '{"model_calls_per_day": 2000, "tokens_per_day": 20000000, "look_at_per_hour": 200, "messages_per_minute": 30}'::jsonb;
