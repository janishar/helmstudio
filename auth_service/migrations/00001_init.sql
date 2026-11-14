-- +goose Up
CREATE TABLE users (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       text NOT NULL UNIQUE CHECK (email = lower(btrim(email))),
    name        text NOT NULL,
    disabled_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE credentials (
    user_id       uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role    text NOT NULL CHECK (role IN ('user', 'admin')),
    PRIMARY KEY (user_id, role)
);

CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id  uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz
);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

CREATE TABLE clients (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id    uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    hash         bytea NOT NULL UNIQUE,
    prefix       text NOT NULL,
    scopes       text[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_used_at timestamptz
);

-- +goose Down
DROP TABLE api_keys, clients, refresh_tokens, roles, credentials, users;
