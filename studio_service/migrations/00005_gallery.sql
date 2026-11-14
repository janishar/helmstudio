-- +goose Up
-- Takes: the gallery (plan 9.1). Every run output is a take (the projector inserts it with the output), and so is a
-- completed upload. The object key never leaves the service; the API signs links. A deleted take keeps its row until
-- the sweep has removed its object.
CREATE TABLE takes (
    id           uuid PRIMARY KEY,
    owner_id     uuid NOT NULL,
    kind         text NOT NULL,                             -- image | video | audio
    source       text NOT NULL,                             -- create | upload
    title        text NOT NULL,
    key          text NOT NULL,                             -- u/<owner_id>/…
    content_type text NOT NULL,
    width        int,
    height       int,
    bytes        bigint NOT NULL,
    starred      boolean NOT NULL DEFAULT false,
    session_id   uuid REFERENCES sessions ON DELETE SET NULL, -- deleting a session keeps its takes
    run_id       uuid REFERENCES runs ON DELETE SET NULL,
    output_idx   int,                                       -- the run output it is; (run_id, output_idx) is unique
    made_with    jsonb,                                     -- null for an upload
    ready        boolean NOT NULL,                          -- false: an upload not completed yet (invisible)
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at   timestamptz,                               -- soft delete; the sweep removes the object, then the row
    UNIQUE (run_id, output_idx)
);
CREATE INDEX takes_gallery ON takes (owner_id, created_at DESC, id DESC) WHERE deleted_at IS NULL AND ready;
CREATE INDEX takes_session ON takes (session_id, created_at DESC, id DESC) WHERE deleted_at IS NULL AND ready;
CREATE INDEX takes_sweep ON takes (created_at) WHERE deleted_at IS NOT NULL OR NOT ready;

CREATE TABLE take_parents (                                 -- lineage edges: empty until workflows take media inputs
    take_id   uuid NOT NULL REFERENCES takes ON DELETE CASCADE,
    parent_id uuid NOT NULL,
    owner_id  uuid NOT NULL,
    PRIMARY KEY (take_id, parent_id)
);

ALTER TABLE run_outputs ADD COLUMN take_id uuid;            -- RunOutput.take_id; the row goes when the take is deleted

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['takes', 'take_parents'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY owner ON %I USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id))', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE run_outputs DROP COLUMN take_id;
DROP TABLE take_parents, takes;
