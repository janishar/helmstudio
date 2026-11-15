-- +goose Up
-- A user's own LoRAs (contracts/README.md "LoRAs", API 1.9): uploaded by the takes' signed PUT under
-- u/<owner_id>/loras/<id>.safetensors and checked at complete. Shared LoRAs are not stored here: they are the live
-- hosts' registry loras keys. A deleted LoRA keeps its row (404 at once) until the sweep has removed its object.
CREATE TABLE loras (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL,
    name       text NOT NULL,
    key        text NOT NULL,                               -- u/<owner_id>/loras/<id>.safetensors
    size_bytes bigint NOT NULL,                             -- declared at create, checked at complete
    state      text NOT NULL DEFAULT 'pending',             -- pending | ready | rejected
    reason     text,                                        -- rejected: the failed check
    family     text NOT NULL DEFAULT 'unknown',             -- detected once, at complete
    rank       int,
    key_style  text,
    licence    text,
    sha256     text,
    swept      boolean NOT NULL DEFAULT false,              -- a rejected LoRA's object is gone
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz
);
CREATE INDEX loras_owner ON loras (owner_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX loras_sweep ON loras (created_at) WHERE deleted_at IS NOT NULL OR state <> 'ready';
ALTER TABLE loras ENABLE ROW LEVEL SECURITY;
ALTER TABLE loras FORCE ROW LEVEL SECURITY;
CREATE POLICY owner ON loras USING (app_can_see(owner_id)) WITH CHECK (app_can_see(owner_id));

-- A run keeps every LoRA of its graph as resolved when it was made ([{node, id, source, name, strength, mode, sha256,
-- bytes}]): the dispatcher's host rule and release, and the take's made_with.loras.
ALTER TABLE runs ADD COLUMN loras jsonb;

-- +goose Down
ALTER TABLE runs DROP COLUMN loras;
DROP TABLE loras;
