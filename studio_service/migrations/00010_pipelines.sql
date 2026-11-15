-- +goose Up
-- Saved pipelines (contracts/README.md "Saved pipelines", "Batches of a pipeline", "Execution view", API 1.7): a user's
-- own pipeline document, batches that run one, the rows' gate state and cells, and every graph step pipelines reports.
CREATE TABLE user_pipelines (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    title      text NOT NULL,
    nodes      jsonb NOT NULL,                              -- node id → inputs | step | gate | output
    edges      jsonb NOT NULL,                              -- [from, to] | [gate, {pass, fail, max, then}]
    based_on   text,                                        -- the pipeline it was copied from (may be gone)
    version    int NOT NULL DEFAULT 1,                      -- +1 on every PUT and PATCH
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX user_pipelines_owner ON user_pipelines (owner_id, updated_at DESC);

ALTER TABLE batches
    ADD COLUMN pipeline_id      uuid,                       -- a batch of a pipeline (it may be deleted since)
    ADD COLUMN pipeline_title   text,
    ADD COLUMN pipeline_version int,
    ADD COLUMN collection       text,
    ADD COLUMN pipeline         jsonb;                      -- what it runs: the document, its workflows and their versions
CREATE INDEX batches_pipeline ON batches (pipeline_id, created_at DESC) WHERE pipeline_id IS NOT NULL;

ALTER TABLE batch_rows
    ADD COLUMN node         text,                           -- where the row is now (pipelines')
    ADD COLUMN parked       jsonb,                          -- {gate, visit, keep, candidates} while waiting
    ADD COLUMN cells        jsonb,                          -- pipelines' cells of the current attempt
    ADD COLUMN seeds        jsonb,                          -- step → base seed, drawn at Submit
    ADD COLUMN picks        jsonb NOT NULL DEFAULT '{}',    -- "<attempt>:<gate>:<visit>" → {take_ids} | {reject: true}
    ADD COLUMN retried_from jsonb NOT NULL DEFAULT '{}';    -- "<attempt>" → {attempt, step} (Retry row from here)

CREATE TABLE batch_steps (                                  -- evt.pipeline.step, the newest per (row, attempt, step)
    batch_id      uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    n             int NOT NULL,
    attempt       int NOT NULL,
    step          int NOT NULL,
    owner_id      uuid NOT NULL,
    seq           bigint NOT NULL,                          -- 0: copied by Retry row from here
    node          text NOT NULL,
    part          text NOT NULL,
    visit         int NOT NULL,
    status        text NOT NULL,                            -- finished | resumed | current
    what          text NOT NULL,
    changed       jsonb NOT NULL,
    edge          jsonb,
    loops         jsonb NOT NULL,
    run_ids       uuid[] NOT NULL DEFAULT '{}',
    take_ids      uuid[] NOT NULL DEFAULT '{}',
    error         text,
    checkpoint_id text,
    at            timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (batch_id, n, attempt, step)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['user_pipelines', 'batch_steps'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE batch_steps;
ALTER TABLE batch_rows DROP COLUMN retried_from, DROP COLUMN picks, DROP COLUMN seeds, DROP COLUMN cells,
    DROP COLUMN parked, DROP COLUMN node;
DROP INDEX batches_pipeline;
ALTER TABLE batches DROP COLUMN pipeline, DROP COLUMN collection, DROP COLUMN pipeline_version,
    DROP COLUMN pipeline_title, DROP COLUMN pipeline_id;
DROP TABLE user_pipelines;
