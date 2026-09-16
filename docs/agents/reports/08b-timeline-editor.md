# M8b — the timeline editor — implementation report

## What was built

**`helm-timeline`**, the editor for a framework-owned sequence (04 §5, §7). It
shows tracks and clips that say who made them, drag and trim snapped to the
daemon's grid, the dissolve and gain, undo through revisions, the export's plan
chip, and export with progress and cancel. **The preview plays each clip's own
bytes in the browser**, decided at this kickoff; ffmpeg is not involved. The
fixture studio got its editor page, and the goldens are in both themes at 1280,
1000 and 380 px.

**It was run against a real daemon, not only a fixture.** The sequencer fixture
ran under `helm dev` on this Mac with the committed test clips, the real studio
API and Homebrew's ffmpeg 9.0.1. A keyboard trim went through the proxy as a
`PATCH`, and Undo as a revert. The preview showed the still and played across a
cut. An export from the editor finished at 216 of 216 frames. None of that is
the machine-bound demo (see "Could not verify"), but it is the component on the
real contract.

**The fifth milestone built out of order**, at the human's direction. M6b and
M8a, which it rests on, are unreviewed. The work is on branch
`feat/timeline-editor` and is not merged.

## Gate

    $ go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since 8d2d4f8
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	1.866s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.380s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	1.122s
    ok  	github.com/janishar/helmstudio/internal/approval	0.388s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.371s
    ok  	github.com/janishar/helmstudio/internal/export	0.378s
    ok  	github.com/janishar/helmstudio/internal/install	9.070s
    ok  	github.com/janishar/helmstudio/internal/library	1.333s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.442s
    ok  	github.com/janishar/helmstudio/internal/media	0.410s
    ok  	github.com/janishar/helmstudio/internal/platform	0.470s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	0.745s
    ok  	github.com/janishar/helmstudio/internal/supervisor	40.369s
    ok  	github.com/janishar/helmstudio/internal/theme	0.490s
    ok  	github.com/janishar/helmstudio/internal/themelint	0.381s
    ok  	github.com/janishar/helmstudio/internal/timeline	0.380s
    ok  	github.com/janishar/helmstudio/internal/weights	0.867s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-css	0.399s
    ?   	github.com/janishar/helmstudio/packages/helm-css/cmd/build	[no test files]
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/node	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-ui-sdk	0.415s
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ?   	github.com/janishar/helmstudio/studios	[no test files]
    ok  	github.com/janishar/helmstudio/test/media	3.051s
    ?   	github.com/janishar/helmstudio/test/studios/sequencer	[no test files]
    ok  	github.com/janishar/helmstudio/web	0.397s
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.529s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	4.158s
    ok  	github.com/janishar/helmstudio/test/visual	91.357s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

macOS 27 on Apple Silicon, Go 1.27.1, Chrome 152, ffmpeg 9.0.1.

**The new goldens were not stable at first, and are now.** The first gate
failed on the timeline goldens, which differed from themselves by 1 to 47
pixels of corner antialiasing. Measured, not guessed. Three causes, removed in
turn:
- the export row redrawing every poll;
- `<video>` elements created with no way to load them;
- the track heads being `position: sticky` in the scroller. With sticky heads,
  four of six full runs failed; with the heads in their own column, one of
  eighteen failed, at the panel's rounded corners;
- the lanes being a scroll container even at fit zoom, where there is nothing
  to scroll.

After the last of these, twelve of twelve full runs passed. That is a
measurement, not a proof, and it is said as one.

## What the tests hold

All in `test/visual/timeline_test.go`, driving the real component in Chrome
against a sequence that `internal/timeline` lays out
(`timeline_fixture_test.go`). **Every one was made to fail by planting its bug,
and two did not fail at first** (see "Tests that did not catch their bug").

- **The arithmetic is the daemon's.** `frames`, `evenFrames`, `snap`, V1's
  layout and duration are compared with `internal/timeline` at every rate. The
  cases include the negative half-frame that JavaScript's `Math.round` rounds
  the other way; planting `Math.round` fails it.
- **A write is what the daemon expects.** The whole of `tracks` goes with the
  `If-Match` read, V1 without `at`, sound clips with it, and nothing the daemon
  sets. Planting V1 positions fails it.
- **A refusal is the daemon's sentence**, and the document goes back to the
  daemon's last answer. Shortening V1 by a frame leaves A2's ambience past the
  end, which the real rules refuse. Keeping the optimistic drawing fails it.
- **A conflict reads the sequence again** and does not replay the edit.
- **Undo and Redo walk revisions** with the right revision and `If-Match` each
  time, and a new edit ends Redo. Undoing from the latest revision rather than
  the one showing fails it.
- **A drag along V1 reorders it**, the daemon lays it again, and the selection
  follows the clip.
- **Clips say who made them**: the caller's own in its accent, others neutral,
  "another studio" when it may not learn, and the same in each clip's accessible
  name. Painting everything neutral fails it.
- **The chip is the daemon's answer**: the first reason from `internal/timeline.Plan`,
  "video stream copy", or "export unavailable" with Export disabled. Naming the
  last reason fails it.
- **An export shows progress in frames and cancels**, the row is a live region,
  and a poll that changes nothing leaves focus on Cancel. Rebuilding the row
  every poll fails it.
- **A read-only editor writes nothing by any path**: keys, `el.append()`, and
  its write path called directly.
- **The preview places each clip where the sequence says.** On a plain frame
  one picture shows at its own time. Inside the dissolve two show, with
  opacities from the mix and the incoming clip reading its handle before `in`.
  During a still's hold, the next clip is parked on its in point. Inverting the
  mix or not parking fails it.
- **`frameAt` on every frame** of the fixture: one or two pictures, opacities
  summing to 1, each sound at its clip's and track's gain, no sound from a still
  or a muted clip.
- **The editor is as wide as the page says**, in a block and in a grid parent.
  Removing the containment fails it by running away.

## Tests that did not catch their bug

Worth more than the ones that did.

- **The read-only guard.** The first test drove only the keys, which check
  `editable` themselves, so planting the guard's removal in the write path
  passed. A second attempt dispatched `change` on a disabled field, which
  Chrome does not deliver, so it passed too. Looking for a path that does reach
  the write path found **a real bug: Cmd+Z undid, and wrote a revert, in a
  read-only editor**, and `el.append()` did not check either. Both are fixed,
  and the test now uses each path, including an undo made reachable by moving
  the sequence on a revision first.
- **Parking the next clip.** The test parked a clip whose in point is 0, which
  is also where an element that was never parked sits. It now checks the ltx
  clip, whose in point is 0.5 s.

## Design contradictions raised

**None that stopped the work.** Two open items are in `docs/decisions.md` under
"Open, not yet decided":

1. **No operation serves another studio's name or hue to a studio's page.**
   `TimelineClip.studio_id` is described as "for the clip's hue and name", and
   03 §11 wants a sequence from three studios to read as three. A studio page
   can learn only its own hue. So the caller's clips wear its accent and every
   other studio's clips are neutral, labelled with the studio id or "another
   studio". Proposed: `studio_name` and `studio_hue` beside `studio_id`, under
   Q11. A contract change, not made.
2. **helm-ui-sdk's rule against building a client greps for `connect`**, which
   is also how Web Audio routes a node. So gain above 0 dB previews at 0 dB,
   and says so. The call was not renamed to slip past the grep.

## Judgement calls

All are in `docs/decisions.md` under "M8b · decided at its kickoff, and judgement
calls while building", for sign-off. The kickoff's own decisions come first:

- **Preview in the browser from each clip's bytes**, rejecting remuxed MSE and a
  rendered proxy. Both need ffmpeg and a new operation, and would leave editing
  without a preview wherever ffmpeg is missing.
- **The structural interface**, now a row in 04 §5's table.
- **Writes send whole `tracks` with V1 unpositioned.** The answer replaces the
  drawing, a conflict is never replayed, and a refusal is never papered over.
- **No embedded picker.** Add is `add-request` plus `el.append()`.
- **The track heads are their own column**, and the component's width is the
  page's.

## What running it for real found

Everything in this section came from the fixture studio's first run under `helm
dev`, not from a test.

**In M8a's fixture studio, fixed here because the demo needs it:**
- **It never started as documented.** `helm dev` runs a studio in its manifest's
  directory, so `go run ./test/studios/sequencer` named a path that does not
  exist.
- **`/run` could never create its sequence.** Its uploads gave no duration, and
  Q7 refuses a video or sound clip whose asset records none. It now probes each
  file with `ffprobe`.
- **Its dissolve had no handles.** It now gives half the dissolve's length on
  each side.

**In M8a's pipeline, not fixed here, offered as its own task.** A dissolve
without handles is accepted and renders short, where Q8 says it is refused
naming the missing frames. The export then fails verification (`the export is
9.250s and the sequence is 9.500s`), so nothing wrong is adopted, but the user
reads the wrong message. This touches the rules and the graph Q3 keeps for the
human.

**In M6b's components, not fixed here, offered as its own task.**
helm-gallery's and helm-player's controls are unreadable in light theme: dark
text on `--helm-ground-inset`, which is near-black in both themes.
`ui-light.png` pins it. helm-timeline hit the same bug in its own goldens.

**In helm-timeline itself, all fixed before commit:**
- the 52,616 px runaway width in a grid parent;
- read-only undo and append;
- focus taken off Cancel by the export poll;
- unreadable inputs, a chip cut mid-word, a double-height ruler row and a
  dissolve marker over the label, all in light theme.

## Could not verify

- **The machine-bound demo.** Q21's conform sequence, with an h3 take, an ltx
  clip, an iris still and an AuK line, edited and exported in `helm-timeline`
  inside the fixture studio. It needs real outputs from all four studios. What
  ran instead is above: the same studio and component on the committed test
  clips.
- **"Gapless within a frame" on real h3 and ltx clips.** Playing across a cut
  was seen to work on the test clips, with the element 0.08 s behind the clock
  at one sample. It was not measured frame by frame, and not on 800×448 h3 takes.
- **The preview's sound by ear**: the mix, a dissolve's crossfade, gain.
- **Safari and Firefox.** Only Chrome ran. `contain: inline-size` and
  `requestVideoFrameCallback` differ between them.
- **A real mouse or trackpad drag.** Only synthetic pointer events.
- **VoiceOver** reading the clips, the chip and the export row.

## Dependencies added

None. `go.mod` is unchanged, and nothing was added to any package.json.

## Files touched outside the milestone's list

M8b's file list was to be confirmed at kickoff, and no review ran to confirm it
against. The work lives in `packages/helm-ui-sdk/**` and `test/visual/**`.
Everything else, named for sign-off:

- **`test/studios/sequencer/**`** (M8a's fixture): its page, and the three
  fixes above.
- **`docs/design/04-packages.md` §5**: helm-timeline's row in the table of
  structural interfaces, and a sentence on why it embeds no picker.
- **`.gitignore`**: `.helm/` anywhere, not only at the root, because `helm dev`
  puts it beside a manifest.
- **`docs/decisions.md`**: the M8b section, two open items, and M7b's open item
  moved to where open items live. It had been left out of that list last
  milestone.

## Decision-log entries appended

Under "2026-09-16 · M8 timeline and export", a section "M8b · decided at its
kickoff, and judgement calls while building" with fifteen entries, and three
entries under "Open, not yet decided":
- other studios' hues;
- the `connect` grep;
- M7b's flow spacing, moved.

## Left undone

- **The machine-bound demo**, above.
- **The launcher's Timeline screen**, which is M9's (Q20).
- **Filmstrips, waveforms and proxies**, which belong to no milestone (Q6).
- **The two findings offered as tasks**: M8a's handle refusal, and M6b's
  light-theme controls.
- **Merging.** Nothing is merged or pushed. Reviews outstanding: M6a, M6b, M7a,
  M8a, M7b, and now M8b.
