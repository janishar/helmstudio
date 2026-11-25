-- +goose Up
-- The Assistant's first launch (API 1.72, plan 32): an admin's drawer opened by itself once; one row per user who saw it.
CREATE TABLE assistant_first_launch (
    owner_id uuid PRIMARY KEY,
    at       timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE assistant_first_launch ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_first_launch FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON assistant_first_launch
    USING (owner_id::text = current_setting('app.user_id', true) OR current_setting('app.role', true) = 'system')
    WITH CHECK (owner_id::text = current_setting('app.user_id', true) OR current_setting('app.role', true) = 'system');

-- +goose Down
DROP TABLE assistant_first_launch;
