-- +goose Up
-- Batches (plan 7.2, 16.3; contracts/README.md "Batches and the pipeline engine"): one workflow run once per row of
-- inputs by pipelines_service. This is the record the UI reads; pipelines' LangGraph checkpoints live in its own
-- database. status is pipelines' (studio writes only queued); control is what the owner asked for last.
CREATE TABLE batches (
    id             uuid PRIMARY KEY,
    owner_id       uuid NOT NULL,
    session_id     uuid REFERENCES sessions ON DELETE SET NULL, -- where its runs are; a finished batch outlives it
    title          text NOT NULL,
    workflow_id    text NOT NULL,
    workflow_title text NOT NULL,
    snapshot       jsonb NOT NULL,                        -- the registry workflow (with node_schemas) at Submit
    inputs         jsonb NOT NULL,                        -- the base inputs
    status         text NOT NULL DEFAULT 'queued',        -- queued | running | paused | done | failed | stopped
    control        text NOT NULL DEFAULT 'run',           -- run | pause | stop
    reason         text,
    commands       int NOT NULL DEFAULT 1,                -- cmd.pipeline.* sent: Nats-Msg-Id <batch_id>:<commands>
    pipeline_seq   bigint NOT NULL DEFAULT 0,             -- seq of the last evt.pipeline.batch applied
    resumed_seq    bigint NOT NULL DEFAULT 0,             -- pipeline_seq at the last Resume: a paused reported later holds
    event_seq      bigint NOT NULL DEFAULT 0,             -- last batch_events.seq
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at     timestamptz,
    finished_at    timestamptz
);
CREATE INDEX batches_owner ON batches (owner_id, created_at DESC, id DESC);
CREATE INDEX batches_session ON batches (session_id);

CREATE TABLE batch_rows (
    batch_id     uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    n            int NOT NULL,                            -- the row number, from 1
    owner_id     uuid NOT NULL,
    attempt      int NOT NULL DEFAULT 1,
    inputs       jsonb NOT NULL,                          -- base, then the row's values, then its seed
    status       text NOT NULL DEFAULT 'queued',          -- pipelines' view: queued | running | succeeded | failed | cancelled
    run_id       uuid REFERENCES runs ON DELETE SET NULL,
    take_ids     uuid[] NOT NULL DEFAULT '{}',
    error        text,
    finished_at  timestamptz,
    pipeline_seq bigint NOT NULL DEFAULT 0,               -- seq of the last evt.pipeline.row applied
    PRIMARY KEY (batch_id, n)
);

CREATE TABLE batch_events (                               -- the batch's event log; data is read fresh (whole state)
    batch_id uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    seq      bigint NOT NULL,
    owner_id uuid NOT NULL,
    type     text NOT NULL,                               -- batch | row
    n        int,                                         -- the row, with type row
    ts       timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (batch_id, seq)
);

ALTER TABLE runs
    ADD COLUMN batch_id uuid REFERENCES batches ON DELETE SET NULL,
    ADD COLUMN batch_row int,
    ADD COLUMN idempotency_key text UNIQUE;                -- studio.runs.create: one key, one run
CREATE INDEX runs_batch ON runs (batch_id) WHERE batch_id IS NOT NULL;

ALTER TABLE takes ADD COLUMN batch_id uuid;               -- source pipeline: the batch that made it

-- Messages whose Nats-Msg-Id is not <job_id>:<attempt> (evt.studio.run_finished: <run_id>:finished).
ALTER TABLE outbox ADD COLUMN msg_id text;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['batches', 'batch_rows', 'batch_events'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE outbox DROP COLUMN msg_id;
ALTER TABLE takes DROP COLUMN batch_id;
ALTER TABLE runs DROP COLUMN idempotency_key, DROP COLUMN batch_row, DROP COLUMN batch_id;
DROP TABLE batch_events, batch_rows, batches;
