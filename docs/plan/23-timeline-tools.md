# helmstudio-next plan — Timeline tools: blade, trim, roll, slip, slide, range, zoom

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-22), queued to build**. A plan, not in a milestone: it becomes one in
[08-milestones.md](08-milestones.md) when the owner schedules it. Nothing here is built. The tools need **no API
change**: they are new entries in the shared edit functions (`t_edit` / `edit`), the README's Timeline rules and the
fixtures. Only the two optional parts (linked sound clips and markers, open points 5 and 6) change the schema; they
would take API **1.46.0** (main is at 1.43.0, `assistant` takes 1.44.0, plan 22 takes 1.45.0).

The owner's ask, 2026-11-22, looking at the Timeline screen: "plan basic editing tools like blade etc as in final cut
pro, five ui option to zoom in and zoom out. it should become more polished professional tool".

Board: [TimelineTools](../mock/TimelineTools.dc.html) (page Timeline): five options for the tool bar and the zoom
control on the existing [Timeline](../mock/Timeline.dc.html) panel, then the shared states whichever option wins.
The look is our own (the app's themes, chips, clip colours and shapes); the conventions are the industry's (blade,
ripple, roll, slip, slide, range, J/K/L). No other editor's look, icons or names are copied (review renamed
Position to Overwrite and skimming to hover scrub, both one editor's own names).

Facts come from a read of the code on 2026-11-22: `web_service/components/timeline/` (`editor.tsx` keys, `commit`,
`save`, zoom state; `lanes.tsx` ruler, drags, wheel zoom, clip drawing), `web_service/lib/timeline.ts` (`Op`, `edit`,
`apply`, `cuts`, `snapPoint`, `dropIndex`, `clipAt`), `web_service/lib/timeline-preview.ts`, `contracts/openapi.yaml`
(`TimelineDoc`, `TimelineClip`, `TimelineTrack`, `TimelineTarget`, `TimelineUpdate`, `TimelineTake`, the PUT, undo and
redo), `contracts/README.md` (Timeline; Timeline rules), `contracts/check.py` (`t_edit`, `snap_point`) and
`contracts/fixtures/timelines/`.

## 27. Timeline tools and zoom

### 27.1 What the code does today

- **Edits are whole-document writes.** The web runs an `Op` through `lib/timeline.ts` `apply` (edit, then normalize),
  draws the result at once and PUTs the whole document with the `revision` it was made on. On a 409 `save` re-reads
  and re-applies the same `Op` once (edits address clips by `id`). studio_service only **normalizes** (checks, snaps,
  lays V1); at run time it runs `add`, `insert`, `fill` and `place` itself and no other edit, but
  `studio_service/api/timeline/edit.go` is a **full Go port** of `t_edit` that `TestEditFixtures` runs against
  `edits.json`. So every edit lives three times: `check.py` (the reference), `lib/timeline.ts` and `edit.go`, and
  `fixtures/timelines/edits.json` holds the shared cases all three pass.
- **A 409 re-apply that fails is silent today**: `save` re-applies the `Op` on the newer revision and, when it no
  longer applies (an error or problems), drops it with no notice; the draft just disappears.
- **The Op union** today: `move` (V1 by index, audio by `at`), `trim {clip, edge, delta}`, `split {track, at, id}`,
  `remove`, `lift`, `replace`, `set`, `add`, `insert`, `place`, `fill`, `add_track`, `remove_track`, `track`.
- **The document**: V1 (video) laid end to end from 0, a gap is an empty slot (`take_id` null, `hold`), so V1 is
  already **magnetic**; then 0–8 audio tracks whose clips sit at `at` and may not overlap (`overlap`, `past_end`).
  A1 is V1's own sound, drawn under it, so V1's picture and sound are always linked. Audio-track clips are not tied
  to any V1 clip: a ripple on V1 leaves them where they are. There are no markers.
- **Time**: seconds on the target's exact frame grid (`frames(s) = rnd(s × num / den)`); audio-track `in` / `out` on
  the sample grid. Lengths compare integers.
- **What the server refuses** (normalize, 422 `TimelineError`): `trim` (in < 0, under one frame, out past the take),
  `hold`, `overlap`, `past_end`, the dissolve rules (`dissolve_handles`, `dissolve_length`, …), `shot_invalid`,
  `too_many_clips` (1000; 500 per track), `too_large` (256 KB). Any tool's result passes the same rules, so a slip
  past the take's end or a slide over a dissolve's handle is refused there too.
- **UI**: Split at playhead (S), New shot, New line, Mark in / out (I / O), Clear range, Undo / Redo (server
  revisions), clip trim in the inspector and by edge drag (7 px handle, snapping to cuts within 6 px), `[` / `]` trim a
  frame, Alt+arrows move, Delete removes (V1 ripples), arrows step a frame (Shift: 1 s), Space plays.
- **Zoom**: one number, `pps` (pixels per second, 5–400, start 40), changed by `=` / `-` (×1.25 / ×0.8) and Ctrl/⌘ +
  wheel. It zooms about the left edge, not the pointer; there is no slider, no fit and no frame ruler.
- **Drawing**: every clip and tick is in the DOM (no virtualising); the lanes' width is `end × pps + 160`. A V1 clip
  shows one poster (`thumb_url`) as its background; an audio clip's "waveform" is a CSS stripe, not the sound.

### 27.2 The tools

One tool is active at a time; holding a tool's key switches to it while held and back on release (a held key over
250 ms is temporary, a tap is sticky). Esc returns to Select; a second Esc (or Esc in Select) clears the selection and
the range as today.

| Tool | Key | Pointer | Edit (one `Op`, one revision, one undo) | New op? |
|---|---|---|---|---|
| Select | A | click selects (Shift adds); drag a clip moves it (V1 reorders, ripples; audio: `at`); drag an edge = ripple trim | `move`, `trim` | no |
| Trim | T | edge = ripple trim; the cut between two clips = roll; a clip's body = slip; Alt + body = slide | `trim`, `roll`, `slip`, `slide` | roll, slip, slide |
| Blade | B | a dashed cut line follows the pointer on its frame; click cuts the clip under it; Shift: every track at that frame | `split` (any frame, not only the playhead); Shift: `split_all` | `split_all` |
| Range | R | drag on the tracks or the ruler marks in / out (snapping); the range then takes Delete, Lift, New shot and the take actions | `remove_range` (ripple), `lift_range` | yes, two |
| Overwrite | P | drag a clip; on V1 it overwrites what is under it and leaves an empty slot behind (no ripple); on audio it overwrites the clips under it | `overwrite` | yes |

Keys without a tool: **⌘B** blade at the playhead on the selected track (else V1), **Shift+⌘B** on every track
(S stays as today's Split at playhead, open point 3); **Delete** = ripple delete (V1 closes the gap; on an audio track
the later clips on that track move left by the length: `remove {clip, ripple: true}`, a new optional flag that only
audio tracks read, absent = today's remove; open point 4), **Shift+Delete** = lift (V1: an empty slot of the
same id, as today's `lift`), **Alt+[ / Alt+]** trim the edge to the playhead.

**Trim rules** (in frames, the same bounds as today's `trim`; `least` = max(1, d_in/2 + d_out/2)):

- **Ripple trim** (V1, default): today's `trim`; later V1 clips move. On an audio track an edge stops at its neighbour
  (overwrite only with the Overwrite tool). A linked sound clip (open point 5) moves with its V1 clip.
- **Roll** `roll {clip, delta}`: the cut before `clip` moves by `delta`: the previous clip's out + delta and this
  clip's in + delta (an image or slot: its hold ± delta); the sequence's length does not change. Bounds: both clips'
  handles (outgoing: `out + delta + d/2 ≤ take length`; incoming: `in + delta − d/2 ≥ 0`) and `least` on both; the
  dissolve on the cut moves with it. V1 only (`roll_track`); first clip: `roll_first`.
- **Slip** `slip {clip, delta}`: in and out + delta, length and place kept. Bounds: `in + delta ≥ d_in/2`, `out + delta
  ≤ take length − d_out/2`; an image or empty slot has nothing to slip (`slip_still`). V1 video and audio-track clips.
  On an audio track `in` / `out` move on the **sample grid** (`delta` in frames → samples, as `trim` does) with
  `in + δ ≥ 0` and `out + δ ≤` the take's length in samples; `at` and the length do not change, so no `overlap`.
- **Slide** `slide {clip, delta}`: the clip keeps its content and length and moves along V1 by `delta`; the previous
  clip's out + delta, the next clip's in + delta (holds for images and slots). Bounds: both neighbours' handles and
  `least`; no previous or next clip: `slide_end`. The two cuts' dissolves stay on their cuts.
- **Overwrite** `overwrite {clip, at, track?, ids}`: V1: the clip leaves an empty slot of its length where it was
  (`lift`), then the frames `[at, at + length)` are cut out of whatever is there (splits at both ends, the inner
  clips removed) and it goes in at that place; past V1's end an empty slot fills the gap first. Audio: the clips under
  `[at, at + length)` on the target track are trimmed or split around it (no `overlap`). The moved clip's dissolve and
  those on the two new cuts go.
- **Blade on every track** `split_all {at, ids}` (a new op; today's `split` keeps its shape): today's `split` on each
  track with a clip strictly inside frame `at` (`split_edge` and `split_shot` clips are skipped, not errors; none at
  all: `split_none`); `ids` in track order. One op so the 409 re-apply in `save` stays one step.
- **Range** `remove_range {from, to, ids}` / `lift_range {from, to, ids}` (sequence seconds, snapped): split every track
  at both ends, then remove (V1 ripples, audio clips after `to` move left by the length; open point 4) or lift (V1: one
  empty slot; audio: a gap). A clip with a shot inside the range: `range_shot`.
- **New ids**: every op that makes clips carries their ids, as `split` (`id`) and `place` (`gap_id`) do today, so
  `check.py`, the web and Go make the same document and a 409 re-apply makes the same ids: `overwrite.ids`,
  `remove_range.ids`, `lift_range.ids` and `split_all.ids` are used in track order, then left to right (the right part
  of each split, then V1's new empty slot), unused ones ignored, too few: `ids_short`. An `overwrite` over a clip with
  a running shot: `overwrite_shot`.

Every new op is also written in `check.py`, `edit.go`, the README's edits list and `edits.json` (each bound, each error
code). Bounds clamp in the web while dragging (the drag stops at the handle, the clip edge turns the warning colour);
the op itself returns an error code when called past them, like `trim`.

### 27.3 Moving around and playing

- **J / K / L**: L plays forward, again ×2, ×4, ×8; K pauses; J plays backward the same way; K+J / K+L step a frame;
  Space plays / pauses. Forward speeds set every preview element's `playbackRate`. Backward is a timed seek every
  frame (browsers do not play video backwards smoothly), drawn from the posters while it runs and silent.
- **Arrows**: ← / → one frame (Shift: 1 s, as today); **↑ / ↓** jump to the previous / next cut on any track
  (`cuts()`); **Home / End** the start and the end.
- **I / O** mark in / out (today), **Alt+X** clears the range, **X** marks the clip under the playhead.
- **Hover scrub** (Shift+S toggles; S stays split, open point 3): a thin line follows the pointer over the tracks
  and the ruler with its timecode; with it on, the viewer shows `frame_at` at that frame (throttled to one seek per
  animation frame; sound off). The playhead does not move until a click. Off by default (open point 7).
- **Shortcut sheet** (`?`, and a button): a dialog listing every key by group (tools, edit, play, zoom, marks).

### 27.4 Zoom

- **Scale**: `pps` becomes a log scale from **fit** (the whole sequence fills the visible track width: `pps_min =
  visible_width / max(duration, 5 s)`) to **frames** (`pps_max = 64 × fps`, 64 px per frame: room for a frame number).
  The slider and the field show the visible span ("8.0 s", "24 f").
- **Controls**: ⌘= / ⌘- (×1.25 / ×0.8; the default page zoom is prevented while the Timeline has focus), the bare `=`
  / `-` as today, **Shift+Z** fit, trackpad pinch (a wheel with `ctrlKey`; Safari's gesture events) and Ctrl/Alt +
  wheel. **Every zoom keeps the time under the pointer still** (keys and buttons: under the playhead when it is on
  screen, else the centre): `scrollLeft' = t × pps' − x`.
- **Ruler**: tick steps from 1 frame to 10 min (frames, 1 s, 2 s, 5 s, 10 s, 30 s, 1 min, 5 min, 10 min, picking the
  first with ≥ 80 px); at frame zoom each frame has a tick and its number within the second (`0:10 · 7`).
- **Detail grows with zoom**: a V1 clip shows a **filmstrip**, one tile per `tile_w` px (16:9 of the lane's height),
  each tile the take's frame at that tile's source time; at frame zoom one tile per frame. An audio clip (and A1)
  shows its **waveform**: peaks per pixel column from the real sound, more columns as it zooms. Both are made in the
  browser (open point 8): frames by a hidden `<video>` seek + canvas, at most 2 at once, visible clips first, cached
  per take and time bucket (an LRU of about 300 tiles); peaks by `decodeAudioData` into a mono 8 kHz
  `OfflineAudioContext`, reduced to a min/max pyramid (×2 levels) and cached per take, takes over 20 min skipped (the
  stripe stays). Plan 22's thumbnails stay the first tile while frames load.

### 27.5 Snapping, linked clips, markers

- **Snapping** (N toggles; a magnet button shows the state; holding ⌘ while dragging inverts it for that drag): the
  targets are today's cuts plus the **playhead**, the range's in / out and, with open point 6, markers. `snap_point`
  is unchanged (6 px, the earlier cut on a tie); the web only adds the extra points to `list`. A thin line and a chip
  ("Snap · cut 0:08.00") show the target while it holds.
- **Linked clips** (open point 5): an audio-track clip may carry `link` (a V1 clip id). Ripple edits on V1 move a linked
  clip by the same amount; removing the V1 clip removes it; blade on V1 at a frame inside a linked clip cuts it too. A
  linked clip that would land on another clip of its track makes the edit fail as today (normalize `overlap`, 422; the
  web stops the drag where it would). Selecting either selects both; Alt+click picks one. Without it, Shift-blade and
  Range already cut every track together.
- **Markers** (open point 6): `markers [{id, at, name?, color?}]` on the document (at most 200), M adds one at the
  playhead (or the hover line), drawn on the ruler, snap targets, ↑ / ↓ stop at them with Alt.

### 27.6 What changes where

- **contracts**: the README's Timeline rules gain the new ops (27.2) and the keys (27.3, 27.5) in "Snapping" and its
  interaction constants; `check.py` gains `roll`, `slip`, `slide`, `overwrite`, `split_all`, `remove_range`,
  `lift_range` in `t_edit`, each with fixtures in `edits.json`; `snap.json` gains the playhead and range points as
  ordinary members of `list`. No openapi change and no version bump for these (the wire is the same whole document).
  Open points 5 and 6 add `TimelineClip.link` and `TimelineDoc.markers` (1.46.0), their normalize rules
  (`link_not_v1`, `link_track`, `marker_past_end`, `too_many_markers`) and fixtures in `normalize.json`.
- **studio_service**: no handler change (normalize already checks every result), but `api/timeline/edit.go` gains the
  same new ops (`Op` gains `ripple`), or `TestEditFixtures` fails on the new `edits.json` cases. For links and markers:
  normalize checks and keeps them (jsonb, no migration), and `remove` / `lift` done by studio itself (none today) would
  follow.
- **web_service**: `lib/timeline.ts` (the new ops, tested against the fixtures), `components/timeline/` (a tool state
  and cursor per tool, the tool bar and zoom control of the chosen option, the shortcut sheet, J/K/L in
  `timeline-preview.ts`, the virtualised lanes, the filmstrip and waveform workers).

### 27.7 Undo and 409s

Each gesture is **one `Op` on pointer-up** (the drag itself only draws the draft), so one gesture is one revision and
one ⌘Z. Composite gestures (Shift-blade, a range delete, an overwrite) are single ops for the same reason and so that
`save`'s 409 re-apply repeats the whole gesture or none of it. A re-apply that no longer fits the newer revision (a
slide past a handle another tab just trimmed) is dropped as today, but with a notice ("The sequence changed: your
slide no longer fits and was not saved."), not silently. Keyboard repeats (holding `[`) send one op per press;
they stay inside the 100 kept revisions but fill undo quickly, so repeats within 400 ms are merged into one trim op
in the web before it is sent (the draft shows each frame).

### 27.8 Performance

- **Virtualise** the lanes: a scroll container with a spacer of `end × pps` px and only the clips, ticks, filmstrip
  tiles and waveform columns that intersect the visible window plus one screen each side are in the DOM. Needed
  because frame zoom on a 10-minute 24 fps sequence is about 920 000 px wide (past browsers' element limits) and the
  document may hold 1000 clips.
- The ruler and waveforms draw on one `<canvas>` per lane, redrawn on scroll and zoom in one animation frame; clips
  stay buttons (focus, labels).
- The draft during a drag recomputes only `edit` + `normalize` on the in-memory document (no request) and throttles to
  one per animation frame.
- Frame and peak extraction run on idle time with a cap of 2 decoders, cancel when the clip scrolls away, and never
  decode for an offline clip.

### 27.9 Accessibility

Tools are a real radio group (`role="radiogroup"`, each a button with `role="radio"`, `aria-checked`, an `aria-label`
and its key in `aria-keyshortcuts` and the title; one tab stop, arrows move inside it). The zoom slider is an `<input
type="range">` with an `aria-valuetext` ("visible span 8 seconds"); −, + and Fit are buttons with labels. Every tool has
a keyboard path: with a clip focused, a key pair does its edit a frame at a time (Trim: `[` / `]`; Roll: Alt+, / Alt+.;
Slip: , / .; Slide: Shift+, / Shift+.), and Blade cuts at the playhead. The blade's cut line and the snap indicator also
show their timecode as text, not only colour. A live region announces each edit ("Cut Shot 3 at 0:10.12").

Keys with Alt or Shift are matched on `e.code`, not `e.key` (macOS Option and Shift change the character: ⌥[ gives
“, ⇧, gives <); ⌘= / ⌘− / ⇧⌘B call `preventDefault` while the Timeline has focus (page zoom, the bookmarks bar). The
existing rule stays: no key acts while typing or in a dialog.

### 27.10 The five UI options (board TimelineTools)

- **A · Palette**: a compact tool palette left of the tracks (icons, key letters) and a zoom slider with fit bottom
  right of the panel.
- **B · Tool row**: a row above the timeline with a segmented tool picker (icon + short name), snap and hover-scrub
  toggles, and a zoom field with −, + and Fit on the right.
- **C · Floating pill**: a floating pill of tools over the timeline's top edge; zoom by dragging the ruler up or down
  and its scale chip.
- **D · Side strip and overview**: tools in a vertical strip at the timeline's left edge; zoom with an overview bar of
  the whole sequence whose window you drag and stretch.
- **E · Minimal**: no tool bar: tools by key with a badge on the cursor; a small zoom slider in the timeline header and
  a "?" button for the shortcut sheet.

Every option reaches what it shows no button for by key: the shortcut sheet by `?`, hover scrub by Shift+S, snapping by
N (A gained a `?` button in review; only B shows a hover-scrub toggle).

Recommended: **B**, with D's overview bar as a later add (open point 1). B keeps every tool visible with its name (new
users find Blade without knowing B), fits the app's chip row above the lanes, takes no track width and leaves the
ruler free for seeking and range marks.

### 27.11 Tests

- `check.py` and `lib/timeline.ts` against `edits.json`: each new op's happy path, each bound (handles, `least`,
  dissolves), each error code, and normalize's acceptance of the result.
- Web unit tests: zoom about the pointer (time under it fixed within half a pixel), fit, the tick-step choice, the
  virtual window, the key map (no collisions; held vs tapped), the repeat merge.
- The browser pass on the `helmstudio-test` stack: each tool on a three-clip sequence with a dissolve and a music
  track, ⌘Z after each, a 409 from a second tab during a slide, frame zoom on a 10-minute sequence (scroll stays
  smooth), the shortcut sheet by keyboard only.

### 27.12 Phases

1. **Design**: this plan and the board, decided by the owner (done when decided).
2. **Contract rules**: README edits, `check.py` ops, fixtures, the Go port in `edit.go` (7 h).
3. **Web edits**: the new ops in `lib/timeline.ts`, the fixture tests, the repeat merge (4 h).
4. **Tools**: tool state, cursors, drags per tool, the blade line, the chosen option's bar, the key map, the shortcut
   sheet, the live region (7 h).
5. **Zoom**: the log scale, pointer-anchored zoom, slider / field / fit, pinch, the frame ruler, virtualised lanes on
   canvas (7 h).
6. **Moving and playing**: J/K/L, ↑ / ↓, Home / End, hover scrub (4 h).
7. **Detail**: filmstrip and waveform extraction, caches, drawing by zoom (6 h).
8. **Optional, 1.46.0**: linked sound clips and markers: contract, normalize in Go and `check.py`, web (8 h).
9. **Gates**: `make test lint` (web), `make contracts-check`, `make test` on `helmstudio-test`, the browser pass (3 h).

About 38 h without phase 8, 46 h with it. Phases 2–5 (about 25 h) ship the tools and the zoom; 6 and 7 add the polish
and can follow.

### 27.13 Open points for the owner

**Decided by the owner, 2026-11-22 (one by one):** 1. UI option **B, the tool row**. 2. Five tools with a smart Trim
(roll, slip, slide by where you grab). 3. S stays split at the playhead, next to ⌘B. 4. Audio tracks ripple (later
clips on the track move left). 5. Linked sound clips, yes (1.46). 6. Markers, yes (1.46). 7. Hover scrub **on by
default** (toggle in the tool row, ⇧S). 8. Filmstrips and waveforms made in the browser. 9. Max zoom 64 px per frame.


Each with the recommended option first.

1. **Which UI option**: B, the tool row (later D's overview bar on top) · A, the palette · C, the floating pill · D,
   the side strip with overview · E, minimal by keys.
2. **Tool set**: five tools (Select, Trim with roll / slip / slide by where you grab, Blade, Range, Overwrite) ·
   separate Roll, Slip and Slide tools (eight; more buttons, no grab zones to learn).
3. **S key**: keep S = Split at playhead beside ⌘B (no habit broken) · S becomes the hover-scrub toggle, split only on
   ⌘B, Shift+S freed.
4. **Ripple on audio tracks** (Delete and range delete): later clips on the same track move left (as V1) · audio
   tracks never ripple (a gap stays; safer for music beds).
5. **Linked sound clips** (`link`, 1.46.0): yes, so dialogue stays in sync when V1 ripples · not now (Shift-blade and
   range edits cut every track; a ripple on V1 can still pull dialogue out of sync).
6. **Markers** (1.46.0): yes, with names and snapping · not now.
7. **Hover scrub**: off by default with a toggle · on by default.
8. **Filmstrip and waveform source**: made in the browser (no contract, works on old takes) · a runner job writing a
   sprite sheet and a peaks file per take (exact and shared, but a media job, storage and 1.46 fields) · posters and
   the stripe as today.
9. **Max zoom**: 64 px per frame (frame numbers fit) · 32 px (less scrolling, numbers every other frame).
