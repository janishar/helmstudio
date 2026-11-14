-- +goose Up
-- The fair dispatcher (plan 17.5, contracts/README.md "The dispatcher"): a run waits in this table until the dispatcher
-- releases it to one host. lane orders the queue; nodes is the graph resolved at create, sent at release; released_at
-- is when the current attempt's command was written (null while waiting); a host is busy while it has a released run
-- that is not terminal.
ALTER TABLE runs
    ADD COLUMN lane text NOT NULL DEFAULT 'interactive',      -- interactive | batch
    ADD COLUMN nodes jsonb,                                   -- the resolved graph cmd.run.execute carries
    ADD COLUMN released_at timestamptz;
-- M0 placed every run at create: those with a host were released then.
UPDATE runs SET released_at = created_at WHERE host IS NOT NULL;
CREATE INDEX runs_waiting ON runs (created_at) WHERE status = 'queued' AND released_at IS NULL;
CREATE INDEX runs_released ON runs (owner_id, released_at) WHERE released_at IS NOT NULL;

-- The projector dedupes on (job_id, attempt, seq): seq restarts at 1 for each attempt.
ALTER TABLE projector_seen ADD COLUMN attempt int NOT NULL DEFAULT 1;
ALTER TABLE projector_seen DROP CONSTRAINT projector_seen_pkey, ADD PRIMARY KEY (job_id, attempt, seq);

-- +goose Down
ALTER TABLE projector_seen DROP CONSTRAINT projector_seen_pkey, ADD PRIMARY KEY (job_id, seq);
ALTER TABLE projector_seen DROP COLUMN attempt;
DROP INDEX runs_released, runs_waiting;
ALTER TABLE runs DROP COLUMN released_at, DROP COLUMN nodes, DROP COLUMN lane;
