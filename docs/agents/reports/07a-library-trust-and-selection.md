# M7a — the library, trust and selection — implementation report

## What was built

**The library, the approval gate and the checkpoint selection.** A studio is now
an entry resolved from three sources rather than a manifest the daemon happened
to load; nothing installs, retries or launches without a current approval; and
`{models.selected}` resolves to the checkpoint a user chose.

**M7b — the screens — is not built.** The library operations exist and are
served, but the launcher can only *use* the approval dialog: Add studio, the
editor and the import dialog are reachable over HTTP and nowhere else.

**A separate report, because M7's own is the kickoff.**
`docs/agents/reports/07-library-editor-iris.md` is the report that stopped
before task 1 and recorded the 21 questions. This one covers what was built
against the answers.

## Gate

    $ go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since f8cdc40
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	1.218s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.067s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	0.892s
    ok  	github.com/janishar/helmstudio/internal/approval	0.182s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.158s
    ok  	github.com/janishar/helmstudio/internal/export	0.175s
    ok  	github.com/janishar/helmstudio/internal/install	8.723s
    ok  	github.com/janishar/helmstudio/internal/library	1.001s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.127s
    ok  	github.com/janishar/helmstudio/internal/media	0.097s
    ok  	github.com/janishar/helmstudio/internal/platform	0.168s
    ok  	github.com/janishar/helmstudio/internal/store	0.380s
    ok  	github.com/janishar/helmstudio/internal/supervisor	40.368s
    ok  	github.com/janishar/helmstudio/internal/theme	0.209s
    ok  	github.com/janishar/helmstudio/internal/themelint	0.090s
    ok  	github.com/janishar/helmstudio/internal/timeline	0.088s
    ok  	github.com/janishar/helmstudio/internal/weights	0.797s
    ok  	github.com/janishar/helmstudio/packages/helm-css	0.091s
    ok  	github.com/janishar/helmstudio/packages/helm-ui-sdk	0.095s
    ?   	github.com/janishar/helmstudio/studios	[no test files]
    ok  	github.com/janishar/helmstudio/test/media	2.788s
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.254s
    ok  	github.com/janishar/helmstudio/test/conformance	3.769s
    ok  	github.com/janishar/helmstudio/test/visual	42.547s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

macOS 27 on Apple Silicon, Go 1.27.1, Chrome 152. `internal/library`,
`internal/approval` and `studios` are new.

## What the tests hold

- **First match wins by existence** (`internal/library/library_test.go`). A
  counting store proves a shadowed source is never read, and an invalid winner
  does not fall through. Planting the fall-through fails two assertions.
- **One validator** (`internal/manifest/entry_test.go`). Every file in
  `testdata` and `studios/` gets the same verdict and the same errors from the
  file path and the bytes path. Nothing escapes: `dest` of `../x`, of `.`, and
  absolute; `test.smoke` outside its root; and `..cache/model`, which only looks
  like an escape, passes.
- **Every command is shown** (`internal/approval/approval_test.go`). The test
  that matters walks every property in `schema/manifest.json` and fails on one
  this package has never been told about — it found five on its first run. A
  command field the schema gains and `Commands()` does not read is a command
  that runs without ever being displayed.
- **The capability sentences are diffed against 03 §18**, the way the tokens
  are. Softening `gallery.read_all` to "Can read your gallery" fails it.
- **The gate is enforced** (`internal/api/approval_test.go`). Launch without a
  digest is 409; reading the preview approves nothing; a stale digest is
  `preview_changed`.
- **Import cannot reach where it should not** (`internal/library/fetch_test.go`).
  http, `file://`, credentials in the URL, loopback by name and by address, IPv6
  loopback, the cloud metadata address, a private address, and a host answering
  with one public and one loopback address.

## Design contradictions raised

**One, and it is load-bearing.**

**02 §5 records the approval on the installation; an approval necessarily
precedes the installation.** Schema v7 adds `approved_digest`,
`approved_commit` and `approved_at` to `installations`, and the whole point of
gating `:install` is that approval happens *before* anything is cloned — at
which moment there is no row. `installations.install_state` has no value
meaning "not installed" (`listed` is an API-level state; the CHECK constraint
refuses it), so a placeholder row cannot be written either.

Writing it in two places — a settings row before install, the columns after —
would be the duplication every other decision in this milestone avoids, and the
two would disagree the first time one write succeeded and the other did not.

**What I did:** one home, a `settings` row keyed `approval:<id>`. **v7's three
columns are unused.** This is flagged in `internal/api/approval.go` and left for
the human: either the columns go from the migration and 02 §5, or install copies
the approval onto the row it creates. It is a decision, not something to settle
in passing, and it gets more expensive once the migration ships to anyone.

Four things had also changed *under* the brief between its being written and
its being built. None needed a decision; all four are recorded in
`docs/decisions.md` and at the top of M7a's section in the brief: M8a took
schema v6 first (so M7a is v7), M6b deleted the plain shelf task 8 was written
against, M6b made every launcher operation carry generation metadata, and M6a's
review still has not run.

## Judgement calls

**Building out of order.** Directed by the human. M6a's review has not run, M6b
was built before M7a against the reverse of the specified order, and M7a was
built under both. What it costs: every class name and token the approval dialog
uses is provisional until M6a's review; a finding there moves the dialog and the
goldens with it.

**Listing and reading are separate operations on a `Store`.** Resolution decides
the winner from what each source *has*, then reads exactly one document. A
resolver that read each source until one parsed would look almost identical, and
the difference only shows on the day a user's override has a typo in it — so
the design is the thing that makes "the lower sources are not read" checkable
rather than intended.

**The four `studios/*.yaml` became pointers**, losing M0's PLACEHOLDER SHAPE
headers, and iris dropped `kv`, `assets` and `gallery`: its `go.mod` at this ref
has no helmstudio dependency, so declaring them would ask the user to grant
access nothing reaches for.

**Rename goes through the YAML node editor**, so a duplicate keeps the comments
and key order of what it was duplicated from. The point of Duplicate is to fork
a studio to point at different weights, and arriving at a reformatted file is
not a good start.

**`helm validate` takes either document**, decided by shape rather than filename
— text pasted into the import dialog is whatever the user pasted. `DetectKind`
reads what is *present* rather than what is absent, so a half-written manifest
reports as an invalid manifest: the errors a person needs are the ones for the
document they were trying to write.

**Criteria are scored out of what is checkable**, not out of fifteen. "7 of 15"
reads as a failing grade for a studio that did everything a manifest can do, and
the six needing a smoke harness are not the author's fault.

**Smaller calls:** the approval digest covers what executes and deliberately not
sizes, free disk, the level or the selection; uninstall is not gated, because it
runs nothing of the studio's; `helm dev` gets none of this, because a
development daemon runs one studio its author is sitting in front of; a save
writes through a temporary file in the same directory, so a crash cannot
truncate a user's manifest.

## Two bugs I introduced, and one test that was worthless

Worth more than the features, because none of them would have been found by
reading the diff.

**Reading the preview approved it.** The first version of `getApproval` recorded
the digest when the preview was served. That would have made *fetching the
screen* equivalent to consenting to it, and `:install` with no digest at all
would simply have run — the gate would have been decorative while looking
complete. Found by asking what the test for "install without a digest is
refused" would actually do after a `GET`. There is now a test for exactly that
sequence.

**A test that passed for the wrong reason.** The redirect-bypass test served the
redirect target from a second `httptest` TLS server, whose certificate the
client did not trust. So the fetch failed on TLS before the redirect rule ever
ran — and **planting the bug (removing the rule) did not fail the test**. The
first plant, checking only the first resolved address, was caught; this one was
not, which is the only reason it came to light. The test now uses a transport
that reaches both servers, so what is under test is the rule and nothing else,
and re-planting fails it.

**`HelmPlayer.frame` clobbered the base element** (M6b, fixed there): a video
frame counter overwrote the base class's `frame` node and broke every empty
state. The base's node is renamed and made non-writable, so the next collision
throws at construction rather than at first render.

## Could not verify

- **The machine-bound demo, in full.** None of it ran: no install completed, no
  checkpoint was chosen for a real iris, nothing was generated, nothing was
  relaunched on a second checkpoint. On the Mac: install iris through the
  approval preview with FLUX.2 Klein 4B chosen, generate an image, choose a
  second checkpoint, fetch or link it, relaunch, generate again.
- **`{models.selected}` against a real weight.** The substitution and its
  refusals are unit-tested; no process has ever been spawned with a path that
  came from a selection.
- **Reading a real repository over the network.** Every repository test drives a
  real git against local repositories. Nothing has fetched from GitHub — depth-1
  with `--filter=blob:none` against a server that supports filtering is
  untested, as is a private repository failing rather than prompting.
- **Import from a real URL.** The rules are tested against local TLS servers.
  No real redirect chain, no real DNS.
- **An install that actually runs.** The gate was confirmed live to refuse and
  then to let a clone start; the clone was killed immediately rather than
  allowed to build.

## Dependencies added

None. `go.mod` is unchanged.

## Files touched outside the milestone's list

- **`docs/agents/milestones/07-library-editor-iris.md`** — the brief, which the
  file list does not name. It records the four things that changed under it, and
  that M7a takes v7 where the brief says v6.
- **`internal/store/store_test.go`** — `TestMigrationsFromEmptyCreateSchemaV1`
  pins the schema version and the index list; v7 changes both. The same edit M2,
  M3, M4 and M8 were directed to make.
- **`internal/manifest/manifest_test.go`, `load_test.go`,
  `install_fields_test.go`** — three tests asserted `studios/*.yaml` are
  manifests, which Q4 made false. Each now reads the document it actually is.

Each of the last four is a test I did not write, edited because the contract
changed under it. Named here for sign-off.

## Decision-log entries appended

Under "2026-09-16 · M7 library, editor and iris", a section "M7a · raised at its
start, and task 0" with six entries: the v6→v7 renumber, task 8 becoming the
launcher's Install path, the generation metadata on launcher operations, the
two responses the API document referred to but never defined, `GET /studios`
becoming the library, and the shared `approval` parameter.

**Not yet appended, and owed:** the approval's home in `settings` rather than on
the installation, and the unused v7 columns. That is the contradiction above and
belongs in the log as an open entry once the human has decided it.

## Left undone

- **M7b, the screens.** Library cards, Add studio, the editor, the import
  dialog, and 03 §13's approval screen proper — which replaces the dialog this
  milestone put in `web/approval.js`.
- **The machine-bound demo.**
- **`helm test`, `helm doctor --studio` and `helm studio init`**, which Q16 took
  out of M7 for a milestone the human places.
- **Committing.** Per `implementer.md` nothing is committed — except that, at
  the human's instruction, all of this is already on `main` and pushed.
