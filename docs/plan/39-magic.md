# helmstudio plan — Magic: the build

[← Plan index](../README.md)

Status: **planned (2026-11-27)**, release list [0.1.0-alpha.6](../release/0.1.0-alpha.6.md) item 5. The concept and its
15 decisions are [plan 37](37-magic-concept.md); this file is how it is built. Boards drawn ([MagicBoard](../mock/MagicBoard.dc.html)); the owner's approval comes
before any app code; no app code and no contract edit yet. Owner, 2026-11-27: "develop the whichever features can be made
independent of each other", "in parallel", then "can we develop the magic screen in parallel?". Section §45 (§44 is left
for plan 38).

## 45. Magic

### 45.1 Boards

UI is board-first: nothing is built until the owner approves its board. The look is the approved boards and the
helmstudio Design System (its copy in `docs/design-system/`): the page shell, chips, popups and the settings drawer of
[TextToVideo](../mock/TextToVideo.dc.html); lanes, ruler, playhead, clips and the floating player of
[Timeline](../mock/Timeline.dc.html); music takes as covers and sound and voice takes as orbs (sound sky, voice amber) of
[CreateMusicSound](../mock/CreateMusicSound.dc.html); the shared menu of [ContextMenus](../mock/ContextMenus.dc.html).

| Board | Frames | State |
|---|---|---|
| [Magic](../mock/Magic.dc.html) | the concept, frames 1–8 (page, Add, a block opened, move and join, the Assistant, Send to Timeline, a storyboard frame, a character asset), with the owner's changes below | the concept; kept |
| [MagicBoard](../mock/MagicBoard.dc.html) (the app look) | 1 the page (every lane, the Assets row, joins, status, the floating player, the side panel, the zoom group); 2 Add at a point (each kind with its orb, Music and Sound greyed before plan 38, the standard request for Video, Video on another model with greyed fields, Voice, Music, Sound, Image); 3 a block opened (@ references, tied, notes, run history, Advanced, takes, pin); 4 move and join (snap, Insert, the tied line moving, every transition, the Morph block, effects); 5 voice and music tied to a shot (the "runs past" warning, the Tie dialog); 6 an asset opened and Import as a copy; 7 a storyboard frame opened (style, framing, Generate from board, Rough cut); 8 the Assistant's proposal, History and the ask card; 9 Send to Timeline and the sends list; 10 the right-click menus; 11 empty, loading, failed, out of date, snapshots, phone | drawn 2026-11-27; owner's review next |
| [ContextMenus](../mock/ContextMenus.dc.html) frame 13 | points to MagicBoard frame 10: a menu for a block of each kind (video, voice, music, sound, a storyboard frame), a lane header, the empty lane at a time, an asset, a transition point, a link mark, an effect badge, several blocks, a snapshot, a send | replaced 2026-11-27 (its old sketch had Open in Timeline on a block, against decision 9) |

### 45.2 Sources

- [plan 37](37-magic-concept.md) decisions 1–15 and its suggestions 1–10.
- [plan 38](38-create-music-sound.md) (Create's Music and Sound, alpha.6 item 6, §44; approved and building): its P1
  is the contract (API 1.79.0, `SoundClass`, stereo Audio, the `mux` media job, `00056_take_sound.sql`); its P3 makes
  the music and sound workflows on the real packs. Magic's phase C rebases on P1 and phase H waits for P3 (44.7);
  everything else here does not wait.
- The code: `studio_service/api/timeline/` (`edit.go`, `agent.go`, `makeshot.go`), `studio_service/api/studio/`
  (`timeline_edits.go`, `runs.go`, `resolve.go`, `assistant_tools.go`), `studio_service/migrations/00019_timelines.sql`,
  `00031_timeline_edits.sql`, `web_service/components/timeline/` (`lanes.tsx`, `editor.tsx`, `assistant.ts`),
  `web_service/components/create/assistant.ts`, `web_service/components/context-menu.tsx`,
  `pipelines_service/assistant.py` (`auto_plan`, Auto mode's caps), `contracts/tools/actions.json` and `catalog.json`.

### 45.3 Corrections from the scout

1. **A sequence is one JSON document with a revision**, not rows per clip (`timelines.doc`, `timeline_revisions` keeping
   the last 100, `by` and `via` per revision). Edits are named ops applied all or nothing under the row lock, against a
   `base_revision` (409 when stale), with `dry_run` returning the diff and warnings. Magic copies this shape exactly: a
   board is a document, edited by ops, tried then applied.
2. **The Assistant's timeline tools are server tools** (`try_timeline_edit`, `edit_timeline`, `restore_timeline`,
   `show_timeline` with `view=agent`, `timeline_frame`) listed in `contracts/tools/catalog.json`, plus **screen actions**
   (`contracts/tools/actions.json`, screen `timeline`) whose generations are cards the user says Yes to. "Proposal, Apply
   or Discard" (decision 8) is the dry run's diff drawn on the board, then the same ops with `base_revision`.
3. **The Timeline has one transition: a dissolve** (`transition_in`, `TimelineDissolve`, frames). Cut is no transition.
   Fade, wipe and morph do not exist there, and decision 9 keeps the Timeline unchanged, so Send to Timeline must bake
   them (45.9).
4. **Take kinds are `image | video | audio`.** Music, sound and voice are all `audio`; plan 38 adds the sound class
   (`Workflow.sound`, `Take.sound`: `music | sound | voice`), and the cover and orb looks come from it, not a new kind.
   Magic's block kinds (Image, Video, Voice, Music, Sound) are Magic's own, mapped to workflows by `media` and `sound`.
5. **A sequence owns a session** (`timelines.session_id`) and New shot runs go there, tracked in `timeline_shots`
   (clip → run). A board does the same: its own session, a block → run index.
6. **No server-side time estimate exists.** `POST /v1/studio/runs/plan` answers the host and the planned peak GB, not
   seconds. Create's "≈ 40 s" is the web's last measured `render_s` of that workflow; the Assistant's Auto mode
   (`auto_plan`) asks when the screen has no time. Magic needs a real estimate in studio (45.10).
7. **The shared menu exists** (`context-menu.tsx`, `openMenu(e, {title, items})`): Magic only adds its items, no new
   component.
8. **Elements are library items** (`library`, with `voice_id`); decision 12 makes assets the board's own, so Import copies
   a take or an Element into the board's document, never links to it.

### 45.4 Facts

- Contract today: API 1.78.0 (plan 40's model folders took 1.78.0 and `00055_host_folders.sql`). Plan 38's P1 makes
  it 1.79.0; Magic's steps are 1.80.0 onward, one minor version per step (45.6). Migrations: plan 38 takes
  `00056_take_sound.sql`, Magic's are numbered after it at build time.
- Music and sound takes are stereo after plan 38 (Audio `channels`); the Timeline's audio tracks take them as plan 38
  leaves them, so Send needs nothing more for them.
- Packs behind the standard request (decision 15): Image (Qwen Image 2.1 and Turbo, MiniMax H3 Image), Video (LTX 2.5,
  MiniMax H3), Voice (Qwen3-TTS, ZONOS3), Music (ACE-Step 1.5 2B and XL, MiniMax Music 3; plan 38), Sound (Stable Audio 3
  Small-SFX, MMAudio; plan 38).
- Timeline limits Magic must respect when it sends: 100 sequences per user (409), V1 is the picture track, sound clips
  can be linked to a V1 clip (`TimelineClip.link`, 1.50), markers exist (1.50).

### 45.5 Scope

**In (decision 13, the whole concept):** the Magic page in the left rail; lanes (Assets row, Storyboard, Shots, Voice,
Music, Sound, free lanes named by the user); Add at any point; every block a node with a preview and a status (draft,
running, done, out of date); move, snap, insert or overwrite, move several; @ picks for assets and links
(`@Shot 1 · last frame`); storyboard frames, Generate from board, the rough cut; voice and music tied to a shot;
transitions (cut, dissolve, fade, wipe, a generated morph; fade in and out at a lane's ends); effects as badges; the
standard generation request per kind with Advanced; pin a take, run history, notes; the GPU estimate and the ask above
the limit; board snapshots; Send to Timeline as a new sequence every time, and the sends list; the Assistant throughout;
the right-click menus.

**Out:** outside services behind the standard request (decision 2: later); changing Create, Pipelines or the Timeline
(decisions 2 and 9); a link back from a sequence to its board; sharing boards between users; a free node canvas
(decision 5).

### 45.6 Design

**Where the standard generation request lives.** In studio, as data the packs declare and studio's resolver reads:
each workflow that can serve a kind adds a `standard` block to its registry entry (`registry.schema.json`): the kind, which
shared fields it supports (prompt, refs, first and last frame, length, size, seed, voice, lyrics, …) and which of its
params each one fills; everything else of the workflow is its Advanced. A block's request (`MagicRequest`: kind, model,
the shared fields, `advanced`) is turned by studio into a normal `RunRequest` and posted to the board's session like any
run. Studio, because it already resolves runs (defaults, presets, library items, rules) and owns the registry view, so
the adapter is a mapping, not code; not the runner, which stays unchanged; not the web, which would leave the Assistant
and the CLI without it. A new pack (plan 38's music and sound ones) serves Magic by adding its `standard` block, and an
outside service later is one more adapter behind the same request.

**The board document** (one JSON doc per board, like a sequence):

| Part | Holds |
|---|---|
| `target` | width, height, fps, sample rate (like a sequence's; from the first shot, else 1280 × 720, 24 fps, 48000 Hz) |
| `lanes[]` | id, name, role (`storyboard`, `shots`, `voice`, `music`, `sound`, or `free`), order, hidden |
| `blocks[]` | id, lane, `at` and `length` (frames), kind (`frame`, `image`, `video`, `voice`, `music`, `sound`), the `request` (`MagicRequest`), `takes[]` (take ids from its runs), `pinned` (a take id), status is derived (45.7), `note`, `effects[]` (badges: name and params), `from_frame` (a shot made from a frame) |
| `links[]` | a voice or music block tied to a shot (`{block, to}`); moving `to` moves `block`; unlink removes the row |
| `refs` | inside a request's text and fields: `@asset` and `@block · first frame | last frame | take` picks, stored as ids |
| `transitions[]` | `{between: [a, b]}` or `{start | end: lane}`, kind (`cut`, `dissolve`, `fade`, `wipe`, `morph`), length, for a morph its own `MagicRequest` and takes |
| `assets[]` | id, kind (`character`, `prop`, `place`), handle, name, `sheet[]` (take ids: views, expressions), `voice_id`, note, `imported_from` (a take or Element id, for the record only) |
| `notes[]` | board notes the Assistant reads |

**Contract changes**, ordered after plan 38's P1 has landed in `openapi.yaml` (1.79.0; Magic takes 1.80.0 onward;
each step is its own version so the phases ship alone):

1. Boards: `GET/POST /v1/studio/magic/boards`, `GET/PATCH/DELETE …/boards/{board_id}` (soft delete; 100 per user, 409),
   `POST …/edits` (named ops, `base_revision`, `dry_run`, the diff and warnings, studio-made ids), `…/revisions`,
   `…/restore`, `…/undo`, `…/redo`, `view=agent`; `MagicBoard`, `MagicDoc`, `MagicOp`. contracts/README.md "Magic".
2. Snapshots: `GET/POST …/snapshots`, `DELETE …/snapshots/{id}` (a named pointer to a kept revision; Restore uses
   `…/restore`).
3. The standard request: `registry.schema.json` `workflows[].standard`; `MagicRequest`; `GET /v1/studio/magic/kinds`
   (per kind: the models that serve it, what each supports, the greyed reasons).
4. Runs: `POST …/boards/{board_id}/run` (block ids, or a lane, or all out of date; 202 with the runs) and
   `POST …/boards/{board_id}/plan` (per block: seconds, host, peak GB; the totals per lane and board; the run order).
   Run events reach the board through the board's session SSE, as today.
5. Send to Timeline: `POST …/boards/{board_id}/send` (202: a media job for the baked transitions when there are any,
   else 201 with the new sequence), `GET …/sends`.
6. Music and Sound kinds: plan 38's packs add their `standard` blocks; no Magic route changes.
7. Tools: `contracts/tools/catalog.json` the Magic server tools (45.8) and `actions.json` screen `magic`.

**Data model and migrations** (studio, one migration per contract step, every table owner-scoped by `app_can_see`, so
another user's id is 404):

| Migration | Tables |
|---|---|
| `magic_boards` | `magic_boards` (id, owner_id, name, doc jsonb, revision, session_id, created, updated, deleted_at); `magic_board_revisions` (board, revision, doc, `by` user / studio / assistant, `via`, `ops_summary`, undo_to, redo_to; the last 100 kept) |
| `magic_snapshots` | `magic_snapshots` (id, board, owner, name, revision, made_at); a snapshot's revision is never pruned |
| `magic_runs` | `magic_runs` (owner, board, block or transition id, run_id, created): the run → block index the projector uses to add a take to its block (as `timeline_shots`) |
| `magic_sends` | `magic_sends` (id, owner, board, revision, timeline_id, name, media_job_id, made_at) |
| `magic_settings` | `magic_settings` (owner, `ask_above_gpu_minutes`, default 5) |

Lanes, blocks, links, refs, transitions and assets live in the document, not in tables: one edit is one revision, undo
and snapshots come free, and the Assistant's try-then-apply is the Timeline's.

**Services touched:** studio_service (routes, ops, the resolver for `MagicRequest`, the estimate, Send, the projector
hook); web_service (the page, the lane editor, block and asset dialogs, menus, the Assistant screen actions); the packs'
registry entries (`standard` blocks); pipelines_service (the Assistant's tool list and its ask rule); runner_service only
for the baked-transitions media job (an ffmpeg job beside export). No new service, no new message type: runs are
`cmd.run.execute`, the bake is a `cmd.media.job` of a new kind `bake` (beside plan 38's `mux`).

### 45.7 Block status

Derived, never stored: **draft** (no take), **running** (a run of it is queued or running), **done** (a pinned or
latest take matches its request), **out of date** (its request, an asset it @-picks, the frame it was made from, or the
block it takes a frame from changed after its take was made: studio compares a hash of the inputs kept on the take's run
with the block's current one). Running an out-of-date block keeps the old takes in its history.

### 45.8 The Assistant

Server tools, the Timeline's pattern: `list_boards`, `show_board` (the agent view: lanes, blocks with status and the
take shown, assets, links, transitions, the estimate), `try_board_edit` (dry run: the diff, drawn on the board as the
proposal with Apply and Discard), `edit_board` (the same ops and `base_revision`; the revision marked `by assistant`),
`restore_board`, `plan_board`, `run_blocks`, `board_frame` (a still at a board time), `send_to_timeline`. Screen actions
(`magic`): select, open a block, play, scrub. It can build a board from a brief (assets first, then the storyboard, then
shots), fill the standard request, compare and pin takes, and send.

**Ask above the limit (decision 10):** `run_blocks` asks `…/plan` first. Storyboard frames and runs whose estimate is
at most the user's `ask_above_gpu_minutes` run without asking; anything over it returns a card with the estimate
("3 shots, about 6 min") whose Yes is the user's click, as Create's Generate card. A run with no estimate asks. Auto
mode's hourly caps still apply on top.

### 45.9 Send to Timeline

Always a **new sequence** (decision 9): `POST /v1/studio/timelines` is never called on an existing one. Studio builds
the sequence's doc from the board at a revision: the Shots lane's blocks in time on V1 (each its pinned or latest take);
voice, music and sound blocks on audio tracks, linked to their shot clip (`TimelineClip.link`) when tied; cut and
dissolve map to the Timeline as they are; fade, wipe and fade in or out are **baked**: a `bake` media job renders the two
clip ends with the transition into a short clip that takes their place, so the Timeline stays unchanged; a morph is its
own generated video take placed between the shots. Storyboard frames and the Assets row never go. A block with no take
is a gap and a warning. Name: `<board> · send <n>`; the sends list shows each with its revision. 409 at 100 sequences,
with the Timeline's text.

### 45.10 GPU-time estimate

Studio, per block: the caller's own recent `render_s` for that workflow and variant on the host it would run on, scaled
by length × size against the measured run (the median of the last 5); with none, the pack's declared seconds per unit in
its `standard` block; with neither, unknown (and the ask rule asks). The page shows it on each block, each lane and the
board, and the run order. The same numbers feed the ask card.

### 45.11 Phases

Each phase ships alone; its board is approved before its code.

| # | Phase | Needs | Parallel with plan 38? |
|---|---|---|---|
| A | **Boards**: MagicBoard and ContextMenus frame 13 (45.1), drawn; owner's approval | nothing | yes: board files only |
| B | **Page shell and lane editor on fake blocks**: the rail item, the page, lanes, the ruler, playhead, Add at a point, move, snap, insert or overwrite, multi-select, links moving with shots, transition points, badges, the right-click menus, undo, the floating player (reusing the Timeline's) on a local document in the web with fake takes; no API | A | **yes**: web only, no contract |
| C | **Contract 1–2 and studio boards**: tables, ops, revisions, snapshots, `view=agent`; B wired to the API | plan 38's P1 landed | after plan 38's P1; then alongside its build |
| D | **The standard request for Image, Video, Voice**: `standard` blocks on Qwen Image, MiniMax H3 Image, LTX, MiniMax H3, Qwen3-TTS, ZONOS3; `/magic/kinds`; block dialogs with Advanced and greying; run, takes, pin, history, status; the estimate (contract 3–4) | C | yes: none of plan 38's packs |
| E | **Assets and storyboard**: the Assets row, Import, @ picks and the "used in" light; storyboard frames, Generate from board, out of date; the rough cut (frames in order with their lines) | D | yes |
| F | **The Assistant**: the server tools and screen actions, the proposal on the board, the ask rule (contract 7) | C for the edits, D for runs | yes |
| G | **Send to Timeline**: the doc builder, cut and dissolve, the `bake` media job for fade and wipe, morph blocks, sends list (contract 5) | D | yes |
| H | **Music and Sound kinds**: their `standard` blocks, Music and Sound lanes running, covers and orbs on blocks (from `Take.sound`), MMAudio from a shot's video (plan 38's video → sound workflow; the block keeps the audio take) | plan 38's P3 | **no: waits for plan 38** |

B and plan 38 can start now; C waits only for plan 38's P1; H waits for plan 38's P3. Until H, the Music
and Sound lanes take placed audio takes (an upload or a gallery take) and their Add dialog shows the kind greyed with
"Comes with Create's Music and Sound".

### 45.12 Tests

- Studio (Go): every op (move, insert, overwrite, link, unlink, transition, asset, snapshot), stale revision 409, dry
  run stores nothing, undo skips studio writes, status derivation, the `MagicRequest` → `RunRequest` mapping per pack,
  the estimate, the Send doc builder (gaps, links, baked transitions). DB tests on `.tools/test-db`.
- Contract tests: each route through Kong in fake mode, another user's board 404, 100 boards 409, long work 202, the
  schemas of every response; new `tests/test_magic_*.py`.
- Web: vitest for snapping, links moving with a shot, the "no longer fits" warning, the menus' items per thing; one
  Playwright journey in fake mode (make a board, add a shot, run it, send it, open the new sequence).
- The Assistant: tool tests in pipelines_service (try then apply, the ask card above the limit, runs without asking below
  it).
- Gates per CLAUDE.md; the full suite once at the end of each phase.

### 45.13 Risks

- **Scope.** The whole concept is large; the phases keep each piece shippable, and H is the only one tied to plan 38.
- **Contract clash with plan 38.** Both edit `openapi.yaml`; Magic waits for plan 38's contract commit and rebases on it.
- **Character consistency.** Only the asset → frame → first frame chain keeps a character; say so on the board and in
  the asset dialog (plan 37 suggestion 2).
- **GPU time.** A board can ask for hours of runs; the estimate, the ask rule and run order per lane keep it visible.
- **Bake quality.** Baked fades and wipes re-encode two clip ends; keep them short and at the sequence's target.
- **Large documents.** A long board with many blocks makes a big JSON doc; the Timeline's doc copes today, and revisions
  are pruned to 100.

### 45.14 Open points (recommended defaults)

1. Fade, wipe and fade in or out in the Timeline: bake them (default, keeps decision 9) or add them as Timeline
   transitions later.
2. `ask_above_gpu_minutes`: 5 minutes by default, set in the Magic page's settings (default) or in the Assistant's
   settings.
3. Snapshots: unlimited named ones (default) or a cap of 50 per board.
4. A board's target: from its first shot (default), editable in the board's settings.
5. The board's session: hidden from Create's session list (default), or listed as `Magic · <board>`.
6. Free lanes: any kind on any lane (decision 5); the role only picks the default kind in Add (default).
7. Dialogue: the approved concept's Add has a Dialogue kind ("lines, two or more"), and MagicBoard keeps it. Default: a
   Dialogue block is a Voice block with turns (`lines[]`: a speaker @asset and the text each), each turn its own Voice
   run on the speaker's voice, played one after another in the block; or drop it and use one Voice block per line.

## Changed by the owner (2026-11-27, on the lane board)

- Transitions use the bowtie set: Dissolve a bowtie chip, Cut a line chip, Fade a half bowtie (picked "B · Bowtie" from six options).
- The Add dialog shows an icon on every kind ("icons here as well").
- Zoom is one group on the right of the board tools, as on the Timeline: zoom out, a slider, zoom in, the span in view, Fit (the whole board across). ⌘ + mouse wheel or a trackpad pinch zooms at the pointer; Shift + wheel scrolls across; ⌘− / ⌘= / ⇧Z ("zoom in / zoom out / fit option of the whole timelime horizontally", "also works with mouse wheel").
- The side panel has the same sliders toggle as Create and the Timeline, open by default and remembered ("option to show hide this like other palces").
- MagicBoard (the app look) carries all four (review, 2026-11-27).

## Decided by the owner (2026-11-27)

Owner: "approved magic, build it": MagicBoard (11 frames) is approved, and the seven open points in 45.14 take their recommended defaults (bake fade and wipe; ask above 5 GPU minutes, set in Magic's settings; unlimited named snapshots; target from the first shot; the board's session hidden from Create's list; any kind on any lane; Dialogue as a Voice block with turns). Contract numbers: plan 40 (model folders) took 1.78.0 and plan 38 takes 1.79.0, so Magic starts at 1.80.0.

## Built

**Phase C (2026-11-27, API 1.80.0; contract steps 1 and 2 in one version, as the owner's run asked).** Boards in
studio: `magic_boards` and `magic_board_revisions` (`00058_magic_boards.sql`), `magic_snapshots`
(`00059_magic_snapshots.sql`), owner-scoped by `app_can_see`; the routes of contracts/README.md "Magic" (boards, edits
by ops with `base_revision` and `dry_run`, revisions, undo, redo, restore, snapshots, `view=agent`). The web's editor
(`components/magic/use-board.ts`) applies each edit locally and sends the same ops to studio one call after another;
undo, redo and snapshots are studio's. Decisions taken on the owner's behalf:

- **One set of ops, two implementations kept equal**: `web_service/lib/magic.ts` and `studio_service/api/magic`,
  replayed from `contracts/fixtures/magic/edits.json` in `make contracts-check` (40 cases). Porting found four gaps in
  phase B's ops, fixed in both: a split made while an op holds blocks off the board could reuse a held block's id;
  `add` and `lane_add` with an id in use are refused (`That id is taken.`); a `transition` names exactly one join
  (`Pick a join.`); a new transition's default length is whole frames.
- **Not built yet (their phases)**: the board's session, `magic_runs`, `magic_settings`, the standard request and runs
  (phase D), so the page's Run items are off with "Runs come with the standard request" and the fake estimates are
  hidden; the agent view's status is draft or done until runs come.
- **Snapshot rename** (`PATCH …/snapshots/{id}`): the approved snapshot menu has Rename.
- **Takes a document names** must be the caller's ready ones (422 `take <id> not found`, the same answer as for an id
  nobody has).
- **First visit**: a user with no boards gets an "Untitled board" (frame 11's empty state); the sample board is in the
  boards menu, its blocks as drafts (its takes are pictures in the browser). The browser fake mode stays for the tests
  (`<MagicBoard local />`).

**Phase D (2026-11-27, API 1.81.0; contract steps 3 and 4 in one version, as the owner's run asked).** The standard
request and runs: a workflow's `standard` block (registry.schema.json; the runner and contracts-check refuse one that
does not fit) on Qwen Image, MiniMax H3 Image, LTX 2.5, MiniMax H3, Qwen3-TTS and ZONOS3 (and the fake host's Echo,
Echo video, Echo keyframes, Echo voice); studio's resolver (`api/studio/magic_runs.go`) turns a block's request into a
normal run in the board's own session; `GET /magic/kinds`, `GET/PUT /magic/settings`, `POST …/run` and `…/plan`;
`00060_magic_runs.sql` (`magic_boards.session_id`, `magic_runs`, `magic_settings`); the projector adds each take to its
block as a studio revision; every board answer carries its runs, takes and derived statuses. The web: Add's step 2
(frames 2c-2h) and the block dialog (frame 3, `components/magic/block-dialog.tsx`, `request.tsx`). Decisions taken on
the owner's behalf:

- **One workflow per model and kind.** LTX 2.5 is `ltx-image-to-video` (the frames are keyframe rows at 0 s and the
  clip's last frame; with none its keyframes node is bypassed: text to video); MiniMax H3 is
  `minimax-h3-image-to-video` (first and last image params; empty ones go). MiniMax H3 really takes a last frame, so
  it is not greyed in studio: greying follows what each pack declares; the fake mode keeps the board's 2d example.
- **Not shared fields:** Look ("LTX only" on 2d: looks are not params) and Emotion ("ZONOS3 only" on 2e: it is under
  ZONOS3's Advanced). Both left for later.
- **Added to `MagicRequest`:** `first`, `last`, `width`, `height`, `seed`, `count` (1-4 runs, seeds seed, seed + 1:
  "Seeds 1 / 4") and `voice_id` (a saved voice, since board assets and their voices come with phase E).
- **The default model** is the kind's fastest declared one (`standard.seconds`), then by id: Qwen Image, LTX 2.5 and
  Qwen3-TTS, as the boards show.
- **The estimate:** units are an image's megapixels, a video's seconds × pixels / 720p, a voice's seconds; yours is the
  median over your last 5 takes of that workflow on the planned host (the variant is not separated); else the pack's
  declared seconds per unit; else unknown. The ask rule lives in `…/plan` (`ask`, `ask_text`); the page's Generate is
  the user's click; Run board asks with it above the limit, which is set in the board panel (Ask before runs over N GPU
  min).
- **Statuses** are studio's (inputs hash on `magic_runs`); the page also marks a block out of date at once when its
  request changes, and studio's answer confirms it. Undo, redo and restore keep every take a run made (a studio write
  must never be undone away); an edit, undo, redo or run made on a revision a run's take moved past is sent again once.
- **Board events** are the board's session events (status, progress, outputs): the page reads the board again on them;
  no new event type. The board's session is titled `Magic · <board>` and left out of GET /sessions.
- **Fix found:** Unpin sent no `pinned` (an absent key is no change in studio); it sends `null`.
- **Not built yet (their phases):** board frames running (E), dialogue blocks running, Music and Sound (H), Compare
  takes, the dialog's Run on host pick, the Assistant's tools and ask card (F).

**Phase E (2026-11-27, API 1.83; phase G took 1.82 while E was building).** Assets and the storyboard: the ops
`asset_add`, `asset` and `take_add` in both implementations (`fixtures/magic/edits.json`, 7 more cases);
`asset_delete` now also clears a first or last frame pick (left over from D) and writes the asset's prompt mentions
as its name; board frames run on the image models (left over from D); Make views (`POST …/assets/{asset_id}/views`,
`00061_magic_assets.sql`: `magic_runs.asset` and `label`); the web's Assets row, asset dialog, Import, @ picks,
frame dialog and rough cut (`components/magic/asset-dialog.tsx`, `import.tsx`, `at-text.tsx`, `frame-dialog.tsx`,
`rough-cut.tsx`, `lib/magic-assets.ts`). Decisions taken on the owner's behalf:

- **Handles follow the Element rule** (`@red-umbrella`, not the board's "@red umbrella": a mention can't hold a
  space); unique on the board whatever the case; kept from `asset_add` (Rename changes the name, so prompts that
  mention the handle never break). The sample board's handles are now `red-umbrella` and `bus-stop`; a board kept
  from before with a spaced handle still loads (the rule is checked on `asset_add`, not on a whole document).
- **@ picks**: typing @ offers the board's assets and other blocks' pictures; an asset is written `@handle` and
  joins the refs (a handle typed in full joins too), a picture joins the refs only (a block's name is no handle).
  Studio writes each board asset's mention as its name, with its picture's row (`Mara <image1>`) when the model
  takes references, so Create's resolver never reads a board handle as an Element; the user's own Elements still
  expand as in Create. "Used in" counts refs, the first and last frame and a voice.
- **Import is a copy of ids**: a take's id, or an Element's pictures, voice and words, onto the asset's sheet
  (`imported_from` for the record). Takes are never changed in place, so later Gallery changes don't reach the board;
  a take deleted from the Gallery leaves its sheet (as a deleted take leaves a block). Import from the Assets row
  makes a new asset (a character named after the first take; an Element keeps its kind, location → place).
- **Views have names** (`MagicAsset.views`, take id → "Front"). Make views runs the image kind's first model once per
  view (character: Front, Side, Back, Full figure; prop: Front, Side, Back; place: Wide, Reverse angle; "Another
  view…" for an expression), square for a character or prop, the first sheet take as the reference, the note in the
  prompt; each take joins the sheet as a studio revision named by its view (undo, redo and restore keep it, as a
  block's take). The first view is the picture an @ pick sends ("Make it the first" in a view's menu).
- **A frame's prompt** is its style in words (Sketch: loose pencil lines, black and white; Rough: simple shapes, grey
  shading; Full: full colour), its shot size and angle, what happens, then "With @x" for each picked asset its words
  don't name. `framing.move` is the shot's ("The camera slowly pushes in"). Each frame run keeps its style
  (`MagicBlockRun.style`), so the dialog groups takes as Sketch · 2, Rough · 1, and Yours for your own pictures.
- **A frame's line** is `request.line`; the rough cut shows it, else the lines of the voice blocks under the frame,
  and plays those voice takes at their place. It plays the frames back to back (gaps between frames are left out).
- **Generate from board** is one edit: the frame's unsaved changes, then the shot under it on the Shots lane (in the
  board's Insert or Overwrite mode, never covering a shot silently), named `Shot n`, with the frame's `shot_model`;
  it then runs. With a shot already made from the frame it runs that shot (its model set to the frame's pick). A shot
  with no prompt of its own takes the frame's framing, words and move; its inputs include the frame's request, so
  editing the frame puts the shot out of date at once (the hash only grows for shots made from frames, so blocks run
  before 1.83 keep their status).
- **A frame added from Add opens in its dialog**; the frame dialog keeps its changes when it closes (the block
  dialog keeps its Save and close).
- **Not built (their phases or later):** dialogue blocks running; Compare takes; a handle rename that rewrites
  prompts; Make views with a model pick (the image kind's first one runs; the request takes `model`).
- **Review fixes (API 1.84):** Make views refuses a view of the asset still queued or running (409 `Front is already
  being made`; the dialog leaves those out of "the missing" and greys them in its menu), so a double click or a second
  Make the missing never makes a view twice; a frame's "With @x" names every asset it picks, also one with no picture
  yet (before, only those with a sheet take); studio's document check holds an asset's bounds (name 1-80, sheet at
  most 50, note at most 4000, voice_id and imported_from at most 80), which `asset_add` and `asset` alone did not.

**Phase G (2026-11-27, API 1.82; contract step 5).** Send to Timeline: `POST …/boards/{board_id}/send` (dry run 200,
201 the new sequence at once, 202 baking), `GET …/sends`, `DELETE …/sends/{send_id}`; `00062_magic_sends.sql`
(`magic_sends`, `media_jobs.send_id`); the doc builder `studio_service/api/magic/send.go` (`BuildSend`); the bake
graph `studio_service/api/timeline/bake.go` (`BuildBake`, the export's picture steps); the runner's `bake` media job
(the export's conform path, `bake.mp4`); morphs with their own request, takes and pin, run as blocks
(`api/studio/magic_morph.go`); the web's dialog, sends list, toast, morph block and dialog
(`web_service/components/magic/send.tsx`). Decisions taken on the owner's behalf:

- **Every board time stays.** A baked join or a morph takes the first shot's last half and the second's first half
  (the clip centred on the cut), so the voice and music land where they were. A side whose take has nothing past its
  block holds its end frame (`tpad`); a take shorter than its block leaves the rest an empty slot (a warning).
- **Dissolve "as it is"** is the Timeline's own `transition_in` only when both takes have the media past the cut (the
  second clip then starts half the dissolve into its take); generated takes usually are exactly their block, so most
  dissolves are baked (xfade `fade`) like a fade. If the Timeline's rules refuse a kept one, the send bakes them all.
- **Fade is through black** (`fadeblack`), **wipe is left to right** (`wipeleft`); the Shots lane's fade in and out
  bake its first block's head and its last block's tail. A voice, music or sound lane's fade is not sent (`skipped`,
  a warning): the Timeline has no sound fades.
- **Effects bake**: Grade (`temperature`, `contrast` ±100: colorbalance and eq), Slow (`rate` 0.5-2, the take slowed
  to fill the block, its sound with atempo) and Stabilize (`deshake`), in the shot's own clip and in the joins that
  read it; another effect is skipped with a warning.
- **Tracks are the Timeline's names**: V1, then A2, A3 … (frame 9 drew A1; the Timeline calls the first sound track
  A2). Only the first Shots lane goes on V1; other picture lanes stay (a warning); up to 8 sound lanes.
- **Bakes run outside the four open media jobs** a user may have (a send can need many); at most 100 per send; one
  send of a board bakes at a time (409 `This board is still sending; wait for it.`). Their takes are a new
  `TakeSource` `bake` (in the gallery like exports), titled `<board> · <join>`. The estimate is your recent bakes'
  seconds per frame, else 0.25 s a frame, plus 4 s each.
- **The 100 sequences 409** is checked on the dry run too (the dialog shows frame 9's "100 sequences is the most"),
  and again when the last bake is in (the send fails with the Timeline's text).
- **A listed send's revision is kept** (never pruned), so its menu's Restore board to rev N always works; Remove from
  list soft-removes it (the sequence stays; a baking one's jobs are cancelled).
- **Morph**: the `transition` op takes `request` and `pinned` (kept while it stays a morph); its runs use the fastest
  video model whose standard block fills both `first` and `last`; `…/run` and `…/plan` take its id as a block's.
- **Bakes are frame-exact**: each side and the output are cut at exactly their frames, fps set again after `tpad`
  (ffmpeg 7.1's xfade refused the unset rate; found on the test stack); a bake a frame short still keeps the times
  (a slot after it).
- **Not built (left for later):** the Assistant's `send_to_timeline` tool (phase F); sound fades; a morph's own prompt
  field in its dialog (its request keeps one when set through the op).

**Phase H (2026-11-27, API 1.85; 1.86 a README wording fix).** Music and Sound kinds: `standard` blocks on MiniMax
Music 3 (`minimax-music3`), ACE-Step 1.5 (`ace-step-music`), Stable Audio 3 Small-SFX (`sa3-sound`) and MMAudio
(`mmaudio-sound`, `mmaudio-video`), and the fake host's Echo music, Echo sound and Echo sound from a video; the shared
fields lyrics, instrumental, key, bpm and from, length also on a float (seconds) param, `standard.default`
(registry.schema.json, check.py, the runner's check); `MagicRequest` lyrics, key and bpm; `MagicModel.video`;
`MagicTake.sound`. Studio runs them (`magic_runs.go`); the web's Add and block dialog take their fields, the menus run
them, and blocks draw covers and orbs (`components/magic/sound-mark.tsx`). Decisions taken on the owner's behalf:

- **The default model** is a pack's `standard.default` (MiniMax Music 3, the owner's pick in plan 38), then the
  fastest declared one. Sound has no default: Stable Audio 3 Small-SFX is first (fastest); a sound made from a shot's
  video with no model picked runs the first model that takes a video (MMAudio), as frame 2g shows.
- **MMAudio is one model with two workflows**: workflows of one kind and one `standard.model` are one model; the one
  without `from` is its id, the one with `from` its `video` (its fields are both's, its Advanced its own). A request
  with `from` runs the video workflow. Stable Audio is greyed `can't take a shot's video`.
- **ACE-Step 1.5 is one model** (XL or 2B is its `model.variant` under Advanced), where frame 2f lists "ACE-Step 1.5
  XL or 2B, MiniMax Music 3": one workflow serves both variants.
- **The fields**: music's prompt is labelled Style; empty lyrics are an instrumental (`instrumental` true; the
  placeholder says Instrumental); Key is Auto or one of ACE-Step's 24 keys, sent only to a key param that has it;
  BPM 30-300 (absent Auto); Key and BPM are two fields (the board's "Key · BPM" row), greyed on MiniMax Music 3 and
  on Echo music's key. The length is the block's seconds on the param's grid and bounds, so a block under 10 s gets a
  10 s song from MiniMax and ACE-Step (their least).
- **The estimate** is seconds of audio × the pack's GPU seconds per second (plan 38 44.4: MiniMax 4.9, ACE-Step 0.55,
  Stable Audio 0.1, MMAudio 0.5 from text and 1.1 from a video; Echo 0.3 and 0.2); "2 cues", "1 sound" on the ask card.
- **The block keeps the audio take**: a voice, music or sound block takes only its runs' audio takes, so MMAudio's
  video with the sound stays in the Gallery (and in the run's outputs); other kinds keep every take a run makes, as
  before (a stricter rule broke the fake runs that answer a video run with a picture).
- **A sound follows its shot**: its inputs include the shot's shown take, so a new take of the shot puts it out of
  date; why it can't run: `Shot 2 has no take yet`, `its shot is not on the board`.
- **Covers and orbs** follow the shown take's class (`MagicTake.sound`): a sound placed on the Music lane draws an orb;
  a music take's cover is the Gallery's gradient (`coverFor`) of the block's style; the side panel and the dialog's
  stage show them large over the player (an audio take's thumb_url, its WAV, is never drawn as a picture).
- **Menus**: Run on music and sound blocks, lanes and several blocks; Music's Model › lists studio's models; Sound's
  Make from Shot n video sets `from` and a model that takes a video, then runs; a shot's Make sound from video adds a
  sound under it on the Sound lane (Overwrite), tied to it, and runs it (greyed without a Sound lane, a model that takes
  a video, or a take). A sound added from Add under a shot starts from that shot's video.
- **Not built:** Loop (frame 2g, "Stable Audio only": Stable Audio 3's sound workflow has no loop param); the music
  form's "Tied to Shots 1-3 · stretches with them" row (the dialog's Tied section and the side panel show the tie); a
  song's sections as markers on its block; Sound's Model › (not on the board's menu); dialogue blocks running; the
  Assistant's music and sound wording (phase F). The sample board keeps its model names (the fake mode). No browser
  walk on the dev stack (the owner uses it live; covered by the vitest journeys in fake mode).
