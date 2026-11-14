# studio_service

The Go service behind Kong for `/v1/studio/*` ([contracts/openapi.yaml](../contracts/openapi.yaml), tag `studio`):
sessions, workflows read from the runners' registry, runs, the per-session event log and stream, hosts. gomicro layout
on [orago](../orago/README.md) (through the `replace` in `go.mod`; image builds pass `../orago` as a named build context),
`pgx` + `goose` on its own `studio` database with row-level security, NATS JetStream through `orago/micro`. Output
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
   `enum` (422 names the key; a `seed` takes null or -1 for random, and `step` / `when` are carried, not checked). It places the run on a live host (hosts key fresher than 30 s) with the workflow's env,
   idle first. In one transaction: the run row (`queued`), the outbox row for `cmd.run.execute` on
   `cmd.run.<host>.<env>` (output prefix `u/<owner>/runs/<run>/`) and a `status queued` event. No live host: the run is
   stored `failed` (`no live host for env <env>`) and nothing is sent. 202 either way.
2. The relay publishes the outbox (`Nats-Msg-Id <run>:<attempt>`).
3. The projector (durable `studio-projector` on `EVT`, `evt.run.>`, one message at a time) checks the run's owner and
   session against the envelope and subject, dedupes on `(job_id, seq)`, applies `status` (queued → running → one of
   succeeded, failed, cancelled; nothing after the end), `progress` (with `phase`, `eta_s`) and `output` (keys under the run's prefix only, its `seed`), keeps a
   `succeeded`'s `resolved` (the seed the runner picked) on the run, and appends each to `run_events` under the session's next `seq`.
4. `GET /sessions/{id}/events` serves the log as JSON (`?since=`) or SSE (`Last-Event-ID`, polls every second,
   keep-alive every 15 s); output keys are signed when read.
5. Cancel: a run still in the outbox is cancelled at once and never sent. Otherwise the run is marked and its id written
   to the KV `cancels`; the runner reports `cancelled`.
6. The lease sweep (every 5 s, after a 30 s start-up grace) ends an open run with no event for 30 s whose host is
   offline or live but not on it (a runner whose hosts key names the run holds it through a long model load), or that was asked to cancel and did not answer: `cancelled` if asked, else `failed`.

NATS user `studio`: publishes `cmd.run.>` and `$KV.cancels.>` (a KV put, sent as a request for the stream's ack),
consumes `evt.run.>` (durable `studio-projector`), watches the KV `registry` and `hosts`, requests
`auth.validate.token`, subscribes to `evt.auth.revoked`.

## Layout

| Path | What |
|---|---|
| `cmd/main.go` | `studio` (server), `studio health` (container check) |
| `startup/` | config → DB (migrations) → NATS → module → relay, projector, registry and hosts watches, lease sweep → serve |
| `config/` | env (see `.env.example`) |
| `common/` | `Caller`, `OwnerPrefix`, `Owns`, `WithUser` (RLS per transaction), `System`, `Signer` (`/files/{key}?exp=&sig=`) |
| `messaging/` | `Enqueue` (outbox), relay (`NatsJetStreamPublisher`), `ProjectorConsumer`, `FirstSeen`, `NatsCancel` (KV cancels) |
| `registry/` | the runners' KV values: `SetRegistry` / `SetHost` (fed by `micro.NatsJetStreamWatchKV`), `Workflows`, `Place`, `Live` |
| `api/auth/` | Bearer token → NATS `auth.validate.token`, cached until expiry, dropped on `evt.auth.revoked`; `middleware/` is the `/v1/` guard |
| `api/studio/` | `controller.go` (routes, SSE), `service.go` (sessions, workflows, hosts), `runs.go` (create, list, get, cancel, the state machine), `resolve.go` (inputs → filled graph), `projector.go`, `events.go`, `sweep.go` |
| `api/studio/{dto,model,message}/` | HTTP bodies, table rows, NATS bodies (`cmd.run.execute`, `evt.run.*`) |
| `migrations/` | `00001` platform (RLS function, outbox, projector dedupe), `00002` sessions, runs, run_outputs, run_events |
