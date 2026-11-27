-- +goose Up
-- Magic runs (API 1.81, plan 39 phase D; contracts/README.md "Magic: runs"): a board's own session (made at its first
-- run, hidden from Create's list), the run → block index the projector uses to add a take to its block (as
-- timeline_shots), with the hash of the block's inputs when it ran (out of date = they differ now), and the Magic
-- settings (ask above this many GPU minutes, default 5). Owner-scoped by app_can_see.
ALTER TABLE magic_boards ADD COLUMN session_id uuid;
CREATE INDEX magic_boards_session ON magic_boards (session_id) WHERE session_id IS NOT NULL;

CREATE TABLE magic_runs (
    run_id      uuid PRIMARY KEY,
    owner_id    uuid NOT NULL,
    board_id    uuid NOT NULL REFERENCES magic_boards ON DELETE CASCADE,
    block       text NOT NULL,
    inputs_hash text NOT NULL,
    seed        bigint,
    via         text,                                         -- web | api | mcp | assistant
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX magic_runs_board ON magic_runs (board_id, block, created_at);

CREATE TABLE magic_settings (
    owner_id              uuid PRIMARY KEY,
    ask_above_gpu_minutes double precision NOT NULL DEFAULT 5
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['magic_runs', 'magic_settings'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE magic_settings, magic_runs;
DROP INDEX magic_boards_session;
ALTER TABLE magic_boards DROP COLUMN session_id;
