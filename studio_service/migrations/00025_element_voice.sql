-- +goose Up
-- Elements carry a voice (contracts/README.md "Dialogue and Timeline audio", API 1.32): a character's voice_id names
-- one of the owner's voices (a UUID) or a starter voice (<family>/<id>); read through a live check, so a deleted or
-- unpublished voice reads null and deleting a voice never touches elements.
ALTER TABLE library_items ADD COLUMN voice_id text;

-- +goose Down
ALTER TABLE library_items DROP COLUMN voice_id;
