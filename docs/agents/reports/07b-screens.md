# M7b — the screens — implementation report

## What was built

**03 §13 and §13a, drawn.** The library's cards state source, level and install
state as three facts, with Override, Revert, Duplicate and a checkpoint choice.
Add a studio reads a repository or a folder, imports, or starts a manifest. The
editor generates its form from `schema/manifest.json` beside the YAML and the
criteria. Import reports before it acts. The approval screen is a screen.

**Most of the work was not the screens.** Drawing them against a real daemon
exposed five places where M7a's operations did not do what the contract says.
One of them made a form edit destroy part of the document. Another meant a saved
Override was shown and run as the registry's version until the daemon restarted.
All five are fixed here, each in its own commit with a test that fails without
it (see "What building the screens found").

**Fourth milestone built out of order**, at the human's direction: M6a, M6b and
M7a are unreviewed beneath it. On branch `feat/library-screens`, 14 commits, not
merged.

## Gate

The final tree, after one flaky run (below):

    $ go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since 2be689c
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	1.790s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.396s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	1.028s
    ok  	github.com/janishar/helmstudio/internal/approval	0.409s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.388s
    ok  	github.com/janishar/helmstudio/internal/export	0.399s
    ok  	github.com/janishar/helmstudio/internal/install	10.277s
    ok  	github.com/janishar/helmstudio/internal/library	1.824s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.966s
    ok  	github.com/janishar/helmstudio/internal/media	0.941s
    ok  	github.com/janishar/helmstudio/internal/platform	0.432s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	1.296s
    ok  	github.com/janishar/helmstudio/internal/supervisor	40.438s
    ok  	github.com/janishar/helmstudio/internal/theme	0.479s
    ok  	github.com/janishar/helmstudio/internal/themelint	0.925s
    ok  	github.com/janishar/helmstudio/internal/timeline	0.377s
    ok  	github.com/janishar/helmstudio/internal/weights	1.327s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-css	0.936s
    ?   	github.com/janishar/helmstudio/packages/helm-css/cmd/build	[no test files]
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/node	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-ui-sdk	0.382s
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ?   	github.com/janishar/helmstudio/studios	[no test files]
    ok  	github.com/janishar/helmstudio/test/media	3.125s
    ?   	github.com/janishar/helmstudio/test/studios/sequencer	[no test files]
    ok  	github.com/janishar/helmstudio/web	0.364s
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.535s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	4.042s
    ok  	github.com/janishar/helmstudio/test/visual	69.079s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

macOS 27 on Apple Silicon, Go 1.27.1, Chrome 152.

**One gate run on this tree failed, in a package M7b does not touch:**

    --- FAIL: TestInterruptedDownloadResumesWhereItStopped (0.03s)
        download_test.go:48: interrupted fetch: err = beginning a write transaction: interrupted (9), want context.Canceled
    FAIL	github.com/janishar/helmstudio/internal/weights	0.580s

Measured on both this branch and `main` at 2be689c: 0 failures in 200 isolated
runs each, and 0 in 400 runs each alongside six concurrent `internal/supervisor`
runs. `git diff 2be689c..HEAD` touches nothing under `internal/weights`,
`internal/store`, `internal/manifest` or `internal/platform`. The cause:
`store.Update` begins its transaction with the caller's context. When a download
is cancelled at that moment, the SQLite driver answers `interrupted (9)` rather
than the context's error. **The test was not touched.** A fix is offered as a
separate task, because it is M1's store and M3's weights.

## What the tests hold

- **The form is placed, not just generated** (`web/sections_test.go`). Every
  property `schema/manifest.json` describes, walked recursively through objects,
  is either in a form section or on the text-only list with a reason. Removing
  `/runtime/precision` from the map fails it by name.
- **The page does not parse YAML** (`web/rules_test.go`). The launcher imports
  only its own files and `/sdk/v1/`, and nothing in it calls a YAML parser.
  Planting `import YAML from "https://cdn.jsdelivr.net/npm/yaml@2/+esm"` and a
  `YAML.parse` fails both. **What these rules cannot prove** is that nobody
  writes a hand-rolled `split(":")` and calls it reading a manifest. That is for
  review.
- **The card and the approval screen agree** (`internal/api/approval_test.go`).
  Every entry is read both ways, and a test fails if the two sources or levels
  differ.
- **An Override is what the next install shows** (`internal/api/manifests_test.go`).
  Saving an Override changes the approval preview's commands, Revert changes them
  back, and a duplicated or imported studio has a preview rather than a 404.
- **A form edit keeps what it did not touch.** This runs in a browser against the
  real daemon (`test/visual/screens_test.go`). Setting the minimum memory on an
  invalid manifest keeps `os`, `arch`, `tools` and the comment. With the original
  bug re-planted, all three fields are lost.
- **A registry entry's form edits the manifest it carries.** Editing the
  repository writes both copies and the entry stays valid. Re-planting the bug
  writes one copy and leaves "1 error".
- **Install sits below every command** on the approval screen. Moving the button
  beside the title fails it.
- **Import names a collision** and adds nothing until someone chooses.
  Auto-overriding fails it with "Add 2 to library".
- **The library redraws when only a card's facts change**: each of eight fields,
  changed alone, must move the redraw signature. Removing `source` fails it.
- **Goldens**: add, editor, import and approve, in both themes at 1280, 1000 and
  380 px. Catalogue, switch and settings goldens moved, each for a reason given in
  its commit.

## Design contradictions raised

**None new that blocked.** One open item recorded in `docs/decisions.md`:

**An edit rewrites flow collections it did not touch.** 03 §13a says an edit
keeps "comments and key order", and it does. But `manifest.Edit` re-encodes the
tree, so `port: { prefer: 8730 }` elsewhere in the file becomes
`port: {prefer: 8730}`. M7a's own comment in `internal/manifest/edit.go` says an
edit should not rewrite lines the author did not touch. Either the encoder keeps
flow spacing, or 03 §13a says only comments and key order are kept. Not resolved
in code.

**Still open from M7a:** the approval's home, and v7's three unused columns.

## What building the screens found

The screens were exercised in the in-app browser against a real daemon rooted in
a scratch `HELMSTUDIO_HOME`: the library, Add, the editor, import with a
collision and a rename, an Override saved and reverted, and the approval preview.
That is where most of these came from. The goldens alone found one.

**In M7a's operations, which the contract already decided:**

1. **A form edit on an invalid manifest destroyed part of it.** ManifestCheck's
   `document` is "the parsed YAML as JSON … whether or not it is valid". M7a only
   sent it for a valid manifest. With nothing to look at, the page took every
   parent to be missing and wrote an empty map over it. Setting the minimum
   memory replaced all of `requires`. Fixed on both sides: the daemon sends the
   document, and the page never creates a parent it has not seen to be absent.
2. **A saved Override was shown and run as the registry's version.** The
   supervisor builds the preview and runs install and launch, and was given the
   library once, at startup. An imported or duplicated studio answered "no
   studio" to Install. Found when a saved Override's card showed the new source
   beside the old description. Every library write now reloads it.
3. **The approval screen said "from the registry" over a file the user wrote.**
   The preview's source and level were hardcoded.
4. **Four declared fields were never served**: `provenance`, `selection`,
   `selectable`, `rebuild_needed_reason`. The response to setting the selection
   did not contain the selection.
5. **"1 processes"**, on the first line of the approval screen.

**In this milestone's own code, all found before anything merged:**

- **Override opened a blank form.** Every bundled studio is a registry pointer
  with its manifest under `manifest:`, and the form read and wrote the pointer's
  top level. The first keystroke made the entry invalid.
- **After a Revert, the card stayed stale.** The launcher redraws only when its
  change signature moves, and the signature did not include what the cards state.
- **The checkpoint choice was offered at launch only when approval was needed.**
  A studio whose approval was current never offered one.
- **An invalid entry read "Running · manifest not loaded"**, and offered Stop
  for a process that did not exist. Found by the 380 px golden.
- `python.version` was starred as required inside an optional object. "What this
  would run has changed" showed on studios never approved. The approval screen
  printed the repository twice.

## Judgement calls

All are in `docs/decisions.md` under "M7b · judgement calls while building". The
ones a reviewer should look at first:

- **The approval screen does not run the operation.** It records the checkpoint,
  grants the digest for exactly one call, and hands control back to the action
  that was asked for. An approval screen that knew how to install would also
  have to know how to launch and retry.
- **The schema is a static document at `/schema/manifest.json`**, not an API
  operation, so it is not in `api/openapi.yaml` or the generated clients.
- **The editor, import and approval goldens are drawn by a real daemon** mounted
  in the fixture server. A canned `:validate` answer would be a second copy of
  the response shape the editor's design rests on. It is also why finding 1
  reproduces in a browser test.
- **Routes are `#/add` and `#/edit/{id}`, not `#/studios/new`**, because `new`
  is a legal studio id.
- **The launcher dims disabled fields**, which helm-css does not. It is a page
  rule, raised for M6a's review.

## Could not verify

- **The DoD's machine-bound demo, in full. None of it ran.** On the Mac:
  1. Take a public repository with no registry entry and no `helmstudio.yaml`.
  2. Add a studio → From a repository: confirm "No helmstudio.yaml there", then
     Write one for it.
  3. Write the manifest in the editor until it validates, and Save to library.
  4. Install from its card through the approval screen, and generate.
  5. Export, and open the pull request by hand (Q19).
- **Reading a repository over the network.** "From a repository" was never
  pointed at a real remote; M7a's local-git tests are all there is.
- **Import from a real URL.**
- **Pressing Install or Launch on the approval screen.** The flow is unit-tested
  on the daemon and was followed as far as the screen in the browser. No install
  ran.
- **Export's clipboard write, and dropping a file onto the import dialog.** No
  test drives either.
- **The card's checkpoint choice against a real download.** The daemon answers
  `not_fetched` for a weight that is not on disk, and the toast carries that
  sentence. No weight existed in the scratch library.

## Dependencies added

None. `go.mod` is unchanged.

## Files touched outside the milestone's list

M7b's file list was to be confirmed at kickoff and never written. What was built
lives in `web/**` and `test/visual/**`. Everything else is named here for
sign-off:

- **`internal/api/api.go`, `library.go`, `manifests.go`, `approval.go`** and
  their tests. These are the served fields, the schema route, the document for an
  invalid manifest, the supervisor reload and the preview's source. Each serves
  what `api/openapi.yaml` already declares; **the API document was not changed**.
- **`internal/approval/approval.go`**, for the plural.
- **`test/visual/visual_test.go`**, whose fixture server now mounts the daemon.

## Decision-log entries appended

Under "2026-09-16 · M7 library, editor and iris", a section "M7b · judgement
calls while building" with seventeen entries and one open item. The headlines:

- schema/manifest.json is served to the page as a static document at
  `/schema/manifest.json`, not as an API operation.
- the new routes are `#/add`, `#/edit`, `#/edit/{id}` and
  `#/studios/{id}/approve?do=install|retry|launch`.
- 03 §13's approval screen is a route, one column, with Cancel and Install at
  the bottom.
- Override and Edit are one action with two labels; Revert is offered only on a
  Local entry that overrides something, and asks first.
- the checkpoint is chosen on the card as well as on the approval screen.
- the form's controls come from the schema by type; an emptied field sends null;
  a field is marked required only when every ancestor is required.
- the map from pointers to sections.
- on a registry pointer, the form edits the manifest the pointer carries.
- the page creates a missing parent only when it has seen that parent to be
  absent.
- `:validate` and `:edit` return `document` whenever the text parses as YAML,
  valid or not.
- every library write re-resolves the library into the supervisor.
- `GET /studios` serves `provenance`, `selection`, `selectable` and
  `rebuild_needed_reason`; the approval preview takes its source and level from
  the library.
- an entry whose manifest does not validate reads "Manifest invalid".
- a card says an approval has changed only for a studio that is installed.
- the editor, import and approval goldens are drawn by a real daemon mounted in
  the fixture server.
- "the page never parses YAML" is held by two source rules.
- the launcher gives a disabled input or select the treatment helm-css gives a
  disabled button.
- **Open:** an edit rewrites flow collections it did not touch.

## Left undone

- **The machine-bound demo**, above.
- **The Test button**, deliberately: no smoke harness exists (Q16).
- **`hue`** is still not served, so every card's stripe is the accent (M6b's
  finding stands).
- **The weights flake**, offered as its own task.
- **Merging.** Nothing is merged or pushed. Reviews outstanding: M6a, M6b, M7a,
  M8a, and now M7b.
