# auth_service

Users, sessions and client API keys (Go, gomicro layout, Gin through helmgo). Contract: `contracts/openapi.yaml` (tag `auth`) and
`contracts/messages/evt.auth.revoked.schema.json`. Built on [helmgo](../helmgo/README.md), through the `replace` in
`go.mod`; image builds need `make image` or compose, which pass `../helmgo` as a named build context.

## Run
- Alone: `cp .env.example .env && docker compose up --build` (own Postgres + NATS, API on `127.0.0.1:8093`; files alone is 8092).
- Full stack: `deploy/docker-compose.yml` (compose service `auth`, port 8080 on the compose network, behind Kong).
  Secrets (DB password, NATS credentials, the JWT signing key `JWT_PRIVATE_KEY_FILE`, the web app's key
  `WEB_API_KEY_FILE` and the CLI's key `HELM_API_KEY_FILE`, registered as clients "web" and "helm" on every start) come from `deploy/secrets/`. Alone, the JWT pair is
  `KEYS_DIR/private.pem`, generated on first start when missing (`auth_service/keys/`, git-ignored). NATS account `auth`: replies on
  `auth.validate.*`, publishes `evt.auth.revoked`, and answers the NATS micro service verbs (`$SRV.>`).
- The root admin: `ADMIN_EMAIL` / `ADMIN_PASSWORD` here. On every start (or `.tools/create-admin`) that account is created
  with that password, or enabled with the admin role (its password is never reset: the admin changes it in the app);
  every other account loses admin. Nobody else can be made admin
  (422), the API refuses any change to that account (409), and there is no public sign-up.
- Tests: start a scratch Postgres (see `.test.env.example`), `cp .test.env.example .test.env`, `make test`.
  Each run creates and drops its own database; without `TEST_DATABASE_URL` the integration tests skip.

## HTTP (port 8080)
| Route | Auth | |
|---|---|---|
| `POST /auth/signin` `{email,password}` | — | 200 Session; 401 generic; 429 after 5 failures / email or 50 / IP in 15 min (client IP from `X-Forwarded-For` only when the peer is a trusted proxy — Kong/web; Kong also rate-limits `/auth/signin\|refresh` per real client IP) |
| `POST /auth/refresh` `{refresh_token}` | — | 200 rotated Session; reuse of a used token → 401 and the family is revoked |
| `DELETE /auth/signout` | Bearer | 204, revokes the session's family |
| `PUT /auth/password` `{current_password,new_password}` | Bearer | 204, revokes all sessions |
| `GET /auth/me` | Bearer | User |
| `GET /auth/admin/users` | admin | `{users: [User + disabled, created_at]}` |
| `POST /auth/admin/users` `{email,password,name?,roles: [user, dev]}` | admin | 201 User (name defaults to the part before @); 409 duplicate; 422 invalid or `admin` asked |
| `PUT /auth/admin/users/{id}/disabled` `{disabled}` | admin | User; disabling revokes sessions |
| `PUT /auth/admin/users/{id}/roles` `{roles: [user, dev]}` | admin | User; revokes sessions; 422 for `admin` |
| `PUT /auth/admin/users/{id}/password` `{password}` | admin | 204; revokes sessions |
| (disabled, roles, password of the `ADMIN_EMAIL` account) | admin | 409: set by auth's `.env` |
| `POST /auth/admin/apikeys` `{client, scopes}` | admin | 201 with `key` (shown once) |
| `GET /auth/admin/apikeys` | admin | `{api_keys: [...]}` |
| `DELETE /auth/admin/apikeys/{id}` | admin | 204 |
| `GET /auth/.well-known/jwks.json` | — | public key |
| `GET /verify/apikey` (header `x-api-key`) | internal | 200 `{client_id, client, scopes}` / 401 — never routed by Kong |
| `GET` or `HEAD /health`, `GET /auth/health` | — | DB + NATS |

Access token: RS256 JWT, 15 min, claims `sub`, `email`, `roles`, `jti`, `fid` (session family), `iss=helm-auth` (`JWT_ISSUER`).

## NATS
- NATS micro service `auth` 1.0.0, mounted by `MountNats` in `api/auth/controller.go`: `validateTokenHandler` and
  `validateRoleHandler` decode with `micro.JsonToMsg` and answer with `micro.RespondNatsMessage` or
  `micro.RespondNatsError` (bodies in `api/auth/message/`); it also answers the `$SRV.*.auth` discovery verbs.
- `auth.validate.token` request `{"token": "<jwt>"}` → `{user_id, email, roles, jti, family_id, expires_at}` or
  `{"error": "..."}` (queue group `auth`).
- `auth.validate.role` request `{"token", "role"}` → the same plus `allowed`.
- Publishes `evt.auth.revoked` (core publish; captured if a stream covers `evt.auth.>`) on sign-out, refresh reuse,
  password change/set, disable and role change.

## CLI
- `auth apikey <client> [scope…]` issues a key and prints it once (e.g. `docker compose -f deploy/docker-compose.yml run --rm auth apikey contract`). Keys are `helm_` + 43 URL-safe characters..
- `auth create-admin` creates the admin from `ADMIN_EMAIL` / `ADMIN_PASSWORD`, then exits (`.tools/create-admin`).
- `auth health` is the container health check.
