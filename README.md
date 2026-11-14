# helmstudio

A self-hosted, multi-user image and video generation studio. Workflows (small graphs of typed nodes) run on GPU
runners; everything is reached through one gateway. Go owns auth and the APIs, Python runs the models, Postgres
holds the data, NATS JetStream carries the messages, an S3 store holds the media. The plan is in
[docs/](docs/README.md); the service architecture is [docs/plan/13-services.md](docs/plan/13-services.md).

Milestone M0 (the walking skeleton) runs end to end on a laptop with no GPU: a **fake runner** with an `echo`
workflow stands in for the models.

## First run

Needs Docker with Compose 2.20 or later, `make`, `openssl` and, for the contract suite, `uv`.

```sh
make up        # makes deploy/secrets/ and the .env files, builds, starts, waits until healthy
open http://localhost:8300
```

Sign in as the stack's admin, `admin@helm.test` / `helm test admin pass 1` (set by `deploy/compose/fake.yml`),
and create users on Admin › Users. There is no public sign-up. The CLI's API key is `deploy/secrets/helm_api_key`
(auth registers it as client `helm` on every start):

```sh
make -C cli build
cli/bin/helm login --url http://localhost:8300 --email you@example.com --api-key-file deploy/secrets/helm_api_key
cli/bin/helm run echo -p echo.steps=5 -p prompt.text="hello" --out .
```

## Daily commands

| Command | What |
|---|---|
| `make up` (= `make dev`) | build and start the stack, wait until healthy |
| `make down` | stop it; the data volumes (`pgdata`, `natsdata`, `seaweeddata`) stay |
| `make logs s=studio` | follow one service's logs (no `s`: all) |
| `make ps` | what is running |
| `make contracts-check` | validate `contracts/` (no stack needed) |
| `make test` | the contract suite on its own stack, `helmstudio-test` (Kong on 8390); `make test-down` stops it |
| `make resilience` | restart each service of `helmstudio-test` mid-run and check the stack recovers (`contract_tests/resilience.sh`) |
| `.tools/test-db` | Postgres for the Go services' DB tests (`helmstudio-test`, port 5464) |
| `make runners` | native GPU runners (M1) |

Each service has its own `Makefile` (at least `test` and `lint`; `dev` and `gen` where they apply).

## Ports

Only two ports are published, both on 127.0.0.1: Kong on `8300` (`KONG_HTTP_PORT`) and Postgres on `5463`
(`POSTGRES_HOST_PORT`), set in `deploy/.env`.

## Layout

```
contracts/        openapi.yaml, NATS message schemas, registry schema, fixtures, check.py
contract_tests/   black-box suite through Kong: the gate for every milestone
orago/            shared Go framework
kong/             gateway config + the API-key plugin
auth_service/     Go: sign-in, users, roles, API keys
studio_service/   Go: /v1/studio (sessions, workflows, runs, events, hosts)
files_service/    Go: signed /files links from the object store
runner_service/   Python: runs workflows (M0: the fake runner)
web_service/      Next.js app and its BFF
cli/              Go: the `helm` command (login, run)
deploy/           compose files, Postgres init, NATS config, secrets (git-ignored)
.tools/           make-secrets, copy-envs, nats-setup, storage-setup, test-db, create-admin
docs/             the plan and notes on the reference system
```
