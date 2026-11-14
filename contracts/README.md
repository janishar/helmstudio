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
| `messages/cmd.pipeline.{submit,resume,pause,stop,retry}.schema.json` | studio → pipelines_service: start, continue, pause, stop a batch; retry one row |
| `messages/evt.pipeline.{batch,row}.schema.json` | pipelines_service → studio's projector: a batch's status, a row's whole state |
| `messages/evt.studio.run_finished.schema.json` | studio → pipelines_service: a batch run ended (any terminal status) |
| `messages/req.studio.runs.create.schema.json` | NATS request/reply pipelines → studio (not enveloped): create a batch-lane run, idempotent on its key |
| `messages/evt.auth.revoked.schema.json` | auth_service → studio: drop cached token validations |
| `messages/defs.schema.json` | shared definitions (uuid, object keys, seq, host/env names, the workflow graph, param values: `inputValue` with `imageRef`, `resolvedValue` with `image`) |
| `registry.schema.json` | KV values: `$defs/nodes`, `$defs/workflows`, `$defs/models` (bucket `registry`), `$defs/host` (bucket `hosts`) |
| `fixtures/{messages,registry}/` | `<name>.valid[.N].json` must pass, `<name>.invalid[.N].json` must fail (request/reply: `<type>.{request,reply}.…`) |
| `fixtures/http/` | HTTP bodies, `<Schema>.{valid,invalid}[.N].json` against `openapi.yaml` `components/schemas/<Schema>` (the names in `check.py` `HTTP_SCHEMAS`) |
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
| `evt.run.<session_id>.status` | stream `EVT` (7 days) | `evt.run.status`, `Nats-Msg-Id <run_id>:<attempt>:<seq>` | runner → studio projector |
| `evt.run.<session_id>.progress` | stream `EVT` | `evt.run.progress`, same | runner → studio projector |
| `evt.run.<session_id>.output` | stream `EVT` | `evt.run.output`, same | runner → studio projector |
| `dlq.<original subject>` | stream `DLQ` (30 days) | the original command, unchanged | runner, for a command that fails its schema |
| `auth.validate.token`, `auth.validate.role` | core request/reply, queue group `auth` | bare JSON (see the schemas) | studio → auth |
| `cmd.pipeline.submit`, `.resume`, `.pause`, `.stop`, `.retry` | stream `CMD` (work queue) | `cmd.pipeline.<same>`, `Nats-Msg-Id <batch_id>:<n>` (n = the batch's command counter, submit = 1) | studio outbox relay → pipelines (durable `pipelines`, filter `cmd.pipeline.>`, one at a time, in order) |
| `evt.pipeline.<batch_id>.batch`, `.row` | stream `EVT` | `evt.pipeline.batch`, `evt.pipeline.row`, `Nats-Msg-Id <batch_id>:<seq>` | pipelines → studio projector (durable `studio-pipelines`, filter `evt.pipeline.>`) |
| `evt.studio.<session_id>.run_finished` | stream `EVT` | `evt.studio.run_finished`, `Nats-Msg-Id <run_id>:finished` | studio outbox relay → pipelines (durable `pipelines-runs`, filter `evt.studio.>`) |
| `studio.runs.create` | core request/reply, queue group `studio` | bare JSON (`req.studio.runs.create`) | pipelines → studio |
| `evt.auth.revoked` | core | `evt.auth.revoked` | auth → every Go service's token cache |
| KV `registry` keys `<host>.<env>.nodes`, `.workflows`, `.models` | KV | `registry.schema.json` `$defs/nodes`, `workflows`, `models` | the runner writes its own keys once at start; studio reads |
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
  `failed` after the last step. Cancel is checked before each step.
- **Seeds.** A `seed` param is an integer (≤ 2^53−1); `null` or `-1` means random: the runner picks one and reports it
  in `evt.run.status` `succeeded` `data.resolved.seed` and in each `evt.run.output` `data.seed` (both ≥ 0).
- **The qwen-image family (torch).** Workflow `qwen-image` (env `torch`), text-to-image, and multi-reference edit
  when `encode.refs` is not empty:
  `prompt` (kind `prompt`, `text`) · `seed` (kind `seed`, `seed` default -1) · `canvas` (kind `canvas`, `mode`
  auto|fixed default auto, `width`, `height` 256–4096 step 32, default 1024) · `model` (`qwen-image/model`,
  `variant` base|turbo, `precision` bf16|int8 default int8) → `encode` (`qwen-image/encode`, in `model`, `prompt`;
  `transparent`, `refs`: rows, 0–10 items `{image: Image}`, mention `<image{n}>`, default `[]`) → `sample`
  (`qwen-image/sample`, in `model`, `cond`, `canvas`, `seed`; `steps` 1–200 default 40, hidden for turbo) → `decode`
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
  `<host>.<env>.workflows` publishes the workflow id. None at creation, or at any later pass while the run is queued:
  the run fails at once with `no live host for env <env>`.
- **One run at a time per host**, across envs: a host is free when studio has no run dispatched to it (any env) that
  is not yet terminal. Studio's own table decides this, not the runner's `state`.
- **Order**, when a host is free: `interactive` before `batch` (batch runs come from `studio.runs.create`); within a lane,
  round-robin between users: the user whose last released run is the oldest goes first (never released = first; a
  tie goes to the older oldest queued run); within one user, the oldest queued run, except that a queued run whose model is
  resident on that host goes first. A run's model is its `<family>/model` node (`variant`, `precision`) with the model
  id of the family in the host's `models` key; resident = an entry of the hosts value's `resident` with the same
  `model`, `variant` and `precision`.
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
  order) failing with the same error pause the batch (`reason: 3 rows in a row failed: <error>`).
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
    its start; the new attempt in the key makes a new run. A `done` batch reports `running` until the row ends.
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
