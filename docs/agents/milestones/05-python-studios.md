# M5 — Python studios

**Effort:** 6–7 days. **Machine-bound demo:** three studios side by side.

> **Scoped, not specified.** This milestone is expanded to M0-level detail at
> its own kickoff, when the two things a good brief needs both exist: the
> contract it builds against, and the previous milestone's review findings.
> Expanding it now would mean guessing, and a guessed brief is worse than none
> because it reads as authority. Expand it with the stronger model, and carry
> forward the review focus that earlier milestones actually produced.

## Shape

Python environment management with `uv`, then ltx-2-studio and AuK brought up
under it, then the switch dialog that one-heavy-at-a-time requires.

Its real shape depends on what M0's four manifests revealed: whether the
manifest can already express what a Python studio needs, or whether it cannot.

## Reads

`docs/design/01-prd.md`, `05-sdk-and-custom-studios.md`, `studios/*.yaml`,
M0's report.

## Known at freeze

- Vendoring `uv` is an **open decision**: bundle a pinned binary versus detect
  and instruct. It is not the implementer's to close.
- The switch dialog uses the optional `busy` probe. Liveness is not occupancy —
  without it the dialog guesses, and guessing wrong costs a user 21 GB of
  loaded weights.

## Review focus

- **Any special case in Go for a specific studio means the schema is wrong.**
  This is the whole point of the milestone: three studios, one code path.
- Is the environment per-studio and reproducible from the manifest alone?
- What happens when `uv` is absent and vendoring was not adopted?
