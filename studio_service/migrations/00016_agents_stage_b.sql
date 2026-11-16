-- +goose Up
-- Agents, fan-out, model sources and custom nodes (contracts/README.md "Agents, fan-out, model sources and custom
-- nodes", API 1.15): a pipeline's remote switch; the admin's model providers (no owner: deployment settings, written
-- only through the admin API; a remote one names its key's secret file, never the key); a pipeline batch's providers,
-- egress, reserved memory, director and remote counters; the director's chat; the agent tool calls studio served
-- (idempotent on call_id); rows an agent added.
ALTER TABLE user_pipelines ADD COLUMN remote jsonb;         -- PipelineRemote; null = off

CREATE TABLE providers (
    name          text PRIMARY KEY,                         -- ^[a-z][a-z0-9-]{0,31}$
    kind          text NOT NULL,                            -- server | remote
    api           text NOT NULL DEFAULT 'openai',
    title         text,
    base_url      text NOT NULL,
    models        text[] NOT NULL,
    default_model text NOT NULL,
    enabled       boolean NOT NULL DEFAULT true,
    timeout_s     int NOT NULL DEFAULT 120,
    host          text,                                     -- server: the GPU host whose memory it shares
    memory_gb     double precision,                         -- server: reserved on host while enabled
    key_secret    text,                                     -- remote: the secret file's name
    max_in_flight int,                                      -- remote: calls in flight over every batch
    created_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at    timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE batches
    ADD COLUMN providers  text[] NOT NULL DEFAULT '{}',     -- the providers its snapshot names (DELETE is 409 meanwhile)
    ADD COLUMN egress     jsonb,                            -- [PipelineEgress] at Submit
    ADD COLUMN reserved   jsonb,                            -- [PipelineReserved] at Submit
    ADD COLUMN director   jsonb,                            -- {node, state, wakes, max_wakes}; null without a director
    ADD COLUMN remote     jsonb,                            -- {calls, tokens, network_failures, max_calls, max_tokens}
    ADD COLUMN rows_added int NOT NULL DEFAULT 0;           -- rows.add so far (at most 100)

ALTER TABLE batch_events ADD COLUMN message_id uuid;       -- type chat: the message

ALTER TABLE batch_steps                                    -- a fanned-out step's branch; an agent's turn and its tool calls
    ADD COLUMN branch     int,
    ADD COLUMN turn       int,
    ADD COLUMN tool_calls jsonb;

ALTER TABLE runs ADD COLUMN pin_host text;                 -- a server-ask run: its provider's host, the only one it goes to

CREATE TABLE batch_chat (                                   -- the director's chat, oldest first
    id         uuid PRIMARY KEY,
    batch_id   uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    owner_id   uuid NOT NULL,
    role       text NOT NULL,                               -- user | director | notice
    text       text NOT NULL,
    wake       int,
    tool_calls jsonb NOT NULL DEFAULT '[]',
    rows       int[] NOT NULL DEFAULT '{}',
    seq        bigint NOT NULL DEFAULT 0,                   -- the evt.pipeline.chat seq (a republish replaces)
    at         timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX batch_chat_batch ON batch_chat (batch_id, at, id);

CREATE TABLE tool_calls (                                   -- studio.tools: the first reply per call_id
    call_id    text PRIMARY KEY,
    batch_id   uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    owner_id   uuid NOT NULL,
    tool       text NOT NULL,
    reply      jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['batch_chat', 'tool_calls'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE tool_calls, batch_chat;
ALTER TABLE runs DROP COLUMN pin_host;
ALTER TABLE batch_steps DROP COLUMN tool_calls, DROP COLUMN turn, DROP COLUMN branch;
ALTER TABLE batch_events DROP COLUMN message_id;
ALTER TABLE batches DROP COLUMN rows_added, DROP COLUMN remote, DROP COLUMN director, DROP COLUMN reserved,
    DROP COLUMN egress, DROP COLUMN providers;
DROP TABLE providers;
ALTER TABLE user_pipelines DROP COLUMN remote;
