# contracts

The single source for everything that crosses a service boundary ([docs/plan/13-services.md](../docs/plan/13-services.md),
section 17.8). Change the contract first, then the services. Services never import each other; each generates or
copies what it needs from here into its `gen/` (`make gen`) and commits it.

| File | What |
|---|---|
| `openapi.yaml` | OpenAPI 3.1: the public HTTP API behind Kong — `/v1/studio/*` (studio_service), `/files/*` (files_service), `/auth/*` (auth_service, tag `auth`, `x-status: built`) |
| `messages/envelope.schema.json` | NATS envelope `{type, version, id, job_id, owner_id, occurred_at, trace, data}`; `job_id` = the run id for every `cmd.run.*` / `evt.run.*` |
| `messages/cmd.run.execute.schema.json` | studio → runner: run one resolved graph (every param filled), write outputs under `output_prefix` |
| `messages/evt.run.{status,progress,output}.schema.json` | runner → studio's projector: lifecycle (`running`, then one of `succeeded`, `failed`, `cancelled`), one event per step, one per stored output |
| `messages/req.auth.validate.{token,role}.schema.json` | NATS request/reply with auth_service (not enveloped): `$defs/request`, `$defs/reply` |
| `messages/evt.auth.revoked.schema.json` | auth_service → studio: drop cached token validations |
| `messages/defs.schema.json` | shared definitions (uuid, object keys, seq, host/env names, the workflow graph) |
| `registry.schema.json` | KV values: `$defs/nodes`, `$defs/workflows`, `$defs/models` (bucket `registry`), `$defs/host` (bucket `hosts`) |
| `fixtures/{messages,registry}/` | `<name>.valid[.N].json` must pass, `<name>.invalid[.N].json` must fail (request/reply: `<type>.{request,reply}.…`) |
| `fixtures/registry/{nodes,workflows,models}.valid.json` | **canonical** fake-runner registry values (echo family): the fake runner publishes them verbatim, with its own `published_at` |
| `fixtures/registry/qwen-image.{nodes,workflows}.valid.json` | **canonical** torch-runner values (qwen-image family, host `mac`, env `torch`): published verbatim; `qwen-image.models.valid.json` is an example (presence and sizes are measured) |
| `check.py` | validates every fixture (a registry fixture may carry a family prefix, `<family>.<key>.…`), checks each canonical workflow against its node schemas (kinds, params, wire types, values and defaults in range), lints `openapi.yaml` and the house rules |

```sh
make -C contract_tests venv              # once: contract_tests' own .venv has the checker's dependencies
make -C contract_tests check-contracts   # exit 1 on any failure
```

## NATS

| Subject | Kind | Body | Producer → consumer |
|---|---|---|---|
| `cmd.run.<host>.<env>` | stream `CMD` (work queue) | `cmd.run.execute`, `Nats-Msg-Id <run_id>:<attempt>` | studio outbox relay → the runner of that host and env (durable `runner-<host>-<env>`) |
| `evt.run.<session_id>.status` | stream `EVT` (7 days) | `evt.run.status`, `Nats-Msg-Id <run_id>:<seq>` | runner → studio projector |
| `evt.run.<session_id>.progress` | stream `EVT` | `evt.run.progress`, same | runner → studio projector |
| `evt.run.<session_id>.output` | stream `EVT` | `evt.run.output`, same | runner → studio projector |
| `dlq.<original subject>` | stream `DLQ` (30 days) | the original command, unchanged | runner, for a command that fails its schema |
| `auth.validate.token`, `auth.validate.role` | core request/reply, queue group `auth` | bare JSON (see the schemas) | studio → auth |
| `evt.auth.revoked` | core | `evt.auth.revoked` | auth → every Go service's token cache |
| KV `registry` keys `<host>.<env>.nodes`, `.workflows`, `.models` | KV | `registry.schema.json` `$defs/nodes`, `workflows`, `models` | the runner writes its own keys once at start; studio reads |
| KV `hosts` key `<host>.<env>` | KV | `registry.schema.json` `$defs/host` | the runner rewrites it every 10 s; studio reads (older than 30 s = offline) |
| KV `cancels` key `<run_id>` (no schema) | KV | the request time, RFC 3339 text (ignored) | studio writes on `POST /v1/studio/runs/{run_id}/cancel`; the runner checks the key before it starts and at every step |

Host and env names are lower-case letters and digits (one subject token each). The fake runner is host `fake`,
env `fake`; the native torch runner is host `mac`, env `torch` (durable `runner-mac-torch`, subject `cmd.run.mac.torch`). The envelope `type` is the kind of message, never the subject.

## How the pieces meet

- **Ids.** studio_service owns every id. It creates the run row (`queued`), picks the host and writes the outbox row
  in one transaction; the runner reports by `job_id` = run id. M0 placement: the one live host (hosts key fresher
  than 30 s) whose env is the workflow's `env`; with none the run is created `failed` (`no live host for env <env>`)
  and nothing is sent.
- **Workflows.** A workflow (registry `workflows`) is `{id, title, family, env, nodes}`; `nodes` maps a node id to
  `{kind, params, in}`, `in` wires an input port to `<node id>.<output>`. A node schema (registry `nodes`) gives a
  kind's ordered `params` (`int`, `float`, `bool`, `string`, `text`, `enum`, `seed` with `default`, `min`, `max`,
  `step`, `enum`, `help`, `when`) and typed `inputs` / `outputs`. Port types: `Text`, `Image`, `Int`, `Float`, `Seed`,
  `Canvas`, and the family-scoped handles `Model`, `Conditioning`, `Latent` (they wire nodes of one family inside one
  workflow and never cross a process). `when` is a visibility expression (`model.variant != 'turbo'`), carried
  through as is: studio evaluates it in a later stage, the runner ignores it. `GET /v1/studio/workflows/{id}` serves the workflow with
  `node_schemas` for its kinds; the UI derives the form from them.
- **Resolving a run.** Values come from the node schema `default`, then the workflow's `params`, then
  `RunRequest.inputs` (`"<node id>.<param>": value`, checked against type, `min`/`max`, `enum`: 422). The command
  carries the whole graph with every param filled, so the runner needs no defaults.
- **The echo family (fake).** Workflow `echo`: `prompt` (kind `prompt`, param `text`) → `echo` (kind `echo/echo`,
  params `steps`, `delay_ms`, `fail`; input `text`: Text, output `image`: Image) → `output` (kind `output`, input
  `media`: Image). Running it: `status running` (seq 1), one `progress` per step (node `echo`, step 1..steps, each
  after `delay_ms`), the PNG written to `<output_prefix>0.png` and one `output` (index 0), then `succeeded`; when `fail` is true, no PNG and
  `failed` after the last step. Cancel is checked before each step.
- **Seeds.** A `seed` param is an integer (≤ 2^53−1); `null` or `-1` means random: the runner picks one and reports it
  in `evt.run.status` `succeeded` `data.resolved.seed` and in each `evt.run.output` `data.seed` (both ≥ 0).
- **The qwen-image family (torch).** Workflow `qwen-image` (env `torch`), text-to-image:
  `prompt` (kind `prompt`, `text`) · `seed` (kind `seed`, `seed` default -1) · `canvas` (kind `canvas`, `width`,
  `height` 256–4096 step 32, default 1024) · `model` (`qwen-image/model`, `variant` base|turbo, `precision` bf16|int8
  default int8) → `encode` (`qwen-image/encode`, in `model`, `prompt`; `transparent`) → `sample`
  (`qwen-image/sample`, in `model`, `cond`, `canvas`, `seed`; `steps` 1–200 default 40, hidden for turbo) → `decode`
  (`qwen-image/decode`, in `model`, `latent`; `tiled` auto|on|off) → `output`. Progress events add `phase`
  (`loading transformer`, `encoding`, `sampling`, `decoding`; a phase without steps is step 1 of 1) and `eta_s`.
  Reference images (multi-reference edit) are not in the graph yet.
- **Models and memory.** The registry `models` value lists, per model id, `variants` with `presence`
  (`present` | `missing`), `bytes` on disk and the `precisions` it can load as: a full-precision variant offers
  load-time quantization (`[bf16, int8]`), an already-quantized one only its own (`[int8]`). The hosts value adds
  `memory {budget_gb, used_gb}` and `resident` components (`component, model, variant, precision, gb, pinned`).
- **Events to the browser.** The projector applies each `evt.run.*` to the run row and appends it to the
  session's event log (per-session `seq`); studio appends its own `queued` and the `failed` / `cancelled` it decides
  itself. `GET /v1/studio/sessions/{id}/events` serves the log as JSON (`?since=`) or SSE (`Last-Event-ID`), with
  object keys turned into signed URLs (`SessionEvent` in openapi.yaml).
- **Hosts.** `GET /v1/studio/hosts` shows host, env, `idle` / `busy` / `offline`, `updated_at` and, when the runner
  reports them, `memory` and `resident`; never a run id or an owner.

## Rules

- **Claim check:** messages carry object keys under `u/<owner_id>/…`, never bytes or signed URLs. Producers only
  write keys under the envelope's `owner_id` prefix; run outputs live under `u/<owner_id>/runs/<run_id>/`
  (`<index>.<ext>`). `check.py` fails any message fixture with a `u/…` string outside its owner's prefix.
- **Envelope** is closed (`additionalProperties: false`); `data` is open: consumers ignore fields they don't know.
- **Versioning:** a message's `version` is the major version of its `data` schema (`const` in the schema).
  Additive, optional fields keep the version. Removing or renaming a field, changing a type or meaning, or making a
  field required is breaking: add `<type>` version N+1 as a new schema, publish both until every consumer reads
  N+1, then drop N. Envelope changes are breaking for every type. KV values carry `schema_version` the same way.
- **HTTP:** `info.version` follows semver; additive response fields and new optional request fields are minor;
  anything that breaks a generated client is a new major. Errors are always `{"detail": string}`. Long work is 202;
  a state conflict is 409. operationIds start with the service: `studio…`, `files…`, `auth…`.
- **Idempotency:** commands use `Nats-Msg-Id = <job_id>:<attempt>`; every `evt.run.*` carries a per-run `seq`
  (from 1, over all three kinds) and publishes with `Nats-Msg-Id = <job_id>:<seq>`; the projector dedupes on
  `(job_id, seq)`. A runner publishes the terminal status and waits for its ack before it acks the command.
- **Ownership:** every message has `owner_id`; cross-user reads answer 404 (not 403).
- **Change process:** edit the schema, add or adjust a valid **and** an invalid fixture, run `check.py`, regenerate
  each service's `gen/`.

## auth_service internals (not in openapi.yaml)

**`GET /verify/apikey`** — internal, called only by Kong's `apikey-auth-plugin` (`http://auth:8080/verify/apikey`);
Kong never routes it. Request header `x-api-key: <key>`. Answers:

- `200 {"client_id": "<uuid>", "client": "<name>", "scopes": ["..."]}` — active key (touches `last_used_at`, at most
  once a minute).
- `401 {"detail": "invalid api key"}` — missing, longer than 200 characters, unknown or revoked.
- `500 {"detail": "internal error"}`.

API keys are `helm_` + 43 URL-safe characters. Clients: `web` (the BFF, registered by auth on every start from
`WEB_API_KEY_FILE`) and `helm` (the CLI, made with `auth apikey helm`).

**NATS request/reply** (core NATS, queue group `auth`, not JetStream, bare JSON — no envelope):

| Subject | Request | Reply |
|---|---|---|
| `auth.validate.token` | `{"token"}` | `{user_id, email, roles, jti, family_id, expires_at}` or `{"error"}` |
| `auth.validate.role` | `{"token", "role"}` | the same + `allowed` (bool), or `{"error"}` |

`roles` (`user`, `dev`, `admin`) is omitted when empty. Errors: `invalid or expired token`, `bad request: …`,
`internal error` — treat any `error` as unauthenticated. Cache a success until `expires_at` and drop it on
`evt.auth.revoked` (`scope: family` → matching `family_id`; `scope: user` → every entry of `user_id`).
