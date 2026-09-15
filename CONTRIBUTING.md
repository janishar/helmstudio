# Contributing

## Where the truth lives

`docs/design/` is the frozen design and it is the contract. `docs/decisions.md` is the running record of what was decided and why, including what was deliberately left open. Read both before changing anything.

If a design document is inconsistent, ambiguous or wrong, **raise it and stop**. Do not resolve it in code. Several parts of the system are built against the same sections, and a contradiction resolved quietly in one place becomes divergence in four. Changing a design document is a deliberate act with a decision-log entry, not a side effect of making a build pass.

Append one line to `docs/decisions.md` for every decision that outlives the change that prompted it.

## Commits

Write them the way you would explain the change to someone reviewing it in six months.

- Imperative subject line, under 72 characters, no trailing period.
- A body when the change needs one: what changed and, more importantly, **why**. The diff already says what.
- Reference the design section a change implements when there is one.
- **No trailers.** A commit is a record of a change to this project, not of how it was produced.
- One logical change per commit. If the subject needs an "and", it is two commits.

## Branches

`feat/<topic>` for features, `fix/<topic>` for fixes, `chore/<topic>` for everything else. Work lands on a branch, the gate runs, then it merges.

## Before committing

`make gate` must pass. A change is not done because the code is written; it is done when the gate is green.

Anything the gate cannot check — anything needing a GPU, real weights, Metal, or a signed binary — is verified by hand on an Apple Silicon Mac and noted in the pull request rather than assumed. The gate runs on Linux too, deliberately: a case-insensitive filesystem hides path bugs that a Linux run surfaces immediately.

## Code

Go, stdlib-first. A new dependency is a decision — say so explicitly and record why.

Never edit generated code by hand. Change the generator, or the document it generates from, and regenerate. The gate regenerates and fails on a diff, so a hand edit is caught rather than merged.
