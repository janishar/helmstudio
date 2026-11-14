-- +goose Up
-- The fair order's history: when each owner last had a run released. Kept apart from runs so deleting a session (its
-- runs cascade) never resets an owner's turn. Written and read by the dispatcher (as system) only.
CREATE TABLE dispatch_owners (
    owner_id         uuid PRIMARY KEY,
    last_released_at timestamptz NOT NULL
);
INSERT INTO dispatch_owners SELECT owner_id, max(released_at) FROM runs WHERE released_at IS NOT NULL GROUP BY owner_id;
ALTER TABLE dispatch_owners ENABLE ROW LEVEL SECURITY;
ALTER TABLE dispatch_owners FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON dispatch_owners USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- +goose Down
DROP TABLE dispatch_owners;
