# pipelines_service

Runs batches: one workflow once per row of inputs, each row a LangGraph thread (plan 16.3, contracts/README.md
"Batches and the pipeline engine"). Python 3.14 + uv; LangGraph with `AsyncPostgresSaver` in the `pipelines`
database; no GPU, no torch, numpy or model code. Compose service `pipelines`, health on 8082.

| File | What |
|---|---|
| `main.py` | connect, migrate, report running batches paused (restart), pull `CMD` and `EVT` |
| `pipeline.py` | the pipeline rules the engine depends on (`problems`, tested on contracts/fixtures/pipelines) and the plan a graph is built from (a batch of one workflow = `step → out`) |
| `graph.py` | the row graph per node: inputs; step `submit` + `wait`; gate `gate` (interrupt) + `route` (pick, edge, loop cap); output; judge `submit` `wait` `verdict` [`gate`] `route`; write `submit` `wait` `apply`; decide `submit` `wait` `route`; memory |
| `agents.py` | judge/write/decide templates (`judge/v1` …) and schemas, the answer parse, `judge_verdict`, `inputs_hash`, the mention scan |
| `memory.py` | pipeline memory on LangGraph's `AsyncPostgresStore` (namespace `<owner>/pipeline/<id>`, 500 items) and the `pipelines.memory` responder |
| `states.py` | `pipelines.state.get` responder (queue group `pipelines`): a row's state at a checkpoint, owner only |
| `engine.py` | batches and rows: row 1 first, pause, resume, stop, retry, restart, events, thread cleanup |
| `consumer.py` | `cmd.pipeline.{submit,resume,pause,stop,retry,pick}`, durable `pipelines` on `CMD`, one at a time, inbox dedupe |
| `events.py` | `evt.studio.<session>.run_finished`, durable `pipelines-runs` on `EVT`: resumes the waiting thread |
| `messaging/` | contract validation (`gen/`), `evt.pipeline.<batch_id>.{batch,row}` publisher, `studio.runs.create` client |
| `migrations/` | `batches` (snapshot, control, seq, fail streak, memory snapshot, replay_from), `batch_rows`, `inbox`, `decisions` (the replay source); LangGraph's checkpoint and store tables via `setup()` |
| `agent_loop.py` | (1.15) the Agent node as plain graph nodes: per turn `submit` (one model call) `wait` `tools` (checked turn, tools run as the owner), then `route`; the director's `wake` … `reply` on its own thread |
| `remote.py` | (1.15) remote calls queued in pipelines' DB and sent by its worker: per-provider in-flight limit, per-batch caps, Pause holds, Stop cancels |
| `providers.py` | (1.15) the only code that calls a model provider (`ChatOpenAI(base_url=…)`, built inside a call): the host rule, key files, failure classes and retries, `pipelines.providers.test` |
| `judge_test.py` | (1.28) `pipelines.judge.test`: a judge test's filled `judge` template (with the judge's memory now) and its verdict from the run's answer (the Judge panel's Test on takes) |
| `assistant.py` | (1.44) the in-app assistant: a LangGraph graph per chat (thread `assistant:<id>`; model → tools → wait with `interrupt()`), `cmd.assistant.{turn,delete}` (durable `pipelines-assistant`), `evt.assistant.step`, per-message and per-user caps (`assistant_spend`), server cards (look_at, web_search, forget) |
| `assistant_memory.py` | (1.44) the assistant's memory on the store (namespace `<owner>/assistant/<kind>`, 200 items) and the `pipelines.assistant.memory` responder |
| `server.py` | (1.15) the `server` env runner for each host in `PIPELINES_SERVER_HOSTS`: registry, hosts key, `cmd.run.<host>.server` → one local-server call → `evt.run.status` |
| `custom.py`, `packs/` | (1.15) custom nodes (pydantic in/out; example `text/prefix`), published to registry `pipelines.engine.custom` |
| `tests/fake_openai.py` | the fake OpenAI-compatible server (stdlib; `python tests/fake_openai.py --port 8399` for the fake overlay) |
| `health.py` | `GET /health`: 200 while NATS is connected and the database answers |

## How a batch runs

- **Submit** stores the snapshot and every row, publishes `running` and drives row 1. When row 1 ends (any
  outcome) every row is released, at most 16 rows of a batch (`engine.WINDOW`) asked of studio and not ended at a
  time (each row end lets the next go), so one user adds a bounded number of runs to the line-up; the dispatcher
  orders the runs. At most `PIPELINES_DRIVE_CONCURRENCY` rows run graph code at once; a parked row costs nothing. A
  row whose drive fails (Postgres or NATS down) is tried again with backoff until it parks, ends or is paused.
- **A step**: `submit` asks `studio.runs.create` (lane `batch`) under `<batch>:<row>:<attempt>:step:0:0`
  (timeouts and `internal` are asked again with backoff, same key); `wait` only calls `interrupt()`; a
  `run_finished` for that run resumes it with the result, a terminal reply skips it. A row lock serialises one
  thread's resumes, so an event that arrives before the thread has parked waits for the park.
- **Pause** starts no new rows; **Resume** republishes the unfinished rows and continues them; **Stop** cancels rows
  that never started, the rest end with their cancelled runs, then `stopped`; **Retry** reruns a row's thread from
  the start with the next attempt (a new key, a new run). Three rows in a row failing with one error pause the batch.
- **Restart**: every running batch is reported paused (`the pipeline service restarted; press Resume`), every row
  republished, and a batch whose rows have all ended ends. A row end is followed up again whenever it is seen again
  (a redelivered `run_finished` or command), so a lost publish never leaves a batch hanging. Commands are redelivered
  without limit. `run_finished` events still resume parked threads. On Resume threads continue from
  their checkpoints; a submit that never recorded its run asks again and gets the same run.
- **Pipelines** (`snapshot.pipeline`): one compiled graph per batch. A step asks one run per seed under
  `<batch>:<row>:<attempt>:<step>:<visit>:<seed index>` and resumes once every run has ended (results wait in
  `batch_rows.results`); a gate parks the row `waiting` (no slot, no window; row 1 parking releases the rows) until
  `cmd.pipeline.pick` for that gate visit (stored in `batch_rows.pick`, any other dropped); a reject takes the fail
  edge until `max`, then `then`. Every graph step is published as `evt.pipeline.step` from the checkpoint history.
  Retry with `from_step` forks the thread from `checkpoint_id` (`aupdate_state`) as the new attempt.
- **Agent nodes** (contracts/README.md "The llm pack and agent nodes"): every judge, write or decide visit is one
  ask run through `studio.runs.create` under `<batch>:<row>:<attempt>:<node>:<visit>:0` (`:1` for the one re-ask
  after its answer failed pipelines' checks), waited for like a step's run. `submit` first looks up the replay key
  `<batch|replay_from>:<row>:<node>:<visit>:<inputs hash>` in `decisions`: a hit is reused, no run. A judge derives
  its verdict (keep the best K that pass every must), records the decision (on `evt.pipeline.step`, stored first),
  then parks like a gate in Suggest (its pick suggested; `agreed` and a pick's `note`, which becomes a judge example)
  or routes alone in Auto; a fail goes along its fail edge until `max`, then `then`. A write sets its `$<Text input>`
  for the rest of the row attempt; a decide takes one of its declared routes (the schema's enum; `then` is forced
  once `max` capped turns are used). A failed run, or a second failed check, fails the row `<node>: <error>`.
- **Memory**: the pipeline's namespace is copied onto the batch at Submit (`evt.pipeline.batch` `memory`); every
  read of the batch (memory read nodes, a judge's examples) uses that copy; memory write nodes write the live
  namespace under one id per (batch, row, node, visit), seen from the next batch. LangSmith tracing is off unless
  `LANGSMITH_TRACING` is set.
- When every row has ended the batch is `done` (or `stopped`) and its threads are deleted; the batch row and
  snapshot stay.

## Stage B (API 1.15)

- **Agent**: each turn is a queue item (local: the turn schema in `ask.schema`; a server: `server-ask` with native
  tools; remote: a queued call), key `…:<node>:<visit>:t<turn>[r]`; tool calls `<key>:<i>` reach studio by
  `studio.tools` or pipelines' memory; caps on turns, tool calls and tokens end the visit (`<node>: stopped at …`).
  A local or server visit replays its end without running its tools again; a remote one never replays.
- **Fan-out**: a step with `each` is `fan` → one `Send` per item to `branch` → `wait`, branch runs keyed
  `<item>-<seed>`, merged in `fan` by a reducer; the thread resumes once every branch's runs ended.
- **Director**: thread `<batch>:director`, one wake per message or row end (`director_inbox`), row 0's queue items;
  its reply and notices go out as `evt.pipeline.chat`; the batch does not end mid-wake (unless stopped).
- **Egress**: remote calls need `PIPELINES_REMOTE_MODELS` and the pipeline's `remote`; servers must resolve to
  loopback or private addresses. `tests/test_stage_b.py` runs every node kind with the switch off under a socket
  guard and asserts no connection left loopback and private ranges.

## Commands

```sh
cp .env.example .env     # then the real NATS and database credentials
make dev                 # the service against the stack's NATS and Postgres
make test                # unit tests; engine tests need .test.env (TEST_DATABASE_URL, .tools/test-db), else skip
make lint                # uv lock --check, ruff check, ruff format --check
make gen                 # copy contracts/messages into gen/ (committed)
```
