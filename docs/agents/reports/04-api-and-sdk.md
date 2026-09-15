# M4 — API and SDK — implementation report

## What was built

The platform API, end to end, from one contract:

- **Contract.** `api/openapi.yaml`, reviewed once, amended by the first review.
- **Generator.** `api/gen` turns it into stdlib-only Go, Python and Node clients, and into the daemon's studio-api router with the schemas' checks.
- **Enforcement.** One implementation: `internal/api/studioapi` (kv, sessions, records and their filter language, assets, gallery with lineage and search, handoff and inbox, task jobs, events, tokens, quotas, asset reclaim) over schema v4 and a hardlink media engine (`internal/media`).
- **Providers.**
  - The daemon serves that router with per-launch tokens injected by the supervisor.
  - The embedded Go module serves the same router in process for a studio with no daemon.
  - `helm dev` runs a studio's own manifest against `./.helm`.
- **Proof.** One conformance suite, run against both providers, catches every deliberate bug planted in either.

Work stopped twice for the human: at kickoff (30 questions) and at the first review (16 findings, five of them decisions). While building, two more questions went to the human: editing M2/M3 tests for the new contract shapes, and where the store lives. Both answers are in `docs/decisions.md`.

## Second review (helmstudio-b6): 0 BLOCKING, 4 SHOULD-FIX, 8 NOTE

I agreed with every finding. The human decided #1 (versioning), #5 and #9, allowed `schema/schema.go` for #1, and confirmed #12. Each change is a "second review (#n)" entry in `docs/decisions.md`.

| # | Change | Test |
|---|---|---|
| 1 | **Human: semver tags at merge.** Every `go.mod` requires `v0.4.0` of the in-repo modules; the `replace` lines stay. **Human: embed the schema.** `schema/schema.go` embeds `manifest.json`, `internal/manifest` compiles it, and `HELM_SCHEMA_PATH` still overrides. | `cmd/helm` `TestTrimpathBinaryValidatesWithoutTheSourceTree` builds with `-trimpath` and validates from a temp directory. Resolution from outside the repository needs the tags pushed; see "Could not verify". |
| 2 | Event ids are `<boot>-<seq>`. A foreign, never-issued or unparsable `Last-Event-ID` gets `gap` first. The `/events` description calls the id opaque. | `TestResumingEventsAcrossARestartSendsAGap` (both providers): restart, pass the old id's number, resume, expect `gap` first. A valid resume has no gap. |
| 3 | The service's `blobs` lock spans ingest's place-and-commit and reclaim's commit-and-remove. | `studioapi` `TestAnUploadDuringReclaimKeepsItsBlob` forces the interleaving through a seam. **Without the lock it reads 410 `asset_missing`**; with it the bytes read back. |
| 4 | The cross-studio check is built from `studioapi.Operations()`. An operation lacking a resource or body in the test fails it. | `TestEveryIDRouteRefusesAnotherStudiosResource` (22 id routes). The reviewer's case, session writes without their studio scope, is caught by this test alone. |
| 5 | **Human: signed off.** "awaiting review" removed from `api/openapi.yaml`. | — |
| 6 | `Platform.Readopted` revokes tokens and clears stages (through `os.Root`) for every group not live, in the daemon and `helm dev`. | `TestClearStagesKeepsLiveGroupsAndFollowsNoSymlink` |
| 7 | Reclaim's `total_bytes` counts only blobs with no link outside the store and library, through a new `platform.LinkCount` seam. The contract says so. | `media` `TestLinkedOutsideSeesAStudiosOwnLink` |
| 8 | The generator passes `r.ContentLength` to the upload. The disk check counts it, and an over-limit body is refused before it is read. | covered by the contract checks; no new case |
| 9 | **Human: 507 is `QuotaExceeded`**, in 04 §4, the contract and the Go, Python and Node clients. | Go SDK `TestErrorKindsFollowTheStatus` |
| 10 | The client smoke tests fail when an interpreter is missing, unless `HELM_ALLOW_MISSING_CLIENTS=1`. | — |
| 11 | `studioapi.NewPlatform` plus `Serve` is the one constructor for `cmd/helmstudio`, `helm dev` and the suite's daemon. The suite's daemon now has launch hooks and the real free-disk check. | the whole suite |
| 12 | **Human: confirmed** the store staying in `internal/` and the M2/M3 test updates. | — |

Two more deliberate bugs, rerun with the harness (all 11 now caught):
- A broker that ignores the boot identity. It was **missed first**: the case resumed before the new sequence reached the old id. The case now passes that number first, and the bug is caught on both providers.
- The reviewer's session writes without their studio scope. Caught.

## Gate

    $ rm -rf bin && go clean -testcache && make gate   # after the second review
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: every go.mod change since 797501a is recorded in docs/decisions.md
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	4.416s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.241s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	1.277s
    ok  	github.com/janishar/helmstudio/internal/install	5.932s
    ok  	github.com/janishar/helmstudio/internal/manifest	3.050s
    ok  	github.com/janishar/helmstudio/internal/media	1.616s
    ok  	github.com/janishar/helmstudio/internal/platform	2.142s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	2.703s
    ok  	github.com/janishar/helmstudio/internal/supervisor	28.911s
    ok  	github.com/janishar/helmstudio/internal/weights	3.766s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ?   	github.com/janishar/helmstudio/web	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.650s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	2.524s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

This is macOS on Apple Silicon only, as decided in M1; `vet-linux` type-checks the root and the three new modules as Linux and runs nothing. Outside the gate, `go test -race -count=1` of `internal/api/...`, `internal/media`, `internal/supervisor`, `internal/store`, `cmd/helm`, the Go SDK module and `test/conformance` is clean, and `-count=10` of the `helm dev` test is clean.

### The Definition of Done, item by item

| DoD | Evidence |
|---|---|
| Conformance passes against both providers, from one suite | `test/conformance`: every case runs through `each`, once as `daemon` (the real `internal/api` server over HTTP with tokens) and once as `embedded` (`embedded.OpenShared`, in process). Two cases are daemon-only because they are about tokens or the Python and Node clients: `TestATokenCannotReachAnotherStudiosNamespace`, `TestPythonAndNodeClientsSmoke` (both clients ran; neither was skipped). `TestEmbeddedProviderOpensFromTheStudiosManifest` is embedded-only. |
| A deliberate bug in one provider is caught, demonstrated | Table below: nine bugs, three only in the embedded provider, three only in the daemon, three shared, plus two after the second review. The first run missed two daemon-only bugs; each got a new case and was then caught. |
| `:adopt` hardlinks; a test compares inode numbers | `TestAdoptHardlinksFromStageAndNeverCopies` (both providers) records the staged file's inode, adopts, checks the stage entry is gone, and finds a stored file with that inode. `TestAdoptFromDataKeepsTheFileAsTheBlobsReadOnlyInode` checks the studio's file stays, is the same inode with a link count of at least 2, is 0444, and cannot be rewritten. `internal/media`'s `TestLinkPlaceAndLibraryShareOneInode` compares blob, library and source inodes. `TestCrossDeviceAdoptionNeverFallsBackToACopy` forces `EXDEV` and checks nothing was copied. |
| A studio token cannot read another studio's namespace; a test tries | `TestATokenCannotReachAnotherStudiosNamespace` gives studio A every id studio B created: kv, record, session, asset, item, job. Every read, update, delete, lineage, thumbnail, log, handoff and gallery-add with them is 404. Every list is empty for A. B's cursor is refused. Tokens are refused when absent, wrong, in the query string, under another scheme, or revoked. Every studio-api route answers 401 with no token. `internal/api`'s `TestLauncherJobQueueHidesTaskJobs` checks the tokenless launcher queue never shows a task job. `studioapi`'s `TestLaunchInjectsTheTokenAndStopRevokesIt` checks the token a real process receives is revoked when its group stops. |
| Generated clients regenerate with no diff | `make drift` generates into a scratch tree and compares all five generated files. Demonstrated: a line appended to `generated.js` failed the gate naming the file; restored, it passed. |
| `helm dev` runs h3 with no daemon present | **Not verified: machine-bound** (see "Could not verify"). `cmd/helm`'s `TestHelmDevRunsAStudioAgainstItsOwnHelmDirectory` runs a real studio process from its own manifest with no daemon. It checks: the injected `HELM_API` is `helm dev`'s own port; `{data}` is `./.helm/data`; the SDK with the injected token can put kv, adopt from the stage and record an item into `./.helm/helm.db`; a capability not declared is refused; stopping ends the process and the API. |
| h3 writes a real take with its inputs in `item_inputs` | **Not verified: machine-bound, and it needs a change in the h3 repository**, which is not in this one. The call site is below. The same path — adopt from `{data}`, record an item with a `first_frame` input — is `TestAdoptFromData…` plus `TestGalleryRecordsProvenance…`. |
| `make gate` green, including drift and conformance | Above. |

### Deliberate bugs

Each was applied alone, the whole suite run, and the file restored (`git diff` is clean against the tree after each).

| # | Bug | Provider | Caught by |
|---|---|---|---|
| 0 | Every studio gets every capability, whatever its manifest says | embedded | `TestEveryOperationChecksItsCapability/embedded`, `TestKVSharedNamespaceNeedsKVShared/embedded`, `TestGalleryScopeUpdateAndDelete/embedded`, `TestLineage…/embedded`, `TestEvents…/embedded`, `TestMeReportsTheCaller/embedded` and 3 more |
| 1 | The in-process transport reports every status as 200 | embedded | 25 cases, all `/embedded` |
| 2 | The in-process transport drops response headers | embedded | `TestAssetBytesWithRange/embedded`, `TestThumbnails…/embedded` |
| 3 | A revoked token still authenticates | daemon | `TestATokenCannotReachAnotherStudiosNamespace` |
| 4 | A studio's data directory is the stage root, so it reaches other studios' stages | daemon | `TestAdoptNeverReachesAnotherStudiosDirectories/daemon` — **missed on the first run**; this case was added |
| 5 | A token is also accepted from a `?token=` query parameter | daemon | `TestATokenCannotReachAnotherStudiosNamespace` — **missed on the first run**; the query-string and wrong-scheme checks were added |
| 6 | `If-Match` ignored on kv writes | shared | `TestKVIfMatch…/daemon`, `/embedded`, and both client smoke tests |
| 7 | Adopt copies instead of hardlinking | shared | `TestAdoptHardlinks…` and `TestAdoptFromData…`, both providers |
| 8 | Reclaim skips hard-deleting soft-deleted items, so the asset delete hits the foreign key | shared | `TestReclaimAfterDeletingAnItem…/daemon`, `/embedded` |

## Design contradictions raised

- **At kickoff:** 30, below under "History".
- **At the first review:** 16 findings, including one BLOCKING (reclaim after a soft delete could not run).
- **While building:**
  - **Q25's mechanism rested on a false premise.** It said `internal/` cannot be imported from another module. Put to the human, who chose to keep the store in `internal/` (decision log). Q25's intent stands: one enforcement implementation.
  - **The contract does not say how `lt`/`gt` compare values of different JSON types.** The suite found `seed:gt:10` matching a seed stored as `"42"`. I made comparisons type-exact, consistent with Q19's reason, and added a sentence to `api/openapi.yaml` marked "awaiting review". **This is a contract change the reviewer should confirm or reject.**
  - **M2/M3 tests assert shapes the reviewed contract changes** (bare arrays, top-level `heavy`, `/jobs` for install jobs). Put to the human, who approved updating them.

## Judgement calls

All are in `docs/decisions.md`. The ones a reviewer should read first:

- **The generator emits the router, not only the clients.** Parameter decoding, body checks and the capability check are generated from the contract, so the daemon and the embedded provider cannot decode a request differently. The hand-written router half (`handler.go`) only matches paths and authenticates.
- **The embedded provider is the remote client over an in-memory transport,** not a second adapter. It shares routing and decoding with the daemon; it differs only in its principal. Bugs 0–2 show the suite still sees that layer.
- **Launch hooks in the supervisor (`Config.Platform`).**
  - Tokens are minted at launch, and again for a re-adopted group whose members may restart (only hashes are stored, so the original cannot be reissued).
  - Tokens are revoked when a group ends, and after re-adoption for every group not live.
  - A manifest `env` naming `HELM_*` refuses the launch.
- **Job events come from a 500 ms poller,** because `internal/install` is outside the file list and knows nothing of events.
- **`embedded.OpenShared` serves several studios from one `./.helm`,** with per-studio stage and data directories. The suite needs two studios to test handoff and cross-studio reads.
- **Upload limit and disk check.** The limit defaults to 4 GiB; the value is open. The free-disk check requires 2 GiB of headroom before any byte is written, but does not see the body's length.
- **Duplicate adopts restore the file's mode.** A linked duplicate that is discarded gets its original mode back, so adopting identical bytes from `{data}` leaves the studio's file writable.
- **`helm dev` specifics.**
  - It honours a manifest `local_path` as the root.
  - It re-adopts rather than launching a second copy.
  - It serves the launcher's process and log routes as well as the studio API.
  - It echoes each process's log.
  - It reports `provider: "embedded"` from `/me`.
- **`ne` keeps SQLite `IS NOT`,** so a document without the field matches `field:ne:x`.
- **An M2 race fixed in `internal/supervisor/group.go`.** `spawn` wrote `p.log` without the lock that `SubscribeLogs` reads it under. Found by the `helm dev` test under `-race`. Pre-existing; not a design question.

## Could not verify

Nothing here touched a model, Metal, real weights or huggingface.co.

- **h3 recording a real take through the SDK.** On an Apple Silicon Mac with the h3 checkout and FL2VA weights:
  1. In `h3c-studio`, add the Go SDK: `go get github.com/janishar/helmstudio/packages/helm-runtime-sdk/go` (a `replace` to a local checkout until the module is tagged).
  2. In `server/runner.go` `finishTake`, after the take is moved into `outputs/`:
     `c, err := helm.FromEnv()`, then `a, err := c.Assets.Adopt(ctx, helm.AdoptRequest{Path: final, Kind: helm.AssetKindVideo, DurationS: &probe.Duration, Width: &w, Height: &h, FPS: &fps})`,
     then `c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindVideo, AssetID: a.ID, Params: params, Inputs: inputsFrom(job.Params.Refs)})`.
     Each ref is first adopted (from `{data}`) to get its asset id. `final` is under `--root {data}`, which adopt accepts.
  3. `studios/h3-studio.yaml` must pass `--root {data}` (08 change 2) and keep `capabilities: [kv, assets, gallery, timeline]`.
  4. Run `./bin/helmstudio`, install and launch h3, render an FL2VA take with a first frame, then:
     `sqlite3 "$HOME/Library/Application Support/helmstudio/helm.db" "SELECT i.id, x.role FROM items i JOIN item_inputs x ON x.item_id = i.id"` shows the take with `first_frame`.
     `ls -li` on the take in `outputs/` and on the file under `assets/blobs/` shows one inode.
- **`helm dev` running h3.** From the h3 checkout: `helm dev -link fl2va=/path/to/MiniMax-H3`. Expect the studio to start with `{models.fl2va}` resolving to the linked directory, a take to land in `./.helm/helm.db`, and `lsof -i :8700` to show nothing.
- **A real cross-volume adopt.** `EXDEV` is simulated through `media.Engine.LinkFunc`. On a Mac with an external drive:
  - Set `HELMSTUDIO_LIBRARY_DIR` to the drive and adopt. Expect the asset, `library_path: null`, and a warning.
  - Set `HELMSTUDIO_DATA_DIR` so stage and blobs differ (not possible by configuration today, since stage is under data). Expect 422 `cross_device`.
- **Finder behaviour on 0444 library files** (first review #14): tags and in-place edits failing.
- **Linux.** It compiles (`vet-linux`); nothing ran there. `syscall.Stat_t` inode checks in the tests are unix-only.
- **The Go modules from outside this repository.** They resolve only once the human pushes the tags at merge:

      git tag v0.4.0
      git tag packages/helm-runtime-sdk/go/v0.4.0
      git tag packages/helm-runtime-sdk/go/embedded/v0.4.0
      git push origin v0.4.0 packages/helm-runtime-sdk/go/v0.4.0 packages/helm-runtime-sdk/go/embedded/v0.4.0

  Then, in an empty directory: `go mod init probe && go get github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded@v0.4.0`, and a `main` importing it for its side effect and calling `helm.FromEnv()` next to a `helmstudio.yaml`, built with `-trimpath`, opens `./.helm`.
- **The OpenAPI document against the OpenAPI 3.1 meta-schema:** not validated. The generator parses every schema it uses and fails on what it cannot render. `make drift` proves the outputs follow the document, not that the document is valid OpenAPI.

## Dependencies added

None from outside this repository. The root `go.mod` requires and replaces the in-repo module `github.com/janishar/helmstudio/packages/helm-runtime-sdk/go v0.0.0`. The new modules require only this repository's modules and the modules the root already requires. Recorded in `docs/decisions.md`. The Python and Node packages declare no dependencies.

## Files touched outside the milestone's list

All approved:
- **Q28 at kickoff:** `internal/supervisor`, `internal/manifest`, `web/`, `docs/design/02/04/05/07`, `go.mod`, `Makefile`.
- **First review #15:** `docs/design/01`, `08`.
- **Test edits:** `internal/api/api_test.go`, `internal/api/install_test.go` (M2/M3), approved during building. `internal/store/store_test.go` (M1's version pin): named in the decision log for sign-off, the same kind of edit M2 and M3 were directed to make.
- **Unused from the approval:** `internal/platform/**` was approved but not needed.
- **New directory:** `api/gen/` is under `api/**`.
- **This report.**

## Decision-log entries appended

In `docs/decisions.md` under "2026-09-15 · M4 API and SDK": one per kickoff answer (30), one per first-review item (15), and eleven from building (store home, the dependency, the generated router, the embedded transport, launch hooks, typed comparisons, media judgement calls, job events, `helm dev`, test edits). `git diff main -- docs/decisions.md` shows no removed lines.

## Left undone

- **Machine-bound:** h3's take with provenance, and `helm dev` running h3 (above).
- **Deferred by decision:** Python async and the Python embedded provider (M5); browser access and the Node browser build (M6); video and audio probing and thumbnails (ffmpeg); records full-text search; theme events (emitted from M6); asset quotas and the upload limit's value.
- **Not in M4's tasks:** `helm dev --fixtures` and `--fail=`, `helm test`, `helm adopt`, `helmstudio fsck`.
- **Launcher only:** reclaim of assets is served at `/assets:reclaim`, with no shelf UI.
- **Committing.** Per `implementer.md`, the work is uncommitted on `feat/api-and-sdk`.

## History: first review (helmstudio-b6), 1 BLOCKING, 6 SHOULD-FIX, 9 NOTE

I agreed with every finding. Items 2, 3, 4, 6 and 15 held a decision; I put
each to the human, who took the recommendation every time. Every other item
was edited as the review asked. Each change is in `docs/decisions.md` as a
"first review (#n)" entry.

| # | Change |
|---|---|
| 1 BLOCKING | Reclaim counts only live items' assets and inputs. Before deleting the assets, it hard-deletes the soft-deleted items that name them, in the same transaction, and lists those items in the preview. SQLite 3.51 with `foreign_keys=ON`: the delete-then-reclaim path deletes the right assets with no FK error. **Conformance case to write:** delete an item, reclaim, and the asset is gone with no error. |
| 2 | **Human: separate paths.** `/launcher/jobs*` are launcher-only and never show tasks. `/jobs*` are studio-api and always need a token; a studio may change or cancel only its own tasks (403 `not_a_task`). The shelf does not call `/jobs`, so `web/` needs nothing. |
| 3 | **Human: only assets referenced once.** Reclaim adds `assets.first_referenced_at IS NOT NULL`, backfilled by v4. The preview reports never-referenced assets as `unreferenced` and never deletes them. |
| 4 | **Human: stage or `{data}`.** Adopting from `{data}` leaves the file in place, read-only; anywhere else is 422 `outside_roots`. 08's line stands. |
| 5 | Clips use the key `asset_id` (02 §5, 05 §6 and §7), and the reclaim query reads that key. |
| 6 | **Human: `./.helm/models`.** |
| 7 | The in-process provider enforces the manifest's capabilities, and the capability cases run against both providers. |
| 8 | `Internal` added to 04 §4; 416 maps to `Invalid`. |
| 9 | `origin_studio` and `library_path` are null for any caller that is not the origin studio. |
| 10 | A filter value is everything after the second `:`; timestamps compare as RFC 3339 instants. |
| 11 | PATCH is an RFC 7396 merge patch everywhere; a session's `state` now merges. |
| 12 | The contract says the asset URLs need Bearer until M6. |
| 13 | v4 backfills `records.etag`. |
| 14 | The read-only cost is recorded in 02 §5. |
| 15 | **Human: approved.** R39, 07 §4 retention and 08's adopt line are amended; `01` and `08` added to the file list. |
| 16 | Uploads answer 413 `too_large` and 507 `disk_space` (`Unavailable`). The limit's value is still open. |

Re-checked after the changes: the scratchpad checker passes, with 64
operations (41 studio-api) and 0 failures, and `make gate` is green. The
document is still not validated against the OpenAPI 3.1 meta-schema.

## History: the kickoff as it was put to the human

---

Work stopped before task 1. `api/openapi.yaml` cannot be written in full
without settling each question below, and the milestone says that document is
the contract every later package is generated from. Nothing has been written
yet apart from this file, on `feat/api-and-sdk`.

Each question quotes both sides, then gives a recommendation. Answering
"recommendations for all" is enough to proceed. The defaults at the end are
decisions I will make myself unless told otherwise, listed so they can be
overruled before they turn into a contract.

Section references: 01 PRD, 02 data model, 04 packages, 05 SDK, 06 storage,
07 platform services, 08 h3 dry run.

---

## A · What the document contains

### Q1. Launcher operations, timeline, and the tag split

- M4 task 1: "Every platform service: kv, sessions, records, assets, gallery,
  jobs, events, handoff/inbox." Timeline is not in that list.
- 04 §1: the runtime SDK covers "state, records, assets, gallery, jobs,
  **timeline**, events."
- 05 §11: the timeline is "the piece most likely to want a second design
  pass".
- The M2 and M3 decisions list "additions M4 must fold in or reject": `:launch?preempt`,
  SSE logs, the install/job/weights shapes, and the error codes.
- The M0 outline has operations nobody has built: `POST /studios`, `:update`,
  `/studios/{id}/models:select`, `/models/{id}:link` (superseded by M3's
  `/studios/{id}/weights/{name}:link`), `:verify`, `/doctor`, `/settings`.
- The `studio-api`/`launcher` tag split has been open since M0.

**Recommendation:**
- Keep one document and keep the tags. The generator emits only `studio-api`
  operations into `helm-runtime-sdk`.
- The launcher section describes exactly what the daemon serves today, with
  the M2/M3 additions folded in and their errors normalised (Q4).
- Remove the outline operations that have no implementation; each returns
  with the milestone that builds it.
- Remove timeline and re-add it in M8. Neither provider could conform to it
  yet, so no test could hold it.

### Q2. Package names and module paths

These three disagree:

- 04 §4: `helm-runtime-sdk/embedded` and `helm-runtime-sdk[embedded]`.
- 04 §8: `go get helmstudio.in/runtime` and `npm i @helmstudio/runtime`.
- 05 §2: "`helmsdk/embedded` in Go, `helmsdk[embedded]` in Python".
- 07 §7: `helmsdk` for Go and Python, and `helm.js` for the browser.

Also, the repository module is `github.com/janishar/helmstudio`.
`helmstudio.in` is a vanity path that only works if someone serves its
`go-import` meta tag. A Go import path is expensive to change once studios use it.

R52: a hosted-only studio "never pulls SQLite into its dependency tree". That
needs the remote client in its own `go.mod`, with the embedded provider in
another.

**Recommendation:** use 04's names; 05 §1 says 04 "is the authority on the
boundaries". For Go:

- If you control `helmstudio.in`, use `helmstudio.in/runtime` and
  `helmstudio.in/runtime/embedded`.
- Otherwise use `github.com/janishar/helmstudio/packages/helm-runtime-sdk/go`
  and `…/go/embedded`.

Either way there are two modules, each with its own `go.mod`. The root module
reaches them through `replace`, which is a `go.mod` change recorded in the
decision log.

**Answered by the human: the GitHub path.** The modules are
`github.com/janishar/helmstudio/packages/helm-runtime-sdk/go` and
`github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded`.

### Q3. How the clients are generated

- 04 §4: "One OpenAPI document generates all three."
- 07 §7: Go is "stdlib only, no dependencies"; Python "sync and async. Depends
  on nothing but the standard library."

Off-the-shelf generators add runtime dependencies: `oapi-codegen/runtime`,
pydantic, urllib3 or httpx. They also need Java or Node in the gate. The system
Python here is 3.9.6.

**Recommendation:** an in-repo generator written in Go.

- It needs no new module; `yaml.v3` is already a dependency.
- It emits stdlib-only clients: Go on `net/http`, Python 3.9+ on `urllib`, and
  Node as fetch-based ESM with no Node built-ins, so a browser build stays
  possible.
- Only the wire types, a low-level client and a provider interface are
  generated. The ergonomic layer (`FromEnv`, `Assets.Adopt(ctx, path,
  helm.Video)`) is hand-written in separate files that are never regenerated.
- The Python client is sync-only in M4; async is recorded as open.
- The gate runs the generator, then `git diff --exit-code` over the generated
  paths.

### Q4. One error shape, and the SDK's error kinds

- 04 §4 names five kinds: "`Conflict` (etag), `QuotaExceeded`, `NotFound`,
  `Unsupported` …, `Unavailable`."
- The daemon already serves `{error, message}` with statuses 400, 403, 404,
  409, 421, 422 and 501. 04's list has no kind for 400, 401, 403 or 422.
- 06 §6 and 07 §3 put an etag conflict at `409`, where HTTP's own status
  would be 412.

**Recommendation:** every non-2xx body is `{error: <snake_case code>, message,
details?}`. Kinds map from the status:

| Status | Kind |
|---|---|
| 400, 422 | `Invalid` |
| 401 | `Unauthenticated` |
| 403 | `Forbidden` |
| 404 | `NotFound` |
| 409 | `Conflict` (the code separates `etag_mismatch` from `in_use` and the rest) |
| 429 | `QuotaExceeded` |
| 501 | `Unsupported` |
| 503, or no connection | `Unavailable` |

This keeps 409 for etags, as the design says. It adds three kinds, so 04 §4 is
amended.

### Q5. Pagination

- 05 §5 tests "pagination cursors"; 06 §6 has `limit` and `cursor`; 04 §5 has
  "cursor paging".
- Today `/studios`, `/models` and `/jobs` return bare arrays, and
  `web/shelf.js` reads them. `web/` is outside the file list.
- Review focus: "Is pagination consistent across every collection endpoint?"

**Recommendation:**
- Every collection returns `{items: [...], next_cursor: string|null}`.
- `limit` defaults to 50, with a maximum of 200. `cursor` is opaque and keyed
  on `(created_at, id)`.
- The launcher collections change too, and the file list widens to `web/**`
  to follow them (Q28).

---

## B · Tokens and namespacing

### Q6. Where tokens live

- 07 §3: "The token is minted per launch … and dies when the process group
  stops."
- M2 re-adopts running studios after a daemon restart, and a studio's
  environment is fixed at spawn.
- 02 has no table for tokens. 02 §11 and R43 keep secrets out of the store.

If tokens live only in memory, every re-adopted studio gets 401 after a daemon
restart until someone relaunches it.

**Recommendation:** a `studio_tokens` table.

- Columns: `token_sha256` as the primary key (never the token itself),
  `studio_id`, `group_run_id`, `capabilities` as JSON, `created_at`,
  `revoked_at`.
- A token is revoked when its group stops.
- The format is `hs_live_<32 random bytes, base32>`, sent as
  `Authorization: Bearer`.
- 02 §5 is amended.

### Q7. Requests with no token

The M2 decision gives the API no authentication until M9, and a request with
no `Origin` is allowed. If a studio-api request with no token were served as
the launcher, a studio could skip its token and read everything. "A token must
not reach another studio's data" would then be one omitted header away.

**Recommendation:**
- Every `studio-api` operation requires a studio token and answers 401
  without one, under both the daemon and `helm dev`.
- The launcher gets no studio-api access in M4; nothing in the shelf needs it.
  A launcher principal comes with the UI that needs one (M6) or the cookie
  (M9).
- Launcher operations keep M2's rules.

### Q8. Which capability each operation needs

The schema's capabilities are `kv records assets gallery timeline jobs
gallery.read_all kv.shared handoff.send`. None covers sessions, the inbox,
events or `/me`.

**Recommendation:**

| Operations | Capability |
|---|---|
| `/me`, `/events` | any valid token (events are filtered by capability) |
| sessions | `kv`, since sessions are state (R31a) |
| inbox read and consume | `gallery`, since an inbox entry is an item |
| `POST /handoff` | `handoff.send` |
| jobs | `jobs` |

A missing capability answers 403 `capability_required`, naming it. The schema
does not change. The alternative is adding `sessions` to the schema enum,
which is a schema change.

### Q9. Who may read an asset

Assets have no owner:

- Dedup gives identical bytes from two studios one row, and `origin_studio`
  names only the first.
- 07 §5: a handoff target must read the sender's asset.
- Review focus: "an id guessed from a URL."

Options:

- **(a)** Any token with `assets` reads any asset by id.
- **(b)** An asset is readable only when the caller is its `origin_studio`, or
  when it is referenced by one of the caller's items or item inputs, by an
  item in the caller's inbox, or the caller holds `gallery.read_all`. Under
  this rule a second studio whose adopt deduped onto an existing asset cannot
  read what it just adopted until it records an item.
- **(c)** As (b), plus a `studio_assets(studio_id, asset_id)` row written on
  every adopt or upload, dedup hits included.

**Recommendation: (c).** 02 §5 is amended.
- `studio_assets` is not a reclaim reference: reclaim stays as 02 §8 has it.
- Another studio's item or asset the caller may not read answers 404, not 403.

---

## C · Assets and the media engine

### Q10. Where `:adopt` takes a file from, and whether it unlinks it

The design disagrees with itself:

- 05 §3: "Adopt hardlinks a file already written to the stage directory."
- 07 §4: "hardlinks the staged file into the blob store and unlinks the stage
  entry".
- 08, h3's adopt call site: `Assets.Adopt(ctx, final, helm.Video)`, where
  `final` is in `outputs/` under `--root {data}`, and "Files stay where they
  are; adoption hardlinks."

The DoD's take goes through 08's call site. If the studio keeps a hardlink to
the blob, the studio can still write that inode. An in-place write then
silently changes a content-addressed blob, and 02 §7 never repairs a corrupt
asset.

Options:

- **(a)** Adopt only from the stage directory, and unlink after adopting. This
  is 07's wording, and h3's call site moves the take into `HELM_STAGE_DIR`
  first.
- **(b)** Adopt from stage or from `{data}`, unlinking only stage entries.

**Recommendation: (a), with blobs made read-only (`0444`).**
- A path must resolve inside the stage directory with no symlink component,
  else 422 `outside_stage`.
- Standalone, the stage directory is `./.helm/stage`.

### Q11. Adopting across volumes: never copy, so what instead?

Stage shares a volume with blobs (M1 decision), but the library root may be on
another volume. That has been open since M1: "Whoever builds adoption (M4)
needs this settled."

**Recommendation:**
- **Blob link fails with `EXDEV`** (possible only if stage was overridden onto
  another volume): refuse with 422 `cross_device`, naming both paths.
- **Library link fails with `EXDEV`:** adopt anyway, leave `library_path`
  NULL, and say so in the response as a `warnings` entry.
- Copying is rejected in both cases. A symlink in the library is also rejected:
  "deleting either one cannot lose data while the other exists" would stop
  being true.
- The library-volume question stays open for the settings UI.

### Q12. Asset reclaim: timelines, and what gets deleted

These disagree:

- 02 §5: "a timeline's clips are `item_inputs` rows on its export, so a
  sequence in progress holds its footage through the same mechanism".
- R48: "A timeline referencing an asset counts as a reference for reclaim."
- 05 §6: "the disk page can never offer to delete footage a sequence is using".

An unexported sequence has no `item_inputs`, and 02 §8's query never looks at
timelines. Separately, while the library hardlink exists, deleting only the
blob frees nothing.

**Recommendation:**
- Reclaim deletes the blob and its derived files. It deletes the library link
  only if that path is still the same inode (`os.SameFile`); otherwise it
  leaves the file and reports it.
- The query is 02 §8's, plus `NOT EXISTS` over the asset ids in
  `timelines.tracks`, so R48 holds once M8 writes timelines.
- The API mirrors M3: `GET /assets:reclaim` returns a preview with a confirm
  digest, and `POST /assets:reclaim {confirm}` deletes. This replaces the
  outline's 202 Job.

### Q13. Probing and thumbnails without ffmpeg

07 §4 has the daemon probe duration and dimensions and generate thumbnails "via
ffmpeg". The ffmpeg decision is open and gates M8, and R49 bundles ffmpeg
rather than expecting one.

**Recommendation for M4:**
- Probe images with the standard library (`image.DecodeConfig`: png, jpeg,
  gif).
- For video and audio, accept `width`, `height`, `duration_s` and `fps` as
  hints in the adopt or upload request. 08 notes h3 already has a probe.
- `/thumb` scales images with the standard library, and answers 501
  `unsupported` for video and audio until the ffmpeg decision.
- The alternative is to use an `ffprobe` found on `PATH`; h3 already requires
  ffmpeg.

### Q14. `POST /assets` uploads, and pinning

The design does not give the upload's body format, has no size cap, and has no
asset quota. 06 §4 says "User imports are `pinned = 1`", but no API lets a
studio mark an upload as a user import.

**Recommendation:**
- The body is the raw bytes, with `Content-Type` as the mime and `kind`,
  `filename` and `pinned` as query parameters.
- `pinned` is accepted on adopt too. A dedup hit sent with `pinned=true` pins
  the asset; nothing unpins.
- There is no quota in M4; it is recorded as open.

---

## D · Gallery

### Q15. Field names on the wire

- 07 §5, 05 §6 and 04 §4's samples: `"asset"` and `"session": "example"`. That
  session value is a name, and 08 passes `job.Session`, a name.
- 02 §5 columns: `asset_id`, and `session_id REFERENCES sessions(id)`.
- The outline's `Item` and every id field served today (`job_id`,
  `log_file_id`, `studio_id`) use the `_id` form.

**Recommendation:**
- The fields are `asset_id` and `session_id`, and a session is given by id,
  never by name.
- An unknown session id answers 422.
- Inputs are `[{asset_id, role}]`.

### Q16. Endpoints the design relies on but never names

- **(a) Deleting an item.** Reclaim depends on deleted items (07 §4, and
  `deleted_at` in 02 §8), but no document has a delete endpoint.
- **(b) Provenance.** 05 §5's conformance suite covers "provenance queries",
  and 02 §8 has the query, but there is no endpoint. That query also does not
  prepare (open since M1), and 06 §7 writes it differently.
- **(c) "Annotate".** 07 §3's `PATCH` does "Star, tag, rename, annotate", and
  no column holds an annotation.

**Recommendation:**

**(a)** `DELETE /gallery/items/{id}`: a soft delete, on the caller's own items
only.

**(b)** Two endpoints:
- `GET /gallery/items/{id}/lineage?direction=up` walks an item's inputs,
  recursively.
- `GET /assets/{id}/lineage?direction=down` returns everything made from an
  asset.
- Without `gallery.read_all`, another studio's items are left out of the
  result entirely.
- 02 §8's query is replaced with this one, which I ran on SQLite 3.51 and will
  prove on modernc:

      WITH RECURSIVE lineage(item_id) AS (
        SELECT item_id FROM item_inputs WHERE asset_id = ?1
        UNION
        SELECT x.item_id FROM lineage l
          JOIN items i ON i.id = l.item_id
          JOIN item_inputs x ON x.asset_id = i.asset_id)
      SELECT item_id FROM lineage;

**(c)** Drop "annotate". `PATCH` changes `title`, `starred` and `tags` (the
whole set).

### Q17. Full-text search

Both text indexes are unworkable as designed:

- `items_fts` indexes a `prompt` column that `items` does not have; the prompt
  is inside `params`. It is contentless and joined on a rowid that `VACUUM`
  may renumber (open since M1).
- `storage.collections[].fts` (records) has no table at all.

**Recommendation:**
- Schema v4 recreates the index as
  `items_fts USING fts5(item_id UNINDEXED, title, prompt, tokenize='porter unicode61')`.
- `prompt` holds `params.prompt`, maintained in the same transaction as an
  item's insert, patch or delete.
- `GET /gallery/items` gains `q=`.
- Records `fts` is ignored in M4 and recorded as open. 02 §5 and §8 are
  amended.

---

## E · Records, KV, sessions, inbox

### Q18. Records have no etag column

- 06 §6: "Insert a JSON document, returns id and etag" and "`If-Match` on the
  etag; `409` on conflict".
- 02 §5's `records` table has no `etag`. The outline has `PATCH` only, where
  06 has both `PUT` and `PATCH`.

**Recommendation:**
- Add `records.etag` in v4.
- Offer both `PUT` and `PATCH`, as 06 does.

### Q19. The filter language

06 §6 gives the shape: `field:op:value`; operators `eq ne lt lte gt gte in
contains exists`; at most eight clauses; queries "over declared fields". It
leaves these undecided:

- **Value types.** In `seed:eq:42`, `json_extract` returns INTEGER 42, and a
  TEXT `'42'` bound against it never matches.
- How `in` lists values, what `contains` means, and what `exists` takes.
- Nested paths, which fields `order` accepts, and whether a field not listed
  in `index` may be filtered.

**Recommendation:**
- **Fields:** a top-level key matching `^[A-Za-z_][A-Za-z0-9_]*$`, or
  `id`/`created_at`/`updated_at`. There are no nested paths.
- **Values:** parsed as JSON when valid (`42`, `true`, `null`, `"42"`),
  otherwise taken as a literal string.
- **`in`:** the value is a JSON array.
- **`contains`:** a case-sensitive substring when the stored value is a
  string, and membership when it is an array.
- **`exists`:** takes `true` or `false`.
- **`order`:** `order=field:asc|desc` on one key, defaulting to
  `created_at:desc`, with `id` breaking ties.
- **Undeclared fields:** allowed. `index` affects speed only.
- **Errors:** bad syntax or more than eight clauses answers 400 `bad_filter`.

### Q20. Quotas

06 §6 makes quotas per studio, answered with "a `429` with the number in it".
`storage.quota` is optional, and the design leaves three things open:

- What applies to a manifest with no quota.
- Whether soft-deleted records count.
- Whether session state counts toward `kv_bytes`.

**Recommendation:**
- The defaults are 100000 records and 8 MiB of `kv_bytes`, the values in 01
  §13.
- Records counts rows that are not deleted.
- `kv_bytes` is kv document bytes plus session state bytes.
- Over quota answers 429 `quota_exceeded` with details `{quota, limit, used}`.

### Q21. Addressing the `kv.shared` namespace

07 §3: `kv.shared` lets a studio "read/write the shared namespace". Nothing
says how it is addressed. `kv`'s primary key is `(studio_id, ns, key)` in a
`WITHOUT ROWID` table, and SQLite forbids NULL in such a key.

**Recommendation:**
- `ns = shared` is reserved as the shared namespace, stored under `studio_id = ''`.
- It requires `kv.shared`, and writes count toward the writer's quota.

### Q22. Draining the inbox

07 §3: "Drained by the studio on read." The M0 outline adds "GET itself is the
drain." A GET that consumes can be retried or prefetched by HTTP machinery,
and a studio that crashes after the response has lost the handoff.

**Recommendation:**
- `GET /inbox` lists what is pending without consuming it.
- `POST /inbox/{id}:consume` sets `consumed_at`.

This amends 07 §3. The alternative is GET as the drain, literally.

### Q23. The handoff fallback

R39: "A studio that has not implemented an inbox receives the file in its stage
directory instead." No manifest field tells the daemon whether a studio has an
inbox, and a stage directory exists only while the studio runs.

**Recommendation:**
- `POST /handoff` always writes an inbox row and an event.
- Delivering a file into the stage directory belongs to the launcher's "Use
  in…" and is deferred to M6. There is a related risk: hardlinking a blob into
  a stage exposes its inode to the studio.

---

## F · Jobs

### Q24. What `POST /jobs` creates

R40 and 05 §3 say "the studio's own renders can use it too". But:

- The daemon does not run a studio's render, so it can neither cancel it nor
  write its log.
- The `jobs.kind` CHECK has no kind for a studio's work.
- `log_files.owner_kind` allows only `process` and `step_run`.

Options:

- **(a) Studio-reported jobs.**
  - `POST /jobs` creates a `kind = 'task'` job.
  - `PATCH` updates progress and state.
  - `POST /jobs/{id}/logs` appends lines to a log file.
  - `:cancel` records a request, delivered as a `job` event; the studio ends
    the job `cancelled`.
  - Needs: v4 rebuilds `jobs` with `task` in its CHECK and a
    `cancel_requested_at` column, and `log_files` gains owner kind `job`.
- **(b) Defer `POST /jobs`.** A studio only reads and cancels its own jobs.

**Recommendation: (a).** The milestone lists jobs as a platform service.
Either way a studio token sees only its own studio's jobs, and cancels only
jobs it created.

---

## G · Providers, conformance, `helm dev`

### Q25. Embedded-provider languages, and sharing the enforcement code

- 04 §4: "an extra in Python (`helm-runtime-sdk[embedded]`)" and "Every
  generated client runs the same conformance suite against both providers."
- Review focus: "Do the two providers share the code that enforces the
  contract, or duplicate it?"

A Python embedded provider is, by construction, a second implementation of
quotas, filters, dedup and adopt. Sharing code between the daemon and a Go
embedded module forces two consequences:

- `internal/` cannot be imported from another module, so the store and its
  migrations move into the embedded module, with `internal/store` wrapping
  them.
- `store.Open` takes its lock through `internal/platform`, so that module needs
  its own lock seam. That is OS-specific code outside `internal/platform`: it
  needs an exception in `make boundaries`, or a small platform package inside
  the embedded module that the check is widened to allow.

**Recommendation:**
- M4 builds one embedded provider, in Go.
- The daemon's handlers are thin HTTP over the same Go enforcement package the
  embedded provider exposes in-process.
- The conformance suite is Go, written against the generated Go interface. It
  runs against the daemon over HTTP and against the embedded provider in
  process.
- The Python and Node clients get a smoke test against the daemon only.
- Python's `[embedded]` is decided in M5 (for example, it drives `helm dev`'s
  server), and is recorded as open.

### Q26. Where `helm dev` keeps things, and what it runs

05 §5a describes `helm dev` as "the daemon restricted to one studio … same
embedded store behind the same HTTP surface". The embedded store is
`./.helm/helm.db`. But M1's `HELMSTUDIO_HOME=<d>` layout would put the database
at `<d>/data/helm.db`.

The design does not say:

- The layout under `./.helm`.
- Whether `helm dev` runs `build[]`.
- How weights get linked.
- Where the studio id comes from standalone.

**Recommendation:**

**Layout:** `./.helm/{helm.db, assets/, library/, stage/, logs/, data/}`.
- `{data}` is `./.helm/data`.
- The models root is `HELMSTUDIO_MODELS_DIR` if set, else the default models
  root.

**What it runs:**
- The studio root is the directory holding `helmstudio.yaml` (or `-f <path>`).
- It does not run `build[]`.
- It records an installation as `ready` at that root.
- It serves on a free loopback port and injects the same environment the
  daemon does.

**Weights:** `helm dev --link <weight>=<dir>` records a linked artifact. It
never downloads.

**In-process embedded provider (no `helm dev`):**
- It uses `./.helm` relative to the working directory, or `HELM_DIR`.
- The studio id comes from `HELM_STUDIO_ID`, else `./helmstudio.yaml`'s `id`.
- A second process of the same studio fails on the single-writer lock, and the
  error names `helm dev`.

**Out of M4:** `--fixtures` and `--fail=`, which the milestone does not list.

### Q27. Browser clients

04 §1 and §4 promise a Node package "with a browser ESM build … precisely what
`helm-ui-sdk` consumes", and 04 §5 calls `fromEnv()` in a page. Two things
block that today:

- A browser has no environment holding `HELM_API` or `HELM_TOKEN`.
- M2 refuses any `Origin` but the daemon's own, so a studio page on
  `127.0.0.1:8710` calling port 8700 gets a 403.

Giving a page the token would also expose it to every script on that origin.

**Recommendation:**
- M4's clients are server-side: Go, Python and Node.
- How a page gets a client (CORS for a running studio's origin, token delivery,
  or the studio's own server proxying) is decided with `helm-ui-sdk` in M6, and
  recorded as open.

---

## H · Housekeeping that changes the contract

### Q28. The file list

M4's list leaves out files its tasks need:

| Path | Why |
|---|---|
| `internal/supervisor/**` | Inject `HELM_API`, `HELM_TOKEN`, `HELM_STAGE_DIR` and `HELM_STUDIO_ID` at spawn; create and clear the stage directory; revoke tokens on stop. M2 recorded all three as M4's. |
| `internal/manifest/**` | `storage` is not in the manifest types. |
| `web/**` | Only if Q5 changes the launcher's shapes. |
| `docs/design/02-data-model.md` | The v4 DDL: Q6, Q9, Q16, Q17, Q18, Q24. |
| `docs/design/04`, `05`, `07` | Text amendments: Q4, Q8, Q10, Q16, Q22. |
| `go.mod` | The module split and `replace` (Q2, Q25). |
| `Makefile` | The DoD puts drift and conformance in the gate. |
| `internal/platform/**` | Only if the lock seam is shared (Q25). |

`HELM_THEME` and `HELM_ACCENT` are not injected in M4:
- No theme setting exists yet.
- `hue` has a dark and a light value, but 07 §3 has only one `HELM_ACCENT`.

Both belong to M6.

**Recommendation:** widen the list to the table above.

**Answered by the human: allowed, every row.**

### Q29. Ids and timestamps

- 07 §4, 05 §7 and 04 §5 show prefixed ids: `as_01JB9…`, `tl_…`, `jb_…`.
- 06 §5 says "ids are ULIDs", and every id served today (jobs, processes,
  artifacts) is a bare ULID.
- 06 §5 stores timestamps as Unix milliseconds; M3 serves RFC 3339.

**Recommendation:**
- Ids are bare ULIDs everywhere.
- The API uses RFC 3339 timestamps, and the database keeps milliseconds.

### Q30. 02 §11 contradicts R31

- 02 §11 says: "Its sessions, prompts, seeds and working files stay in its own
  directories in its own shape."
- R31, 02 §5, 08 and the storage decision all say all studio state goes
  through the SDK.

This does not block the API.

**Recommendation:** amend 02 §11 to match R31.

---

## Defaults I will take unless told otherwise

**ETags and documents**
- **ETags** are strong and opaque, sent both as the `ETag` header and as
  `etag` in the body.
- **`If-Match`** is optional; without it, a write is unconditional.
- **KV documents** must be JSON objects. `PATCH` is an RFC 7396 merge patch.
  `GET /kv/{ns}` returns a page of `{key, bytes, updated_at}`.
- **Request bodies** for kv, records, sessions and items are capped at 1 MiB,
  answered with 413.

**Sessions**
- `PATCH` changes `name` and/or `state` (replacing it), with `If-Match`.
- `:duplicate` takes `{name}`; a name already in use answers 409 `name_taken`.
- `:activate` sets `opened_at` and answers 204.
- Lists are ordered by `opened_at` descending (nulls last), then `created_at`.

**Items**
- `kind` is one of `image video audio other`.
- A `role` matches `^[a-z][a-z0-9_]*$`.
- `params` must be an object, and may be empty.
- Tags: at most 32 per item, 1–64 characters each.
- Filters: `scope=self|all`, `studio`, `kind`, `tag` (repeatable; all must
  match), `session_id`, `starred`, `since`, `until`, `q`, `asset_id`.

**Paths on disk**
- **Blob:** `assets/blobs/<ab>/<cd>/<sha256><.ext>`, the extension taken
  lowercased from the source name.
- **Derived files** go under the cache root, as `derived/<sha256>/`. 06 §4's
  roots table puts them in cache; its own tree and 07 §2 put them under
  `assets/`.
- **Library:** `library/<studio>/<YYYY-MM>/<source basename>`, suffixed `-2`
  and so on when a name is taken. There is one library link per asset, made by
  whichever studio adopts it first.
- **Stage:** `<data>/stage/<studio>/<group_run_id>/`, created at launch and
  removed through `os.Root` when the group stops.

**Serving an asset**
- `http.ServeContent`, with `ETag` set to the sha256.
- An asset whose file is gone answers 410 `asset_missing`.

**`/me`** returns:
- `studio_id`, `capabilities`
- `quota`, as `{records: {limit, used}, kv_bytes: {limit, used}}`
- `paths`, as `{stage, data}`
- `provider` (`daemon` or `embedded`), `api_version`, `daemon_version`

**Events**
- The event names are `theme`, `inbox`, `item`, `job` and `shutdown`.
- An in-memory ring of 1024 events backs `Last-Event-ID`.
- A studio receives events for its own items and jobs; other studios' items
  only with `gallery.read_all`.
- `theme` is documented now; M6 emits it.

**Handoff**
- The body is `{item_id, to_studio, role?}`.
- The target must be a known studio. A handoff to the sender itself answers 422.
- The sender must be able to read the item.
- The inbox row lets the recipient read that item and its asset.

**Environment at spawn**
- `HELM_API` and `HELM_STUDIO_ID` are always injected.
- `HELM_TOKEN` is injected only when the studio declares capabilities
  (07 §3).
- A manifest's `env` may not set any `HELM_*` variable.

## Notes, no decision needed

- The milestone says "Store schema v2", but v2 and v3 already exist (M2, M3),
  so this is schema v4.
- These are outside M4's tasks: `helmstudio fsck` (R42), `helm adopt`,
  `helm test`, `helm studio init`, and `helm dev --fixtures`/`--fail`.
- **Machine-bound DoD items:** "`helm dev` runs h3 with no daemon present" and
  "h3 writes a real take with its inputs recorded in `item_inputs`" both need
  the h3 checkout, a Metal build and weights. They will be listed under "Could
  not verify" with a script to follow, not reported as passing.
