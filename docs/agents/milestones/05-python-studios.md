# M5 — Python studios

**Effort:** 6–7 days. **Machine-bound demo:** three studios on one shelf,
switched in turn.

Expanded at kickoff on 2026-09-15 from the draft in
`docs/agents/reports/05-python-studios.md`. The human answered the kickoff's 16
questions with "recommendations for all" and approved this expansion. The
answers are in `docs/decisions.md` under "M5 Python studios".

## Shape

Three things:
- Python environment management with `uv`.
- ltx studio and AuK brought up under it.
- The switch dialog that one heavy studio at a time requires.

M0's manifests showed the schema could already express a Python studio. What
it lacked was an agreed answer to three questions:
- How a build step sees the environment.
- What `busy` answers.
- What happens to work a studio detaches from its group.

## Reads

- `docs/design/01-prd.md` (R8–R13, R21, R25–R27)
- `02-data-model.md` §4 and §7
- `04-packages.md` §4
- `05-sdk-and-custom-studios.md` §2, §5a and §9
- `07-platform-services.md` §7
- `08-h3-dry-run.md`
- `studios/*.yaml`
- M0's report
- `docs/decisions.md` in full

## Known at freeze

- Vendoring `uv` was an open decision. Kickoff Q3 settled it: detect and
  instruct now, bundle with the app in M9.
- The switch dialog uses the optional `busy` probe. Liveness is not occupancy:
  without the probe the dialog guesses, and guessing wrong costs a user 21 GB
  of loaded weights.

## Demo

"Side by side" means installed on one shelf and launched in turn, with one
heavy studio running at a time (Q2). Their peaks together are 67 GB, over
this design's 64 GB.

## Tasks

1. **Environment.**
   - Probe for `uv` (Q3).
   - Create the environment, keep or recreate it (Q4, Q6, Q7), and record it
     in `runtime_env`.
   - Uninstall removes it.
2. **Activation.**
   - Set Q5's environment variables for build steps, processes and `exec`
     probes.
   - Substitute `{venv}`.
   - Remove M2's `{venv}` refusal and M3's `python:` refusal.
3. **Teardown that reaches detached descendants** (Q11 b).
4. **`busy`.**
   - The contract (Q12) and the probe.
   - Conflict details with a confirm digest (Q13).
   - Update the launcher contract, regenerate, and change the plain shelf's
     dialog text.
5. **`helm dev`** takes an existing environment (Q15).
6. **Manifests.**
   - ltx and AuK per Q9, Q10 and Q12, moving to upstream refs once those
     exist.
   - h3's `busy` path per Q12.
7. **Docs.** Amend 01, 02, 04, 05 and 07 per the answers, and add the schema's
   `busy` description.

## File list

- `internal/install/**`, `internal/supervisor/**`, `internal/platform/**`
- `internal/api/**`, `api/openapi.yaml` (launcher operations only), and the
  generator's outputs
- `web/**`, `cmd/helm/**`
- `studios/auk-studio.yaml`, `studios/ltx-studio.yaml`, `studios/h3-studio.yaml`
- `schema/manifest.json` (the `busy` description only)
- `docs/design/01-prd.md`, `02-data-model.md`, `04-packages.md`,
  `05-sdk-and-custom-studios.md`, `07-platform-services.md`
- `docs/decisions.md`, and the report

## Definition of Done

- **Three manifests, one code path.** No Go outside `internal/manifest` tests
  names `ltx`, `auk` or `h3`. `grep -rniE 'ltx|auk|h3' --include='*.go'
  internal cmd` finds only test fixtures.
- **Absent `uv`.** A `python:` manifest with no `uv` on `PATH` is refused
  before the clone, with `tool_missing` naming it. A test covers it.
- **The environment.**
  - It is created at `<data>/studios/<id>/venv` for the declared minor version.
  - A second install reuses it.
  - A changed `python.version` recreates it.
  - Two studios never share it.
  - Uninstall removes it.
  - Each of these has a test using the fake `uv`.
- **Activation.** A build step and a process both see `VIRTUAL_ENV`, `PATH` and
  `UV_*`. A studio without `python:` sees none of them.
- **Stop reaches detached children.** A detached child that ignores SIGTERM is
  SIGKILLed after grace on Stop and on preemption. A recycled pid is never
  signalled. Tests cover both.
- **Unknown is never idle.** A non-conforming `busy` endpoint reads `unknown`,
  never `idle`, and a test covers it.
- **Stale confirms are refused.** A preempt with a stale digest is 409
  `preview_changed`, and no studio is stopped.
- **`make gate` is green**, including drift.
- **Machine-bound.** The switch h3 → ltx → AuK, with `ps` showing no surviving
  render after each switch.

## Review focus

- **One code path.** Any special case in Go for a specific studio means the
  schema is wrong. Three studios, one code path, is the whole point of the
  milestone.
- **Reproducibility.** Is the environment per-studio and reproducible from the
  manifest alone? Does anything read user uv config or pick a system
  interpreter?
- **Absent `uv`.** What happens when `uv` is absent and vendoring was not
  adopted?
- **Signalling.** Can teardown signal a process that is not a verified
  descendant? This is M2's identity rule, now applied beyond the group.
- **What the dialog says.** Can the dialog ever say "idle" when it does not
  know, or stop a studio whose state changed after the user read the dialog?
- **Tests.** Were the tests written from the contract? M4 found two
  daemon-only bugs that the first suite missed.
