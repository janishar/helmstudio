# pipelines_service

Runs batches: one workflow once per row of inputs, each row a LangGraph thread (plan 16.3, contracts/README.md
"Batches and the pipeline engine"). Python 3.14 + uv; LangGraph with `AsyncPostgresSaver` in the `pipelines`
database; no GPU, no torch, numpy or model code. Compose service `pipelines`, health on 8082.

| File | What |
|---|---|
| `main.py` | connect, migrate, report running batches paused (restart), pull `CMD` and `EVT` |
| `graph.py` | the row graph `START → submit → wait → record → END` (submit → record on a terminal reply) |
| `engine.py` | batches and rows: row 1 first, pause, resume, stop, retry, restart, events, thread cleanup |
| `consumer.py` | `cmd.pipeline.{submit,resume,pause,stop,retry}`, durable `pipelines` on `CMD`, one at a time, inbox dedupe |
| `events.py` | `evt.studio.<session>.run_finished`, durable `pipelines-runs` on `EVT`: resumes the waiting thread |
| `messaging/` | contract validation (`gen/`), `evt.pipeline.<batch_id>.{batch,row}` publisher, `studio.runs.create` client |
| `migrations/` | `batches` (snapshot, control, seq, fail streak), `batch_rows`, `inbox`; LangGraph's tables via `setup()` |
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
- When every row has ended the batch is `done` (or `stopped`) and its threads are deleted; the batch row and
  snapshot stay.

## Commands

```sh
cp .env.example .env     # then the real NATS and database credentials
make dev                 # the service against the stack's NATS and Postgres
make test                # unit tests; engine tests need .test.env (TEST_DATABASE_URL, .tools/test-db), else skip
make lint                # uv lock --check, ruff check, ruff format --check
make gen                 # copy contracts/messages into gen/ (committed)
```
