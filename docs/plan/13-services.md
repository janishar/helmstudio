# helmstudio-next plan — Service architecture

[← Plan index](../README.md)

## 17. Services, data and messaging

Decided 2026-11-14: helmstudio-next is a **multi-user, self-hosted** system
built as microservices, following the reference repository
`oralab-catalog-agents` (its `docs/ARCHITECTURE.md` and `docs/HELMGO.md`). Go
owns auth and every API layer; Python owns model execution and LangGraph;
Postgres holds the data; NATS carries the messages. This section owns the
service boundaries. What happens inside a runner is section 6; pipelines are
section 16.

### 17.1 Services

| Service | Language | Does | Owns |
|---|---|---|---|
| **kong** + API-key plugin | Kong, Go plugin | The only entry point: routing, API-key check, rate and size limits, correlation ids | — |
| **auth_service** | Go on helmgo | Sign-in, refresh, sign-out, users, roles (`user`, `dev`, `admin`), API keys; RS256 access tokens with rotating refresh tokens; answers `auth.validate.*` over NATS | `auth` DB |
| **studio_service** | Go on helmgo | Every `/v1/studio/*` route: sessions, workflows, takes and the gallery, library items, pipelines, batches, sequences, the model catalog, hosts. Owner scoping. The resolver (defaults, presets, library items, `when` / `requires`). Outbox → commands. Projector: events → tables. The event stream (SSE). The **dispatcher** (section 17.5). Signs `/files` links. | `studio` DB |
| **files_service** | Go on helmgo | Serves signed `/files/*` links from the object store with range requests; receives uploads; storage janitor | — |
| **pipelines_service** | Python, LangGraph | Runs pipeline graphs (section 16): consumes `cmd.pipeline.*`, asks studio to create runs, resumes threads when runs finish; calls remote models | `pipelines` DB (inbox, per-job seq, LangGraph checkpoints and store) |
| **runner_service** | Python, one process per env (`mlx`, `torch`, `native`) on each GPU host | Executes workflow runs (section 6): node graph, cache, residency manager, loaders, LoRA. Local LLM calls. ffmpeg jobs (thumbnails, export). Publishes its part of the registry and its host state. | no database |
| **web_service** | Next.js | The UI (section 8) and a backend-for-frontend that holds the web API key and the user's tokens in httpOnly cookies | nothing persistent |
| **nats** | NATS + JetStream | Streams `CMD`, `EVT`, `DLQ`; KV `registry`, `hosts`, `cancels`; core request/reply and live subjects | streams, KV |
| **postgres** | Postgres | One instance, one database and owner role per service | — |
| **object store** | S3 API (SeaweedFS locally, S3 in the cloud) | Every take, upload, cached result and export, under `u/<owner_id>/` | bucket |

`helmgo/` is the shared Go framework, copied from the reference, not a
service. `cli/` is `helm`, an API client.

```
 browser ─▶ kong ─▶ web_service (BFF) ─▶ kong ─┬▶ auth_service ──┐
                                               ├▶ studio_service ─┼─▶ postgres
                                               └▶ files_service   │
                          studio ◀──▶ NATS JetStream ◀──▶ pipelines_service
                                          ▲
                                          └──▶ runner_service (mlx)    ┐ native on a Mac,
                                          └──▶ runner_service (torch)  │ containers on CUDA
                                          └──▶ runner_service (native) ┘ (native: Mac only)
        studio, files, pipelines, runners ──▶ object store (S3 API)
        runners ──▶ model weights on the host's own disk
```

### 17.2 How work flows

**Long work** — the reference's pattern:
outbox → `CMD` → worker → `EVT` → projector → event stream.

1. The UI posts to studio_service (through the BFF and Kong). Studio writes the
   rows and an outbox row in one transaction and answers `202` with the
   updated slice.
2. The relay publishes the command. A run goes out only when the dispatcher
   releases it (section 17.5).
3. The worker does the work, reads and writes media in the object store, and
   publishes events carrying a per-job sequence number.
4. Studio's projector runs three durable consumers on `EVT` (`evt.run.>`,
   `evt.pipeline.>`, `evt.media.>`), each with the reference's settings (one
   message at a time, in order). It runs as `System`, drops events whose owner
   does not own the session or whose attempt is not the run's current one,
   de-duplicates on `(job_id, seq)`, applies them, and appends to the
   session's event log.
5. The UI follows `GET …/events` (SSE, resumable from the last event id).

**Short calls** use core NATS request/reply with a timeout that becomes a
`503`: token validation (studio → auth), eviction before a hand-over
(studio → runner), creating a run for a pipeline step (pipelines → studio).
Schema resolution, admission and estimates need no call: studio computes them
from the registry, the packs' formulas, host state and measured history.

**Live progress** — sampler steps and previews are too frequent for the stored
log. Runners publish `live.<owner>.<session>` on core NATS, enveloped, type
`live.run.progress` (run, node, step, steps, optional preview). The SSE handler
subscribes only after loading the session as the caller, only to that caller's
own subject, and drops any message whose owner or session differs. Live frames
are sent with no event id, so resume stays on the stored sequence.
A live preview is the one exception to the claim check: a JPEG of at most
64 KB inline, at most two a second, never stored. When a take is done the
runner writes one small strip of step previews for the scrubber.

**Files** follow the claim check: rows and messages hold object keys, never
bytes or URLs. Studio signs `/files/<key>?exp=&sig=` links as the reference
does, only for keys read from rows the caller can see; a link is stable for a
day and the object is cached as immutable, because a key never changes
content. files_service verifies, then streams with range support.

**Uploads** bypass the BFF body. Studio creates the upload row and returns a
single-use upload token bound to a key under `u/<owner>/uploads/`, a size limit
and the allowed types. The browser sends the file in parts to files_service
through Kong; files_service streams them into the object store. Studio checks
size and type before the take exists. The object store is never exposed to
browsers.

### 17.3 Users and isolation

- Two credentials, as in the reference: an **API key** identifies the calling
  application (Kong checks it), a **JWT** identifies the user (studio validates
  it over NATS and caches the answer until it expires or is revoked).
- The browser never holds a key; the BFF does. `helm` sends its own API key
  and the token of the user signed in with `helm login`.
- **Root admin**, as in the reference: one account, named by `ADMIN_EMAIL` and
  `ADMIN_PASSWORD` in the server's `.env`. On every start auth_service creates
  it or syncs it to those values (enabled, that password, the `admin` role)
  and removes `admin` from every other account. The API refuses to grant
  `admin` (`422`) and refuses any change to the root account (`409`); it is
  changed only by editing `.env` and restarting.
- **Accounts**: there is no public sign-up. The root admin creates every
  account on the Admin screen (`POST /auth/admin/users`: email, name,
  temporary password, roles `user` and `dev`), and can change roles, reset a
  password, and disable or enable an account
  (`/auth/admin/users/{id}/{roles,password,disabled}`). Disabling revokes the
  account's refresh tokens at once. API keys for calling applications are
  made with the `auth apikey <client>` command and listed on the same screen.
- Every row in the `studio` database has `owner_id`, enforced by row-level
  security through `WithUser`. The relay, projector, dispatcher and the
  `studio.runs.create` handler run as `System` and check the owner themselves.
  The `pipelines` database is reached only by messages that carry `owner_id`;
  it has no row-level security.
- Every object key is under `u/<owner_id>/` and every envelope carries
  `owner_id`; every incoming key is checked against it. Another user's id is
  a `404`.
- The gallery, sessions, library items, pipelines, memory and sequences are
  per user. Sharing between users is not in v1.
- **Nothing leaks through shared machinery.** Cache keys include the owner, so
  one user's cached embeddings or latents are never reused for another. Host
  state shown to a user is redacted: another user's run appears as "busy",
  another user's LoRA as a size.
- Models are shared (section 9.2); the GPUs are shared fairly (section 17.5).

### 17.4 Data ownership

| Database | Tables (main ones) |
|---|---|
| `auth` | users, credentials, roles, refresh tokens, clients, API keys |
| `studio` | sessions, workflows, takes (with `inputs[]` lineage), uploads, library items, pipelines, batches and batch items, sequences and revisions, runs (with `attempt`), measured node times and peaks, the per-session event log, outbox, projector de-dupe |
| `pipelines` | inbox, per-job sequence and cancel flag, LangGraph checkpoints and store |

studio_service owns every id: it creates the run row and assigns the take keys
before the command is sent, so workers report by id and write straight to
their final keys. Migrations are embedded SQL files applied at start (goose in
Go; plain SQL under an advisory lock in Python). Runners have no database:
what they measure travels in events, and what they cache lives in the object
store.

### 17.5 The dispatcher — sharing GPUs fairly

A JetStream work queue cannot express "one heavy run per GPU host, interactive
before batch, prefer the models already loaded, round-robin between users", so
the queue lives in studio's `runs` table and a dispatcher in studio_service
decides what goes out.

- A run is `queued` until the dispatcher writes its command to the outbox for
  one host and env. Each runner has a durable consumer on its own subject with
  one run in flight. Runs created for pipeline steps join the same queue, with
  the batch's owner and lane.
- **One run at a time per host**, across envs, because they share its memory.
- **Order**: interactive runs before batch items; within a lane, round-robin
  across users; among a user's ready items, one that uses what is loaded.
- **Placement**: a host is eligible if it has the run's env, every weight file
  the run uses, the pack code the run was validated with, and enough memory in
  total. Among eligible hosts, prefer one with the run's components resident,
  then the one idle longest. No eligible host fails the run at once, with the
  reason.
- **Host state**: each runner keeps its own key in the `hosts` KV — env,
  device, total and free memory, resident components and pins, current run —
  refreshed while it is alive and making progress.
- **Hand-over**: before sending a run to the other env on the same host, the
  dispatcher asks the first runner to evict and waits for its measured answer.
- **Finishing**: a runner publishes the run's terminal event and waits for the
  publish to be acknowledged before it acks the command. The dispatcher frees
  the host only when the projector commits a terminal status for that run and
  attempt, or when the lease is lost.
- **Retries have one owner.** A run command is delivered once. If a runner's
  host key is not refreshed for 30 seconds, or the run makes no progress within
  its stall limit (section 6), the lease is lost: the dispatcher removes the
  old command, increments the run's attempt and queues it again, once. A
  second loss fails the run with the reason.
- **Cancel**: studio marks the run `cancelling` in the same transaction as the
  request. A run not yet dispatched becomes `cancelled` at once and is never
  sent. Otherwise studio writes the run's id to the `cancels` KV; the runner
  checks it before starting and at every cancel point. The run ends
  `cancelled`, keeping takes already stored.

### 17.6 Messaging

| Kind | Subjects | Producer → consumer |
|---|---|---|
| Commands, stream `CMD` (work queue) | `cmd.run.<host>.<env>`, `cmd.media.<host>`, `cmd.pipeline.submit`, `cmd.pipeline.resume` | studio outbox relay → runner / pipelines |
| Events, stream `EVT` (7 days) | `evt.run.<session>.{status,step,take,usage}`, `evt.media.<session>.*`, `evt.pipeline.<session>.{status,item,decision}` | workers → studio's projector |
| | `evt.studio.<session>.run_finished` | studio → pipelines. Published after any terminal run status, including ones studio decides itself (cancelled, lease lost, no eligible host), so a parked thread always wakes. |
| Dead letters, stream `DLQ` (30 days) | `dlq.cmd.>` | workers |
| Request/reply (core) | `auth.validate.token`, `auth.validate.role` | studio → auth |
| | `runner.<host>.<env>.evict` | studio → runner |
| | `pipelines.state.get` | studio → pipelines: one row's state at a checkpoint, for its owner (section 16.8) |
| | `studio.runs.create` | pipelines → studio. Idempotent on batch, row, node, loop and branch; the owner and lane are taken from the batch, never from the request. |
| Revocation (core) | `evt.auth.revoked` | auth → every Go service's token cache |
| Broadcast (core) | `pipelines.cancel` | studio → pipelines: stops remote calls and parks the batch |
| Live (core, not stored) | `live.<owner>.<session>` | runner → studio's SSE handler |
| KV | `registry` (keys `<host>.<env>.<kind>`: models present, node schemas with pack hash, templates, looks), `hosts` (key `<host>.<env>`), `cancels` | each runner writes only its own keys; studio and pipelines read; studio writes `cancels` |

Rules carried over from the reference:

- A closed envelope (`type, version, id, job_id, owner_id, occurred_at, trace,
  data`). The envelope's `type` is the kind of message, never the routing
  subject: `cmd.run.execute` travels on `cmd.run.<host>.<env>`. Host names are
  lower-case letters and digits.
- `Nats-Msg-Id` of `job:attempt` for commands and `job:seq` for events;
  durable pull consumers created in code; an `in_progress` heartbeat under a
  short `ack_wait`; inbox de-duplication; errors classed as transient, final
  or invalid, with dead letters.
- One NATS user per service, and one per GPU host for runners, each allowed
  only its own subjects and KV keys; every new subject is a config change.
  Pipeline commands keep the reference's redelivery settings; run commands do
  not (section 17.5).

### 17.7 Where things run

| | Apple Silicon | Linux + CUDA |
|---|---|---|
| kong, auth, studio, files, web, pipelines, postgres, nats, object store | Docker Compose | Docker Compose |
| runner_service (`mlx`, `torch`, `native`) | **Native on the host** (Docker cannot reach the Apple GPU), started by `make runners` or a launch agent | Container with the NVIDIA runtime, or native |
| Model weights | The host's own disk, never inside an image (section 9.2) | The host's disk, bind-mounted read-only into the runner |

- The `native` env (M7 pack 3) loads no model in Python: its packs run a
  native binary as a child process (MiniMax H3 video: the vendored `h3`,
  `runner_service/vendor/h3c`, built by `make h3`), so its process has no torch
  or MLX, its memory is measured with the child's, and nothing stays resident
  after a run. On a Mac it is the third runner (`make runner-native`, health
  port 8093, NATS user `runner_mac_native`) sharing the host's one budget.
- A runner connects only to NATS and the object store, never to Postgres. On
  the stack's own machine those two ports are published on `127.0.0.1`;
  another GPU host connects over TLS with its own NATS and S3 credentials: a
  NATS account of its own (connection, subscription and message-size caps;
  HELM exports it exactly its subjects) and an S3 identity that reads under
  `u/`, writes only under the runner prefixes and never deletes.
- Per-service `.env`, secrets as files (`X_FILE`), a `health` subcommand for
  container health checks, one compose file per service for isolated
  development, and a separate compose project with fake providers for tests —
  all as in the reference.
- Every model provider has a deterministic **fake mode**, and a fake runner
  lets the whole stack run on a laptop with no GPU.

### 17.8 Contracts first

`contracts/` is the single source for everything that crosses a service
boundary: `openapi.yaml` (HTTP), `messages/*.schema.json` (NATS),
`registry.schema.json` (KV), and the small expression grammar used by `when`,
`requires` and memory formulas. Change the contract first, then the services.
Services never import each other's code. Go request and response bodies are
hand-written DTOs with `Validate()`; the web's TypeScript types are generated;
Python validates every message against the copied schemas. A black-box
contract suite through Kong is the gate for every milestone.

### 17.9 What differs from the reference, and why

| Reference | helmstudio-next | Why |
|---|---|---|
| `agents_service` does everything in Python | Split into `pipelines_service` (LangGraph, no GPU) and `runner_service` (models) | Runners must run natively on a Mac and hold tens of GB; orchestration should not live or die with them |
| Commands go straight from the outbox to workers | Runs wait for the dispatcher | GPUs are scarce and shared between users |
| Up to four deliveries of a command | One delivery of a run; the dispatcher retries once | A run costs minutes of GPU time, and two retry mechanisms could run it twice |
| Events only through the stored log | Stored log plus live subjects, with one subscription per open stream | Step progress and previews need sub-second delivery |
| Workers never call the domain service | pipelines creates runs over request/reply and parks threads | A pipeline step is a queued GPU run, not in-process work |
| Cancel by broadcast | Cancel through a KV key | A native runner may be reconnecting when the broadcast is sent |
| One writer of the registry | Each runner writes its own keys | Hosts differ in which models and packs they have |
| Remote image providers | In-process models with a residency manager | The product is local models |
| All files in the object store | Media in the object store, weights on the host disk | Weights are read straight into GPU memory |
| Workers have a database | Runners have none | A native runner should not reach Postgres |
