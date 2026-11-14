-- +goose Up
-- Every render try of an image ([{n, key, review, best}] from evt.catalog.image attempts_detail; keys under the
-- owner's prefix, images/<image_id>/attempt_<n>.<ext>); null for images rendered before tries were kept.
ALTER TABLE catalog_images ADD COLUMN tries jsonb;

-- +goose Down
ALTER TABLE catalog_images DROP COLUMN tries;
