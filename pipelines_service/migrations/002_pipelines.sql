-- M2 stage C: saved pipelines. A row's seeds, where it is (node, parked, cells: evt.pipeline.row), the results of
-- its current step's runs (run id → {status, take_ids, error}, kept until every run of the visit has ended), a
-- gate's pick waiting to resume the thread, the last evt.pipeline.step published, and a retry's fork checkpoint.
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS seeds    jsonb NOT NULL DEFAULT '{}';
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS node     text;
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS parked   jsonb;
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS cells    jsonb;
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS results  jsonb NOT NULL DEFAULT '{}';
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS pick     jsonb;
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS step_pub int NOT NULL DEFAULT 0;
ALTER TABLE batch_rows ADD COLUMN IF NOT EXISTS fork     text;
