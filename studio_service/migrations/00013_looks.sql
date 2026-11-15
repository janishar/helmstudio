-- +goose Up
-- Looks (contracts/README.md "Looks", API 1.10): a user's own looks are library items of kind look (handle null) with
-- 0-4 LoRAs ([{lora_id, name, strength}], the name as saved) and the take "Save as look" was made from. Starter looks
-- are not stored: they are the live hosts' registry looks keys.
ALTER TABLE library_items ADD COLUMN loras jsonb NOT NULL DEFAULT '[]';
ALTER TABLE library_items ADD COLUMN from_take_id uuid;

-- A run keeps the looks it was asked with (Run.looks, as requested); what they did is in runs.mentions ({looks}).
-- A batch keeps the copy of every look a row or step uses, which its runs resolve against; a row its looks.
ALTER TABLE runs ADD COLUMN looks jsonb;
ALTER TABLE batches ADD COLUMN looks jsonb;
ALTER TABLE batch_rows ADD COLUMN looks jsonb;

-- +goose Down
ALTER TABLE batch_rows DROP COLUMN looks;
ALTER TABLE batches DROP COLUMN looks;
ALTER TABLE runs DROP COLUMN looks;
ALTER TABLE library_items DROP COLUMN from_take_id;
ALTER TABLE library_items DROP COLUMN loras;
