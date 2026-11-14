# catalog_service

Catalog Agent: the Go service behind Kong for `/v1/catalog/*` ([contracts/openapi.yaml](../contracts/openapi.yaml)).
gomicro layout, Gin through `orago/network`, per-feature DTOs, `pgx` + `goose` on its own `catalog` database, Postgres
row-level security, NATS JetStream through `orago/micro`'s helpers (outbox → `CMD`, `EVT` projector, `registry` KV
watcher), SSE from the `catalog_events` table, pluggable object store (`s3` / `memory`). Its answers carry signed `/files/…`
links that [files_service](../files_service/README.md) serves (shared `FILE_URL_SECRET`). Built on
[orago](../orago/README.md), through the `replace` in `go.mod`; image builds need `make image` or compose, which pass
`../orago` as a named build context.

```sh
cp .env.example .env            # then FILE_URL_SECRET etc. (on the stack secrets come from deploy/secrets via *_FILE)
cp .test.env.example .test.env  # TEST_DATABASE_URL: scratch catalog_test DB owned by the catalog role (see the file)
../.tools/test-db               # the ora-catalog-test Postgres on 5444
make test                       # unit tests; integration tests skip without TEST_DATABASE_URL
make dev                        # go run ./cmd: .env names the stack hosts; on the host point it at the test Postgres (5444, never the live 5443) and NATS/S3 you published by hand
docker compose -f ../deploy/docker-compose.yml up -d --build catalog   # on the ora-catalog stack (deploy/compose/catalog.yml)
```

Env: `.env.example`. Secrets (DB password, NATS credentials, S3 keys, `FILE_URL_SECRET`) are read from `<NAME>_FILE`
when set (compose `secrets:`), else from `<NAME>`. NATS user `catalog`: publishes `cmd.catalog.>`, `agents.cancel` and `agents.cancel.image` (Stop), consumes `evt.catalog.>`,
requests `catalog.interpret` / `catalog.poses.suggest` from agents_service, reads the `registry` KV (`catalog_library`,
`catalog_prices`). `RAW_MAX_BODY_MB` (default 512) caps raw image uploads, `MAX_BODY_MB` everything else.

Models and backgrounds: a step item's `text` is the brand's description (trimmed, at most 500 characters; picks or a
text required); Start copies it to every SKU's brief (`model_description` / `background_description`) and leaves the
model or background null when nothing is picked; `PATCH /skus/{id}` sets a SKU's own (an override); a run sends a
described-only choice as null with its description. A SKU's `attributes.wearer` (agents set it; `PATCH /skus/{id}`
`wearer` changes it, a move to a product of one wear sets it) limits its models to that wearer's: Start spreads only
the fitting picks (randomized within each wear), else the product's `default_men_model` (men's wear) or
`default_model` (`registry.Category.DefaultModelFor`, `Library.SuitsWearer`); a step description of the other wear's
look (`textWearer`) gives way to that default; Apply to all skips a model of another wear.

Poses: an item's `custom_poses` and `pose_refs` are checked against its plan and the session's pose_ref uploads;
Start sets each SKU's `shots_brief` from its product's brief (links removed, one line); a run carries a custom shot's
`prompt`, `on_model` and its reference photos (`references`) and the SKU's `shots_brief`.

Background loops, started in `startup/` and joined on shutdown before NATS and the pool close: outbox relay → `CMD`
through `messaging.NatsJetStreamPublisher`; `EVT` projector (`catalog.Projector`), consumed with
`micro.NatsJetStreamConsume` (`messaging.ProjectorConsumer`: durable `catalog-projector`, `evt.catalog.>`), one handler per `evt.catalog.*.<type>`
(status, log, step, question, skus, image, run): dedupe on `(job_id, seq)`, applied to the `catalog_*` tables and appended
to `catalog_events` with a per-session `seq` (object keys stay in the tables; stored events name row ids, so links are
signed when read); registry, kept by `micro.NatsJetStreamWatchKV` feeding `registry.Set`. Download zips go under
`u/<owner>/archives/`, which files_service's janitor empties after a day.

## Layout

| Path | What |
|---|---|
| `cmd/main.go` | `catalog` (server), `catalog health` (container check) |
| `startup/` | config → DB (migrations) → store → NATS → module (`micro.NewRouter`, NATS micro service `catalog`) → relay/projector/watcher → serve |
| `config/` | env (see `.env.example`) |
| `common/` | `Caller` (`MustCaller`, `SetCaller`), `OwnerPrefix`, `Owns`, `WithUser` (RLS settings per transaction), `System`, `Signer` (`Sign`, `SignPtr`: `/files/{key}?exp=&sig=` links) |
| `utils/` | `ReadImage` (size cap + JPEG/PNG/WEBP sniff) |
| `messaging/` | `Enqueue` (outbox), relay (`NatsJetStreamPublisher`), `ProjectorConsumer`, `FirstSeen` (projector dedupe) |
| `registry/` | the `registry` KV snapshot: `Set` (fed by `micro.NatsJetStreamWatchKV`), `Current` |
| `api/auth/` | `Service`: Bearer token → NATS `auth.validate.token` through `micro.RequestNats`, cached until expiry, dropped on `evt.auth.revoked`; `middleware/` is the `/v1/` guard |
| `api/catalog/` | Catalog Agent, every `/v1/catalog/*` route: `controller.go` (routes, multipart, SSE), `service.go` (sessions, inputs, memory, library, favorites, estimate, stats, quality metrics, questions, Start), `generation.go` (runs with each SKU's `passed` / `not_passed` live images, Stop a run or chosen images (broadcasts `agents.cancel` / `agents.cancel.image`), regenerate, review, approve, SKU overrides, download), `requests.go` (typed requests via `catalog.interpret`), `nextstep.go` (Next step rules; leads with SKUs whose main image the gate is waiting on), `projector.go`, `events.go`, `state.go` (a session's rows + derived SKU statuses), `archive.go` (zip), `jobs.go` (the dev Jobs view: the caller's jobs and one job's events) |
| `api/catalog/{dto,model,message}/` | HTTP bodies, `catalog_*` rows, NATS bodies (`cmd.catalog.process`, `cmd.catalog.run`, `evt.catalog.*`, the two requests) |
| `migrations/` | `00001` platform (RLS function, outbox, projector dedupe), `00002` catalog tables, `00003` the deferred re-check flag, `00004` per-image attempts, first_pass and the product at render time (metrics), `00005` every render try of an image (`tries`: keys under the owner's prefix, signed as `Image.attempts`) |

## Shared helpers

```go
common.MustCaller(ctx) common.Caller            // {ID, Email, Roles, ClientID}; .IsAdmin(); controllers only
network.SendJSON / SendError / SendMixedError / ReqBody[T] / ReqBodyOptional[T]   // orago/network
common.WithUser(ctx, pool, caller, func(tx pgx.Tx) error) error   // RLS: app.user_id / app.role
common.System(ctx, pool, fn)                     // service-level (projector), RLS sees everything
common.OwnerPrefix(owner) / common.Owns(owner, key)
utils.ReadImage(fh *multipart.FileHeader, label string, maxBytes int64) (data []byte, ext string, err error)
messaging.Enqueue(ctx, tx, messaging.Command{JobID, OwnerID, Attempt, Data}) error   // Data has Subject()
signer.Sign(key) string / signer.SignPtr(*string) *string
registry.Current() registry.Snapshot              // Library, Prices (KV)
```

A feature exports `NewService(...)` and `NewController(service, ...) micro.Controller` with its routes in `MountRoutes`;
the service owns the transactions and imports no Gin. A root middleware authenticates all of `/v1/`, unknown paths
included, and the body limit answers 411 and 413 before that. The event stream (`GET .../events` with
`Accept: text/event-stream`) replays `catalog_events` after `since` / `Last-Event-ID` and then polls the table every
second (`catalog.PollEvery`), so it works on any replica without a NATS subscription.

Tests: `make test`; the integration suite in `tests/` (HTTP → outbox → hand-driven agents events → projector → read
models, SSE, per-user 404s, zip) needs `TEST_DATABASE_URL` and skips without it.
