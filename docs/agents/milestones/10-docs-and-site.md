# M10 — Docs and site

**Effort:** 8–10 days. **Machine-bound demo:** the quickstart, on a clean machine.

Expanded at kickoff from the fourteen questions in
`docs/agents/reports/10-docs-and-site.md`, answered "recommendations for all"
(`docs/decisions.md`, "2026-09-16 · M10 docs and site", Q1–Q14). Before that it
was scoped, not specified, to be expanded at its own kickoff; the six milestones
before it are unreviewed, so it expands without the review findings its brief
wanted, and says so.

**Taken before M9, 2026-09-16, by the human's decision** ("work on the m10, we
will m9 later"). What depends on M9 — a shipping binary for the quickstart, the
install section, the screen recording — waits for it, and the costs are recorded
in `docs/decisions.md`. The kickoff is `docs/agents/reports/10-docs-and-site.md`.

## Shape

SDK documentation good enough for a developer who has never seen this project
to ship a studio, and the site at **helmstudio.in**.

Deliberately last: documentation written against a moving API is rewritten, and
a site built before the screens exist is a mockup of a guess.

## Known at freeze

- The audience is a studio author, not a contributor to the framework.
- The quickstart's measure is a clean machine and a stopwatch, not a review.

## The brief

### Tasks

0. **The contract, before any content.** The answers are in `docs/decisions.md`.
   Amend phase 9 in `docs/plan/01-build-plan.md` where they change it: the
   quickstart's source build and example studio, `helm studio init`'s absence,
   and what waits for M9.
1. **The generator** (`site/`, a module of its own, so the daemon never links
   `github.com/yuin/goldmark`):
   - Markdown pages rendered with goldmark, laid out by `html/template` on
     helm-css copied from `packages/helm-css`;
   - the API reference, generated from `api/openapi.yaml` for the `studio-api`
     and `public` operations, with each operation's Go, Python and JavaScript
     calls — checked against the generated clients, so a documented call that a
     client does not have fails the gate;
   - the manifest reference, generated from `schema/manifest.json`;
   - the CLI reference, generated from `helm`'s own usage;
   - samples included by path, never written inline;
   - `make site`, into `site/out/`.
2. **The example studio** (`site/samples/hello-studio/`): a Python studio that
   makes an image, adopts it, records a gallery item with its parameters, and
   reads it back. The gate runs it under `helm dev`, in a Python environment
   made with the standard library's `venv`.
3. **The quickstart**, against a source build: clone, build `helm`, make an
   environment, install the SDK from the clone, copy the example, `helm dev`,
   make an image, see it recorded. Every step the gate cannot run — the ones
   that need the network — is marked on the page.
4. **Concepts**: the manifest; process groups; managed and linked weights;
   capabilities; the two providers; why a studio stays its own repository.
5. **Guides**: wrap a repository with no manifest; record with provenance;
   sessions; hand off to the timeline; theme with helm-css; develop in isolation,
   as it is today.
6. **Publishing**: the criteria as scored, the levels, and what a registry pull
   request contains.
7. **The four annotated manifests**, keyed by JSON pointer, with a digest that
   a changed studio file has to be read again for.
8. **The site**: the thesis; the problem made concrete; the launch studios with
   badges generated from their manifests; platform support as verified; add your
   own studio; the repository. No recording, no `.dmg` and no Homebrew until M9;
   no placeholder in their place.
9. **The gate**:
   - `make site` builds, and the site module's tests fail on a broken internal
     link, a sample that does not run, an annotation out of step, or a reference
     page for an operation the document no longer has;
   - the example studio runs under `helm dev`;
   - `helm validate -theme` passes on the site's stylesheet;
   - goldens of the landing page and the quickstart in both themes at 1280, 1000
     and 380 px;
   - `make deps` reads every `go.mod` in the repository, not only the root's, so
     the site's dependency is held to the same rule.

### File list

- `site/**` (new, its own `go.mod`)
- `Makefile`, `docs/agents/gate.md`, `.gitignore` (`site/out/`)
- `test/visual/**`: the site's goldens only
- `docs/plan/01-build-plan.md` (phase 9, per the answers)
- `docs/decisions.md`, and the milestone report

### Definition of Done

- **`make site` builds from a clean checkout**, and the gate fails on a broken
  internal link, a failing or unexecuted sample, an annotation out of step, or a
  stale reference page.
- **Every code sample on every page is a file the gate runs**, and a step the
  gate cannot run says so on the page.
- **The reference is generated.** No reference page is hand-written, and every
  client call it names exists in that client.
- **Nothing on the site claims what the software does not do**: no mockups, no
  future tense, unbuilt tools named as unbuilt, platform support as verified,
  and no install section for binaries that do not exist.
- **The site is built with helm-css**, passes `helm validate -theme`, and has
  goldens in both themes at 1280, 1000 and 380 px.
- **`make gate` is green.**
- **Machine-bound:** the quickstart, followed literally on a Mac that has never
  built helmstudio, from a source build, timed. Every assumed step is a finding.
  After M9 it runs again against released binaries.

## Review focus

- Follow the quickstart literally, on a machine with nothing installed. Every
  assumed step is a finding.
- Does the SDK documentation describe the contract, or the current
  implementation?
- Is every code sample in the docs executed by the gate? Samples rot silently.
- Does the site claim anything the software does not do?
