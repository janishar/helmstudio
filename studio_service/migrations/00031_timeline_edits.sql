-- +goose Up
-- Timeline edits by ops (contracts/README.md "Timeline: edits by ops", API 1.49; plan 24 section 28): who made a
-- revision (via: web | api | mcp | assistant, from X-Helm-Client; null for studio's writes and, read as web, for the
-- user writes from before) and what it was (ops_summary: an /edits call's op names, else the write's kind). No backfill.
ALTER TABLE timeline_revisions ADD COLUMN via text, ADD COLUMN ops_summary text;

-- +goose Down
ALTER TABLE timeline_revisions DROP COLUMN ops_summary, DROP COLUMN via;
