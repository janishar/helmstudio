-- +goose Up
-- Garments became 5 groups: kurtis → tops, kurta-pant and kurta-pant-dupatta → sets, and their pose ids
-- kurti-<pose> → top-<pose>, kurta-pant-with(out)-dupatta-<pose> → set-<pose>. Rewrites every stored id for every
-- owner (system role: the tables FORCE row-level security); a no-op on a database without garment rows, and running
-- it again changes nothing. Chat messages and catalog_events are history and keep the old words (events are read
-- through today's rows).
SELECT set_config('app.role', 'system', true);

-- garment_ids rewrites the old ids where they are whole JSON strings (values or keys) of doc. A session whose Poses
-- step had picks for both kurta-pant products keeps one list under sets (jsonb keeps the last duplicate key).
-- +goose StatementBegin
CREATE FUNCTION garment_ids(doc jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
  SELECT regexp_replace(regexp_replace(regexp_replace(regexp_replace(doc::text,
    '"kurta-pant-with(out)?-dupatta-([a-z0-9-]+)"', '"set-\2"', 'g'),
    '"kurti-([a-z0-9-]+)"', '"top-\1"', 'g'),
    '"kurtis"', '"tops"', 'g'),
    '"kurta-pant(-dupatta)?"', '"sets"', 'g')::jsonb
$$;
-- +goose StatementEnd

-- garment_id rewrites one old category or pose id (any other id, and null, come back unchanged).
-- +goose StatementBegin
CREATE FUNCTION garment_id(id text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
  SELECT garment_ids(to_jsonb(id)) #>> '{}'
$$;
-- +goose StatementEnd

-- garment_items rewrites the Poses step's items (an array): only their poses and briefs maps, never labels or ids.
-- +goose StatementBegin
CREATE FUNCTION garment_items(items jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
  SELECT coalesce(jsonb_agg(CASE WHEN jsonb_typeof(item) = 'object' THEN item || jsonb_strip_nulls(jsonb_build_object(
      'poses', garment_ids(item->'poses'), 'briefs', garment_ids(item->'briefs'))) ELSE item END ORDER BY n), '[]')
  FROM jsonb_array_elements(items) WITH ORDINALITY t(item, n)
$$;
-- +goose StatementEnd

-- the SKU: product, subcategory (from the old product's), group named after the product, poses in brief and overrides
UPDATE catalog_skus SET
    category    = garment_id(category),
    subcategory = CASE
        WHEN category = 'kurtis' THEN CASE subcategory WHEN 'top' THEN 'tops' WHEN 'tunic' THEN 'tunics' ELSE 'kurtis' END
        WHEN subcategory = 'co-ord' THEN 'co-ords'
        ELSE 'kurta-sets' END,
    group_key   = garment_id(group_key),
    group_label = CASE group_label WHEN 'Kurtis / tops' THEN 'Tops'
        WHEN 'Kurta pant without dupatta' THEN 'Sets' WHEN 'Kurta pant with dupatta' THEN 'Sets' ELSE group_label END,
    brief       = garment_ids(brief),
    overrides   = garment_ids(overrides)
WHERE category IN ('kurtis', 'kurta-pant', 'kurta-pant-dupatta');

-- images: the shot (pose id) and the product at render time. A SKU moved between the two kurta-pant products can have
-- both old shots of one pose; they become one shot, renumbered by age so the newest stays the live revision.
UPDATE catalog_images i SET shot = n.shot, revision = n.revision
FROM (SELECT id, garment_id(shot) AS shot,
             CASE WHEN min(shot) OVER w = max(shot) OVER w THEN revision
                  ELSE row_number() OVER (w ORDER BY created_at, revision) END AS revision
      FROM catalog_images WHERE garment_id(shot) <> shot
      WINDOW w AS (PARTITION BY sku_id, garment_id(shot))) n
WHERE i.id = n.id;
UPDATE catalog_images SET category = garment_id(category) WHERE garment_id(category) <> category;

UPDATE catalog_runs SET category = garment_id(category), group_key = garment_id(group_key)
WHERE garment_id(category) <> category OR garment_id(group_key) <> group_key;

UPDATE catalog_uploads SET category = garment_id(category) WHERE garment_id(category) <> category;

-- the Poses step (picks by product, briefs by product) in sessions and in memory; questions' options (labels are
-- sentences, so only ids match whole strings there)
UPDATE catalog_sessions SET inputs = jsonb_set(inputs, '{poses,items}', garment_items(inputs->'poses'->'items'))
WHERE jsonb_typeof(inputs->'poses'->'items') = 'array' AND garment_items(inputs->'poses'->'items') <> inputs->'poses'->'items';
UPDATE catalog_memory SET items = garment_items(items)
WHERE step = 'poses' AND jsonb_typeof(items) = 'array' AND garment_items(items) <> items;
UPDATE catalog_questions SET options = garment_ids(options) WHERE garment_ids(options) <> options;

-- favorite poses: both kurta-pant poses of one name become one favorite
INSERT INTO catalog_favorites (owner_id, kind, item_id, created_at)
SELECT owner_id, kind, garment_id(item_id), min(created_at) FROM catalog_favorites
WHERE kind = 'poses' AND garment_id(item_id) <> item_id GROUP BY owner_id, kind, garment_id(item_id)
ON CONFLICT DO NOTHING;
DELETE FROM catalog_favorites WHERE kind = 'poses' AND garment_id(item_id) <> item_id;

DROP FUNCTION garment_items, garment_id, garment_ids;

-- +goose Down
-- the old ids cannot be told apart again (two products became one): nothing to undo
SELECT 1;
