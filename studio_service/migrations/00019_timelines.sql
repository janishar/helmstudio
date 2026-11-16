-- +goose Up
-- M5b (API 1.17): sequences (contracts/README.md "Timeline"), their revisions (the last 100), the shot index (a
-- generation filling a slot) and media jobs (exports and frames, run by a runner's media worker). Every table is
-- owner-scoped by the app_can_see policy, so another user's id is 404.
CREATE TABLE timelines (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    name       text NOT NULL,
    doc        jsonb NOT NULL,                                -- the normalized document at revision (target, tracks, …)
    defaults   jsonb NOT NULL DEFAULT '{"cast": [], "looks": []}',
    revision   int NOT NULL DEFAULT 1,
    session_id uuid REFERENCES sessions ON DELETE SET NULL,   -- where its shots run
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz                                    -- soft delete
);
CREATE INDEX timelines_owner ON timelines (owner_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE timeline_revisions (                             -- every stored document; revision <= current - 100 pruned
    timeline_id uuid NOT NULL REFERENCES timelines ON DELETE CASCADE,
    owner_id    uuid NOT NULL,
    revision    int NOT NULL,
    doc         jsonb NOT NULL,
    by          text NOT NULL,                                -- user | studio (undo skips a studio write)
    undo_to     int,
    redo_to     int,
    saved_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (timeline_id, revision)
);

CREATE TABLE timeline_shots (                                 -- (run | batch + step) → the slot its take fills
    owner_id    uuid NOT NULL,
    timeline_id uuid NOT NULL REFERENCES timelines ON DELETE CASCADE,
    clip        text NOT NULL,
    run_id      uuid,
    batch_id    uuid,
    step        text,
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX timeline_shots_run ON timeline_shots (run_id) WHERE run_id IS NOT NULL;
CREATE INDEX timeline_shots_batch ON timeline_shots (batch_id, step) WHERE batch_id IS NOT NULL;

CREATE TABLE media_jobs (
    id             uuid PRIMARY KEY,
    owner_id       uuid NOT NULL,
    kind           text NOT NULL,                             -- export | frame
    status         text NOT NULL DEFAULT 'queued',            -- queued | running | succeeded | failed | cancelled
    host           text NOT NULL,
    env            text NOT NULL,
    timeline_id    uuid,                                      -- export
    timeline_name  text,                                      -- export: the name when exported
    revision       int,                                       -- export
    parents        jsonb,                                     -- export: the distinct takes of its clips, in order
    source_take_id uuid,                                      -- frame: the video take
    at             text,                                      -- frame: first | last
    mode           text,                                      -- export: copy | conform
    reason         text,
    frames         int,                                       -- export: the progress total
    progress       jsonb,                                     -- {frames, total}
    take_id        uuid,
    failure        jsonb,                                     -- {class, detail}
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    heard_at       timestamptz NOT NULL DEFAULT clock_timestamp(), -- its release, then every evt.media.*
    started_at     timestamptz,
    finished_at    timestamptz
);
CREATE INDEX media_jobs_open ON media_jobs (heard_at) WHERE status IN ('queued', 'running');
CREATE INDEX media_jobs_timeline ON media_jobs (timeline_id, created_at DESC) WHERE timeline_id IS NOT NULL;
CREATE INDEX media_jobs_frame ON media_jobs (source_take_id, at) WHERE kind = 'frame';

ALTER TABLE takes
    ADD COLUMN frame_of jsonb,                                -- source frame: {take_id, at}
    ADD COLUMN export   jsonb;                                -- source export: {timeline_id, timeline_name, revision, job_id, mode}
CREATE UNIQUE INDEX takes_frame_of ON takes (owner_id, (frame_of->>'take_id'), (frame_of->>'at'))
    WHERE frame_of IS NOT NULL AND deleted_at IS NULL;
ALTER TABLE batches ADD COLUMN timeline_id uuid;              -- a sequence's Re-render

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['timelines', 'timeline_revisions', 'timeline_shots', 'media_jobs'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE batches DROP COLUMN timeline_id;
DROP INDEX takes_frame_of;
ALTER TABLE takes DROP COLUMN export, DROP COLUMN frame_of;
DROP TABLE media_jobs, timeline_shots, timeline_revisions, timelines;
