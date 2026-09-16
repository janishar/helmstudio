# M7 — Library, editor and iris

**Effort:** 6–8 days. **Machine-bound demos:** iris installed and relaunched on a
second checkpoint (M7a); wrap a repository that ships no manifest (M7b).

Expanded at kickoff from the questions in `docs/agents/reports/07-library-editor-iris.md`,
answered "recommendations for all" (`docs/decisions.md`, "2026-09-16 · M7 library,
editor and iris", Q1–Q21). Two briefs, reviewed separately (Q2), in this order:

1. M6a's review, with its findings addressed.
2. **M7a**, the library, trust and selection.
3. M6b, built on a reviewed M6a and a reviewed M7a.
4. **M7b**, the screens, built on a reviewed M6b and a reviewed M7a.

## Reads

- `docs/design/01-prd.md` R1–R3d, R5, R14, R61–R67, §13
- `docs/design/02-data-model.md` §1, §4, §7
- `docs/design/03-design-system.md` §5, §13, §13a, §18
- `docs/design/05-sdk-and-custom-studios.md` §5a, §8–§11
- `docs/design/06-storage.md` §4, `docs/design/07-platform-services.md` §3
- `schema/manifest.json`, and the launcher operations in `api/openapi.yaml`
- `docs/decisions.md` in full; the reports of M0, M3, M6 and this milestone's
  kickoff; M6a's review findings
- iris studio at `b4f857f`

## Known at freeze

- The manifest lives in the studio's **own repo** as `helmstudio.yaml`; the
  registry holds pointers. Two copies drift; one does not.
- **Three resolution sources, first match wins:** local file → in-repo
  `helmstudio.yaml` → registry inline pointer. Most repositories will never
  ship a manifest, so anyone must be able to write one. A manifest is a
  description of a repository, not a file only its author can provide.
- The library is a thing a user browses, chooses from and adds to — not a
  config file.
- The approval screen is the last point before a user's machine runs someone
  else's build steps. What it shows is a security decision, not a layout one.

---

## M7a — the library, trust and selection

Starts only once M6a's review is done and its findings are addressed (Q2). If a
finding changes a convention this brief copies (launcher tags, the Origin
exemption, error shapes), the finding wins, and the change is raised at M7a's
start rather than resolved in code.

**Raised at M7a's start, 2026-09-16.** Four things changed under this brief
between its being written and its being built. None needed a decision; each is
recorded in `docs/decisions.md`.

1. **Schema v6 is taken.** M8a shipped it first. Tasks 0, 6 and 7 say v6
   throughout; **M7a takes v7**, and 02 §5 carries v7's DDL.
2. **The plain shelf no longer exists.** Task 8 was written to add approval
   controls to it "so the demo runs before any screen exists". M6b replaced it
   with the launcher's screens, so **task 8 is the launcher's Install path**:
   the approval preview as a plain dialog and the checkpoint choice, which is
   what Q2's own answer says M6b's Install shows until M7b draws 03 §13.
3. **Launcher operations are generated.** M6b tags every launcher operation
   with `x-helm-group` and `x-helm-method` and generates `web/launcher.js` from
   them, so this milestone's eleven operations carry those extensions and the
   launcher client regenerates. The file list's "the generator's outputs if a
   shared schema changes" now means every launcher change.
4. **M7a is the third milestone built out of order.** M6a's review has still
   not run, and M6b is now unreviewed beneath this too.

### Tasks

0. **The contract, before any code.**
   - Amend the design per Q4–Q21:
     - 01: R1–R3d and R62–R64
     - 02: §4 and §7
     - 03: §13, §13a and §18, including the capability sentences; and §5's
       reference to "the approval harness's job (M7)", which moves with the
       harness (Q16)
     - 05: §5a, §8 and §9, including criterion 4; and §11's "Needs 4" for the
       add flow (Q13, Q16)
     - the `selectable` description in `schema/manifest.json`
   - Add `schema/registry-entry.json` (Q4).
   - Add the launcher operations (Q18) to `api/openapi.yaml`.
   - Write schema v6's DDL into 02 first (Q10, Q20).
1. **Library resolution** (`internal/library`, new).
   - Read the bundled registry, the local directory `<data>/studios/*.yaml`,
     installed checkouts and `<cache>/manifests/` (Q5, Q7).
   - First match wins by which source exists, not by validity. An invalid winner
     is listed invalid, and the lower sources are not read for its id (Q6).
   - Record `source`, `overrides` and provenance, which lives in
     `<data>/studios/<id>.source.json` (Q8).
   - Nothing touches the network at startup (Q7).
2. **Registry entries.**
   - The four `studios/*.yaml` become pointers with inline manifests, and the
     PLACEHOLDER SHAPE headers go (Q4).
   - iris's inline manifest drops `kv`, `assets` and `gallery` (Q3).
   - The registry is embedded through `studios/studios.go`; `-studios` stays as
     a development override.
3. **Reading a repository.**
   - Git only. A depth-1 fetch of the ref without file contents, then only
     `helmstudio.yaml` and `.gitmodules`.
   - The ref is resolved to a commit and the result cached (Q7).
   - A local pointer resolves as a registry pointer does (Q8).
   - "From folder" reads only `<path>/helmstudio.yaml` at a typed absolute path
     (Q14).
4. **The validator's additions** (`internal/manifest`), so every caller shares
   one implementation:
   - a bytes entry point that `Validate(file)` itself calls (Q17)
   - the registry-entry envelope, with its field-equality rule (Q4)
   - the YAML node-tree edit behind `:edit`, keeping comments and key order (Q17)
   - the `dest` and `test.smoke` rules (Q14)
   - the static criteria (Q16) and the capability sentences (Q12)
   - `helm validate` accepting a registry entry, and `-criteria`
5. **Library operations** (Q18, Q19):
   - `GET /studios` extended into the library
   - `GET /studios/{id}/manifest`
   - validate and edit
   - save, with `If-Match` on the file's digest
   - Revert, which moves the file to `<data>/studios/_reverted/` and never
     deletes (Q9)
   - Duplicate
   - import from text and from a URL, with Q14's checks and a confirm digest
   - `POST /launcher/repositories:read`
   - export, byte-identical
6. **Approval** (Q10–Q13).
   - `GET /studios/{id}/approval` returns the preview and its digest:
     - every command, grouped by when it runs, verbatim, with control, zero-width
       and bidirectional characters flagged
     - submodules
     - weights with sizes, and the default checkpoint
     - capabilities as sentences, and declared hosts
     - the level (Q15)
     - checks that execute nothing; theme conformance and the smoke test listed
       as "Not run before install"
   - `approval=<digest>` on `:install`, `:retry` and `:launch` whenever the
     recorded approval does not match. With none, the answer is 409
     `approval_required`; with a stale one, 409 `preview_changed`.
   - Install checks out exactly the approved commit.
   - Schema v6 records the approval on the installation.
   - `helm dev` records its own installation as approved.
7. **Selectable weights** (Q20, Q21).
   - Schema v6's partial unique index on `studio_model_bindings.selected`.
   - The checkpoint is chosen with the install, and install downloads only that
     one.
   - `PUT /studios/{id}/selection`, refused while running (409 `conflict`) and
     for a weight not downloaded (409 `not_fetched`).
   - Launch with no selection is 422 `selection_required`.
   - `{models.selected}` is substituted, and M2's refusal is removed.
8. **The plain shelf** (`web/`). Show the approval preview as text and send its
   digest, and choose a checkpoint, so the demo runs before any screen exists.

### File list

- `internal/library/**` (new)
- `internal/manifest/**`, `internal/install/**`, `internal/supervisor/**`,
  `internal/weights/**`, `internal/store/**` (v6), `internal/api/**`
- `api/openapi.yaml` (launcher operations only), and the generator's outputs if
  a shared schema changes
- `schema/registry-entry.json` (new), `schema/manifest.json` (the `selectable`
  description only), `schema/schema.go`
- `studios/**`, including `studios/studios.go` (new)
- `cmd/helm/**`, `cmd/helmstudio/**`
- `web/**` (the plain shelf only)
- `test/conformance/**` and `test/visual/**`, only where an existing test must
  send an approval (Q10), each edit named for sign-off
- `docs/design/01-prd.md`, `02-data-model.md`, `03-design-system.md` (§5,
  §13, §13a, §18), `05-sdk-and-custom-studios.md`
- `docs/decisions.md`, and the milestone report

### Definition of Done

- **First match short-circuits, visibly.**
  - With the same id in all three sources, the local one resolves, and a
    counting fake fetcher proves source 2 was never read.
  - `GET /studios` names the source, and what it overrides.
  - An invalid local file is listed invalid, and the registry manifest is not
    used in its place.
- **One validator.**
  - Every file in `internal/manifest/testdata` and `studios/` gets the same
    verdict and errors from `helm validate`, from the daemon's loader and from
    `POST /launcher/manifests:validate`.
  - Nothing outside `internal/manifest` decodes a manifest or a registry entry.
- **Nothing escapes.** A test refuses each of:
  - a `dest` of `../x`, and one of `.`
  - a `cwd` or `test.smoke` outside its root
  - an import from a loopback, link-local, private or non-https URL, and one
    that redirects elsewhere
- **What runs is what was shown.**
  - The preview's commands equal the manifest's strings byte for byte, tested
    with shell metacharacters, a newline and a bidirectional character.
  - A test lists every string-valued property in `schema/manifest.json`, and
    fails on one that is neither shown on the preview nor classified as running
    nothing. A new command field cannot be missed.
  - `:install` without a digest is 409 `approval_required`, and with a stale one
    is 409 `preview_changed`. Nothing is cloned in either case.
  - A branch that moves after approval still builds the approved commit.
  - An imported `local_path` manifest cannot install without approval.
  - An Override that changes a `cmd` makes the next `:launch` refuse with
    `approval_required`.
- **A user's manifest is never deleted.** Revert moves the file, and a test
  checks the bytes survive.
- **Selection.**
  - The selected weight's path is substituted for `{models.selected}`, and only
    that checkpoint is downloaded.
  - No selection is 422 `selection_required`.
  - Changing the selection while running is refused, and changing it while
    stopped needs no new approval.
  - The index refuses a second selected binding.
- **No network at startup.** A daemon started with a registry pointer that has
  nothing cached makes no outbound connection, and a test proves it.
- **Registry.** The four pointers validate in the gate, with no PLACEHOLDER
  header left.
- **`make gate` is green**, including drift.
- **Machine-bound.** On the Mac with the real daemon:
  1. Install iris from its registry entry through the approval preview, with
     FLUX.2 Klein 4B chosen.
  2. Generate an image.
  3. Choose a second checkpoint, fetch or link it, and relaunch.
  4. Generate again.

### Review focus

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
  - put the selection into the approval digest
- **Stated as verified?** Is anything machine-bound stated as verified?

---

## M7b — the screens

Built on a reviewed M6b and a reviewed M7a. Its file list and DoD are confirmed
at M7b's kickoff against what those reviews changed.

### Tasks

- **Library cards** showing source, level and install state as three facts
  (03 §13a), with Override, Revert and Duplicate.
- **Add studio:** from a repository, from a folder (a typed path), by import,
  and by writing one.
- **The editor** (03 §13a) over M7a's operations.
  - The form is generated from `schema/manifest.json` served to the page, with
    the map to 03 §13a's sections and its test (Q17).
  - Beside it, the YAML pane and the criteria.
  - Save and Export. No Test until the harness exists (Q16).
- **The import dialog:** report before acting, and Override, Rename or Skip per
  entry.
- **The approval screen** (03 §13), replacing M6b's plain dialog.
- **The checkpoint choice** on install and on launch.
- **Goldens** in both themes at 1280, 1000 and 380 px.

### Definition of Done (draft)

- **The page never parses YAML.** No YAML parser ships in `web/`, and every
  validation goes through M7a's operations.
- **Screens** as above, each with goldens in both themes.
- **`make gate` is green.**
- **Machine-bound.** On the Mac with the real daemon:
  1. Take a public repository with no registry entry and no `helmstudio.yaml`.
  2. Write its manifest in the editor.
  3. Install it through the approval screen, and generate.
  4. Export the manifest. The pull request is opened by hand (Q19).
