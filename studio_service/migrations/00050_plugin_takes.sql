-- +goose Up
-- Plugins: takes (API 1.70, alpha.2 items 11 and 13): a take a plugin tool made (source tool): {plugin, tool, input}.
-- 00043-00049 are left to other alpha.2 branches in flight.
ALTER TABLE takes ADD COLUMN tool jsonb;

-- +goose Down
ALTER TABLE takes DROP COLUMN tool;
