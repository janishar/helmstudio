-- API 1.66 (alpha.2 item 30): a model call's provider, model, prompt tokens and the prompt cache hits its reply
-- reported (null: none reported), for the hit rate per model in Admin › AI agents (pipelines.assistant.cache).
ALTER TABLE assistant_spend ADD COLUMN IF NOT EXISTS provider text;
ALTER TABLE assistant_spend ADD COLUMN IF NOT EXISTS model text;
ALTER TABLE assistant_spend ADD COLUMN IF NOT EXISTS prompt_tokens bigint;
ALTER TABLE assistant_spend ADD COLUMN IF NOT EXISTS hit_tokens bigint;
