-- +goose Up
-- Jewellery subcategories became the product list's types: every old style subcategory (solitaire, band, jhumka,
-- choker, kada…) maps to its group's main type, except a pendant's charm (now charms); a necklace's mangalsutra keeps
-- its id (now a type), and null and a new type stay as they are. Three groups were renamed, so a batch named after its
-- product follows (Necklaces → Necklaces & chains, Necklace sets → Jewellery sets, Pendants with chain → Pendants), and
-- garment_kind questions are kind questions. No category or pose id changes. Rewrites every owner's rows (system role: the tables
-- FORCE row-level security); running it again changes nothing.
SELECT set_config('app.role', 'system', true);

-- only the old style ids (none is a new type's id), so a rerun, or a SKU already on a new type, is never collapsed
UPDATE catalog_skus SET subcategory = CASE
        WHEN category = 'pendants' AND subcategory = 'charm' THEN 'charms'
        WHEN category = 'necklace-sets' THEN 'jewellery-sets'
        ELSE category END
WHERE category IN ('rings', 'mens-rings', 'earrings', 'necklaces', 'necklace-sets', 'pendants', 'bracelets', 'bangles',
                   'anklets')
  AND subcategory IN ('solitaire', 'band', 'statement', 'stackable', 'signet', 'stone', 'stud', 'hoop', 'drop', 'jhumka',
                      'choker', 'chain', 'layered', 'bridal', 'choker-set', 'pendant-set', 'plain', 'enamel', 'charm',
                      'cuff', 'tennis', 'kada', 'pair', 'beaded');

UPDATE catalog_skus SET group_label = CASE group_label WHEN 'Necklaces' THEN 'Necklaces & chains'
        WHEN 'Necklace sets' THEN 'Jewellery sets' ELSE 'Pendants' END
WHERE group_label IN ('Necklaces', 'Necklace sets', 'Pendants with chain');

-- the garment_kind check became kind (garments and jewellery): an open question keeps its key, so a recheck that asks it
-- again finds it instead of adding a second one
UPDATE catalog_questions SET check_id = 'kind', key = 'kind:' || substr(key, length('garment_kind:') + 1)
WHERE check_id = 'garment_kind';

-- +goose Down
-- the old style subcategories cannot be told apart again (several became one): nothing to undo
SELECT 1;
