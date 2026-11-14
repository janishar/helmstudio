-- +goose Up
-- User workflows (contracts/README.md "User workflows", API 1.5): a user's own copy of a workflow graph, edited in the
-- Graph view. Owner-scoped by row-level security; templates stay in the registry.
CREATE TABLE user_workflows (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    title      text NOT NULL,
    family     text NOT NULL,
    env        text NOT NULL,
    nodes      jsonb NOT NULL,                              -- node id → {kind, params, in, bypass, position}
    studio     jsonb,                                       -- StudioLayout; null = the derived form
    based_on   text,                                        -- the template or workflow it was made from (may be gone)
    version    int NOT NULL DEFAULT 1,                      -- +1 on every PUT and PATCH
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX user_workflows_owner ON user_workflows (owner_id, updated_at DESC);
ALTER TABLE user_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON user_workflows USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- A run of a user workflow keeps the version it ran and the title it had (made_with); a batch the version it snapshot.
ALTER TABLE runs ADD COLUMN workflow_version int, ADD COLUMN workflow_title text;
CREATE INDEX runs_workflow_open ON runs (workflow_id) WHERE status IN ('queued', 'running');
ALTER TABLE batches ADD COLUMN workflow_version int;

-- +goose Down
ALTER TABLE batches DROP COLUMN workflow_version;
DROP INDEX runs_workflow_open;
ALTER TABLE runs DROP COLUMN workflow_title, DROP COLUMN workflow_version;
DROP TABLE user_workflows;
