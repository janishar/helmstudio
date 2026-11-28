-- +goose Up
-- Magic board names as sequence names (API 1.91, 00054; contracts/README.md "Magic", Names): who wrote a board's name,
-- the model that named it, and whether the namer was asked (once per board). An existing "Untitled board" is default,
-- so its next stored document with a prompt names it. (A board's own session is made title_source user, so its runs
-- never name it: the board does.)
ALTER TABLE magic_boards
    ADD COLUMN title_source text NOT NULL DEFAULT 'user',  -- default | prompt | user | model
    ADD COLUMN named_by     text,                          -- the model's short name while title_source is model
    ADD COLUMN name_asked   boolean NOT NULL DEFAULT false; -- named from a block's prompt (once per board)
UPDATE magic_boards SET title_source = 'default' WHERE name = 'Untitled board';

-- +goose Down
ALTER TABLE magic_boards DROP COLUMN title_source, DROP COLUMN named_by, DROP COLUMN name_asked;
