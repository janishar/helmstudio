-- +goose Up
-- Voices (contracts/README.md "Voices", API 1.30): a saved voice is a library item of kind voice whose own fields
-- (VoiceInfo: source, design, language, style) are library_items.voice; null for elements and looks. A run's audio
-- output keeps its WAV facts ({duration_s, sample_rate}) as run_outputs.audio, like takes.audio.
ALTER TABLE library_items ADD COLUMN voice jsonb;
ALTER TABLE run_outputs ADD COLUMN audio jsonb;

-- +goose Down
-- a voice row without its voice column would read as a broken item: saved voices go with it (as system: the
-- table forces row-level security; set_config is local to the migration's transaction)
SELECT set_config('app.role', 'system', true);
DELETE FROM library_items WHERE kind = 'voice';
ALTER TABLE run_outputs DROP COLUMN audio;
ALTER TABLE library_items DROP COLUMN voice;
