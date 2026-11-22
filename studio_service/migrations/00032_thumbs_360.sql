-- +goose Up
-- Thumbnails cut-off at a short side over 360 px (contracts/README.md "Thumbnails"; plan 22 section 26, owner
-- 2026-11-23; was 512): the image and video takes the old rule skipped (thumb null, tries spent, short side 361–512;
-- a video's is its poster's) get their tries back, so the thumbs loop picks them again. As system: takes forces
-- row-level security; set_config is local to the migration's transaction.
SELECT set_config('app.role', 'system', true);
UPDATE takes SET thumb_tries = 0
WHERE thumb IS NULL AND deleted_at IS NULL AND thumb_tries >= 3
  AND CASE kind
      WHEN 'image' THEN width > 360 AND height > 360 AND least(width, height) <= 512
      WHEN 'video' THEN (video->'poster'->>'width')::int > 360 AND (video->'poster'->>'height')::int > 360
          AND least((video->'poster'->>'width')::int, (video->'poster'->>'height')::int) <= 512
      ELSE false END;

-- +goose Down
-- nothing to undo: a take that got its thumbnail keeps it
SELECT 1;
