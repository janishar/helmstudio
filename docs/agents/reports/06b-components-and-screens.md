# M6b — Components and screens — implementation report

## What was built

**The three `helm-ui-sdk` components and the launcher's screens.**
`packages/helm-ui-sdk/` is new: `helm-terminal`, `helm-gallery` and
`helm-player`, custom elements in Shadow DOM that import nothing at all and
call methods on a client they are handed. `web/` is no longer the plain shelf
but the launcher — a top bar with the theme control, a nav, and the catalogue,
studio detail and install, process group, models and disk, and settings —
drawn against a launcher client generated from the same OpenAPI document by the
same generator.

**A separate report, because M6a's is not mine to overwrite.** M6a's is
`06-design-and-ui.md`; this milestone shares its brief and needed its own file.

**This was built out of order.** M6b's brief says it is built on a reviewed M6a
and, since M7 Q2, on a reviewed M7a. Neither exists. The costs are under
"Judgement calls".

## Gate

    $ go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since 0816a27
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	1.176s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.260s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	1.089s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.387s
    ok  	github.com/janishar/helmstudio/internal/export	0.395s
    ok  	github.com/janishar/helmstudio/internal/install	9.127s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.393s
    ok  	github.com/janishar/helmstudio/internal/media	0.384s
    ok  	github.com/janishar/helmstudio/internal/platform	0.421s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	0.725s
    ok  	github.com/janishar/helmstudio/internal/supervisor	40.356s
    ok  	github.com/janishar/helmstudio/internal/theme	0.494s
    ok  	github.com/janishar/helmstudio/internal/themelint	0.328s
    ok  	github.com/janishar/helmstudio/internal/timeline	0.340s
    ok  	github.com/janishar/helmstudio/internal/weights	0.833s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-css	0.084s
    ?   	github.com/janishar/helmstudio/packages/helm-css/cmd/build	[no test files]
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/node	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-ui-sdk	0.089s
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ok  	github.com/janishar/helmstudio/test/media	3.031s
    ?   	github.com/janishar/helmstudio/test/studios/sequencer	[no test files]
    ?   	github.com/janishar/helmstudio/web	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.502s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	3.993s
    ok  	github.com/janishar/helmstudio/test/visual	43.046s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

Run on macOS 27 on Apple Silicon, Go 1.27.1, Chrome 152. `packages/helm-ui-sdk`
is new in this run; `test/visual` grew from eleven goldens to fifty-one and
gained `TestComponentsBehave`.

## What the tests hold

The pieces a reviewer should try to break, and what each proves:

- **The dependency arrow** (`packages/helm-ui-sdk/rules_test.go`). Five tests,
  one per rule in 04 §11: nothing in the package names `fetch(`,
  `XMLHttpRequest`, `EventSource`, `/api/v1`, `Authorization`, `Bearer` or
  `hs_live_`; nothing constructs a client or imports outside the package;
  the runtime SDK names no component; helm-css styles no component element;
  every `var(--helm-…)` a component uses exists in `tokens.json`; and the
  component stylesheets pass the same colour-literal lint a studio gets.
  Planted and confirmed failing: a `fetch("/api/v1/jobs")` in the terminal, a
  `--helm-log-danger` that does not exist, and a `helm-gallery { }` rule in
  helm-components.css.
- **Component behaviour** (`test/visual/ui_test.go`), read from the DOM in a
  real browser. Carriage returns rewrite in place; the surviving row is the
  last write; ANSI becomes spans and no escape survives into Copy; every item
  gets a thumbnail through the client's own method; a sequence's export is
  labelled "timeline"; the filmstrip says Unsupported; the picker resolves to
  an asset. Planted and confirmed failing: a terminal that does not rewrite
  (13 rows, not 10), and a gallery that drops the timeline label.
- **The token is write-only** (`internal/api/secrets_test.go`). Storing,
  reading back and deleting; a token added outside helmstudio has no time; an
  empty token and a pasted newline are 422 with a sentence; a platform with no
  secret store answers 200 with a reason on `GET` and 501 on the writes;
  without `WithSecrets` the routes do not exist. Planted and confirmed failing:
  a `token` field added to the response.
- **The goldens.** Six screens at three widths in both themes, the three
  components in both themes, and helm-css's own eleven. Two consecutive
  `make golden` runs are byte-identical, and a run after them passes.

## Design contradictions raised

Four, all raised rather than resolved in code, and all recorded as open entries
in `docs/decisions.md`.

| # | One side | The other side | What I did |
|---|---|---|---|
| 1 | 03 §6: a card has "a 3px identity stripe"; §7's header reads `github.com/janishar/h3c-studio · main · a4f91c2`; §7's left column compares what the manifest requires with what this Mac has | `Studio` in `api/openapi.yaml` carries no `hue`, no `repo`, no `ref` and no `requires`, and nothing anywhere reports the host's OS version, memory, free disk or tools | Drew what is served. Every card wears the fallback accent, the header shows the checkout and the commit, and **the Requirements block is not drawn at all**. Adding fields to `Studio` is an API change and not mine to make to fit a screen. |
| 2 | 03 §6: "Every state in the canonical vocabulary has a row here" | `cloned`, `built` and `removing` have no row | Took §6's own fallback — the raw name in an idle chip — which the DoD also asks for. `cloned` and `built` are mid-install states and read poorly that way; a row each, or a deliberate acceptance, is the human's. |
| 3 | 03 §17: focus containment, Escape and focus order for a dialog | helm-css ships `.helm-scrim` for a hand-built overlay and no `dialog::backdrop`, so a native `<dialog>` has an unstyled browser backdrop | Used a native `<dialog>` and put one `dialog.helm-dialog::backdrop { background: var(--helm-scrim) }` rule in the launcher's own stylesheet. The rule belongs in helm-css; that is M6a's file. |
| 4 | 03 §6's shell, and the DoD's 380 px goldens | `.helm-topbar` is a single flex row that does not wrap, and the shell overflows 380 px, scrolling the whole page sideways | One `flex-wrap: wrap` in `web/launcher.css`, which is the override path 04 §3 promises. `flex-wrap` belongs in `helm-layout.css`; that is M6a's file, and its goldens would move. |

Nothing else in the design was found wrong while building. Two defects were
found in this milestone's **own** code, both by tests and both after a planted
bug proved the first version of a test did not discriminate — see "Judgement
calls".

## Judgement calls

**Building out of order.** The human directed M6b before M6a's review and
before M7a. What that costs, so a reviewer can see it:

- **Rework.** M7a changes `/studios`, `:install` and `:launch`, which the
  catalogue and the detail screen read and call. Expect the state vocabulary in
  `web/ui.js` and `web/catalogue.js` to move.
- **No approval preview.** M6b's brief says Install shows M7a's approval
  preview as a plain dialog. M7a's preview does not exist, so Install shows the
  install checklist and nothing else. Adding a studio is M7's screen; the
  catalogue says where studios come from rather than offering a button that
  goes nowhere.
- **M6a unreviewed.** Every class name and token the screens use is provisional
  until M6a's review (its own judgement call says so). A finding that renames
  one moves the screens and all fifty-one goldens.

**The shape of the launcher.** A screen is `screen(ctx, ...args) → Node`: a
pure function of a context and its route. `ctx` carries the client, the store,
the query and the actions; nothing owns a timer; one poll replaces the studio
list and the current screen is drawn again. Three things follow, and each was a
decision:

- **The page is rebuilt only when a signature of what it draws has changed**,
  and never while the caret is in one of its fields. A two-second rebuild that
  threw the caret away would have made the token field unusable.
- **A `<helm-terminal>` is kept across rebuilds by key**, and the component
  keeps its buffer across a disconnect and resumes from its last event id.
  Moving an element in the DOM is a disconnect; without both, the install log
  restarted twice a minute.
- **A fixture can draw any screen** by handing it a context whose client is a
  fake, which is what the goldens do — against the real modules, not a copy.

**The launcher client.** Generated to `web/launcher.js` from the
`launcher`-tagged operations, by the same generator, and carrying operations
only: `Transport` and `HelmError` come from the runtime SDK's own transport,
served at `/sdk/v1/runtime/transport.js`. 04 §4 says the typed error shape must
mean the same thing everywhere, and a second copy of the status-to-kind table
is a second thing to drift. To generate it at all, every launcher operation
gained the `x-helm-group` and `x-helm-method` extensions every studio-api
operation already carries; the generator still filters by tag, and `make drift`
shows the runtime SDK unchanged.

**`helm-ui-sdk` imports nothing.** A component reads `err.kind` off what it is
thrown rather than importing the runtime SDK's error class. The package has no
import outside itself, so 04 §11 rule 2 holds by construction.

**How a component reaches bytes.** `Asset.url` tells a *page* to prefix `/helm`
for its proxy, and a component may not know a proxy exists. So it asks the
client — `assets.read(id, {range: "bytes=0-0"})` — cancels the body at once and
gives `<video>` the address off the response the client resolved. A thumbnail
is read as a blob and shown through an object URL, released on disconnect.

**Smaller calls:**
- The sixteen ANSI colours collapse onto the four log tokens 03 §2a gives: red
  is error, yellow warning, everything else accent, dim muted.
- `[hidden] { display: none }` is the last rule in every component's
  stylesheet, because a flex `display` above it beats the user agent's rule.
- A route carries a query, so which process log is open survives a reload.
- A stored secret's time is shown as `YYYY-MM-DD HH:MM UTC`, not in the
  machine's locale: it is read across machines and pinned in a screenshot.
- `added_at` for the Hugging Face token is a `settings` row, because the
  Keychain records no time; a token added by hand has none, and says so rather
  than showing an invented one.
- The token routes are served only with `api.WithSecrets`, which `helm dev`
  does not pass.
- The gallery emits `select` and `pick` and takes a studio's own buttons in an
  `actions` slot: what can be done with an item is the studio's (rule 5).

**Two defects in this milestone's own code, both found by planting a bug:**
- **A line ending in a carriage return rendered empty.** `progress()` took the
  text after the last `\r`, which is right for `25%\r50%\r75%` and wrong for
  `25%\r` — the content of a progress write is *before* the return. The first
  version of the test asserted a row count that was the same either way; the
  planted bug passed, which is what exposed it.
- **`HelmPlayer`'s frame counter was named `frame`**, which overwrote the base
  class's `frame` element and broke every empty state — the gallery and the
  player rendered nothing at all when a client arrived after the element
  connected. The base's node is renamed `region` and made non-writable, so the
  next collision throws at construction rather than at first render.

**Goldens.** Six screens × three widths × two themes, plus the components and
helm-css's own. **No golden holds a decoded raster image**: a thumbnail in the
page changes how Chrome composites the panel around it, by one unit in one
channel at the rounded corners and differently from run to run. Measured: two
`make golden` runs of the same page differed by 78 pixels with a thumbnail and
were byte-identical without one. M6a chose exact goldens deliberately and
proved a tolerance of 4 hides a token moving by 2/255, so the image came out of
the picture rather than the tolerance going in. The thumbnail path is held by
`TestComponentsBehave` instead. A fixture also declares itself ready only once
its markup and images have been the same across two polls, and one that never
settles fails rather than being pinned half-drawn.

## Could not verify

- **Everything through a real daemon beyond a smoke test.** The daemon was
  started on a temporary root and every screen was opened in Chrome with no
  console errors, the theme was toggled and persisted, and the four manifests
  listed. What was **not** exercised against real data: an install running to
  completion, a real process log, a real heavy conflict and the switch dialog
  it opens, a real weights download, and reclaim. Every one of those needs a
  studio that actually installs, which needs the network and, for three of
  them, tens of gigabytes.
- **The Keychain.** `internal/api/secrets_test.go` runs against a fake secret
  store. The real `security` path is M1's, unchanged, but no test here writes
  the user's Keychain — deliberately. On the Mac: add a token in Settings,
  check `security find-generic-password -s helmstudio -a huggingface-token`,
  then remove it and check it is gone.
- **The components against a studio's own page.** They were driven from
  fixtures with fake clients, and served correctly from `/sdk/v1/ui/*.js`, but
  no studio imports them yet: h3 stays at level 2 (M6 Q20), and the fixture
  studio Q20 asks for is M7's.
- **`helm-player` playing anything.** Its transport, scrubber and degraded
  bands are pinned; the golden shows the "cannot play these bytes" state,
  because a decodable video in a golden would need committed media and a codec.
  Frame stepping, A/B compare and Extract frame are unexercised. On the Mac:
  open a take in a studio page, step with the arrow keys and check the frame
  counter against ffprobe.
- **M6a's machine-bound demo** (the theme toggle reaching a running studio) is
  still owed; M6b did not run it either.

## Dependencies added

None. `go.mod` is unchanged. No npm package, no bundler: the components and the
launcher are hand-written ES modules served as they are.

## Files touched outside the milestone's list

M6b's file list did not exist; it was confirmed at this kickoff and is now in
the brief. Against it, one file was touched that it does not name:

- **`docs/agents/milestones/06-design-and-ui.md`** — the brief itself, which
  task 0 of a scoped milestone expands. It gains M6b's file list, its confirmed
  Definition of Done, and a note that it was built out of order.

Everything else is inside the list: `packages/helm-ui-sdk/**` (new),
`api/gen/**` and `api/openapi.yaml` with the generator's output,
`internal/api/**`, `cmd/helmstudio/**`, `web/**`, `test/visual/**`, the
`Makefile`, `docs/agents/gate.md`, `docs/design/04-packages.md`, the decision
log and this report. `web/shelf.js` was removed: the launcher replaces it.

## Decision-log entries appended

Under a new "M6b · components and screens" heading inside
"2026-09-16 · M6 design and UI": eighteen entries for the decisions above,
under a line naming them judgement calls for the reviewer to confirm or
overturn. Five entries were added to "Open, not yet decided": the four
contradictions in the table, and the launcher's two-second poll, which exists
because there is no launcher-wide event stream.

## Left undone

- **Nothing in M6b's scope**, with one stated exception: 03 §7's Requirements
  block, which no endpoint can supply. See contradiction 1.
- **Adding a studio, the manifest editor and the approval screen** — M7's.
- **The launcher's Gallery and Timeline screens** — M9's, behind its cookie
  (M6 Q11, M8 Q20).
- **Doctor** — hidden in the nav, because it belongs to no milestone (M7 Q16).
- **`helm-timeline`** — M8b's, which this unblocks: `packages/helm-ui-sdk/`
  now exists.
- **Committing.** Per `implementer.md`, nothing is committed.
