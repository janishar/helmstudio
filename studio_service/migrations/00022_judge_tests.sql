-- +goose Up
-- Judge tests (contracts/README.md "Judge test", API 1.28): the Judge panel's Test on takes, one ask run of a saved
-- judge on takes the user picked. result is pipelines' verdict once the run succeeded (or {error} when its answer
-- failed the checks); the test goes with its pipeline.
CREATE TABLE judge_tests (
    id          uuid PRIMARY KEY,
    owner_id    uuid NOT NULL,
    pipeline_id uuid NOT NULL REFERENCES user_pipelines ON DELETE CASCADE,
    node        text NOT NULL,
    take_ids    uuid[] NOT NULL,                        -- in the order the judge saw them
    criteria    jsonb NOT NULL,                         -- {must, nice} as tested
    keep        int NOT NULL,
    run_id      uuid REFERENCES runs ON DELETE SET NULL,
    result      jsonb,                                  -- {verdict, first_failed, picked, takes} or {error}
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE judge_tests ENABLE ROW LEVEL SECURITY;
ALTER TABLE judge_tests FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON judge_tests USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- +goose Down
DROP TABLE judge_tests;
