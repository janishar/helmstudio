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
| `.tools/test-db` | Postgres for the services' DB tests (`helmstudio-test`, port 5464) |
| `make runners` | the native runners of this Mac (torch, mlx, native) in the background (`make runners-stop` stops them) |

Each service has its own `Makefile` (at least `test` and `lint`; `dev` and `gen` where they apply).

A stack whose `pgdata` volume predates a service's database (pipelines, added in M1) gets it once from
`deploy/postgres/init.sh` (idempotent; the first command recreates Postgres with the new secret, the volume stays):

```sh
.tools/make-secrets
docker compose -f deploy/docker-compose.yml -f deploy/compose/fake.yml up -d --wait postgres
docker compose -f deploy/docker-compose.yml -f deploy/compose/fake.yml exec -T postgres sh /docker-entrypoint-initdb.d/init.sh
make up
```

For `helmstudio-test`, `.tools/test-db` does the same.

## Native runners

Real models run outside Docker, natively on the GPU host. On Apple Silicon there are three envs, each its own
process: `torch` (PyTorch MPS; the Qwen Image and MiniMax H3 Image packs; uv project `runner_service/`), `mlx` (MLX;
the LTX and LLM packs; uv project `runner_service/envs/mlx`) and `native` (packs that run a native binary as a child
process: MiniMax H3 video through the vendored `h3`, `runner_service/vendor/h3c`; uv project `runner_service/` with no
extra, so no torch or MLX in the process). `make runner-native` builds `h3` first (`make h3`, about a second, Command
Line Tools clang); `h3` must run with its folder as the working directory (it compiles its Metal shaders from there).

```sh
make up                         # the stack (the fake runner stays in it for the contract suite)
make runners                    # all three, in the background (host mac, envs torch, mlx and native)
make runner-torch / runner-mlx / runner-native   # one of them
make runners-stop               # stops them (only processes make started, by their pid files)
```

They join the stack through the loopback ports compose publishes for them: NATS on `4322` as user `runner_mac_<env>`
(`deploy/secrets/nats_runner_mac_<env>_url`; each may only consume its `cmd.run.mac.<env>`, write its `mac.<env>`
registry and hosts keys and answer its `runner.unload.mac.<env>`) and the SeaweedFS S3 API on `8433`. Weights are read from
`HELMSTUDIO_MODELS` (default `~/GenAI/models`) and never downloaded, built into an image or put in the object store.
Each has a pid file and a log, `runner_service/.run/runner-mac-<env>.{pid,log}`, and a `GET /health` on its own
port (torch `8091`, mlx `8092`, native `8093`); a small sh supervisor restarts it after a stall (exit 70) or a
SIGKILL (137).

One GPU, one memory budget: studio admits a run only when its planned peak plus everything resident on the host's
envs fits the host's budget; otherwise it asks the env(s) holding the least recently used components to unload exactly
those (`runner.unload`), and keeps the run queued until they have (contracts/README.md, Two envs on one host).

## Remote GPU hosts

A Linux machine with an NVIDIA GPU joins the stack as another host (`gcp` in the test rig): only its runner runs
there, as a container with the NVIDIA runtime (`deploy/gpu-host/`: compose file, `.env.example`, and a README with
every step). It reaches only NATS and the object store, over TLS, with its own NATS user `runner_<h>_torch` and S3
identity `runner-<h>`; the dispatcher picks among the eligible hosts and each host keeps its own budget
(contracts/README.md, Several hosts).

```sh
make gpu-host-secrets GPU_HOSTS=gcp       # the host's credentials; the running stack reloads (no restart)
# STACK_TLS=on in deploy/.env, then make up: NATS TLS (same port) and S3 https (8443) via deploy/compose/tls.yml
make gpu-host-tunnel VM=<instance> ZONE=<zone>   # test rig: SSH reverse tunnel puts NATS and S3 on the VM's loopback
make gpu-host-tunnel-stop
```

`helmstudio-test` always runs with TLS (the fake runner joins over `tls://` and `https://`), from a self-signed CA
that `.tools/make-secrets` makes (`deploy/secrets/tls_ca`, `stack_tls_cert`, `stack_tls_key`).

## Ports

Published on 127.0.0.1, set in `deploy/.env`: Kong on `8300` (`KONG_HTTP_PORT`), Postgres on `5463`
(`POSTGRES_HOST_PORT`), and for native runners and the GPU host tunnel NATS on `4322` (`NATS_HOST_PORT`), the S3
API on `8433` (`S3_HOST_PORT`) and, with `STACK_TLS=on`, S3 https on `8443` (`S3_TLS_HOST_PORT`).
`helmstudio-test` uses 8390, 5464, 4323, 8434 and 8444.

## Layout

```
contracts/        openapi.yaml, NATS message schemas, registry schema, fixtures, check.py
contract_tests/   black-box suite through Kong: the gate for every milestone
helmgo/            shared Go framework
kong/             gateway config + the API-key plugin
auth_service/     Go: sign-in, users, roles, API keys
studio_service/   Go: /v1/studio (sessions, workflows, runs, events, hosts, batches)
files_service/    Go: signed /files links from the object store
runner_service/   Python: runs workflows (the fake runner in Docker; native envs torch, mlx and native)
pipelines_service/ Python, LangGraph: runs batches (NATS only, database pipelines, health on 8082)
web_service/      Next.js app and its BFF
cli/              Go: the `helm` command (login, run)
deploy/           compose files, Postgres init, NATS config, secrets (git-ignored), gpu-host/
.tools/           make-secrets, copy-envs, nats-setup, storage-setup, test-db, create-admin, gpu-host-tunnel
docs/             the plan and notes on the reference system
```
