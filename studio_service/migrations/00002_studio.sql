-- +goose Up
-- Sessions, their runs, the runs' outputs and the per-session event log. Every row has owner_id and the app_can_see
-- policy, so another user's id is never visible (404).
CREATE TABLE sessions (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    title      text NOT NULL,
    event_seq  bigint NOT NULL DEFAULT 0,                   -- last run_events.seq of the session
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() -- last rename or run
);
CREATE INDEX sessions_owner ON sessions (owner_id, created_at DESC);

CREATE TABLE runs (
    id                  uuid PRIMARY KEY,
    owner_id            uuid NOT NULL,
    session_id          uuid NOT NULL REFERENCES sessions ON DELETE CASCADE,
    workflow_id         text NOT NULL,
    env                 text NOT NULL,                      -- the workflow's runner env
    inputs              jsonb NOT NULL,                     -- RunRequest.inputs as requested
    status              text NOT NULL,                      -- queued | running | succeeded | failed | cancelled
    progress            jsonb,                              -- the last evt.run.progress {node, step, of}
    error               text,
    host                text,                               -- where the command went; null when none was live
    attempt             int NOT NULL DEFAULT 1,
    cancel_requested_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at          timestamptz,
    finished_at         timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT clock_timestamp() -- last sign of life: the lease sweep reads it
);
CREATE INDEX runs_session ON runs (session_id, created_at DESC);
CREATE INDEX runs_open ON runs (updated_at) WHERE status IN ('queued', 'running');

CREATE TABLE run_outputs (                                  -- evt.run.output: object keys, never bytes
    run_id   uuid NOT NULL REFERENCES runs ON DELETE CASCADE,
    owner_id uuid NOT NULL,
    idx      int NOT NULL,
    key      text NOT NULL,
    kind     text NOT NULL,                                 -- image | video
    width    int,
    height   int,
    bytes    bigint NOT NULL,
    PRIMARY KEY (run_id, idx)
);

CREATE TABLE run_events (                                   -- the session's event log, per-session seq
    session_id uuid NOT NULL REFERENCES sessions ON DELETE CASCADE,
    seq        bigint NOT NULL,
    owner_id   uuid NOT NULL,
    run_id     uuid NOT NULL,
    type       text NOT NULL,                               -- status | progress | output
    ts         timestamptz NOT NULL,
    data       jsonb NOT NULL,                              -- output events keep the key; it is signed when read
    PRIMARY KEY (session_id, seq)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['sessions', 'runs', 'run_outputs', 'run_events'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE run_events, run_outputs, runs, sessions;
