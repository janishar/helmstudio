-- +goose Up
-- Catalog Agent (api/catalog): sessions and what hangs off them, the caller's memory and favorites. Every row has
-- owner_id and the app_can_see policy, so another user's id is never visible (404).
CREATE TABLE catalog_sessions (
    id            uuid PRIMARY KEY,
    owner_id      uuid NOT NULL,
    project_name  text NOT NULL,
    stage         text NOT NULL DEFAULT 'setup',            -- setup | ready
    flow_position text NOT NULL DEFAULT 'raw',
    inputs        jsonb NOT NULL,                           -- step -> {items, skipped}
    messages      jsonb NOT NULL DEFAULT '[]',
    rules         jsonb,                                    -- evt.catalog.skus data.rules (object keys, not urls)
    expected_skus int,                                      -- distinct SKUs of the latest CSV
    event_seq     bigint NOT NULL DEFAULT 0,                -- last catalog_events.seq of the session
    created_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at    timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX catalog_sessions_owner ON catalog_sessions (owner_id, created_at DESC);

CREATE TABLE catalog_processes (                                -- cmd.catalog.process jobs; the newest is Session.processing
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    session_id uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    status     text NOT NULL DEFAULT 'queued',              -- queued | running | succeeded | failed
    error      text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX catalog_processes_session ON catalog_processes (session_id, created_at DESC);

CREATE TABLE catalog_uploads (                                  -- files stored for a session (keys, never bytes)
    id           uuid PRIMARY KEY,
    owner_id     uuid NOT NULL,
    session_id   uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    kind         text NOT NULL,                             -- raw | guideline | brief | model | background | pose_ref
    name         text NOT NULL,
    content_type text NOT NULL,
    size         bigint NOT NULL,
    category     text,
    key          text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX catalog_uploads_session ON catalog_uploads (session_id, created_at);

CREATE TABLE catalog_memory (                                   -- last-used items per step, saved by Start processing
    owner_id     uuid NOT NULL,
    step         text NOT NULL,
    items        jsonb NOT NULL,
    project_name text NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (owner_id, step)
);

CREATE TABLE catalog_favorites (
    owner_id   uuid NOT NULL,
    kind       text NOT NULL,                               -- models | backgrounds | poses
    item_id    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (owner_id, kind, item_id)
);

CREATE TABLE catalog_skus (
    id          uuid PRIMARY KEY,
    owner_id    uuid NOT NULL,
    session_id  uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    code        text NOT NULL,
    name        text NOT NULL,
    category    text NOT NULL,
    subcategory text,
    group_key   text,
    group_label text,
    variant_of  uuid,
    attributes  jsonb NOT NULL,
    confirmed   boolean NOT NULL DEFAULT false,
    brief       jsonb NOT NULL,                             -- {model, background, poses, resolution, ratio}
    overrides   jsonb NOT NULL DEFAULT '{}',                -- brief fields set by answers and SKU edits; Start keeps them
    raw_keys    jsonb NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (session_id, code)
);

CREATE TABLE catalog_questions (
    id          uuid PRIMARY KEY,
    owner_id    uuid NOT NULL,
    session_id  uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    key         text NOT NULL,
    check_id    text NOT NULL,
    text        text NOT NULL,
    options     jsonb NOT NULL,                             -- [{label, apply}] as agents published them
    blocking    boolean NOT NULL,
    sku_codes   jsonb NOT NULL DEFAULT '[]',
    answer      int,
    answered_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (session_id, key)
);

CREATE TABLE catalog_runs (
    id             uuid PRIMARY KEY,
    owner_id       uuid NOT NULL,
    session_id     uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    scope          text NOT NULL,                           -- sample | batch | all | skus | regenerate
    category       text,
    group_key      text,
    sku_ids        uuid[] NOT NULL,
    image_ids      uuid[] NOT NULL,
    credits_quoted double precision NOT NULL,
    feedback       text,
    status         text NOT NULL DEFAULT 'queued',          -- queued | running | succeeded | failed
    error          text,
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at    timestamptz
);
CREATE INDEX catalog_runs_session ON catalog_runs (session_id, created_at DESC);

CREATE TABLE catalog_images (                                   -- every revision; the newest per (sku_id, shot) is live
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    session_id uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    sku_id     uuid NOT NULL REFERENCES catalog_skus ON DELETE CASCADE,
    run_id     uuid NOT NULL REFERENCES catalog_runs ON DELETE CASCADE,
    shot       text NOT NULL,
    revision   int NOT NULL,
    status     text NOT NULL DEFAULT 'generating',          -- generating | to_review | approved | rejected | failed
    key        text,
    feedback   text,
    review     jsonb,
    error      text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (sku_id, shot, revision)
);
CREATE INDEX catalog_images_session ON catalog_images (session_id);

CREATE TABLE catalog_events (                                   -- evt.catalog.* as stored by the projector, per-session seq
    session_id uuid NOT NULL REFERENCES catalog_sessions ON DELETE CASCADE,
    seq        bigint NOT NULL,
    owner_id   uuid NOT NULL,
    type       text NOT NULL,                               -- status | log | step | question | skus | image | run
    work_id    uuid,
    ts         timestamptz NOT NULL,
    data       jsonb NOT NULL,
    PRIMARY KEY (session_id, seq)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['catalog_sessions', 'catalog_processes', 'catalog_uploads', 'catalog_memory', 'catalog_favorites', 'catalog_skus',
                           'catalog_questions', 'catalog_runs', 'catalog_images', 'catalog_events'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE catalog_events, catalog_images, catalog_runs, catalog_questions, catalog_skus, catalog_favorites, catalog_memory, catalog_uploads,
           catalog_processes, catalog_sessions;
