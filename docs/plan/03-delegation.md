# Delegating the build

How work is split when it is handed to someone — or something — that has no
memory of the last conversation.

## What makes a task delegable

**A written contract**, so that correctness has an answer. **A mechanical
verification**, because anyone optimises for the check they are given, and
"looks right" produces code that looks right. **A bounded surface**, because
the failure mode of a capable worker is not incompetence, it is scope creep.

The manifest schema, the OpenAPI document, the conformance suite and the golden
tests were already right as engineering. Under delegation they are the
substrate that makes delegation safe, which is why they are front-loaded harder
here than a solo plan would put them.

## The hard constraint

**Nothing that touches a model can be verified anywhere but on the machine.**
Split every brief accordingly.

- **Verifiable anywhere** — schema and manifest validation, the store and its
  migrations, supervisor logic with fake children, the HTTP layer, SDK clients
  and conformance, helm-css and visual regression, docs, most unit tests.
- **Machine-bound, serial, one at a time** — the same constraint the product
  itself enforces: spawning real studios, Metal builds, weight downloads, model
  loads, memory measurement, ffmpeg on videotoolbox, real screenshots.

The rhythm that follows: contract-verified work accumulates through the day,
then **one integration window on the Mac**, late enough that there is something
to integrate. Keep it sacred. It is the only place the product is proven.

## Six roles

Six briefs, not necessarily six people.

**Contract author** — rare; the output is a specification, never code. A
specification written by its implementer describes what is easy, not what is
right.

**Implementer** — one bounded slice, isolated working tree, never edits tests
it did not write, never changes a schema.

**Test author** — adversarial; writes failing tests from the contract *without
reading the implementation*. A suite derived from the code proves only that the
code does what the code does.

**Reviewer** — reads a diff, produces findings, changes nothing. Ask
specifically for contradictions with the frozen design; that is where the value
is concentrated.

**Mechanical** — token migration, client regeneration, manifest authoring, docs
generation. Unambiguously better delegated than done by hand.

**Explorer** — read-only, answers one question. The h3 dry run was this.

## Brief shape

GOAL, one sentence — if it needs two, it is two tasks · CONTRACT, the frozen
artefacts by path · TOUCH and DO NOT TOUCH · DONE, the exact commands that must
pass · OUT OF SCOPE · **IF THE DESIGN IS WRONG: stop, report the contradiction
with a quotation, propose a change. Do not improvise a schema field, rename an
endpoint, or relax a test.**

That last block is the highest-value paragraph in any brief. Anyone capable,
handed an inconsistent design, *will* resolve it — silently, plausibly, and
differently from the other four people resolving the same inconsistency.
Required escalation turns a design flaw into one question instead of five
divergent implementations.

## Verification ladder

Run in order, stop at the first failure.

1. gofmt, go vet, **dependency diff** — catches a dependency added quietly.
2. Unit tests for the touched package.
3. `helm validate` on all four manifests — catches a schema change that broke a
   real studio.
4. **Generated-client drift** — regenerate and diff. Hand-editing generated
   code is the classic shortcut.
5. Conformance, both providers.
6. Visual regression, both themes — catches a token rename restyling four
   studios.
7. The Linux build — path and case assumptions.
8. **Integration on the Mac** — everything that actually matters.
9. Review against the frozen design — code that passes every check and
   contradicts the design anyway.

Rungs 1–7 must be one command and one CI workflow. If running the gate is more
effort than reading the diff, it stops being run and the whole model collapses.

## Two rules

**Nobody merges their own work.** The output is a branch and a report; the gate
decides.

**A failure returns the failing check**, not "this isn't right". "Conformance
case `etag_conflict_returns_409` fails" is actionable. Vagueness produces a
rewrite of the wrong thing.

## Parallelism by phase

Phase 0, manifests — four explorers and four mechanical tasks; near-ideal.
Phase 1, supervision — mostly not delegable; the skeleton is indivisible.
Phase 2 — three slices: the git and build runner, the HF downloader, linked
weights. Phase 3 — write the OpenAPI yourself, then fan out hard. Phase 4 —
half and half. Phase 5 — highly parallel: each screen, each component, each CSS
layer, the h3 token migration. Phase 6 — parallel: editor, import and export,
criteria, approval. Phase 7 — **least delegable**; ffmpeg work is empirical and
machine-bound, so the golden harness is delegated and the filter graph is not
(note, M8 Q3: the implementer drafts the graph as a pure function under the
golden harness, and the human signs it off on the Mac). Phase 8 — mixed;
signing is yours, because the certificates are. Phase 9 — the most parallel of
all.

**Throughput rises sharply once a phase's contract exists and is near zero
before it.** Which is another reason not to compress phase 3: a rushed contract
does not merely cost later, it removes the ability to parallelise at all.

## Never delegated

Schema and API shape · security posture — tokens, capabilities, Host and Origin
checks, what the approval screen shows · licensing, including any new
dependency · deleting or weakening a test, which is a finding and not an edit ·
anything destructive on the machine — nothing touches a models directory or
runs reclaim · design judgement; the frozen design is implemented, not revised.

## Mechanics

Isolated working trees rather than shared branches, so that two pieces of work
on adjacent files never collide. Batch review by gate rather than by author —
reviewing a diff that has not passed rung 5 is wasted attention. And **the
decision log is the shared memory**: there is no continuity between runs, so
anything not written down is re-decided, differently, every time. One
append-only file handed into every brief is what keeps a dozen separate runs
coherent.

## Honest ceiling

Delegation does not compress phase 1, which is an indivisible skeleton, or
phase 7, which is empirical. Expect sixteen weeks to become roughly ten or
eleven, not four. The gain concentrates in phases 0, 3, 5, 6 and 9 — the phases
with the clearest contracts. That correlation is the whole thesis.
