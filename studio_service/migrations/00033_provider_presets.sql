-- +goose Up
-- Model provider presets (plan 25, API 1.51; contracts/README.md "Model sources: presets"): what a provider's OpenAI
-- layer refuses (compat, from a preset: Anthropic's) and the preset a provider was filled from (display only). Both
-- nullable: absent = the usual calls, no preset.
ALTER TABLE providers
    ADD COLUMN compat jsonb,                                -- defs providerCompat {temperature, strict_schema}
    ADD COLUMN preset text;                                 -- a preset id of contracts/providers/presets.json

-- +goose Down
ALTER TABLE providers DROP COLUMN compat, DROP COLUMN preset;
