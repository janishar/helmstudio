# M8a — Timeline and export: the document, the API and the export pipeline — implementation report

## What was built

**M8a: the timeline document, the API and the export pipeline.** A sequence is
now a document the daemon owns, with rules it enforces and a grid it snaps to;
an export is a Job that renders it, copying the picture when that is genuinely
legal and re-encoding when it is not, and becoming an asset only once the file
has been read back and found to be the target it declared.

**M8b — the editor — is not built.** It is `helm-timeline` in
`packages/helm-ui-sdk/`, and that package does not exist: M6b has not been
built. Nothing in M8a waits on it.

**This was built out of order, at the human's instruction.** M8's own brief puts
M8a after M7b's review, and M7a, M6b and M7b are still unbuilt. The costs are
listed under "Judgement calls".

## Gate

    $ go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since 641ca5b
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	1.129s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	1.931s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	0.798s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.104s
    ok  	github.com/janishar/helmstudio/internal/export	0.112s
    ok  	github.com/janishar/helmstudio/internal/install	7.812s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.109s
    ok  	github.com/janishar/helmstudio/internal/media	0.107s
    ok  	github.com/janishar/helmstudio/internal/platform	0.175s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	0.420s
    ok  	github.com/janishar/helmstudio/internal/supervisor	40.052s
    ok  	github.com/janishar/helmstudio/internal/theme	0.197s
    ok  	github.com/janishar/helmstudio/internal/themelint	0.084s
    ok  	github.com/janishar/helmstudio/internal/timeline	0.091s
    ok  	github.com/janishar/helmstudio/internal/weights	0.521s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-css	0.092s
    ?   	github.com/janishar/helmstudio/packages/helm-css/cmd/build	[no test files]
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/node	[no test files]
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ok  	github.com/janishar/helmstudio/test/media	2.594s
    ?   	github.com/janishar/helmstudio/test/studios/sequencer	[no test files]
    ?   	github.com/janishar/helmstudio/web	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.258s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	3.709s
    ok  	github.com/janishar/helmstudio/test/visual	11.938s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

Run on macOS 27 on Apple Silicon, Go 1.27.1, Chrome 152, Homebrew ffmpeg 9.0.1.
`internal/timeline`, `internal/export` and `test/media` are new in this run;
`test/media` is the export's goldens, and it fails rather than skips when no
ffmpeg is there, unless `HELM_ALLOW_MISSING_FFMPEG` is set.

## What the tests hold

The pieces a reviewer should try to break, and what each one proves:

- **The legality predicate** (`internal/timeline/plan_test.go`). A table starts
  from two clips that copy and changes one probed fact at a time — container,
  codec, profile, level, size, aspect ratio, field order, pixel format, time
  base, frame rate, each of the four colour tags, parameter sets — and each
  alone turns the plan to conform, naming that clip. The document's own shape
  does it too: a trim, a still, a dissolve. A lying adopt hint cannot buy a
  copy, because nothing in the predicate reads one.
- **The copy really is bit-identical** (`test/media`). The export's decoded
  frames are compared with its sources' decoded frames, one md5 per frame.
- **The sound is not copied, and the check bites.** Each clip's tone has to land
  where that clip starts. The same test runs the obvious implementation — a
  plain `-c copy` concat — and measures it putting the second clip's sound 2,306
  samples (48 ms) late on the gate's fixtures, which is what the design's own
  words would have produced. The kickoff measured the same fault at 2,336
  samples (81 ms) on two real h3 takes; the fixtures are shorter, so the number
  is smaller and the fault is the same.
- **The conform graph is compared with a golden of what it produced**, before
  any encoder saw it, pinned to the ffmpeg major and architecture. A planted
  one-frame shift fails it.
- **A clip with no sound gets silence for exactly its span**, and the clips after
  it still land where their pictures do. Planting a graph that closes the gap
  pulls the next clip 47,998 samples early and fails it.
- **The encoded file is probed, never hashed**: size, rate, pixel format, colour
  tags, sample rate, channels and duration.
- **Access** (`internal/api/studioapi/timeline_test.go`, `test/conformance`).
  Another studio cannot read, edit, delete or export a sequence, and is told
  what it would be told about one that does not exist; a clip naming an asset
  the caller cannot read is refused in the same words whether or not it exists;
  `gallery.read_all` reaches everything.
- **Undo and concurrency.** Every write keeps the document it replaced, a stale
  `If-Match` is refused, and undo writes an old revision forward.
- **Reclaim.** An asset is held by a live sequence, and not by a deleted one or
  by an old revision.
- **The sweep.** An export left running is recorded interrupted and what it was
  writing is removed.
- **The licensing line holds.** Every argument list the daemon builds is checked
  against the allowlist, and the check refuses `libx264`, `libx265` and
  `libfdk_aac`.

## Design contradictions raised

The eight the kickoff raised are in the table below; the human's answers settled
every one, and this milestone implements those answers. Nothing new was found in
the design while building. Two defects were found in helmstudio's own code, both
by tests rather than by reading, and both fixed: the router preferred a bare id
route over an action route, and M6's same-origin proxies did not forward the
timeline (see "Judgement calls").

| # | One side | The other side | See |
|---|---|---|---|
| 1 | 03 §11's mockup: the chip "stream copy · no re-encode" on a sequence holding a 2.2 s still, from three studios, at 1920×1080 | R47: stream copy only "When all clips already match and there are no transitions or gain changes" | Q13 |
| 2 | 05 §6: a concat stream copy is "seconds, no re-encode, bit-identical picture" | Measured: bit-identical picture, and 2,336 extra samples of sound | Q13 |
| 3 | Build plan, phase 7: "A 14-second sequence from three studios; stream-copy verified bit-identical" | Those two cannot be one sequence | Q21 |
| 4 | 05 §6: "undo is the previous revision" | 02 §5 stored one document and an integer | Q10 |
| 5 | 04 §5: "opened in the launcher or inside any studio" | M4 Q9's asset read rule; #9's hidden origin | Q11 |
| 6 | M6 Q2: "Timeline (§11) is M8's" | M6 Q11: no launcher gallery until M9's cookie | Q20 |
| 7 | 05 §7: a preset named "h264-1080p24" | The target already states size and rate | Q9 |
| 8 | 05 §7: "poll /jobs/{id}"; R46's "cancellation" | `/jobs` needs `jobs`, which h3 and ltx do not declare | Q17 |

## Judgement calls

Where the design did not decide, and I did. The calls made at the kickoff — the
four corrections, the details added while recording, and what the brief says
beyond the draft — follow these, unchanged, and the human signed them off.

**Building out of order.** The human directed M8a to be built before M7a, M6b
and M7b. What that costs, so a reviewer can see it:
- **The schema version.** M8 took v6, which M7a's brief had. Whichever lands
  second renumbers.
- **No approval.** M7a's approval digest does not exist, so nothing here sends
  one; the tests that launch studios are untouched.
- **The editor.** M8b cannot start until `packages/helm-ui-sdk/` exists.
- **Rework.** M7a changes `/studios`, `:install` and `:launch`, not the timeline,
  so the overlap is the API document and the generator's output.

**The pipeline's shape.** Three packages: `internal/timeline` decides (rules,
snapping, legality, the ffmpeg argument list — all pure), `internal/export`
carries out (finds ffmpeg, probes, runs one render in its own process group),
and `internal/api/studioapi` enforces, where every other operation already is.
Everything but the running is then checkable without a model or a GPU.

**The copy path is one ffmpeg invocation**, not two and a join: the concat
demuxer carries the picture with `-c:v copy` while the same clips, opened again,
carry their sound through the graph. One process, one output, no intermediate
file, and the sound is conformed exactly as it is on the slow path.

**The graph's own choices**, each found by running it rather than by reading:
- `settb=AVTB` ends every video chain, or a dissolve is refused as soon as one
  of its sides has been through a concat.
- `setparams` tags the frames with the colour they were converted to;
  videotoolbox writes its own VUI and drops primaries and transfer, so asking
  the encoder is not enough. This is the one I would most like a second pair of
  eyes on: it is right on this machine and I cannot say it is right on another.
- A clip's sound is placed by prefixing exactly its position in silence and
  concatenating, not with `adelay`, which names whole milliseconds while a frame
  at 24 fps is 41.666 ms.
- Each clip's sound is padded and then cut to its picture's length, because a
  take's sound rarely ends with its picture.
- A dissolve's sound is a fade out and a fade in, summed — which is a crossfade
  — because every clip is already a separate input of one `amix`.

**Two defects in helmstudio's own code, found by tests:**
- **The router preferred the bare id route.** `/timeline/{id}` matched
  `<id>:plan` before `/timeline/{id}:plan` was tried, so a `GET` action was read
  as an id ending in `:plan`. Every earlier action is a `POST` on a path whose
  bare route has no `POST`, which is why this never showed. The router now tries
  the more specific route first, and a regression test holds it.
- **The proxies did not forward the timeline.** M6 Q10's allowlist is by first
  path segment, so a studio's page could not have reached any of these
  operations. All three runtime SDKs now list `timeline` and `timeline:append`.

**Smaller calls:** a video's poster is taken a tenth of the way in, so a clip
that fades up from black does not give a black poster; bytes ffmpeg cannot read
are refused as `unsupported`, the answer an undecodable image already gets; the
minimum ffmpeg is major 6; an export writes to `<assets>/tmp/export-<job>/`,
which shares the blob store's volume so adoption is a link; one export at a time
per sequence.

**Corrections to answers.** In four places, two recommendations contradicted
each other once read as decisions. Each is also marked in `docs/decisions.md`.
- **Q5, what answers 501 without ffmpeg.** The kickoff said "export and video
  probing answer 501". Probing is not an operation, since Q6 leaves adopt and
  upload unchanged. The answer now names the three operations that need ffmpeg
  (`:plan`, `:export` and a video `/thumb`), and says creating and editing a
  timeline never need it, as R45 requires.
- **Q7, what `in` and `out` snap to.** The kickoff snapped them to a whole
  *source* frame or sample. That needs the source's rates whenever a timeline is
  written, but Q6 probes only at export, and Q5 keeps writes working without
  ffmpeg.
  - They now snap to the target's frames or samples, which is identical for a
    copy, whose rate equals the target's.
  - Checks that need a clip's length use the asset's recorded `duration_s` when
    the timeline is written, and are made again against probed facts at export.
  - A clip whose asset records no duration must give `out`.
- **Q9 against Q13, colour on the copy path.** Q9 said "Every export is
  converted to, and tagged as, BT.709". Q13's copy path cannot convert pixels,
  and tagging BT.601 pixels as BT.709 would be false. Conversion and tags now
  apply to conform exports, and a copy keeps its sources' tags, untagged
  included.
- **Q10 against Q19, `If-Match`.** Q10 required it "on every write". Q19's
  `:append` may name no timeline, and then has no revision to match. It is now
  required on `PATCH` and `:revert`, checked on `DELETE` when sent, and not
  taken by `:append`, which applies to the current revision.

**Added while recording.** Each fills a gap an answer left, without changing
it, and is marked in the log.
- **Q4's allowlist** also passes stream copy, and `rawvideo` and `pcm_s16le` for
  posters and probes. With only the two export encoders on it, the test Q4 asks
  for would fail on Q4's own copy path.
- **Q8:** clips sent without `at`, as in 05 §7's create request, are laid end to
  end from 0 on their track.
- **Q9:** mono sound goes equally into both output channels.
- **Q15:** "exactly the timeline's sample count" is `round(duration ×
  sample_rate)`, since 29.97 fps at 48 kHz is not a whole number of samples per
  frame.
- **Q17:** `Item` gains `timeline_id` on the wire, which the gallery's
  "timeline" label needs. The launcher's `/launcher/jobs/{id}:cancel` reaches
  the export runner, as R46's "reusing install's … cancellation" implies.
- **Q19:** "the caller's most recently created or edited timeline" is the most
  recently updated timeline the caller owns, since ownership (Q11) is what the
  daemon records.
- **Defaults:**
  - a video poster is one `rawvideo` frame through M4's thumbnailer
  - an export needs at least one V1 clip
  - tests that encode fail on a host without `h264_videotoolbox` unless
    `HELM_ALLOW_MISSING_FFMPEG` is set

**Where the brief says more than the draft did:**
- **The review focus** adds "extradata, trims and sound" to the milestone's own
  legality question, from the kickoff's measurements. It also checks that a
  copy's colour tags are left as they were (the Q9 correction).
- **The DoD** tests the corrections and additions:
  - a clip with no recorded duration and no `out` is refused
  - `:append` needs no `If-Match`
  - a copy keeps its tags
  - an old revision does not hold footage against reclaim
  - creating and editing work without ffmpeg
  - revert and append obey the access rule
- **The machine-bound list** adds the graph's sign-off (Q3), and a daemon
  killed mid-export.
- **M8a's opening** says that a finding in M7a's or M7b's review that changes a
  convention this brief copies wins, and is raised at M8a's start.

## Could not verify

Everything below needs the Mac, real media and a person looking. The gate proves
the arithmetic and the argument lists; it cannot prove that a file looks right.

- **The two machine-bound demos** (Q21): a run of h3 takes through the fast path,
  and the four-clip sequence through conform. Neither was run.
- **videotoolbox's own output.** Every encode in the gate goes through it on this
  machine, and the tests check the file's shape, never its quality or its
  bitrate. Settings deserve a look on the Mac.
- **Playback in QuickTime, Safari and Chrome.** The exports were decoded by
  ffmpeg only. The priming measurement does not depend on the player, but how
  each one treats an untagged copy does.
- **Colour by eye.** An untagged source is read as BT.601 and a conform export is
  tagged BT.709; that is right for how h3 writes its takes (measured), and it
  was not looked at on a display.
- **A real `kill -9` mid-export.** The sweep is tested against a row that stands
  for a render left behind, not against a daemon killed while ffmpeg ran. The
  identity rule it uses is M3's, unchanged.
- **A Linux build.** `make vet-linux` type-checks the tree; no Linux machine has
  run an export, and a Linux build has no videotoolbox at all (open entry).

## Dependencies added

None. `go.mod` is unchanged; ffprobe's JSON is read with `encoding/json`, and
ffmpeg is spawned through the existing platform seams.

## Files touched outside the milestone's list

- **`packages/helm-runtime-sdk/go/proxy.go`,
  `packages/helm-runtime-sdk/python/helm_runtime_sdk/proxy.py` and
  `packages/helm-runtime-sdk/node/src/proxy.js`.** M8a's file list allows the
  embedded module and generated files, not these. The conformance suite's proxy
  case requires every studio-api path to reach the daemon through the proxy, and
  the timeline's did not: a studio's page could not have used any of it. Three
  lines, one per SDK.
- **`docs/plan/01-build-plan.md`, `02-milestones.md`, `03-delegation.md`,
  `docs/agents/milestones/09-mac-app-and-release.md`, and this milestone's own
  brief.** The list names the design documents, the decision log and the report,
  not the plan or another milestone's brief. These are the kickoff's work, not
  the implementation's: each carries a note where an answer changed what that
  document said — Q21's two demos, Q2's two briefs, Q20's move of the Timeline
  screen to M9, Q3's split of the graph from its harness, Q4 closing the
  licensing question — and the brief itself is task 0's expansion. Nothing in
  them is code.
- Everything else is inside the list: `internal/timeline/**` and
  `internal/export/**` (new), `internal/media/**`, `internal/api/**`,
  `internal/store/**`, `api/openapi.yaml` with the generator's output,
  `cmd/**`, `test/conformance/**`, `test/media/**`, `test/studios/sequencer/**`,
  the `Makefile`, `docs/agents/gate.md`, the five design documents task 0 names,
  the decision log and this report. `internal/platform/**` was not touched: the
  spawn and identity seams M3 built needed nothing added.

## Decision-log entries appended

Under "2026-09-16 · M8 timeline and export", after the kickoff answers:
- the human's sign-off on the four corrections, and Q21's fixture studio;
- eight entries for the decisions made while building, under a line naming them
  judgement calls for the reviewer to confirm or overturn: the three packages,
  the copy path's single invocation, the graph's five choices, the router's
  preference, the proxies, video posters, the minimum ffmpeg major, and schema
  v6;
- one entry naming the four places where tests written before M8 were edited,
  and what each still holds.

## Left undone

- **M8b, the editor.** It needs `packages/helm-ui-sdk/`, which M6b has not
  built.
- **The launcher's Timeline screen**, which moves to M9 with the Gallery (Q20).
- **Waveforms, filmstrip sprites and h264 proxies** (Q6), which belong to no
  milestone; `helm-player` keeps those parts `Unsupported`.
- **The machine-bound demos** (Q21), which need the Mac.
- **Committing.** Per `implementer.md`, nothing is committed.

---

## The kickoff

The human answered "recommendations for all" on 2026-09-16. The questions follow
as they were asked. The corrections made while recording the answers are under
"Judgement calls".

### What I measured

The brief says M8's shape "depends on how the media engine behaves in
practice". These are the facts the questions below cite.

**h3's takes.** 30 files across five sessions, probed with `ffprobe`:
- **Picture.** H.264 High, `yuv420p`, 24/1 fps, time base 1/12288, progressive,
  B-frames, no sample aspect ratio.
- **Colour.** Range, matrix, primaries and transfer are all unspecified.
- **Sizes and levels.** Five sizes: 256×448 and 512×320 at level 2.1; 384×672
  and 800×448 at 3.0; 768×1344 at 3.2.
- **Parameter sets.** The SPS/PPS extradata is byte-identical among takes of
  one size, even across sessions and lengths (124, 158 and 243 frames). It
  differs between sizes.
- **Sound.** AAC-LC, **32 kHz**, stereo.
- **How they are made.** h3's engine pipes `rgb24` frames into
  `ffmpeg … -c:v libx264 -crf 18 -pix_fmt yuv420p` (`h3c/h3_ffmpeg.c:535`,
  `:675`).
- **h3's own timeline.** `CombineTimeline` re-encodes with `libx264 -crf 20`
  and resamples sound to 48 kHz (`server/timeline.go:151`, `:158`).

**ltx's clips.** Five files: H.264 High, 704×448, 24/1, level 3.0, colour
unspecified, AAC **48 kHz** stereo, identical extradata among themselves.

**AuK.** It writes each generation as `.wav` at the model's sample rate
(`web/server.py:526–527`). No generated output was on disk to probe.

**A stream copy of two real h3 takes.** Two 256×448 takes (124 and 158 frames,
identical extradata), joined with the concat demuxer and `-c copy`:
- **Picture: bit-identical.** 282 frames, whose decoded `framemd5` equals the
  first take's frames followed by the second's.
- **Sound: not.** The sources decode to 165,600 and 210,400 samples. The export
  decodes to 378,336, which is **2,336 samples (73 ms) more**.
  - The first take reports `initial_padding=1024`; the export reports 0. Its
    1,024 priming samples now play at the start.
  - The first take's 288 trailing padding samples, and the second take's 1,024
    priming samples, play at the cut.
  - So the second take's sound starts **81 ms after its picture**: 32 ms of
    untrimmed priming, 8 ms by which the first take's sound outlasts its
    picture, 9 ms of padding, and 32 ms of priming. Each further cut adds its
    own padding and priming.

**The colour matrix h3's path writes.** A pure red `rgb24` frame through h3's
exact encode arguments decodes to Y, U, V = 81, 90, 240. That is **BT.601**
limited range (BT.709 would be 63, 102, 240), and the file is tagged with no
matrix.

**This Mac's ffmpeg.** Homebrew's 9.0.1, configured `--enable-gpl
--enable-version3 --enable-libx264 --enable-libx265 --enable-videotoolbox
--enable-audiotoolbox`, so it is GPLv3 (`ffmpeg -L`). It has `libx264`,
`h264_videotoolbox`, FFmpeg's own `aac`, and `aac_at`.

---

### A · The brief itself

#### Q1. Who writes M8's contract

This is the same question as M5, M6 and M7 Q1. The recorded answer is "M5's
contract is the expansion drafted at kickoff and approved by the human".

**Recommendation:** the same.
- The proposed expansion at the end is a draft.
- The human edits it into the milestone document.
- Implementation starts after that.

#### Q2. The brief's preconditions, and where M8 goes in the order

- **M8's brief:** it is expanded "when the two things a good brief needs both
  exist: the contract it builds against, and the previous milestone's review
  findings".
- **M7 Q2's recorded order:** M6a's review, then M7a, then M6b, then M7b.
- **The decision log, M7:** "Nothing is built yet: M7a starts after M6a's
  review."
- **The tree:** no `internal/library`, no `packages/helm-ui-sdk/`, and `web/`
  is the plain shelf. The newest commits are M7's kickoff documents.

What M8 needs from those milestones:
- **From M6b:** the `helm-ui-sdk` package, its component conventions and the
  structural client interfaces (M6 Q12) that `helm-timeline` is built against;
  `helm-player`'s transport for preview; the launcher shell whose nav lists
  Timeline (03 §6).
- **From M7a:** schema v6, which fixes M8's migration number; the approval
  parameter on `:launch`, which every test that launches a studio will send;
  and the launcher API conventions M7a copies from M6a.
- **Review findings:** none exist for M6a, M7a, M6b or M7b.

Most of M8 is not a screen, though:
- the timeline document and its API
- probing, the legality check and the conform graph
- export as a job, with cancellation and the startup sweep
- the golden tests

**Recommendation:**
1. **Split M8 the way M6 and M7 were split**, into two briefs reviewed
   separately.
   - **M8a, the document, the API and export.** Headless: everything in the
     list above, with the conformance suite.
   - **M8b, the editor.** `helm-timeline`, plus whatever launcher screen Q20
     allows. Its file list and DoD are confirmed at its own kickoff.
2. **Order:** M6a's review → M7a → M6b → M7b → M8a → M8b.
   - M8a needs M7a's schema and conventions but no screen. If you want to run
     it beside M6b instead, it can start after M7a's review; the two share
     `api/openapi.yaml` and the generator, so they would need separate working
     trees and a careful merge.
   - M8b needs a reviewed M6b, M7b and M8a.
3. **Answer the questions now anyway.** Q4 also gates M6b's player (M6 Q13) and
   M9's bundle, and the answers change nothing already built.

This reorders the plan, so it is the human's call.

#### Q3. What is delegated in this milestone

- **03-delegation, phase 7:** "**least delegable**; ffmpeg work is empirical and
  machine-bound, so the golden harness is delegated and the filter graph is
  not."
- **03-delegation, "Never delegated":** "Schema and API shape · security
  posture — tokens, capabilities, Host and Origin checks … · licensing,
  including any new dependency".
- **CLAUDE.md:** "ffmpeg on videotoolbox" is among what is "checked by hand on
  an Apple Silicon Mac".
- **M8's brief:** "Expand it with the stronger model".

The plan keeps the graph with the human because ffmpeg's behaviour has to be
found by trying it. But most of that trying can now be a test: the golden
harness can hash the graph's decoded output, frame by frame, before any
encoder touches it (Q15). What cannot be a test is the encoder under
videotoolbox, playback in real players, and colour judged by eye.

**Options:**
- **(a) As planned.** The human writes the conform graph, and the legality
  rules it depends on. The implementer builds everything around them: the
  document, the API, probing, jobs, the sweep and the harness.
- **(b) Drafted under the harness.** The implementer writes the graph builder
  as a pure function, from the document and the probed facts to an ffmpeg
  argument list, with a golden for every filter choice. Each choice is listed
  as a judgement call. The human signs the graph off on the Mac: encoder
  settings, three players, colour.
- **(c) Fully delegated.**

**Recommendation: (b)**, if you are willing to amend the plan's line. Otherwise
(a), with the graph as the one M8a task left to you. Not (c): the plan's
reason still holds for everything a test cannot see.

---

### B · ffmpeg

#### Q4. ffmpeg licensing (Open: "ffmpeg licensing … Gates M8")

- **01 §15:** "LGPL build with videotoolbox, or a GPL build with x264". Lean:
  "LGPL plus videotoolbox — also the fast encoder on Apple Silicon, and it
  avoids a GPL question in a signed MIT app."
- **05 §6:** "linked against x264 it is GPL, which is a real question for an MIT
  app shipping a signed `.dmg`. The clean answer is an LGPL build using
  videotoolbox for h264".
- **M8's brief:** "It gates this milestone and what can be bundled in a signed
  `.dmg`. It is the user's to close, not the implementer's."
- **M9's review focus:** "Does anything bundled in the `.dmg` conflict with the
  ffmpeg licensing decision closed in M8?"

**Facts:**
- This Mac's ffmpeg is a GPLv3 build with both x264 and videotoolbox (see "What
  I measured").
- h3 encodes every take with `libx264` through whatever ffmpeg the user
  installed. That is h3's own business: helmstudio neither ships nor runs it.

**What each choice means for M8** (engineering, not legal advice):
- **LGPL + videotoolbox.**
  - The export encoder is `h264_videotoolbox`; sound uses FFmpeg's own `aac`
    (or AudioToolbox's `aac_at`). helmstudio never names `libx264` or
    `libx265`.
  - Hardware encoding is fast on Apple Silicon.
  - Its output bytes can change with the OS release and the chip. No golden may
    hash an encoded file. Today's macOS 27 upgrade, which moved every visual
    golden while Chrome stayed at 152, is that class of change.
  - Linux has no videotoolbox, so a Linux build needs another encoder or
    refuses conform exports. A macOS virtual machine may not expose the
    encoder at all (not verified).
- **GPL + x264.**
  - Deterministic software encoding with fixed settings and threads.
  - The binary helmstudio bundles is GPL. That is the question 01 and 05 raise
    for the signed `.dmg`.
- **Either way,** the gate can test against Homebrew's GPL build. Tests are not
  distribution, provided helmstudio only uses encoders the chosen build has. A
  test can hold that line.

**Recommendation: the design's lean, LGPL + videotoolbox.**
- The export encoders are `h264_videotoolbox` and FFmpeg's native `aac`.
- A test fails when any ffmpeg argument list helmstudio builds names an encoder
  outside that allowlist.
- No golden hashes encoded output (Q15).
- The decision gets its entry, and closes the open one.

If the `.dmg`'s licensing matters beyond this, it deserves a check by someone
qualified; this question only records the engineering consequences.

#### Q5. Where ffmpeg comes from before M9 bundles it

- **R49:** "ffmpeg is bundled at a pinned version rather than expected on the
  machine, since 'install ffmpeg first' is the friction helmstudio exists to
  remove."
- **No app exists until M9.** M5 Q3 answered the same question for `uv`:
  "detect and instruct from M5, with a pinned binary in the app from M9
  (checked before `PATH` then)".
- **05 §7:** with no framework, "the sequence is still created and still
  exports, because export is the embedded media engine doing the same work
  headlessly". A standalone studio has nothing bundled.
- **The visual gate's precedent:** "A missing browser fails the gate unless
  `HELM_ALLOW_MISSING_BROWSER` is set", and goldens are "tied to the Chrome
  major".
- **h3** already requires `ffmpeg` in `requires.tools`, overridable with
  `H3_FFMPEG` and `H3_FFPROBE`.

**Recommendation:**
- **Detect and instruct in M8; bundle in M9**, as for `uv`.
  - `HELM_FFMPEG` and `HELM_FFPROBE` when set, else `PATH`. M9's bundled binary
    is checked first, from then on.
  - The daemon records the path, version and configuration it found.
  - It refuses a build older than a minimum major M8a records (the one its
    goldens are made with), or one missing an encoder Q4 allows.
- **Without a usable ffmpeg,** export and video probing answer 501
  `unsupported`, with `details: {code: tool_missing, tool: ffmpeg}` and a
  message naming `brew install ffmpeg` and `HELM_FFMPEG`. That is how `/thumb`
  already degrades (M4 Q13), and how components render an empty state (R56).
- **The embedded provider** uses the same lookup.
- **The gate** requires ffmpeg and ffprobe. A missing one fails unless
  `HELM_ALLOW_MISSING_FFMPEG` is set. Goldens are tied to the major in an
  `FFMPEG_MAJOR` file, as visual goldens are to `CHROME_MAJOR`.

#### Q6. How much of the ffmpeg-gated platform M8 takes on

- **R36:** "The daemon generates thumbnails, poster frames, waveforms and
  browser-playable proxies once per asset".
- **M4 Q13:** video and audio take `width`, `height`, `duration_s` and `fps` "as
  hints in the request"; `/thumb` answers 501 for them "until the ffmpeg
  decision".
- **M6 Q13:** `helm-player`'s filmstrip, waveform and h264 proxy render
  `Unsupported` "until the ffmpeg decision".
- **M8's Shape:** "The timeline document, the editor, the export pipeline, and
  golden tests."
- **The legality check** needs facts it can trust. A hint is the studio's word,
  and the probes above show what hints do not carry: level, extradata, colour
  tags, sample rate.

**Recommendation:**
- **In M8a:**
  - every clip is probed with `ffprobe` at export time, and nothing about
    legality or conform comes from a hint
  - `/thumb` for video: a poster frame, since the editor's clips and every
    studio's gallery need one
- **Not in M8:** waveforms, filmstrip sprites and h264 proxies. `helm-player`
  keeps its `Unsupported` parts, and a later milestone the human places takes
  them.
- **Adopt and upload are unchanged.** Hints stay hints. Replacing them with
  probed values would change M4's contract and what a studio sees, for no
  timeline need.

---

### C · The document

#### Q7. How time is written

- **R44:** "a target (resolution, fps, sample rate) and tracks of clips
  referencing assets with in and out points".
- **05 §6's document:** `"target": {"width": 1920, "height": 1080, "fps": 24,
  "sample_rate": 48000}`, with clips `{"in": 0, "out": 5.04, "at": 0}`,
  `{"hold": 2.2, "at": 9.10}` and `"transition_in": {"type": "dissolve",
  "duration": 0.25}`. Every time is seconds, as a JSON number.
- **`Asset.fps`** is a JSON number.

**Facts:**
- 5.04 s at 24 fps is 120.96 frames, which is not a frame. h3's takes are 124
  frames, 5.1667 s of picture and 5.175 s of sound.
- 23.976 and 29.97 fps are 24000/1001 and 30000/1001, which no JSON number
  writes exactly.
- Two editors that round an unaligned time differently write different
  documents, and a cut lands on different frames.

**Options:**
- **(a) Seconds, snapped by the daemon.**
  - Timeline positions (`at`, `hold`, a transition's `duration`) snap to the
    nearest whole frame at the target's rate.
  - `in` and `out` snap to a whole source frame for video, and a whole source
    sample for sound.
  - What is stored and returned is the snapped document, so writing it back
    changes nothing.
  - `fps` is one of a closed list, each an exact ratio: 23.976, 24, 25, 29.97,
    30, 48, 50, 59.94 and 60.
- **(b) Integers.** Positions in frames at the target rate, `in` and `out` in
  source frames or samples, and `fps` as `{num, den}`.
- **(c) Exact ratios everywhere,** as strings.

**Recommendation: (a).** It keeps 05's document and requests as written (`"in":
1.2, "out": 5.26`), and makes the rounding rule part of the contract instead of
each client's choice. Sound placed on the timeline is frame-aligned, which is
coarse for lip-sync nudges, and 01 §14's "Cuts, holds, one transition type,
audio gain" asks for nothing finer.

#### Q8. What tracks, clips, holds, the transition and gain mean

- **01 §14:** "Cuts, holds, one transition type, audio gain."
- **05 §6's example:**
  - one video track with `{asset_id, in, out, at}` clips
  - a still, `{asset_id, hold: 2.2, at: 9.10}`
  - `transition_in: {type: dissolve, duration: 0.25}` on the second clip, whose
    `at` (5.04) is exactly the first clip's end
  - an audio track with `gain_db: -3` on the track
- **03 §11's mockup:** tracks V1 video, A1 dialogue and A2 ambience.
- **05 §7:** `:append` sends `{"asset_id": …, "track": "V1"}`, but tracks in the
  document have a `kind` and no name.
- **h3's and ltx's clips carry their own sound** (see "What I measured").

**Not said anywhere:**
- how many video tracks there may be
- where a video clip's own sound goes
- whether V1 may have gaps, or clips on one track may overlap
- whether `hold` applies to a video (a freeze frame) or only to a still
- how a dissolve gets its frames, since the example's second clip starts
  exactly where the first ends
- whether gain is per track, per clip, or both
- what names `V1`

**Recommendation:**
- **One video track, V1,** contiguous from 0 with no gaps. The sequence's
  duration is V1's end. A second video track is compositing, which 01 §14's
  "no … effects" excludes.
- **Audio tracks A1…A8.** Tracks are named by kind and 1-based position among
  tracks of that kind, which is what `:append`'s `V1` implies. An audio clip may
  sit anywhere inside the sequence, and clips on one track never overlap.
- **A video clip's own sound plays under it**, at the clip's `gain_db`
  (default 0). `"audio": false` mutes it. There is no hidden linked track.
- **`hold` is for images only:** an image clip is a still for that long, with
  no `in` or `out`. A freeze frame of video is not in v1.
- **The dissolve** is `transition_in` on any V1 clip but the first.
  - It is centred on the cut and needs half its duration of handle beyond the
    outgoing clip's `out` and before the incoming clip's `in`. It is refused,
    naming the missing frames, when a handle is short. A still has unlimited
    handle.
  - `at` values do not move when a transition is added. That matches 05's
    example, whose second clip starts at the first's end with 1.2 s of handle.
  - The two clips' own sounds crossfade over the same span.
- **Gain** is `gain_db` on an audio track and on any clip, from −60 to +12 dB,
  with no keyframes.
- **These rules go into 05 §6** and into the API's schema, so the validator and
  the editor enforce the same ones.

#### Q9. The target, the preset, and colour

- **R44:** the target is "(resolution, fps, sample rate)". **05 §6:** `{width,
  height, fps, sample_rate}`. **05 §7's create request** leaves out
  `sample_rate`.
- **05 §7's export request:** `{ "preset": "h264-1080p24" }`, which names a size
  and a rate again.
- **05 §6:** clips "differ in resolution, frame rate, pixel aspect, colour range
  and sample rate", and conform sets an "explicit colour range".
- **M8's review focus:** legality covers "pixel format, colour metadata".

**Facts:**
- Every h3 and ltx file is tagged with no colour range, matrix, primaries or
  transfer, and no sample aspect ratio.
- h3's encode path wrote BT.601 values into those untagged files (see "What I
  measured").
- HD H.264 is conventionally BT.709. A conform that reads an untagged clip as
  BT.709 would shift h3's colour silently, which is the "subtly broken" class
  05 §6 warns about.

**Not said:** the output's codec, pixel format, colour tags or channel layout;
what "matches the target" means for a codec when the target names none; what
wins when the preset and the target disagree.

**Recommendation:**
- **The target is geometry and rates only:** `width` and `height` (even),
  `fps` (Q7's list), and `sample_rate` of 44100 or 48000, defaulting to 48000.
  Output is always stereo.
- **A preset names encoding only, never size or rate.** M8 has one, `h264`:
  `yuv420p`, High profile, MP4 with fast start, sound as AAC-LC at 192 kb/s.
  05 §7's `h264-1080p24` becomes `h264`.
- **Every export is converted to, and tagged as, BT.709, limited range,
  progressive.** An untagged source is read as BT.601: that is what h3's
  pipeline writes (measured) and what FFmpeg's scaler uses by default when it
  converts RGB. ltx writes video through its `ltx_core_mlx` dependency, which I
  did not measure. A tagged source is read as tagged.
- **The colour rule is checked by eye on the Mac** as part of Q21's demo,
  because a golden proves the arithmetic, not the choice.

#### Q10. Undo and revisions

- **05 §6:** "Every edit is a patch, so undo is the previous revision".
- **02 §5:** `revision INTEGER NOT NULL DEFAULT 1, -- optimistic concurrency +
  undo`, on a table holding one document. Nothing stores an earlier one.
- **M4 first review #11:** "PATCH always means an RFC 7396 JSON merge patch …
  an array or scalar replaces", so any patch touching `tracks` resends all of
  them.
- **Every other document** uses `ETag` and `If-Match` (kv, records, sessions).
- **02 §8's reclaim** reads only the current `tracks`.

**Options:**
- **(a) The daemon keeps revisions.** Each write stores the document it
  replaces. Undo is `POST /timeline/{id}:revert {revision}`, which writes the
  old document as a new revision.
- **(b) Undo lives in the editor.** The page keeps earlier documents and writes
  one back. The daemon stores only the current revision.

**Recommendation: (a),** because 05 says undo is a revision, and a
sequence edited in a studio and then in the launcher has no single page that
holds its history.
- **Concurrency:** the `ETag` is the revision; `If-Match` is required on every
  write, and a stale one is 409 `etag_mismatch`.
- **Retention:** the newest 100 revisions per timeline.
- **Reclaim** counts only the current document, as 02 §8 says. Otherwise
  deleting a clip keeps its footage for 100 edits.
- **So a revert can name an asset reclaimed since.** It is refused, naming the
  clips, rather than restoring a sequence that cannot export.

#### Q11. Who may read, edit and export a timeline

This is security posture, which the plan never delegates.

- **04 §5:** "the same sequence can be opened in the launcher or inside any
  studio and stay one thing". **04 §7:** "a sequence opened in the launcher and
  one opened inside a studio are the same sequence, mid-edit". **05 §6:** it is
  "nobody's studio document".
- **02 §5:** `timelines` has no owner column.
- **M4 Q9, as built** (`internal/api/studioapi/assets.go`): a studio reads an
  asset only when it has a `studio_assets` row for it, its own items or item
  inputs name it, an item in its inbox names it, or it holds
  `gallery.read_all`. Anything else is 404.
- **M4 first review #9:** `origin_studio` is null "for any caller that is not the
  origin studio".
- **04 §5, 03 §11 and 03 §17:** clips are "coloured by the studio that produced
  them", and "a timeline clip carries its studio's name as well as its hue".
- **M7 Q12's sentence for `timeline`:** "Can create sequences on your timeline."

**The conflict.**
- If timelines are shared and any `timeline` holder may write any clip, a
  studio can put another studio's asset id into a sequence, export it, and read
  the export: bytes Q9 refuses it. Shared documents also hand every clip id to
  every studio holding `timeline`.
- If timelines are private, "inside any studio" is false.
- Either way, the hue and name need an origin that #9 hides.

**Options:**
- **(a) Shared, with clips checked.** Any `timeline` holder reads and edits
  every timeline. A write may add only assets the caller can read, and export
  needs every clip readable. Cost: every `timeline` holder sees what every
  studio made and when, which is what `gallery.read_all` exists to ask for.
- **(b) Owned.** A timeline records the studio that created it (null for the
  launcher).
  - A studio reads, edits and exports its own timelines, and every timeline
    when it also holds `gallery.read_all`, which already means "read
    everything you have ever made".
  - A write may add only assets the caller can read under Q9, and export
    re-checks every clip.
  - The launcher (M9) reads and edits all of them.

**Recommendation: (b).**
- **04 §5's "inside any studio"** becomes "inside the studio that made it, or
  any studio that can read everything". 04 §7's point, one document through
  one set of endpoints, stands.
- **A clip carries a `studio_id`** when the caller could already learn it from
  an item it may read: its own, its inbox's, or any item with
  `gallery.read_all`. Otherwise it is null, and the editor shows "another
  studio" in a neutral hue, with the words as well as the colour (03 §17).
- **An unreadable asset in a write** is 422 naming the clip's position, never
  whether the asset exists.
- **`timelines` gains `studio_id`** (schema change, 02 first). M7 Q12's sentence
  stays.

#### Q12. Deleting a timeline

- **02 §5:** `items.timeline_id TEXT REFERENCES timelines(id)`, with no
  `ON DELETE` action. Under `foreign_keys(ON)`, a timeline that has been
  exported cannot be deleted.
- **02 §5:** `timelines` has no `deleted_at`.
- **02 §8's reclaim** reads the tracks of every timeline.
- **Nothing in the design deletes a timeline.**

**Recommendation:**
- **Soft delete,** like items and sessions: `DELETE /timeline/{id}` sets
  `deleted_at`.
- **A deleted timeline stops holding its footage.** 02 §8's query gains
  `t.deleted_at IS NULL`.
- **Its exports keep `timeline_id`,** so lineage survives.
- **No restore in M8,** so a deleted sequence never comes back pointing at
  reclaimed assets.

---

### D · Export

#### Q13. When the fast path is legal, and what it does with sound

- **05 §6:** "When every clip already matches the target and there are no
  transitions or gain changes, export is a concat demuxer **stream copy** —
  seconds, no re-encode, bit-identical picture".
- **R47:** "export is a concat stream copy with no re-encode, and the UI says
  so". **03 §11:** the chip "stream copy · no re-encode".
- **M8's review focus:** "Codec, profile, level, timebase, pixel format, colour
  metadata. A fast path taken illegally produces a file that plays on the
  developer's machine and nowhere else."
- **03 §11's mockup** shows that chip on a 1920×1080 sequence of h3 takes, an
  ltx clip and a 2.2 s iris still.

**Facts** (see "What I measured"):
- **Picture:** the copy of two same-size h3 takes is bit-identical.
- **Sound:** it is not. The second take's sound starts 81 ms after its picture,
  and each cut adds more. A lip-sync error that grows along the sequence is
  exactly "a file that plays on the developer's machine".
- **Extradata** matches only among takes of one size. Two takes can agree on
  codec, profile, level, pixel format and rate and still differ here.
- **Trims:** `in` and `out` with a stream copy cut at packets, not frames. A
  copied clip is frame-accurate only when it is used whole.
- **Stills** cannot be copied, so the mockup's chip contradicts R47.

**Recommendation:**
- **The fast path copies picture only.** Sound always goes through the conform
  graph: each clip's sound trimmed to its picture, priming dropped, resampled,
  joined and encoded. Encoding sound alone is cheap next to picture.
- **R47, 05 §6 and 03 §11 are amended.** The chip reads "video stream copy",
  and the conform chip names the reasons.
- **Copy is legal only when** every V1 clip, probed at export time, has:
  - codec H.264, in an MP4 or MOV
  - the same profile and level
  - width and height equal to the target's
  - sample aspect ratio unset or 1:1, and progressive field order
  - the same pixel format and time base
  - a frame rate exactly equal to the target's
  - the same four colour tags, untagged counting as a value
  - byte-identical extradata
  - no trim (`in` at 0, `out` at its end), no hold, no image, no transition
- **Sound tracks and gain do not affect it,** since sound is always re-encoded.
- **Anything else conforms.**
- **The plan is visible before export.** An operation returns `{mode:
  "copy" | "conform", reasons}`, so the chip is the daemon's answer rather than
  the editor's guess.

#### Q14. The conform graph's choices

- **05 §6:** "one filter graph: scale with pad, `fps`, `setsar=1`, explicit
  colour range, `aresample` with `async`, then `concat` — one pass, no
  intermediate files."
- **h3's `CombineTimeline`** letterboxes onto the largest clip, uses `fps=24`,
  generates `anullsrc` silence for a clip without sound, and resamples to
  48 kHz stereo.

**Not said:**
- letterbox or crop when aspects differ; h3 has portrait takes (384×672,
  768×1344) and landscape ones
- the fill colour
- how frame rates convert
- how a still becomes frames
- that a clip's sound and picture differ in length (8 ms in the take above)
- how audio tracks mix; FFmpeg's `amix` divides by its input count unless told
  not to, which quietly lowers every voice line
- which filters carry the dissolve

**Recommendation** (under Q3's answer):
- **Fit, never crop:** letterbox or pillarbox in black.
- **Frame rate:** FFmpeg's `fps` filter, which drops or repeats frames. No
  blending.
- **Stills** loop for their `hold`.
- **Each clip's sound** is trimmed or padded to its picture's length. A clip
  without sound gets silence.
- **Mixing:** `amix` with normalisation off, gain as `volume`.
- **Dissolve:** `xfade` for picture, `acrossfade` for sound.
- **Colour:** Q9's rule, as explicit scaler matrices and output tags.
- **Every choice has a golden** (Q15), and each is listed in M8a's report for
  sign-off on the Mac.

#### Q15. What the golden tests compare

- **M8's review focus:** "Do the golden tests compare frame hashes, or only that
  a file appeared?"
- **The build plan:** "stream-copy verified bit-identical".
- **03-delegation:** "the golden harness is delegated".
- **The visual gate:** goldens are exact and tied to the Chrome major. Today's
  OS upgrade moved all eleven while Chrome did not change (see Gate).

**Facts:**
- videotoolbox's encoded bytes can change with the OS and the chip.
- Decoded frame hashes of a copy are independent of any encoder: the copy
  above matched frame for frame.
- A filter graph's raw output, before encoding, is repeatable for one FFmpeg
  build when run with its bit-exact flags. Whether arm64 and x86 builds agree
  is not verified, so goldens are per major and per architecture until shown
  otherwise.

**Recommendation:**
- **Copy:** the export's decoded `framemd5`, frame by frame, equals the
  sources' in order. Its sound has exactly the timeline's sample count, and
  starts with the picture at every cut.
- **Conform:** goldens of the graph's decoded output, picture as raw frames and
  sound as PCM, before any encoder, run bit-exact and stored with
  `FFMPEG_MAJOR` and the architecture. The encoded export is checked with
  `ffprobe`: codec, profile, size, rate, frame count, duration, colour tags,
  sample rate, channels and start times. Never by hashing its bytes.
- **Fixtures** are tiny clips made once by a committed script from FFmpeg's
  test sources and committed as files, so their bytes do not depend on the
  machine that runs the test. There is one per mismatch: size, rate, pixel
  format, colour tags, sample rate, extradata, no sound, a still, a portrait
  clip, AAC priming.
- **Planted bugs the tests must catch:** skip the extradata comparison; copy the
  sound; take the copy path with a trim; leave `amix` normalising; shift the
  graph by one frame; read untagged as BT.709.

#### Q16. An export interrupted halfway

- **M8's review focus:** "What happens to an export interrupted halfway — a
  partial file left where a finished one is expected?"
- **R46:** export reuses "install's progress, cancellation and log".
- **M3 review #1:** a build step's pid, start time and pgid are recorded as it
  starts, and the startup sweep stops a verified survivor.
- **M1's directories decision:** "stage must share a volume with
  `assets/blobs` because adoption is `os.Link`".

**Not said:** where the export is written, when it becomes an asset, what the
sweep does with a running ffmpeg, whether Retry resumes.

**Recommendation:**
- **Written where nobody looks:** `<assets>/tmp/export-<job>.mp4`, on the blob
  store's volume, never in the library or a studio's directories.
- **An asset only when proven:** after ffmpeg exits 0 and `ffprobe` confirms the
  target's size, rate and sample rate, a frame count equal to the timeline's,
  and a duration within one frame. Then it is adopted by the existing hardlink
  path, and its item and inputs are written.
- **ffmpeg leads its own process group,** and its pid, start time and pgid are
  recorded before it writes a byte (schema change, 02 first).
- **The startup sweep** stops a verified survivor (SIGTERM, grace, SIGKILL),
  deletes its temporary file and marks the job `interrupted`. An identity that
  does not match is never signalled, as for build steps.
- **Cancel** stops the group and deletes the file; the job is `cancelled`.
- **Retry is a new export.** Nothing resumes.
- **Under the embedded provider,** ffmpeg is the studio's child. An export whose
  studio exited is `interrupted` at the next open.

#### Q17. What an export becomes, and whose it is

- **R48:** "An exported sequence becomes a gallery item whose `inputs[]` are its
  clips". **02 §10:** Export "writes an `items` row with `timeline_id` set and
  one `item_inputs` row per clip".
- **02 §5:** `items.studio_id` is `NOT NULL`, and the library file is
  `library/<studio>/<YYYY-MM>/<name>`.
- **02 §5:** `item_inputs`' primary key is `(item_id, asset_id, role)`.
- **02 §4:** `jobs.kind` has `export`; `log_files.kind` has no export kind, and
  `log_files.studio_id` is `NOT NULL`.
- **03 §10's gallery mockup** labels an export "café sequence (timeline ·
  14.2s)".
- **The studio id pattern** `^[a-z][a-z0-9-]{1,38}[a-z0-9]$` accepts `timeline`,
  so a pseudo-studio named `timeline` could collide with a real one, as
  `reverted/` would have in M7 Q9. `_timeline` fails the API's own
  `StudioIdValue`.
- **Progress and cancel:** 05 §7 says "poll /jobs/{id}"; M4 Q8 puts `/jobs`
  behind `jobs`, which h3 and ltx do not declare; M4 first review #2 lets a
  studio cancel only its own `task` jobs. The generated router checks exactly
  one capability per operation (`x-helm-capability`).

**Recommendation:**
- **A studio's export is that studio's.** The item's `studio_id`, the asset's
  `origin_studio` and its library folder are the exporting studio's. The
  gallery shows "timeline" as a label because `timeline_id` is set, not as a
  studio. Who owns a launcher export is decided with M9's screen.
- **Inputs:** one `clip` input per distinct asset, which is all the primary key
  allows. 02 §10's "one row per clip" is amended.
- **Progress and cancel belong to `timeline`:** `GET /timeline/{id}/exports` and
  `POST /timeline/{id}/exports/{job}:cancel`. The `job` event on `/events`
  reaches the studio that started the export. `/jobs` is unchanged, and a
  studio holding `jobs` still sees its exports there.
- **The log** is `log_files` kind `export`, owner kind `job`, under the exporting
  studio, keeping the newest five exports per studio as install keeps five
  builds.

---

### E · The API and where it is shown

#### Q18. The timeline operations

M4 Q1 removed the timeline surface "to return with the milestone that builds it
(timeline: M8)". API shape is never delegated, so these are proposed for
approval. All are tagged `studio-api` with `x-helm-capability: timeline`, so
`helm dev` and the embedded provider serve them and the conformance suite holds
both.

| Operation | Does |
|---|---|
| `POST /timeline` | Creates a sequence from `{name, target, clips}` (05 §7) or `{name, target, tracks}`. `clips` go to V1 for video and images, A1 for sound. Returns the document with `revision`, `duration_s` and `ETag`. |
| `GET /timeline` | The caller's timelines (Q11), paged like every collection (M4 Q5). |
| `GET /timeline/{id}` | The snapped document (Q7). |
| `PATCH /timeline/{id}` | A merge patch, `If-Match` required (Q10). Returns the snapped document. |
| `DELETE /timeline/{id}` | Soft delete (Q12). |
| `GET /timeline/{id}/revisions` · `POST /timeline/{id}:revert` | Q10. |
| `POST /timeline:append` | `{asset_id, track, timeline_id?}` (Q19). |
| `POST /timeline/{id}:open` | 501 until the launcher has a Timeline screen (Q19, Q20). |
| `GET /timeline/{id}:plan?preset=` | `{mode, reasons}` (Q13). |
| `POST /timeline/{id}:export` | `{preset}`; answers 202 with the job. |
| `GET /timeline/{id}/exports` · `POST /timeline/{id}/exports/{job}:cancel` | Q17. |

- **`:export`, not `/export`.** 05 §7 writes a slash, and M0 kept it literally.
  Every other action here is a colon verb (`:open`, `:append`, `:adopt`,
  `:cancel`, `:consume`). 05 §7 is amended.
- **`/timeline` stays singular,** as 05 writes it; `/gallery` and `/inbox` are
  singular too.
- **Errors:** 409 `etag_mismatch`; 422 `invalid_timeline` with `details` listing
  each clip's position and reason (overlap, short handle, unreadable asset,
  wrong kind); 501 `unsupported` for `tool_missing` (Q5).

#### Q19. `:open`, `:append`, and "the open one"

- **R45:** "`:append` adds to the open one without stealing focus, and `:open`
  asks the framework to show its editor."
- **05 §7:** `POST /timeline:append {"asset_id": …, "track": "V1"}` names no
  timeline; `:open` answers `{"opened": true, "surface": "app"}`, "a launcher
  window, or a tab the daemon focuses".
- **05 §5a:** `:open` "returns `501` with a reason, exactly as it would under a
  daemon with no window open".
- **The delivery decision:** the browser is the reference implementation, and
  "no feature may be reachable only from the app".

The daemon has no record of which timeline is open anywhere, and cannot focus a
browser tab. Nothing says what "a window open" means to a daemon.

**Recommendation:**
- **`:open`** answers 501 `unsupported` in M8, with a reason naming the launcher
  screen's milestone (Q20). When that screen exists, it holds an event stream,
  the daemon publishes `open_timeline` to it, and `:open` answers
  `{opened: true, surface: "browser" | "app"}`, or 501 when no launcher page is
  connected.
- **`:append`** appends to `timeline_id` when given. Without one it appends to the
  caller's most recently created or edited timeline, and with none it is 409
  `no_timeline`. That is "the open one" as far as a daemon can know it.

#### Q20. The launcher's Timeline screen before M9's cookie

- **M6 Q2:** "Timeline (§11) is M8's". **03 §6's nav** lists Timeline.
- **M6 Q11:** "no launcher Gallery screen until M9's cookie … a read-only
  launcher gallery under M2's Host/Origin rules would let any local process
  read every studio's items and bytes".
- **03 §11's screen** has "Add from gallery", and shows and plays every
  studio's clips.
- **M4 Q7:** the launcher has no studio-api access. **Open (M2 review):** any
  local process, including another account, can reach launcher operations.

The launcher's editor needs exactly the cross-studio read that M6 Q11 refused
to expose before M9.

**Options:**
- **(a) The screen waits for M9,** with the Gallery. M8 ships the API, export
  and `helm-timeline`, proven in a fixture studio page; `:open` answers 501.
- **(b) Build it in M8** under Host and Origin rules alone, accepting that any
  local process could read every studio's media through it.

**Recommendation: (a),** for M6 Q11's reason. Until M9, a cross-studio sequence
is edited in a studio holding `timeline` and `gallery.read_all` (Q11), which is
what those capabilities are for.

---

### F · The demo

#### Q21. What M8's machine-bound demos are

- **01's old milestone list (M5):** "a fourteen-second sequence assembled from
  three studios with an AuK voice line, exported and appearing in the gallery
  with its clips as inputs."
- **Build plan, phase 7:** "A 14-second sequence from three studios;
  stream-copy verified bit-identical".
- **M5 Q16 and M7 Q3:** ltx, AuK and iris do not adopt the runtime SDK in their
  milestones, so none of them records an asset. Only h3 does (M4's demo).

**Facts:** h3's 800×448 takes with 32 kHz sound and ltx's 704×448 clips with
48 kHz can never share a copy, and neither can a still or a voice line. The
build plan's two halves describe two different sequences.

**Recommendation:** two demos for M8a, one for M8b.
1. **M8a, the fast path.** Four h3 takes from one session at one size, exported
   to a target declared from them.
   - Seconds, with the plan saying "video stream copy".
   - Decoded frame hashes equal to the sources'.
   - Sound starting with the picture at every cut, measured.
   - Plays in QuickTime, Safari and Chrome.
2. **M8a, conform.** An h3 take, an ltx clip, an iris still and an AuK line,
   with a dissolve and gain, exported through the conform path.
   - Plays in the same three players, with colour compared against the sources
     by eye.
   - Appears as an item with its clips as inputs.
   - **Where the non-h3 assets come from is yours to choose:**
     - ltx, AuK and iris each adopt their outputs upstream first, as h3's
       `finishTake` does, so the clips genuinely come from three studios; or
     - a fixture studio uploads them, and the demo says every clip but h3's
       has one origin.
3. **M8b.** The same sequence edited in `helm-timeline` inside a fixture studio
   holding `timeline` and `gallery.read_all`.

---

### Defaults I will take unless told otherwise

- **Packages.** The document's rules, snapping and the legality predicate in a
  new `internal/timeline`, as pure functions. Running ffmpeg, export jobs and
  the sweep in a new `internal/export`. Enforcement stays in
  `internal/api/studioapi`, shared by both providers (M4 Q25).
- **No Go dependency.** `ffprobe -of json` is read with `encoding/json`, and
  ffmpeg is spawned through the platform seams with a process group, like
  build steps.
- **Progress** comes from `ffmpeg -progress pipe:1`, as frames written over the
  timeline's frame count.
- **Schema** is whatever version follows M7a's v6. DDL goes into 02 first.
- **`duration_s`** is computed and served, never stored.
- **One preset,** `h264`.
- **Tests** write only under temp roots. Fixture media and the script that makes
  it live under `test/media/`, a few hundred kilobytes in total.
- **The conformance suite** covers create, patch with `If-Match`, snapping,
  access, append, `:open`'s 501 and a copy export of fixtures, against both
  providers.
- **`helm dev`** serves the timeline operations, with `:open` at 501.

### Notes, no decision needed

- **Open entries this kickoff would resolve:** ffmpeg licensing (Q4).
- **Open entries it leans on without resolving:**
  - no API authentication until M9 (Q20)
  - M6 Q13's degraded player parts, which stay (Q6)
- **h3 and ltx declare `timeline` and call no timeline API.** h3 has
  `CombineTimeline`, and 08 plans to replace it after assets and the gallery
  are live. Criterion 9 asks for "the minimum capabilities it uses, and no
  more", as M7 Q3 applied to iris. Worth revisiting when each adopts, not now.
- **The reading copy of 05 §7** (`docs/artifacts/`) still writes `"asset"`. The
  markdown's `asset_id` wins (00-index).
- **M8b's kickoff decides the preview.** 04 §5's "gapless preview across cuts"
  needs either Media Source Extensions over remuxed fragments or a rendered
  preview, and both need ffmpeg on the daemon's side. Neither is chosen here.
- **The red visual gate** is recorded under Gate, and is not M8's.
- **Machine-bound work:**
  - **M8a:** both demos of Q21, in QuickTime, Safari and Chrome; the colour
    rule by eye; `h264_videotoolbox` encoder settings; a daemon killed with
    `kill -9` mid-export and restarted
  - **M8b:** editing and exporting through `helm-timeline` in a real browser

  None can run in the gate.

---

### Proposed expansion (draft, for Q1)

Written for "recommendations for all". If Q2 is answered otherwise, the tasks
keep this order in one brief.

#### M8a — the document, the API and export

##### Reads

- `docs/design/01-prd.md` R36, R40, R44–R49, §14's timeline line, §15
- `docs/design/02-data-model.md` §4 (jobs, log files), §5, §8, §10
- `docs/design/03-design-system.md` §10, §11, §17
- `docs/design/04-packages.md` §5 and §7
- `docs/design/05-sdk-and-custom-studios.md` §5a, §6, §7
- `docs/design/06-storage.md` §4, `07-platform-services.md` §3–§5, and 08's
  timeline section
- `api/openapi.yaml`: assets, gallery, jobs, events
- `docs/decisions.md` in full; the reports of M4, M6, M7 and this kickoff; M7a's
  review findings
- h3 at `a6eb54f`: `server/timeline.go` and `h3c/h3_ffmpeg.c`

##### Tasks

0. **The contract, before any code.**
   - Amend the design per the answers:
     - 01: R44–R49
     - 02: §5 (timelines: owner, `deleted_at`, revisions; export identity; the
       log kind), §8, §10
     - 03: §11's chip, and §10's export label
     - 04: §5 and §7
     - 05: §6 and §7
   - Record Q4 and close its open entry.
   - Add Q18's operations to `api/openapi.yaml`, then `make generate`.
   - Write the new schema version's DDL into 02 first.
1. **ffmpeg** (Q4, Q5): lookup, version floor, encoder allowlist, `tool_missing`;
   `ffprobe` into typed stream facts; a video poster for `/thumb` (Q6).
2. **The document** (`internal/timeline`): tracks, clips, holds, the dissolve and
   gain (Q8); snapping (Q7); target and preset (Q9); duration.
3. **Storage and operations** (Q10–Q12, Q18, Q19): the schema; revisions and
   revert; access checks; soft delete and reclaim's live-timeline clause;
   append; `:open` at 501; served by the daemon, `helm dev` and the embedded
   provider.
4. **The plan** (Q13): the legality predicate over probed facts, and
   `GET …:plan`.
5. **The graph** (Q3, Q14): a pure function from the document and probes to an
   ffmpeg argument list, for both paths.
6. **Export jobs** (Q16, Q17): the temporary file, process identity, progress,
   cancel, the sweep, verification, adoption, the item and its inputs, events
   and logs.
7. **The golden harness** (Q15): fixtures and their script, `FFMPEG_MAJOR`,
   decoded-hash comparisons and structural checks, in `make gate`.
8. **Conformance:** the timeline operations against both providers.

##### File list

- `internal/timeline/**` and `internal/export/**` (new)
- `internal/media/**`, `internal/api/**`, `internal/store/**`,
  `internal/platform/**` (spawn and identity seams only)
- `api/openapi.yaml` (the timeline operations, and job text), and generated
  files only through `make generate`
- `packages/helm-runtime-sdk/go/embedded/**`, if the embedded provider needs the
  export runner wired
- `cmd/helmstudio/**`, `cmd/helm/**` (`helm dev`)
- `test/conformance/**`, `test/media/**` (new)
- `Makefile`, `docs/agents/gate.md`
- `docs/design/01-prd.md`, `02-data-model.md`, `03-design-system.md` (§10,
  §11), `04-packages.md`, `05-sdk-and-custom-studios.md`
- `docs/decisions.md`, and the milestone report

##### Definition of Done

- **Legality is a predicate over probed facts, and every fact counts.**
  - A table test starts from fixture clips that copy, and changes one fact at a
    time: codec, profile, level, size, aspect ratio, pixel format, field order,
    rate, time base, each colour tag, extradata, a trim, a hold, an image, a
    transition.
  - Each change alone turns the plan to conform, and names that reason.
  - A fixture adopted with false hints still conforms.
- **The copy is bit-identical where it claims to be.**
  - The export's decoded picture hashes equal the sources' in order.
  - Its sound has exactly the timeline's sample count, with nothing at the start
    or at a cut. A planted stream copy of the sound fails.
- **Conform is checked by content, never by a file existing.**
  - A golden of the graph's decoded output for each mismatch fixture, pinned to
    `FFMPEG_MAJOR` and the architecture.
  - A one-frame shift, BT.709 read for untagged, or `amix` normalising each
    fails a golden.
  - The encoded export is checked with `ffprobe`, never by its bytes.
- **Time is canonical.** An unaligned write returns the snapped document;
  writing that document back changes nothing; 23.976 fps round-trips.
- **Access.** A studio without `gallery.read_all`:
  - cannot list, read, edit or export another studio's timeline (404)
  - cannot add an asset it cannot read (422, without saying whether it exists)
  - cannot export a timeline holding a clip it can no longer read
- **Undo and concurrency.** A stale `If-Match` is 409 `etag_mismatch`. A revert
  writes the old document as a new revision. A revert naming a reclaimed asset
  is refused.
- **Reclaim.** An asset named only by a live timeline is not reclaimable; one
  named only by a deleted timeline is.
- **An interrupted export leaves nothing behind.**
  - With the daemon killed by `kill -9` mid-export, the next start stops the
    verified ffmpeg, deletes its file, and marks the job `interrupted`. No asset
    and no item exist.
  - Cancel does the same, as `cancelled`. A failed verification adopts nothing.
- **Survives uninstall.** After the studio whose clips a timeline holds is
  uninstalled, the document, its export and its lineage are still readable by
  whoever could read them, and it still exports.
- **Licensing.** A test fails if any argument list helmstudio builds names an
  encoder outside Q4's allowlist.
- **No ffmpeg.** Export answers 501 `unsupported` with `tool_missing`. The gate
  fails without ffmpeg unless `HELM_ALLOW_MISSING_FFMPEG` is set.
- **Conformance** covers the timeline operations against both providers.
- **`make gate` is green**, including drift.
- **Machine-bound:** Q21's two M8a demos.

##### Review focus

- **The milestone's own four:**
  - Is stream-copy taken only when genuinely legal?
  - Do the golden tests compare frame hashes, or only that a file appeared?
  - Does the document survive a studio being uninstalled?
  - What happens to an export interrupted halfway?
- **Sound.** Is it ever copied? Does any cut add priming or padding?
- **Hints.** Does anything in legality or conform read an adopt hint?
- **Colour.** Is untagged read as BT.601 and exported tagged BT.709, and was that
  looked at on the Mac?
- **Access.** Can a studio obtain bytes it cannot read, through a timeline, a
  revision, an export or an append?
- **Identity.** Can the sweep ever signal an ffmpeg it did not start?
- **The tests.** Plant Q15's bugs.
- **Stated as verified?** Is anything machine-bound stated as verified?

#### M8b — the editor

Built on a reviewed M6b, M7b and M8a. Its file list and DoD are confirmed at
M8b's kickoff against what those reviews changed.

- **`helm-timeline`** in `packages/helm-ui-sdk/`, against a structural interface
  (M6 Q12):
  - tracks, and clips with their studio's name and hue, or "another studio"
  - drag and trim, snapping to frames
  - the dissolve and gain
  - undo through revisions
  - the plan's chip, and export with progress and cancel
- **Preview,** as decided at M8b's kickoff.
- **A fixture studio page** holding `timeline` and `gallery.read_all`, for the
  goldens and the demo.
- **Goldens** in both themes at 1280, 1000 and 380 px.
- **The launcher's Timeline screen** moves to M9, with the Gallery (Q20).
- **Machine-bound:** Q21's third demo.
