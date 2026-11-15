-- +goose Up
-- The library (contracts/README.md "Elements", API 1.8): a user's elements now, looks (M3) in the same table. Images
-- are take ids only (never keys or URLs); one whose take is deleted is left out when read.
CREATE TABLE library_items (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    kind       text NOT NULL,                               -- element (look later)
    item_group text NOT NULL,                               -- character | location | prop (a look's groups later)
    name       text NOT NULL,
    handle     text,                                        -- what @ mentions; null for a look
    text       text NOT NULL DEFAULT '',
    images     uuid[] NOT NULL DEFAULT '{}',                -- takes of the owner, in order
    version    int NOT NULL DEFAULT 1,                      -- +1 on every PUT
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX library_items_handle ON library_items (owner_id, handle) WHERE handle IS NOT NULL;
CREATE INDEX library_items_owner ON library_items (owner_id, updated_at DESC);
ALTER TABLE library_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE library_items FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON library_items USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- A run keeps what the mention expansion did ({typed, elements}: made_with); a batch the copy of every element it
-- mentions, which its runs (Retry included) resolve against.
ALTER TABLE runs ADD COLUMN mentions jsonb;
ALTER TABLE batches ADD COLUMN elements jsonb;

-- +goose Down
ALTER TABLE batches DROP COLUMN elements;
ALTER TABLE runs DROP COLUMN mentions;
DROP TABLE library_items;
