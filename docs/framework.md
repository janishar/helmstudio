# The helmstudio framework

A map of the repository: what the pieces are, why they sit where they do, and
how to build and run the thing. It is laid out the way rust-analyzer's
`architecture.md` is — bird's-eye view, entry points, a code map, then
cross-cutting concerns, with the deliberate constraints called out as
invariants — because that is the shape a stranger can read top to bottom and a
maintainer can jump into by heading.

**This document describes; it does not decide.** `docs/design/` is the contract
and `docs/decisions.md` is the record. If anything here disagrees with either,
this file is wrong and should be fixed — and if the *design* is what is wrong,
the rule in `CLAUDE.md` applies: raise it and stop, do not resolve it in code.
Section references like *01 §7* point at `docs/design/01-prd.md`, section 7;
`R21` is a numbered requirement in the PRD.

---

## 1 · Bird's-eye view

helmstudio is a **local launcher and platform for open-weight generative
models**. A *studio* is somebody else's repository — a video model, an image
model, a speech model, with its own server and its own UI. helmstudio installs
it, builds it, fetches its weights, supervises its processes, gives it storage,
a gallery, media handling and a timeline, and shows the whole shelf in one
window.

Two sentences carry the whole design:

- **Studios stay independent repos.** helmstudio never vendors, forks, patches
  or imports studio code. A studio must stay fully usable by someone who clones
  it and ignores helmstudio entirely.
- **The daemon is never a model runtime.** No inference, no tensors, no Metal,
  no Python in-process. Its job is lifecycle and platform: fetch, build,
  download, supervise, store, serve. The moment a feature needs to understand a
  model format, it belongs in a studio.

``` mermaid
flowchart TB
    user(["Someone with a Mac"])

    subgraph helm ["helmstudio"]
        shelf["Launcher UI<br/>web/ — served on loopback"]
        daemon["helmstudio daemon<br/>cmd/helmstudio"]
        cli["helm CLI<br/>cmd/helm"]
    end

    subgraph studios ["Studios — independent repos, never imported"]
        h3["h3 studio<br/>Go + Metal C"]
        ltx["ltx studio<br/>Python + MLX"]
        auk["AuK studio<br/>Python + PyTorch"]
    end

    subgraph owned ["What the daemon owns"]
        db[("helm.db<br/>SQLite, one file")]
        blobs["assets/blobs<br/>content-addressed bytes"]
        models["models/<br/>weights, managed or linked"]
        logs["logs/<br/>build and run logs"]
    end

    net(["GitHub · Hugging Face<br/>the only outbound calls"])

    user --> shelf --> daemon
    user --> cli
    daemon -- "spawn, health-gate, stop" --> studios
    studios -- "platform API over loopback<br/>helm-runtime-sdk" --> daemon
    daemon --> db
    daemon --> blobs
    daemon --> models
    daemon --> logs
    daemon -. "clone, download" .-> net
```

**Nothing leaves the machine.** The only outbound calls are the ones a user
action implies — GitHub, Hugging Face, and whatever a studio's own build
fetches. No telemetry, no analytics, no account. A reviewer can grep the daemon
for every hostname it contacts.

---

## 2 · The four things that make it work

Everything in the code map is downstream of these.

| Idea | What it is | Where it lives |
|---|---|---|
| **The manifest** | One YAML file in the studio's own repo describing how to build it, what weights it needs, what processes to run and what platform services it wants. Four genuinely different studios fit one schema with no special case in Go. | `schema/manifest.json`, `internal/manifest`, `studios/*.yaml` |
| **Supervision** | Process groups, assigned ports, health gates with real timeouts, teardown in reverse dependency order, and re-adoption of survivors after the daemon is killed. | `internal/supervisor` |
| **The platform API** | One HTTP contract giving every studio state, records, assets, a gallery with provenance, jobs, a timeline and events — so no studio builds a take list, a thumbnailer or a store again. | `api/openapi.yaml`, `internal/api/studioapi` |
| **Two providers, one contract** | The same API is served by the daemon *and* by an embedded in-process provider, so a studio developed standalone and the same studio running hosted are the same code path. A conformance suite runs against both. | `packages/helm-runtime-sdk/go/embedded`, `test/conformance` |

---

## 3 · Entry points

Start here, in this order, depending on what you are trying to understand.

| To understand… | Read |
|---|---|
| What is being built and why | `docs/design/01-prd.md` |
| The daemon's startup, in one function | [`cmd/helmstudio/main.go`](../cmd/helmstudio/main.go) — `run()` is the whole boot sequence in about a hundred lines |
| The CLI | [`cmd/helm/main.go`](../cmd/helm/main.go), then `validate.go` and `dev.go` |
| The manifest contract | [`schema/manifest.json`](../schema/manifest.json), then `internal/manifest/rules.go` for the checks a JSON Schema cannot express |
| The API contract | [`api/openapi.yaml`](../api/openapi.yaml) — the header comment states every convention that is not repeated per operation |
| What a real studio declares | [`studios/h3-studio.yaml`](../studios/h3-studio.yaml) |
| The launcher's screens | [`web/app.js`](../web/app.js) — the shell, the router and the one poll; each screen is a function of a context beside it |
| A prebuilt component | [`packages/helm-ui-sdk/src/terminal.js`](../packages/helm-ui-sdk/src/terminal.js) — the clearest example of receiving a client rather than building one |
| What must pass before a commit | [`Makefile`](../Makefile), then `docs/agents/gate.md` |
| Why something is the way it is | `docs/decisions.md` |

---

## 4 · Code map

```
cmd/helmstudio     the daemon — one process, loopback only
cmd/helm           the CLI — validate, dev, and more to come
internal/          daemon internals; nothing here is importable by a studio
packages/          what studios actually depend on
schema/            manifest.json — the manifest contract
api/               openapi.yaml — the platform API contract, and its generator
studios/           registry entries, one per studio
web/               the launcher's screens and its generated client
site/              the documentation site and its generator — its own module
test/              conformance, visual, media, packaging; fixture studios
docs/              design, decisions, plan, agent briefs, this file
```

### cmd/

**`cmd/helmstudio`** is the daemon. One process, listening on `127.0.0.1:8700`
by default. Its `run()` does, in order: resolve the five directory roots and
create them, open `helm.db` and migrate it, build the studio-API platform,
sweep installs a dead daemon left mid-flight, re-adopt surviving process
groups, revoke tokens and clear stages belonging to groups that are gone, stop
any export left half-rendered, then serve. On `SIGINT`/`SIGTERM` it shuts the
studios down; only `kill -9` leaves children behind, and those are what
re-adoption is for.

**`cmd/helm`** is the studio author's CLI. Today it has `validate` — schema
plus semantic rules, with line numbers resolved against the YAML node; with
`-criteria` it also scores the certification criteria a manifest alone can
answer, and with `-theme` it lints a studio's stylesheets — and `dev`, which is
the daemon restricted to one studio and a local path, over `./.helm` instead of
the user's data, and never downloading anything. 01 §6, §10 and §11 also
specify `adopt`, `doctor --studio`, `test` and `studio init`; no milestone
builds those yet (see §13).

### internal/

| Package | What it does | Lines |
|---|---|---|
| `platform` | Every OS-specific decision: the five directory roots, the Keychain, the exclusive file lock, process groups and signals, disk free, hardlink counts. Darwin, Linux and an explicit refusal elsewhere, chosen by build constraint. | ~1.8k |
| `store` | Opens `helm.db` with its pragmas on every connection, enforces a single writer, and runs forward-only migrations. Seven schema versions so far, each a `.sql` file. | ~1.2k |
| `manifest` | Loads and validates a manifest: JSON Schema draft 2020-12, plus the rules the schema cannot express, each error carrying a line number. Also the editor's edits, which keep comments and key order, and the certification criteria. | ~2.9k |
| `library` | Every studio this machine knows, resolved from three sources — a local manifest, the studio's own repository, a registry entry's inline manifest — first match by existence, not validity, so a broken override is shown broken instead of falling through to the registry's copy. | ~1.8k |
| `approval` | What a studio would run — every command, not only the build steps — and the digest that authorises it. Nothing installs, retries or launches without a current approval. | ~0.8k |
| `supervisor` | Manifest → resolved plan → running group: ports assigned, placeholders substituted, dependency order fixed, health probes, teardown, re-adoption, the heavy-group rule, the `busy` probe, log tailing and SSE, uv environments. | ~6.4k |
| `install` | Clone with submodules at a pinned ref, ordered build steps with resume-from-failed-step, cancellation, the startup sweep, uninstall. | ~3.7k |
| `weights` | The Hugging Face downloader — resumable per file, re-resolving expired signed URLs, checking size and etag and free disk — plus linked directories and reclaim. | ~3.2k |
| `api` | The launcher's own HTTP surface: studios, processes, logs, install and weights, jobs, models, theme, the library and its manifests, approvals, the Hugging Face token, and the static SDK bundles. | ~4.0k |
| `api/studioapi` | The platform API itself: kv, sessions, records and their filter language, assets, gallery and lineage, handoff and inbox, jobs, events, tokens, quotas, timeline and export. One implementation, served by both providers. | ~8.3k |
| `media` | The media engine: hashing, hardlink adoption, probing, thumbnails, posters, waveforms. | ~0.7k |
| `timeline` | The timeline document — validation, frame and sample snapping, the render graph, the export plan. | ~1.7k |
| `export` | Running ffmpeg: probing clips, building the command, streaming progress. | ~0.5k |
| `theme` / `css` / `themelint` | The launcher theme and its event stream, helm-css serving, and the stylesheet linter behind `helm validate -theme`. | ~1.3k |
| `chrome` | Drives an installed Chrome over its DevTools pipe, for the visual-regression suite. Standard library only. | ~0.3k |

> **Invariant — one place decides what an OS is.** Only `internal/platform` may
> branch on the operating system, name an OS-suffixed file, or look up a home
> directory. Every path in the system comes from its `Dirs` helper. `make
> boundaries` fails the gate on `runtime.GOOS`, an OS build constraint, an
> OS-suffixed filename or a `$HOME` lookup anywhere else in the tree.

> **Invariant — studios never open the database.** A studio has no SQL, no
> passthrough and no file handle on `helm.db`. It calls the API; the daemon
> holds one write connection and a read pool under WAL. This is what makes one
> backup, one migration path and one crash-safe write implementation serve
> every studio instead of one per author.

> **Invariant — no Go code names a studio.** A special case in the daemon for a
> specific studio means the schema is wrong. Three very different studios —
> Go+Metal, Python+MLX, Python+PyTorch — run through one code path.

### packages/

What studios depend on, with a strict one-way dependency:

``` mermaid
flowchart LR
    css["helm-css<br/>tokens, base, layout, components<br/>depends on nothing — it is text"]
    rt["helm-runtime-sdk<br/>go · python · node<br/>knows the API"]
    ui["helm-ui-sdk<br/>helm-gallery · helm-player<br/>helm-terminal · helm-timeline<br/>knows both"]

    api["api/openapi.yaml"] -.->|"generates"| rt
    ui --> css
    ui --> rt
```

> **Invariant — the arrow never reverses.** The runtime SDK must never import a
> component, and `helm-css` must never assume a component's markup exists. The
> day one of those arrows reverses, the three packages have become one package
> with extra steps. `helm-ui-sdk` never constructs a URL and never sets a
> header: it receives a runtime client, so an API change regenerates one
> package and leaves the components untouched.

- **`helm-css`** — four layers plus a bundle and `tokens.json`, built by `make
  css`. Every class is prefixed `helm-`, nothing is `!important`, specificity
  stays at one class, so a studio overrides by writing one rule instead of
  fighting. IBM Plex ships with it under the OFL.
- **`helm-runtime-sdk`** — Go, Python and Node, all generated from the one
  OpenAPI document so the three cannot drift, with identical method names in
  each. Provider selection is automatic: remote when `HELM_API` is set. The Go
  client falls back to the **embedded provider** — a separate module, so a
  studio that only runs hosted never pulls SQLite into its dependency tree.
  Python and Node fail as `Unavailable` and name `helm dev` (M5 Q14).
- **`helm-ui-sdk`** — `helm-terminal`, `helm-gallery`, `helm-player` and
  `helm-timeline`, as custom elements in Shadow DOM: helm-css's classes cannot
  reach inside them and its custom properties can, which is exactly how they
  are themed. The package **imports nothing at all** — a component is handed a
  client and calls the few methods 04 §5 lists, and the only thing it knows
  about a failure is the `kind` the runtime SDK puts on it, read off the error
  rather than imported from it. `helm-player` ships without its filmstrip,
  waveform and h264 proxy, which need ffmpeg; they render `Unsupported` with a
  reason rather than being absent. `helm-timeline` edits a framework-owned
  sequence — drag and trim on the daemon's grid, dissolve and gain, undo
  through revisions, export with progress and cancel — and previews by playing
  each clip's own bytes in the browser, with no ffmpeg (M8b). The launcher
  uses the same `helm-terminal` for its install and process logs.

### The four adoption levels

Levels are a choice, not a ladder. A studio may deliberately sit at level 2
forever, and that is a success, not a gap.

| Level | Takes | Gets |
|---|---|---|
| 0 | nothing | Installs, launches, runs. No gallery, no shared cache. *This must stay viable or helmstudio is not a launcher.* |
| 1 | `helm-css` | Looks like it belongs to the family, keeps its own identity hue. |
| 2 | `+ helm-runtime-sdk` | Assets, gallery, records, jobs — with the studio's own UI. **All four launch studios land here**, because none wants a front-end toolchain. |
| 3 | `+ helm-ui-sdk` | Prebuilt gallery, player, terminal and timeline editor. |

---

## 5 · The two contracts, and what is generated from them

Two files are the contract. Neither may be edited to make an implementation
pass; they change first, deliberately, with a decision-log entry.

``` mermaid
flowchart TB
    schema["schema/manifest.json<br/>the manifest contract"]
    openapi["api/openapi.yaml<br/>the platform API contract"]

    schema --> mf["internal/manifest<br/>schema + semantic rules + line numbers"]
    mf --> validate["helm validate"]
    mf --> sup["internal/supervisor"]

    openapi --> gen["api/gen<br/>one generator, stdlib output"]
    gen --> gosdk["helm-runtime-sdk/go<br/>zz_types.go · zz_client.go"]
    gen --> py["helm-runtime-sdk/python<br/>_generated.py"]
    gen --> node["helm-runtime-sdk/node<br/>generated.js"]
    gen --> router["internal/api/studioapi<br/>zz_server.go — the daemon's router"]
    gen --> launcher["web/launcher.js<br/>the launcher's own client"]

    gen -.->|"make drift regenerates into a scratch tree<br/>and fails on any difference"| gate(["make gate"])
```

> **Invariant — generated code is committed, and never hand-edited.** The
> checked-in copies exist so `make drift` has something to compare against. A
> hand edit, or a contract change nobody regenerated, fails the gate whether or
> not the files are committed yet. Change the generator or the document it
> reads, then `make generate`.

**Two clients, one generator.** Only `studio-api` operations reach the runtime
SDK, because a studio has no business installing or launching anything (M4 Q1).
The `launcher` operations go to `web/launcher.js`, served with the launcher's
own page and never under `/sdk/`. That is what lets the launcher's install and
process screens use the same `helm-terminal` a studio uses: the component is
handed a client, and both clients are generated (M6 Q12). The launcher's
carries operations only — its transport and its error shape are the runtime
SDK's, so there is one mapping of status to kind rather than two that drift.

**The manifest's substitutions are a closed set.** A process command may
contain `{port}`, `{ports.<name>}`, `{models.<name>}`, `{models.selected}`,
`{root}`, `{data}` and `{venv}` — and nothing else. A typo like `{prot}` is a
validation error with a line number, not a literal brace that surfaces four
minutes into a build.

---

## 6 · Lifecycles

### 6.1 · Install state

Install state and process state are **separate machines and never merged**
(01 §5). A state with no chip in the UI is a bug in the design, not licence to
invent a synonym.

``` mermaid
stateDiagram-v2
    [*] --> listed
    listed --> cloning: install
    cloning --> cloned
    cloned --> building
    building --> built: each build[] step, in order, in its own cwd
    built --> fetching_weights
    fetching_weights --> ready
    fetching_weights --> auth_required: gated repo, 401 or 403
    auth_required --> fetching_weights: token added
    ready --> update_available: upstream ref moved
    update_available --> cloning: update

    cloning --> failed_clone
    building --> failed_build
    fetching_weights --> failed_weights
    failed_build --> building: retry resumes at the first step that has not succeeded
    failed_clone --> cloning: retry
    failed_weights --> fetching_weights: retry

    ready --> removing: uninstall
    removing --> listed
```

**Work already done is kept.** A failure records the phase, the step index, the
exit code and a log reference; retry resumes from the first step that has not
succeeded rather than starting over. `auth_required` is not a failure — nothing
broke, a token is needed.

### 6.2 · Process state

``` mermaid
stateDiagram-v2
    [*] --> queued: waiting for a heavy slot or a dependency
    queued --> starting: spawned as leader of its own process group
    starting --> running: health probe passes
    starting --> failed: health timed out, or it exited before ready
    running --> stopping: stop, in reverse dependency order
    running --> failed: crashed
    stopping --> exited: SIGTERM to the group, SIGKILL after grace
    failed --> queued: Restart — a user action, never automatic for a main process
```

> **Invariant — a failed health check never restarts a `main` process.** An
> eight-minute generation must survive a slow probe. A `main` process that
> exits unexpectedly fails the group, surfaces the last 200 log lines and
> offers Restart; only a `sidecar` with `restart: on-failure` retries, with
> backoff, at most three times in ten minutes.

> **Invariant — re-adoption checks all three of pid, start time and pgid.** A
> recycled pid must never be signalled. This is checked on daemon restart, and
> again by the sweep that stops a build step which outlived a killed daemon.

### 6.3 · A launch, end to end

``` mermaid
sequenceDiagram
    participant U as User
    participant D as daemon
    participant S as supervisor
    participant P as studio process
    participant A as studio API

    U->>D: POST /api/v1/studios/{id}:launch
    D->>S: plan the launch
    S->>S: assign ports, substitute placeholders,<br/>fix dependency order
    S->>S: check the heavy rule — one heavy group at a time
    S->>D: mint a per-launch token, scoped to<br/>this studio's declared capabilities
    S->>S: write every row in one transaction
    S->>P: spawn as process-group leader<br/>HELM_API · HELM_TOKEN · HELM_STUDIO_ID · HELM_STAGE_DIR<br/>HELM_THEME · HELM_ACCENT_DARK/LIGHT · HELM_SDK_BASE
    Note over S,P: a studio inherits nothing else from the daemon's environment
    loop every interval_s, until timeout_s
        S->>P: health probe — path, tcp or exec
    end
    P-->>S: healthy
    S-->>U: running, with elapsed against the budget
    P->>A: POST /assets:adopt — hardlink the file it just wrote
    A-->>P: {id, sha256, thumb, duration_s, ...}
    P->>A: POST /gallery/items — params and inputs[]
    U->>D: POST /api/v1/studios/{id}:stop
    D->>P: SIGTERM to the group, SIGKILL after grace
    D->>A: revoke the token, clear the stage directory
```

---

## 7 · Storage: rows here, bytes there

**SQLite for metadata, the filesystem for bytes.** There is no server, no port,
no second daemon — SQLite is a library linked into the binary and the store is
one file the user can copy. Media bytes are never a BLOB.

``` mermaid
flowchart LR
    studio["a studio writes<br/>its output file"]
    stage["stage/&lt;studio&gt;/&lt;group_run&gt;/<br/>per-launch scratch"]
    blob["assets/blobs/7c/1f/7c1fa9….mp4<br/>named by sha256, immutable"]
    lib["library/h3-studio/2026-09/<br/>cafe-window-drift.mp4"]
    derived["&lt;cache&gt;/derived/7c1fa9…/<br/>thumb · poster · waveform"]
    db[("helm.db<br/>assets · items · item_inputs<br/>sessions · records · kv · timelines")]

    studio --> stage
    stage -->|"POST /assets:adopt<br/>hardlink, then unlink the stage entry"| blob
    blob -->|"os.Link — same inode, no copy"| lib
    blob -->|"generated once by the daemon,<br/>under the cache root — purgeable"| derived
    blob -.->|"one row, path relative to the assets root"| db
```

**Two paths, one copy.** The blob path is the content hash, which makes writes
idempotent, dedup a `UNIQUE` constraint rather than an algorithm, and
corruption detectable. The library path is what a person browses in Finder,
organised by studio and month with the title they gave it. Same inode, so the
readable tree costs nothing.

**Adoption never copies.** An 18 MB take and a 2 GB render both cost two inode
operations. If the hash already exists, the link is simply dropped — the second
studio to produce identical bytes stores nothing.

**Derived files sit under the cache root, deliberately.** Thumbnails, posters,
waveforms and proxies are regenerable, so they live in a root that can be moved
where the OS purges it and a backup skips it — which is exactly wrong for the
blobs beside them. That is most of the reason the roots are separate in the
first place.

### The five roots

Everything lives in one tree, `~/.helmstudio`, on every platform — the shape
`helm dev` keeps in a studio's `./.helm`. No path is hardcoded: each root
resolves separately, so any one can be moved on its own — models to an external
drive, the cache out of a backup.

| Root | Holds | Default | Override |
|---|---|---|---|
| **data** | `helm.db`, studio checkouts, `assets/blobs`, `stage/` | `~/.helmstudio` | `HELMSTUDIO_DATA_DIR` |
| **cache** | derived thumbs, proxies, fetched manifests — regenerable only | `<data>/cache` | `HELMSTUDIO_CACHE_DIR` |
| **logs** | build and run logs | `<data>/logs` | `HELMSTUDIO_LOGS_DIR` |
| **library** | the human-readable media tree | `<data>/library` | `HELMSTUDIO_LIBRARY_DIR` |
| **models** | weights, often on an external drive | `<data>/models` | `HELMSTUDIO_MODELS_DIR` |

Resolution order per root: the specific variable, then `HELMSTUDIO_HOME`, then
a stored setting (library and models only), then the default in the data root.
`HELMSTUDIO_HOME=<dir>` is the whole tree at `<dir>`, which is what lets a test,
a CI run and `helm dev` each work in an isolated tree without ever touching the
user's.

> **Invariant — the library can always be moved on its own.** A person has to
> be able to find what they made, so the library is settable and lives wherever
> they browse.

> **Invariant — a linked model directory is read-only, forever.** helmstudio
> never writes into a directory the user pointed at — no partial files, no
> repair, no permission changes. Reclaim never follows a symlink; deleting a
> linked artifact removes the link and nothing else, and the action is labelled
> Unlink rather than Delete.

---

## 8 · The studio API and the capability model

At spawn the daemon injects the environment every SDK reads. The token is
minted per launch, scoped to that studio's id and its declared capabilities,
and dies when the process group stops — so a studio cannot read another
studio's namespace, and a token scraped from a log is useless later.

| Surface | Endpoints |
|---|---|
| **State** | `/kv/{ns}/{key}` with `ETag`/`If-Match`, `/sessions` — create, rename, duplicate, activate, soft-delete |
| **Records** | `/records/{collection}` — a per-studio document store with a closed filter language: `eq ne lt lte gt gte in contains exists`, at most eight clauses, always parameterised |
| **Assets** | `POST /assets:adopt` (hardlink, no HTTP body), `POST /assets` (bytes), `GET /assets/{id}` with `Range`, `/thumb`, `/lineage` |
| **Gallery** | `POST /gallery/items` with params and an `inputs[]` provenance list; query by kind, tag, session, date, text |
| **Cross-studio** | `POST /handoff`, `GET /inbox`, `POST /inbox/{id}:consume` |
| **Work** | `/jobs` with progress, cancellation and a log file — so no studio builds a second queue |
| **Timeline** | `/timeline`, `:append`, `:open`, `:plan`, `:export` and its revisions |
| **Live** | `GET /events` — SSE: theme changed, job progress, inbox arrived, shutting down |
| **Self** | `GET /me` — studio id, capabilities, quota, paths, daemon version |

> **Invariant — no SQL passthrough from a studio, ever.** The filter language
> is closed and parameterised. This is not a performance decision.

> **Invariant — the default is the safest one.** A studio with no
> `capabilities` in its manifest gets **no token at all**. Every studio
> endpoint refuses a request without one.

**Capabilities are declared in the manifest and shown before install**, as
plain sentences on an approval screen, alongside every build command verbatim,
the weights and their sizes, and the declared network hosts. `kv` covers `/kv`
and `/sessions`; `records`, `assets`, `gallery` and `jobs` cover their own
surfaces; `gallery.read_all` is needed to see another studio's items;
`handoff.send` to push into another studio's inbox; `kv.shared` for the shared
namespace. `/me` and `/events` need only a token.

**A studio's page never holds a token.** The runtime SDK ships a same-origin
proxy the studio mounts at `/helm/`, which forwards platform-API paths with the
Bearer token added and forwards nothing else. So `<img
src="/helm/api/v1/assets/{id}">` works from the browser with no credential ever
reaching it.

---

## 9 · Two providers, one contract

This is the claim that keeps studios independent, so it is proved rather than
asserted.

``` mermaid
flowchart TB
    suite["test/conformance<br/>one suite, written from the contract"]

    subgraph d ["Provider A — hosted"]
        daemon["the daemon over HTTP<br/>cmd/helmstudio"]
    end
    subgraph e ["Provider B — standalone"]
        emb["the embedded provider, in process<br/>helm-runtime-sdk/go/embedded"]
    end

    svc["internal/api/studioapi<br/>ONE implementation"]
    daemon --> svc
    emb --> svc
    suite --> daemon
    suite --> emb

    dev["helm dev<br/>the daemon restricted to one studio,<br/>over ./.helm"]
    dev --> svc
```

A studio author writes one code path: `helm.FromEnv()` returns the remote
client when `HELM_API` is set and the embedded provider otherwise. Running
`helm dev` in the studio's own checkout gives the real HTTP surface over
`./.helm` — and the embedded provider enforces the studio's declared
capabilities exactly as the daemon does, answering the same `403
capability_required`. So a studio that forgot to declare `assets` is refused in
development rather than working standalone and failing once hosted, which is a
bug the M4 review actually caught. (R57's `--fixtures` and `--fail=` are
deliberately not in M4 — `docs/decisions.md`, M4 Q26.)

> **Invariant — the conformance suite is written from the contract, not from
> the implementation.** The question asked of every test: if someone introduced
> a deliberate bug here, would this fail? For this suite the answer was checked
> by planting bugs in both providers.

---

## 10 · Timeline and export

Sequencing is inherently cross-studio — a sequence made from an h3 clip, an ltx
clip, an iris still and an AuK voice line is nobody's studio document — so the
framework owns it, and it must survive uninstalling the studio that made half
of it.

A timeline is a **document**: a target (resolution, fps, sample rate) and
tracks of clips referencing assets with in and out points. It is
non-destructive; sources are never modified or copied. Times are stored in
seconds already snapped to the target's frames — or to its samples on an audio
track — so what is stored is what will be rendered.

``` mermaid
flowchart TB
    seq["a sequence<br/>one video track, contiguous from 0<br/>up to eight audio tracks"]
    probe["probe every clip at export time —<br/>never trust what a studio declared"]
    check{"every clip matches the target<br/>and each other on codec, profile, level,<br/>size, aspect, field order, pixel format,<br/>time base, frame rate, colour tags,<br/>parameter sets — and is used whole?"}
    copy["video stream copy<br/>picture bytes untouched"]
    conform["conform — re-encode through<br/>h264_videotoolbox"]
    audio["audio is ALWAYS re-encoded"]
    job["an export is a Job:<br/>progress, cancellation, a log file"]
    item["the export becomes a gallery item,<br/>one clip input per distinct asset —<br/>lineage survives the edit"]

    seq --> probe --> check
    check -->|yes| copy
    check -->|no| conform
    copy --> audio
    conform --> audio
    audio --> job --> item
```

> **Invariant — the chip reads "video stream copy", not "no re-encode".** A
> copied AAC stream carries its own priming and padding into every cut: two
> real h3 takes joined that way put the second take's sound 81 ms behind its
> picture. The sound is always re-encoded, and the UI says what actually
> happened.

**ffmpeg** is an LGPL build with videotoolbox. helmstudio names only
`h264_videotoolbox`, ffmpeg's own AAC, stream copy and the uncompressed pair
its probes and posters read through — and a test holds that line. Until the Mac
app bundles a pinned build, ffmpeg is found through `HELM_FFMPEG` or the
`PATH`; without one, the operations that need it answer `501` and everything
else still works.

---

## 11 · Setup and execution

### 11.1 · Prerequisites

| Need | Why | Note |
|---|---|---|
| Go, matching `go.mod` | Builds everything; the `go` line is `1.27.1` | The whole daemon is Go |
| `git` | Cloning studios, with submodules | |
| A C toolchain, `make` | Some studios build native engines | Per-studio, declared in `requires.tools` |
| `uv` on `PATH` | Per-studio Python environments | Only for studios declaring `python:`; install is refused before the clone when it is missing |
| `ffmpeg` / `ffprobe` | Probes, posters, timeline export | Found via `HELM_FFMPEG`/`HELM_FFPROBE` or `PATH`; without one those operations answer `501` |
| Python 3, Node and `curl` | The Python and Node clients' smoke tests, the packaging checks, and the site's samples | Tests only; `HELM_ALLOW_MISSING_CLIENTS=1` to skip |
| Chrome | Visual-regression tests only | `HELM_CHROME` names it; `HELM_ALLOW_MISSING_BROWSER=1` to skip |

**No cgo.** SQLite is `modernc.org/sqlite`, a pure-Go translation, so the
daemon cross-compiles and the gate can type-check the tree as Linux without a
toolchain per target. Four external modules are required directly —
a JSON Schema validator, `golang.org/x/sys`, `yaml.v3` and SQLite — and the
list is meant to stay that short: Go here is stdlib-first, and **a new dependency is a
decision** that must be named in `docs/decisions.md` — the gate fails a `go.mod`
change with no matching entry.

### 11.2 · Build

```bash
make build
```

Writes `bin/helm` and `bin/helmstudio`. `make clean` removes them.

### 11.3 · The gate

```bash
make gate
```

This is the whole definition of done. A change is not finished because the code
is written; it is finished when the gate is green.

| Target | Checks |
|---|---|
| `fmt` | `gofmt -l` is clean |
| `vet` | `go vet` over the root module and the three side modules |
| `vet-linux` | The tree type-checks as `GOOS=linux`, so the Linux side of each platform seam cannot silently stop compiling |
| `boundaries` | No OS branching or home lookup outside `internal/platform` |
| `deps` | Every `go.mod` change on this branch is named in `docs/decisions.md` |
| `drift` | Regenerate the clients and router into a scratch tree; fail on any difference |
| `test` | `go test ./...` |
| `sdk` | The runtime SDK and embedded provider modules' own tests |
| `conformance` | One suite against the daemon over HTTP **and** the embedded provider in process, plus the Python and Node client smoke tests |
| `site` | Builds the documentation site into `site/out` |
| `site-test` | Every internal link on the site leads somewhere; every sample runs or its page says why not — the quickstart as written, under `helm dev`; the annotated manifests match the studio files; the reference matches the contract; the site's stylesheet passes the theme lint |
| `visual` | helm-css and the four components, with the launcher's screens, the timeline editor and the site's landing and quickstart pages at 1280, 1000 and 380 px, in both themes, pixel-exact against goldens in a real Chrome. What a picture cannot show — a thumbnail loading, a carriage-return line rewriting in place — is asserted against the DOM instead |
| `validate` | `helm validate studios/*.yaml` — every registry manifest, every time |

Two more, run deliberately and never as part of the gate, because they change
what the goldens mean:

```bash
make golden        # rewrite the visual goldens — then look at the images
make golden-media  # rewrite the export goldens — then look at the diff
```

### 11.4 · Running the daemon

```bash
./bin/helmstudio
```

Then open `http://127.0.0.1:8700`. Flags:

| Flag | Default | Does |
|---|---|---|
| `-addr` | `127.0.0.1:8700` | The loopback address for the launcher and both APIs |
| `-studios` | `studios` | The directory of manifests to load |

On startup it logs its resolved data and logs roots, every re-adopted group,
everything that is gone since the last run, and every manifest it skipped with
the field and reason. **One bad manifest never blocks the others.**

To run against a throwaway tree instead of your real one — which is what you
want when trying things out:

```bash
HELMSTUDIO_HOME=/tmp/helm-scratch ./bin/helmstudio
```

A gated Hugging Face repo needs a token. The contract puts it behind
`PUT /api/v1/launcher/settings/huggingface-token`; it can also be set by hand,
and either way it lives in the Keychain and never in the database:

```bash
security add-generic-password -s helmstudio -a huggingface-token -w
```

### 11.5 · Developing a studio, with no daemon at all

From inside the studio's own checkout:

```bash
helm validate helmstudio.yaml
helm dev -f helmstudio.yaml
```

`helm validate -criteria helmstudio.yaml` also scores the certification
criteria a published studio is held to, as far as a manifest alone can answer
them.

`helm dev` is the same supervisor, manifest loader, substitution and studio API
the daemon uses, restricted to one studio, over `./.helm`. It runs no `build[]`
steps and never downloads: it records the installation as `ready` at the
manifest's own directory, links weights you already have, serves on a free
loopback port, echoes each process's log, re-adopts what a killed `helm dev`
left running instead of starting a second copy, and stops the group on
`SIGINT`. Flags:

| Flag | Default | Does |
|---|---|---|
| `-f` | `helmstudio.yaml` | The manifest to run |
| `-addr` | `127.0.0.1:0` | Port 0 picks a free one |
| `-link` | — | `-link <weight>=<directory>`, repeatable: use weights you already have |
| `-venv` | `$VIRTUAL_ENV` | The Python environment a studio declaring `python:` runs in |

A studio's stylesheets can be checked against helm-css without running
anything:

```bash
helm validate -theme ./web          # report
helm validate -theme -strict ./web  # fail on a finding
```

### 11.6 · Environment variables

Injected into every process in a studio's group:

| Variable | Is |
|---|---|
| `HELM_API` | The platform API base — its presence is what selects the remote provider |
| `HELM_TOKEN` | Per-launch, capability-scoped. Never reaches the studio's page |
| `HELM_STUDIO_ID` | The studio's id |
| `HELM_STAGE_DIR` | Per-launch scratch; write output here, then adopt it |
| `HELM_THEME` | `system`, `light` or `dark` |
| `HELM_ACCENT_DARK` / `HELM_ACCENT_LIGHT` | This studio's identity hue, one per theme |
| `HELM_SDK_BASE` | Where the helm packages for this studio's pinned majors are served |

Read by the tooling:

| Variable | Is |
|---|---|
| `HELMSTUDIO_HOME` | One directory under which every root resolves |
| `HELMSTUDIO_{DATA,CACHE,LOGS,LIBRARY,MODELS}_DIR` | A single root, overriding the above |
| `HELM_FFMPEG` / `HELM_FFPROBE` | The ffmpeg binaries to use |
| `HELM_CHROME` | The Chrome binary for the visual suite |
| `HELM_SCHEMA_PATH` | Override the embedded manifest schema |
| `HELM_ALLOW_MISSING_BROWSER` / `_FFMPEG` / `_CLIENTS` | Let the gate pass without a tool it cannot find |
| `HELM_UPDATE_GOLDEN` | Rewrite goldens — what `make golden` sets |

> **Invariant — a studio inherits nothing else.** A studio process and its
> `exec` probe get only `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`,
> `LANG`, `LC_*`, `TERM` and `TZ`, plus the manifest's own `env`, plus the
> injected block above. A `HF_TOKEN` or `AWS_SECRET_ACCESS_KEY` set on the
> daemon does not reach any studio, and a test proves it.

---

## 12 · Cross-cutting concerns

**Errors get context, not just propagation.** A user reading a log should be
able to tell what failed and what to do about it: the tool that is missing and
the command that installs it, the port and its occupant, the path that was
expected, the actual number of free bytes.

**Testing.** 300-plus Go tests, plus three suites the gate treats separately.
Tests assert the *contract*, not what the implementation happens to do — and
several milestones' fixes were **mutation-checked**: break the fix, confirm its
test fails. Tests never write to `~/.helmstudio`; they get a temp tree
through the directories helper. Nothing touches a user's models
directory, and nothing runs reclaim.

**Concurrency is solved by ownership, not locking.** Studios never touch the
file; they call the API. The daemon holds one write connection and a read pool
under WAL, so readers never block the writer. Callers still get `ETag` and
`If-Match`, so a studio with two tabs open gets a `409` rather than a lost
update.

**Reconciliation is expected, not exceptional.** Two files-and-rows systems
disagree eventually, usually because a person moved something in Finder — and a
creative tool must not call that corruption. A row with no file marks the asset
`missing` and keeps the gallery item intact with its params; a file with no row
is reported as reclaimable and **never deleted silently**.

**What the gate cannot tell you.** Nothing that touches a model. Metal builds,
real weights, unified-memory behaviour, ffmpeg on videotoolbox, the Keychain,
code signing and notarisation are checked by hand on an Apple Silicon Mac. Each
milestone lists its machine-bound checks, and they queue for an integration
window rather than blocking. They are never reported as passing because the
gate was green. `docs/agents/gate.md` puts it bluntly: an agent that writes
"verified" about a Metal path has told you nothing except that it did not
understand the boundary.

**The gate has no CI yet.** As of M1, by the maintainer's direction, `go test`
runs on the host only; the Linux and Windows legs are out of the gate. The one
workflow, `.github/workflows/site.yml`, publishes the site and runs no gate.
`make vet-linux` type-checks and runs nothing. The Linux run is wanted
eventually for a specific reason: macOS has a case-insensitive filesystem,
which hides a class of path bug that a Linux run surfaces on the first try.

---

## 13 · What exists today, honestly

As of the last commit on `main` — built and gate-green: **M0–M8 and M10**, and
M9 in part. M10 was taken before M9 at the maintainer's direction. The Mac
app's shell is built — a native window around the launcher that starts the
daemon or adopts one, with the handshake on both sides of it — and its release
is not: it is unsigned and un-notarised, it bundles neither ffmpeg nor uv, and
its auth cookie is not written. The launcher's Gallery and Timeline screens
wait on that cookie. Each milestone has an implementation report in
`docs/agents/reports/`, and those reports are the place to look for what a
milestone could *not* verify.

**From M6a on, nothing is reviewed.** The loop in `docs/agents/README.md` is
implement, report, review by a *different* agent, fix, land. M6a's review never
ran, and M6b, M7a, M7b, M8a, M8b and M10 were each built at the maintainer's
direction on top of unreviewed work; all seven are on `main` without that step.
Whatever their reports call a judgement call is exactly that — a decision one
agent made, not one anybody has checked.

| | Milestone | State |
|---|---|---|
| M0 | Contracts — manifest schema, four manifests, `helm validate` | built |
| M1 | Foundation — platform seams, directories, store, the gate | built |
| M2 | Supervision — spawn, health, teardown, re-adopt, logs, SSE | built |
| M3 | Install and weights — clone, build, resume, downloader, linked weights, reclaim | built |
| M4 | API and SDK — OpenAPI, the generator, the router, three clients, embedded provider, conformance, `helm dev` | built |
| M5 | Python studios — uv environments, the switch dialog with its `busy` probe | built |
| M6a | helm-css, tokens, the theme path a running studio follows | built, **unreviewed** |
| M6b | Components and screens — `helm-ui-sdk`, the launcher client, the six screens | built, **unreviewed** |
| M7a | Library, trust and selection — three-source resolution, the approval gate, checkpoint selection | built, **unreviewed** |
| M7b | The library's screens — cards, Add a studio, import, the manifest editor, the approval screen | built, **unreviewed** |
| M8a | Timeline document, API and export pipeline | built, **unreviewed** |
| M8b | The timeline editor — `helm-timeline` | built, **unreviewed** |
| M9a | The Mac app's shell — window, handshake, adoption, icon, `.dmg` | built, **unreviewed** |
| M9b | Its release — the auth cookie, signing, notarisation, bundled ffmpeg and uv | **not built** |
| M10 | Docs and the site — `site/` and the workflow that publishes it | built, **unreviewed** |

Concretely, that means:

- **`helm-ui-sdk` ships all four components.** `helm-terminal`, `helm-gallery`,
  `helm-player` and `helm-timeline` are built and served at `/sdk/v1/ui/`. The
  launcher uses `helm-terminal` for its install and process logs, and the
  sequencer fixture under `test/studios/` edits its sequence in
  `helm-timeline`; no registry studio imports them yet — h3 stays at level 2
  (M6 Q20). `helm-player`'s filmstrip, waveform and h264 proxy belong to no
  milestone, and render `Unsupported` with a reason.
- **The launcher is the library and its screens**: the shell with its
  System/Light/Dark control; the catalogue, whose cards state each studio's
  source, level and install state, with Override, Duplicate, Revert and a
  checkpoint choice; Add a studio, from a repository, a folder, an import or a
  new manifest; the manifest editor, a form generated from
  `schema/manifest.json` beside the YAML and the criteria; the approval screen;
  studio detail and install; the process group; models and disk; and settings.
  Three screens are deliberately absent. Gallery and Timeline wait for M9's
  cookie, because a read-only launcher gallery under today's Host and Origin
  rules would let any local process read every studio's work — so
  `POST /timeline/{id}:open` answers `501` with a reason, and a sequence is
  edited inside a studio. Doctor belongs to no milestone yet, and is hidden
  rather than shown empty.
- **The documentation site is built** by `site/`: fifteen written pages, and
  API, manifest and CLI references generated from the contract and the code.
  Every code block on it is one of 29 samples under `site/samples/`, and the
  gate runs 25 of them — the quickstart among them, as written, under
  `helm dev`. `.github/workflows/site.yml` publishes it to helmstudio.in;
  turning on Pages, the domain and its DNS are the maintainer's.
- **03 §7's Requirements block is not drawn, and every card wears the accent
  instead of its identity stripe** — contradictions raised rather than
  resolved. `GET /studios` serves no `hue`, `repo`, `ref` or `requires`, and
  nothing anywhere reports the host's OS version, memory, free disk or which
  tools are present, so neither the "required" column nor the "found" one has a
  source. Of the four launch manifests, only h3's declares a `hue`.
- **What the later milestones found in the design is recorded, not fixed.**
  Each left entries under "Open, not yet decided" in `docs/decisions.md` —
  among them where an approval is stored (schema v7's three approval columns
  are unused), how a studio page learns another studio's name and hue for a
  sequence's clips, a criteria table that disagrees with 05 §9, and three
  timeline behaviours that differ from 05 §6.
- **`helm` has `validate` and `dev`.** `helm test`, `helm doctor --studio`,
  `helm adopt`, `helm studio init`, and `helm dev --fixtures` and `--fail` are
  specified and unbuilt; the smoke harness they share has no milestone placed
  yet (M7 Q16).
- **`studios/*.yaml` are registry entries** (`schema/registry-entry.json`): a
  pointer — `id`, `repo` and a pinned `ref` — carrying its studio's manifest
  inline until the studio's own repository ships a `helmstudio.yaml`. The
  library reads a local manifest first, then the studio's own, then the inline
  copy.
- **No machine-bound demo from M6a on has run.** Each of those reports lists its
  demo — on the Mac, with real weights and real outputs — under *Could not
  verify*, with what ran instead.

---

## 14 · Where to read next

| Question | Document |
|---|---|
| What are we building, and what are the requirements? | `docs/design/01-prd.md` |
| What is stored, and in what shape? | `docs/design/02-data-model.md` |
| Colour, type, screens, components, copy | `docs/design/03-design-system.md` |
| The three packages and the dependency rule | `docs/design/04-packages.md` |
| Writing a studio, and the certification criteria | `docs/design/05-sdk-and-custom-studios.md` |
| Why SQLite, and where the line between rows and bytes sits | `docs/design/06-storage.md` |
| The platform services and the capability model | `docs/design/07-platform-services.md` |
| A real studio audited against the design | `docs/design/08-h3-dry-run.md` |
| What was decided, when, and why | `docs/decisions.md` |
| Build order and the reasoning behind it | `docs/plan/` |
| How a milestone is actually run | `docs/agents/` |
| Commit style, branches, the gate | `CONTRIBUTING.md` |
