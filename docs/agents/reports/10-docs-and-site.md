# M10 — Docs and site — implementation report

## What was built

The documentation and the site for helmstudio.in, built by `site/`, a Go module
of its own so the daemon never links goldmark. Fifteen pages are written in
Markdown: the overview, the quickstart, six concepts, six guides and
publishing. The rest are generated: the API reference for the 54 `studio-api`
and 2 `public` operations, each with its Go, Python and JavaScript call; the
manifest and CLI references; the four launch manifests, annotated field by
field; and a landing page whose studio cards are read from `studios/*.yaml`.
Every code block on every page is one of 29 files under `site/samples/`. The
gate runs 25 of them, including the quickstart as written under `helm dev`, and
the page says why it cannot run the other four.

`.github/workflows/site.yml`, from a parallel session and already on this
branch, publishes the build to GitHub Pages. Nothing was deployed from here.

## Gate

    $ make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: every go.mod change since 4e71298 is recorded in docs/decisions.md
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	1.946s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.457s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	1.124s
    ok  	github.com/janishar/helmstudio/internal/approval	0.460s
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css	0.442s
    ok  	github.com/janishar/helmstudio/internal/export	0.456s
    ok  	github.com/janishar/helmstudio/internal/install	10.104s
    ok  	github.com/janishar/helmstudio/internal/library	1.838s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.423s
    ok  	github.com/janishar/helmstudio/internal/media	0.982s
    ok  	github.com/janishar/helmstudio/internal/platform	1.071s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	1.347s
    ok  	github.com/janishar/helmstudio/internal/supervisor	40.949s
    ok  	github.com/janishar/helmstudio/internal/theme	1.074s
    ok  	github.com/janishar/helmstudio/internal/themelint	0.383s
    ok  	github.com/janishar/helmstudio/internal/timeline	0.389s
    ok  	github.com/janishar/helmstudio/internal/weights	1.431s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-css	0.392s
    ?   	github.com/janishar/helmstudio/packages/helm-css/cmd/build	[no test files]
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/node	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-ui-sdk	0.417s
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ?   	github.com/janishar/helmstudio/studios	[no test files]
    ok  	github.com/janishar/helmstudio/test/media	3.103s
    ?   	github.com/janishar/helmstudio/test/studios/sequencer	[no test files]
    ok  	github.com/janishar/helmstudio/web	0.391s
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.550s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	4.097s
    site: 37 pages in /Users/janisharali/GenAI/helmstudio/site/out
    ?   	github.com/janishar/helmstudio/site	[no test files]
    ?   	github.com/janishar/helmstudio/site/cmd/site	[no test files]
    ok  	github.com/janishar/helmstudio/site/gen	10.205s
    ?   	github.com/janishar/helmstudio/site/samples/providers/embedded	[no test files]
    ?   	github.com/janishar/helmstudio/site/samples/theming/go	[no test files]
    ok  	github.com/janishar/helmstudio/test/visual	102.683s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

Run on this Mac (macOS 27, Chrome 152, Go 1.27.1, Python 3.9.6, Node 26.8.2),
on the tree the two commits before this report hold, with nothing set to skip:
the quickstart, every Python sample, all three theming servers and the site's
twelve goldens ran. `internal/weights`, whose flake is offered as its own task,
passed.

## What the tests hold

In `site/gen` unless named. **Each was made to fail by planting its bug**, and
every plant failed on its first run.

- **The site builds for GitHub Pages** under `/` and under `/helmstudio/`. Every
  `href`, `src` and `#fragment` in the written HTML leads to a file and an id,
  and `CNAME`, `.nojekyll`, `404.html`, helm-css and its fonts are in the
  output. A planted dead link, missing fragment and relative link are each
  reported.
- **The command builds what `make site` builds**, so `cmd/site` is run as well
  as the function.
- **The navigation reaches every page**, and each link carries its page's own
  title.
- **A stale page does not survive a build.** A page planted in the output is
  gone after the next build. A directory holding other files and no `.nojekyll`
  is refused rather than emptied.
- **The API reference is the contract**: one section per `studio-api` and
  `public` operation in `api/openapi.yaml`, and no others. Planting a skipped
  operation fails it. A documented call missing from a generated client fails
  the build: `api_test.go` feeds the matcher a doc comment the Go generator has
  wrapped, and removing the fix fails it.
- **Annotations fail when out of step.** A changed studio file, a field with no
  note, and a note for a field that is not there each fail with their own
  message.
- **Markdown**: an inline code block and a misspelt directive are refused; a
  sample says which file it is and whether it runs; internal links go under the
  base path; raw HTML is escaped. `@capabilities` and `@criteria` render the
  software's own tables.
- **Every sample is on a page, and run or marked.** A file on no page, a sample
  both marked and run, and a sample neither marked nor run each fail.
  `samples_test.go`'s table is what the running tests take their work from.
- **The quickstart runs as written**: steps 2, 3 and 5 in one shell, step 6
  exec'd so the test interrupts `helm dev` itself, then steps 7 to 9, checked
  against what the page says each prints. `06-output.txt` is matched line by
  line against `helm dev`'s output, with `…` for what varies. Planting a changed
  seed in step 7, or a changed line in the output, fails it.
- **The Python samples run** inside a studio under `helm dev`, with a token, a
  stage directory and capabilities. Planting a wrong lineage assertion fails it.
- **The theming studio serves what its page links**, with its Python, Go and
  JavaScript servers in turn: helm-css, the browser runtime, the manifest's hue
  in `accent.css`, and the theme stream through the proxy. Its stylesheet
  passes `lint.sh`. Removing the proxy mount fails it.
- **The embedded provider sample records** with no daemon, into `./.helm`.
- **The manifest samples validate.** A planted unknown `depends_on` fails it.
- **The site's stylesheet passes the theme lint**, and a planted colour literal
  fails it.
- **`make deps` reads every `go.mod`.** With `site/go.mod` new and no
  decision-log line, it failed naming all twenty of its lines; with the line,
  it passes.
- **The site's goldens** (`test/visual/site_test.go`): the landing page and the
  quickstart, both themes, at 1280, 1000 and 380 px. Changing the quickstart's
  release note moved all six quickstart goldens and none of the landing's.

## Tests that did not catch their bug

None of the plants passed. One test failed when nothing was wrong, which is
worth recording. The quickstart test failed two runs in three, because `helm
dev` prints a process's output a tick after the process writes it and drops
what it has not printed when interrupted. The test went on as soon as the
health check answered and stopped `helm dev` within half a second. It now waits
for the studio's own line first, as a reader does, and passed three runs in a
row and under `-race`. The behaviour itself is an open entry.

## Design contradictions raised

None of these is a design document contradicting itself, the case that stops a
milestone. Each is software that disagrees with the design, in files that are
not M10's, so each is recorded under "Open, not yet decided" and the pages
describe what the software does:

- **The criteria table is not 05 §9's** for 3, 6, 7, 10, 11 and 12, and it has
  10 and 14's levels the other way round. The publishing page renders the
  software's table, so it shows the software's titles.
- **03 §2's hues for ltx, iris and AuK are in no manifest.** The launcher, the
  timeline and the site's cards all show ramp hues, two of them teal. An M8b
  open entry says every launch studio declares a hue; only h3 does.
- **05 §6's dissolve** is refused "when either side is short of handle"; today
  it is accepted. Also, `:append` cannot add a still, and a dissolve into a
  still exports short. The timeline guide lists all three.
- **The schema's `run` shorthand cannot validate**, as M0 recorded. The manifest
  page tells authors to use `processes`.

## Judgement calls

Each is in `docs/decisions.md` under "M10 · judgement calls while building",
with its reason:

- The quickstart installs the SDK without `-e`, and says no package is on PyPI
  or npm in the present tense. This amends Q5.
- Platform support says "type-checked for Linux", not "build". This amends Q13.
- The capability sentences and criteria are read from `internal/manifest`, not
  written into pages.
- The proxy is shown from all three languages, plus the Go embedded provider.
  Those are the places the clients differ in more than spelling (Q4).
- The example declares its needs, so it fails only criteria 13 and 15.
- The quickstart workspace links the checkout entry by entry, and step 4
  becomes a `.pth` file.
- The site's goldens live beside helm-css's and use the same pins.
- `CNAME` is kept for a deploy from a branch, and a non-site output directory is
  refused.

Not in the log, because they decide nothing anyone else builds on:

- The sidebar's words are each page's title.
- Below 900 px the sidebar becomes a Contents disclosure, so a phone reader
  reaches the page first.
- Descriptions on the landing cards are not clamped to one line.
- The theming sample is lantern studio, with a hue no launch studio has. It was
  first given `#4dc1cb`, which turned out to be ltx's ramp hue.

## What running it for real found

- **`pip install -e` fails with the pip macOS ships** (21.2.4 on Python 3.9.6):
  editable installs of a `pyproject.toml`-only project need pip 21.3. The
  quickstart as drafted would have failed at step 4 on a clean Mac. A plain
  install from the same pip works, and was run by hand.
- **The example failed three criteria**, not the two the page said. It declared
  no tools, memory, disk or peak. It now does.
- **`helm validate -theme <dir> -strict`**, the order the usage line gives,
  reads `-strict` as a manifest file.
- **The API reference failed to build** on a doc comment the Go generator
  wraps, which split the `(PATCH /gallery/items/{id})` marker across two lines.
  The parallel session found it; the matcher now reads each comment whole.
- **`helm dev` names "run install again"** for an unlinked weight, which under
  `helm dev` means `-link`.
- **The timeline's three problems**, above, were found running the guide's
  sample before this report's first draft.

## Could not verify

- **The machine-bound demo**: the quickstart followed literally, and timed, on a
  Mac that has never built helmstudio. This Mac has a warm Go module cache and
  its checkout. Steps 1 and 4 need the network, so the gate does not run them.
  Step 4 was run once by hand, with the change above. What would show it: a
  clean macOS account with the Xcode command-line tools and Go 1.27.1, the
  commands copied from the page one by one, and a stopwatch. Every step that
  needs something the page does not say is a finding.
- **The site at helmstudio.in.** Enabling Pages from Actions, the custom domain
  in the repository's settings, and the DNS are the human's. The build was
  served locally, and checked under `/` and `/helmstudio/`.
- **The generator on Linux.** The workflow runs it on `ubuntu-latest`, where it
  builds `helm` for the CLI reference. That first run on a push to `main` is its
  first run on Linux.
- **Assistive technology.** The pages use landmarks, a skip link, `lang` and
  real headings, but no screen reader was run over them.

## Dependencies added

`site/go.mod`, a new module:

- **`github.com/yuin/goldmark` v1.8.6** renders Markdown, which the standard
  library cannot. Recorded as Q7.
- **`gopkg.in/yaml.v3` v3.0.1** is already the root's.
- **The root module and the two runtime SDK modules**, through `replace`.
- **Indirectly, the root's own versions** of jsonschema, `x/sys`, SQLite and
  SQLite's dependencies. The generator reads `internal/manifest` and
  `internal/theme`, and the Go samples build against the embedded provider.

All are named in one decision-log line, which is what `make deps` now checks
across every `go.mod`.

## Files touched outside the milestone's list

- **`docs/agents/reports/10-docs-and-site.md`**: this report, above the kickoff.
- **`.claude/launch.json`**, which is not committed: a local preview
  configuration named `m10-site`.

The workflow and its decision entry came from the parallel session. Everything
else is in the file list: `site/**`, `Makefile`, `docs/agents/gate.md`,
`.gitignore`, `test/visual/` (the site's test and goldens),
`docs/plan/01-build-plan.md` and `docs/decisions.md`.

## Decision-log entries appended

- **Under "2026-09-16 · M10 docs and site"**, a section "M10 · judgement calls
  while building" with nine entries:
  - the site module's dependencies;
  - the quickstart's SDK install (amends Q5);
  - the Linux wording (amends Q13);
  - the tables read from `internal/manifest`;
  - three languages for the proxy;
  - the example's declared needs;
  - the quickstart's workspace;
  - the site's goldens;
  - `CNAME` and the output guard.
- **Under "Open, not yet decided"**, eight entries:
  - the criteria table against 05 §9;
  - the missing hues;
  - `-strict`'s position;
  - `pip install -e` with macOS's pip;
  - "run install again" under `helm dev`;
  - `helm dev`'s late output;
  - the timeline's three problems;
  - sixteen undescribed schema fields.

The phase 9 amendment in `docs/plan/01-build-plan.md` now says the site is
published by the workflow, which Q11's resolution had overtaken.

## Left undone

- **The machine-bound quickstart**, above. It runs again against released
  binaries after M9.
- **What waits for M9** (Q2): the install section's `.dmg` and Homebrew, and the
  screen recording. The site holds no placeholder for either.
- **Commands the pages name as unbuilt**: `helm studio init`, `helm test`,
  `helm doctor`, `helm adopt`, and `helm dev --fixtures` and `--fail`.
- **The eight open entries**, each in files that are not M10's.
- **Merging.** Nothing is merged or pushed.

---

## The kickoff

**Nothing is built.** M10's brief is scoped, not specified: it is "expanded to
M0-level detail at its own kickoff". This is that kickoff. It stops before task 1
on the questions below, the way M7's and M8's did.

**Taken before M9**, by the human's decision (`docs/decisions.md`, "2026-09-16 ·
M10 docs and site"). That decision shapes most of the questions, because the
plan wrote phase 9 against M9's outputs.

---

## What I measured

**The contract to document.**
- `api/openapi.yaml`: 4,004 lines, 94 operations in 17 groups. By tag, 54 are
  `studio-api`, 38 `launcher` and 2 `public`. Studio authors need the 54 and the
  2; the launcher's 38 are the daemon's own page.
- `schema/manifest.json`: 298 lines, 26 top-level properties.
  `schema/registry-entry.json`: 36 lines.
- `studios/*.yaml`: four registry pointers carrying inline manifests (M7 Q4), no
  longer the full manifests M0 wrote. "The four launch manifests annotated line
  by line" is now four inline manifests inside pointers.

**The tools the plan's quickstart and guides use**, set against what exists:

| 05 §4 / phase 9 | Exists? |
|---|---|
| `helm validate` (with `-theme`, `-strict`, `-json`) | yes |
| `helm dev` (`-f`, `-addr`, `-link`, `-venv`) | yes |
| `helm dev --fixtures` (seeded gallery, a fake sibling studio, an inbox) | **no** |
| `helm dev --fail=assets:500,…` (fault injection) | **no** |
| `helm studio init <name>` (the scaffold the quickstart starts from) | **no** — belongs to no milestone (M7 Q16) |
| `helm test` (the smoke harness) | **no** — belongs to no milestone (M7 Q16) |
| `helm doctor --sdk` / `--studio` | **no** — belongs to no milestone (M7 Q16) |
| `helm adopt` (in phase 9's CLI reference list) | **no** — in no brief |

Of the six commands phase 9's CLI reference names — init, dev, validate, test,
doctor, adopt — **two exist**.

**Distribution.**
- `@helmstudio/runtime` and `@helmstudio/ui` are `1.0.0-dev.0`, and
  `helm-runtime-sdk` (Python) is `1.0.0.dev0`. **None is published.**
- helm-css has no package manifest; the daemon serves it under `/sdk/v1/`.
- There is no Homebrew formula and no `.dmg`; both are M9's.
- A quickstart today can install none of these the way 04 §8 says a studio
  author does.

**The domain.** M4 Q2 recorded "nobody serves `helmstudio.in`" (the human chose
the GitHub path for module names). Phase 9 says "Static output, deployed to
helmstudio.in with the documentation at /docs". Nothing in the repository
deploys anything: there is no CI workflow and no hosting configuration.

**What already exists to build from.**
- `docs/artifacts/` holds the design sprint's reading copies: static HTML, one
  file each. They are not the contract, and not user documentation.
- The README is one line.
- `api/gen` already generates three clients and `web/launcher.js` from the
  OpenAPI document. A reference generator is the same kind of program.
- `make drift` already fails on generated output that has moved.
- The gate runs the Python and Node clients' smoke tests (`make conformance`).
  Python 3.9.6, Node 26.8.2 and Go 1.27.1 are on this Mac.

**What M9 will change under the docs.** Browser auth and the CSRF cookie; the
launcher's Gallery and Timeline; `:open` reaching a connected page; who owns a
launcher export; the bundled ffmpeg.

---

## Questions

### A · Order and what M10 can honestly contain

#### Q1. Who writes M10's contract

The same question as M5 to M8's Q1.

**Recommendation:** the same answer. The proposed expansion at the end is a
draft, the human edits it into the milestone document, and implementation starts
after that.

#### Q2. What M10 builds before M9, and what waits

Phase 9's own guidance: "Phase 9 is the one place shipping less is genuinely
fine: a quickstart, the generated reference and the four annotated manifests are
worth more than a complete site."

**Recommendation — build now, because none of it waits on M9:**
- the generated reference, for the API and the manifest;
- concepts;
- the guides the software already supports:
  - wrap a repository with no manifest (M7b's editor);
  - record with provenance, and sessions (the runtime SDK);
  - hand off to the timeline (M8);
  - theme with helm-css;
  - develop in isolation with `helm dev` as it is;
- publishing, as it is scored today (see Q6);
- the four annotated manifests;
- the site, minus the next list.

**Wait for M9:**
- the install section's `.dmg` and Homebrew;
- the screen recording;
- the quickstart run "against the released binaries on a clean machine".

Until then the quickstart is written against a source build, with a banner
saying so, and is re-run on a clean machine when M9 ships.

### B · The quickstart and the tools it assumes

#### Q3. The quickstart starts from `helm studio init`, which does not exist

Phase 9 says the quickstart goes "from nothing to a scaffolded studio recording
its first gallery item", and "if it takes more than ten minutes, fix the tooling
rather than the prose". 05 §4's loop is `helm studio init`, then `helm dev
--fixtures`. Neither exists, and M7 Q16 left init, test and doctor for a
milestone the human places.

Options:
- **(a) M10 builds `helm studio init`.** Phase 9's rule argues for fixing the
  tooling. Cost: R68 is a feature, placed by nobody, in a docs milestone.
- **(b) The quickstart copies an example studio** that lives in the repository,
  and runs it under `helm dev`. The gate builds and runs the example. When init
  is built, it scaffolds exactly that example, and the quickstart's first step
  changes from a copy to one command.
- **(c) Place init, test and doctor in their own milestone before M10.**

**Recommendation: (b).** It is the smallest thing that is true today. It makes
the quickstart executable by the gate (the review focus's "is every code sample
executed by the gate?"), and it leaves init a small change later. `--fixtures`
and `--fail` are not built for M10. The guide to developing in isolation says
what exists, and says what does not.

#### Q4. The quickstart's languages

There are three runtime SDKs, and M5's studios are Python.

**Recommendation:** the quickstart in Python, the language most open-weight model
repositories are in. Guides show Python, and add Go and JavaScript wherever the
calls differ in more than spelling. The reference lists all three clients' calls
for every operation, generated from `x-helm-group` and `x-helm-method`.

#### Q5. Installing an SDK nothing publishes

`pip install helm-runtime-sdk`, `npm i @helmstudio/runtime` and `go get …/go` are
04 §8's paths. The first two are not published, and publishing them is
outward-facing and the human's.

**Recommendation:** the quickstart installs from the repository: `pip install -e`
from a clone, and Go through `replace`. It states that the registry names come
with the first release. M10 publishes nothing.

### C · How the documentation is built

#### Q6. Publishing and the fifteen criteria, today

M7 Q16 scores 7 of 15 criteria. The rest need the harness (Q3), and no studio can
be Verified or Registry (M7 Q15).

**Recommendation:** document the criteria as the software scores them: "n of m
checkable", with each unscored criterion named and why. The page says what a
registry pull request contains, and that certification beyond Unverified waits
for the harness. Criteria are not described as checked when they are not.

#### Q7. Authoring format, the generator, and a dependency

The site needs Markdown, or something like it, turned into HTML.
- **(a) Standard library only**: pages authored as HTML fragments, assembled
  with `html/template`. No dependency; prose is harder to write and review.
- **(b) Markdown, rendered by `goldmark`**: a pure-Go library with no
  dependencies of its own. It is a new dependency, recorded, and kept out of
  the daemon's module graph by living in its own module (`site/go.mod`), as the
  embedded provider's SQLite is.
- **(c) A JavaScript static-site generator**: puts a package manager into the
  gate, which M6 Q16 refused.

**Recommendation: (b), in its own module.** Documentation is prose that
contributors will edit, and Markdown is how the rest of `docs/` is already
written. The daemon's binary never links it.

#### Q8. Where it lives, what is committed, and the gate

**Recommendation:**
- **Location.** `site/` holds the content (`site/content/`), templates,
  samples and the generator. `docs/` stays the project's internal documents.
- **Committed.** The build output is not. The generated reference is produced
  at build time.
- **`make site`** builds everything into `site/out/`.
- **The gate builds the site and fails on:**
  - a broken internal link;
  - a sample that does not run (Q9);
  - an annotated manifest out of step with its file (Q10);
  - a reference page for an operation the document no longer has.

#### Q9. Every code sample is executed by the gate

The review focus: "Samples rot silently."

**Recommendation:**
- **No code blocks written inline in prose.** A sample is a real file under
  `site/samples/`, included by path.
- **The gate runs it.** Go samples build and run against the embedded
  provider. Python and Node samples run against a `helm dev` started by the
  test, as the conformance suite's smoke tests already do.
- **Shell transcripts** are run in a temporary directory where they can be, and
  the ones that cannot are marked.

#### Q10. The four annotated manifests stay true

**Recommendation:**
- Annotations are keyed by JSON pointer, not by line number.
- A test fails when:
  - a pointer names nothing in `studios/*.yaml`;
  - a field is added with no annotation;
  - a studio file changes without its annotations being reread. This is a
    digest check with an explicit bump.

### D · The site

#### Q11. `helmstudio.in` — who owns it, and where it is served

M4 Q2: "nobody serves `helmstudio.in`". Publishing a site is outward-facing, and
the domain, DNS and hosting are the human's.

**Recommendation:** M10 produces static output and `make site`, and ends there.
Deployment waits for the human: GitHub Pages from this repository with a
`CNAME`, or another host, once the domain is confirmed. Going live is not in
M10's DoD.

#### Q12. The screen recording

Phase 9 calls it "the single highest-value asset on the site". It needs a real
install and a real generation, which means network, gigabytes and a person
recording, and the plan meant the app (M9).

**Recommendation:** it waits for M9. The page has no placeholder image and no
mockup. The review focus asks "Does the site claim anything the software does
not do?", and a still of a screen that does not ship would.

#### Q13. Platform support, stated honestly

**Recommendation:** state exactly what has been verified.
- macOS on Apple Silicon for the launch studios.
- The daemon and SDKs build on Linux (the gate's `vet-linux`), with no studio
  verified there.
- Windows is not supported.
- Each launch studio's badges are generated from its manifest (`runtime`,
  `requires`, `peak_ram_gb`), never written by hand.

#### Q14. Built with helm-css

**Recommendation:**
- **helm-css is not forked.** The site copies `helm.css` and its fonts from
  `packages/helm-css` into the output at build time.
- **Theme.** A System, Light and Dark control, stored in the browser only.
- **Nothing else.** No runtime SDK, no components that need a daemon, no
  analytics, no signup.
- **The gate lints it.** `helm validate -theme` runs over the site's own
  stylesheet, as it does over a studio's.

---

## Defaults I will take unless told otherwise

- Content in British English, as the design documents are written.
- The API reference covers the 54 `studio-api` and 2 `public` operations. The
  38 `launcher` operations are the daemon's own and are not documented for
  studio authors.
- The manifest reference is generated from `schema/manifest.json`'s own
  descriptions. Where a description is missing, the page says so rather than
  inventing one. Missing descriptions are listed in the report as schema
  findings; the schema is not edited to fill them (CLAUDE.md).
- The CLI reference is generated from `helm`'s own flag definitions, for the two
  commands that exist.
- No page describes a feature in the future tense as if it existed. Unbuilt
  things are named as unbuilt.

---

## Proposed expansion (draft, for Q1)

### Tasks

0. **The contract, before any content.**
   - Record the answers in `docs/decisions.md`.
   - Amend `docs/plan/01-build-plan.md`'s phase 9 where an answer changes it:
     the quickstart's source build, init's absence, and the recording waiting
     for M9.
1. **The generator** (`site/`, its own module):
   - Markdown with `goldmark`;
   - templates on helm-css;
   - the API reference from `api/openapi.yaml`;
   - the manifest reference from `schema/manifest.json`;
   - the CLI reference from `helm`'s flags;
   - `make site`.
2. **The example studio** (`site/samples/hello-studio/` or `examples/`): a Python
   studio that adopts an output and records a gallery item. The gate runs it
   under `helm dev`.
3. **The quickstart**, against a source build, executed by the gate.
4. **Concepts**: the manifest, process groups, managed and linked weights,
   capabilities, the two providers, and why a studio stays its own repository.
5. **Guides**: wrap a repository with no manifest; record with provenance;
   sessions; timeline hand-off; theming with helm-css; developing in isolation,
   as it is.
6. **Publishing**: the criteria as scored, the levels, and what a registry pull
   request contains.
7. **The four annotated manifests**, keyed by pointer and tested.
8. **The site**:
   - the thesis;
   - the problem made concrete;
   - the launch studios with generated badges;
   - honest platform support;
   - add your own studio;
   - the repository.

   No recording, no `.dmg` and no Homebrew until M9.
9. **The gate**: links, samples, annotations, reference drift and the theme
   lint.

### Definition of Done (draft)

- `make site` builds from a clean checkout. The gate fails on a broken link, an
  unexecuted or failing sample, an annotation out of step, or a stale reference
  page.
- Every code sample on every page is a file the gate runs.
- The reference pages are generated, and no reference page is hand-written.
- Nothing on the site claims what the software does not do. That covers:
  - no mockups and no future tense;
  - unbuilt tools named as unbuilt;
  - platform support as verified.
- `helm validate -theme` passes on the site's stylesheet, and it renders in both
  themes at 1280, 1000 and 380 px with goldens.
- `make gate` is green.
- **Machine-bound:** the quickstart, followed literally on a Mac that has never
  built helmstudio, from a source build, timed. Every assumed step is a finding.
  After M9 it runs again against the released binaries.

### File list (draft)

- `site/**` (new, its own `go.mod`)
- `Makefile`, `docs/agents/gate.md`
- `.gitignore` (`site/out/`)
- `docs/plan/01-build-plan.md` (phase 9, per the answers)
- `docs/decisions.md`, and the milestone report
