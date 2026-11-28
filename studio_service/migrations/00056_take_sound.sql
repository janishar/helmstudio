-- +goose Up
-- Music and sound (API 1.79, plan 38): an audio take's sound class; every audio take made before is voice. Null on
-- images and videos. The Gallery's Voice / Music / Sound filters read it with the owner and kind.
ALTER TABLE takes ADD COLUMN sound text CHECK (sound IN ('music', 'sound', 'voice'));
UPDATE takes SET sound = 'voice' WHERE kind = 'audio';
CREATE INDEX takes_owner_kind_sound ON takes (owner_id, kind, sound);

-- +goose Down
DROP INDEX takes_owner_kind_sound;
ALTER TABLE takes DROP COLUMN sound;
