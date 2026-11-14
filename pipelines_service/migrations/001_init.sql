-- pipelines DB: the batches this service runs (snapshot, control, per-batch event seq), each row's state, and the
-- command inbox. LangGraph's checkpoint tables are created by AsyncPostgresSaver.setup(). Runs on every start.
CREATE TABLE IF NOT EXISTS batches (
    batch_id    uuid PRIMARY KEY,
    owner_id    uuid NOT NULL,
    session_id  uuid NOT NULL,
    snapshot    jsonb NOT NULL,                    -- cmd.pipeline.submit data.snapshot: what the batch runs
    trace       jsonb NOT NULL,                    -- the submit command's trace, carried on every event
    control     text NOT NULL DEFAULT 'run',       -- run | pause | stop: may new rows start
    status      text NOT NULL DEFAULT 'running',   -- running | paused | done | failed | stopped (last published)
    reason      text,
    released    boolean NOT NULL DEFAULT false,    -- row 1 has ended once: every row may run
    seq         bigint NOT NULL DEFAULT 0,         -- last evt.pipeline.* seq (committed before the publish)
    fail_streak int NOT NULL DEFAULT 0,            -- rows failed in a row (finish order) with fail_error
    fail_error  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS batch_rows (
    batch_id uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    row      int NOT NULL,
    inputs   jsonb NOT NULL,
    attempt  int NOT NULL DEFAULT 1,
    status   text NOT NULL DEFAULT 'queued',       -- queued | running | succeeded | failed | cancelled
    run_id   uuid,
    take_ids jsonb,
    error    text,
    PRIMARY KEY (batch_id, row)
);
CREATE TABLE IF NOT EXISTS inbox (
    msg_id     text PRIMARY KEY,                   -- the command's Nats-Msg-Id (<batch_id>:<n>)
    created_at timestamptz NOT NULL DEFAULT now()
);
