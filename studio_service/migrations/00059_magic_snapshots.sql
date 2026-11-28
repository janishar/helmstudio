-- +goose Up
-- Board snapshots (API 1.80, plan 39 phase C): a named pointer to a kept revision; while it exists its revision is
-- never pruned. Unlimited per board (owner, 2026-11-27).
CREATE TABLE magic_snapshots (
    id       uuid PRIMARY KEY,
    board_id uuid NOT NULL REFERENCES magic_boards ON DELETE CASCADE,
    owner_id uuid NOT NULL,
    name     text NOT NULL,
    revision int NOT NULL,
    made_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX magic_snapshots_board ON magic_snapshots (board_id, made_at DESC);
ALTER TABLE magic_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE magic_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON magic_snapshots USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- +goose Down
DROP TABLE magic_snapshots;
