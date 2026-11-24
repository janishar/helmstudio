-- +goose Up
-- Plugins (API 1.60; contracts/README.md "Plugins"): what studio found in plugins/ (the manifest as read, off until an
-- admin turns it on); a provider made from a plugin names it, and one made from a personal plugin has an owner: the
-- providers table's policy shows it only to that owner and to the service itself (never to other admins); an admin's
-- own assistant provider (an Only-you one) beside the deployment's.
CREATE TABLE plugins (
    name       text PRIMARY KEY,                            -- its folder under plugins/
    manifest   jsonb NOT NULL,                              -- plugins/manifest.schema.json, as read at the last scan
    enabled    boolean NOT NULL DEFAULT false,
    settings   jsonb NOT NULL DEFAULT '{}',
    found_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE providers
    ADD COLUMN owner_id uuid,                               -- Only you: the admin who added it; NULL = everyone's
    ADD COLUMN plugin   text;                               -- the plugin it was made from
ALTER TABLE providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE providers FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON providers
    USING (owner_id IS NULL OR owner_id::text = current_setting('app.user_id', true)
           OR current_setting('app.role', true) = 'system')
    WITH CHECK (owner_id IS NULL OR owner_id::text = current_setting('app.user_id', true)
                OR current_setting('app.role', true) = 'system');

CREATE TABLE assistant_personal (                           -- an admin's own assistant provider (AssistantSettings.personal)
    owner_id   uuid PRIMARY KEY,
    provider   text NOT NULL REFERENCES providers (name) ON DELETE CASCADE ON UPDATE CASCADE,
    model      text,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE assistant_personal ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_personal FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON assistant_personal
    USING (owner_id::text = current_setting('app.user_id', true) OR current_setting('app.role', true) = 'system')
    WITH CHECK (owner_id::text = current_setting('app.user_id', true) OR current_setting('app.role', true) = 'system');

-- +goose Down
DROP TABLE assistant_personal;
DROP POLICY owner ON providers;
ALTER TABLE providers NO FORCE ROW LEVEL SECURITY;
ALTER TABLE providers DISABLE ROW LEVEL SECURITY;
ALTER TABLE providers DROP COLUMN owner_id, DROP COLUMN plugin;
DROP TABLE plugins;
