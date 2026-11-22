-- API 1.42 (plan 18, 22.3): the in-app assistant's per-user caps, counted at the bound as remote calls are. One row per
-- model call (its bound, then its reported tokens) or look_at, keyed by the call so a replay counts once. The chats'
-- state is LangGraph's checkpoints (thread assistant:<thread_id>); memory is LangGraph's store.
CREATE TABLE IF NOT EXISTS assistant_spend (
    key      text PRIMARY KEY,                     -- <thread_id>:<turn>:m<n> (a model call) or a look_at call id
    owner_id uuid NOT NULL,
    kind     text NOT NULL,                        -- model | look
    tokens   bigint NOT NULL DEFAULT 0,
    at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS assistant_spend_owner ON assistant_spend (owner_id, kind, at);
