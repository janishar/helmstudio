-- +goose Up
-- Quality metrics per image (GET /metrics): renders made for it and whether the first one passed review, from
-- evt.catalog.image, and the product its SKU was when it rendered (a SKU moved later keeps its history there); null
-- for images rendered before this migration.
ALTER TABLE catalog_images ADD COLUMN attempts int, ADD COLUMN first_pass boolean, ADD COLUMN category text;

-- +goose Down
ALTER TABLE catalog_images DROP COLUMN attempts, DROP COLUMN first_pass, DROP COLUMN category;
