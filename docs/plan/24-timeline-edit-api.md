# helmstudio-next plan — Timeline edit API: edits by agents, without the browser

[← Plan index](../README.md)

Status: **a design for the owner to decide, not built, not in a milestone.** It takes API **1.48.0** (1.45 is plan 22,
1.46 plan 23's links and markers, 1.47 the Plan 20 gaps). It becomes a milestone in
[08-milestones.md](08-milestones.md) when the owner schedules it.

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
(`t_edit`, `t_apply`), `contracts/tools/catalog.json` (`list_timelines`, `show_timeline`), plans 16, 18 and 23.

## 28. Timeline edit API

### 28.1 What exists today

- **Edits are computed in the browser.** `lib/timeline.ts` `apply` (edit, then normalize) makes the new document;
  `PUT /v1/studio/timelines/{id}` stores the **whole document** with the `revision` it was made on. Studio only
  normalizes (checks, snaps, lays V1) and stores revision + 1; stale = 409 `timeline changed since you opened it (now
  revision <n>)`; a broken rule = 422 `TimelineError` with `problems`. The README says why: one validator in Go, edit
  maths as shared pure functions, "a named-op API would duplicate each edit in Go".
- **That duplication already happened.** `api/timeline/edit.go` is a full Go port of `t_edit` (every op of today's
  union), `Apply` = edit + `Normalize`, and `TestEditFixtures` runs it against `contracts/fixtures/timelines/edits.json`.
  Studio itself runs it only for `add` (`POST …/clips`), `insert` / `place` (shots, lines) and `fill` (the projector).
  So every edit lives three times: `check.py` (the reference), `lib/timeline.ts`, `edit.go`, held equal by one fixture
  file.
- **Today's ops**: `move` (V1 by index, audio by `at`), `trim {clip, edge, delta}`, `split {track, at, id}`, `remove`,
  `lift`, `replace`, `set`, `add {take_ids, track, ids}`, `insert`, `place`, `fill`, `add_track`, `remove_track`,
  `track`. Plan 23 (decided, not built) adds `roll`, `slip`, `slide`, `overwrite`, `split_all`, `remove_range`,
  `lift_range`, `remove {ripple}` and, in 1.46, `link` on clips and `markers` on the document.
- **New clip ids are made by the caller.** `split`, `insert`, `add`, `split_all`, `overwrite` and the range ops carry
  the ids of the clips they create; the web makes them with `newId` (8 random `[a-z0-9]`, unused in the document).
- **Revisions exist.** `timeline_revisions(timeline_id, owner_id, revision, doc, by user|studio, undo_to, redo_to,
  saved_at)`, the last **100** kept (`keptRevisions`). `POST …/undo` and `…/redo {revision}` write an old document
  forward as a new revision; studio writes (fills) are skipped by undo. There is no read of the history.
- **Frames.** `POST /v1/studio/takes/{take_id}/frame` makes a first or last frame at once (an object copy); any other
  frame is a media job `frame` at an `index` on a runner (202). Plan 22 adds one thumbnail per take.
- **Tools.** The shared catalog has `list_timelines` and `show_timeline` (read, `assistant` + `mcp`); its description
  already says timeline write tools come with `helm mcp`. Plan 16 lists `add_to_timeline` and `export_timeline` and
  leaves out "the Timeline's clip-level editing". The assistant (plan 18) has **no server write tools except memory**:
  its writes are browser screen actions and cards.
- **No idempotency key** anywhere in the timeline API; the `revision` check is the only guard against a double write.

### 28.2 The endpoint

`POST /v1/studio/timelines/{timeline_id}/edits`

```json
{"base_revision": 41, "ops": [{"op": "split", "track": "V1", "at": 12.5}, {"op": "remove", "clip": "k3"}],
 "dry_run": false}
```

- **Applies the ops in order** with `edit.go` `Apply` (edit, then normalize) on the stored document at
  `base_revision`: the same rules and fixtures as `check.py` and `lib/timeline.ts`. All or nothing: one revision for
  the whole list, so one undo undoes the call.
- **200** `{revision, timeline, diff, warnings}`: `timeline` as `GET …/{id}` answers it (normalized, `takes`
  signed); `diff` (28.3); `warnings` = what studio changed beyond the ops (a time snapped to the frame grid, a dissolve
  dropped by a `fill`), usually empty, never a refusal.
- **`dry_run: true`**: the same answer with `revision` = `base_revision` and nothing stored. The agent's "try it".
- **409** when `base_revision` is not the current one: `{detail: "timeline changed since you opened it (now revision
  <n>)", revision: <n>}`. No silent re-apply on the server: the agent re-reads and re-plans (the web's 409 re-apply
  stays a web behaviour). `base_revision` absent = apply to the current revision (as `POST …/clips` does), allowed only
  when every op names clips by id; recommended off for agents (open point 5).
- **422 per op**: `{detail: "ops[3]: split_edge", op_index: 3, code: "split_edge", problems: [...]}`: the existing edit
  error codes (`split_edge`, `split_shot`, `slip_still`, `least`, `slide_end`, `range_shot`, `unknown clip <id>`, …)
  and normalize's `TimelineError` codes (`overlap`, `past_end`, `dissolve_handles`, `too_many_clips`, …) for the
  document after that op. Nothing is stored. A body that does not fit the schema is the usual `{detail}` 422.
- **Owner scoping**: RLS (`WithUser`) as every timeline route; another user's sequence is 404, a take another user owns
  named in an op is `take <id> not found` (422), never a leak.
- **Limits**: at most **200 ops** per call (an agent's plan for a whole cut fits; one call stays under one transaction
  and one normalize per op, each O(clips)), the body under the existing 256 KB document cap, the result under the
  document caps (1000 clips, 500 per track). Over the op cap: 422 `too_many_ops`.
- **Ids for new clips**: as today, an op may carry the ids of the clips it makes (plan 23's rule: `id` / `ids`, in
  track order, `[a-z0-9]{1,16}`, unused). New here: **absent ids are made by studio** (the `newId` rule, Go side) and
  answered in `diff.added`, so an agent need not invent ids. A later op in the same call may name a new clip only by
  an id the caller gave (open point 6).
- **Idempotency**: optional header `Idempotency-Key` (`^[A-Za-z0-9._:-]{1,200}$`). Stored on the revision it made
  (`timeline_revisions.idem_key`, unique per timeline among the kept 100); a repeat with the same key answers that
  revision's result (200) instead of a 409, so an agent whose connection dropped can retry and learn the edit landed.
  A dry run stores nothing and ignores the key.
- **Revision author**: stored with `by = user` (the caller), plus a new `via` (`web` | `api` | `mcp` | `assistant`,
  from the key's client) so the history can show who made an edit. Undo treats it as a user write.

### 28.3 The diff

What an agent needs to check its own work without re-reading 1000 clips:

```json
{"added": ["k9"], "removed": ["k3"], "changed": [{"clip": "k5", "fields": {"in": [2.0, 2.5], "at": [10.0, 9.5]}}],
 "tracks": {"added": ["A4"], "removed": []}, "duration": [62.0, 58.5], "markers": {"added": [], "removed": []}}
```

Computed by comparing clip ids of the two normalized documents (fields `take_id`, `in`, `out`, `hold`, `at`, `gain_db`,
`mute`, `transition_in`, `link`, `track`); a V1 ripple shows as `at` changes on the later clips. One Go function plus
its `check.py` reference and a `diffs.json` fixture file; the web does not need it.

### 28.4 Revisions and restore

- `GET …/timelines/{id}/revisions?before=<n>&limit=<k>` (default 20, max 100): `[{revision, by, via, saved_at,
  ops_summary, clips, duration}]`, newest first, the kept 100 only. `ops_summary` = the op names of the write
  (`split, remove`) stored with the revision when it came through `/edits` (a PUT: `edit`; studio: `fill`).
- `GET …/timelines/{id}/revisions/{n}`: that revision's document (normalized, as stored), for an agent comparing.
- `POST …/timelines/{id}/restore {revision: <current>, to: <n>}`: writes the document of revision `n` as a new
  revision (never rewrites history), `undo_to` = the current, `redo_to` = null, then **reconciled** as undo is (a slot
  whose shot already ended is filled or loses its shot). 409 on a stale `revision`; 422 `revision <n> is not kept`.
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
- **A frame at a time, for `look_at`**: `POST …/timelines/{id}/frame {at}` maps sequence time to the clip under it
  and its take time (`t − at + in`, snapped to the take's frames) and answers: an empty slot = `{empty: true}`; an
  image take = its thumbnail (plan 22) at once; a video take at frame 0 or its last frame = the stored poster or last
  frame (object copy, free, as `…/takes/{id}/frame`); any other frame = the existing media job `frame` at that `index`
  (202, a runner's ffmpeg, about 0.3–1 s, no GPU), idempotent per (take, index), its image a take under
  `u/<owner>/frames/`. Recommended: the **nearest stored frame** (poster, last frame, or plan 22's thumbnail) when the
  caller asks `exact: false`, the media job only for `exact: true`, so a look over 20 cuts costs nothing (open
  point 8). Dissolves and audio are not composited: the answer is the V1 clip's own frame.

### 28.6 Tools (contracts/tools/catalog.json)

| Tool | Surfaces | Does | API |
|---|---|---|---|
| `show_timeline` (changed) | assistant, mcp | the agent view (28.5) | `GET …/{id}?view=agent` |
| `edit_timeline` (new) | mcp; assistant per open point 1 | `{timeline_id, base_revision, ops, dry_run}` → result or per-op error | `POST …/{id}/edits` |
| `timeline_revisions` (new, read) | assistant, mcp | the history | `GET …/{id}/revisions` |
| `restore_timeline` (new) | mcp; assistant per open point 1 | back to a kept revision | `POST …/{id}/restore` |
| `timeline_frame` (new, read) | mcp (the assistant's `look_at` calls the same route) | a frame at a time as MCP image content | `POST …/{id}/frame` |

- The `ops` input schema is the op union written out in the catalog (self-contained, no `$ref`, as the catalog's
  rule says), each op with a one-line description an LLM can follow ("`slip {clip, delta}`: shift the clip's source
  in and out by `delta` frames; length and position stay").
- **Destructive**: `edit_timeline` carries `destructive_ops: ["remove", "lift", "remove_range", "lift_range",
  "remove_track", "overwrite", "replace"]`; `helm mcp` registers it with `destructiveHint: true` and, as the MCP
  client asks for it, a call with none of those ops is labelled non-destructive in its result. `restore_timeline` is
  `destructiveHint: true` (it replaces the current cut, though undo brings it back). Deleting a sequence stays out.
- `helm mcp` registers every tool marked `mcp` from the copied catalog (plan 16 20.2); `add_to_timeline` stays a
  task-sized shortcut over `POST …/clips`.

### 28.7 The in-app assistant (owner decision)

Plan 18's rule is **no server write tools except memory**: the assistant's writes are screen actions the user sees
happen, costly ones behind a card. Two ways to let it edit the cut:

- **A (recommended): keep screen actions.** The assistant sends `timeline.edit {ops}` as a screen action; the open
  Timeline tab runs the ops through `lib/timeline.ts`, draws them, and saves through `/edits` (28.8) or the PUT as
  today. The user sees every edit as it happens and ⌘Z works as for their own. Needs the Timeline open in that tab
  (the assistant navigates there first, as for any screen action). No rule change.
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

### 28.10 Contract changes (API 1.48.0)

- `openapi.yaml`: `POST …/edits` (`TimelineEdits` request, `TimelineEditResult` with `diff`, `TimelineEditError` with
  `op_index`, `code`), `GET …/revisions`, `GET …/revisions/{n}`, `POST …/restore`, `POST …/frame`, `view=agent` on GET
  (`TimelineAgentView`), the `Idempotency-Key` header parameter, `TimelineOp` as the op union (today it is only implied
  by README prose and the web type).
- README Timeline: edits by ops, the 200-op cap, studio-made ids, `via`, restore, the agent view, frames at a time.
- `catalog.json` (28.6), and `check.py`: `t_diff`, `t_agent_view`, `frame_at_time` with fixtures (`diffs.json`,
  `agent_view.json`, `frame_time.json`).
- Migration (studio): `timeline_revisions` gains `via text`, `ops_summary text`, `idem_key text` and a unique index on
  `(timeline_id, idem_key)` where not null. No backfill: old rows read `via` null as `web`.

### 28.11 Order

Needs plan 23's ops for the useful set (roll, slip, slide, blade everywhere, ranges) and `helm mcp` (plan 16) for the
MCP part. Recommended: **a first slice now with today's ops** (endpoint, dry run, diff, ids, idempotency, revisions,
restore, agent view, the CLI `helm timeline edit -f ops.json [--dry-run]`) since `edit.go` already covers them; plan
23's ops join the endpoint for free when phase 2 of plan 23 adds them to `edit.go`; the tools register when `helm mcp`
is built. Open point 2.

### 28.12 Tests

- Go: `TestEditFixtures` unchanged plus `/edits` handler tests on a scratch database: all-or-nothing on a failing op
  (nothing stored, `op_index` right), 409 with `revision`, dry run stores nothing, idempotent repeat answers the same
  revision, another user's sequence 404 and take 422, 200-op cap, studio-made ids unique, restore reconciles a
  finished shot, pruning keeps 100.
- `check.py`: the new fixture files; the three-way replay of `edits.json`.
- Contract suite (`make test` on `helmstudio-test`, fake mode): the CLI edits a three-clip sequence by ops, dry run,
  apply, restore, a 409 from a parallel write.
- MCP (when plan 16 is built): a scripted client lists tools (destructive hints present), dry-runs and applies an edit,
  reads a frame.
- Web (with 28.8 A): a gesture saves through `/edits`; a 409 re-apply still works and its failure shows plan 23's
  notice.

### 28.13 Phases

1. **Design**: this plan, decided by the owner.
2. **Contract**: openapi 1.48.0, README, `TimelineOp`, `check.py` `t_diff` / `t_agent_view` / `frame_at_time`,
   fixtures, catalog entries (4 h).
3. **Studio**: `/edits` (apply loop, per-op errors, ids, idempotency, `via`), diff, migration, revisions list and
   read, restore, agent view, frame at a time, handler tests (8 h).
4. **CLI**: `helm timeline show --agent`, `edit -f [--dry-run]`, `revisions`, `restore` (2 h).
5. **Web** (28.8 A): save gestures through `/edits`, history panel optional (3 h).
6. **Tools**: catalog wiring in studio's tool handler; MCP registration when `helm mcp` exists; the assistant per open
   point 1 (A: a `timeline.edit` screen action, 2 h; B: the card and diff view, 4 h).
7. **Gates**: studio and web `make test lint`, `make contracts-check`, `make test` on `helmstudio-test` (2 h).

About 21 h with assistant option A (19 h without phase 5). Plan 23's new ops add no work here beyond their own
`edit.go` port.

### 28.14 Open points for the owner

Each with the recommended option first.

1. **The assistant**: keep browser screen actions (`timeline.edit {ops}` run by the open tab; no new server write
   tool) · a server `edit_timeline` behind a card per apply showing the dry run's diff.
2. **Order**: a first slice now with today's ops, plan 23's ops join when built, tools when `helm mcp` lands · wait
   for plan 23 and plan 16 and build it all at once.
3. **The web editor**: keep local drafts, save the op through `/edits` (one write path, server answer wins) · keep the
   PUT (two write paths) · send every edit and wait (no local edit code; laggy).
4. **Revisions kept**: 100 as today, agents told to batch ops · 500 (jsonb per revision, up to 256 KB each, about
   128 MB per busy sequence at worst) · 100 plus a named "checkpoint" an agent can pin that pruning skips.
5. **Calls without `base_revision`**: refused for `/edits` (always optimistic concurrency) · allowed when every op
   names clips by id (as `POST …/clips`).
6. **New clip ids**: studio makes absent ids and answers them in `diff.added`; a caller that needs to chain ops in one
   call gives its own · the caller must always give them (as today).
7. **Agent view**: a `view=agent` query on the existing GET (one route, documented in openapi) · reshape only inside
   the tool handler (no API change, but CLI users do not get it).
8. **Frame at a time**: nearest stored frame by default, the runner's frame job only on `exact: true` · always exact
   (a media job per frame, 0.3–1 s each, frames kept as takes) · stored frames only (no exact frames).
9. **Implementations**: keep three held by fixtures plus a three-way replay in `make contracts-check` · drop
   `check.py`'s edit code (Go is the reference) · one Go copy compiled to WebAssembly.
10. **Ops per call**: 200 · 50 · 1000.
