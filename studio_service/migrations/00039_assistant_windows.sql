-- +goose Up
-- The assistant's chat window per model (API 1.66, alpha.2 item 30): "<provider>/<model>" → tokens (model empty:
-- the provider's default model); a model not in it has 60000.
ALTER TABLE assistant_settings ADD COLUMN windows jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE assistant_settings DROP COLUMN windows;
