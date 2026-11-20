-- +goose Up
-- API 1.29: a V1 clip's shot number (contracts/README.md, Timeline rules: shot numbers). Live sequences are numbered
-- by position once; older revisions are not (studio numbers a restored clip when it stores it).
SET LOCAL app.role = 'system';
UPDATE timelines SET doc = jsonb_set(doc, '{tracks,0,clips}', (
    SELECT coalesce(jsonb_agg(c || jsonb_build_object('number', i) ORDER BY i), '[]'::jsonb)
    FROM jsonb_array_elements(doc #> '{tracks,0,clips}') WITH ORDINALITY AS x(c, i)))
WHERE jsonb_typeof(doc #> '{tracks,0,clips}') = 'array';

-- +goose Down
SET LOCAL app.role = 'system';
UPDATE timelines SET doc = jsonb_set(doc, '{tracks,0,clips}', (
    SELECT coalesce(jsonb_agg(c - 'number' ORDER BY i), '[]'::jsonb)
    FROM jsonb_array_elements(doc #> '{tracks,0,clips}') WITH ORDINALITY AS x(c, i)))
WHERE jsonb_typeof(doc #> '{tracks,0,clips}') = 'array';
