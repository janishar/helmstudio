-- +goose Up
-- Thinking per model (plan 28, API 1.53; contracts/README.md "Model sources: thinking"): the level the admin set for
-- each of a provider's models, {model: level}. {} = nothing set (the provider's own default); models and
-- default_model are unchanged, so rows saved before read as they did.
ALTER TABLE providers ADD COLUMN thinking jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE providers DROP COLUMN thinking;
