# Platform Services

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

Five questions — local persistence, an API for studios to save state, shared media storage, a cross-studio gallery, and one palette across independent repos — are one question: what does the daemon expose so that a studio never has to build storage, a gallery, or a design system again? This is that surface.

## 0 · The one rule everything else follows from

A studio must run perfectly with the daemon and perfectly without it.

The moment a studio requires helmstudio to function, it stops being an independent repo and helmstudio stops being a launcher — it becomes a framework people have to buy into, and the first principle in the PRD is gone. So every platform service is offered through an SDK with a local fallback: when `HELM_API` is present in the environment, the SDK talks to the daemon; when it is absent, the same calls read and write `./.helm/` inside the studio's own directory with identical semantics. A studio author writes one code path and gets both. This is also why nothing here is mandatory: a studio that ignores the SDK entirely still installs, launches and works — it just doesn't appear in the central gallery.

## 1 · Three stores, because there are three access patterns

One storage mechanism cannot serve a 200-byte settings blob, a 2 GB video, and a 40,000-row index with the same efficiency. Trying to make it is how local apps end up shipping a database server. Match the mechanism to the pattern and no server is needed at all.

All three live under `~/.helmstudio/`, all three are plain files a person can open, and none of them needs a process of its own. The daemon is the only writer, which is what makes single-writer techniques — atomic rename and an append-only journal — sufficient where four concurrent processes would otherwise need locking.

## 2 · How state persists, with no database *server*

Revised

An earlier draft of this section proposed three hand-built mechanisms — a JSON document, a content-addressed blob store and an append-only journal — specifically to avoid a dependency. The blob store survives unchanged. The other two are now one embedded SQLite file, because the platform's queries turned out to be relational and hand-rolling eight indexes plus a graph traversal is how you end up writing a worse database.

**SQLite for metadata, the filesystem for bytes.** There is still no server, no port, no daemon of its own — SQLite is a library linked into the helmstudio binary, and the whole store is one file the user can copy. What changes is that the daemon now gets real indexes, joins, foreign keys, transactions and full-text search over prompts, instead of maintaining those by hand in Go.**

| Data                                                                                                  | Where                             | Why there                                                                                                           |
|-------------------------------------------------------------------------------------------------------|-----------------------------------|---------------------------------------------------------------------------------------------------------------------|
| KV namespaces, records, asset index, gallery, provenance, tags, plus the daemon's own lifecycle state | `helm.db`                         | One transaction boundary, one backup file, one migration path. Two persistence mechanisms is a tax paid forever.    |
| Media bytes                                                                                           | `assets/blobs/<ab>/<cd>/<sha256>` | Content-addressed files: hardlink adoption, `Range` streaming, dedup as a side effect. A 2 GB video is never a row. |
| Thumbnails, posters, waveforms                                                                        | `assets/derived/<sha256>/`        | Regenerable, so they must sit outside the thing you back up.                                                        |
| Build and run logs                                                                                    | `studios/<id>/logs/`              | Append-only files. Subprocess stdout must never be a transaction.                                                   |
| Download progress                                                                                     | Nowhere                           | Derived from `.part` lengths at startup. A database does not make a pointless write worth making.                   |

Concurrency is solved by ownership rather than locking: studios never touch the file, they call the API, and the daemon holds one connection for writes and a pool for reads under WAL, so readers never block the writer. Two studios recording a render simultaneously are two serialised transactions, not two processes racing on a file. Optimistic concurrency is still exposed to callers as `ETag` and `If-Match`, so a studio with two tabs open gets a `409` rather than a lost update.

Dedup stops being an algorithm and becomes a `UNIQUE` constraint on `sha256`; asset reclaim stops being a sweep and becomes one `NOT EXISTS` query; "everything ever made from this reference image" stops being a graph traversal you write and becomes a recursive CTE. The schema, the generic per-studio records API and the driver choice are in the storage document.

## 3 · The studio API

At spawn, the daemon injects the environment every SDK reads. The token is minted per launch, scoped to that studio's id and its declared capabilities, and dies when the process group stops — so a studio cannot read another studio's namespace, and a token scraped from a log is useless an hour later.

    # injected into every process in the group
    HELM_API=http://127.0.0.1:8700/api/v1
    HELM_TOKEN=hs_live_9f2c…          # per-launch, capability-scoped
    HELM_STUDIO_ID=h3-studio
    HELM_STAGE_DIR=/Users/…/.helmstudio/stage/h3-studio/01JB9   # zero-copy adoption
    HELM_THEME=dark                    # system|light|dark
    HELM_ACCENT=#e0a33c                # this studio's identity hue

| Endpoint                                                 | Does                                                                                                                                    |
|----------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------|
| GET /kv/{ns}/{key}           | Read a JSON document. Returns `ETag`.                                                                                                   |
| PUT /kv/{ns}/{key}          | Replace. `If-Match` for optimistic concurrency; `409` on conflict.                                                                      |
| PATCH /kv/{ns}/{key}        | JSON merge patch, for a single field without resending the document.                                                                    |
| GET /kv/{ns}?prefix=         | List keys with sizes and modified times. No values.                                                                                     |
| DELETE /kv/{ns}/{key}       | Remove.                                                                                                                                 |
| POST /assets                | Upload bytes, returns `{id, sha256, bytes, kind, url}`. Idempotent on content.                                                          |
| POST /assets:adopt          | Adopt a file already written into `HELM_STAGE_DIR` by hardlink — **no copy, no HTTP body**. This is how a 2 GB render enters the store. |
| GET /assets/{id}             | Stream bytes with `Range` support, so a studio can scrub a video without downloading it.                                                |
| GET /assets/{id}/thumb?w=320 | Poster frame or waveform, generated once by the daemon and cached. No studio implements thumbnailing.                                   |
| POST /gallery/items         | Record an output with its params, inputs and session. One call per generation.                                                          |
| GET /gallery/items?…         | Query by kind, tag, text, date, session. Scoped to the caller unless `scope=all` and the capability is held.                            |
| PATCH /gallery/items/{id}   | Star, tag, rename, annotate.                                                                                                            |
| POST /handoff               | Send an item to another studio's inbox — the cross-studio move.                                                                         |
| GET /inbox                   | Items other studios sent here. Drained by the studio on read.                                                                           |
| GET /events                  | SSE: theme changed, inbox arrived, item added elsewhere, shutting down.                                                                 |
| GET /me                      | Studio id, capabilities, quota, paths, daemon version.                                                                                  |

**Capabilities are declared in the manifest and shown before install**, so the user sees what a studio is asking for and the token enforces it:**

    capabilities: [kv, assets, gallery]        # the normal set
    # gallery.read_all — see other studios' items (the launcher, and tools that curate)
    # kv.shared      — read/write the shared namespace, e.g. a common prompt library
    # handoff.send   — push items into another studio's inbox

A studio with no `capabilities` gets no token at all, which keeps the default the safest one.

## 4 · One media store for every studio

The point is that a studio writes an output file and makes one call. Everything after that — hashing, deduplication, thumbnails, poster frames, waveforms, duration and dimension probing, retention, reclaim — is the daemon's job, done once, the same way for all four studios.

    # the fast path: the studio already wrote the file, so nothing is copied
    POST /assets:adopt
    { "path": "$HELM_STAGE_DIR/take-0915-053255.mp4", "kind": "video" }
    → { "id": "as_01JB9…", "sha256": "7c1f…", "bytes": 18443120,
        "kind": "video", "width": 800, "height": 448, "duration_s": 5.17,
        "thumb": "/api/v1/assets/as_01JB9…/thumb?w=320" }

Adoption hardlinks the staged file into the blob store and unlinks the stage entry, so a 2 GB video costs two inode operations rather than a 2 GB copy through an HTTP body. If the hash already exists, the link is simply dropped — the second studio to produce identical bytes stores nothing.

| Path                                    | Holds                                                                                                       |
|-----------------------------------------|-------------------------------------------------------------------------------------------------------------|
| assets/blobs/\<ab\>/\<cd\>/\<sha256\>   | Immutable bytes, two-level fan-out so no directory holds 100k entries.                                      |
| assets/derived/\<sha256\>/thumb-320.jpg | Thumbnails, poster frames, waveform PNGs, generated lazily via ffmpeg and safe to delete.                   |
| stage/\<studio\>/\<group_run\>/         | Per-launch scratch the studio writes into. Cleared when the group stops; anything not adopted is discarded. |
| state/\<studio\>/\<ns\>.json            | That studio's documents. Never readable by another studio.                                                  |

**Retention** reuses the model-weight mechanism exactly: an asset's reference count is the number of gallery items pointing at it, deleting an item decrements it, and an asset at zero becomes reclaimable rather than deleted. The disk page gains a second table and no new concepts. Inputs a user imported are pinned by default, because deleting the reference photo someone dragged in is a much worse outcome than keeping a stale render.**

## 5 · One gallery, two views

A gallery item is a row about an asset: what produced it, from what, with which parameters. Because every studio writes the same shape, the launcher's cross-studio gallery and a studio's own panel are the same query with a different scope — there is no second implementation and no syncing.

    POST /gallery/items
    { "kind": "video", "asset": "as_01JB9…",
      "title": "café window drift",
      "session": "example",
      "params": { "model": "MiniMax-H3", "mode": "FL2VA", "seed": 42, "steps": 12,
                  "resolution": "800x448", "frames": 124, "prompt": "The woman in the grey knit…" },
      "inputs": [ { "asset": "as_01JB7…", "role": "first_frame" } ],
      "tags": ["café", "test"] }

`inputs[]` is the field that earns the central store. It makes provenance a graph: this video came from that last frame, which came from that image, which came from that prompt in another studio. No studio could record that alone, because the earlier link happened somewhere else.

### A studio's own gallery view

Two ways, and a studio picks by how distinctive its list needs to be. At level 3 it drops in `<helm-gallery scope="self">` from `helm-ui-sdk` and gets a themed, paginated, filterable panel with thumbnails, tagging, live insertion and picker mode. At level 2 it renders its own panel from `GET /gallery/items` — which is what h3 studio would do, because a take list with seeds, frame counts and chain actions is not AuK's generation list with task types, and a generic component should not pretend otherwise.

Either way the framework provides the expensive shared parts: thumbnails, poster frames and waveforms generated once, the query with its filters and cursor, the provenance, and the `/events` stream so a panel updates the moment a render lands. The launcher's cross-studio gallery is the same endpoint at `scope=all` — one API, several consumers.

### The handoff

The cross-studio move is the payoff: an image generated in iris studio becomes the first frame of an h3 studio render, and a voice line from AuK becomes the audio bed. `POST /handoff` puts the item in the target studio's inbox; the target reads `GET /inbox` on start and on an SSE event, and shows it as a pending input. The launcher offers the same thing as a "Use in…" menu on any gallery item, which works even for a studio that has not implemented the inbox — it simply arrives as a file in that studio's stage directory.

## 6 · One palette across independent repos

Consistency cannot be enforced on a repo you do not control, and pretending otherwise produces a framework nobody adopts. What works is making conformance the cheapest possible path and non-conformance visible. Three layers, in ascending order of commitment.

Three packages, one direction

Consistency rests on `helm-css` — tokens, base, layout and component classes, plus `tokens.json` — which depends on nothing and can be adopted alone. `helm-ui-sdk` layers prebuilt gallery, player, terminal and timeline components on top of it, and consumes `helm-runtime-sdk` for every call it makes. A studio takes as many layers as it wants; the arrow never points back up.

    /* a studio's stylesheet only ever names tokens */
    .take-card { background: var(--helm-ground-panel); border: 1px solid var(--helm-border-hairline);
                 border-radius: var(--helm-radius-md); color: var(--helm-text-primary); }
    .render-btn { background: var(--helm-studio-accent); color: var(--helm-on-accent); }

**Identity survives.** `--helm-studio-accent` is set from the manifest's `hue`, so h3 studio's Render button stays amber and AuK's stays violet while every ground, border, text colour, radius and font matches. Studios are consistent, not uniform — which is the actual goal.**

**Theme follows the launcher.** The daemon passes `HELM_THEME` at spawn and pushes a `theme` event over SSE when the user toggles it; the SDK's two-line theme bridge sets `data-theme` on the studio's root element. Flip the launcher to light and every running studio follows within a frame. A standalone studio falls back to `prefers-color-scheme`, which is what it would have done anyway.**

### What "enforce" can honestly mean

| Mechanism                                                                                                                                                         | Strength                                                                                                      |
|-------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| `helmstudio validate` lints the studio's CSS for raw hex, rgb and hsl literals outside its vendored token file, and for font families that are not the Plex stack | Advisory locally, **blocking in the registry CI** — a manifest pull request that fails the lint is not merged |
| Contrast and dark-mode check: the validator renders the studio's own stylesheet against both token sets and fails on a pair below 4.5:1                           | Blocking in CI, and it catches the real failure mode — a studio that only ever tested in dark                 |
| A `theme: helm-v1` claim in the manifest, shown on the card as a small conformance mark                                                                           | Social. Visible to users, cheap to earn, embarrassing to lack                                                 |
| The launcher never restyles a studio's own UI                                                                                                                     | Absolute. Injecting CSS into someone else's app is how a launcher earns a reputation for breaking things      |

For studios you wrote yourself — all four of the launch set — the lint is simply a refactor you do once. For third-party studios it is a merge gate, which is the strongest lever a registry has and the only one that does not require owning their code.

## 7 · SDK packages

All three are thin wrappers over the HTTP API with the same method names, so a studio author moving between them is not learning a new product.

| Package             | For                      | Shape                                                                                                                                    |
|---------------------|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------|
| `helmsdk` (Go)      | h3 studio, iris studio   | `c := helm.FromEnv()` → `c.KV.Put`, `c.Assets.Adopt`, `c.Gallery.Add`. stdlib only, no dependencies, fits the studios' existing posture. |
| `helmsdk` (Python)  | ltx studio, AuK studio   | `helm = Helm.from_env()` → same methods, sync and async. Depends on nothing but the standard library.                                    |
| `helm.js` (browser) | Every studio's front end | `import {helm} from '/sdk/helm.js'` — API client, theme bridge, SSE subscription, and the web components.                                |

    // Go, inside h3 studio, right after a render completes
    as, _ := c.Assets.Adopt(ctx, outPath, helm.Video)
    c.Gallery.Add(ctx, helm.Item{
        Kind: helm.Video, Asset: as.ID, Title: prompt.Short(),
        Params: helm.M{"seed": seed, "steps": steps, "mode": "FL2VA"},
        Inputs: []helm.Input{{Asset: firstFrameID, Role: "first_frame"}},
    })

## 8 · The standalone fallback

Every SDK constructor checks for `HELM_API`. When it is missing the client switches to a local backend with the same interface: `./.helm/state/<ns>.json` for documents, `./.helm/assets/` for blobs using the identical content-addressed layout, and `./.helm/gallery/index.jsonl` for items. The semantics are identical, down to ETags and reference counts.

This has a useful second effect: because the layouts match, a studio that ran standalone for six months can be adopted wholesale when the user later installs helmstudio — the daemon imports `./.helm/` by hardlinking blobs and replaying the journal, and nothing is lost or duplicated.

## 9 · Adopting what the studios already have

All four launch studios already store data their own way, and a platform that orphans it is worse than no platform. Each gets a one-time importer that runs on first launch under helmstudio, is idempotent, and never moves or deletes the originals.

| Studio      | Has today                                                                                        | Import                                                                                                                                                                                                       |
|-------------|--------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| h3 studio   | `sessions/<name>/outputs/*.mp4` with per-take metadata, plus `sessions/h3.json` and `model.json` | Takes become gallery items with their real seed, steps, resolution and frame count; anchors and reference images become inputs, giving genuine provenance from day one. Remembered paths become KV settings. |
| ltx studio  | Takes, timeline entries, chained shots                                                           | Same mapping; a timeline becomes a session with ordered items.                                                                                                                                               |
| AuK studio  | A reference audio library and 18 generations with task type and text                             | Reference clips become pinned assets — they are user inputs, so they are never reclaimable. Generations become audio items whose params carry the task and instruction.                                      |
| iris studio | Per-session state and reference images                                                           | Images become items; multi-reference combinations become multi-input provenance.                                                                                                                             |

The importer is a manifest field — `import: { run: "go run ./cmd/helm-import" }` — so it is a data change like everything else, and a studio that has nothing to import simply omits it.

## 10 · What this changes elsewhere

| Document      | Change                                                                                                                                                                                          |
|---------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Manifest      | Adds `capabilities[]`, `theme`, `import`. `hue` now feeds `--helm-studio-accent` rather than only the card stripe.                                                                              |
| Data model    | Adds `Asset` (content-addressed, ref-counted like ModelArtifact), `GalleryItem` (with `inputs[]` provenance), and `KVDoc`. The reference-count and reclaim machinery is reused, not reinvented. |
| Design system | The token file stops being documentation and becomes a shipped artifact, served at `/sdk/helm-tokens.css` and versioned with the daemon.                                                        |
| PRD           | A new requirement group for the platform API, and a non-goal worth stating plainly: **the SDK is optional**. A studio that ignores it must still install, launch and work.                      |
| Milestones    | Platform lands after M3, once two studios exist to prove the API against — building it before that would be designing an interface with one caller.                                             |

The order that matters

Build the asset store and the gallery first, with h3 studio as the only caller, and resist generalising until ltx studio is the second. An SDK designed against one studio is a refactor of that studio; an SDK designed against two is an interface.
