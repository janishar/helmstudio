-- +goose Up
-- Magic boards (API 1.80, plan 39 phase C; contracts/README.md "Magic"): one document per board, edited by named ops,
-- stored as revisions (the last 100, and every snapshot's: 00059) with the undo and redo pointers, as a sequence's
-- (00019, 00031). Owner-scoped by app_can_see, so another user's board is 404.
CREATE TABLE magic_boards (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    name       text NOT NULL,
    doc        jsonb NOT NULL,                               -- MagicDoc at revision
    revision   int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz                                   -- soft delete
);
CREATE INDEX magic_boards_owner ON magic_boards (owner_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE magic_board_revisions (
    board_id    uuid NOT NULL REFERENCES magic_boards ON DELETE CASCADE,
    owner_id    uuid NOT NULL,
    revision    int NOT NULL,
    doc         jsonb NOT NULL,
    by          text NOT NULL,                               -- user | studio (undo skips a studio write)
    via         text,                                        -- a user write: web | api | mcp | assistant
    ops_summary text NOT NULL,                               -- an /edits call's op names, else the write's kind
    undo_to     int,
    redo_to     int,
    saved_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (board_id, revision)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['magic_boards', 'magic_board_revisions'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE magic_board_revisions, magic_boards;
