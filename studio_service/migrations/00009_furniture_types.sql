-- +goose Up
-- Furniture subcategories became the product list's types: every old style subcategory maps to its group's type
-- (sofas: three-seater, two-seater → sofas, sectional → sectional-sofas, recliner → recliners; beds: king, queen, single →
-- beds, storage → storage-beds; side tables: round, square → side-tables, nesting → nesting-tables, bedside →
-- bedside-tables; TV units: wall-mounted, floor-standing, console → tv-units); null and a new type stay as they are.
-- Side tables became Tables, so a batch named after it follows. No category or pose id changes. Rewrites every owner's
-- rows (system role: the tables FORCE row-level security); running it again changes nothing.
SELECT set_config('app.role', 'system', true);

-- only the old style ids (none is a new type's id), so a rerun, or a SKU already on a new type, is never collapsed
UPDATE catalog_skus SET subcategory = CASE subcategory
        WHEN 'sectional' THEN 'sectional-sofas'
        WHEN 'recliner' THEN 'recliners'
        WHEN 'storage' THEN 'storage-beds'
        WHEN 'nesting' THEN 'nesting-tables'
        WHEN 'bedside' THEN 'bedside-tables'
        ELSE category END
WHERE category IN ('sofas', 'beds', 'side-tables', 'tv-units')
  AND subcategory IN ('three-seater', 'two-seater', 'sectional', 'recliner', 'king', 'queen', 'single', 'storage',
                      'round', 'square', 'nesting', 'bedside', 'wall-mounted', 'floor-standing', 'console');

UPDATE catalog_skus SET group_label = 'Tables' WHERE category = 'side-tables' AND group_label = 'Side tables';

-- +goose Down
-- the old style subcategories cannot be told apart again (several became one): nothing to undo
SELECT 1;
