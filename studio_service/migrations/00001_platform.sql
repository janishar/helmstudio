-- +goose Up
-- Row-level security: every owned table gets the same policy. app.user_id / app.role are set per transaction by
-- common.WithUser (role user|admin) or common.System (role system: projector, relay, lease sweep).
-- +goose StatementBegin
CREATE FUNCTION app_can_see(owner uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
  SELECT owner::text = current_setting('app.user_id', true)
      OR current_setting('app.role', true) IN ('admin', 'system')
$$;
-- +goose StatementEnd

CREATE TABLE outbox (                                       -- cmd.* waiting for the relay
    id         bigserial PRIMARY KEY,
    type       text NOT NULL,                               -- the envelope type, e.g. cmd.run.execute
    subject    text NOT NULL,                               -- where it goes, e.g. cmd.run.<host>.<env>
    job_id     uuid NOT NULL,
    owner_id   uuid NOT NULL,
    attempt    int NOT NULL,
    data       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX outbox_job ON outbox (job_id);
ALTER TABLE outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON outbox USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

CREATE TABLE projector_seen (                               -- projector dedupe on (job_id, seq); written as system only
    job_id uuid NOT NULL,
    seq    int NOT NULL,
    PRIMARY KEY (job_id, seq)
);

-- +goose Down
DROP TABLE projector_seen, outbox;
DROP FUNCTION app_can_see;
