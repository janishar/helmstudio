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
| `messages/cmd.pipeline.{submit,resume,pause,stop,retry,pick}.schema.json` | studio → pipelines_service: start, continue, pause, stop a batch; retry one row (optionally from a step); a gate's pick |
| `messages/evt.pipeline.{batch,row,step}.schema.json` | pipelines_service → studio's projector: a batch's status, a row's whole state (parked on a gate: `waiting`), one graph step of a pipeline row |
| `messages/req.pipelines.state.get.schema.json` | NATS request/reply studio → pipelines (not enveloped): a row's state at one checkpoint |
| `messages/evt.studio.run_finished.schema.json` | studio → pipelines_service: a batch run ended (any terminal status) |
| `messages/req.studio.runs.create.schema.json` | NATS request/reply pipelines → studio (not enveloped): create a batch-lane run, idempotent on its key |
| `messages/evt.auth.revoked.schema.json` | auth_service → studio: drop cached token validations |
| `messages/defs.schema.json` | shared definitions (uuid, object keys, seq, host/env names, the workflow graph, param values: `inputValue` with `imageRef` / `loraRef`, `resolvedValue` with `image` / `lora`) |
| `registry.schema.json` | KV values: `$defs/nodes`, `$defs/workflows`, `$defs/models` (with a family's `loraBlock`), `$defs/loras`, `$defs/looks` (bucket `registry`), `$defs/host` (bucket `hosts`) |
| `fixtures/{messages,registry}/` | `<name>.valid[.N].json` must pass, `<name>.invalid[.N].json` must fail (request/reply: `<type>.{request,reply}.…`) |
| `fixtures/http/` | HTTP bodies, `<Schema>.{valid,invalid}[.N].json` against `openapi.yaml` `components/schemas/<Schema>` (the names in `check.py` `HTTP_SCHEMAS`) |
| `fixtures/registry/{nodes,workflows,models,looks}.valid.json` | **canonical** fake-runner registry values (echo family): the fake runner publishes them verbatim, with its own `published_at` |
| `fixtures/registry/qwen-image.{nodes,workflows}.valid.json` | **canonical** torch-runner values (qwen-image family, host `mac`, env `torch`): published verbatim; `qwen-image.models.valid.json` is an example (presence and sizes are measured), so are `qwen-image.{loras,looks}.valid.json` |
| `fixtures/graphs/` | the **shared graph-rule fixtures** (Graph rules, below): `palette.json` (a `NodePalette`), `valid.*` / `invalid.*` (a `WorkflowDoc` and its expected problems), `autowire.*`, `wiredrop.*`. studio_service (Go) and the web editor (TypeScript) both run them |
| `fixtures/pipelines/` | the **shared pipeline-rule fixtures** (Pipeline rules, below): `workflows.json` (the workflows the caller can run), `valid.*` / `invalid.*` `{description, pipeline, problems}`, optionally `looks` and `loras` (the caller's usable looks and LoRAs, as the look fixtures take them). studio_service and the web editor both run them |
| `fixtures/mentions/` | the **shared mention and look fixtures** (Mentions, Looks, below): `scan.json` (prompt → parts), `fit.json` (an item and a workflow → the fit reason), `resolve.*.json` (a graph with merged values, a library, chosen looks → the expanded inputs, `typed`, `elements`, `looks`, or `errors`), against `fixtures/graphs/palette.json`. studio_service and the web editor both run them |
| `fixtures/loras/` | the **shared LoRA fixtures** (LoRA normaliser, below): header-only files `{description, name, metadata, tensors: {key: {dtype, shape}}, values: {alpha key: number}, ignore?, expect}` that runner_service and studio_service tests turn into synthetic safetensors (random A/B values, `values` for the alphas); `expect` = `{key_style, rank, total, applied, ignored, unapplied, error, family, licence}` against the canonical qwen-image lora block (`ignore`, when present, replaces its ignore list); `detect/families.json`: family detection cases (families with their envs and blocks, a LoRA fixture, the detected family) |
| `check.py` | validates every fixture (a registry fixture may carry a family prefix, `<family>.<key>.…`), checks each canonical workflow against its node schemas (kinds, params, wire types, values and defaults in range), runs the graph fixtures through its reference implementation of the graph rules (`graph_problems`, `autowire`, `wire_drop`), the pipeline fixtures through `pipeline_problems`, the mention and look fixtures through `scan` / `look_fit` / `resolve_mentions`, the LoRA fixtures through `normalise`, lints `openapi.yaml` and the house rules |

```sh
make -C contract_tests venv              # once: contract_tests' own .venv has the checker's dependencies
make -C contract_tests check-contracts   # exit 1 on any failure
```

## NATS

| Subject | Kind | Body | Producer → consumer |
|---|---|---|---|
| `cmd.run.<host>.<env>` | stream `CMD` (work queue) | `cmd.run.execute`, `Nats-Msg-Id <run_id>:<attempt>` | studio outbox relay → the runner of that host and env (durable `runner-<host>-<env>`) |
| `evt.run.<session_id>.status` | stream `EVT` (7 days) | `evt.run.status`, `Nats-Msg-Id <run_id>:<attempt>:<seq>` | runner → studio projector |
| `evt.run.<session_id>.progress` | stream `EVT` | `evt.run.progress`, same | runner → studio projector |
| `evt.run.<session_id>.output` | stream `EVT` | `evt.run.output`, same | runner → studio projector |
| `dlq.<original subject>` | stream `DLQ` (30 days) | the original command, unchanged | runner, for a command that fails its schema |
| `auth.validate.token`, `auth.validate.role` | core request/reply, queue group `auth` | bare JSON (see the schemas) | studio → auth |
| `cmd.pipeline.submit`, `.resume`, `.pause`, `.stop`, `.retry`, `.pick` | stream `CMD` (work queue) | `cmd.pipeline.<same>`, `Nats-Msg-Id <batch_id>:<n>` (n = the batch's command counter, submit = 1) | studio outbox relay → pipelines (durable `pipelines`, filter `cmd.pipeline.>`, one at a time, in order) |
| `evt.pipeline.<batch_id>.batch`, `.row`, `.step` | stream `EVT` | `evt.pipeline.batch`, `.row`, `.step`, `Nats-Msg-Id <batch_id>:<seq>` | pipelines → studio projector (durable `studio-pipelines`, filter `evt.pipeline.>`) |
| `evt.studio.<session_id>.run_finished` | stream `EVT` | `evt.studio.run_finished`, `Nats-Msg-Id <run_id>:finished` | studio outbox relay → pipelines (durable `pipelines-runs`, filter `evt.studio.>`) |
| `studio.runs.create` | core request/reply, queue group `studio` | bare JSON (`req.studio.runs.create`) | pipelines → studio |
| `pipelines.state.get` | core request/reply, queue group `pipelines` | bare JSON (`req.pipelines.state.get`) | studio → pipelines (5 s) |
| `evt.auth.revoked` | core | `evt.auth.revoked` | auth → every Go service's token cache |
| KV `registry` keys `<host>.<env>.nodes`, `.workflows`, `.models`, `.loras`, `.looks` | KV | `registry.schema.json` `$defs/nodes`, `workflows`, `models`, `loras`, `looks` | the runner writes its own keys once at start; studio reads |
| KV `hosts` key `<host>.<env>` | KV | `registry.schema.json` `$defs/host` (`run_id` and `attempt` while busy) | the runner rewrites it every 10 s; studio reads (older than 30 s = offline) |
| KV `cancels` key `<run_id>` (no schema) | KV | the request time, RFC 3339 text (ignored) | studio writes on `POST /v1/studio/runs/{run_id}/cancel`; the runner checks the key before it starts and at every step |

Host and env names are lower-case letters and digits (one subject token each). The fake runner is host `fake`,
env `fake`; the native torch runner is host `mac`, env `torch` (durable `runner-mac-torch`, subject `cmd.run.mac.torch`). The envelope `type` is the kind of message, never the subject.

## How the pieces meet

- **Ids.** studio_service owns every id. It creates the run row (`queued`, lane `interactive`, attempt 1); the
  dispatcher later writes the outbox row; the runner reports by `job_id` = run id. With no eligible host at creation
  the run is created `failed` (`no live host for env <env>`) and nothing is sent.
- **Workflows.** A workflow (registry `workflows`) is `{id, title, family, env, nodes}`; `nodes` maps a node id to
  `{kind, params, in}`, `in` wires an input port to `<node id>.<output>`. A node schema (registry `nodes`) gives a
  kind's ordered `params` (`int`, `float`, `bool`, `string`, `text`, `enum`, `seed`, `image`, `rows` with `default`,
  `min`, `max`, `step`, `enum`, `item`, `mention`, `help`, `when`; see Image inputs) and typed `inputs` / `outputs`. Port types: `Text`, `Image`, `Int`, `Float`, `Seed`,
  `Canvas`, and the family-scoped handles `Model`, `Conditioning`, `Latent` (they wire nodes of one family inside one
  workflow and never cross a process). `when` is a visibility expression (`model.variant != 'turbo'`), carried
  through as is: studio evaluates it in a later stage, the runner ignores it. `GET /v1/studio/workflows/{id}` serves the workflow with
  `node_schemas` for its kinds; the UI derives the form from them.
- **Resolving a run.** Values come from the node schema `default`, then the workflow's `params`, then
  `RunRequest.inputs` (`"<node id>.<param>": value`, checked against type, `min`/`max`, `enum`: 422). The command
  carries the whole graph with every param filled, so the runner needs no defaults.
- **The echo family (fake).** Workflow `echo`: `prompt` (kind `prompt`, param `text`) → `echo` (kind `echo/echo`,
  params `steps`, `delay_ms`, `fail`, `refs` (rows, max 10, item `image`: Image, default `[]`); input `text`: Text,
  output `image`: Image) → `output` (kind `output`, input `media`: Image). Before its first step the fake runner reads
  every `refs` image (Image inputs, below); the PNG it writes is unchanged. Running it: `status running` (seq 1), one `progress` per step (node `echo`, step 1..steps, each
  after `delay_ms`), the PNG written to `<output_prefix>0.png` and one `output` (index 0), then `succeeded`; when `fail` is true, no PNG and
  `failed` after the last step. Cancel is checked before each step. The family's LoRA stand-in `echo/lora-stack`
  (title `LoRA Stack`, not in the `echo` workflow): input `text`: Text, output `text`: Text, param `rows` as
  `<family>/lora-stack`'s (LoRAs, below); it writes `<text> [LoRA <name> x<strength>, …]` (no rows: the text as it
  is) and applies nothing. Its models entry carries a `lora` block (`component` `transformer`, `modules` = exactly the
  modules the qwen-image fixtures of `fixtures/loras/` name, `renames` and `ignore` `[]`), so those files fit echo on
  a stack without a GPU.
- **Seeds.** A `seed` param is an integer (≤ 2^53−1); `null` or `-1` means random: the runner picks one and reports it
  in `evt.run.status` `succeeded` `data.resolved.seed` and in each `evt.run.output` `data.seed` (both ≥ 0).
- **The qwen-image family (torch).** Workflow `qwen-image` (env `torch`), text-to-image, and multi-reference edit
  when `encode.refs` is not empty:
  `prompt` (kind `prompt`, `text`) · `seed` (kind `seed`, `seed` default -1) · `canvas` (kind `canvas`, `mode`
  auto|fixed default auto, `width`, `height` 256–4096 step 32, default 1024) · `model` (`qwen-image/model`,
  `variant` base|turbo, `precision` bf16|int8 default int8) → `lora` (`qwen-image/lora-stack`, in `model`, `rows` default
  `[]`: LoRAs, below; only `sample` reads `lora.model`) · `encode` (`qwen-image/encode`, in `model`, `prompt`;
  `transparent`, `refs`: rows, 0–10 items `{image: Image}`, mention `<image{n}>`, default `[]`) → `sample`
  (`qwen-image/sample`, in `model` = `lora.model`, `cond`, `canvas`, `seed`; `steps` 1–200 default 40, hidden for turbo) → `decode`
  (`qwen-image/decode`, in `model`, `latent`; `tiled` auto|on|off) → `output`. Progress events add `phase`
  (`loading transformer`, `encoding`, `sampling`, `decoding`; a phase without steps is step 1 of 1) and `eta_s`.
  Canvas `auto`: with references, the size is about 1 MP at the **last** reference's aspect (the diffusers pipeline's
  `calculate_dimensions(1024², w/h)`: sides rounded to multiples of 32), as the old studio did with no size given
  (generate.py `plan_size(…, images[-1], …)`); without references, `width` × `height`. `fixed`: always `width` ×
  `height` (the UI sets it when the user picks a size in an edit). The sample stage applies it (it sees the
  references through `cond`); the take's `width` / `height` are the real size, `made_with` keeps `mode: auto`.
  The registry also has the core source kind `image` (param `image`: image, output `image`: Image), unused by
  `qwen-image` in this version.
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

## The dispatcher (plan 17.5)

The queue is studio's `runs` table; a dispatcher in studio_service decides what goes out. It runs after every run
create, terminal status and cancel, and every few seconds.

- **Eligible host** for a run: its hosts key is fresher than 30 s, its env is the workflow's `env`, and its registry
  `<host>.<env>.workflows` publishes the workflow id, and its `<host>.<env>.loras` lists every **shared** LoRA of the
  run's resolved graph (own LoRAs are fetched by any host). None at creation, or at any later pass while the run is
  queued: the run fails at once with `no live host for env <env>` (`no live host for env <env> has LoRA <name>` when
  live hosts of the env exist but none has the first such LoRA, graph order). A queued run whose own LoRA was deleted
  since it was created fails at its next pass: `LoRA <name> was deleted`.
- **One run at a time per host**, across envs: a host is free when studio has no run dispatched to it (any env) that
  is not yet terminal. Studio's own table decides this, not the runner's `state`.
- **Order**, when a host is free: `interactive` before `batch` (batch runs come from `studio.runs.create`); within a lane,
  round-robin between users: the user whose last released run is the oldest goes first (never released = first; a
  tie goes to the older oldest queued run); within one user, the oldest queued run, except that a queued run whose model is
  resident on that host goes first. A run's model is its `<family>/model` node (`variant`, `precision`) with the model
  id of the family in the host's `models` key; resident = an entry of the hosts value's `resident` with the same
  `model`, `variant` and `precision`, and the same baked LoRAs (`loras`: the run's `bake` rows' `(lora_id, strength)` in
  graph order, one LoRA in two chained stacks once with the summed strength; none = an entry without `loras`).
- **Placement**: among the run's eligible free hosts, one where its model is resident, else the one idle longest
  (oldest last terminal run on it). Releasing = in one transaction set `runs.host`, write the outbox row
  (`cmd.run.execute` on `cmd.run.<host>.<env>`, `Nats-Msg-Id <run_id>:<attempt>`).
- **Position** (`Run.position`, only while queued): 1 + the queued runs of anyone the order above would release on the
  run's host before it, computed on read. `GET /v1/studio/queue` shows the caller's runs and, per host, only a count of
  other users' queued runs.
- **Lease**: a dispatched run holds its host while the host's key is fresh and, from 30 s after it was released, the
  key's (`run_id`, `attempt`) is this run's. Otherwise the lease is lost. First loss: purge the subject
  `cmd.run.<host>.<env>` (it holds at most that command), set `attempt` 2, status `queued`, append a
  `status` event `queued` with `attempt: 2`, and let the next pass place it again (it fails at once if no host is
  eligible). Second loss: `failed`, `runner on host <host> stopped responding; gave up after 2 attempts`. The retry
  drops attempt 1's outputs: their run output rows go and their takes are deleted (the sweep removes the objects);
  attempt 2 writes under its own `output_prefix` (`…/runs/<run_id>/2/`).
- **Fair-order history**: each owner's last release is kept in its own row (`dispatch_owners`), so deleting a
  session does not reset an owner's turn.
- **Starting up**: until studio's watches of KV `hosts` and `registry` have delivered their current values, a pass
  releases, retries and fails nothing, and a new run is never failed for want of a host.
- **Cap**: at most 100 open (queued or running) interactive runs per user; `POST …/runs` above it is a 409. Batch-lane
  runs are not counted (a batch is bounded by its 500 rows and the 5 unfinished batches a user may have).
- **Held**: a queued batch run whose batch is `paused` or has `control: pause` is skipped by every pass (no
  `position`); it becomes eligible again on Resume. The no-live-host check still fails it while held.
- **Attempts in events**: every `evt.run.*` carries `data.attempt` (absent = 1), `seq` restarts at 1 per attempt and
  `Nats-Msg-Id` is `<run_id>:<attempt>:<seq>`. The projector dedupes on `(job_id, attempt, seq)` and drops events of an
  attempt that is not the run's current one. The runner sets `attempt` in its hosts value while busy.
- **Cancel** of a run not yet released: `cancelled` at once, never sent. Of a released one: KV `cancels`, as before.
- Not in API 1.4: evict hand-over between two envs of one host, memory admission.

## Takes, the gallery and uploads (plan 9.1, 17.2)

- **Every run output is a take.** The projector, applying `evt.run.output`, inserts the take in the same transaction
  (owner = the run's owner, `source: create`, or `source: pipeline` with `batch_id` for a batch run, `kind`, `bytes`, `width`, `height` from the event, `content_type` from
  the key's extension, `made_with` snapshotted from the run: workflow id and title, the resolved inputs of every node
  with the event's `seed` in place of a random one, the `prompt` node's text, the `<family>/model` node) and puts
  its id in the output (`RunOutput.take_id`). The take keeps the object key; the API shows only signed links.
- **Lineage**: see Image inputs: `parents` and, on `GET /v1/studio/takes/{id}`, `children`, stored as edges.
- **Deleting** a take is soft (gone from every answer at once); a sweep deletes the object later. Deleting a session
  keeps its takes (`session_id`, `run_id` → null).
- **Uploads**: `POST /v1/studio/uploads` stores a pending take and returns `upload_url`, a signed `PUT` link to
  files_service for `u/<owner>/uploads/<take_id>.<png|jpg|webp>` (15 minutes). The browser PUTs the bytes through Kong
  with exactly the signed `Content-Type` and `Content-Length`; files_service streams them to the object store and never
  overwrites an object. `POST /v1/studio/uploads/{take_id}/complete` checks size and image header (HEAD + a 64 KB
  range read of the object), records width and height and shows the take. The original is its own thumbnail in API
  1.2. Images only (png, jpeg, webp), at most 50 MB; Kong must let a 50 MB `PUT /files/*` through.
- **`/files` signatures** (the shared `FILE_URL_SECRET`, lower-case hex HMAC-SHA256):
  - `GET`/`HEAD /files/<key>?exp=&sig=`: `sig = HMAC(key + "\n" + exp)`.
  - `PUT /files/<key>?exp=&bytes=&ct=&sig=`: `sig = HMAC("PUT\n" + key + "\n" + exp + "\n" + bytes + "\n" + ct)`
    (`ct` URL-encoded in the query, raw in the HMAC). A GET signature never verifies as a PUT. Answers: 204 stored,
    403 bad / expired / path, 409 exists, 411 `Content-Length` not the signed bytes, 415 `Content-Type` not `ct`.

## Batches and the pipeline engine (plan 7.2, 16.3)

A batch runs one workflow once per row of inputs. Studio owns the API and the batch record; **pipelines_service**
(Python, LangGraph; database `pipelines`, NATS user `pipelines`, compose service `pipelines`, health on 8082) runs it.

- **Submit** (`POST /v1/studio/batches`): rows come from pasted `lines`, a `table` (CSV parsed by the client) or a
  `grid` (cartesian product, at most two axes), each optionally repeated `seeds.count` times with consecutive seeds.
  A row's inputs = base `inputs`, then its values, then its seed. Every check happens here (workflow, each row's
  inputs with the run rules, 1–500 rows, at most 5 unfinished batches per user, a live host, at most 768 KB of row
  inputs); a failure is 422 naming the input row. Image values ride in rows by take id: a `table` cell or `grid` value
  is the param's value (`{take_id}`, or a rows list); `lines` on an image param, or on a rows param whose item has one
  Image field, take one take id per line (gallery multi-select "Use in…"). Studio checks them at Submit and resolves
  them again when `studio.runs.create` makes each row's run (a take deleted since: that row fails, `invalid`). Studio stores the batch `queued` with its **snapshot** (the
  registry workflow as published now), every row (`queued`, attempt 1), and the outbox row `cmd.pipeline.submit`
  carrying the snapshot and every row's full inputs. The batch runs that snapshot, never a later version.
- **Threads**: one LangGraph thread per row, `thread_id = "<batch_id>:<row>"`, state small plain JSON (row inputs,
  run ids, take ids, status, counters; never media), `durability="sync"` with `AsyncPostgresSaver` in `pipelines`.
  A batch of one workflow is the graph `START → step.submit → step.wait → END`.
- **A step is two nodes.** `submit` asks `studio.runs.create` (lane `batch`) under the **idempotency key**
  `<batch_id>:<row>:<row attempt>:<node>:<loop>:<branch>` (here `…:<row>:<attempt>:step:0:0`) and records the run id;
  asking again returns the same run with its current status. `wait` contains only `interrupt()`. When
  `evt.studio.<session_id>.run_finished` for that run arrives, the thread is resumed with it whatever the outcome
  (succeeded, failed or cancelled). A reply that already shows a terminal status resumes the thread at once; the
  event that follows finds no thread waiting on that run and is dropped. Resumes of one thread are serialised.
- **Row 1 first**: at Submit only row 1 runs. Once row 1 has succeeded, failed or been cancelled, every other row is
  released. Ordering between rows and users is the dispatcher's job (batch lane after interactive, round-robin
  between users, loaded models first); pipelines never paces runs itself.
- **Failures**: a failed row is marked with its error and the batch continues. Three rows in a row (by finish
  order) failing with the same error pause the batch (`reason: 3 rows in a row failed: <error>`). A row a gate's reject
  decided (`rejected at <gate>`, or any end after its `then`) is a reviewer's answer and never counts.
- **Two records.** LangGraph's checkpoints resume threads and nothing else reads them. The batch record the UI reads
  (batch, rows, counts, batch events) lives in the `studio` database, built by studio's projector from
  `evt.pipeline.*` (pipelines is the only writer of `status`; studio writes `queued`, `control` and a retried row).
  `BatchRow.status` is the row's end once pipelines reports one; before that `running` while its run runs, else
  `queued`. Counts are computed from the rows.
- **Events**: `seq` per batch over both kinds, committed in pipelines' database before the publish (gaps are normal,
  never reused). Each event is a whole state, so the projector keeps the newest per row (and for the batch) and
  ignores older seqs and row events of an older attempt. On start and on Resume, pipelines republishes every
  unfinished row.
- **Control** (Pause, Resume, Stop, Retry) travels as `cmd.pipeline.*` commands written by studio's outbox in the
  transaction that changes the batch record, not as a KV key like `cancels`: the change and the command commit
  together; they share one ordered, redelivered durable consumer with submit; and pipelines is event-driven (a
  parked thread never polls, unlike a runner that checks `cancels` at every step). Studio enforces the part that
  must be immediate itself, through the dispatcher.
  - **Pause**: `control: pause`; the batch's queued runs are held (not released, no position); running runs finish
    and their rows complete; pipelines starts no new rows and reports `paused`. Runs asked for while paused are
    created `queued` and held.
  - **Resume**: `control: run`; held runs become eligible; pipelines continues every unfinished row and reports
    `running`.
  - **Stop**: `control: stop`; studio cancels the batch's queued runs at once and puts released ones in KV
    `cancels` (takes already stored stay); runs asked for later are created `cancelled`; pipelines marks rows never
    started `cancelled` and reports `stopped` when every row is final. Stop is final.
  - **Retry** (failed rows only): studio sets the row `queued`, attempt + 1; pipelines reruns the row's thread from
    its start; the new attempt in the key makes a new run. A `done` batch reports `running` until the row ends: it is
    unfinished again, so the retry is refused (409, as Submit) while the user has 5 unfinished batches.
- **Resume after a crash**: when pipelines_service starts, every batch it had running is reported `paused`
  (`reason: the pipeline service restarted; press Resume`) and waits for the user's Resume (held as above; runs
  already released keep running). Their `run_finished` events wait in `EVT` and resume their threads when consumed,
  paused or not. On Resume: threads that were mid-node continue from their last checkpoint; a step whose run id was
  never recorded asks `studio.runs.create` again under the same key and gets the same run (or the run it created
  before the crash); a known result resumes its thread. Nothing runs twice: one key, one run. A redelivered
  `cmd.pipeline.submit` for a batch pipelines already has continues it the same way.
- **Ending**: `done` when every row has ended (some may have failed), `stopped` after Stop, `failed` when the engine
  cannot run the batch at all (`reason`). Then its threads are deleted; the snapshot stays in `pipelines` (Retry).
  Takes made by a batch have `source: pipeline` and the batch's `batch_id` (its collection).

## Image inputs (API 1.4)

- **Params.** `image`: one Image, default `null` (a run needs one: `<node>.<param>: an image is required`). `rows`: an
  ordered list of items, default `[]`; `min`/`max` bound the count (max ≤ 100); `item` maps each field to a port type
  (only `Image` in this version: required in every item) or a param schema without `name` (optional, its default
  when left out); `mention` (`<image{n}>`) is how the prompt names item n, from 1: the rows editor labels items with
  it, the prompt travels as typed, the pack never rewrites it. The core source kind `image` outputs its param as
  `Image`. A registry workflow never holds a take (its `image` params are null, its rows have no Image item).
- **Over HTTP** (RunRequest.inputs, batch rows, `made_with`, `reuse`, `use`) and in every pipelines message an Image
  value is `{"take_id": "<uuid>"}` (`imageRef`, closed): never an object key or a URL. Example:
  `"encode.refs": [{"image": {"take_id": "…"}}, {"image": {"take_id": "…"}}]`.
- **Studio resolves** each one when it creates the run (Create, and `studio.runs.create` for a batch row), as the
  caller (row-level security): the take must be the caller's, completed, not deleted, `kind: image`; studio HEADs its
  object (it must exist) and takes its ETag (a run output's comes from `evt.run.output` `etag`; one recorded without it
  is hashed on first use). Errors are 422 naming the param and item from 1:
  `encode.refs[2].image: take <id> not found` (unknown, another user's, deleted, pending, object missing: one
  message, nothing leaks), `… take <id> is not an image`, `… take <id> has no size` (an output recorded without
  `width` / `height`), `encode.refs: at most 10 items`, `encode.refs[1]: unknown field <f>`. A batch row: the same text in the 422 (`row 3: …`) or the `invalid` reply.
- **In `cmd.run.execute`** every Image is resolved (`defs image`): `{take_id, key, content_type, width, height, bytes,
  etag}`, `key` under `u/<owner_id>/` (the take's object: `…/runs/<run_id>/<attempt>/<n>.<ext>` or
  `…/uploads/<take_id>.<ext>`). `check.py`'s foreign-key rule covers these keys like any other.
- **The runner** reads only the keys in the command's image values, only under the envelope owner's prefix (anything
  else: the command fails its check and goes to `dlq`), straight from the object store, never through a URL. It checks
  the size against `bytes` and reads with `If-Match: <etag>`; a missing object, another size or another ETag (the object
  was PUT again after studio checked it) fails the run (`input image <take_id> is gone`). It decodes PNG, JPEG (a
  multi-picture JPEG: its first frame) and WebP of at most 50 MP (`… is larger than 50 MP`; uploads are capped the
  same), and applies a JPEG's EXIF orientation, as studio does when it records an upload's `width` / `height`. Its
  cache key for an image and everything derived from it (VL / VAE encodings) includes `(owner_id, key, etag)`; keys
  are immutable, the ETag guards that.
- **Lineage.** When the projector stores a take, `parents` = the take ids of every Image value of the run's inputs,
  each once, in input order (node, then item); stored as edges. `made_with.inputs` keeps the values as `{take_id}`.
  A deleted parent is left out of `parents`; `GET /v1/studio/takes/{id}` adds `children` (made from it, newest first,
  at most 50, `{id, kind, title, thumb_url}`) for the gallery's "Used in".
- **"Use in…"** `POST /v1/studio/takes/{id}/use {workflow_id, param?}` → `{workflow_id, inputs}` with one key: the
  workflow's only image or Image-rows param (or `param`) holding this take (as reference 1). The client merges it
  over the form. 422 when the workflow has no such param, several and no `param`, or the take is not an image.

## User workflows (API 1.5)

A user can open any workflow as a node graph (Create's Studio | Graph switch), edit it and save it as their own.
Studio and Graph are two views of one `WorkflowDoc`; a run always names a saved workflow by id (an edited, unsaved
graph is saved first: "Save" for one's own, "Save as my workflow" for a template).

- **Document** (`WorkflowDoc`): `title`, `family`, `env`, `nodes` (node id → `{kind, params?, in?, bypass?,
  position?}`), optional `studio`. Templates are the registry workflows (`source: template`, slug ids, read-only);
  a user's own are `source: mine`, UUID ids, owner-scoped by RLS (another user's id is 404), at most 200 per user,
  with `based_on` (the id it was copied or saved from, or null), `version` (1, +1 on every PUT and PATCH),
  `created_at`, `updated_at`.
- **Endpoints** (`/v1/studio`): `GET /workflows?source=template|mine` (templates by title, then mine newest first;
  no `problems`); `GET /workflows/{id}` (either kind, `node_schemas` for every kind the registry has, `problems`
  against the live palette); `POST /workflows` (a whole doc with optional `based_on`, or `{based_on, title?}` to copy:
  201); `PUT /workflows/{id}` (`WorkflowUpdate` = doc + `version`; stale = 409); `PATCH` (`{title}`); `DELETE` (204;
  409 while a run of it is queued/running or a batch of it queued/running/paused); `POST /workflows/validate`
  (`WorkflowValidation {problems, can_save, can_run}`, nothing saved); `GET /nodes?env=` (the palette). POST and PUT
  with a problem that blocks saving answer 422 `WorkflowError {detail, problems}`; problems that block only running
  are saved and returned in `problems`. PUT, PATCH and DELETE on a template id are 409.
- **Studio block** (`StudioLayout`, plan 04-workflows.md section 7): `composer`, `quick` (lists of paths; `quick` also
  holds the library tiles `elements`, `look:<group>`: Looks, composer tiles),
  `inspector` (`[{title, items}]`), `labels` (path → field title), `presets` (name → `{path: value}`, at most 8).
  A path is `<node>.<param>` or `<node>.bypass`. Present = Create shows exactly the placed paths, each at most once;
  every other param keeps its graph value. Absent = the derived form (every param, as today). Graph view "Show in
  Studio" on a param: if `studio` is absent the editor first writes the derived form out as a block (the web's own
  derivation, kept stable), then appends the path to the inspector section titled like the node's schema title
  (created last when missing); "Hide" removes the path from wherever it is placed (and its label). Deleting a node
  removes its paths from `studio` and its keys from every preset; wiring a param removes it from `studio`.
- **Runs, batches and takes** name a user workflow by id like a template (`RunRequest.workflow_id`,
  `BatchCreate.workflow_id`, `TakeUse.workflow_id`). Creating a run checks the stored version with the graph rules
  (any problem: 422 `workflow cannot run: <message>`), stores the resolved graph as the run's snapshot and
  `Run.workflow_version`; the dispatcher sends that snapshot, so editing or deleting the workflow never changes a
  queued, running or past run. Eligible host for a user-workflow run: live, the workflow's `env`, and its
  `<host>.<env>.nodes` publishes every kind of the snapshot (instead of publishing the workflow id). A batch's
  snapshot is the version at Submit (nodes keep `bypass`, no `position`, no `studio`); `studio.runs.create` resolves
  rows against it as before, so Retry after an edit or a delete still runs the old graph.
- **Takes**: `made_with` keeps `workflow_id`, `workflow_title` as it was, and `workflow_version`. `POST
  /takes/{id}/reuse` adds `workflow_status`: `current`, `changed` (inputs that are no longer a settable param of the
  current version are dropped), `deleted` (inputs as recorded; running it is 422). `use` is unchanged.
- **Resolving a run of a graph**: `inputs` may also set `<node>.bypass` (a bool, bypassable nodes only); an input
  naming a wired param is 422 (`<node>.<param>: wired from <ref>; it cannot be set`). Studio then splices every
  bypassed node out (each consumer of output O is rewired to the source of O's pass-through input, following chains
  of bypassed nodes) and drops `position`, so `cmd.run.execute` never carries `bypass` or `position`. A wired value
  param reaches the stage as that param (the runner reads `in` keys that name params; its `params` entry is still
  filled and ignored). An unwired optional input is simply absent from the stage's inputs.
- **Before studio's registry watches have delivered**, POST/PUT `/workflows`, `/workflows/validate`, `/nodes` and
  runs of user workflows answer 503.

## Graph rules (normative)

studio_service (Go) and the web editor (TypeScript) implement exactly these rules and both test against
`fixtures/graphs/`; `check.py` holds a reference implementation (`graph_problems`, `autowire`, `wire_drop`). The
**palette** is every node kind a live host (fresh `hosts` key) publishes, with the envs that publish it
(`GET /v1/studio/nodes`, `PaletteNode.envs`); a kind not in it is **unknown**. `family(kind)` is the part before
`/` (`qwen-image/sample` → `qwen-image`), none for a core kind (`prompt`). A schema's param and input names are
disjoint and no param is named `bypass` (registry.schema.json).

**Types.** A wire `in[key] = "<src>.<out>"` carries the type of `src`'s output `out`. `key` is an input port (its
type) or a **wireable value param** — values are both parameter and port: `int`→`Int`, `float`→`Float`,
`seed`→`Seed`, `string`/`text`→`Text`, `image`→`Image` (`bool`, `enum`, `rows` take no wire). The types must be
equal. The handles `Model`, `Conditioning`, `Latent` must also join two kinds of the same family (`Model[f]` is not
`Model[g]`). One wire per key (the `in` map has one value per key; connecting to a wired key replaces the wire); an
output may feed any number of keys.

**Inputs.** An input is required unless its port has `optional: true`. A node that is not bypassed needs every
required input wired. **Bypass**: a bypassed node passes through, for each of its outputs, the first input (schema
order) of the same type; a kind with no outputs or with an output that has no same-type input cannot be bypassed.
A bypassed node needs exactly those pass-through inputs wired (its other inputs and its params do not matter).

**Problems.** Each is `{code, message, blocks, node?, port?, param?}`; `port` is the `in` key, `param` a param name.
The set of `(code, node, port, param)` is what fixtures compare (each at most once; `message` is free text; the API
sorts by node, whole-workflow ones first, then code, port, param). `run` problems block running only; every other
code blocks saving (and running). A document with `too_many_nodes` or `too_large` gets only those (nothing else is
checked, so the work stays bounded).

| Code | Blocks | Where | When |
|---|---|---|---|
| `too_many_nodes` | save | — | more than 64 nodes |
| `too_large` | save | — | the document as compact JSON (`json.Marshal` / `JSON.stringify`, UTF-8) is over 262144 bytes |
| `too_many_outputs` | run | — | more than 4 nodes of kind `output` (a run's work budget: each output pulls its own chain) |
| `too_many_seeds` | run | — | more than one unwired `seed` param over the known kinds (a run reports one seed, as a batch does) |
| `wrong_family` | save | node | `family(kind)` exists and is not the workflow's `family` (a workflow is one family; crossing families is a pipeline) |
| `unknown_kind` | run | node | the kind is not in the palette; nothing else of that node is checked (its params, its `in`, wires from it, studio paths and presets on it) |
| `env_mismatch` | run | node | the palette kind's `envs` lack the workflow's `env` |
| `unknown_param` | save | node, param | a `params` key the kind does not have |
| `param_invalid` | save | node, param | the value breaks the run rules (type, `min`/`max` — string length in Unicode code points, `enum`, rows count and fields; a `Lora` field missing or not `{lora_id}`) or holds a take (a workflow holds none: image params null, no Image items in rows; LoRA ids are allowed) |
| `unknown_input` | save | node, port | an `in` key that is neither an input nor a wireable param of the kind |
| `unknown_source` | save | node, port | the wire names a node that is not in the graph, or an output its (known) kind does not have |
| `type_mismatch` | save | node, port | the types differ, or a handle joins two families |
| `not_bypassable` | save | node | `bypass: true` on a kind that cannot be bypassed |
| `required_input` | run | node, port | a required input unwired (bypassed: a pass-through input unwired) |
| `cycle` | save | node | the node can reach itself through wires (one problem per node on a cycle; a self-wire counts) |
| `no_output` | run | — | no node of kind `output` |
| `studio_unknown_path` | save | node, param | a path in `composer`, `quick`, `inspector` or `labels` whose node is missing, whose param the kind lacks, which is wired, or `<node>.bypass` of a node that cannot be bypassed |
| `studio_duplicate` | save | node, param | a path placed more than once over composer, quick and inspector; a tile (`elements`, `look:<group>`) twice in `quick`: no node, `param` = the tile (a tile is never `studio_unknown_path`) |
| `preset_invalid` | save | node, param | a preset key that would be `studio_unknown_path`, or a value that is `param_invalid` (`bypass`: not a bool) |
| `lora_duplicate` | save | node, param | a valid rows value with a `Lora` field names one LoRA id in two items |
| `lora_unknown` | run | node, param | a node that is not bypassed has a LoRA id the caller cannot use now (unknown, another user's, deleted, pending, rejected, or shared and listed by no live host of the env) |
| `lora_family` | run | node, param | a node that is not bypassed has a LoRA the caller can use whose detected family (`unknown` included) is not `family(kind)` |

Ids: node ids match `^[a-z][a-z0-9_]{0,63}$` (the schema) and are **stable**: made once, never renamed. The editor
makes a new node's id from its kind's last segment with `-` → `_` (`qwen-image/sample` → `sample`), adding `_2`,
`_3`, … for the first free one; a deleted id may be reused.

**Auto-wiring** (client). When node N of kind K is added, for each required input I of K in schema order: the
candidates in a scope are the outputs `<m>.<out>` of known-kind nodes m ≠ N in the scope whose type is I's type (a
handle: of K's family). If a node is selected and it offers exactly one candidate, wire it; else if the whole graph
offers exactly one, wire it; else leave I unwired. Optional inputs and params are never auto-wired. A candidate the new node already reaches (a
wire-drop from an input gives it an outgoing wire) is left out, so this never makes a cycle.

**Wire-drop search** (client). A wire dragged from port P of node m (`side: output`, or `side: input` where P may be
a wireable param) and dropped on empty canvas offers the palette kinds k with the workflow's `env` in `envs` and
`family(k)` equal to the workflow's family or none, that have — for an output P of type T — an input or a wireable
param of type T, or — for an input P of type T — an output of type T; for a handle T only kinds of `family(m.kind)`.
Order: the workflow's family first, then core kinds, each by `title`, then `kind` (code-point order, not locale).
Picking one adds the node and wires P to its first port of type T (inputs before params, schema order; for an
input P, P is wired from the new node's first output of type T), then auto-wires its other inputs with m selected
for an output P and nothing selected for an input P (m consumes the new node; it is not its source).

The `lora_*` codes need the caller's LoRA library (studio: as the caller, at read time; the editor: from `GET
/v1/studio/loras`); preset values are not looked up (a preset's LoRA is checked when a run applies it). A graph fixture
may carry `loras` (id → detected family: the library), absent = empty.

**Fixtures** (`fixtures/graphs/`, all against `palette.json`, which adds a fictional `test` family to the canonical
echo and qwen-image kinds: `test/noise`, `enhance` (bypassable Text → Text), `mix` (optional input `b`, wireable `int`
and `float` params), `count` (an Int source), `model`, `lora` (bypassable Model → Model), `upscale` (torch only)):
`valid.*.json` / `invalid.*.json` `{description, workflow, problems: [{code, node?, port?, param?}]}`;
`autowire.*.json` `{description, workflow, selected, add: {id, kind}, expect_in}`; `wiredrop.*.json`
`{description, workflow, from: {node, port, side}, expect: [kind, …]}`, optionally `pick: {id, kind}` and
`expect_in`: the picked node's `in` after the drop and its auto-wiring.

## Saved pipelines (API 1.7, plan 16.2, 7.2)

A user's own pipeline document runs a chain of workflows with gates and capped loops over a batch's rows. This
version has four node kinds: `inputs`, `step`, `gate`, `output` (judge, write, decide, agent, memory and custom come
later).

- **Document** (`PipelineDoc`): `title`, `nodes` (id → node, ids `^[a-z][a-z0-9_]{0,63}$`, stable), `edges`. Every node
  may carry `title` (the card's) and `position` (as workflows).
  - `inputs`: `inputs` = ordered `[{name, type, label?}]`, type `Text | Image | Int | Float | Seed`; the batch's columns.
    A batch addresses input `prompt` of inputs node `inputs` as `inputs.prompt`; a step binds it as `$prompt`.
  - `step`: `workflow` (a template slug or one of the caller's workflow UUIDs), `seeds` (1–8, default 1), `preset`
    (a name in the workflow's `studio.presets`), `inputs` (fixed `<node>.<param>` values as `RunRequest.inputs`; never a
    take), `bind` (`<node>.<param>` of the workflow ← a source). A run's inputs = the preset, then `inputs`, then the
    binds, then its seed.
  - `gate`: `takes` (a step id: its latest takes are the candidates), `keep` (1–8, default 1): a pick is 1..`keep` of
    the candidates, or reject all.
  - `output`: `from` (`<step>.takes` or `<gate>.picked`: what the row ends with), `collection` (its name).
- **Sources**: `$<input>`; `<step>.takes` (that step's takes from its latest visit, up to `seeds`); `<gate>.picked` (the
  gate's latest pick, up to `keep`). A source not set yet at run time fails the row (`nothing from <node> yet`), except
  an output's `from`, which then ends the row with no takes.
- **Edges**: `[from, to]` (plain) or `[gate, {pass, fail, max, then}]` (the only branch in this version). A plain edge
  out of a gate = pass; rejecting at such a gate fails the row (`rejected at <gate>`). With a branch, a pick goes to
  `pass`; a reject goes to `fail` while the gate's fail count (per row attempt, never reset) is below `max` (1–10),
  then +1; once it is `max`, a reject goes to `then`. Every node but an output has exactly one outgoing edge (no
  fan-out yet); reaching an output ends the row.
- **Seeds**: a step's runs on visit `v` (from 0) of row attempt `a` are `seeds` runs with seed
  `(base + (a−1) × 64 + v × seeds + i) mod 2^53`, `i` = 0..seeds−1 (so a fail edge back to a step, or a Retry, means
  new seeds; 64 = the run bound of a row). `base` = the bound `$<Seed input>` value, else a step `inputs` seed
  ≥ 0, else a seed studio draws per row and step at Submit. The seed param is the workflow's one unwired `seed` param
  (`seed_param` in the snapshot).
- **Endpoints** (`/v1/studio`, mirroring `/workflows`): `GET /pipelines` (mine, newest `updated_at` first, no
  `problems`, each with `workflow_titles` and `last_batch`); `GET /pipelines/{id}` (with `problems`, as the caller,
  against the live palette); `POST /pipelines` (a whole doc with optional `based_on`, or `{based_on, title?}` to
  copy: 201); `PUT /pipelines/{id}` (`PipelineUpdate` = doc + `version`; stale = 409 `pipeline changed since you opened
  it (now version <n>)`); `PATCH` (`{title}`, `version` + 1); `DELETE` (204; 409 while a batch of it is queued, running,
  waiting or paused); `POST /pipelines/validate` (`PipelineValidation`). Save-blocking problems on POST/PUT are 422
  `PipelineError {detail: "pipeline has <n> problems: <first message>", problems}`. Owner-scoped by RLS (another
  user's id is 404); at most 100 per user (409 `You have 100 pipelines; delete some first.`); 503 before studio's
  registry watches have delivered.

## Pipeline rules (normative)

studio_service (Go) and the web editor (TypeScript) implement exactly these rules and test against
`fixtures/pipelines/` (`workflows.json` holds the workflows the caller can run, checked with the graph rules against
`fixtures/graphs/palette.json`); `check.py` holds the reference implementation (`pipeline_problems`). Problems have the
`Problem` shape (`PipelineProblem`: `node` = the pipeline node, `port` = a bind key or `from`, `param` = an input name
or a step `inputs` key), are compared and sorted the same way, and block `save` or only `run` as below. A document with
`too_many_nodes` or `too_large` gets only those.

Terms: **reach(a)** = the nodes reachable from `a` along one edge or more (a branch counts as three edges: pass,
fail, then; a refused edge counts for nothing). **a dominates b**: a ≠ b and every path from the inputs node to b passes
through a (b not reachable from the inputs node with a removed); the inputs node dominates every node; unchecked (true)
without exactly one inputs node. A **capped edge** is a branch's fail edge.

| Code | Blocks | Where | When |
|---|---|---|---|
| `too_many_nodes` | save | — | more than 24 nodes |
| `too_large` | save | — | over 65536 bytes as compact JSON |
| `inputs_node` | save | — | not exactly one node of kind `inputs` |
| `duplicate_input` | save | node, param | an input name twice in one inputs node |
| `unknown_node` | save | node (the edge's from) | an edge names a node not in the document (from, to, pass, fail or then); the edge is refused |
| `branch_not_gate` | save | node | a `{pass, fail, max, then}` edge out of a node that is not a gate; refused |
| `edge_invalid` | save | node | a second outgoing edge of a node, an edge out of an output, or an edge into the inputs node; refused (edges are taken in document order) |
| `uncapped_cycle` | save | node | the node reaches itself along edges that are not capped (every cycle must pass through a capped edge; one problem per node on such a cycle) |
| `too_many_runs` | save | — | Σ over steps of `seeds × visits` > 64, where `visits(s) = 1 + Σ max(e)` over the capped edges e = g→f with s on a cycle through e (s = f or s ∈ reach(f), and g ∈ reach(s)) |
| `gate_takes` | save | node | `takes` is not a step, or that step does not dominate the gate |
| `gate_keep` | save | node | `keep` > that step's `seeds` |
| `bind_source` | save | node, port | a source naming no input (`$x`), `<n>.takes` where n is not a step, `<n>.picked` where n is not a gate; for a step: n is the step itself or does not dominate it; for an output (`port: from`): the output is not in reach(n) |
| `unreachable` | run | node | not reachable from the inputs node (checked with exactly one) |
| `dead_end` | run | node | a node other than an output whose reach holds no output (checked only when there is an output) |
| `no_output` | run | — | no node of kind `output` |
| `unknown_workflow` | run | node | the step's workflow is neither a published template nor one of the caller's (nothing else about it is checked) |
| `workflow_cannot_run` | run | node | the workflow has any graph-rule problem now (message: its first) |
| `unknown_preset` | run | node | `preset` is not in the workflow's `studio.presets` |
| `step_input_invalid` | run | node, param | an `inputs` key that is not a settable param (`<node>.<param>` of a known kind, not wired; or `<node>.bypass` of a bypassable node, a bool), or a value that is `param_invalid` (a take included) |
| `bind_unknown` | run | node, port | a bind key that is not a settable param of the workflow |
| `bind_type` | run | node, port | the source does not fit: `$x` of type T fits a param whose wire type is T (Text: string/text, Int: int, Float: float, Seed: seed, Image: image), or an Image into a rows param with exactly one Image field (a one-item list); a takes/picked source of at most n takes fits an `image` param when n = 1, or such a rows param when n ≤ its `max` |
| `unbound_input` | run | node, port | an `image` param, or a rows param with one Image field and `min` ≥ 1, that is not bound (the workflow holds no take, so it has no default) |
| `step_seeds` | run | node | `seeds` > 1 and the workflow has no unwired `seed` param |
| `unknown_look` | run | node, param `looks` | a step `looks` id that is not a look the caller can use now (Looks: unknown look) |
| `look_fit` | run | node, param `looks` | (no `unknown_look`) a step look does not fit the step's workflow as stored (Looks: fit) |

Problems that depend on another document (the workflows, the palette) only block running, so a pipeline stays
savable when a workflow it uses changes. The bound of 64 runs per row is what Submit's worst case uses.

## Batches of a pipeline (API 1.7)

- **Submit** (`POST /v1/studio/batches` with `pipeline_id` instead of `workflow_id`): the pipeline must be the caller's
  (`unknown pipeline <id>`: 422) with no problem (`pipeline cannot run: <message>`). Columns are its inputs
  (`inputs.prompt`); each row needs every input (`row 3: inputs.edit: a value is required`), except a Seed (drawn);
  values are checked by type (Image = `{take_id}`, checked as image inputs); `lines` on a Text or Image input; `seeds`
  repeats rows over the pipeline's one Seed input (422 when none or several). A live host for every step's env; the
  768 KB limit counts the snapshot. Studio stores the batch with `pipeline_id`, `pipeline_title`, `pipeline_version`,
  `collection` (the first output's by node id), `workflow_id` / `workflow_title` = the first step's.
- **Snapshot** (`cmd.pipeline.submit` `snapshot.pipeline`, `defs pipelineSnapshot`): `{id, title, version, nodes, edges,
  workflows}`: each step's `inputs` already merged over its preset (`preset` kept for display) plus `seed_param`
  (`<node>.<param>` or null), every workflow a step uses as a `workflowSnapshot`. `snapshot.workflow_id` / `workflow`
  are the first step's. Each row carries `seeds` (step id → base seed). The batch runs this, never the live documents.
- **Threads**: the row graph per node: inputs → `inputs`; step → `submit` + `wait` (as today; one `submit` asks
  `studio.runs.create` once per seed, `wait` resumes once all of the visit's runs have ended; any failed run fails the
  row, later nodes are skipped); gate → `gate` (only `interrupt()`) + `route` (applies the pick, chooses the edge,
  counts the loop); output → `output` (ends the row `succeeded` with its `from` takes). Idempotency key
  `<batch>:<row>:<row attempt>:<step id>:<visit from 0>:<seed index>`; `workflow_id` in the request is the step's
  workflow (one of `snapshot.pipeline.workflows`). Studio reads node and loop from the key for `made_with.pipeline`
  (`{id, title, version, node, attempt = visit + 1, row}`; the take keeps `source: pipeline`, `batch_id`).
- **Parking**: a row at a gate is `waiting`: `evt.pipeline.row` with `status: waiting`, `node`, `parked {gate, visit,
  keep, candidates}` (the row-parked event). A parked row holds nothing: no run, no window slot (the window of 16
  rows asked of studio counts only rows not ended and not waiting), and it never holds up row release (row 1 parking
  counts as row 1 ending for "row 1 first"). A picked (or rejected) row resumes through the same window, before
  queued rows. `BatchRow.status` `waiting`, `counts.waiting`; studio reports the batch `waiting` (computed) when pipelines says `running` and every row not ended is waiting.
- **Pick** (`POST …/rows/{n}/pick {gate, take_ids}` or `{gate, reject: true}`): studio checks the row is parked on
  that gate (409), takes ⊆ candidates, 1..keep, distinct (422), records it on the gate cell, sets the row `running`
  with `parked` null, writes `cmd.pipeline.pick {batch_id, row, attempt, gate, visit, take_ids | reject}` in one
  transaction. The same pick again for that visit: 202, nothing sent. `GET …/review` lists every parked row with
  signed candidates, `on_reject` (`retry` / `then` / `fail`) and `rejects_left`. Pipelines resumes only a thread parked
  on that gate visit and attempt; any other pick is dropped.
- **Cells**: `evt.pipeline.row` carries `cells` (one per node reached, first-reached order, `attempts` per visit:
  `status`, `run_ids`, `take_ids`, `candidates`, `seeds`, `error`); studio adds `seconds` (the attempt's runs, started
  to finished, summed), `thumb_url`, and shows a step attempt `queued` while none of its runs has started.
  `BatchRow.node` / `parked` / `cells` exist only for pipeline batches.
- **Pause, Resume, Stop** as before; a parked row is left parked by Pause and Resume and is `cancelled` by Stop.
  Picks are accepted while paused (the row's next runs are created held).

## Execution view (API 1.7, plan 16.8)

- **Step events**: pipelines publishes `evt.pipeline.step` for every graph step of a pipeline row: `{seq, row,
  attempt, step, node, part, visit, status, what, changed, edge, loops, run_ids, take_ids, error, checkpoint_id}`;
  `step` numbers the row attempt's graph steps from 1; a step that parks (a step's `wait`, a `gate`) is published
  `current` (checkpoint_id null) and again `resumed` with the same number when it continues; the latest seq per
  (row, attempt, step) wins. `what` is plain words (`4 runs queued for Qwen Image`, `Parked, then resumed with 4
  takes`, `Rejected all 4; back to still (1 of 2)`), `changed` the state touched (`takes.still = 4`, `loops.review = 1`).
- **`GET …/rows/{n}/execution[?attempt=]`** (`RowExecution`): the snapshot doc, the step list, per node visits and
  last status, the edges taken with counts, the loop counts, `current`; `has_state` per step while the checkpoint can
  be read (the batch's threads exist and `checkpoint_id` is set). 404 for a workflow batch.
- **`GET …/execution/{step}/state`**: studio sends `pipelines.state.get {owner_id, batch_id, row, checkpoint_id}`;
  pipelines answers `{checkpoint_id, state}` only for the owner's batch and that row's thread, else `not_found` (404
  `this row's state is gone; the batch has finished`); no answer in 5 s is 503. The browser never reads a checkpoint.
- **Retry row from here** (`POST …/rows/{n}/retry {from_step}`): studio sets the row `queued`, attempt + 1, copies the
  attempt's steps `1..from_step−1` to the new attempt (`retried_from {attempt, step}`), and writes `cmd.pipeline.retry
  {batch_id, row, attempt, from_step, checkpoint_id}` (the checkpoint of step `from_step − 1`; none for step 1).
  Pipelines forks the thread from that checkpoint with the new attempt: state before it is reused, every key from it
  on carries the new attempt, so its runs are new. Without a body (or `from_step: 1`) the row restarts from its start.
  That deletes the thread, so an older attempt's steps show `has_state` only while every later attempt forked.

## Save as pipeline (API 1.7)

`POST /v1/studio/takes/{id}/pipeline {title?}` walks the lineage up from the take: each take on the chain is a run
output (`made_with` set); its **parent run take** is its one parent with `made_with` (more than one: 422 `take <id>
was made from several runs' takes; a pipeline is a chain`); parents that are uploads become Image inputs. An image
value in `made_with.inputs` that is not among the take's parents (deleted): 422 `take <id> in the lineage was
deleted`. The chain stops at a take without a parent run take; at most 8 steps (422 `the lineage is longer than 8
steps`); every workflow must still exist for the caller (422 `workflow <id> no longer exists`); an upload: 422 `an
upload has no lineage to save`. The document, oldest run first: inputs node `inputs` with `prompt` (Text: the first
step's `prompt.text`; only when its workflow has that param, so every input is bound) and one Image input per upload parent (`image`, `image_2`, …,
in lineage order); steps `step_1`…`step_n`, each `workflow` = `made_with.workflow_id`, `seeds: 1`, `inputs` =
`made_with.inputs` minus image values, minus its one seed param and minus bound keys; binds: `step_1`'s `prompt.text`
← `$prompt`; the param that held the parent run take ← `step_<k−1>.takes`, a param that held an upload ← its
`$image…` input (a rows param holding several takes: 422 `take <id> mixes several sources in <param>`); output `output` `{from: step_n.takes, collection: title}`;
edges in a line. Title: `title`, else `<take title> pipeline` cut to 80. No gate is added; the editor adds them.

## Elements (API 1.8, plan 10-elements-and-looks.md section 14)

An element is a **library item** of the caller's: `kind: element`, `group` character | location | prop, `name`,
a unique `handle` (what `@` mentions), optional `text`, 1–4 images picked from the caller's gallery. Elements and
looks are one record shape and one table (`LibraryItem`); a look is `kind: look` with `handle` null (Looks, below).

- **Endpoints** (`/v1/studio/library`, owner-scoped by RLS, another user's id is 404): `GET` (filters `kind`,
  `group`, `q`, `limit`; `q` is the `@` menu's prefix search on handle and name), `POST` (201; the gallery's "Make
  element" is this call: pick takes, give a name), `GET /{id}`, `PUT /{id}` (`LibraryItemUpdate` = every field +
  `version`; stale = 409 `element changed since you opened it (now version <n>)`; `version` + 1), `DELETE /{id}`
  (204, always allowed). The full error texts are in openapi.yaml (`studioCreateLibraryItem`).
- **Rules**: `name` 1–80 (trimmed), `handle` `^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$` unique per owner over every kind
  (409 `handle @<h> is already used`), `text` ≤ 2000 code points (trimmed, `""` = none), images 1–4 distinct takes
  that are the caller's, completed, not deleted (else 404 `images[<i>]: take <id> not found`), `kind: image` with a
  size (else 422 `images[<i>]: take <id> is not an image` / `… has no size`); at most 200 items per user (409
  `You have 200 library items; delete some first.`). An image is stored as its take id only: never a key or URL.
- **A deleted take drops out**: deleting a take is never refused because an element uses it; from then on the
  element is read and resolved without that image (it may end with 0 images: it still resolves, name and text
  only; the next PUT needs 1–4 again). Takes already made are unaffected: they stored their resolved graph.
- **Delete** is never refused: past takes keep their resolved graph and `made_with.elements`; reuse says `deleted`.
- **Runs**: Create (`POST …/runs`) resolves mentions against the caller's elements at create time; the resolved
  graph is what is checked for a host, hashed and cached, stored as the snapshot, sent in `cmd.run.execute` and
  recorded in the take (`made_with.inputs`). `Run.inputs` stays as requested. No message schema changes: the runner
  only ever sees an ordinary prompt and reference rows.
- **Batches**: a workflow batch resolves every row at Submit (all failing rows listed in one 422, openapi
  `studioCreateBatch`); a pipeline batch checks only unknown handles at Submit (in each step's graph as each input
  row's run resolves it, the step workflow's own params included), the rest when each run is made (a failure fails the row, `invalid`). Studio keeps a copy of every
  mentioned element with the batch (id, handle, name, version, text, image take ids) and resolves the batch's runs
  (`studio.runs.create`, Retry included) against that copy; rows and messages still carry the prompt as typed.
- **Takes**: `made_with.elements` = `[{id, handle, name, version}]` in first-mention order; `made_with.typed` = the
  pre-expansion value of each param the expansion changed; `made_with.prompt` is the prompt as typed (the title and
  the gallery's `q` see the mentions). Lineage (`parents`) and Save as pipeline read the **typed view**
  (`inputs` with `typed` laid over it): element images are not parents and never become pipeline inputs; they come
  back through the mentions when the pipeline runs.
- **Reuse**: `inputs` = the typed view; `elements_status` per element: `current` (same version now), `changed`
  (another version), `deleted`, with `current_handle` (null when deleted). A renamed element's old `@handle` in the
  returned prompt is then unknown (422 at run) or names another element: the UI says so from `current_handle`.

## Mentions (normative)

studio_service (Go) and the web editor (TypeScript: highlighting, the `@` menu, the preview of what a mention adds)
implement exactly these rules and test against `fixtures/mentions/`; `check.py` holds the reference implementation
(`scan`, `reference_rows`, `resolve_mentions`).

**Grammar** (`scan`). Word characters are ASCII `A-Z a-z 0-9 _` only (so bytes, runes and UTF-16 units agree). Left
to right: `@@` is an escaped literal `@` (both characters consumed; resolves to one `@`). Otherwise an `@` that is at
the start or not after a word character starts a token: the longest run of `A-Z a-z 0-9 _ -` after it, with trailing
`-` and `_` removed. A token whose first character is an ASCII letter is a **mention** of handle = the token
lower-cased (case-insensitive: `@Fox` = `@fox`); any other `@` is literal text (`a@b.c`, `@ 3pm`, `@3pm`, a lone `@`).
A mention is never a partial handle: `@fox-cub` mentions `fox-cub`, not `fox`. The scan answers parts
`{text}` (raw, `@@` kept) and `{mention, raw}`; adjacent text is one part; the raw parts join to the input.

**Where.** Every param of type `text` (not `string`) of a known, not bypassed node, not wired: today exactly
`prompt.text` (Create's composer, the graph view's prompt param, a batch row's or line's prompt, a pipeline input
bound to it, a step's fixed `inputs`). Params are taken in node-id order (code points), then schema order.

**When** (plan 14.2): values are merged first (schema defaults, the workflow's params, the preset, the user's
inputs) and checked by the run rules; the expansion then runs on those values. The user's own edits are never
overridden: their reference rows keep positions 1..k and their prompt text stays as typed except the mentions.

**Reference rows** (`reference_rows`): the first (node id, then schema order) unwired `rows` param of a known, not
bypassed node whose `mention` contains `{n}` and whose `item` has exactly one `Image` field (Qwen and echo:
`encode.refs` / `echo.refs`, `<image{n}>`, max 10). None = the family takes text only.

**Expansion** (`resolve_mentions`), with `used` = the distinct mentioned handles in first-mention order over all
params:
1. **Unknown handles**: one error per distinct unknown handle per param, first-appearance order:
   `<node>.<param>: unknown element @<handle>`; when any, nothing else is checked.
2. **Images** (only with reference rows): row numbers = the user's rows first (a take already in a row keeps the
   first row that has it). For each element in `used`, its images (stored order) not yet in a row are appended as
   `{<image field>: {take_id}}` (no other fields: they take their defaults), each element once however often it is
   mentioned. If an element's new images exceed `max` (absent: 100), it and every later element that brings new
   images do not fit: `<node>.<param>: at most <max> images; <rows before it> used, @<h> +<new>, … do not fit`.
   An element's **tokens** = `mention` with `{n}` = the row number of each of its images, in its image order.
   **Canvas**: element rows never change the output size. When this step appended rows, every known, not bypassed
   `canvas` node whose `mode` is `auto` (with references, auto follows the *last* row) is pinned to the size auto
   gives without them: `mode` → `fixed`; with no user rows its own `width` × `height` stay; with user rows, `width`
   and `height` → the auto size of the last user row's take (r = its width / height, `w = √(1024²·r)`: `round(w/32)·32`
   × `round(w/r/32)·32`, rounding half to even, as the runner), clamped to the params' `min` / `max`. A take whose
   size studio does not know (it fails the image checks anyway) pins `mode` only. The pinned params are changed
   paths like the others (`inputs` / `typed`).
3. **Prompts**: each mention becomes `<name>` followed by its tokens, space-separated (`Fox <image3> <image4>`;
   the name alone without reference rows or without live images); each text part becomes its raw text with every
   `@@` (left to right) as `@`. Then, if any element mentioned **in that param** has non-empty text, append `

`
   and one line per such element, first-mention order: `<name>: <text>`, joined by `
`.
4. **Length**: the result over the param's `max` (code points) is `<node>.<param>: at most <max> characters once
   elements are added (<length>)`.
5. **Result**: `inputs` = each changed path → its resolved value (a prompt with only `@@` changes too); `typed` = the
   same paths → their value before; `elements` = `used` as `{id, handle, name, version}`.

Errors reach the API as 422 `detail` = the errors joined by `; ` (a batch row prefixes each with `row <n>: `). The
take of a run stores `inputs` merged over every other param, `typed` and `elements` (Elements, above).

**Fixtures**: `scan.json` `{cases: [{text, parts}]}`; `resolve.*.json` `{description, workflow (a WorkflowDoc whose
params hold the merged values; missing params take the schema default), library: [{id, handle, name, version, text,
images: [take id]}] (as studio reads it: deleted takes already left out), sizes (optional: take id → [width, height]
of the user's row takes), expect: {inputs, typed, elements} or
{errors}}` (missing keys of a success = empty).

## Looks (API 1.10, plan 10-elements-and-looks.md section 14)

A look is a **library item** (`LibraryItem`, the elements' table and endpoints) of `kind: look`, `group` film | camera |
colour | lighting, `handle` always null: a `name`, a `text`, 0-4 reference images, 0-4 LoRAs with a saved strength, a
thumbnail. Looks are **chosen, not typed**: the run request names them (`looks`), never the prompt. Two sources:

- **Starter looks** (`source: starter`), shipped by a pack and read-only for everyone. A pack holds
  `runner_service/packs/<pack>/looks/*.yaml`, each file a list:
  `{id, group, name, text?, version?, lora?: {file, strength}, image?}`: `id` a slug `^[a-z0-9][a-z0-9-]{0,63}$`
  unique in the family; `text` ≤ 2000 code points (default `""`); `version` ≥ 1 (default 1; bump it when the look
  changes); `lora.file` a file name (no path) looked up in the folders the shared LoRA index scans (`<models>/loras/`, a
  `loras/` folder inside one of the family's model folders), `strength` −4..4 (default 1); `image` a png, jpeg or webp
  file next to the YAML, at most 2 MB. A look needs non-empty `text` or a `lora`. At most 200 looks per pack. A broken
  file (schema, duplicate id, missing image) stops the runner like a broken pack; a `lora.file` this host does not have
  is not an error (`lora_id: null`, logged). At start the runner publishes registry `<host>.<env>.looks`
  (`$defs/looks`): id `<family>/<id>` (stable across hosts), `family`, `group`, `name`, `text`, `version`, `loras`
  (`[{file, name, strength, lora_id}]`, `lora_id` = the file's SHA-256 from its LoRA index, else null), `thumb`
  (the image PUT once to `packs/<family>/looks/<id>-<sha256[:16]>.<ext>`, never overwritten; null without one). The
  image is only a thumbnail, never a reference image (it is not a take). The fake runner publishes
  `fixtures/registry/looks.valid.json` verbatim (echo pack).
- **Own looks** (`source: mine`): the caller's, owner-scoped by RLS like elements (another user's id is 404), id a
  UUID, `family` null, counted in the same 200 items, `version` / 409 like elements (`look changed since you opened it
  (now version <n>)`), delete never refused.

**Starter looks in the API.** `GET /v1/studio/library` lists the caller's own items and the union of the live hosts'
`looks` keys (fresh `hosts` key). Studio keeps an entry only when its `id` is `<family>/<slug>`, its `family` is one
of that host's `models` families, its `name` has no control character and its `text` none but `\n` `\r` `\t` (others
are dropped and logged); a `thumb.key` that is not `packs/<family>/looks/<slug>-<16 hex>.<png|jpg|webp>` of the entry's
family is dropped (`thumb_url` null). One id published by several live hosts: a non-`fake` env's entry before env
`fake`'s, then the highest `version`; on a tie the first host by (host, then env name); each LoRA's `lora_id` = the first non-null over
the live hosts publishing that id, version and `file`. `created_at` = `updated_at` = the oldest `published_at` of
those hosts; `thumb_url` = a signed GET of `thumb.key`; `images` `[]`; `family` the pack's. A starter id is
`<family>/<id>`: `GET /v1/studio/library/{family}/{look_id}` (the same URL as `/library/` + the id unescaped); PUT and
DELETE are 409 `starter looks are read-only; save your own copy`; an id no live host publishes is 404. A shared LoRA
named by a starter look is an ordinary shared LoRA: the dispatcher's LoRA rule places the run.

**Fields** (every item from 1.10): `source`, `family`, `loras` `[{lora: {lora_id} | null, name, strength}]` (an
element: `[]`; null only for a starter LoRA no live host has), `needs` (computed, in this order: `text` when `text` is
not empty, `images` with at least one image, `lora` with at least one LoRA), `from_take_id`. `thumb_url`: the first
image's thumb; else the `from_take_id` take's thumb (null once that take is deleted); else the starter `thumb`; else
null. With `?workflow=<id>` every item also gets `fits` and `reason` (fit, below). List filters: `kind`, `group`, `q`,
`source`, `family` (items whose `family` is null or equal), `limit` (≤ 1000).

**Own look rules** (POST / PUT, after trimming; elements unchanged): `group` must be a look group (422 `group <g> is not
a look group`; an element: `… is not an element group`); `handle` absent or null (422 `a look has no handle`); `text`
≤ 2000; `images` 0-4 with the element image checks; `loras` 0-4 (422 `loras: a look has at most 4 LoRAs`), each a LoRA
the caller can use now (own `ready`, or shared and listed by a live host; else 404 `loras[<i>].lora: LoRA <id> not
found`), distinct (422 `loras[<i>]: LoRA <name> is listed twice`), `strength` −4..4; at least one of text, an image, a
LoRA (422 `a look needs text, an image or a LoRA`). An element with `loras` is 422 `loras: an element has no LoRAs`.
The stored LoRA keeps its id, its name at save time (shown while it is gone) and the strength.

**Save as look** (the take's action, plan 06-platform.md 9.1) = `POST /v1/studio/library` with `kind: look` and
`from_take {take_id, prompt = true, loras = true}`. The take must be the caller's, not deleted (else 404 `from_take:
take <id> not found`), made by a run (an upload: 422 `from_take: take <id> was not made by a run`); `from_take` on an
element is 422 `from_take makes looks only`. It fills only what the body leaves out: `text` (when `prompt`) =
`made_with.prompt` as typed with each `@@` turned into `@` (as the prompt reached the runner; other mentions stay literal
text: look text is never scanned), trimmed, cut to 2000
code points (null prompt = `""`); `loras` (when `loras`) = `made_with.loras` in order whose LoRA the caller can use now,
the first per id, `{lora: {lora_id: id}, strength}` (the mode is not kept), at most 4. Images are never copied (the
client sends `images`, e.g. the take itself). Then the rules above apply; `from_take_id` = the take.

**Composer tiles.** `studio.quick` may hold `elements` and `look:<group>` among its paths, in order; each is a tile that
opens the shared visual picker on that group (elements insert an `@` mention; looks join the run's `looks`). A
workflow without `studio` (every template today: registry workflows carry none) shows the `elements` tile when it has a
prompt param, then one tile per look group, in the order film, camera, colour, lighting, that has at least one item
with `fits: true` for it. With `studio`, exactly the tiles its `quick` lists.

**Fit** (normative; `look_fit`; studio for the list's `fits` / `reason`, at run and in pipeline problems; the web for
the picker's preview). Against a workflow's graph and `loras` = the caller's usable LoRAs now (own `ready`; shared
listed by a live host of the workflow's env) with their detected families. The first that holds is the reason:
1. `text` in needs, or an element, and the graph has no prompt param (Mentions, Where): `this workflow has no prompt`.
   An element fits otherwise (its images are dropped where there are no reference rows).
2. `images` in needs and no reference rows (Mentions): `this workflow takes no reference images`.
3. `lora` in needs and no look stack (below): `this workflow has no LoRA stack`.
4. per LoRA in order: `lora_id` null: `LoRA <name> is on no live host`; not in `loras`: `LoRA <name> is not
   available`; detected `unknown`: `LoRA <name> fits no known model`; another family than the stack's: `LoRA <name> is
   for <family>`.
A starter look's `family` decides nothing here: a text-only look fits every family with a prompt.

**The look stack**: among the known, not bypassed nodes of kind `<family>/lora-stack` with an unwired rows param that
has a `Lora` field, the first by node id from which no other such node is reachable along wires (the last of a
chain). None: looks with LoRAs do not fit; studio never adds a node (the graph stays typed).

**Resolving** (normative, `resolve_mentions` with `looks`, `chosen`, `loras`; one resolver with the mentions, plan
14.2): values are merged first (defaults, the workflow, the preset, the user's inputs) and checked; the expansion then
only appends, so the user's own edits keep their places. `chosen` = RunRequest.looks; a repeated id counts once, at its
first place `i` (from 1).
1. **Unknown**: the unknown-handle errors first, then per distinct id not among the caller's usable looks (own, or a
   starter a live host publishes; an element id is unknown too): `looks[<i>]: unknown look <id>`. Any: stop.
2. **Fit**: per look that does not fit: `looks[<i>]: look <name> does not fit this workflow (<reason>)`. Any: stop.
3. **Images** (Mentions step 2, extended): after the user's rows and the element images, each look's images not yet in
   a row are appended in stored order, in look order, under the same `max`; a look that does not fit stops it and every
   later item with new images: `<node>.<param>: at most <max> images; <rows before it> used, @fox +2, look <name> +1 do
   not fit`. A look's **tokens** = `mention` with `{n}` = the row of each of its images. The canvas pin rule applies
   when element or look rows were appended.
4. **LoRAs**: each look's LoRAs, in look order then stored order, go to the look stack's rows as `{<lora field>:
   {lora_id}, strength}` (the saved strength clamped to the item's `strength` min/max; other fields take their
   defaults). Skipped: a LoRA already in a row of any known, not bypassed node's Lora rows (the user's row wins, at its
   strength and mode) or added by an earlier look. Over the param's `max`: `<node>.<param>: at most <max> LoRAs;
   <rows before it> used, look <name> +<new> do not fit` (that look and every later look with new LoRAs).
5. **Prompt**: mentions expand per prompt param as before. A look's **line** = its text, then its tokens,
   space-separated (empty: no line); lines in look order, an identical line once. The lines go to the **first** prompt
   param only (Where order): its value = the non-empty of (expanded prompt with its element lines, the look lines
   joined by `\n`) joined by `\n\n`. Over `max`: `<node>.<param>: at most <max> characters once elements and looks are
   added (<n>)` when look lines went in, else the element text.
6. **Result**: `inputs` / `typed` as for mentions (a look stack's rows, the prompt, refs, a pinned canvas), `looks` =
   the distinct chosen looks in order as `{id, source, name, version}`.
Then the LoRA values of the resolved graph are resolved as today (LoRAs). Errors reach the API as 422 `detail` joined by
`; ` (a batch row: `row <n>: …`).

**Runs**: `RunRequest.looks` (≤ 8 ids); the resolved graph is checked for a host, hashed, cached, stored as the
snapshot, sent and recorded; `Run.looks` = as requested. `cmd.run.execute` is unchanged (the runner sees an ordinary
prompt, reference rows and LoRA rows).
**Batches** (a workflow): a row's looks = its `looks` cell (table column or grid axis named `looks`, each value a
list of ids; null = the batch's) else `BatchCreate.looks`; `BatchRow.looks` shows them; `seeds` repeats keep them; every
row resolves at Submit like mentions. A pipeline batch: `looks` or a `looks` column is 422 `looks: a pipeline batch
takes its looks from its steps`; a step's runs use `PipelineStepNode.looks` (in the snapshot as `looks`, which
pipelines ignores); `unknown_look` / `look_fit` are pipeline problems (Submit: `pipeline cannot run: …`), the rest is
checked as each run is made (the row fails `invalid`). Studio keeps a **copy** of every look a row or step uses (id,
source, name, version, text, image take ids, LoRAs `{lora_id, name, strength}`) with the batch, like the elements, and
resolves its runs (`studio.runs.create`, Retry included) against that copy; no message changes.
**Takes**: `made_with.looks` = the result's `looks`; `made_with.typed` holds every path the looks changed; lineage and
Save as pipeline read the typed view (look images are never parents); Save as pipeline gives each step `looks` = its
take's `made_with.looks` ids. `made_with.loras` lists the look LoRAs too (they ran).
**Reuse**: `inputs` = the typed view; `looks` = the `made_with.looks` ids; `looks_status` per entry: `current` (usable
now, same version), `changed` (another version), `deleted` (own, deleted since), `missing` (starter, no live host
publishes it now).

**Fixtures**: `fixtures/mentions/fit.json` `{workflows: {name: WorkflowDoc}, cases: [{description, workflow, item,
loras?, expect: reason | null}]}`; `resolve.*.json` may add `looks` (the usable looks: `{id, source, name, version,
text, images: [take id], loras: [{lora_id | null, name, strength}]}`), `chosen` (RunRequest.looks) and `loras` (LoRA id
→ detected family); `expect.looks` (missing = `[]`).

## LoRAs (API 1.9, plan 03-runtime.md section 6.4)

A LoRA is a safetensors file of low-rank weight deltas for one family's `lora.component` (Qwen: the transformer).
**Shared** LoRAs are files on the GPU hosts, usable by everyone; **own** LoRAs are a user's uploads, usable only by them.
Sharing a user's LoRA with others is not in this version.

- **Shared** (registry `<host>.<env>.loras`, `$defs/loras`): at start the runner indexes every `*.safetensors` under
  `<models>/loras/` and under any `loras/` folder inside a model folder (not recursive below those), from the header
  only, plus one SHA-256 of the whole file (kept by path, size and mtime, so a restart rehashes nothing). Each entry:
  `id` = the SHA-256 (the same file on two hosts is one LoRA), `name` (file stem, cut to 80), `family` (family
  detection, LoRA normaliser, over its packs' families: one env, so by name), `rank`, `bytes`, `key_style`, `licence`,
  `modified_at`. A file that is not a LoRA (LoRA upload checks below) is left out and logged. Rescans come with M5a.
- **Own** (studio table `loras`, owner-scoped by RLS, another user's id is 404 everywhere): uploaded by the takes'
  two-step signed PUT. `POST /v1/studio/loras {filename, bytes}` → 201 `LoraUpload` (the LoRA `pending`, a 15-minute
  `PUT` link for `u/<owner>/loras/<lora_id>.safetensors`, `Content-Type: application/octet-stream`); the client PUTs
  the file to files_service; `POST /v1/studio/loras/{lora_id}/complete` → studio checks it (LoRA upload checks), hashes
  it, detects its family (family detection, LoRA normaliser) over every family the registry publishes a lora block
  for (detected once, at complete), records `rank`, `key_style`, `licence`, `sha256` and
  answers `ready`; a failed check is 422 with the reason and the LoRA stays listed `rejected` with that `reason`.
  `.safetensors` only, at most 2 GiB (2147483648 bytes); per user at most 50 LoRAs (any state) and 20 GiB (21474836480
  bytes, pending + ready, and deleted ones until the sweep removes their object). A LoRA still `pending` an hour after
  its creation is deleted. Rename = `PATCH {name}`; `DELETE` = 204, never refused (soft at once, the object a day
  later by the sweep). Shared ids: PATCH and DELETE are 409 `shared LoRAs are managed on their host`; `complete` is 404.
- **Library** (`GET /v1/studio/loras`, `GET /v1/studio/loras/{lora_id}`, `Lora`): shared LoRAs = the union over live
  hosts (fresh `hosts` key), `hosts` = those listing the id, `family` = the first not `unknown` over them (a non-fake env's before env `fake`'s), `created_at`
  = the oldest `modified_at`; plus the caller's own in every state (`hosts: []`). `thumb_url` = the caller's own oldest
  take whose `made_with.loras` holds the id (never another user's take). The picker shows the LoRAs whose `family` is
  the connected Model's family and `state` is `ready`; the others greyed with the reason (`for <family>`, `model
  unknown`, `checking…` (pending), `rejected: <reason>`).
- **Values.** A rows `item` field may be the ref kind `Lora` (like `Image`: required in every item). Over HTTP, in
  workflows, presets, batch rows, `made_with` and every pipelines message a Lora value is `{"lora_id": "<id>"}`
  (`loraRef`, closed): never a path, key or URL. Unlike takes, a user's workflow and presets may hold LoRA ids (a
  registry workflow holds none). Example: `"lora.rows": [{"lora": {"lora_id": "3f0a…"}, "strength": 0.8, "mode":
  "adapter"}]`.
- **The stack node** `<family>/lora-stack` (`qwen-image/lora-stack`, title `LoRA Stack`): input `model: Model`, output
  `model: Model` (so it is bypassable: bypassed = the model as it is), one param `rows` (label `LoRAs`, default `[]`,
  `min` 0, `max` 8 = the family's row cap) with item `{lora: Lora, strength: float -4..4 default 1 step 0.05, mode:
  enum adapter|bake default adapter}`. A family that cannot run adapters yet publishes `enum: [bake]`. Graph rules:
  Model → Model within one family (the handle rules), `lora_duplicate`, `lora_unknown`, `lora_family` (Graph rules).
  Stacks may chain: the Model's LoRA set is the upstream set followed by this node's rows. An empty stack is the
  identity: same handle, same cache identity, same pixels (the template carries one, so M2 goldens hold).
- **Studio resolves** each Lora value when it creates the run (Create, and `studio.runs.create` for a batch row), as
  the caller, after values and mentions are merged and checked: own = the caller's, `ready`, not deleted; shared = an
  id a live host of the workflow's env lists. Errors are 422 naming the param and item from 1 (openapi
  `studioCreateRun`): `lora.rows[2].lora: LoRA <id> not found` (one message for unknown, another user's, deleted,
  pending, rejected, on no live host: nothing leaks), `lora.rows[2].lora: LoRA <name> does not fit qwen-image (it is
  for <family>)` / `(its model is unknown)`, `lora.rows[2].lora: LoRA <name> is already in row 1`. A batch row: the
  same text as `row <n>: …` or the `invalid` reply.
- **In `cmd.run.execute`** each Lora value is resolved (`defs lora`): shared `{lora_id, source: shared, name, sha256,
  bytes}`; own adds `key` (`u/<owner_id>/loras/<lora_id>.safetensors`). No URL, ever; `check.py`'s foreign-key rule
  covers the key. The message version stays 1 (an additive value form).
- **The runner** finds a shared LoRA by `sha256` in its index (absent: the run fails `LoRA <name> is not on host
  <host>`); fetches an own one, only under the envelope owner's `u/<owner_id>/loras/`, straight from the object store
  into its LoRA cache (`<cache>/loras/<sha256>.safetensors`, LRU, phase `fetching LoRA <name>`), and checks its size
  and SHA-256 (else `LoRA <name> is gone`). Then it normalises it (LoRA normaliser) and applies it:
  - **adapter**: `y = W x + s · B(A x)` around plain and int8-quantized Linears; `s` is a tensor held by the wrapper, set
    per run, so a strength change or an adapter swap never reloads the base and compiled graphs see it.
  - **bake**: `W' = W + s · (B @ A)` in fp32, cast to the component dtype, in the loader's hook chain rename → cast →
    **bake** → quantize, so it works on an int8 base. A baked component is its own residency entry, keyed by (files,
    precision, quantisation, the baked `(sha256, strength)` list in order; one file in two chained stacks is one delta
    with the summed strength), reported in the hosts value's `resident[].loras`; a baked strength change is a
    different entry (a load), a rename is not. The deltas (fp32, while loading) and the adapters count in the memory
    budget before they are allocated (at most the file's size / 2 elements each).
  - `s = strength · alpha / rank` per module (alpha absent: `s = strength`).
- **Cache keys** (plan 6.1). The Model handle carries its ordered LoRA set `[(sha256, strength, mode)]`. A stage's key
  hashes a Model input per component it `uses`, and the LoRA set only with the family's `lora.component`: Sample
  (`uses: [transformer]`) includes it; Encode (`text_encoder`, `vae`) and Decode (`vae`) never do. So a strength
  change, a mode change or another LoRA re-runs Sample, and Decode only because its latent changed; Encode is a hit;
  adapters never reload the base. An empty set adds nothing to any key.
- **Takes**: `made_with.loras` = every row of every lora-stack node that ran (node id order, then row order) as
  `{node, id, source, name, strength, mode, sha256}` with the name as it was; `made_with.inputs` keeps `{lora_id}`.
  **Reuse** adds `loras_status` per entry: `current` (the caller can use it now), `missing` (shared, no live host
  lists it now), `deleted` (own, deleted since); running a missing or deleted one is 422.
- **Nothing leaks.** Another user's own LoRA never appears in a list, a get, a problem message or an error (404 / `not
  found`); in `GET /v1/studio/hosts` a baked entry shows it as its size only (`lora_id`, `name`, `strength` null).
  Cache keys include the owner as before.

## LoRA normaliser (normative)

runner_service (`core/lora`, at load) and studio_service (at upload: style, rank and family detection) implement
exactly this and test against `fixtures/loras/`; `check.py` holds the reference (`lora_style`, `lora_parse`,
`normalise`, `expand_modules`). Inputs: the file's header (tensor key → `{dtype, shape}`; `__metadata__` is not a
tensor) and one family's lora block (registry `models[].lora`, `$defs/loraBlock`).

**Family block** (model.yaml `lora:` → registry): `component` (the patched component), `modules` (its Linear module
names, canonical dotted form as the model's `named_modules`, sorted; one `{a..b}` range per entry expands to a..b
inclusive; generated by the runner from the meta-device model; unique with `.` read as `_`), `renames` (ordered
`{from, to}` prefix rewrites of a key's module part; the first whose `from` starts it applies; most families `[]`),
`ignore` (fnmatch globs, case-sensitive, over raw keys a family declares harmless; Qwen: `[]`). The node's row cap,
strength range and modes are in the stack node schema; model.yaml's `lora:` block holds both (`component`, `renames`,
`ignore`, `modes`, `max_rows`, `strength {min, max, default}`) and the runner generates the node and the registry
block from it.

**Key styles** (detected from the keys, never configured). The first rule that matches any key decides:
1. `kohya`: a key starts `lora_unet_` or `lora_transformer_`. Roles by suffix `.lora_down.weight` (A), `.lora_up.weight`
   (B), `.alpha`. Module part = between the prefix and the suffix, `.` written as `_`; it names the module `m` of
   `modules` whose `m` with every `.` read as `_` equals it (`lora_unet_transformer_blocks_0_attn_to_out_0.lora_down.weight`
   → `transformer_blocks.0.attn.to_out.0`). Other prefixes (`lora_te1_…`) do not parse.
2. `comfyui`: a key starts `diffusion_model.`. Suffixes `.lora_A.weight` / `.lora_down.weight` (A), `.lora_B.weight` /
   `.lora_up.weight` (B), `.alpha` (`diffusion_model.transformer_blocks.7.attn.to_q.lora_A.weight`).
3. `peft` (PEFT / diffusers): a key ends `.lora_A.weight`, `.lora_B.weight`, `.lora.down.weight`, `.lora.up.weight` (or
   `.alpha` with one of them present). A leading `base_model.model.`, then a leading `transformer.`, is cut (each at most
   once, in that order) (`transformer.transformer_blocks.0.attn.to_q.lora_A.weight`).
None matches: the style is unrecognised and no key parses. Keys of another style in the same file do not parse.

**Canonical form**: `module → (A [rank, in], B [out, rank], alpha | none)`. Per key, in code-point order: a key matching
an `ignore` glob is **ignored**; else one that does not parse in the style is **unapplied**; else its module part is
renamed and grouped with the other keys of that part. A group **applies** when it has exactly one A and one B and at
most one alpha (no other role), A and B are 2-D with `A.shape[0] == B.shape[1] ≥ 1` (the rank), alpha's shape is `[]`
or `[1]`, every dtype is F32, F16 or BF16, and the part names a module of `modules`; the runner also needs `A.shape[1]`
= the Linear's `in_features` and `B.shape[0]` = its `out_features`. Its keys are then **applied**; otherwise every key
of the group is unapplied. `rank` = the largest rank of the groups whose shapes are right (applied or not); `total` =
every tensor key. **Applied** means: the module's delta is added (adapter) or merged (bake) with `s = strength · alpha /
rank`, `s = strength` without alpha.

**No key is silently dropped**: a file fits a family when `applied ≥ 1` and nothing is unapplied (`total = applied +
ignored`). Otherwise loading fails the run with (unapplied keys in code-point order, the first three named):
- `LoRA <name>: 0 of <total> keys apply to <family> (<peft|kohya|comfyui|unrecognised> keys); is it for another model?`
- `LoRA <name>: <n> of <total> keys do not apply to <family>: <k1>, <k2>, <k3>` + ` and <n − 3> more` when n > 3.
The runner logs every unapplied key.

**Family detection** (studio at complete, the runner's shared index; reference `detect_family`): the families the file
fits; of those, one a non-fake env publishes (in any registry key, live or not) comes before one only env `fake`
publishes, then by name; none: `unknown`. So a real LoRA whose keys are all echo's modules is still `qwen-image` when a
torch host publishes it. Studio cannot check `in` / `out` against the model, so a file whose shapes do not fit still
fails at the runner with the second text.

**LoRA upload checks** (studio at complete; the runner's index skips files that fail them), in this order, each a 422
`detail` and the rejected `reason`: `size differs from the declared bytes` (HEAD); first bytes `PK\x03\x04` or
`\x80` followed by 2-5 (zip / pickle): `pickle checkpoints are not accepted; convert it to safetensors`; header length
(little-endian u64 at 0) under 2 or past the file: `not a safetensors file`; over 8388608: `safetensors header is
larger than 8 MB`; not a UTF-8 JSON object whose entries are `{dtype, shape, data_offsets}` (and an optional
`__metadata__` of strings): `not a safetensors file`; more than 20000 tensors: `more than 20000 tensors`; offsets
outside the data or overlapping: `tensor <key> lies outside the file`; a dtype other than F32, F16, BF16: `tensor <key>
has dtype <d>; LoRAs must be F32, F16 or BF16`; no style: `no LoRA keys found (is it a full model?)`; a key that does
not parse in the style and matches no published family's `ignore`: `<key> is not a LoRA weight`; a group with A but
not B (or B but not A): `<module part>: lora_B is missing` (`lora_A`); ranks that differ: `<module part>: lora_A and
lora_B ranks differ`; a rank over 4096: `rank <r> is over 4096`. The first failing key in code-point order is named.
`licence` = `__metadata__` `modelspec.license`, else `license`, trimmed, cut to 200; null when neither.

## Rules

- **Claim check:** messages carry object keys under `u/<owner_id>/…`, never bytes or signed URLs. Producers only
  write keys under the envelope's `owner_id` prefix; run outputs live under `u/<owner_id>/runs/<run_id>/<attempt>/`
  (`<index>.<ext>`). `check.py` fails any message fixture with a `u/…` string outside its owner's prefix.
- **Envelope** is closed (`additionalProperties: false`); `data` is open: consumers ignore fields they don't know.
- **Versioning:** a message's `version` is the major version of its `data` schema (`const` in the schema).
  Additive, optional fields keep the version. Removing or renaming a field, changing a type or meaning, or making a
  field required is breaking: add `<type>` version N+1 as a new schema, publish both until every consumer reads
  N+1, then drop N. Envelope changes are breaking for every type. KV values carry `schema_version` the same way.
- **HTTP:** `info.version` follows semver; additive response fields and new optional request fields are minor;
  anything that breaks a generated client is a new major. Errors are always `{"detail": string}`. Long work is 202;
  a state conflict is 409. operationIds start with the service: `studio…`, `files…`, `auth…`.
- **Idempotency:** commands use `Nats-Msg-Id = <job_id>:<attempt>`; every `evt.run.*` carries `attempt` and a
  per-attempt `seq` (from 1, over all three kinds) and publishes with `Nats-Msg-Id = <job_id>:<attempt>:<seq>`; the
  projector dedupes on `(job_id, attempt, seq)`. A runner publishes the terminal status and waits for its ack before it acks the command.
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
