# M7 — Library, editor and iris — kickoff report

## What was built

Nothing is built. M7 stopped at kickoff, before task 1, on
`feat/library-editor-iris` (branched from `main` at `cd38a00`), on the 21
questions below. The human answered "recommendations for all" on 2026-09-16.

**Since the answers**, following them:
- **The brief.** The approved expansion is now
  `docs/agents/milestones/07-library-editor-iris.md` (Q1), with the corrections
  under "Judgement calls".
- **The decision log.** `docs/decisions.md` records the 21 answers, the
  defaults, and three new open entries.
- **The order.** M6's brief and the plan's milestone table note the new order
  (Q2).

**M7a has not started, by Q2's answer.** The order is M6a's review, then M7a.
M7a's first task writes the contract, and M6a's review can change conventions
that contract copies.

**Why it stopped at kickoff.**
- **The brief is not written.** `docs/agents/milestones/07-library-editor-iris.md`
  says it is "Scoped, not specified". It has no tasks, no file list and no
  Definition of Done. `implementer.md` says "Touch only the files the milestone
  names", and this milestone names none.
- **The brief's own preconditions do not hold.** It is to be expanded "when the
  two things a good brief needs both exist: the contract it builds against, and
  the previous milestone's review findings". No review of M6a is recorded, and
  M6b is not built (Q2).
- **Several answers are not the implementer's.** `docs/plan/03-delegation.md`
  never delegates "security posture — tokens, capabilities, Host and Origin
  checks, what the approval screen shows". Q10–Q14 are that.

Each question quotes both sides and gives a recommendation. Replying
"recommendations for all" is enough to go ahead. The defaults at the end are
decisions I will make myself unless told otherwise. The proposed expansion at
the end is written to go into the milestone document once the answers are in.

Section references: 01 PRD, 02 data model, 03 design system, 05 SDK and custom
studios, 06 storage, 07 platform services. "Open: …" means an entry under
"Open, not yet decided" in `docs/decisions.md`.

## Gate

    $ go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since cd38a00
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	7.612s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	4.021s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	2.168s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.673s
    ok  	github.com/janishar/helmstudio/internal/install	16.692s
    ok  	github.com/janishar/helmstudio/internal/manifest	3.647s
    ok  	github.com/janishar/helmstudio/internal/media	5.202s
    ok  	github.com/janishar/helmstudio/internal/platform	4.871s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	5.815s
    ok  	github.com/janishar/helmstudio/internal/supervisor	46.319s
    ok  	github.com/janishar/helmstudio/internal/theme	3.240s
    ok  	github.com/janishar/helmstudio/internal/themelint	4.334s
    ok  	github.com/janishar/helmstudio/internal/weights	6.376s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-css	2.676s
    ?   	github.com/janishar/helmstudio/packages/helm-css/cmd/build	[no test files]
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/node	[no test files]
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ?   	github.com/janishar/helmstudio/web	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.704s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	3.409s
    ok  	github.com/janishar/helmstudio/test/visual	11.179s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

Run on the untouched tree, macOS on Apple Silicon only, as decided in M1. Go
1.27.1, git 2.54.0, Chrome at the standard application path.

After the answers were recorded, the gate ran again with a clean test cache, on
the final tree. That tree changes only documents. The result was the same:
`gate: green`, with `deps: go.mod unchanged since cd38a00`.

## Design contradictions raised

Each is quoted in full in the question named. I stopped on all of them, and none
is resolved in code. The human's answers settle each one. The design documents
change in M7a's task 0.

| # | One side | The other side | See |
|---|---|---|---|
| 1 | R1: the registry holds "pointers (`id`, `repo`, `ref`, certification level)"; 05 §5a's pointer says `certified: verified` | 05 §9: a studio in the registry is level **Registry**, "verified in CI on every release"; the schema has no `certified` | Q4, Q15 |
| 2 | 02 §4: `installations.selected_weights` is "how iris studio remembers which checkpoint to launch with" | 02 §4 DDL and schema v3: `studio_model_bindings.selected` holds the same fact | Q20 |
| 3 | 05 §8: the approval screen comes "after fetching the manifest and before executing anything" | 05 §8 and 03 §13 list "Smoke test passed — health 6.1s" on that screen | Q13 |
| 4 | 05 §5a: "Authoring a manifest is not the risk — you wrote it" | R3c: an import "adds the entry to the library as Local" | Q10 |
| 5 | R2: "an invalid one is not listed" | R2a, R3a: a local file wins, first match | Q6 |
| 6 | 05 §8's approval buttons: "Cancel · Run checks" | 03 §13's: "Cancel · Install anyway" | Q13 |
| 7 | R62: "**every build command** verbatim" | 05 §10: "Show the commands before running them. Verbatim"; the schema also runs `processes[].cmd`, `health.exec` and `import.run` | Q11 |
| 8 | 05 §9 criterion 4: "At least one `main` process" | `helm validate` rule 3: exactly one (Open, M0) | Q16 |

## Judgement calls

No code was written. Creating `feat/library-editor-iris` from `main` was the
human's instruction. Recording the answers needed the calls below, each for the
human to confirm or overturn.

**Corrections to answers.** Each is also marked in `docs/decisions.md`.
- **Q9, Revert's directory.** The kickoff wrote `<data>/studios/reverted/`.
  `reverted` is a valid studio id, so that directory would collide with the
  studio's own `<data>/studios/reverted/`. It is now `_reverted/`, since no id
  can start with an underscore.
- **Q14, `dest`.** The kickoff said `dest` "stays under" the models root. A
  `dest` of `.` passes rule 7's check and is the models root itself. It must
  resolve strictly under it.
- **Q16, criterion 2.** The kickoff said "the five fields". The check is
  `requires.tools`, `requires.ram_gb`, `requires.disk_gb` and `peak_ram_gb`;
  the schema already requires `os` and `arch`.
- **Q21 and the defaults, the approval digest.** The defaults put "the weights
  and the selection" in the digest. Then every checkpoint switch would ask for
  approval again, against Q21 ("Switching checkpoints is stop, choose, launch")
  and Q3's demo. The selection is now shown on the preview but not covered by
  the digest, like sizes.

**Where the brief says more than the draft did:**
- **Task 0's amendments** also name 01 R3 and R64, 03 §5 and 05 §11. Each still
  says what an answer changed:
  - R3's editor "runs the smoke harness"
  - R64 shows all four levels
  - 03 §5 gives "the approval harness's job" to M7
  - 05 §11's add flow "Needs" `helm test`
- **The file list** adds `test/conformance/**` and `test/visual/**`, only where
  an existing test must send an approval. The kickoff's notes said those tests
  would change, but its draft file list left them out.
- **The DoD** tests answers the draft's DoD did not:
  - no outbound connection at startup (Q7)
  - a `dest` of `.`, and an import URL that redirects (Q14)
  - nothing cloned without an approval (Q10)
  - a selection changed while stopped needing no approval (Q21)
- **The schema-walk item** is worded so it can be built: every string property
  is either shown on the preview or classified as running nothing.
- **The review focus** adds one planted bug: the selection in the digest.
- **M7a's reads** add M6a's review findings. M7a's opening says a finding that
  changes a convention the brief copies wins, and is raised at M7a's start.
- **M7b** gains a draft DoD in M6b's form, confirmed at its kickoff.

## Could not verify

Nothing was attempted. The machine-bound work M7 will need is listed under
"Notes".

## Dependencies added

None.

## Files touched outside the milestone's list

The kickoff had no file list. Besides this report:
- **`docs/agents/milestones/07-library-editor-iris.md`.** Replaced with the
  approved expansion (Q1).
- **`docs/decisions.md`.** M7's kickoff section, and three open entries.
- **`docs/agents/milestones/06-design-and-ui.md`.** One amendment note under
  M6b: it is built after a reviewed M7a (Q2). Without the note, M6b's brief
  still says only "Built on a reviewed M6a", and an implementer pointed at M6b
  after the review would skip M7a.
- **`docs/plan/02-milestones.md`.** A note on M7's row: the split, the order,
  and the harness moving out (Q2, Q16). M6's row carries M6's note in the same
  way.

## Decision-log entries appended

In `docs/decisions.md`:
- **A section "2026-09-16 · M7 library, editor and iris"** with:
  - one entry per kickoff answer, Q1–Q21, four of them carrying a correction
    made while recording
  - the kickoff defaults
- **Under "Open, not yet decided":** three "Raised at M7 kickoff" entries:
  - the smoke harness, `helm doctor --studio` and `helm studio init` belong to
    no milestone
  - `weights[]` has no licence field
  - `peak_ram_gb` is one figure for every selectable checkpoint

`git diff main -- docs/decisions.md` shows only added lines.

## Left undone

- **M6a's review**, which comes first (Q2). The human starts it, against `main`:
  M6a is `37f6b5c..cd38a00`.
- **All of M7a**, which starts once that review's findings are addressed.
- **M6b, then M7b**, after M7a.
- **Placing a milestone** for the smoke harness, `helm doctor --studio` and
  `helm studio init` (Q16).
- **Committing.** Per `implementer.md`, the changes are uncommitted on
  `feat/library-editor-iris`.

---

## The kickoff

The human answered "recommendations for all" on 2026-09-16. The questions follow
as they were asked. The corrections made while recording the answers are under
"Judgement calls".

### A · The brief itself

#### Q1. Who writes M7's contract

This is the same question as M5 Q1 and M6 Q1. The recorded answer is "M5's
contract is the expansion drafted at kickoff and approved by the human".

**Recommendation:** the same as M5 and M6.
- The proposed expansion at the end is a draft.
- The human edits it into the milestone document.
- Implementation starts after that.

#### Q2. The brief's own preconditions are not met

- **M7's brief:** it is expanded "when the two things a good brief needs both
  exist: the contract it builds against, and the previous milestone's review
  findings".
- **M6's brief:** "**M6b** is built on a reviewed M6a."
- **M6's report:** "M6b (components and screens) waits for M6a's review."
- **The decision log** lists M6a's judgement calls as "each for the reviewer to
  confirm or overturn". Unlike M4 and M5, it records no review entries for M6.
- **The tree:** `packages/helm-ui-sdk/` does not exist, and `web/` is still the
  plain shelf.
- **M6 Q2** gave M7 "Adding a studio and the manifest editor (§13, §13a)". Those
  are screens. They need M6b's shell, M6b's catalogue (the library *is* the
  catalogue, R3b) and M6b's generated launcher browser client (M6 Q12).

So M7's screens have nothing to be built on. M7's launcher API would also be
designed before M6a's review settles the conventions it copies: the Origin
exemption, the launcher tags, the provisional class names.

Most of M7 is not a screen, though:
- where a manifest comes from, and which one wins
- the registry entry's shape
- the approval rule and its enforcement
- selectable weights
- validate, import and export as launcher operations

**Recommendation:**
1. **Review M6a first.** M7's launcher operations follow M6a's API conventions,
   and a finding there is cheaper before M7 copies it.
2. **Split M7 the way M6 was split**, with two briefs reviewed separately.
   - **M7a, the library, trust and selection.** Contracts, daemon, API and CLI,
     plus the plain-shelf controls its demo needs, as M6a added a theme
     control.
   - **M7b, the screens.** Add studio, the editor, import and export, and the
     approval screen. Its file list and DoD are confirmed at its own kickoff.
3. **Order:** M6a's review, then M7a, then M6b, then M7b. M6b then builds the
   catalogue on the library's fields and Install on a real approval step.
   M7b adds what is M7's.
   - **Cost:** M6b's Install shows M7a's approval preview as a plain dialog
     until M7b draws §13.
   - **The alternative** (M6b before M7a) costs more: M7a would change
     `:install`, `:launch` and `/studios` under M6b's finished screens, and
     would have to edit them.

This reorders the plan, so it is the human's call.

#### Q3. What "iris" means in M7, and the two demos

- **The name.** The milestone is "Library, editor and iris", and its Shape
  never mentions iris. Build plan phase 6: "iris: confirm the assigned port; add
  health (blocks phase 6)".
- **Why iris.** iris is the only studio with `selectable` weights.
  `internal/supervisor/plan.go` refuses `{models.selected}`: it "needs a
  recorded weight selection, which lands with the library milestone". So iris
  cannot launch today.
- **Port and health.** `studios/iris-studio.yaml` already passes
  `--port {port}` and probes over tcp. iris's `main.go` takes one `--model`
  directory and has no health endpoint, so tcp is the probe.
- **Capabilities.** iris declares `capabilities: [kv, assets, gallery]`, but its
  code has no helmstudio dependency at all (`go.mod` at `b4f857f`).
  - Criterion 9 asks for "the minimum capabilities it uses, and no more".
  - Q12 would put three sentences on its approval screen for access it never
    uses.
- **The demo.** "Wrap a repository that ships no manifest" needs the editor,
  which is M7b under Q2. None of the four studio repositories ships
  `helmstudio.yaml`. All four already have registry entries, though, so none is
  "a repo that never heard of helmstudio" (build plan).

**Recommendation:**
- **iris in M7.** iris installs with one chosen checkpoint, launches with
  `{models.selected}`, and relaunches on another, all through the library.
  - iris adopting the runtime SDK is not in M7, as M5 Q16 decided for ltx and
    AuK.
  - iris's registry entry drops the three capabilities its code does not use.
- **M7a's machine-bound demo.** Install iris from its registry entry through
  the approval preview, with one checkpoint. Generate an image, choose another
  checkpoint, relaunch, and generate again.
- **M7b's machine-bound demo.** The milestone's, on a public repository the
  human picks, with no registry entry and no `helmstudio.yaml`. Write its
  manifest in the editor, install it through the approval screen, generate,
  and export the file.

---

### B · The library: where a manifest comes from

#### Q4. What a registry entry is (Open: "`studios/*.yaml`: bare pointer … vs the full manifest")

- **R1:** "The bundled registry holds **pointers** (`id`, `repo`, `ref`,
  certification level), and the daemon resolves and caches the manifest from
  the repo at that ref."
- **05 §5a's example pointer:** `id`, `repo`, `ref` and `certified: verified`.
- **R2a:** the third source is "an inline manifest carried by the registry
  pointer for repos that have none".
- **`schema/manifest.json`** has no pointer shape and no `certified`. M0 wrote
  full manifests as a placeholder, each headed "PLACEHOLDER SHAPE".
- **The repositories.** None of the four ships `helmstudio.yaml`, so every
  registry entry needs the inline form today.
- **The gate** runs `helm validate studios/*.yaml`.

Nothing says what "carried by the registry pointer" looks like, or what
`certified` holds. Q15 covers why `verified` there contradicts 05 §9.

**Options:**
- **(a) A pointer with an optional inline manifest.** `id`, `repo`, `ref`, and a
  `manifest:` holding a complete manifest for a repository with none. A new
  `schema/registry-entry.json` describes the envelope and refers to
  `manifest.json` for the inline part.
- **(b) Keep full manifests.** A registry entry *is* a manifest with `repo` and
  `ref`, read as source 3. There is no envelope.

**Recommendation: (a).**
- **It is R1 and 05 §5a as written.**
- **Still one copy.** When a repository later ships `helmstudio.yaml`, the pull
  request that moves `ref` shrinks the entry to three lines, and the inline copy
  is gone.
- **The pointer's fields stay outside the manifest.** A new reviewed `ref` does
  not edit the studio's own description.

It comes with these rules:
- **Matching fields.** The inline manifest's `id`, `repo` and `ref` must equal
  the pointer's where set, or the entry is invalid.
- **One validator.** `helm validate` accepts either document: the envelope
  through its own schema, and the inline manifest through the same validator as
  any other manifest.
- **Offline.** A pointer with no inline manifest validates as an envelope only,
  since the gate never touches the network.
- **No `certified` field** (Q15).
- **The PLACEHOLDER SHAPE headers go.**
- **`schema/manifest.json` does not change.**

#### Q5. Where local manifests and fetched manifests live

- **R2a, 05 §5a and the editor mockup (03 §13a):** local manifests are at
  `~/.helmstudio/studios/<id>.yaml`.
- **05 §5a:** fetched manifests are "cached under
  `~/.helmstudio/cache/manifests/<id>@<ref>.yaml`".
- **06 §4:** "The tree below is written as `~/.helmstudio/` for readability,
  but no path is hardcoded."
- **M1** decided five OS-resolved roots and deferred adopting `~/.helmstudio`.
  `make boundaries` rejects a `"~/` literal.
- **R3b:** "a user can hand-edit or version-control their own entries". On macOS
  the data root is `~/Library/Application Support/helmstudio`, which nobody
  finds by browsing.
- **The cache key.** `<id>@<ref>` goes stale when `ref` is a branch.

**Recommendation:**
- **Local manifests: `<data>/studios/<id>.yaml`.**
  - This is the design's own path through M1's mapping. R7's clones already
    live at `<data>/studios/<id>/src`.
  - It is backed up, never purged, and isolated in tests by `HELMSTUDIO_HOME`.
  - The editor shows the real path and offers Reveal.
  - Uninstall never touches the file.
- **Fetched manifests: `<cache>/manifests/<id>@<commit>.yaml`**, keyed by the
  resolved commit.
- **No new root and no new environment variable.**
- **Cost:** it is harder to find than a dot-directory. Reveal is the answer
  until the Mac app.

#### Q6. First match wins, when the first match is invalid

- **R2a:** "first match winning".
- **R3a:** "A local manifest overrides a registry entry with the same `id`".
- **R2:** "Each manifest is validated at load; an invalid one is not listed and
  its field and reason are logged."
- **The review focus:** "Does first-match-wins actually short-circuit, and is
  the chosen source visible to the user?"

Take a typo in a local override `h3-studio.yaml`. There are two readings:
- **Fall through.** The registry manifest is used. The override silently stops
  applying, and the user installs without the flag they added.
- **Not listed.** h3 disappears from the library, because R2 hides invalid
  files.

Neither one short-circuits visibly.

**Recommendation:**
- **An entry is decided by the highest source that exists for its id, valid or
  not.**
  - An invalid winner is listed as invalid, with its source, its file and its
    errors, and with Edit and Revert.
  - It cannot be installed or launched.
  - The lower sources are not read for that id.
- **The same holds between sources 2 and 3.** An invalid `helmstudio.yaml` at
  the pinned ref is not replaced by the inline manifest.
- **R2's rule still holds across ids:** "one bad manifest never blocks the
  others". R2 is amended to list an invalid manifest as invalid rather than hide
  it. A group already running is unaffected.
- **Every entry carries `source`**, and `overrides` when it shadows another.

#### Q7. When helmstudio touches the network, and what an installed studio reads

- **R3b and 05 §5a:** the library "is derived at startup from files — bundled
  pointers, the local directory, and fetched manifests cached on disk".
- **05 §5a, the registry route:** "the manifest is fetched from the repo at the
  reviewed ref."
- **06 §4:** the cache root "may be purged by the OS".
- **01 §3:** "Nothing leaves the machine. The only outbound calls are the ones a
  user action implies".
- **M3 Q18:** "nothing is fetched or built at daemon start".

The design leaves three things unsaid:
- when a registry pointer's manifest is fetched
- what a pointer with nothing cached shows
- what an installed studio reads once macOS has purged the cache and the Mac is
  offline

**Recommendation:**
- **Startup never touches the network.** The library is built from files only.
  - A pointer with nothing to read is listed from the pointer, with "Manifest
    not fetched" and a Fetch action.
  - A pointer with an inline manifest whose repository has not been read
    resolves to the inline manifest, and says so.
- **Fetching is a user action:** Fetch, the approval preview, and Add from a
  repository.
  - Reading the repository can change an entry's source.
  - The approval preview always reads it, so the source it shows is the one
    that will be used.
- **Git only, never a forge's HTTP API.** A depth-1 fetch of the ref without
  file contents, then only `helmstudio.yaml` and `.gitmodules` (Q11). The
  resolved commit is recorded.
- **An installed studio reads its manifest from its own checkout.** For a
  studio whose manifest comes from its repository, the checkout is a copy
  nothing purges. A purged cache or an offline Mac never makes it unlaunchable.
  The cache serves studios that are not installed.

#### Q8. What "From a repository" writes, and where "a note of where it came from" lives

- **05 §5a's routes:** "From a repository — Paste a git URL; the manifest is
  read from `helmstudio.yaml` at the root, or written by hand if the repo has
  none."
- **03 §13a:** "From repo" means "The studio's author ships `helmstudio.yaml`."
- **R3b:** the library is "bundled pointers, the local directory, and fetched
  manifests cached on disk".
- **05 §5a:** "No table, no sync".
- **R3c:** an import is added "as Local with a note of where it came from".
- **R3d:** export is "unchanged".

A repository the user adds is not bundled, and the cache may be purged. The only
durable place is the local directory, which holds complete manifests:
- **Copying the author's manifest there** makes it Local, and the author's next
  change never arrives. That is two copies.
- **A provenance note written into the file** as a comment would be exported
  upstream with it.

**Recommendation:**
- **The local directory may also hold pointers.** A pointer there has the same
  envelope as Q4 with no inline manifest: `<data>/studios/<id>.yaml` holding
  `id`, `repo` and `ref`.
  - A local pointer is shown as From repo.
  - It resolves exactly as a registry pointer does, but it is the user's and
    carries no review.
- **A complete manifest in the local directory is Local** (source 1). Adding a
  repository with no `helmstudio.yaml` opens the editor pre-filled with `repo`
  and `ref`, and saving writes a Local manifest.
- **Provenance lives beside the manifest, not in it.** Notes such as "imported
  from <url> on <date>", "duplicated from h3-studio" and "written in the editor"
  go in `<data>/studios/<id>.source.json`, so export stays byte-identical. It is
  a note for the card, not a trust signal (Q10).

#### Q9. Override, Revert and Duplicate

- **R3a:** a local manifest with a registry id "overrides it; the card says so
  and offers Revert". An import whose `id` exists "offers Override, Rename or
  Cancel". "Duplicate-and-edit copies any entry to local under a new id".
- **R3b:** users "can hand-edit or version-control their own entries".
- **What is missing:** nothing says what Revert does to the file, or to a studio
  installed or running from the override.

A Revert that deletes `<data>/studios/<id>.yaml` destroys something the user
wrote, possibly the only copy.

**Recommendation:**
- **Revert moves the file and never deletes it.** It goes to
  `<data>/studios/reverted/<id>-<timestamp>.yaml`, which is not scanned, and the
  card says where it went.
- **Override and Revert leave an installed or running studio alone.**
  - The running group keeps running.
  - The card shows `rebuild_needed` when the resolved manifest's digest differs
    from the installation's.
  - The next install or launch goes through approval (Q10).
- **Duplicate** writes a Local manifest under a new id the user types, changing
  `id` and nothing else. For an entry resolved from a repository, the resolved
  text is copied.
- **Rename on import** is the same edit of `id`.

---

### C · Trust: the approval screen

#### Q10. When approval is needed, and whether the API enforces it

- **05 §8:** "after fetching the manifest and before executing anything,
  helmstudio shows what it is about to do … That screen is the whole security
  model made visible".
- **05 §5a, on trust:**
  - "Authoring a manifest is not the risk — you wrote it, and you can read every
    command in it."
  - "the approval screen still appears when installing from a repository you do
    not own".
  - "A hand-written manifest pointing at your own checkout needs no ceremony;
    the same manifest pointing at a stranger's repository needs all of it."
- **R3c:** an imported manifest joins "as Local".
- **`api/openapi.yaml`:** `POST /studios/{id}:install` takes no body and no
  parameter, so any client installs without a screen.
- **`internal/install`:** install fetches `ref` as it is at install time, so a
  branch can move between the screen and the clone.
- **Precedent:** M5 Q13 made the switch dialog's confirm a digest, refused as
  `preview_changed`. M3 made reclaim confirm a digest.

This leaves four problems:
1. **Ownership is not testable.** "A repository you do not own" has no test a
   daemon can run.
2. **"You wrote it" is false for Local entries.** Import, paste and Duplicate
   put someone else's commands there. An imported manifest with `local_path`
   needs "no ceremony" by 05's letter.
3. **Launch runs commands too.** `processes[].cmd` runs at launch, not install,
   so an Override of an installed studio changes what launches without any
   install.
4. **Nothing ties what was approved to what runs.**

**Options:**
- **(a) As written.** Approval for any install from a `repo`, since nothing is
  "owned", and none for `local_path`. Problems 2–4 remain.
- **(b) Provenance.** As (a), but a `local_path` manifest is exempt only when it
  was last saved in the editor. The daemon sees only files, and cannot tell the
  editor from any other writer.
- **(c) One rule.** Approval is required before any install, retry or launch
  whose recorded approval does not match. The digest covers everything that
  executes and where it comes from (Q11), plus capabilities and network hosts.
  No source is exempt.

**Recommendation: (c).**
- **Why.** The screen exists for someone else's commands. The library now has
  three routes by which someone else's text becomes a Local entry, and a rule
  that depends on who wrote a file cannot be checked.
- **Your own manifest.** The ceremony is reading your own commands once per
  change. `helm dev` stays the development loop and never asks.
- **The API enforces it, as it enforces the switch dialog:**
  - `GET /studios/{id}/approval` returns the preview and its digest. It reads
    the repository (Q7) and resolves `ref` to a commit.
  - `:install`, `:retry` and `:launch` take `approval=<digest>` whenever the
    installation's recorded approval does not match what would run now. With
    none, the answer is 409 `approval_required` with the preview in `details`.
    With a stale one, it is 409 `preview_changed`.
  - Install checks out exactly the approved commit, so a branch that moved
    needs a new approval.
  - The approval is recorded on the installation (schema v6).
- **Costs:**
  - 05 §5a's "needs no ceremony" is amended.
  - Every studio installed before v6 asks once, at its next launch. Recording
    those as approved would claim a decision nobody made.
  - `helm dev` records its own installation as approved.
- **Honest limit, as in M5.** Until M9's cookie, any local process can fetch a
  preview and send its digest. The digest proves the screen was current, not
  that a person read it.

#### Q11. "Every build command" is not every command

- **R62:** "**every build command verbatim**".
- **05 §10:** "Show the commands before running them. Verbatim".
- **05 §8's mockup:** "Will run these commands on your Mac — uv sync
  --all-extras · bash scripts/build_metal.sh · uv run python -m wan.setup".
- **What the schema runs besides `build[].run`:**
  - `processes[].cmd` (and `run.cmd`), at every launch
  - `health.exec`, while running
  - `import.run`, "run on first launch under helmstudio"
  - `env`, which changes what those commands do (`PATH`, `DYLD_…`)
- **Submodules.** `submodules: true` brings in other repositories whose code the
  build compiles. The commit pins them, but at URLs the user never sees.

A manifest with harmless build steps and a `cmd` of `./server; curl … | sh`
passes a screen that shows only build commands.

**Recommendation:** the screen shows everything verbatim, grouped by when it
runs.
- **At install:**
  - every `build[]` step, with its `cwd` and `shell`
  - for a `python` studio, that helmstudio makes a uv environment for that
    Python
- **At every launch:**
  - every process `cmd`, with its `cwd`, `shell` and `env`, placeholders left as
    written (`{models.selected}`, not a path)
  - every `health.exec`
- **Once, on first launch:** `import.run` with its `cwd`. Nothing runs it yet,
  but it is declared.
- **Also fetched:** the repository URL and commit, and each submodule's URL and
  path, read from `.gitmodules` at that commit.
- **Verbatim means byte for byte.**
  - Control characters and newlines are shown escaped and flagged.
  - Zero-width and bidirectional characters are flagged.
  - The DoD tests a manifest that has them.

#### Q12. Capabilities as sentences

- **R62:** "the capabilities requested as plain sentences".
- **03 §13:** "The capability line is written as a sentence, because
  'gallery.read_all' tells a person nothing."
- **The design writes one sentence** (03 §18): "This studio asks to read
  everything you have ever made, in every studio. It needs that to offer your
  past renders as references." Its second half is one studio's reason, and the
  manifest has nowhere to state a reason.
- **The schema has nine capabilities**, and "No capabilities means no token is
  issued at all".

`03-delegation.md` never delegates what this screen shows, so the wording is
yours.

**Recommendation.** Proposed wording, to edit freely:

| Capability | Sentence |
|---|---|
| none | Uses no helmstudio services. It gets no access token. |
| `kv` | Saves its own settings and sessions in helmstudio. |
| `records` | Keeps its own records, such as a list of takes, in helmstudio. |
| `assets` | Stores the files it makes in your helmstudio library. |
| `gallery` | Adds what it makes to your gallery, and receives items other studios send it. |
| `jobs` | Reports its long-running work to helmstudio. |
| `timeline` | Can create sequences on your timeline. |
| `gallery.read_all` | Can read everything you have ever made, in every studio. |
| `kv.shared` | Can read and change settings shared by every studio. |
| `handoff.send` | Can send items to other studios. |

- **One source for the copy.** The table goes into 03 §18. One Go table in
  `internal/manifest` serves the preview, and a test diffs the two, as the token
  table is diffed.
- **No reasons.** "It needs that to …" is dropped. A manifest field for a reason
  would be a schema change nobody has asked for.
- **Warnings.** `gallery.read_all` and `kv.shared` are marked as warnings, as
  05 §8's mockup marks `gallery.read_all`.

#### Q13. Checks that cannot run before anything executes

- **05 §8:** the screen comes "before executing anything", and "Checks run
  before install, not after."
- **R63:** "Checks run before install: manifest validity, host requirements,
  capability review, theme conformance and the smoke harness."
- **The smoke test.** 05 §8's mockup shows "Smoke test passed — health 6.1s · 1
  asset · 1 item · clean exit · no stray writes". A smoke test builds the studio
  and runs it, which is exactly what the screen exists to ask permission for.
- **Theme conformance** lints the studio's stylesheets, which are not on disk
  until the clone.
- **The buttons disagree.** 05 §8 has "Cancel · Run checks". 03 §13 has
  "Cancel · Install anyway".
- **The harness does not exist.** No milestone brief builds `helm test`, the
  smoke harness of R58 (Q16).
- **Open (M5):** "whether a manifest's `network` must list the hosts its build
  reaches". The screen shows "the declared network hosts", and nothing enforces
  them.

**Recommendation:**
- **Before approval, only checks that execute nothing from the studio:**
  - the schema and the rules
  - host requirements: the blocks and warnings install applies today
  - the commands (Q11) and the capabilities (Q12)
  - the declared hosts
  - the weights, with sizes and whether each is already downloaded or linked
- **Theme conformance and the smoke test** are listed as "Not run before
  install", with the reason, never as passes. The smoke test waits for the
  harness (Q16).
- **Buttons: "Cancel" and "Install".** A failed required check makes it "Install
  anyway", as in 03 §13. There is no "Run checks".
- **Hosts** are shown as: "The manifest says it contacts these hosts. helmstudio
  does not restrict network access." The M5 entry stays open.
- **05 §8, 03 §13 and R63 are amended.**

#### Q14. Can an imported manifest reach outside its roots?

The review focus asks this directly. Today's validator covers some fields and
not others:
- **`build[].cwd`, `processes[].cwd`, `import.cwd`:** must not escape the root.
  The check is lexical (M0 rule 7). M2 also refuses, at launch, a process `cwd`
  that resolves through a symlink outside the checkout.
- **`weights[].dest`:** nothing. The schema says "Directory name under the
  shared models root". `dest: ../../Documents` passes `helm validate`; M3's
  `os.Root` refuses it only at download or link time.
- **`test.smoke`:** nothing. The schema says "relative to the studio root".
- **`local_path`:** any absolute directory, by design. The schema says
  "Development only — a registry manifest never sets this". An imported
  manifest with `local_path: /Users/you` builds in your home directory, without
  cloning anything.
- **`repo`:** any URI. `file:///…` clones a directory on this Mac. git's own
  policy refuses `ext::`. M3's tests use `file://` remotes.

Two import routes raise their own issues:
- **Import from a URL** makes the daemon fetch an address chosen by a request
  that any local process can send. That includes loopback services, the LAN and
  `file://`.
- **"From folder"** (05 §8) needs an absolute path, and a browser folder picker
  never gives a page one. The design's only folder picker is the Mac app's
  (R70, 03 §14).

**Recommendation:**
- **Two new validator rules**, so `helm validate`, the editor and import refuse
  the same manifests:
  - `dest` stays under the models root after cleaning, the same lexical check as
    rule 7.
  - `test.smoke` stays under the studio root.

  Each changes what `helm validate` accepts, so each gets its own decision
  entry, as M0's added rules did. None of the four manifests is affected.
- **`local_path` in an import or Duplicate is kept but flagged.** The import
  report says it "builds in /path on this Mac, without cloning". Approval covers
  it under Q10 (c).
- **The validator does not restrict `repo` schemes.** `file://` is how M3's
  tests and local development work. The screen names the transport: "Clones a
  directory on this Mac: /path".
- **Import from a URL:**
  - `https://` only, with no redirect to anything else
  - refused when the host resolves to a loopback, link-local or private address
  - at most 1 MiB, with a 10 s timeout and no cookies or credentials
  - the bytes are parsed as a manifest, and never echoed back as text when they
    are not one
- **Files, drops and paste are read by the page and sent as text.** The daemon
  never opens a path the page names, with one exception: "From folder" takes a
  typed absolute path and reads only `<path>/helmstudio.yaml`.
- **No directory-listing endpoint.** Another account on this Mac can reach
  loopback (Open, M2 review).

---

### D · Certification and the criteria

#### Q15. What a certification level is, and where it comes from

- **05 §9 defines four levels:**
  - **Draft:** "A local folder. No checks enforced; the card says so. This is
    where every studio starts."
  - **Unverified:** "From a repository, manifest valid, but checks not all
    passing or not run on a clean machine."
  - **Verified:** "All required criteria pass, and the smoke harness ran from a
    clean state on this machine."
  - **Registry:** "Merged into the helmstudio catalogue; verified in CI on every
    release."
- **R1 puts a "certification level" in the pointer**, and 05 §5a's pointer says
  `certified: verified`. That is a registry entry whose level is not Registry.
- **R64 and 03 §13a:** source, level and install state are "three independent
  facts".
- **Nothing can earn the top two levels today.** There is no CI (M1, by the
  human's direction) and no smoke harness (Q16). No studio can honestly be
  Verified or Registry, including the four in the registry.

**Recommendation:**
- **The level is derived, never declared.** There is no `certified` field (Q4).
- **In M7 it takes two values:** Draft for a manifest with `local_path`, and
  Unverified for everything else.
- **Verified and Registry are defined but not shown** until the harness exists
  and its runs are recorded. The four registry studios show source Registry and
  level Unverified, which is true.
- **R1, 05 §5a's example and 05 §9 get notes** saying so.

#### Q16. Which criteria M7 scores, and the harness

- **Scope.** The milestone's Shape lists neither the criteria nor the harness.
  `03-delegation.md` lists phase 6 as "editor, import and export, criteria,
  approval".
- **R3:** the editor "shows the certification criteria updating, runs the smoke
  harness without installing". 03 §13a's mockup shows "13 of 15 criteria" and a
  Test button.
- **What 05 §9 checks them with:**
  - criteria 3, 6, 7, 10, 11 and 14: a smoke run, with a filesystem watch (6)
    and a proxy (10)
  - criterion 9: a "Static check against SDK calls" in the studio's source
  - criterion 12: the repository's stylesheets
- **Unassigned tools.** `helm test` (R58), `helm doctor --studio` (R6c) and
  `helm studio init` (R68) are in no milestone brief. CLAUDE.md lists `test` and
  `doctor` among `helm`'s commands.
- **Criterion 2** requires `requires.tools`, `ram_gb`, `disk_gb` and
  `peak_ram_gb`. The schema requires only `os` and `arch`.
- **Criterion 8** wants "any weight licences it accepts on the user's behalf".
  `weights[]` has no licence field.
- **Open (M0):** rule 3, "exactly one process has role: main", against
  criterion 4, "at least one `main` process".

**Recommendation:**
- **M7 scores only what the manifest alone can answer:**
  - **1:** valid, with a well-formed `id`
  - **2:** the five fields are declared
  - **4:** exactly one `main`, with a health probe; "realistic" is not judged
  - **5:** no `port.fixed`, and `{port}` in the command of each process that
    declares a port
  - **8:** `license` is declared; weight licences are not checkable
  - **13:** `test.profile` is declared
  - **15:** `sdk` and `ref` are declared; whether a ref is a branch is not
    decided offline
- **Every other criterion is listed as not checked, with the reason:**
  - 3, 6, 7, 10, 11 and 14: "needs the smoke harness"
  - 9: "needs the studio's source"
  - 12: "needs the studio's stylesheets"

  The count reads "7 of 7 checkable pass", never "13 of 15".
- **The criteria live in `internal/manifest`**, beside the validator, so
  `helm validate -criteria`, the editor and the approval preview share one
  implementation.
- **Criterion 4 is amended to "exactly one".** That is what the validator
  enforces, and what `role: main`'s own description implies ("main owns the UI
  and the Launch button"). This resolves the M0 entry.
- **The harness, `helm doctor --studio` and `helm studio init` get a milestone
  of their own**, which the human places. The editor has no Test button until
  then.

---

### E · The editor, import and export

#### Q17. One validator: where YAML is parsed

- **The review focus:** "Does the editor produce manifests that `helm validate`
  accepts — the same validator, not a second implementation?"
- **05 §5a:** "Validation runs as you type — the same `helm validate` the
  registry gates on".
- **03 §13a:** "Form on the left for the fields, YAML on the right … both live
  and both editable".
- **M6's defaults:** "no bundler and no npm dependencies — hand-written ES
  modules".
- **The code:** `manifest.Validate` takes a file path.

Both obvious designs create a second implementation:
- **A form that reads YAML in the browser** needs a YAML parser in JavaScript.
  Wherever it disagrees with `yaml.v3` (anchors, merge keys, `on` and `yes`,
  duplicate keys, number forms), the user sees one manifest and the validator
  checks another.
- **A form hand-written over the schema's fields** is a second copy of the
  schema, which drifts when the schema changes.

**Recommendation:**
- **The page never parses YAML.** It sends text to
  `POST /launcher/manifests:validate` and gets back:
  - the errors, with lines and pointers
  - the criteria (Q16)
  - the document as JSON, for the form
- **A form edit is sent as a JSON pointer and a value.** The daemon applies it
  to the YAML node tree, keeping comments and key order, and returns the new
  text.
- **`internal/manifest` gains a bytes entry point**, which `Validate(file)`
  itself calls. The CLI, the daemon's loader, the editor, import and save then
  share one path.
  - A test runs every file in `internal/manifest/testdata` and `studios/`
    through `helm validate` and through the endpoint.
  - It requires the same verdict and the same errors from both.
- **The form is generated from `schema/manifest.json`**, which is served to the
  page, plus a small hand-written map from pointers to 03 §13a's sections. A
  test fails when a schema property is neither in a section nor sent to the
  YAML pane.
- **Typing is debounced**, and every round trip stays on loopback.

#### Q18. The launcher operations

M4 Q1 removed `POST /studios` and `/studios/{id}/models:select` "to return with
the milestone that builds it". API shape is never delegated, so these are
proposed for approval. All are tagged `launcher` and all fall under M2's Host
and Origin rules.

| Operation | Does |
|---|---|
| `GET /studios`, extended | The library: every id from the three sources, with `source`, `overrides`, `level`, `manifest_valid`, `errors` and `provenance`. Installed studios are a subset, as R3b says. |
| `GET /studios/{id}/manifest` | The resolved text, with its digest, source, file and resolved commit. Export reads this. |
| `POST /launcher/manifests:validate` | Text in; errors, criteria and the JSON document out. Writes nothing. |
| `POST /launcher/manifests:edit` | Text, a pointer and a value in; new text out (Q17). Writes nothing. |
| `PUT /launcher/manifests/{id}` | Saves a Local manifest or pointer. Refuses an invalid one (422 with the errors). `If-Match` on the file's digest, so two tabs cannot overwrite each other. |
| `DELETE /launcher/manifests/{id}` | Revert (Q9). |
| `POST /launcher/manifests/{id}:duplicate` | `{new_id}` (Q9). |
| `POST /launcher/manifests:import` | Items of `{text}` or `{url}` in; a report per item (new, collision or invalid) and a `confirm` digest out. With `{confirm, resolutions}`, one of override, rename or skip per id, it adds to the library. It installs nothing. |
| `POST /launcher/repositories:read` | `{repo, ref}` or `{path}` in; what the repository or folder holds at that ref, with the resolved commit (Q7, Q8, Q14). Writes nothing. |
| `GET /studios/{id}/approval` | The approval preview and its digest (Q10–Q13). |
| `POST /studios/{id}:install`, `:retry`, `:launch` | As today, plus `approval=` (Q10). |
| `PUT /studios/{id}/selection` | `{weight}` (Q20, Q21). |

**Cost, recorded rather than solved.** The API has no authentication until M9
(Open, M2 review), so every write above is reachable by any local process,
including another account on this Mac.
- **Such a process can already** install and launch.
- **What is new:** it can put an Override into the library. Q10 (c) makes the
  user's next install or launch of that studio show the changed commands
  instead of running them.
- **What stays out of scope until M9:** a process that confirms the approval
  itself.

#### Q19. Export, and "open as a pull request"

- **R3d:** a library entry can be "written out as a file or copied as text,
  unchanged and ready to commit upstream, attach to an issue, or send to
  someone else".
- **05 §5a:** "**Export** hands back the file to commit to the repo or open as
  a pull request."
- **Build plan phase 6's demo:** "export the manifest as a pull request".
- **01 §14:** "No account, sign-in or settings sync". There is no GitHub token
  and no place to keep one.
- **The schema's `local_path`:** "a registry manifest never sets this". A
  manifest written for your own checkout carries your absolute path.

**Recommendation:**
- **Export is a file download and a copy**, byte-identical to the stored text.
  For an inline registry manifest, the inline document is written out on its
  own.
- **helmstudio does not open pull requests.** No GitHub API and no token: the
  person commits the file, and the build plan demo's pull request is opened by
  hand.
- **An exported manifest with `local_path`** gets a warning beside the button,
  and the file is not changed.

---

### F · Selectable weights

#### Q20. One selection, stored once

- **02 §4, `installations`:** "`selected_weights` — Placeholder → artifact id for
  `selectable` weights. This is how iris studio remembers which checkpoint to
  launch with." Schema v3 has the column.
- **02 §4's DDL, `studio_model_bindings`:** `selected INTEGER NOT NULL DEFAULT 0`.
  Schema v3 has that column too.
- **The schema:** `{models.selected}` is one placeholder, and a selectable
  weight is "One of a set the user picks between at launch … iris studio takes
  exactly one checkpoint per run."
- **M3 Q5:** one artifact can back two weights, so an artifact id cannot say
  which weight was chosen.

Two places hold one fact, and one of them holds it in a form that cannot name
the weight.

**Recommendation:**
- **The binding's `selected` flag is the selection.** Exactly one selectable
  binding per studio is set, enforced by a partial unique index in schema v6.
- **`installations.selected_weights` goes unused**, and 02 says so. The column
  stays: rebuilding `installations` to drop it costs more than it saves.
- **The flag names the weight**, since a binding keeps its placeholder.
  Uninstall's cascade clears it.

#### Q21. What install downloads, and when the choice is made

- **M3 Q6:** "install downloads every weight not marked `optional`,
  `selectable` ones included."
- **Open (M3 review):** "iris's install downloads all five `selectable`
  checkpoints … each a whole repository because none declares `files`. FLUX.2
  Klein alone is about 16 GB … Put to the human; not decided."
- **01 §13:** "the shelf asks which to launch with and records the choice on the
  installation."
- **The schema:** "One of a set the user picks between at launch".

**Recommendation:**
- **The choice is made before install.**
  - It is on the approval preview, defaulting to the first `selectable` weight
    declared.
  - Install downloads only that one.
  - Other selectable weights are downloaded or linked on demand, with the
    existing `:fetch` and `:link`.
  - M3 Q6 is amended for `selectable`.
- **The choice can change** with `PUT /studios/{id}/selection` while the studio
  is not running.
  - Choosing a weight that is not downloaded is 409 `not_fetched`, naming
    `:fetch` and `:link`. The shelf offers both.
  - While running, it is 409 `conflict`. Switching checkpoints is stop, choose,
    launch.
- **A launch with no selection** (an installation from before v6) is 422
  `selection_required`, listing the choices.
- **iris's heavy arithmetic** stays one `peak_ram_gb` for all five checkpoints.
  The schema has no per-weight figure. This is noted, not changed.

---

### Defaults I will take unless told otherwise

- **The registry is compiled in.** `studios/*.yaml` is embedded in the daemon
  through `studios/studios.go`, as `schema/schema.go` embeds the schema. R72's
  "bundled registry" then ships with the binary. `-studios` stays as a
  development override.
- **A local file is named for its id.** `<data>/studios/foo.yaml` must declare
  `id: foo`, or it is listed invalid, naming both.
- **The library is re-derived** after every write through the API, and on a
  Rescan action. There is no file watching: the standard library has none.
- **Unsaved editor text** stays in the page (`sessionStorage`), since Save
  refuses an invalid manifest.
- **Tests:**
  - Git remotes are local bare repositories, as in M3's tests.
  - A counting fake fetcher proves resolution never reaches source 2 when
    source 1 exists.
  - URL import's address checks run against an injected resolver.
  - Nothing touches a models directory or the user's roots.
- **What the approval digest covers:**
  - the studio id, source and resolved commit, and `submodules`
  - every command field of Q11, with its `cwd`, `shell` and `env`
  - capabilities, network hosts and `python.version`
  - the weights and the selection

  Free disk and sizes are shown but not covered, because they move.

### Notes, no decision needed

- **Open entries this kickoff would resolve:**
  - the pointer against the full manifest (Q4)
  - criterion 4 against rule 3 (Q16)
  - iris's five checkpoints (Q21)
- **Open entries it leans on without resolving:**
  - no API authentication until M9 (Q18)
  - no manifest field for a secret a studio needs, such as `HF_TOKEN`, so the
    approval screen cannot show one (M2 review)
  - which hosts `network` must list (Q13)
  - `heavy` on a thin wrapper, which iris is
- **A forward reference that moves with the harness.** M6's amendment to 03 §5
  says "Rendering a studio's own stylesheet for contrast is the approval
  harness's job (M7)". Under Q16 it goes with the harness.
- **Schema gaps, noted but not proposed:**
  - no weight licence (criterion 8)
  - no per-weight peak memory (iris's 4B and 9B checkpoints share 30 GB)
  - no reason field for a capability (Q12)
- **Wording, not a conflict.** 03 §13a's import mockup offers "Override, Rename
  or Skip" per file, and Cancel for the dialog. R3a's "Override, Rename or
  Cancel" is the same set.
- **Tests Q10 would change.** M3's install tests and the launch tests of M2, M4,
  M5 and M6 (including `test/conformance` and `test/visual`) call `:install` and
  `:launch` without an approval. They would be edited to send one and named for
  sign-off, as M4, M5 and M6 did.
- **iris has no `LICENSE` at its root** (M0's report), which criterion 8 will
  show.
- **Machine-bound work:**
  - **M7a:** a real iris install with one checkpoint (FLUX.2 Klein 4B), its
    Metal build, an image, then a second checkpoint chosen, a relaunch and
    another image
  - **M7b:** a real repository wrapped in the editor, installed and generated
    with

  Neither can run in the gate.

---

### Proposed expansion (draft, for Q1)

Written for "recommendations for all". If Q2 is answered otherwise, the tasks
keep this order in one brief.

#### M7a — the library, trust and selection

##### Reads

- `docs/design/01-prd.md`: R1–R3d, R5, R14, R61–R67, §13
- `docs/design/02-data-model.md` §1, §4, §7
- `docs/design/03-design-system.md` §13, §13a, §18
- `docs/design/05-sdk-and-custom-studios.md` §5a, §8–§10
- `docs/design/06-storage.md` §4, `docs/design/07-platform-services.md` §3
- `schema/manifest.json`, and the launcher operations in `api/openapi.yaml`
- `docs/decisions.md` in full, and the reports of M0, M3 and M6
- iris studio at `b4f857f`

##### Tasks

0. **The contract, before any code.**
   - Amend 01, 02, 03 §13 and §18, 05 §5a, §8 and §9, and the schema's
     descriptions, per the answers.
   - Add `schema/registry-entry.json` (Q4).
   - Add the launcher operations (Q18) and schema v6's DDL (Q10, Q20).
1. **Library resolution** (`internal/library`, new).
   - Read the bundled registry, the local directory, checkouts and the cache.
   - First match wins by which source exists, and an invalid winner is listed
     (Q6).
   - Record `source`, `overrides` and `provenance`.
   - No network at startup (Q7).
2. **Registry entries.**
   - The four `studios/*.yaml` become pointers with inline manifests, and the
     PLACEHOLDER headers go.
   - iris drops the capabilities it does not use (Q3).
   - `helm validate` accepts the envelope, and the registry is embedded.
3. **Reading a repository.**
   - A git-only read of `helmstudio.yaml` and `.gitmodules` at a ref, resolved
     to a commit and cached.
   - "From folder" reads one file at a typed path (Q14).
4. **The validator's additions:**
   - a bytes entry point
   - the `dest` and `test.smoke` rules (Q14)
   - the static criteria (Q16)
   - the capability sentences (Q12)
5. **Library operations** (Q17–Q19): validate, edit, save, revert, duplicate,
   import from text and from a URL, read a repository, and export.
6. **Approval** (Q10–Q13).
   - The preview, and the digest on `:install`, `:retry` and `:launch`.
   - Install checks out exactly the approved commit.
   - Schema v6 records the approval.
7. **Selectable weights** (Q20, Q21).
   - The selection is chosen on the preview, and install fetches only it.
   - `PUT …/selection`.
   - `{models.selected}` is substituted, and M2's refusal is removed.
8. **The plain shelf.** Show the approval preview as text and send its digest,
   and choose a checkpoint, so the demo runs before M7b.

##### File list

- `internal/library/**` (new)
- `internal/manifest/**`, `internal/install/**`, `internal/supervisor/**`,
  `internal/weights/**`, `internal/store/**` (v6), `internal/api/**`
- `api/openapi.yaml` (launcher operations only), and the generator's outputs
- `schema/registry-entry.json` (new), `schema/manifest.json` (descriptions
  only), `schema/schema.go`
- `studios/**`, including `studios/studios.go` (new)
- `cmd/helm/**`, `cmd/helmstudio/**`
- `web/**` (the plain shelf only)
- `docs/design/01-prd.md`, `02-data-model.md`, `03-design-system.md` (§13,
  §13a, §18), `05-sdk-and-custom-studios.md`
- `docs/decisions.md`, and this report

##### Definition of Done

- **First match short-circuits, visibly.**
  - With the same id in all three sources, the local one resolves, and a
    counting fetcher proves source 2 was never read.
  - `GET /studios` names the source, and what it overrides.
  - An invalid local file is listed invalid, and the registry manifest is not
    used in its place.
- **One validator.**
  - Every file in `internal/manifest/testdata` and `studios/` gets the same
    verdict and errors from `helm validate`, from the daemon's loader and from
    `POST /launcher/manifests:validate`.
  - Nothing outside `internal/manifest` decodes a manifest.
- **Nothing escapes.**
  - A test refuses each of: `dest: ../x`; a `cwd` or `test.smoke` outside its
    root; an import from a loopback, private or non-https URL.
  - An imported `local_path` manifest cannot install without approval.
- **What runs is what was shown.**
  - The preview's commands equal the manifest's strings byte for byte, tested
    with shell metacharacters, a newline and a bidirectional character.
  - A test walks `schema/manifest.json` for every property that runs a command,
    so a new one fails until the preview shows it.
  - `:install` without a digest is 409 `approval_required`, and with a stale one
    is 409 `preview_changed`.
  - A branch that moves after approval still builds the approved commit.
  - An Override that changes a `cmd` makes the next `:launch` refuse with
    `approval_required`.
- **A user's manifest is never deleted.** Revert moves the file, and a test
  checks the bytes survive.
- **Selection.**
  - The selected weight's path is substituted for `{models.selected}`, and only
    that checkpoint is downloaded.
  - No selection is 422 `selection_required`.
  - Changing the selection while running is refused.
  - The index refuses a second selected flag.
- **Registry.** The four pointers validate in the gate, with no PLACEHOLDER
  header left.
- **`make gate` is green**, including drift.
- **Machine-bound:** iris, per Q3.

##### Review focus

- **The milestone's own four:**
  - Does first-match-wins short-circuit, and is the chosen source visible?
  - Does the editor produce manifests that `helm validate` accepts, through the
    same validator?
  - Can an imported manifest reference a path outside the studio root?
  - Does the approval screen show the commands that will run, verbatim?
- **Approval.** Can any command run that no current approval covers? Check
  launch, retry, an Override, a branch that moved and a submodule.
- **Network.** Does anything reach the network at startup, or fetch an address
  a request chose without Q14's checks?
- **User files.** Can any path delete or overwrite a manifest the user wrote?
- **One code path.** A special case in Go for one studio or one source means the
  contract is wrong.
- **The tests.** Plant bugs:
  - skip the short-circuit
  - drop `health.exec` from the preview
  - validate in the endpoint with a second parser
- **Stated as verified?** Is anything machine-bound stated as verified?

#### M7b — the screens

Built on a reviewed M6b and a reviewed M7a. Its file list and DoD are confirmed
at its own kickoff.

- Library cards showing source, level and install state as three facts
  (03 §13a), with Override and Revert
- Add studio: from a repository, from a folder, by import, and by writing one
- The editor (03 §13a) over M7a's operations: form, YAML, criteria, Save and
  Export
- The import dialog
- The approval screen (03 §13)
- The checkpoint choice on install and on launch
- Goldens in both themes at 1280, 1000 and 380 px
- **Machine-bound:** the milestone's demo, per Q3
