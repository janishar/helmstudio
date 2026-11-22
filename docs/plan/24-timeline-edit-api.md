# helmstudio-next plan — Timeline edit API: edits by agents, without the browser

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-22); the first slice (today's edits) built (2026-11-23)**, as-built notes in
28.15 and 28.16. Plan 23's ops, links and markers and exact frames are still to come. API version: **1.49.0** (main was at 1.48.0, plan 22's
thumbnails, when the first slice was built; plan 23's links and markers are still unbuilt, so numbers are not landing
in plan order). A plan that lands after it renumbers then, never below main's version. Plan 23's new ops need a bump here once `TimelineOp` is in
openapi (28.10).

The owner's ask, 2026-11-22: "if I have to power the coding agent to plan and edit the clips, what's the standard way
without the browser", then "Yes, plan it".

The short answer: the same thing every scriptable editor offers, a list of **named edit operations sent to the
server**, which applies them with the one set of rules, answers the new document and a diff, and can try them first
without saving (dry run). An agent reads the sequence, plans ops, dry-runs them, then applies them. `helm mcp`
([16-mcp.md](16-mcp.md)) and the CLI expose that as tools; the in-app assistant may too (open point 1).

Facts come from a read of the code on 2026-11-22: `studio_service/api/timeline/edit.go` (`Op`, `Edit`, `Apply`),
`studio_service/api/studio/timelines.go` (`UpdateTimeline`, `store`, `keptRevisions`, the fill path),
`studio_service/migrations/00019_timelines.sql`, `web_service/lib/timeline.ts` (`Op`, `edit`, `apply`, `newId`),
`contracts/openapi.yaml` (the timeline paths), `contracts/README.md` (Timeline; Frames), `contracts/check.py`
(`t_edit`, `t_apply`), `contracts/tools/catalog.json` (`list_timelines`, `show_timeline`), `catalog.schema.json`, `actions.json`
(`timeline.edit`), `contracts/messages/cmd.media.job.schema.json` (`frame`), `cli/timeline.go`, plans 16, 18 and 23.

## 28. Timeline edit API

### 28.1 What exists today

- **Edits are computed in the browser.** `lib/timeline.ts` `apply` (edit, then normalize) makes the new document;
  `PUT /v1/studio/timelines/{id}` stores the **whole document** with the `revision` it was made on. Studio only
  normalizes (checks, snaps, lays V1) and stores revision + 1; stale = 409 `timeline changed since you opened it (now
  revision <n>)`; a broken rule = 422 `TimelineError` with `problems`. The README says why: one validator in Go, edit
  maths as shared pure functions, "a named-op API would duplicate each edit in Go".
- **That duplication already happened.** `api/timeline/edit.go` is a full Go port of `t_edit` (every op of today's
  union; its error codes are `unknown_op`, `unknown_clip`, `unknown_track`, `take_not_found`, `split_edge`,
  `split_none`, `split_shot`, `lift_track`, `video_track`, `too_many_tracks`, `not_alternative`), `Apply` = edit +
  `Normalize` against the document before, and `TestEditFixtures` runs it against `contracts/fixtures/timelines/edits.json`.
  Studio itself runs it only for `add` (`POST …/clips`), `insert` / `place` (shots, lines) and `fill` (the projector).
  So every edit lives three times: `check.py` (the reference), `lib/timeline.ts`, `edit.go`, held equal by one fixture
  file.
- **Today's ops**: `move` (V1 by index, audio by `at`), `trim {clip, edge, delta}`, `split {track, at, id}`, `remove`,
  `lift`, `replace`, `set`, `add {take_ids, track, ids}`, `insert`, `place`, `fill`, `add_track`, `remove_track`,
  `track`. `fill` is studio's (the projector filling a slot with its shot's take); the web never sends it. Plan 23
  (decided, not built) adds `roll`, `slip`, `slide`, `overwrite`, `split_all`, `remove_range`,
  `lift_range`, `remove {ripple}` and, in 1.46, `link` on clips and `markers` on the document.
- **New clip ids are made by the caller.** `split`, `insert`, `add`, `split_all`, `overwrite` and the range ops carry
  the ids of the clips they create; the web makes them with `newId` (8 random `[a-z0-9]`, unused in the document).
- **Revisions exist.** `timeline_revisions(timeline_id, owner_id, revision, doc, by user|studio, undo_to, redo_to,
  saved_at)`, the last **100** kept (`keptRevisions`). `POST …/undo` and `…/redo {revision}` write an old document
  forward as a new revision; studio writes (fills) are skipped by undo. There is no read of the history.
- **Frames.** `POST /v1/studio/takes/{take_id}/frame {at: first | last}` makes a first or last frame take at once (an
  object copy of the poster or the stored last frame), idempotent per (take, `at`); only a video take made before
  1.17 needs the media job `frame` (202). That job's message (`cmd.media.job` `frame`) carries `at: first | last` and
  an `index`, so a frame **at any time does not exist yet**: it needs the enum widened, the runner's media worker
  and a new idempotency key. Plan 22 adds one thumbnail per take.
- **Tools.** The shared catalog has `list_timelines` and `show_timeline` (read, `assistant` + `mcp`); its description
  already says timeline write tools come with `helm mcp`. Its schema (`catalog.schema.json`, closed) has `read_only`
  and `card` but **no destructive field**. Plan 16 lists `add_to_timeline` and `export_timeline` and leaves out "the
  Timeline's clip-level editing". The assistant (plan 18) has **no server write tools except memory**: its writes are
  browser screen actions and cards, and **`timeline.edit {ops}` already exists** in `contracts/tools/actions.json`
  (effect `undo`, at most 32 ops, today's op names) run by the open Timeline tab through `lib/timeline.ts` and the
  PUT.
- **No idempotency key** anywhere in the timeline API; the `revision` check is the only guard against a double write.

### 28.2 The endpoint

`POST /v1/studio/timelines/{timeline_id}/edits`

```json
{"base_revision": 41, "ops": [{"op": "split", "track": "V1", "at": 12.5}, {"op": "remove", "clip": "k3"}],
 "dry_run": false}
```

- **Applies the ops in order** with `edit.go` `Apply` (edit, then normalize) on the current document, which must be
  `base_revision`, with the sequence row locked (`editTimeline`, as the PUT): the same rules and fixtures as
  `check.py` and `lib/timeline.ts`. The takes every op names (`take_id`, `take_ids`) are loaded once with the
  document's (`loadTakes`, owner-scoped). All or nothing: one `storeUser` for the whole list, so one undo undoes the
  call. **`fill` is refused** (422 `fill is studio's`): it is the projector's op, and a slot's shot is studio's.
- **200** `{revision, timeline, diff, warnings}`: `timeline` as `GET …/{id}` answers it (normalized, `takes`
  signed); `diff` (28.3); `warnings` = what studio changed beyond the ops (a time snapped to the frame grid, a dissolve
  dropped by a `fill`), usually empty, never a refusal.
- **`dry_run: true`**: the same answer with `revision` = `base_revision` and nothing stored (the transaction rolls
  back). The agent's "try it". A dry run on a stale `base_revision` is the same 409, so a dry run's diff is always the
  diff an apply at that revision would make.
- **409** when `base_revision` is not the current one: the existing `staleRevision` answer, `{detail: "timeline
  changed since you opened it (now revision <n>)"}`, unchanged (errors stay `{detail}`; the agent re-reads anyway).
  No silent re-apply on the server: the agent re-reads and re-plans (the web's 409 re-apply stays a web behaviour).
  `base_revision` is **required** (open point 5); `POST …/clips` keeps applying to the current revision.
- **422 per op**: `{detail: "ops[3]: split_edge", op_index: 3, code: "split_edge", problems: [...]}`, a
  `TimelineError` with two more fields: `edit.go`'s codes (28.1; plan 23 adds `slip_still`, `least`, `slide_end`,
  `range_shot`) through `editError`'s texts (`unknown clip <id>`), and normalize's problem codes (`overlap`,
  `past_end`, `dissolve_handles`, `too_many_clips`, …) for the document after that op. Nothing is stored. A body that
  does not fit the schema is the usual `{detail}` 422.
- **Owner scoping**: RLS (`WithUser`) as every timeline route; another user's sequence is 404, a take another user owns
  named in an op is `take <id> not found` (422, `take_not_found`), never a leak.
- **Limits**: at most **200 ops** per call (an agent's plan for a whole cut fits; one call stays under one transaction
  and one normalize per op, each O(clips)), the body under the existing 256 KB document cap, the result under the
  document caps (1000 clips, 500 per track). Over the op cap: 422 `too_many_ops`. The assistant's `timeline.edit`
  screen action keeps its own 32.
- **Ids for new clips**: as today, an op may carry the ids of the clips it makes (plan 23's rule: `id` / `ids`, in
  track order, `[a-z0-9]{1,16}`, unused). New here: **absent ids are made by studio** (the `newId` rule, Go side) and
  answered in `diff.added`, so an agent need not invent ids. A later op in the same call may name a new clip only by
  an id the caller gave (open point 6). Studio fills them before `Edit` (`newClipID`, as `addOp` does today), since
  `edit.go` uses the op's `id` / `ids` as given.
- **A dropped connection**: with `base_revision` required, a retry of an edit that landed is a 409, never a double
  write; the agent then reads `GET …/revisions` (28.4: `via`, `ops_summary`) to see whether its edit is the newest.
  So no `Idempotency-Key` in the first slice (open point 11); if added later it needs a hash of the body so a reused
  key with other ops is a 422, not the old answer.
- **Revision author**: stored with `by = user` (the caller), plus a new `via` (`web` | `api` | `mcp` | `assistant`)
  so the history can show who made an edit. `via` comes from a request header Kong passes (`X-Helm-Client`, set by
  the web, `helm` and `helm mcp`; absent = `api`); it is a label, not a permission. Undo treats it as a user write.

### 28.3 The diff

What an agent needs to check its own work without re-reading 1000 clips:

```json
{"added": ["k9"], "removed": ["k3"], "changed": [{"clip": "k5", "fields": {"in": [2.0, 2.5], "at": [10.0, 9.5]}}],
 "tracks": {"added": ["A4"], "removed": []}, "duration": [62.0, 58.5], "markers": {"added": [], "removed": []}}
```

Computed by comparing clip ids of the two normalized documents (fields `take_id`, `in`, `out`, `hold`, `at`, `gain_db`,
`mute`, `transition_in`, `alternatives`, `shot`, `offline`, `track`, and `link` from plan 23's 1.46 part); V1 clips
store no `at`, so the diff uses their computed start and a V1 ripple shows as `at` changes on the later clips; a
track's `gain_db` / `mute` change shows under `tracks.changed`. One Go function plus
its `check.py` reference and a `diffs.json` fixture file; the web does not need it.

### 28.4 Revisions and restore

- `GET …/timelines/{id}/revisions?before=<n>&limit=<k>` (default 20, max 100): `[{revision, by, via, saved_at,
  ops_summary, clips, duration}]`, newest first, the kept 100 only. `ops_summary` = the op names of the write
  (`split, remove`) stored with the revision when it came through `/edits` (a PUT: `edit`; studio: `fill`).
- `GET …/timelines/{id}/revisions/{n}`: that revision's document (normalized, as stored), for an agent comparing.
- `POST …/timelines/{id}/restore {revision: <current>, to: <n>}`: writes the document of revision `n` as a new
  revision (never rewrites history), `undo_to` = the current, `redo_to` = null, then **reconciled** as undo is (a slot
  whose shot already ended is filled or loses its shot). 409 on a stale `revision`; 422 `revision <n> is not kept`.
  It is today's `restore` (undo/redo) with the target given instead of read from the pointers: a small change.
  This is undo to any point, the safety net for an agent's run of edits.
- Kept: still **100**. An agent making many small calls can push a good state out; the recommendation is to keep 100
  and tell agents to batch ops (open point 4).

### 28.5 Reading for agents

`show_timeline` today returns the raw `Timeline`. Agents need it flat and in both units:

- **`GET …/timelines/{id}?view=agent`** (or a tool-side reshaping in studio's tool handler; open point 7): per clip
  `{id, track, index, take_id, take_title, kind, in, out, hold, at, end, length}` with every time in **seconds and
  frames** (`at_f`, `length_f`, on the target's grid), `transition_in`, `shot` status, `link`, `offline`; per track
  `{name, gain_db, mute}`; `markers`; linked groups as `{v1_clip: [audio clips]}`; `revision`, `duration`, `fps`.
  Plain data, no signed URLs (an agent reads a frame by `look_at`).
- **A frame at a time, for an agent to look**: `POST …/timelines/{id}/frame {at}` maps sequence time to the V1 clip
  under it and its take time (`t − at + in`, snapped to the take's frames) and answers: an empty slot =
  `{empty: true}`; an image take = the image (or plan 22's thumbnail) at once; a video take = the **nearest stored
  frame** (the poster at frame 0 or the stored last frame, an object copy, as `…/takes/{id}/frame`), with the frame
  index it shows, so the agent knows how near it is. Recommended for the first slice (open point 8). An **exact
  frame** is later: the media job `frame` with `at` widened to `first | last | index` (`cmd.media.job` and the
  runner's media worker), idempotent per (take, index), 202, a runner's ffmpeg (about 0.3–1 s, no GPU), its image a
  frame take under `u/<owner>/frames/` that the Gallery would then list (or a kept-out kind). Dissolves and audio are
  not composited: the answer is the V1 clip's own frame. The MCP tool answers it as image content (`helm mcp`
  downloads the signed URL); the in-app assistant keeps `look_at` on takes.

### 28.6 Tools (contracts/tools/catalog.json)

| Tool | Surfaces | Does | API |
|---|---|---|---|
| `show_timeline` (changed) | assistant, mcp | the agent view (28.5) | `GET …/{id}?view=agent` |
| `try_timeline_edit` (new, read) | mcp | `{timeline_id, base_revision, ops}` → the dry run's result or per-op error | `POST …/{id}/edits` with `dry_run: true` |
| `edit_timeline` (new) | mcp; assistant only per open point 1 B | `{timeline_id, base_revision, ops}` → result or per-op error | `POST …/{id}/edits` |
| `timeline_revisions` (new, read) | assistant, mcp | the history | `GET …/{id}/revisions` |
| `restore_timeline` (new) | mcp | back to a kept revision | `POST …/{id}/restore` |
| `timeline_frame` (new, read) | mcp | a frame at a time as MCP image content | `POST …/{id}/frame` |

- The `ops` input schema is the op union written out in the catalog (self-contained, no `$ref`, as the catalog's
  rule says), each op with a one-line description an LLM can follow ("`slip {clip, delta}`: shift the clip's source
  in and out by `delta` frames; length and position stay"). It leaves out `fill` (28.2).
- **Destructive**: MCP annotations are **per tool, not per call**, so the dry run is its own read-only tool
  (`readOnlyHint: true`, no prompt) and `edit_timeline` and `restore_timeline` are `destructiveHint: true` (an
  apply can remove, lift, overwrite or replace clips; restore replaces the current cut, though undo brings it back):
  the client asks before each apply, never before a try. The catalog schema gains a `destructive` boolean (it is
  closed today) that `helm mcp` maps to `destructiveHint`; `read_only` maps to `readOnlyHint`. Deleting a sequence
  stays out.
- `helm mcp` registers every tool marked `mcp` from the copied catalog (plan 16 20.2); `add_to_timeline` stays a
  task-sized shortcut over `POST …/clips`.

### 28.7 The in-app assistant (owner decision)

Plan 18's rule is **no server write tools except memory**: the assistant's writes are screen actions the user sees
happen, costly ones behind a card. Two ways to let it edit the cut:

- **A (recommended): keep screen actions.** This is what plan 18 already has: `timeline.edit {ops}` in
  `actions.json`, run by the open Timeline tab through `lib/timeline.ts`, saved by the PUT today and through `/edits`
  with 28.8 A. The user sees every edit as it happens and ⌘Z works as for their own. Needs the Timeline open in that
  tab (the assistant navigates there first, as for any screen action). No rule change and no new work here beyond
  adding plan 23's op names to the action's enum when they exist. `restore` stays a web Undo, not an assistant tool.
- **B: a server tool behind a card.** `edit_timeline` on the assistant surface with `card: timeline_edit`: the
  assistant dry-runs, the card shows the diff ("Cut Shot 3 at 0:10.12, remove Shot 4, ripple 3.5 s") with Apply /
  Skip, and Yes applies with the dry run's `base_revision`. Works with the Timeline closed, but it is the first
  assistant write tool outside memory, and a card per apply is heavy for a 20-step cut.

### 28.8 The web editor (owner decision)

- **A (recommended): keep computing locally, save through `/edits`.** The drag draft and the result still come from
  `lib/timeline.ts` (instant, offline-tolerant), but the save sends the **op** (not the document) to `/edits` with the
  revision; the server's answer is the truth and the web replaces its draft with it. The server's document wins if the
  two ever differ (logged as a parity bug, caught by the fixtures first). Gives one write path for web, CLI, MCP and
  assistant, `via` and `ops_summary` in history, and lets the PUT be retired later.
- **B: keep the PUT as is.** No web change; agents use `/edits`, the web writes documents. Two write paths, history
  without op names for web edits.
- **C: send every edit and wait.** One source of truth with no local edit code, but a round trip on every gesture and
  no draft while dragging; not for an editor.

### 28.9 Keeping the implementations equal

Three copies of the edit rules is the real cost. Options:

- **A (recommended): keep three, held by fixtures.** `check.py` stays the reference that defines a rule; `edit.go`
  (now the write path, so it matters most) and `lib/timeline.ts` (drafts) pass every case in `edits.json`; plan 23's
  new ops land in all three in its phase 2, with a fixture per error code. Add a cross-check: `make contracts-check`
  replays every `edits.json` case through all three and compares the normalized documents byte for byte.
- **B: two (drop `check.py`'s edit code).** `edit.go` becomes the reference; `check.py` keeps only fixture validation.
  Saves one copy but loses the Python reference every other rule has; the house pattern says the reference is Python.
- **C: one (Go compiled to WebAssembly for the web).** One copy, but a Go-wasm build in `web_service` (a toolchain and
  about 2 MB more to load) for drafts during drags; not worth it at this size.

### 28.10 Contract changes (API 1.49.0)

- `openapi.yaml`: `POST …/edits` (`TimelineEdits` request, `TimelineEditResult` with `diff`, `TimelineEditError` =
  `TimelineError` plus `op_index`, `code`), `GET …/revisions`, `GET …/revisions/{n}`, `POST …/restore`, `POST
  …/frame`, `view=agent` on GET (`TimelineAgentView`), `TimelineOp` as the op union (today it is only implied by
  README prose and the web type; from then on each new op, plan 23's included, is an openapi change and a version).
- README Timeline: edits by ops, the 200-op cap, studio-made ids, `fill` refused, `via`, restore, the agent view,
  frames at a time.
- `catalog.json` (28.6), `catalog.schema.json` (`destructive`), and `check.py`: `t_diff`, `t_agent_view`,
  `frame_at_time` with fixtures (`diffs.json`, `agent_view.json`, `frame_time.json`).
- Migration (studio): `timeline_revisions` gains `via text` and `ops_summary text`. No backfill: old rows read `via`
  null as `web`.

### 28.11 Order

Needs plan 23's ops for the useful set (roll, slip, slide, blade everywhere, ranges) and `helm mcp` (plan 16) for the
MCP part. Recommended: **a first slice now with today's ops** (endpoint, dry run, diff, ids, revisions, restore,
agent view, stored frames, the CLI `helm timeline edit -f ops.json [--dry-run]` next to today's `show` / `add` /
`export`) since `edit.go` already covers them; plan 23's ops join the endpoint when its phase 2 adds them to
`edit.go`, at the cost of their `TimelineOp` and catalog entries (about 1 h, in that phase); the tools register when
`helm mcp` is built. Open point 2.

### 28.12 Tests

- Go: `TestEditFixtures` unchanged plus `/edits` handler tests on a scratch database: all-or-nothing on a failing op
  (nothing stored, `op_index` right), 409 on a stale apply and a stale dry run, dry run stores nothing, a retry of a
  landed edit is a 409 (no double write), `fill` refused, another user's sequence 404 and take 422, 200-op cap,
  studio-made ids unique, one undo undoes a many-op call, restore reconciles a finished shot, pruning keeps 100.
- `check.py`: the new fixture files; the three-way replay of `edits.json`.
- Contract suite (`make test` on `helmstudio-test`, fake mode): the CLI edits a three-clip sequence by ops, dry run,
  apply, restore, a 409 from a parallel write.
- MCP (when plan 16 is built): a scripted client lists tools (`try_timeline_edit` read-only, `edit_timeline` and
  `restore_timeline` destructive), tries and applies an edit, reads a frame.
- Web (with 28.8 A): a gesture saves through `/edits`; a 409 re-apply still works and its failure shows plan 23's
  notice.

### 28.13 Phases

1. **Design**: this plan, decided by the owner.
2. **Contract**: openapi (next free version), README, `TimelineOp`, `check.py` `t_diff` / `t_agent_view` /
   `frame_at_time`, fixtures, catalog entries and the `destructive` field, the three-way replay (6 h).
3. **Studio**: `/edits` (apply loop, per-op errors, ids, `fill` refused, `via`), diff, migration, revisions list
   and read, restore, agent view, stored frame at a time, handler tests (12 h).
4. **CLI**: `helm timeline show --agent`, `edit -f [--dry-run]`, `revisions`, `restore` (3 h).
5. **Web** (28.8 A): save gestures through `/edits`, the 409 re-apply on the new answer, `X-Helm-Client` (4 h).
6. **Tools**: catalog wiring in studio's tool handler (1 h); MCP registration when `helm mcp` exists (in plan 16's
   own hours); the assistant per open point 1 (A: nothing new; B: the card and diff view, 4 h).
7. **Gates**: studio, web and CLI `make test lint`, `make contracts-check`, `make test` on `helmstudio-test` (2 h).

About 28 h with assistant option A (24 h without phase 5); B adds 4 h. Exact frames (open point 8, later) add about
5 h (media job contract, runner, tests). Plan 23's new ops add about 1 h each phase beyond their own `edit.go` port
(their `TimelineOp`, catalog and action enum entries).

### 28.14 Open points for the owner

**Decided by the owner, 2026-11-22 (one by one, all the recommended options):** 1. The assistant keeps plan 18's
browser action `timeline.edit`. 2. A first slice now with today's ops; plan 23's ops join when built; MCP tools when
helm mcp lands. 3. The web editor keeps local drafts and saves each op through `/edits` (the server's answer wins).
4. 100 revisions kept; agents batch ops. 5. `base_revision` required. 6. Studio makes missing clip ids and returns
them in the diff. 7. `view=agent` on the existing GET. 8. Stored frames now, exact frames later. 9. Keep the three
implementations, replayed against each other in `make contracts-check`. 10. At most 200 ops per call. 11. No
idempotency key now. 12. `try_timeline_edit` is its own read-only MCP tool; `edit_timeline` and `restore_timeline`
are destructive.


Each with the recommended option first.

1. **The assistant**: keep plan 18's browser screen action (`timeline.edit {ops}`, already in `actions.json`; no new
   server write tool) · a server `edit_timeline` behind a card per apply showing the dry run's diff.
2. **Order**: a first slice now with today's ops, plan 23's ops join when built, tools when `helm mcp` lands · wait
   for plan 23 and plan 16 and build it all at once.
3. **The web editor**: keep local drafts, save the op through `/edits` (one write path, server answer wins) · keep the
   PUT (two write paths) · send every edit and wait (no local edit code; laggy).
4. **Revisions kept**: 100 as today, agents told to batch ops · 500 (jsonb per revision, up to 256 KB each, about
   128 MB per busy sequence at worst) · 100 plus a named "checkpoint" an agent can pin that pruning skips.
5. **Calls without `base_revision`**: refused for `/edits` (always optimistic concurrency; it is also what makes a
   retry safe) · allowed when every op names clips by id (as `POST …/clips`).
6. **New clip ids**: studio makes absent ids and answers them in `diff.added`; a caller that needs to chain ops in one
   call gives its own · the caller must always give them (as today).
7. **Agent view**: a `view=agent` query on the existing GET (one route, documented in openapi) · reshape only inside
   the tool handler (no API change, but CLI users do not get it).
8. **Frame at a time**: stored frames only in the first slice (image, poster, last frame; the answer says which
   frame), exact frames later on `exact: true` · exact from the start (a media job contract and runner change, about
   5 h more, frames kept as takes) · no frame tool (agents use `look_at`-style reads of whole takes).
9. **Implementations**: keep three held by fixtures plus a three-way replay in `make contracts-check` · drop
   `check.py`'s edit code (Go is the reference) · one Go copy compiled to WebAssembly.
10. **Ops per call**: 200 · 50 · 1000.
11. **Idempotency key**: not now (required `base_revision` already prevents a double write; the history shows
    whether an edit landed) · an `Idempotency-Key` header stored per revision with a body hash (a column, an index,
    about 2 h).
12. **Dry run as its own MCP tool**: yes, `try_timeline_edit` read-only so clients never prompt for a try · one
    `edit_timeline` with `dry_run` (every try prompts, since hints are per tool).

### 28.15 As built: the first slice (phases 2, 3, 4 and 6; 2026-11-23)

Built with today's ops only (plan 23's new ops, links and markers are not here). Phase 5 (web) came after: 28.16.

- **Contract (API 1.49.0).** openapi: `POST …/edits` (`TimelineEdits`, `TimelineOp` as a `oneOf` of today's ops
  without `fill`, `TimelineEditResult {revision, dry_run, timeline, diff, warnings}`, `TimelineDiff`,
  `TimelineEditError`), `GET …/revisions` (`TimelineRevisionList`), `GET …/revisions/{revision}`
  (`TimelineRevisionDoc`), `POST …/restore` (`TimelineRestore`), `POST …/frame` (`TimelineFrameRequest`,
  `TimelineFrame`), `view=agent` on GET (`TimelineAgentView`, `TimelineAgentClip`; the 200 is a `oneOf` of it and
  `Timeline`). README Timeline: "Edits by ops". check.py: `t_diff`, `t_agent_view`, `frame_at_time` with
  `fixtures/timelines/diffs.json`, `agent_view.json`, `frame_time.json`; http fixtures for the new shapes; the
  three-way replay (`check_edit_replay`: every `edits.json` case through check.py, studio's `go test -run
  TestEditReplay` and `contracts/replay_edits.mjs`, which loads `web_service/lib/timeline.ts` in node with its types
  stripped; compared as canonical JSON; it needs `go` and `node` on PATH and fails without them); the catalog's ops
  must be `TimelineOp`'s names, each with a description. Catalog: `show_timeline` answers the agent view;
  `timeline_revisions` (assistant, mcp; `req.studio.tools` and `offered.json` follow); `try_timeline_edit`,
  `timeline_frame` (mcp, read-only); `edit_timeline`, `restore_timeline` (mcp, destructive). The catalog's `ops`
  schema is generated from openapi's `TimelineOp` with its refs inlined.
- **Studio.** `api/studio/timeline_edits.go` (`EditTimeline`, `applyOps`, revisions, `GetTimelineAgent`,
  `TimelineFrame`), `api/studio/dto/timeline_edits.go` (per-op shape checks: fields each op takes and needs, ids,
  tracks, ranges, `set` / `track` value types), `api/timeline/agent.go` (`DiffDocs`, `Agent`, `FrameAtTime`, the
  Go ports, held by the new fixtures). Migration **00031** adds `timeline_revisions.via` and `ops_summary`. Every
  write now stores them: `via` from `X-Helm-Client` (`common.ViaOf`: web, mcp, assistant; helm, other or none = api;
  studio's writes null; rows from before read `web` for user writes); `ops_summary` the /edits op names, else the
  write's kind (`create`, `edit` a PUT, `add`, `shot`, `line`, `rerender`, `undo`, `redo`, `restore`, studio's
  `fill`, `drop_shot`). Restore is `restore` with a target (`undo_to` = current, `redo_to` = null, reconciled).
  **Kong needs no change**: it passes request headers; its plugin only clears `X-Client-Id` and `X-Key-Scopes`.
- **CLI.** `helm timeline show --agent`, `edit -f ops.json [--revision n] [--dry-run]` (the file is a list of ops or
  `{base_revision, ops}`; a revision is required, from the flag or the file), `revisions [--before] [--limit]`,
  `restore --to n --revision n`. helm sends `X-Helm-Client: helm`, helm mcp `mcp`. helm mcp registers the new tools
  through the catalog as before; `timeline_frame` adds the image as image content (16 MB at most).
- **Tests.** Go: `TestAgentFixtures`, `TestEditReplay`, `tests/timeline_edits_test.go` (28.12's list: all or nothing
  with `op_index`, stale apply and dry run, a dry run stores nothing, a retry is a 409, `fill` refused, another
  user's sequence 404 and take 422, the 200-op cap, unique studio-made ids, one undo per call, via and ops_summary,
  the history, restore reconciling a finished shot, 100 kept), the assistant's two tools. CLI and MCP unit tests.
  Contract suite: `test_timeline_edits.py` (the CLI by ops: dry run, apply, a 409 from a parallel write, restore,
  history, the agent view; helm mcp: the hints, try, apply, a 409, history via mcp, a frame as an image, restore).
- **Deviations.** (1) `fill` and an unknown op are refused as a body that does not fit (`{detail}` 422, `ops[2]: fill
  is studio's`), since `TimelineOp` leaves `fill` out; over 200 ops is `{detail}` `too_many_ops: …`. (2) The frame
  at a time signs the stored object as it is (a run's poster, or the stored last frame) instead of copying it into a
  frame take: a read must not add gallery items, and `timeline_frame` is read-only. A video with no frame 0 stored
  (an upload or import) answers its last frame, else its poster (`shown: poster`, time unknown). (3) The diff and the
  agent view have no `markers` and no `link` (plan 23, not built). (4) A revision item has `duration_s`, not
  `duration`, as the house names it. (5) **The web's generated types (`web_service/gen/api.ts`) are not regenerated
  here**: the GET 200 is now a union, and three uses in `components/timeline/editor.tsx` need narrowing; that is
  phase 5's first step (the web agent).

### 28.16 As built: the web (phase 5, 28.8 A; 2026-11-23)

- **The editor** (`components/timeline/editor.tsx`) still draws every edit with `lib/timeline.ts` `apply`, then
  `save` sends the op to `POST …/edits` (`editTimeline` in `lib/api.ts`: `{base_revision, ops: [op]}`,
  `X-Helm-Client: web`); the answer's `timeline` replaces the draft. A 409 re-reads, says "reloaded, and your edit
  applied to it" and re-applies the op once on the new document (dropped when it no longer applies, as before); any
  other error shows and reloads. Undo and redo are unchanged (`…/undo`, `…/redo`). No UI change.
- **The assistant's `timeline.edit`** (`components/timeline/assistant.ts` `save`) checks each op locally first (a
  refusal says why and saves nothing), then sends the run of ops in one `/edits` call labelled `assistant`; an `add`
  is its own call (studio reads the takes; it went through `POST …/clips` before). Its 409 re-apply and its Undo
  (the sequence's own, once per revision made) are unchanged.
- **Add to timeline** (`components/timeline/add.tsx`) makes the missing audio track with an `add_track` op through
  `/edits` too, so no web code sends the PUT any more; the PUT stays on the server for now.
- **The BFF** (`lib/proxy.ts`) passes `X-Helm-Client` to Kong (a label, not a permission).
- `gen/api.ts` regenerated for 1.49; the GET's 200 is a union, narrowed to `Timeline` by `getTimeline` (the web never
  asks `view=agent`).
- **Tests**: `assistant-timeline.test.tsx` (its mock plays studio with `lib/timeline`): a gesture (S, split) saves
  through `/edits` labelled web and shows the answer; a gesture's 409 re-applies once, and when the op no longer
  applies the notice shows and nothing more is sent; the assistant's edit saves through `/edits` labelled assistant,
  its 409 re-apply and Undo / Redo. `auth.test.ts`: the BFF passes `X-Helm-Client`, not other browser headers.
