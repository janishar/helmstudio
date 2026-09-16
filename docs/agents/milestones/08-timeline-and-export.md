# M8 — Timeline and export

**Effort:** 10–12 days. **Machine-bound demos:** a run of h3 takes exported by
the fast path, and a four-clip sequence exported through conform (M8a); that
sequence edited in `helm-timeline` (M8b).

Expanded at kickoff from the questions in `docs/agents/reports/08-timeline-and-export.md`,
answered "recommendations for all" (`docs/decisions.md`, "2026-09-16 · M8 timeline
and export", Q1–Q21). Four answers carry a correction made while recording
(Q5, Q7, Q9, Q10), signed off by the human. Two briefs, reviewed separately
(Q2), in this order:

1. M7's order through M7b, each reviewed: M6a's review, M7a, M6b, M7b.
2. **M8a**, the document, the API and export.
3. **M8b**, the editor, built on a reviewed M6b, M7b and M8a.

## Reads

- `docs/design/01-prd.md` R36, R40, R44–R49, §14's timeline line, §15
- `docs/design/02-data-model.md` §4 (jobs, log files), §5, §8, §10
- `docs/design/03-design-system.md` §10, §11, §17
- `docs/design/04-packages.md` §5 and §7
- `docs/design/05-sdk-and-custom-studios.md` §5a, §6, §7
- `docs/design/06-storage.md` §4, `docs/design/07-platform-services.md` §3–§5,
  and the timeline section of `docs/design/08-h3-dry-run.md`
- `api/openapi.yaml`: assets, gallery, jobs, events
- `docs/decisions.md` in full; the reports of M4, M6, M7 and this milestone's
  kickoff, whose "What I measured" is the evidence several answers rest on; the
  review findings of M7a and M7b
- h3 studio at `a6eb54f`: `server/timeline.go` and `h3c/h3_ffmpeg.c`

## Known at freeze

- **The framework owns the document and the export pipeline; `helm-ui-sdk`
  ships the editor.** A sequence made from four studios is nobody's studio
  document and must survive uninstalling half of it.
- **Conform to a declared target, with a stream-copy fast path when legal.** A
  run of takes from one studio is the common case and should cost seconds, not
  a re-encode.
- h3's `CombineTimeline` always re-encodes. That is the behaviour being
  replaced, and it is why the fast path exists.
- **ffmpeg licensing is closed (Q4): an LGPL build with videotoolbox.** It was
  the user's to close, and it decides what M9 can bundle in a signed `.dmg`.

---

## M8a — the document, the API and export

Starts only once M7b's review is done and its findings are addressed (Q2). If a
finding in M7a's or M7b's review changes a convention this brief copies (the
launcher tags, error shapes, the schema version), the finding wins, and the
change is raised at M8a's start rather than resolved in code.

### Tasks

0. **The contract, before any code.**
   - Amend the design per Q4–Q21:
     - 01: R44–R49, and §15's ffmpeg row
     - 02: §5 (`timelines` gains `studio_id` and `deleted_at`; revisions; the
       export's process identity; `log_files` kind `export`), §8's reclaim
       clause, and §10's "one `item_inputs` row per clip"
     - 03: §11's chip, and §10's export label
     - 04: §5's "inside any studio", and §7
     - 05: §6's document, rules and export, and §7's operations
   - Add Q18's operations to `api/openapi.yaml`, with `timeline_id` on `Item`
     (Q17), then `make generate`.
   - Write the new schema version's DDL into 02 first.
1. **ffmpeg** (Q4, Q5, Q6).
   - Lookup: `HELM_FFMPEG` and `HELM_FFPROBE`, else `PATH`. Record the path,
     version and configuration; check the minimum major and the allowed
     encoders.
   - Without a usable ffmpeg, `:plan`, `:export` and a video `/thumb` answer
     501 `unsupported` with `tool_missing`. Nothing else needs ffmpeg.
   - `ffprobe -of json` into typed stream facts.
   - A video poster for `/thumb`: one frame read as `rawvideo`, then scaled and
     encoded by M4's thumbnailer.
   - The encoder allowlist, as a test over every argument list built.
2. **The document** (`internal/timeline`, new; pure functions).
   - Tracks, clips, holds, the dissolve and gain (Q8).
   - Snapping to the target (Q7), and the checks that need a clip's length,
     against its asset's recorded `duration_s`.
   - Target and preset (Q9), and duration.
3. **Storage and operations** (Q10–Q12, Q17–Q19).
   - The schema: owner, soft delete, revisions, export identity, the log kind.
   - Every operation in Q18, with `If-Match` per Q10 and access per Q11.
   - Reclaim's query reads only live timelines.
   - `:append` and `:open` per Q19.
   - Served by the daemon, `helm dev` and the embedded provider.
4. **The plan** (Q13): the legality predicate over probed facts, and
   `GET /timeline/{id}:plan`.
5. **The graph** (Q3, Q14): a pure function from the document and the probed
   facts to an ffmpeg argument list, for the copy path (picture copied, sound
   through the graph) and for conform. Each filter choice is a judgement call in
   the report, for the human to sign off on the Mac.
6. **Export jobs** (Q16, Q17).
   - ffmpeg writes `<assets>/tmp/export-<job>.mp4` as a process group whose
     identity is recorded before it writes.
   - Progress comes from `-progress pipe:1`. Cancel from
     `POST /timeline/{id}/exports/{job}:cancel` and from
     `/launcher/jobs/{id}:cancel` goes through one runner.
   - The finished file is verified with `ffprobe`, adopted by the existing
     hardlink path, and recorded as an item with `timeline_id` and its `clip`
     inputs, with the `job` event and the log.
   - The startup sweep runs in the daemon and in `helm dev`.
7. **The golden harness** (Q15, `test/media/`).
   - Fixtures, and the committed script that makes them.
   - `FFMPEG_MAJOR` and the architecture beside the goldens.
   - Copy: decoded hashes against the sources. Conform: goldens of the graph's
     decoded output. Encoded files: `ffprobe` only.
   - In `make gate`, failing without ffmpeg unless `HELM_ALLOW_MISSING_FFMPEG`
     is set.
8. **Conformance** (`test/conformance/`): the timeline operations against both
   providers.
9. **The conform demo's fixture studio** (Q21, `test/studios/sequencer/`, new):
   a manifest with `local_path`, and a small program that uploads the ltx, AuK
   and iris clips with `POST /assets`, assembles the sequence and exports it. It
   declares `assets`, `timeline` and `gallery.read_all`, the last so that it may
   add h3's take (Q11). It is for the machine-bound demo, not the gate.

### File list

- `internal/timeline/**` and `internal/export/**` (new)
- `test/studios/sequencer/**` (new)
- `internal/media/**`, `internal/api/**`, `internal/store/**`
- `internal/platform/**`: spawn and process-identity seams only
- `api/openapi.yaml` (the timeline operations, `Item.timeline_id`, and the job
  and thumbnail text); `api/gen/**` only if generation needs it; generated files
  only through `make generate`
- `packages/helm-runtime-sdk/go/embedded/**`, if the embedded provider needs the
  export runner wired
- `cmd/helmstudio/**`, `cmd/helm/**` (`helm dev`)
- `test/conformance/**`, `test/media/**` (new)
- `Makefile`, `docs/agents/gate.md`
- `docs/design/01-prd.md`, `02-data-model.md`, `03-design-system.md` (§10,
  §11), `04-packages.md`, `05-sdk-and-custom-studios.md`
- `docs/decisions.md`, and the milestone report

### Definition of Done

- **Legality is a predicate over probed facts, and every fact counts.**
  - A table test starts from fixture clips that copy, and changes one fact at a
    time: codec, container, profile, level, size, aspect ratio, field order,
    pixel format, time base, frame rate, each colour tag, extradata, a trim, a
    hold, an image, a transition.
  - Each change alone turns the plan to conform, and names that reason.
  - A fixture adopted with false hints gets the plan its probed facts give.
- **The copy is bit-identical where it claims to be.**
  - The export's decoded picture hashes equal the sources' in order.
  - Its sound has `round(duration × sample_rate)` samples, with nothing added at
    the start or at a cut. A planted stream copy of the sound fails.
  - Its colour tags are the sources', untagged included (Q9).
- **Conform is checked by content, never by a file existing.**
  - A golden of the graph's decoded output for each mismatch fixture, pinned to
    `FFMPEG_MAJOR` and the architecture.
  - A one-frame shift, BT.709 read for an untagged source, or `amix` normalising
    each fails a golden.
  - The encoded export is checked with `ffprobe` (codec, profile, size, rate,
    frames, duration, BT.709 tags, sample rate, channels, start times), never
    by its bytes.
- **Time is canonical.**
  - An unaligned write returns the snapped document, and writing that document
    back changes nothing.
  - 23.976 fps round-trips.
  - A clip whose asset records no duration, and that gives no `out`, is refused.
- **Access.** A studio without `gallery.read_all`:
  - cannot list, read, edit, revert or export another studio's timeline (404)
  - cannot add an asset it cannot read, by create, patch, append or revert
    (422, identical for an asset that does not exist)
  - cannot export a timeline holding a clip it can no longer read
  - sees a clip's `studio_id` only as Q11 says
- **Undo and concurrency.**
  - `PATCH` and `:revert` without `If-Match` are refused, and with a stale one
    are 409 `etag_mismatch`.
  - A revert writes the old document as a new revision, and a revert naming a
    reclaimed asset is refused.
  - `:append` needs no `If-Match` and returns the new revision.
- **Reclaim.** An asset named only by a live timeline is not reclaimable. One
  named only by a deleted timeline, or only by an old revision, is.
- **An interrupted export leaves nothing behind.**
  - With the daemon killed by `kill -9` mid-export, the next start stops the
    verified ffmpeg, deletes its file, and marks the job `interrupted`. No asset
    and no item exist, and nothing is signalled whose identity does not match.
  - Cancel, by either path, does the same as `cancelled`.
  - A failed verification adopts nothing.
- **Survives uninstall.** After the studio whose clips a timeline holds is
  uninstalled, the document, its exports and their lineage are still readable
  by whoever could read them, and it still exports.
- **Licensing.** A test fails if any argument list helmstudio builds names an
  encoder outside Q4's allowlist.
- **No ffmpeg.** `:plan`, `:export` and a video `/thumb` answer 501
  `unsupported` with `tool_missing`, while creating and editing a timeline still
  work. The gate fails without ffmpeg or `h264_videotoolbox` unless
  `HELM_ALLOW_MISSING_FFMPEG` is set.
- **Conformance** covers the timeline operations against both providers.
- **`make gate` is green**, including drift.
- **Machine-bound.** On the Mac with the real daemon:
  1. **The fast path.** Four h3 takes from one session at one size, exported to
     a target declared from them. The plan says "video stream copy", the export
     takes seconds, decoded picture hashes equal the sources', and sound starts
     with the picture at every cut. It plays in QuickTime, Safari and Chrome.
  2. **Conform.** An h3 take, an ltx clip, an iris still and an AuK line, with a
     dissolve and gain. It plays in the same three players, its colour is
     compared with the sources by eye, and it appears as an item with its clips
     as inputs. The ltx, AuK and iris clips are uploaded by the fixture studio
     of task 9, so every clip but h3's carries that one origin (Q21).
  3. **The graph signed off:** videotoolbox settings, the three players, colour
     (Q3).
  4. **A daemon killed with `kill -9` mid-export,** then restarted.

### Review focus

- **The milestone's own four:**
  - Is stream-copy taken only when genuinely legal? Codec, profile, level,
    timebase, pixel format, colour metadata — and, from the kickoff's
    measurements, extradata, trims and sound. A fast path taken illegally
    produces a file that plays on the developer's machine and nowhere else.
  - Do the golden tests compare frame hashes, or only that a file appeared?
  - Does the document survive a studio being uninstalled?
  - What happens to an export interrupted halfway — a partial file left where a
    finished one is expected?
- **Sound.** Is it ever copied? Does any cut add priming or padding?
- **Hints.** Does anything in legality or conform read an adopt or upload hint?
- **Colour.** Is an untagged source read as BT.601, a conform export tagged
  BT.709, and a copy's tags left as they were?
- **Access.** Can a studio obtain bytes or ids it may not read, through a
  timeline, a revision, an export, an append or a clip's `studio_id`?
- **Identity.** Can the sweep ever signal an ffmpeg it did not start?
- **The tests.** Plant Q15's bugs:
  - skip the extradata comparison
  - copy the sound
  - copy a trimmed clip
  - leave `amix` normalising
  - shift the graph by one frame
  - read untagged as BT.709
- **One code path.** A special case in Go for one studio means the contract is
  wrong.
- **Stated as verified?** Is anything machine-bound stated as verified?

---

## M8b — the editor

Built on a reviewed M6b, M7b and M8a. Its file list and DoD are confirmed at
M8b's kickoff against what those reviews changed.

**Built out of order, 2026-09-16, at the human's direction.** It is the fifth
milestone to be, and none of the three it rests on has been reviewed. The
preview was decided at this kickoff by the implementer rather than put to the
human: it plays each clip's own bytes in the browser, with no ffmpeg. That
decision, and the rest of the kickoff's, are in `docs/decisions.md` for
sign-off. No review ran to confirm the file list against. The work landed in
`packages/helm-ui-sdk/**` and `test/visual/**`, plus the fixture studio,
04 §5's table and `.gitignore`, each named in the report. The DoD below stands
as drafted, and all of it holds except the machine-bound demo, which did not
run. The report is `docs/agents/reports/08b-timeline-editor.md`.

### Tasks

- **`helm-timeline`** in `packages/helm-ui-sdk/`, against a structural client
  interface (M6 Q12):
  - tracks, and clips labelled with their studio's name and hue, or "another
    studio" in a neutral hue (Q11)
  - drag and trim, snapping to frames (Q7)
  - the dissolve and gain (Q8)
  - undo through revisions (Q10)
  - the plan's chip, and export with progress and cancel (Q13, Q17)
- **Preview,** decided at M8b's kickoff. 04 §5's "gapless preview across cuts"
  needs either Media Source Extensions over remuxed fragments or a rendered
  preview, and both need ffmpeg on the daemon's side.
- **A fixture studio page** holding `timeline` and `gallery.read_all`, for the
  goldens and the demo (Q20).
- **Goldens** in both themes at 1280, 1000 and 380 px.
- **Not in M8b:** the launcher's Timeline screen, which moves to M9 with the
  Gallery (Q20); waveforms, filmstrips and proxies (Q6).

### Definition of Done (draft)

- **The dependency arrow** holds for `helm-timeline` as M6b's DoD states it.
- **The component** as above, with goldens in both themes.
- **`make gate` is green.**
- **Machine-bound.** On the Mac with the real daemon, Q21's conform sequence
  edited and exported in `helm-timeline` inside the fixture studio.
