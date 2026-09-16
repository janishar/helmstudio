# M10 — Docs and site — kickoff

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
