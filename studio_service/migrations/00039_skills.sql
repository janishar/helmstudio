-- +goose Up
-- Skills (API 1.64; contracts/README.md "Skills"): where a plugin came from (built in, or yours in ~/.helm/plugins: a
-- folder or a .zip), the folder its files are read from, the built-in version yours replaces, and why it can't be
-- used (needs_newer: a skill of a newer format; broken: yours could not be read).
ALTER TABLE plugins
    ADD COLUMN origin   text NOT NULL DEFAULT 'built_in',   -- built_in | yours
    ADD COLUMN dir      text NOT NULL DEFAULT '',           -- the container path of its files
    ADD COLUMN replaces text,                               -- yours: the built-in version of the same name
    ADD COLUMN blocked  text NOT NULL DEFAULT '',           -- '' | needs_newer | broken
    ADD COLUMN problem  text;                               -- why, in plain words

-- +goose Down
ALTER TABLE plugins DROP COLUMN origin, DROP COLUMN dir, DROP COLUMN replaces, DROP COLUMN blocked, DROP COLUMN problem;
