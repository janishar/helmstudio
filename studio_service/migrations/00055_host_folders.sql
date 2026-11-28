-- +goose Up
-- Model folders (API 1.78, plan 40): a host's model folders in priority order and the downloads folder's index; null
-- = never set (each runner's HELMSTUDIO_MODELS). A model job names the folders it works in by id.
ALTER TABLE host_settings ADD COLUMN folders text[], ADD COLUMN downloads int;
ALTER TABLE model_jobs ADD COLUMN folders text[];

-- +goose Down
ALTER TABLE model_jobs DROP COLUMN folders;
ALTER TABLE host_settings DROP COLUMN folders, DROP COLUMN downloads;
