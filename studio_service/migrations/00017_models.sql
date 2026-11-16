-- +goose Up
-- M5a (API 1.16): model jobs (contracts/README.md "Models and model jobs"), the hosts admin's settings ("Hosts admin")
-- and imported takes ("Import"). model_jobs and host_settings belong to the deployment (no owner, admin API only, like
-- providers); an imported take is an ordinary takes row of its owner.
CREATE TABLE model_jobs (
    id           uuid PRIMARY KEY,
    kind         text NOT NULL,                             -- download | delete | lora_delete
    host         text NOT NULL,
    env          text NOT NULL,                             -- the runner that does it
    model        text,                                      -- download, delete
    variant      text,                                      -- download: the variant; delete: null = every variant
    lora_id      text,                                      -- lora_delete: the shared LoRA's SHA-256
    title        text NOT NULL,
    state        text NOT NULL DEFAULT 'queued',            -- queued | running | succeeded | failed | cancelled
    progress     jsonb,                                     -- the latest evt.model.progress
    failure      jsonb,                                     -- {class, detail}
    result       jsonb,
    keeps        jsonb NOT NULL DEFAULT '[]',               -- delete: shared components left on disk
    licence      jsonb,                                     -- download: {id, sha256} accepted
    requested_by uuid NOT NULL,                             -- the admin
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    released_at  timestamptz,                               -- when its cmd.model.job went to the outbox
    started_at   timestamptz,
    ended_at     timestamptz
);
CREATE INDEX model_jobs_open ON model_jobs (host, created_at) WHERE state IN ('queued', 'running');
CREATE INDEX model_jobs_newest ON model_jobs (created_at DESC);

CREATE TABLE host_settings (                                -- the truth behind KV host_settings key <host>
    host            text PRIMARY KEY,
    version         int NOT NULL,
    after_run       text NOT NULL,                          -- unload | keep_warm | keep_pinned
    idle_unload_min int NOT NULL,
    reserve_gb      double precision NOT NULL,
    pins            jsonb NOT NULL DEFAULT '[]',            -- [PinRef]
    updated_at      timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE takes
    ADD COLUMN import_key  text,                            -- source import: helm import's idempotency key
    ADD COLUMN legacy      jsonb,                           -- source import: {settings, imported_at}
    ADD COLUMN import_wait jsonb;                           -- pending import: {width, height, poster {content_type, bytes, width, height}}
CREATE UNIQUE INDEX takes_import_key ON takes (owner_id, import_key) WHERE import_key IS NOT NULL;

CREATE TABLE import_edges (                                 -- lineage by import key: a take_parents row once both are ready
    owner_id   uuid NOT NULL,
    key        text NOT NULL,
    parent_key text NOT NULL,
    idx        int NOT NULL,
    PRIMARY KEY (owner_id, key, parent_key)
);
ALTER TABLE import_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_edges FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON import_edges USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- +goose Down
DROP TABLE import_edges;
DROP INDEX takes_import_key;
ALTER TABLE takes DROP COLUMN import_wait, DROP COLUMN legacy, DROP COLUMN import_key;
DROP TABLE host_settings, model_jobs;
