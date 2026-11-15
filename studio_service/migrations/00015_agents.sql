-- +goose Up
-- The llm pack and agent nodes (contracts/README.md "The llm pack and agent nodes", API 1.14): an ask run's answer is
-- stored on the run (never a take); a pipeline batch keeps its worst case, its resolved models, its memory snapshot
-- and what it was run again from; every judge, write and decide visit pipelines reports is a decision.
ALTER TABLE runs
    ADD COLUMN answer jsonb,                                -- defs answer: {tries, seen, tokens_in, tokens_out, seconds}
    ADD COLUMN thumbs int;                                  -- an ask run: thumbnails it sends (image 1, video 3)

ALTER TABLE batches
    ADD COLUMN bounds         jsonb,                        -- {runs, llm_calls} per row at Submit
    ADD COLUMN models         jsonb,                        -- judge/write/decide id → its ask workflow
    ADD COLUMN memory         jsonb,                        -- {examples, notes, taken_at}: pipelines' snapshot
    ADD COLUMN again_of       uuid,                         -- "Run again" of this batch
    ADD COLUMN same_decisions boolean NOT NULL DEFAULT false;

ALTER TABLE batch_steps
    ADD COLUMN decision_id text,                            -- the decision this step derived
    ADD COLUMN memory      jsonb;                           -- {read, wrote}

CREATE TABLE decisions (                                    -- defs decision, the newest seq per id
    id         text PRIMARY KEY,                            -- <batch_id>:<row>:<attempt>:<node>:<visit>
    batch_id   uuid NOT NULL REFERENCES batches ON DELETE CASCADE,
    n          int NOT NULL,
    attempt    int NOT NULL,
    owner_id   uuid NOT NULL,
    seq        bigint NOT NULL,
    kind       text NOT NULL,                               -- judge | write | decide
    data       jsonb NOT NULL,
    workflow_title text,                                    -- the ask workflow's title in the batch's snapshot
    take_ids   uuid[] NOT NULL DEFAULT '{}',                -- a judge's candidates: the takes it affected
    user_pick  jsonb,                                       -- Suggest: the take ids picked ([] = rejected all)
    agreed     boolean,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX decisions_row ON decisions (batch_id, n, attempt);
CREATE INDEX decisions_takes ON decisions USING gin (take_ids);
ALTER TABLE decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE decisions FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON decisions USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- +goose Down
DROP TABLE decisions;
ALTER TABLE batch_steps DROP COLUMN memory, DROP COLUMN decision_id;
ALTER TABLE batches DROP COLUMN same_decisions, DROP COLUMN again_of, DROP COLUMN memory, DROP COLUMN models,
    DROP COLUMN bounds;
ALTER TABLE runs DROP COLUMN thumbs, DROP COLUMN answer;
