-- +goose Up
-- The host memory budget mode (API 1.75, plan 35): auto follows the macOS GPU limit, custom is RAM (VRAM) - reserve_gb.
ALTER TABLE host_settings ADD COLUMN reserve_mode text NOT NULL DEFAULT 'custom';  -- auto | custom

-- +goose Down
ALTER TABLE host_settings DROP COLUMN reserve_mode;
