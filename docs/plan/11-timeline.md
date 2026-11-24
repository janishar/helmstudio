# helmstudio plan — Timeline

[← Plan index](../README.md)

## 15. Timeline

The document model, snapping maths and export rules are ported from the current
helmstudio (`internal/timeline` and `internal/export`, about 2.3k lines of Go).
The existing `<helm-timeline>` and `<helm-player>` elements are reused through
a small client adapter and a token shim; this is real work, not a drop-in. What
is new is that the timeline is where shots are directed.

### 15.1 Sequences

- **New sequence**: name, size, frame rate; defaults come from the first clip.
- Clips arrive through the gallery (section 9.1): "Add to timeline" on a take,
  or the gallery picker opened from the timeline's add-clips tray.
- A sequence has **defaults for new shots**: a list of library items — its
  cast (elements) and its look.

### 15.2 Editing

- Tracks: one or more video tracks, the clips' own audio, and extra audio
  tracks for music or voice.
- Drag to reorder, trim either edge (snaps to frames and to neighbouring
  clips), split at the playhead, dissolve between neighbours, per-clip gain
  and mute. Images are clips with a duration you set.
- Every edit is saved as a revision; undo and redo walk the revisions.
- Preview plays across cuts in the browser without rendering.

### 15.3 Directing shots

Four things, and only four, generate from the timeline.

- **New shot** — at the playhead or in a gap, opens the composer (section 8.2)
  docked in the timeline, prefilled with the sequence's defaults. Options:
  - start from the previous clip's last frame;
  - end on the next clip's first frame (in a gap the duration snaps to fit);
  - "from this clip": prefill a selected clip's own settings to redo it.

  The result fills the slot. While it runs, the slot shows a placeholder clip
  with progress.
- **Take actions on a clip** — a clip offers its take's actions (section 9.1:
  Extend, Retake…). The result goes into the clip's slot, replacing it or
  inserted after it. "Retake" on a clip takes the selected range.
- **Replace** — a pick (section 4) among the alternatives made for that slot.
- **Re-render** — on the sequence or a selection of clips, choose a preset (for
  example Final). studio_service builds one pipeline with a step per clip (each
  clip's own template, seed and inputs) and an edge from a clip to any clip
  that starts from its last frame, and runs it as a batch (section 7.2); each
  result becomes the current alternative in its slot.

Each queues a normal run; the new take is in the gallery with lineage to the
clip it came from. An option or action appears only when the clip's family has
the template for it.

### 15.4 Export

- An export plan is shown before running: **stream copy** when every clip
  matches the sequence's size, frame rate and codec; otherwise the reason it
  must re-encode. Audio is always re-encoded (the 81 ms AAC lesson).
- Export is a queued job with progress and cancel; the file is a take whose
  inputs are its clips.
- Hardware encode on Mac (VideoToolbox); software fallback on Linux.

### 15.5 Storage

`timelines(id, name, target{width,height,fps,sample_rate}, tracks JSON,
defaults[] library item ids, revision)` plus `timeline_revisions` (last 100).
A clip is `{take_id, in, out, gain, transition}`. A missing take shows as an
offline clip and never breaks the sequence.
