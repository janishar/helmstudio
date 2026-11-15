# studio_service

The Go service behind Kong for `/v1/studio/*` ([contracts/openapi.yaml](../contracts/openapi.yaml), tag `studio`):
sessions, workflows (the runners' registry templates and each user's own), runs, the per-session event log and stream, hosts. gomicro layout
on [helmgo](../helmgo/README.md) (through the `replace` in `go.mod`; image builds pass `../helmgo` as a named build context),
`pgx` + `goose` on its own `studio` database with row-level security, NATS JetStream through `helmgo/micro`. Output
links are signed `/files/…` URLs that [files_service](../files_service/README.md) serves (shared `FILE_URL_SECRET`).

```sh
cp .env.example .env            # then FILE_URL_SECRET etc. (on the stack secrets come from deploy/secrets via *_FILE)
cp .test.env.example .test.env  # TEST_DATABASE_URL: the scratch studio_test DB owned by the studio role
make test                       # unit tests; the integration tests in tests/ skip without TEST_DATABASE_URL
make lint
docker compose -f ../deploy/docker-compose.yml up -d --build studio   # on the helmstudio stack (deploy/compose/studio.yml)
```

Env: `.env.example`. Secrets are read from `<NAME>_FILE` when set, else from `<NAME>`.

## How a run goes

1. `POST /sessions/{id}/runs` resolves the workflow (registry) with the inputs: node schema default, then the
   workflow's value, then `"<node>.<param>"` from the request, each checked against the param's type, `min`/`max` and
   `enum` (422 names the key; a `seed` takes null or -1 for random, and `step` / `when` are carried, not checked). It stores the run `queued` (lane
   `interactive`, attempt 1, no host) with the resolved graph, and a `status queued` event. No eligible host (below):
   the run is stored `failed` (`no live host for env <env>`, or the model variant missing on every live host) and
   nothing is sent. 202 either way; the answer has the host it is lined up for and its `position`.
2. The dispatcher (`api/studio/dispatcher.go`, every 500 ms, one pass at a time across replicas by a Postgres
   advisory lock) releases at most one run per host (across envs) while no released run on it has ended. Eligible
   host: hosts key fresher than 30 s, the run's env, its runner publishes the workflow, and the run's model variant is
   present when its `models` key lists variants. Order: interactive before batch; round-robin between users (oldest
   last release first); within a user the oldest run, but one whose model (`<family>/model` variant and precision)
   is resident on that host first; a run resident on another free host goes there, else hosts idle longest go first.
   Release: `runs.host`, `released_at`, the owner's `dispatch_owners` row (its last release: deleting a session
   never resets a turn) and the outbox row for `cmd.run.execute` on `cmd.run.<host>.<env>` (output prefix
   `u/<owner>/runs/<run>/<attempt>/`) in one transaction. No pass decides anything until the `hosts` and `registry`
   watches have delivered their current values (create answers 503 meanwhile); at most 100 open runs per user (409). `position` and `GET /queue` replay the same order on read.
   Lease: from 30 s after release the host's key must name the run and attempt, and it must stay fresh; a loss seen
   for 10 s (and not while the projector has a backlog) purges `cmd.run.<host>.<env>` and queues attempt 2 (a
   `status queued` event with `attempt: 2`; attempt 1's outputs and their takes are dropped); a second loss fails the run. A lost run asked to cancel ends `cancelled`.
   The relay publishes the outbox (`Nats-Msg-Id <run>:<attempt>`).
3. The projector (durable `studio-projector` on `EVT`, `evt.run.>`, one message at a time) checks the run's owner and
   session against the envelope and subject, drops events of an attempt that is not the run's current one, dedupes on `(job_id, attempt, seq)`, applies `status` (queued → running → one of
   succeeded, failed, cancelled; nothing after the end), `progress` (with `phase`, `eta_s`) and `output` (keys under the run's prefix only, its `seed`), keeps a
   `succeeded`'s `resolved` (the seed the runner picked) on the run, and appends each to `run_events` under the session's next `seq`.
4. `GET /sessions/{id}/events` serves the log as JSON (`?since=`) or SSE (`Last-Event-ID`, polls every second,
   keep-alive every 15 s); output keys are signed when read.
5. Cancel: a run not released, or still in the outbox, is cancelled at once and never sent. Otherwise the run is marked and its id written
   to the KV `cancels`; the runner reports `cancelled`.

## Image inputs (API 1.4)

- `resolve.go` checks `image` and `rows` values (`{take_id}` only, item fields, count); `images.go` checks the takes
  when a run is created (Create, each batch row at Submit, `studio.runs.create`) with an explicit owner check: the
  owner's ready, undeleted image with its object present and of its size, else 422 `encode.refs[2].image: take <id> not
  found` / `is not an image`. The run keeps `{take_id}` (so `made_with` and `reuse` do).
- `takes.etag` is the object's md5 hex (the S3 ETag of a single-part PUT): set at upload complete, and lazily, the first
  time a run output is used as a reference (no backfill job).
- Release (`dispatcher.go`) resolves each `{take_id}` as System into `{take_id, key, content_type, width, height, bytes,
  etag}`; a reference deleted since the run was created fails it (`input image <take_id> is gone`), never sent.
- Lineage: the projector stores `take_parents` (input order, each once); deleted parents are left out on read;
  `GET /takes/{id}` adds `children`. `POST /takes/{id}/use` (gallery) fills a workflow's image input.

## User workflows (API 1.5)

- `user_workflows` (migration 00009, RLS on `owner_id`) holds a user's own documents (`nodes` with `bypass` and
  `position`, `studio`, `based_on`, `version`); templates stay the registry's. `workflows.go` serves `GET/POST
  /workflows`, `GET/PUT/PATCH/DELETE /workflows/{id}` (PUT checks `version`: 409 when stale; a template id is 409),
  `POST /workflows/validate` and `GET /nodes` (`registry.PaletteGroups`: kinds of live hosts only). At most 200 per user.
- `graph.go` is the graph rules (contracts/README.md "Graph rules"), tested on every `contracts/fixtures/graphs`
  valid/invalid fixture (`graph_test.go`); auto-wiring and wire-drop are the editor's and live in the web.
- A run or batch of a user workflow checks it against the live palette (any problem: 422 `workflow cannot run: …`);
  `resolve` takes `<node>.bypass` inputs, refuses inputs to wired params, splices bypassed nodes out and drops positions,
  so the run's `nodes` (sent as is) never change after an edit or a delete. The run keeps `workflow_version` and
  `workflow_title`; a batch keeps `workflow_version` with its snapshot. Eligible host for such a run: its `.nodes`
  publish every kind of the graph. `made_with` keeps the version and the settable inputs; reuse answers
  `workflow_status` (`current`, `changed` with unsettable inputs dropped, `deleted`).

## Batches (pipelines_service runs them)

- `POST /batches` builds the rows (lines, table or grid, then seed repeats; `batch_rows.go` `expand`), checks each with
  the run rules (422 `row N: …`), the 500-row and 768 KB caps, a live host, 5 unfinished batches (409), then stores the
  batch with its snapshot (the registry workflow with `node_schemas`), its rows and `cmd.pipeline.submit` (outbox,
  `Nats-Msg-Id <batch>:1`) in one transaction.
- Pause / Resume / Stop / Retry (`batches.go`) change `control` or the row and write `cmd.pipeline.<kind>` under the
  batch's next command number in the same transaction. The dispatcher holds a batch's queued runs while `control` is
  `pause` or pipelines reported `paused` since the last Resume; Stop cancels the open runs (as Cancel does).
- `studio.runs.create` (NATS request/reply, queue group `studio`) creates a lane-`batch` run once per idempotency key,
  as System, after checking owner, session, workflow and row against the batch record.
- Every terminal status of a batch run (projector, cancel, no eligible host, lease lost twice, created failed or
  cancelled) writes `evt.studio.<session>.run_finished` to the outbox (`Nats-Msg-Id <run>:finished`) in that
  transaction (`batch_record.go`).
- The batch projector (durable `studio-pipelines` on `evt.pipeline.>`, `batch_projector.go`) dedupes on (batch, seq),
  keeps the newest state per row and for the batch, drops older attempts, another owner's events, foreign runs and takes.
- `batch_events` is the per-batch log; an event carries no data: `GET /batches/{id}/events` (JSON or SSE, the session
  stream's code) sends the batch or row as it is when read, the last event per row and batch in a page.
- A row's shown status: pipelines' end once reported, else `running` once its run left the queue, else `queued`.

## Saved pipelines (API 1.7)

- `user_pipelines` (migration 00010, RLS) holds a user's pipeline documents; `pipelines.go` serves `/pipelines` like
  `/workflows` (the same 422 shape, 100 per user, delete 409 while a batch of it is unfinished). `pipeline_rules.go`
  is the pipeline rules, tested on every `contracts/fixtures/pipelines` fixture (`pipeline_rules_test.go`).
- `POST /batches {pipeline_id}` (`pipeline_batches.go`): rows over `<inputs node>.<name>`, every row checked against
  every step (takes from earlier steps stand in as placeholders), a base seed drawn per row and step (`batch_rows.seeds`),
  the document and each workflow with its schemas stored in `batches.pipeline`, `snapshot.pipeline` in the submit.
- `studio.runs.create` for a pipeline batch takes the step's inputs from the request (pipelines fills the binds),
  checks the key's step, seed index, workflow and the row's attempt; the take's `made_with.pipeline` comes from the key.
- The projector stores `waiting`, `parked`, `cells` (`evt.pipeline.row`) and every `evt.pipeline.step` (`batch_steps`,
  newest seq per row, attempt and step). A batch is shown `waiting` when pipelines says running and every unended row
  waits. Parked rows hold no run, so the dispatcher never counts them.
- `pipeline_review.go`: review grid, pick (recorded in `batch_rows.picks` and on the gate cell, `cmd.pipeline.pick`
  in one transaction; the same pick again sends nothing), the execution view, a step's state over
  `pipelines.state.get` (5 s; the caller's own batch only). Retry with `{from_step}` copies the steps before it to the
  new attempt and names the checkpoint of the step before. `save_pipeline.go`: Save as pipeline from a take's lineage.

## Elements and @ mentions (API 1.8)

- `library_items` (migration 00011, RLS) is one table for every library kind (elements now, looks in M3); images are
  take ids, checked like reference rows (`vetTakes`), a deleted take left out when read. `library.go` serves
  `/library` (200 per user, handle unique per owner, PUT with `version`, DELETE never refused).
- `mentions.go` is the mention rules (`scan`, `referenceRows`, `expandMentions`), tested on every
  `contracts/fixtures/mentions` fixture. `runGraphs` is the one place a run's graph is made: resolve, then the
  mentions against the owner's elements now (Create, a workflow batch's rows at Submit) or the batch's copy
  (`batches.elements`, `studio.runs.create`, Retry included). The resolved graph is stored, sent and recorded;
  `runs.mentions` keeps `{typed, elements}` for `made_with`; lineage and Save as pipeline read the typed view.

## Looks (API 1.10)

- Own looks are `library_items` of kind `look` (migration 00013: `loras` `[{lora_id, name, strength}]`,
  `from_take_id`); starter looks are the live hosts' registry `looks` keys (`registry.Looks`: highest version,
  read-only, `/library/{family}/{look_id}` answers 409 to writes). `GET /library` merges both, filters by `source` /
  `family`, and with `workflow` adds `fits` / `reason` (`lookFit`). "Save as look" is POST with `from_take`.
- `expandMentions` is the one library resolver: mentions and the chosen looks (`RunRequest.looks`, a batch row's
  `looks` cell, a pipeline step's `looks`) in the contracts' order: look images after the element images, look LoRAs
  into the look stack (`lookStack`; the user's row wins), look lines in the first prompt. The LoRA check that follows
  (and so the dispatcher's host rule) sees the look LoRAs. `runs.looks` keeps the request, `runs.mentions` the
  applied `looks` (made_with); a batch keeps a copy in `batches.looks`, a row its ids in `batch_rows.looks`. Reuse
  answers `looks` and `looks_status`; pipeline problems `unknown_look` / `look_fit` (`pipelineLooks`).

## Video and two envs on one host (API 1.11, M4 stage A)

- Video takes (migration 00014): `takes.video` / `run_outputs.video` keep `evt.run.output`'s `video` block
  (`{duration_s, fps, frames, has_audio, poster {key, width, height, bytes}}`); the projector drops a video output
  without it or with a poster outside the run's prefix. `Take.video` / `RunOutput.video` sign the poster; a video's
  `thumb_url` (gallery, children, review candidates, a look saved from it) is its poster. The sweep deletes both
  objects. Image-only inputs (`vetTakes`, "Use in…") refuse a video take: `take <id> is not an image`.
- `Workflow.media`: a template's as published, a user workflow's from its graph (`workflowMedia`); `GET /workflows?media=`.
  Graph rules use `fits` (an input of type `Media` takes `Image` or `Video`); a `frames` param is on its grid
  (`checkFrames`).
- The GPU gate of stage A became the hand-over of stage C (below).

## Video, stage B (API 1.12, M4 stage B)

- Run rules in `resolve.go` (Create, batch rows, `studio.runs.create`): `"auto"` on a `frames` param only where it
  declares `auto` (`checkFrames`); an int's `values` and, on the node's merged values, `values_by`
  (`valuesByDetail`: `sample2.steps: must be one of 3`); a rows param with an Image field may be `[]` in a workflow and
  needs its `min` at run; item fields left out take their defaults and are checked; keyframe positions
  (`positionErrors`, `position: seconds`: past the clip, a frame used twice; with auto length only the second).
  Graph rules add `values_by` to `param_invalid` and `preset_invalid` (`graph.go`). Tested on
  `contracts/fixtures/video/keyframes.json` and the graph, pipeline, mention and LoRA fixtures.
- Keyframe images are image inputs like reference rows (`images.go`: the owner's ready image takes; lineage parents in
  row order; "Use in…" fills `keyframes.images` row 1). Gallery multi-select to a batch is a `table` with the image
  column and `prompt.text` (no new API).
- LoRAs: renames with `at: end` (`api/lora/normalise.go`); bake-only stacks refuse `mode: adapter` by their enum; a row
  naming a LoRA the family's block lists in `prefused` for the graph's model variant is 422 (`checkLoras`).
- Auto length: the runner's `resolved.params` is kept on the run (`evt.run.status` succeeded) and, from
  `evt.run.output`, in the take's `made_with.inputs` with `made_with.typed` keeping `"auto"`.

NATS user `studio`: publishes `cmd.run.>`, `$KV.cancels.>` (a KV put, sent as a request for the stream's ack),
`runner.unload.>` (the GPU gate's requests) and `$JS.API.STREAM.PURGE.CMD` (the dispatcher's purge of a lost run's command),
consumes `evt.run.>` (durable `studio-projector`) and `evt.pipeline.>` (durable `studio-pipelines`), publishes
`cmd.pipeline.>` and `evt.studio.>`, serves `studio.runs.create`, requests `pipelines.state.get`, watches the KV `registry` and `hosts`, requests
`auth.validate.token`, subscribes to `evt.auth.revoked`.


## Two envs on one host: the hand-over (API 1.13, M4 stage C)

- `dispatcher.go` `handover` / `admit`: the run the order picks for a host is released when its planned peak
  (`planOf`: the models key's `memory` item, plus its LoRA files; none = the whole budget) plus every other resident
  entry of the host's present keys fits the smallest `memory.budget_gb`; else victims (fresh, unpinned, not what it
  reuses; unreferenced baked sets, then `last_used`, then larger) are named in one `runner.unload.<host>.<env>` per
  env (one in flight, every 10 s, 60 s wait). An ok of the current generation stands for that env's key until the
  key is rewritten after it or a run is released there; 120 s without room fails the run. The command carries
  `budget_gb`. All arithmetic in hundredths of a GB.
- Order (`pick`): lane, then users round-robin; a user's head is their oldest resident run, else their oldest; the first
  head resident or overdue (`SwapSkips` 4 passes, `SwapWait` 180 s, counted in memory on release or failure) goes.
  Positions use the dispatcher's skip counts (`Service.SetDispatcher`).
- `GET /hosts`: `host_memory` per host and `last_used` per resident component. Save as pipeline: a step of another family
  than the one before gets its own input `prompt_<k>`; saved steps carry `seeds: 1`.
- Tests: `api/studio/handover_test.go` runs every `contracts/fixtures/dispatch` fixture (`admit.*` through `admit`,
  `order.*` through a simulation of `pick` and `admit`); `tests/handover_test.go` the interleavings on the DB.

## The llm pack and agent nodes (API 1.14, M4b stage A)

- Text (ask) workflows (`registry.IsText`: an `answer` node with its text wired; `media: text`) are internal: left out
  of `GET /workflows`, 422 from Create, a workflow batch and "Use in…" (`refuseText`), `workflow_cannot_run` as a step.
  Only `studio.runs.create` for a judge, write or decide node makes one (`pipelineStepRun`: the node's resolved ask
  workflow, branch 0 or 1, visit < 32). Its `Media` rows take image or video takes (`vetTakes`/`resolveImages` allow a
  video for a text graph, sent in the image form); `runs.thumbs` counts 1 per image and 3 per video for the memory plan.
  The answer (`evt.run.status` `answer`) is stored on the run (`runs.answer`, `Run.answer`), copied into
  `run_finished` and a terminal `runs.create` reply; no take, no lineage.
- Dispatcher: `Model.Plan(variant, precision, workflow)` takes the item naming the run's workflow, else the general
  one; more thumbnails than `planned_at.images` plans at the whole budget. llm is one more mlx family (no new mechanism).
- Pipeline rules (`pipeline_rules.go`, every `contracts/fixtures/pipelines` fixture incl. `bounds` and `models`):
  judge/write/decide/memory nodes, decide routes, `too_many_calls`, `unknown_model` and `auto` resolution
  (`pipelineModels`). `Pipeline.bounds`, `PipelineValidation.{bounds, models}`; Submit stores `batches.bounds`/`models`,
  puts every ask workflow in the snapshot, fills each node's defaults and `workflow`, and checks a live host for each ask
  as its runs will ask (a family ask on the reused step's variant and precision: `askModel`).
- Decisions (migration 00015): the batch projector keeps each step event's `decision` (`decisions`, newest seq per id,
  a judge's candidates in `take_ids`) and `memory`, and the batch's `memory` snapshot. `GET …/rows/{n}/decisions`,
  `GET /takes/{id}/decisions`, `Take.decisions` (gallery), the execution view's `decision`/`memory`, the review grid of a
  judge (suggested, verdict, model, each candidate's checks). A judge pick derives `agreed`, records it on the cell and
  the decision and sends it with `note` (a gate pick with a note is 422).
- Run again (`batch_again.go`): an ended batch's snapshot, session, rows and seeds as a new batch; `same` sends
  `replay_from`. Memory (`memory.go`): `/memory` checks the pipeline is the caller's, then asks `pipelines.memory` as
  the caller (5 s, else 503); deleting a pipeline asks `clear`. DELETE `/memory/{id}` asks each of the caller's
  pipelines (the request needs a pipeline id).

## Layout

| Path | What |
|---|---|
| `cmd/main.go` | `studio` (server), `studio health` (container check) |
| `startup/` | config → DB (migrations) → NATS → module → relay, projector, registry and hosts watches, dispatcher → serve |
| `config/` | env (see `.env.example`) |
| `common/` | `Caller`, `OwnerPrefix`, `Owns`, `WithUser` (RLS per transaction), `System`, `Signer` (`/files/{key}?exp=&sig=`) |
| `messaging/` | `Enqueue` (outbox), relay (`NatsJetStreamPublisher`), `ProjectorConsumer`, `FirstSeen`, `NatsCancel` (KV cancels), `NatsPurge` (CMD subject) |
| `registry/` | the runners' KV values: `SetRegistry` / `SetHost` (fed by `micro.NatsJetStreamWatchKV`), `Workflows`, `Publishes`, `Model`, `Live` |
| `api/auth/` | Bearer token → NATS `auth.validate.token`, cached until expiry, dropped on `evt.auth.revoked`; `middleware/` is the `/v1/` guard |
| `api/studio/` | `controller.go` (routes, SSE), `service.go` (sessions, workflows, hosts), `runs.go` (create, list, get, cancel, the state machine), `resolve.go` (inputs → filled graph), `projector.go`, `events.go`, `dispatcher.go` (the fair queue, leases, `GET /queue`) |
| `api/studio/{dto,model,message}/` | HTTP bodies, table rows, NATS bodies (`cmd.run.execute`, `evt.run.*`) |
| `api/studio/{workflows,graph}.go` | user workflows (CRUD, validate, the palette) and the graph rules, `splice` |
| `api/lora/` | the LoRA library (`/v1/studio/loras`: list, get, upload by signed PUT, complete, rename, delete, the sweep), `safetensors.go` (the bounded header parser: 8 MB, 20000 tensors, offsets, dtypes; no tensor data held), `normalise.go` (key styles, the normaliser, upload checks, family detection; tests run `contracts/fixtures/loras` as real files), `Usable` (what runs may use) |
| `api/studio/loras.go` | LoRA values of a run: checked as the owner in `runGraphs`, kept as `runs.loras`, resolved into the command at release; the dispatcher's shared-LoRA host rule, baked residency and deleted-LoRA failure |
| `api/studio/batch*.go` | batches: `batches.go` (Submit, reads, control, events, `studio.runs.create`), `batch_rows.go` (rows from lines/table/grid/seeds), `batch_projector.go` (`evt.pipeline.*`), `batch_record.go` (`run_finished`, the batch log) |
| `migrations/` | `00001` platform (RLS function, outbox, projector dedupe), `00002` sessions, runs, run_outputs, run_events, … `00007` batches, batch_rows, batch_events, run batch columns, `outbox.msg_id`, … `00012` loras, `runs.loras`, `00013` looks (`library_items.loras`, `from_take_id`, `runs.looks`, `batches.looks`, `batch_rows.looks`) |
