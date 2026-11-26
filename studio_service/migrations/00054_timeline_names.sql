-- +goose Up
-- Sequence names like Create's session titles (API 1.76, alpha.5 item 14): who wrote a sequence's name, the model that
-- named it, and whether the namer was asked (once per sequence). An existing "Untitled sequence" is default, so its
-- next stored change names it.
ALTER TABLE timelines
    ADD COLUMN title_source text NOT NULL DEFAULT 'user',  -- default | prompt | user | model
    ADD COLUMN named_by     text,                          -- the model's short name while title_source is model
    ADD COLUMN name_asked   boolean NOT NULL DEFAULT false; -- named from a prompt (once per sequence)
UPDATE timelines SET title_source = 'default' WHERE name = 'Untitled sequence';

-- +goose Down
ALTER TABLE timelines DROP COLUMN title_source, DROP COLUMN named_by, DROP COLUMN name_asked;
