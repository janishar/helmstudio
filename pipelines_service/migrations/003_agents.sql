-- M4b stage A: agent nodes. A batch's memory snapshot (taken when it starts: every read of the batch uses it) and
-- the batch whose decisions it reuses (Run again: same decisions); every judge, write and decide visit's decision
-- (the replay source: a stored decision with the same replay key is reused instead of asking the model). Memory
-- itself is LangGraph's store (AsyncPostgresStore.setup()).
ALTER TABLE batches ADD COLUMN IF NOT EXISTS replay_from uuid;
ALTER TABLE batches ADD COLUMN IF NOT EXISTS memory      jsonb;   -- memoryItems, newest first; null: no pipeline
ALTER TABLE batches ADD COLUMN IF NOT EXISTS memory_at   timestamptz;
CREATE TABLE IF NOT EXISTS decisions (
    decision_id text PRIMARY KEY,                  -- <batch>:<row>:<attempt>:<node>:<visit>
    batch_id    uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    replay_key  text NOT NULL,                     -- <batch>:<row>:<node>:<visit>:<inputs hash>
    decision    jsonb NOT NULL,                    -- defs decision, as published
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS decisions_replay ON decisions (replay_key, created_at);
