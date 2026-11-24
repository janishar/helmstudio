# §36 Production on this Mac

Decided by the owner, 2026-11-25. Production here means, in the owner's words: "run current version as production,
production means no fake tests, not debug build, not from the server deployment perspective". The owner picked the
**helmstudio-prod option** (a stack of its own next to the dev stack) and **Start fresh** (empty production data).
Essential checks only.

## What it is

- Compose project `helmstudio-prod`: `deploy/docker-compose.yml` plus `deploy/compose/prod.yml`, nothing else (no
  `fake.yml`, `dev-remote.yml`, `test-admin.yml` or `tls.yml`).
- The dev stack (`helmstudio`, `make up`) keeps the fakes for building and testing; the test stack (`helmstudio-test`,
  `make test`) is unchanged. The fake runner moved from the base file into `fake.yml`, so the base has no fakes.
- The Mac app (plan 31) will later run this same overlay inside its VM.

## What it drops and adds

| Dropped (dev and test only) | Added (`prod.yml`, `make prod`) |
|---|---|
| fake runner (host `fake`, Echo), fake Hub, fake OpenAI server | `restart: unless-stopped` on every long-running service |
| `REMOTE_ALLOW_PRIVATE=on`, the fake CA and placeholder key | log caps: json-file, 10 MB × 3 per container |
| `SERVER_HOSTS=fake` (empty in prod) | memory limits (Postgres, SeaweedFS, pipelines 1 GB; the rest 128-512 MB) |
| the test admin | images tagged `prod`, built from the current checkout |

pipelines keeps the base settings: real provider keys from the writable folder `deploy/secrets-prod/providers/` (pasted
in Admin › Model providers), the image's public CA bundle, private remotes off. Local model servers on this Mac
(`http://host.docker.internal:<port>`, plan 25) still work: the host rule allows them without `REMOTE_ALLOW_PRIVATE`.
`REMOTE_MODELS` comes from `deploy/.env`, as on the dev stack.

## Its own everything

| | dev | prod |
|---|---|---|
| Kong (the app) | 127.0.0.1:8300 | 127.0.0.1:8400 |
| Postgres | 5463 | 5465 |
| NATS / S3 / S3 https | 4322 / 8433 / 8443 | 4324 / 8435 / 8445 |
| network | 172.30.97.0/24 | 172.30.99.0/24 |
| secrets | `deploy/secrets/` | `deploy/secrets-prod/` (made fresh by `make prod`, git-ignored) |
| volumes, image tag | `helmstudio_*`, `dev` | `helmstudio-prod_*`, `prod` |

The admin is the owner's dev admin: `ADMIN_EMAIL` / `ADMIN_PASSWORD` from `auth_service/.env`, used to create the
account on first start. auth no longer re-applies `ADMIN_PASSWORD` on every start (audit item 9): a password changed in
the app survives a restart.

## How to run it

```sh
make prod                     # secrets-prod (first time), build images :prod, start, wait until healthy
open http://localhost:8400
make prod-ps; make prod-logs s=studio; make prod-down   # down keeps the data
make runners-stop; make runners STACK=prod             # the Mac runners join prod (default STACK=dev)
```

## Essential checks

auth `make test lint` (the admin password survives a restart), `make contracts-check`, the dev and test stacks come up
as before (same compose config; `make test T="tests/test_hosts.py tests/test_auth.py"`), `make prod` healthy on its
ports, sign-in as the admin, `GET /v1/studio/hosts` and models show no fake host and no Echo, the Mac runners on prod.

## Later

Server deployment (a cloud VM): HTTPS in front of Kong, proxy trust, secure cookies and the public URL, remote GPU
hosts over TLS, an image registry and releases by tag, backups, firewall (docs/backlog.md, Deployment).
