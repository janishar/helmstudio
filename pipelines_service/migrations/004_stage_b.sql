-- M4b stage B: remote calls (queued and sent by pipelines itself, never runs: Pause holds them, Stop cancels them;
-- the per-batch caps count them), the batch's remote counters, and the director's chat (its state, what woke it
-- and is not read yet, its turns' results waiting for its thread, the last wake whose messages were published).
ALTER TABLE batches ADD COLUMN IF NOT EXISTS remote_calls     int NOT NULL DEFAULT 0;
ALTER TABLE batches ADD COLUMN IF NOT EXISTS remote_tokens    bigint NOT NULL DEFAULT 0;
ALTER TABLE batches ADD COLUMN IF NOT EXISTS network_failures int NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS remote_calls (
    key        text PRIMARY KEY,                   -- the call's idempotency key (as an ask run's would be)
    batch_id   uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    row        int NOT NULL,                       -- 0 = the director's chat
    provider   text NOT NULL,
    request    jsonb NOT NULL,                     -- {provider, model, system, prompt, schema?, tools?, max_tokens}
    status     text NOT NULL DEFAULT 'queued',     -- queued | sent | done
    result     jsonb,                              -- {status, take_ids, error, answer?, kind?, network_failures}
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS remote_calls_queued ON remote_calls (status, created_at);
CREATE TABLE IF NOT EXISTS directors (
    batch_id  uuid PRIMARY KEY REFERENCES batches ON DELETE CASCADE,
    node      text NOT NULL,
    state     text NOT NULL DEFAULT 'idle',        -- idle | waiting | thinking | off
    wakes     int NOT NULL DEFAULT 0,
    max_wakes int NOT NULL,
    results   jsonb NOT NULL DEFAULT '{}',         -- run id or call key → result, for its parked thread
    published int NOT NULL DEFAULT 0               -- the last wake whose evt.pipeline.chat messages went out
);
CREATE TABLE IF NOT EXISTS director_inbox (
    batch_id   uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    id         text NOT NULL,                      -- a chat message's id, or row:<n>:<attempt>
    value      jsonb NOT NULL,                     -- {message_id, text, at} | {row, status, error}
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (batch_id, id)
);
