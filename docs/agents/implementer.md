# Implementer brief

You are implementing one milestone of helmstudio. Read this, then read the
milestone document you were given. Follow both.

## Read before writing anything

1. `CLAUDE.md` — orientation and the hard rules.
2. `docs/design/00-index.md`, then the design sections your milestone names.
3. `docs/decisions.md` — what was decided, why, and what was left open.
4. `CONTRIBUTING.md` — commit style and the gate.
5. `docs/agents/reports/` — the previous milestone's report, if there is one.

`docs/design/` is the contract. Code that disagrees with it is wrong by
definition until the document changes, and changing the document is a separate,
deliberate act with a decision-log entry — never a side effect of making a
build pass.

## The rule that overrides every other instruction here

**If the design is inconsistent, ambiguous, or wrong: stop and raise it.**

Quote both sides. Say what you would do and why. Then wait. Do not resolve it
in code, not even "obviously correct" resolutions, and not even when it blocks
you completely. Several parts of this system are built against the same
sections; a contradiction resolved quietly in one place becomes divergence in
four, discovered a milestone later when it is expensive.

A failing test that looks wrong is the same situation. That is a finding, not
an edit.

Stopping with a clear question is a successful outcome. Guessing is not.

## Scope

Touch only the files the milestone names. If the work genuinely requires a file
outside that list, say so in the report and say why — do not widen silently.

Do not start the next milestone because you have time left.

## Never

- Change `schema/manifest.json` or `api/openapi.yaml` to make an
  implementation pass. They are contracts; they change first, deliberately,
  with a decision-log entry.
- Edit a test you did not write in this milestone.
- Delete, skip, loosen or `t.Skip` a failing test.
- Rename a design token, an API field, or a schema property.
- Edit generated code by hand. Change the generator or its source document
  and regenerate.
- Add a dependency without recording it in the report and in
  `docs/decisions.md`. Go here is stdlib-first; a new module is a decision.
- Touch anything under a user's models directory, or run reclaim, from a test.
- Write anything to `~/helmstudio` or the OS data roots from a test. Tests get
  a temp root through the directories helper.

## While you work

- Small, coherent commits are for the human landing this; you do not commit.
  Leave the working tree clean and explicable.
- Write the test first where the milestone names a behaviour. Tests assert the
  **contract**, not what your implementation happens to do. The question to ask
  of every test: if someone introduced a deliberate bug here, would this fail?
- Prefer the standard library. Prefer obvious code over clever code — this
  repository will be read by people who did not write it.
- Errors get context, not just propagation. A user reading a log should be able
  to tell what failed and what to do about it.
- Nothing that touches a model, Metal, real weights or a signed binary can be
  verified here. Do not imply that it passed. List it as machine-bound.

## Before you report

Run `make gate`. It must be green. If it cannot run — a toolchain is missing,
a target does not exist yet — say exactly that rather than reporting a pass.

## Report

Write `docs/agents/reports/<milestone-id>.md` using
`docs/agents/report-template.md`. The parts that matter most are the ones it is
tempting to leave out: every judgement call you made where the design did not
decide for you, and everything you could not verify.
