-- +goose Up
-- The in-app assistant (contracts/README.md "Assistant", API 1.42; plan 18, 22.3): chats (threads) and their rows,
-- events and the admin's settings. Pipelines runs the agent and keeps its checkpoints and memory; studio keeps what
-- the drawer shows, the tab that runs a chat's actions and what a parked turn waits on.
CREATE TABLE assistant_threads (
    id              uuid PRIMARY KEY,
    owner_id        uuid NOT NULL,
    title           text NOT NULL DEFAULT 'New chat',
    state           text NOT NULL DEFAULT 'idle',            -- idle | working | waiting
    client_id       text,                                    -- the tab that runs its actions
    pending         jsonb,                                   -- AssistantPending while waiting
    caps            jsonb NOT NULL DEFAULT '{"model_calls": 0, "tool_calls": 0, "memory_writes": 0}',
    roles           text[] NOT NULL DEFAULT '{}',            -- the owner's roles at the last input (studio.tools checks them)
    actions         text[] NOT NULL DEFAULT '{}',            -- the screen's actions at the last screen summary
    turn            int NOT NULL DEFAULT 0,                  -- inputs sent (cmd.assistant.turn's turn)
    step_seq        int NOT NULL DEFAULT 0,                  -- the last evt.assistant.step applied
    event_seq       bigint NOT NULL DEFAULT 0,               -- the last event of GET …/events
    last_message_at timestamptz,                             -- the last message (the 24 h summary)
    summarized_at   timestamptz,                             -- its summary was asked for since that message
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at      timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX assistant_threads_owner ON assistant_threads (owner_id, updated_at DESC, id DESC);

CREATE TABLE assistant_entries (                             -- AssistantEntry: messages, words, lines, cards, notices
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id uuid NOT NULL REFERENCES assistant_threads ON DELETE CASCADE,
    owner_id  uuid NOT NULL,
    key       text NOT NULL,                                 -- a line or card: its call id (same id replaces)
    turn      int NOT NULL,
    kind      text NOT NULL,                                 -- user | assistant | line | card | notice
    at        timestamptz NOT NULL DEFAULT clock_timestamp(),
    text      text,
    line      jsonb,
    card      jsonb,
    error     jsonb,
    UNIQUE (thread_id, key)
);
CREATE INDEX assistant_entries_user ON assistant_entries (owner_id, at) WHERE kind = 'user'; -- messages a minute

CREATE TABLE assistant_events (                              -- GET …/events, numbered per chat
    thread_id uuid NOT NULL REFERENCES assistant_threads ON DELETE CASCADE,
    seq       bigint NOT NULL,
    owner_id  uuid NOT NULL,
    body      jsonb NOT NULL,                                -- AssistantEvent without seq
    PRIMARY KEY (thread_id, seq)
);

CREATE TABLE assistant_tool_calls (                          -- studio.tools for the assistant: the first reply per call
    call_id    text PRIMARY KEY,
    thread_id  uuid NOT NULL REFERENCES assistant_threads ON DELETE CASCADE,
    owner_id   uuid NOT NULL,
    tool       text NOT NULL,
    reply      jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['assistant_threads', 'assistant_entries', 'assistant_events', 'assistant_tool_calls'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

CREATE TABLE assistant_settings (                            -- one row: the admin's settings (no owner, like providers)
    id              boolean PRIMARY KEY DEFAULT true CHECK (id),
    enabled         boolean NOT NULL,
    provider        text,
    model           text,
    web_search      boolean NOT NULL,
    search_provider text,
    limits          jsonb NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE assistant_settings, assistant_tool_calls, assistant_events, assistant_entries, assistant_threads;
