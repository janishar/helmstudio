-- +goose Up
-- Magic: Send to Timeline (API 1.82, plan 39 phase G; contracts/README.md "Magic: Send to Timeline"): each send of a
-- board (its revision is kept while it is listed), the sequence's document it waits to write while its bakes run
-- (`plan`: the doc and which V1 clip each bake job fills), the sequence once made; media_jobs.send_id ties a bake job
-- to its send. Owner-scoped by app_can_see.
CREATE TABLE magic_sends (
    id          uuid PRIMARY KEY,
    owner_id    uuid NOT NULL,
    board_id    uuid NOT NULL REFERENCES magic_boards ON DELETE CASCADE,
    n           int NOT NULL,                       -- the board's nth send (its default name)
    name        text NOT NULL,
    revision    int NOT NULL,
    status      text NOT NULL DEFAULT 'baking',     -- baking | done | failed
    plan        jsonb NOT NULL,
    timeline_id uuid,
    failure     text,
    made_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    removed_at  timestamptz                         -- Remove from list (the sequence stays)
);
CREATE INDEX magic_sends_board ON magic_sends (board_id, made_at DESC);

ALTER TABLE media_jobs ADD COLUMN send_id uuid;
CREATE INDEX media_jobs_send ON media_jobs (send_id) WHERE send_id IS NOT NULL;

ALTER TABLE magic_sends ENABLE ROW LEVEL SECURITY;
ALTER TABLE magic_sends FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON magic_sends USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- +goose Down
DROP INDEX media_jobs_send;
ALTER TABLE media_jobs DROP COLUMN send_id;
DROP TABLE magic_sends;
