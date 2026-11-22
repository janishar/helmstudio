-- +goose Up
-- Sessions and clean-up (contracts/README.md "Deleting runs", "Sessions: titles and naming", API 1.43): a finished run
-- deleted from its session (soft), a session's hidden naming run (purpose name), who wrote a session's title, the
-- model that named it and whether it was asked, and the admin's session-names setting (one row; no owner, like
-- providers). 00028, not 00027: branch assistant holds 00027.
ALTER TABLE runs
    ADD COLUMN deleted_at timestamptz,                     -- DELETE /runs/{id}: gone from the session, its takes kept
    ADD COLUMN purpose    text;                            -- name: the session's hidden naming run; null otherwise

ALTER TABLE sessions
    ADD COLUMN title_source text NOT NULL DEFAULT 'user',  -- default | prompt | user | model
    ADD COLUMN named_by     text,                          -- the model's short name while title_source is model
    ADD COLUMN name_asked   boolean NOT NULL DEFAULT false; -- cmd.pipeline.name sent (once per session)
UPDATE sessions SET title_source = 'default' WHERE title = 'Untitled';
-- sessions that already have a succeeded run are past their first one: never named in a sweep
UPDATE sessions s SET name_asked = true WHERE EXISTS (SELECT 1 FROM runs r WHERE r.session_id = s.id AND r.status = 'succeeded');

CREATE TABLE session_names (
    one        boolean PRIMARY KEY DEFAULT true CHECK (one),
    mode       text NOT NULL,                              -- off | local | remote
    provider   text,                                       -- remote: a providers.name
    model      text,                                       -- remote: one of its models
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE session_names;
ALTER TABLE sessions DROP COLUMN title_source, DROP COLUMN named_by, DROP COLUMN name_asked;
ALTER TABLE runs DROP COLUMN deleted_at, DROP COLUMN purpose;
