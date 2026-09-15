# M5 — Python studios — implementation report

## What was built

A studio that declares `python` now installs and runs inside its own uv
environment:
- **Where it lives.** `<data>/studios/<id>/venv`, outside the checkout.
- **How it is made.** Before step 0, with a managed interpreter, helmstudio's
  uv cache and no user uv config.
- **Reuse and cleanup.** Kept while the Python matches, remade when it does
  not, removed at uninstall.
- **Activation.** Active in every build step, process and `exec` probe.
- **When `uv` is missing.** Install is refused before the clone.

Three more pieces:
- **Stopping a studio** now also stops the processes it detached from its
  group, the way ltx starts renders.
- **The switch dialog** asks the running studio's `busy` probe under a written
  contract, where unknown is never idle. Its confirmation is a digest, refused
  when the running studio changed after the user read it.
- **`helm dev`** runs a Python studio in the author's own environment.

No Go code names a studio. Three manifests, one code path.

Work stopped at kickoff on 16 questions, kept below under "History". The human
answered "recommendations for all" and approved the expansion, now in
`docs/agents/milestones/05-python-studios.md`.

## Gate

    $ rm -rf bin && go clean -testcache && make gate   # after the first review
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since bb7faaf
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm	2.130s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.207s
    ok  	github.com/janishar/helmstudio/internal/api/studioapi	0.759s
    ok  	github.com/janishar/helmstudio/internal/install	11.160s
    ok  	github.com/janishar/helmstudio/internal/manifest	0.709s
    ok  	github.com/janishar/helmstudio/internal/media	0.998s
    ok  	github.com/janishar/helmstudio/internal/platform	0.909s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	1.290s
    ok  	github.com/janishar/helmstudio/internal/supervisor	41.354s
    ok  	github.com/janishar/helmstudio/internal/weights	1.575s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ?   	github.com/janishar/helmstudio/schema	[no test files]
    ?   	github.com/janishar/helmstudio/web	[no test files]
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go	0.561s
    ?   	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded	[no test files]
    ok  	github.com/janishar/helmstudio/test/conformance	2.808s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

This is macOS on Apple Silicon only, as decided in M1. `vet-linux` type-checks
the new `/proc` process table and runs nothing.

Outside the gate:
- **Race detector.** `go test -race -count=1` over every new and edited test
  in `internal/supervisor`, `internal/install`, `internal/api` and `cmd/helm`
  is clean.
- **Real `uv`.** `HELM_REAL_UV=1 go test ./internal/install -run RealUV`
  passed against uv 0.12.1. It downloaded a managed CPython 3.11.15 under the
  test's data root, and `python` in the build step ran from the environment.
- **ltx's lock file.** `uv lock --check` in the ltx checkout at the pinned
  commit reports its `uv.lock` current, so `uv sync --locked` will not fail on
  a stale lock.

### The Definition of Done, item by item

| DoD | Evidence |
|---|---|
| **Three manifests, one code path.** `grep -rniE 'ltx\|auk\|h3' --include='*.go' internal cmd` finds only test fixtures | After the first review (#11), it finds nothing outside `_test.go`. Before that it found two comments from M2, which were reworded. No code path depends on a studio id. |
| **Absent `uv`.** Refused before the clone, with `tool_missing` naming it | `install` `TestNoUVBlocksBeforeTheClone`: 422 `blocked` with `details.code = tool_missing`, `details.tool = uv`, the message naming `brew install uv`; no installation row and no `src/`. `internal/api` `TestInstallRefusalCarriesItsDetails` shows the details reach the client. If uv disappears between that check and the build, the build's tool probe fails with `tool_missing`. |
| **The environment.** Created at `<data>/studios/<id>/venv` for the declared minor | `TestPythonStudioGetsItsOwnEnvironment` (fake uv): exactly one `uv venv --no-project --python 3.11 <venv>`, with the four `UV_*` settings, run outside the checkout; `pyvenv.cfg` names 3.11; `runtime_env` has `venv`, `python` (the interpreter's full version), `uv` and `torch`; a job-owned build log. Since the first review (#1), a step that replaces the environment fails the build: `TestABuildStepThatReplacesTheEnvironmentFailsTheBuild`. |
| A second install reuses it | Same test: a second install on a ready studio does not call uv. `TestEnvironmentFollowsTheManifestAndGoesWithUninstall`: a manifest change that keeps the Python rebuilds both steps and still does not call `uv venv`. **This case was added after planted bug 1 went uncaught.** |
| A changed `python.version` recreates it | Same test: 3.11 → 3.10 remakes it as 3.10 and leaves the other studio's 3.11 alone. |
| Two studios never share it | Same test: two studios, two `uv venv` calls at two paths. |
| Uninstall removes it | Same test: the venv is gone; `data/` and the other studio's venv remain. |
| **Activation.** A build step and a process see `VIRTUAL_ENV`, `PATH`, `UV_*`; a studio without `python:` sees none | Build step: `TestPythonStudioGetsItsOwnEnvironment` reads the step's own `env`. Process and exec probe: `supervisor` `TestAPythonStudioRunsInsideItsEnvironment`. The group reaches `running` only because the exec probe saw `VIRTUAL_ENV`; the process's own `env` dump is checked, and a studio without `python:` has none of the variables and `{venv}` refused. `TestPythonConfigAndManifestEnvOverride`: a manifest's `env` wins. |
| **Stop reaches detached children.** SIGKILLed after grace on Stop and on preemption | `TestStopReachesAChildThatLeftTheGroup`: a child started with `setsid` that ignores SIGTERM is outside the studio's group, receives SIGTERM, and is gone after Stop. `TestPreemptionReachesAChildThatLeftTheGroup`: gone before the preempting launch returns. Removing the process-table snapshot fails both, and I checked that. |
| A recycled pid is never signalled | `TestARecycledDetachedPIDIsNeverSignalled`. A real stranger (a group leader with a detached child) is walked when its identity verifies. It is not walked when the recorded start time differs. A detached identity with the wrong start time is not killed. `TestDetachedDescendantsWalk` covers depth, in-group children, unrelated processes and self-parented entries. |
| **Unknown is never idle** | `TestBusyProbeReadsTheContractAndNothingElse`: ten answers, including ltx's and h3's JSON list and AuK's health body. Every non-conforming one is `unknown`, and its message never contains "no work in flight". `TestNoBusyProbeIsUnknown`, `TestGroupBusyFoldsItsMembers`. |
| **Stale confirms are refused** and nothing is stopped | `TestAStaleConfirmationStopsNothing`: idle then busy gives `preview_changed` with the busy state; `true` and a made-up digest are refused; the running studio keeps its pid; progress moving alone still confirms. `internal/api` `TestAStalePreemptIsPreviewChanged` over HTTP: the group run is unchanged. |
| **`make gate` is green**, including drift | Above. `api/openapi.yaml` changed a shared schema description, so `make generate` rewrote a comment in `zz_types.go`; drift is clean. |
| **Machine-bound.** h3 → ltx → AuK, no surviving render | **Not verified: machine-bound.** Script under "Could not verify". |

### Planted bugs

Each was applied alone and the related tests run; the file was restored after
each, and a grep confirmed no mutation was left behind. The first run caught 12 of 14:
- **Bug 8** was planted badly: `loaded` is overwritten when the studio reports
  it, so the mutation changed nothing. Rewritten to change the digest itself,
  it was caught.
- **Bug 1** was a real gap: a second install on a ready studio never reaches
  the build, so always remaking the environment passed. The rebuild case above
  was added, and bug 1 was then caught.

| # | Bug | Caught by |
|---|---|---|
| 1 | Environment remade on every build | `TestEnvironmentFollowsTheManifestAndGoesWithUninstall` (**after the added case**) |
| 2 | Uninstall keeps the environment | `TestEnvironmentFollowsTheManifestAndGoesWithUninstall` |
| 3 | Build steps not activated | `TestPythonStudioGetsItsOwnEnvironment` |
| 4 | Processes not activated | `TestAPythonStudioRunsInsideItsEnvironment`, `TestPythonConfigAndManifestEnvOverride` |
| 5 | No uv check before the clone | `TestNoUVBlocksBeforeTheClone`, `TestHostMismatchBlocksAndShortfallsWarn` |
| 6 | A body without `busy` read as idle | `TestBusyProbeReadsTheContractAndNothingElse` |
| 7 | Any confirmation accepted | `TestAStaleConfirmationStopsNothing`, `TestAStalePreemptIsPreviewChanged` |
| 8 | Digest covers the studio's message (**re-planted**) | `TestAStaleConfirmationStopsNothing` |
| 9 | Detached pid signalled without its start time | `TestARecycledDetachedPIDIsNeverSignalled` |
| 10 | Group walked without verifying its leader | `TestARecycledDetachedPIDIsNeverSignalled` |
| 11 | One environment for every studio | three tests |
| 12 | `helm dev` gives the author's environment helmstudio's uv settings | `TestHelmDevUsesTheAuthorsEnvironment` |
| 13 | Launch skips the environment check | `TestAMissingOrMismatchedEnvironmentRefusesTheLaunch` |
| 14 | `helm dev` accepts another Python's environment | `TestHelmDevUsesTheAuthorsEnvironment` |

Separately, removing the detached snapshot from `terminate` failed both
detached-child tests.

## First review (helmstudio-b6): 0 BLOCKING, 3 SHOULD-FIX, 9 NOTE

I agreed with every finding. The review marked two of them as the human's to decide. I raised both and recorded them as open; I did not decide them. Each change is a "review (#n)" entry in `docs/decisions.md`.

| # | Change | Test |
|---|---|---|
| 1 | **A build step can replace the environment.** The environment is checked again after the last step. One a step replaced or broke fails `env_failed`, saying so, with no `runtime_env`; every step record is dropped. **Found while fixing it:** an environment made or remade drops the step records so every step runs again, and install on a studio past its build whose environment no longer checks builds again. Before this, a retry skipped `uv sync` against the new environment, and "install again" did nothing. **Human:** whether activation also sets `UV_PYTHON` (Open). | `TestABuildStepThatReplacesTheEnvironmentFailsTheBuild` (a step rewrites `pyvenv.cfg` to 3.14), `TestARemadeEnvironmentRunsEveryStepAgain` |
| 2 | **`runtime_env.python` is the interpreter's own version.** It comes from `platform.python_version()` in the activated environment. Fakes now write `version_info = <minor>`, as uv 0.12.1 does. The Q6 entry gains the patch-drift cost. I confirmed the minor-only `pyvenv.cfg` with a separate probe. My earlier real-uv run had recorded `3.11.15` from `pyvenv.cfg`, so uv does not write it consistently either way; reading the interpreter covers both. | `TestPythonStudioGetsItsOwnEnvironment` expects `3.11.7` from the interpreter and `3.11` in the cfg. `TestRealUVMakesTheEnvironment` asserts a patch component; it passed with 3.11.15. |
| 3 | **Human: uninstall leaves `<data>/python` and `<cache>/uv` (R67).** Recorded as open with both options; not decided. | — |
| 4 | 01 R51 and 04's table and "Provider selection" row amended as in 07 §8. | — |
| 5 | **Busy parsing is strict.** One object, the exact key `busy`, a non-null boolean, typed optional keys. A transport error is worded from the error. | Six more cases in `TestBusyProbeReadsTheContractAndNothingElse` (`{"Busy": false}`, trailing data, two objects, null, a non-boolean `loaded`, `null`). `TestBusyProbeWordsTransportErrorsFromTheError`. |
| 6 | **A member re-adopted without a plan is asked** through the loaded manifest. With no manifest, the reason says that. | `TestARunningGroupWithoutAPlanIsStillAsked` |
| 7 | — | `TestAConfirmationDoesNotCarryOverToARestartedStudio` |
| 8 | — | `TestPreemptionReachesAChildThatLeftTheGroup` now has a child that ignores SIGTERM, and checks it received one. |
| 9 | **The table read must itself show the verified leader** before its entries are trusted. | **Not tested:** it needs the leader recycled between two reads microseconds apart. |
| 10 | **An environment needs `bin/python` to resolve.** Install remakes one that does not; launch and `helm dev` refuse it. | `TestAMissingOrMismatchedEnvironmentRefusesTheLaunch` (a dangling interpreter link), `TestARemadeEnvironmentRunsEveryStepAgain`, and the `helm dev` refusal cases |
| 11 | The two M2 comments are reworded. `grep -rniE 'ltx\|auk\|h3' --include='*.go' internal cmd` finds nothing outside `_test.go`. | — |
| 12 | The report now lists the `JobError` description and the regenerated `zz_types.go` under files outside the list. | — |

Planted bugs for the review changes, each caught:
- no check after the build
- step records kept for a remade environment
- a studio past its build never rebuilding a broken environment
- the interpreter link not checked
- `python` read from `pyvenv.cfg`
- the busy key matched case-insensitively
- a digest without the group run
- a planless member not asked

One (`python` from `pyvenv.cfg`) was first planted in a way that did not compile; I re-planted it as a valid change.

## Design contradictions raised

- **At kickoff: 16 questions** (below, under "History"), all answered.
- **While building: one.** 07 §8 said "Every SDK constructor … switches to a
  local backend", which Q14 contradicts for Python and Node. It was already
  superseded for storage in M1. I added an amendment note under Q14's
  authority rather than stopping, because Q14 had decided the substance. **A
  reviewer should confirm that reading.**

## Judgement calls

All are in `docs/decisions.md` under "M5 Python studios". The ones to read
first:

- **What the digest covers.** The running studio, its group run, the wanted
  studio, the busy state and `loaded`; not progress or message. A digest over
  progress could never be confirmed during a render. Cost: a render that goes
  from 10 % to 90 % while the dialog is open confirms under the old reading.
- **When detached processes are found.** They are read from the process table
  only while M2's group identity verifies, and each is signalled by pid and
  start time (`SameProcess`), not group. A detached process may change group
  without becoming another process. The table is read twice, before SIGTERM
  and before SIGKILL. A descendant spawned in the grace period after the second
  read, or re-parented before the first, is missed.
- **`unknown` explains itself.** helmstudio puts its reason in `message`
  ("process "studio": /api/queue did not answer with a JSON object carrying
  "busy""), and the contract documents that.
- **Launch refuses a missing or mismatched environment** as `not_launchable`,
  naming "install <id> again". A re-adopted group whose environment no longer
  checks is watched only.
- **`uv venv` runs in the studio's data directory, with `--no-project`.**
  Neither the checkout's `pyproject.toml` nor `.python-version` can steer
  `uv venv` itself. **A build step can:** `uv sync` remakes the environment
  for the project's Python. The first review (#1) caught this, and the build
  now checks the environment again.
  It gets its own 30-minute timeout (`Config.EnvTimeout`), because it may
  download an interpreter.
- **`helm dev` checks the environment twice:** before creating `./.helm`,
  and at every launch through `Config.Python`.
- **Manifests keep their non-conforming `busy` paths.** The paths read as
  `unknown`, with a comment saying why and what changes the manifest, rather
  than being deleted. Deleting them would say the same thing with less
  information.

## Could not verify

Nothing here touched a model, Metal or real weights.

- **A real ltx install.** On an Apple Silicon Mac, from this branch:
  1. `go build -o bin/helmstudio ./cmd/helmstudio && ./bin/helmstudio`.
  2. `curl -X POST 127.0.0.1:8700/api/v1/studios/ltx-studio:install`. The weights are 71 GB; link an existing copy first with `curl -X POST '127.0.0.1:8700/api/v1/studios/ltx-studio/weights/ltx:link' -d '{"path":"/path/to/LTX-2.5"}'`.
  3. Expect `~/Library/Application Support/helmstudio/studios/ltx-studio/venv/pyvenv.cfg` to name 3.11.x.
  4. Expect a CPython under `…/helmstudio/python/` and uv's cache under `~/Library/Caches/helmstudio/uv`.
  5. Expect `uv sync --locked --all-extras` to succeed in `build-<job>-00.log`.
  6. `GET /studios/ltx-studio` should show `runtime_env.mlx`.
  7. Launch it, and `lsof -i` should show `python server.py` on the assigned port.
- **AuK.** Install and build can be run the same way. **Its launch cannot use
  any weight until AuK gains flags upstream (Q10).** It starts, but finds no
  checkpoints under its own `ckpts/`. `runtime_env.torch` should be 2.7.x.
- **The switch, h3 → ltx → AuK:**
  1. Launch h3 and start a render.
  2. `POST /studios/ltx-studio:launch`. Expect 409 `heavy_conflict`, a message saying helmstudio "cannot tell whether h3 studio has work in flight", with the reason (h3's `/api/queue` does not serve the contract), and `details.heavy.confirm`.
  3. Post that digest as `preempt`. Expect h3 to stop.
  4. `ps -axo pid,pgid,sess,rss,command | grep -E 'h3studio|/h3 '` shows nothing.
  5. In ltx, start a render: `python -m ltx_pipelines_mlx`, in its own session.
  6. Launch AuK and confirm.
  7. The render receives SIGTERM, and after 30 s of grace SIGKILL. `ps -axo pid,pgid,sess,rss,command | grep ltx_pipelines` is empty before AuK spawns, and memory is released (Activity Monitor or `vm_stat`).
  8. The daemon log names the detached pid ("left group … but descends from it").
- **A render re-parented before teardown.** Kill ltx's server with `kill -9`
  mid-render; expect the render to survive, as recorded open.
- **Linux.** `ProcessTable`, `TerminateProcess` and `KillProcess` compile for
  Linux (`vet-linux`); none of it ran there.
- **uv versions other than 0.12.1**, and a proxy or private index in uv's user
  config (now ignored).

## Dependencies added

None. `golang.org/x/sys/unix.SysctlKinfoProcSlice` is in the version already
required. The Python package lost its empty `embedded` extra.

## Files touched outside the milestone's list

- **`packages/helm-runtime-sdk/python/pyproject.toml`.** It declared an empty
  `embedded = []` with a comment deferring the choice to M5. Q14 removed the
  extra, and leaving it would contradict that. Two lines.
- **`api/openapi.yaml` `JobError` description and
  `packages/helm-runtime-sdk/go/zz_types.go`.** `JobError` is also the
  studio API's `jobs.last_error`, not a launcher-only schema. Adding
  `env_failed` to its description regenerated a comment in the runtime SDK,
  a module tagged v0.4.0. It was regenerated with `make generate`, not edited
  by hand. Listed here after the first review (#12).
- **`internal/manifest/types.go`.** A comment on `Busy` that said nothing calls
  it. `internal/manifest/**` is not in the list; the comment would otherwise be
  false.
- **`docs/agents/milestones/05-python-studios.md`.** Replaced with the approved
  expansion (Q1).
- **Tests written before M5 and edited.** Listed in `docs/decisions.md`, last
  M5 entry, for sign-off:
  - M2 `supervisor_test.go` and `review_test.go`
  - M2 `internal/api/api_test.go`
  - M3 `install_test.go`
  - additive helper flags in `helper_test.go`

  No assertion was removed; each keeps its check under the new contract.

## Decision-log entries appended

In `docs/decisions.md`:
- **"Open, not yet decided":** nine "Raised in M5" entries —
  - build-time network hosts
  - `python.extras`
  - AuK's weights
  - the busy endpoints upstream
  - ltx's SIGTERM handler and re-parented renders
  - `uv venv` not identity-recorded
  - no minimum uv version
  - the digest-check window
  - Python async
- **"2026-09-15 · M5 Python studios":**
  - one entry per kickoff answer, Q1–Q16 (Q12, Q13 and Q15 as sub-lists)
  - three judgement calls
  - the test edits
- **"Changes":** one line.

After the first review:
- **Open:** two more entries for the human: `UV_PYTHON` in activation (#1), and shared Python files left by uninstall (#3).
- **The M5 section:** nine "review (#n)" entries.

`git diff main -- docs/decisions.md` shows 84 added lines and no removed
lines.

## Left undone

- **Machine-bound:** the three-studio switch and real installs (above).
- **Upstream, outside this repository:**
  - AuK's weight flags (Q10)
  - a `busy` endpoint in h3, ltx and AuK (Q12)
  - a SIGTERM handler in ltx (Q11 a)
  - SDK adoption in ltx and AuK (Q16)

  When each lands, its manifest's `ref` and `cmd` or `busy` path change.
- **Deferred by decision:** a pinned `uv` in the app (M9); Python async; the
  styled switch dialog (M6). The plain shelf shows the message in `confirm()`.
- **Committing.** Per `implementer.md`, the work is uncommitted on
  `feat/python-studios`.

---

## History: the kickoff as it was put to the human

Work stopped before task 1, on `feat/python-studios`. The only change in the
tree is this file. `make gate` on the untouched tree reports `gate: green`.

**Why it stopped.** `docs/agents/milestones/05-python-studios.md` says it is
"Scoped, not specified" and is "expanded to M0-level detail at its own
kickoff". It has no tasks, no file list and no Definition of Done.
`implementer.md` says "Touch only the files the milestone names", and this
milestone names none. On top of that, several questions under it belong to the
human. One of them, vendoring `uv`, the milestone itself says "is not the
implementer's to close".

Each question below quotes both sides and gives a recommendation. Replying
"recommendations for all" is enough to go ahead. The defaults at the end are
decisions I will make myself unless told otherwise. The proposed expansion at
the end is written to go into the milestone document once the answers are in.

Section references: 01 PRD, 02 data model, 04 packages, 05 SDK, 07 platform
services, 08 h3 dry run. "Open: …" means an entry under "Open, not yet decided"
in `docs/decisions.md`.

---

### A · The brief itself

#### Q1. Who writes M5's contract

- The milestone: "Expand it with the stronger model, and carry forward the
  review focus that earlier milestones actually produced."
- `docs/agents/README.md`: "One agent implements a milestone. A different agent
  reviews it", and the stronger model is used "for expanding a scoped
  milestone".
- `implementer.md`: "Touch only the files the milestone names."

If an implementer writes its own file list and DoD and then builds against
them, nobody has checked the brief.

**Recommendation:**
- Treat the "Proposed expansion" at the end as a draft.
- The human, or a separate expansion or review pass, edits it and puts it into
  `docs/agents/milestones/05-python-studios.md`.
- Implementation starts only once that is done.

#### Q2. "Three studios side by side" against one heavy studio at a time

- Milestone demo: "three studios side by side".
- R25: "Only one group marked `heavy` runs at a time."
- h3, ltx and AuK are all `heavy: true`, with `peak_ram_gb` of 21, 21 and 25.
  That totals 67 GB, above the 64 GB this design targets.

Taken literally, the demo needs the rule to break.

**Recommendation:** the demo is all three studios installed on one shelf and
launched in turn. Each switch goes through the switch dialog, and one studio
runs at a time.

---

### B · `uv` and the environment

#### Q3. Vendoring `uv` (the human's decision)

- 01 §15: "Pinned binary in the bundle, or detect and instruct". The lean is
  "Ship it in the app, detect for the headless binary."
- There is no app until M9.
- Review focus: "What happens when `uv` is absent and vendoring was not
  adopted?"

**Recommendation: detect and instruct in M5. Bundling moves to M9 with the
app.**

- **How `uv` is found.** From `PATH` today. M9 adds a bundled path, which is
  checked before `PATH`.
- **When it is required.** Any manifest with `python:` needs `uv`, whether or
  not `requires.tools` lists it. This follows from the field being present;
  no studio is named.
- **When it is missing.** Install is refused before the clone, as R11 does for
  any tool: 422 `blocked`, code `tool_missing`. The message names `uv` and how
  to install it (`brew install uv`, or the uv documentation URL; not a
  `curl | sh`).
- **What is recorded.** `uv --version` goes into `runtime_env`. No minimum
  version is enforced: only 0.12.1 was available to test here.

#### Q4. Where a studio's environment lives

- R12: "a per-studio `uv` environment pinned to `python.version`".
- The schema: "the daemon create[s] a per-studio uv environment … before any
  build step".
- Neither says where it lives.
- uv's own convention is `<project>/.venv`, and both repositories gitignore
  `.venv/`.

Options:

- **(a) `<root>/.venv`.** Uninstall already removes it with `src/`. But a
  `local_path` studio or a `helm dev` checkout is the author's own tree. The
  daemon would overwrite the `.venv` the author built with a different Python.
- **(b) `<data>/studios/<id>/venv`.** It is outside the checkout and owned by
  helmstudio. Uninstall has to remove it as well.

**Recommendation: (b).** helmstudio never writes into a user's checkout, which
is the same argument as "nothing symlinked into a studio checkout". This
amends M3's Q11 entry ("uninstall removes exactly `<data>/studios/<id>/src`")
to also remove `venv`.

#### Q5. How build steps and processes see the environment (Open: "How a `build[]` step sees…")

- ltx's `uv sync` and `uv run`, and AuK's `uv pip install` and `python3`, all
  assume an environment is already active.
- `{venv}` is documented for `process.cmd` only.
- `build[].run` documents no substitutions.

**Recommendation: the daemon activates the environment rather than asking
manifests to name it.**

- **What is set.** Every build step, process and `exec` health probe of a
  studio with `python:` gets:
  - `VIRTUAL_ENV=<venv>`
  - `UV_PROJECT_ENVIRONMENT=<venv>`
  - `PATH=<venv>/bin:` followed by the allowlisted `PATH`.
- **The template.** `{venv}` stays a `cmd` substitution, as the schema says,
  and `build[].run` gets no substitutions. There is no schema change.
- **Scope.** Nothing here reads `pyproject.toml` or `uv.lock`. What to run is
  the manifest's business.

#### Q6. Interpreter, uv cache, user uv config

Three things decide whether an environment is "reproducible from the manifest
alone" and whether "Uninstall is complete" (R67):

- **The interpreter.** By default uv may pick a Homebrew 3.11 or download a
  managed one (from github.com). A Homebrew upgrade can then break the venv,
  and two machines get different patch versions.
- **The cache.** It defaults to `~/.cache/uv`, outside helmstudio's roots.
- **User config.** `~/.config/uv/uv.toml` still applies because `HOME` passes.
  A user's index or resolution settings then silently change what installs.

**Recommendation:**
- `UV_PYTHON_PREFERENCE=only-managed`.
- `UV_PYTHON_INSTALL_DIR=<data>/python`. It goes under data, not cache,
  because a purged interpreter breaks every venv linked to it.
- `UV_CACHE_DIR=<cache>/uv`, shared between studios. It is a cache, and the
  environments stay separate.
- `UV_NO_CONFIG=1`.
- Proxy variables still pass (M2).

The cost: someone who relies on a private index in their uv config cannot
install. The outbound hosts this adds (github.com for interpreters, pypi.org,
files.pythonhosted.org) are recorded in the decision log. Whether a manifest's
`network` must list build-time hosts is left open.

#### Q7. Environment creation in the install state machine

02 §7 has `building → built` running `build[]`. `step_runs` rows are indexed by
`build[]` position, and "step n of m" is shown from them.

**Recommendation:**

- **When it runs.** At the start of `building`, before step 0, and not as a
  `step_runs` row.
- **Its log.** Written to a log owned by the install job (owner kind `job`,
  which exists since M4).
- **Reuse.** The environment is kept when its `pyvenv.cfg` reports the declared
  minor version, and recreated otherwise.
- **Failure.** The install ends `failed_build` with `last_failure.code =
  env_failed` and a null `step_index`. The API enum and 02 §7 are amended.
- **Retry.** A changed `python.version` changes the manifest digest, so M3's
  rule already reruns everything.
- **What `runtime_env` records** (02 §4):
  - `python` (the full version), `venv`, `uv`.
  - The installed version of the framework the manifest declares. A table maps
    the `runtime.framework` enum to a distribution name: pytorch→torch,
    mlx→mlx, jax→jax, onnx→onnxruntime. Nothing is recorded for
    `native-kernel`, `ggml` or `other`.
- **Superseded.** M3's Q15 entry, which refuses a `python:` manifest, goes.

#### Q8. `python.extras` has no meaning

The schema declares `extras: [string]` with no description. Nothing reads it,
neither manifest uses it, and ltx puts `--all-extras` in its own build step.

**Recommendation:** M5 ignores the field. Raise it as a schema question for
later: describe it or remove it. That is a schema change and not M5's to make.

#### Q9. The manifests' own uv commands

- **ltx's install.** `build: uv sync --all-extras` may rewrite `uv.lock` in the
  checkout. `--locked` would fail instead.
- **ltx's launch.** `cmd: uv run python server.py …` syncs the environment at
  every launch by default. A launch can then reach the network and change the
  environment it runs in.
- **AuK.** It has no lock, so `uv pip install -e .` resolves at install time.

**Recommendation:**
- Fix it in the manifests, not with daemon-wide `UV_FROZEN`/`UV_NO_SYNC`, so
  the approval screen shows what really runs.
  - ltx: `uv sync --locked --all-extras`, and `cmd: python server.py …`, since
    the venv is on `PATH`.
- AuK stays as it is, and the record says plainly that its dependencies are
  reproducible only to the Python minor version and whatever resolves that day.

---

### C · What ltx and AuK cannot do as written

#### Q10. AuK cannot be given any weight

- `web/server.py:39`: `CKPTS = ROOT / "ckpts"`, a hard-coded path in the
  checkout with no flag or variable. M0 reported this.
- The frozen decision: "absolute paths substituted into commands; nothing
  symlinked into a studio checkout".

No manifest can make AuK find weights helmstudio downloaded.

Options:
- **(a)** AuK gains flags upstream, for example `--auk-dir {models.auk}
  --auk-flash-dir {models.auk_flash} --qwen-dir {models.qwen}`, and the
  manifest moves to that ref. 08 did the same for h3.
- **(b)** A schema field that links weights into the checkout. This reverses a
  frozen decision.

**Recommendation: (a).** It is a change in the AuK repository, which is not in
this one. It blocks AuK's machine demo and nothing in the gate.

#### Q11. Stopping ltx orphans its render (Open: "a studio that sets `Setpgid` on its own children")

- ltx `web/server.py:765` starts each render with `start_new_session=True`, and
  the server installs no SIGTERM handler. Its `_stop` runs only on the user's
  Cancel.
- R26: SIGTERM "to each group".
- Criterion 7: "Exits cleanly on SIGTERM … leaving no children".

So Stop, or a confirmed switch, kills `server.py` and leaves
`python -m ltx_pipelines_mlx` running outside the group with the model
resident. The switch dialog would then start AuK on top of it. The one heavy
studio rule is the main thing M5 is meant to show, and it would not hold.

Options:
- **(a)** ltx adds a SIGTERM handler that stops its render (upstream). Every
  studio that detaches a child needs the same fix.
- **(b)** Before signalling a group, the supervisor lists the leader's
  descendants by parent pid (a new `internal/platform` process-table seam),
  records each one's pid and start time, and sends the same SIGTERM, grace and
  SIGKILL to any verified descendant outside the group. No studio is named.
  This extends M2's identity rule to processes helmstudio did not spawn
  directly.
- **(c)** Accept it, document criterion 7 as the only guarantee, and have the
  dialog never claim memory was freed.

**Recommendation: (b) in helmstudio, plus (a) upstream for ltx.** (b) also
closes the same gap for h3's renders, recorded in M2. Descendants re-parented
to launchd before the snapshot are still missed, and the decision log will say
so.

---

### D · The switch dialog

#### Q12. `busy` needs a response contract (Open: "`busy`'s response has no contract")

- Schema: `busy.path` "Returns whether the studio currently holds a model or
  has work in flight."
- 08: "the switch dialog can say 'h3 studio is 40 % through a render'".
- The milestone: "without it the dialog guesses, and guessing wrong costs a
  user 21 GB of loaded weights."
- All three current endpoints answer 200 whatever the studio is doing: h3 and
  ltx `/api/queue`, AuK `/api/health`.

"Holds a model" and "has work in flight" are two facts. Stopping the first
costs a reload; stopping the second loses a render.

**Recommendation:**

- **The shape.** `GET <busy.path>` answers 200 with
  `{"busy": bool, "loaded"?: bool, "message"?: string, "progress"?: number
  0..1}`.
- **Unknown.** Anything else is `unknown`: another status, a body that does not
  parse, `busy` missing, or no answer within 2 s. The dialog shows `unknown`
  as "can't tell". It is never shown as idle.
- **Where it is written.** In the `busy` description in `schema/manifest.json`
  (a description-only change, with a decision entry) and in 01 and 07.
- **The studios.** None of the three endpoints conforms. Each studio adds one
  upstream, and until then its current path reads as `unknown`.

#### Q13. What the API gives the dialog, and what a confirm means

- Today (M2): 409 `heavy_conflict` carries the arithmetic in `details.heavy`,
  and `:launch?preempt=true` stops whatever is running.
- R25: "on confirm, stops the first".

A dialog shown while a studio was idle, then confirmed after a render started,
would kill that render.

**Recommendation:**
- `details.heavy` gains, for each running heavy group, `busy: {state:
  busy|idle|unknown, loaded, message, progress}` and a `confirm` digest over
  that snapshot.
- `:launch?preempt=<digest>` replaces `preempt=true`. If the snapshot has
  changed, the answer is 409 `preview_changed` with fresh details, as M3 and
  M4 reclaim already do.
- Stopping a busy studio stays allowed on confirm.
- The launcher part of `api/openapi.yaml` changes, and the plain shelf shows
  the new text. Styling is M6's.

---

### E · Packages and `helm dev`

#### Q14. Python's `[embedded]` extra, "decided with the Python studios"

- 04 §4 (amended in M4): "Python's `[embedded]` extra is decided with the
  Python studios."
- 05 §2 and R52: an embedded provider is shipped as an extra.
- M4 Q25: a second embedded provider "would duplicate the enforcement the suite
  exists to hold still".

Options:
- **(a)** No Python embedded provider. A standalone Python studio runs under
  `helm dev`. `Helm.from_env()` without `HELM_API` raises `Unavailable`, and
  the message names `helm dev`.
- **(b)** The extra starts a `helm dev`-like Go binary as a subprocess.
- **(c)** A Python implementation, which duplicates the enforcement.

**Recommendation: (a).** Remove the extra from 04 §4, 05 §2 and R52. Python
async (07 §7) stays open, since neither studio needs it for the demo.

#### Q15. `helm dev` for a Python studio

M4 Q26: `helm dev` "runs no `build[]`". A Python studio still needs an
environment with its dependencies installed.

**Recommendation:**
- `helm dev` takes the author's environment from `VIRTUAL_ENV`, or from
  `-venv <dir>`, and checks its `pyvenv.cfg` against `python.version`.
- Without one, it refuses and names the command that makes one.
- It never creates or changes an environment.
- It activates the environment exactly as Q5 describes.

#### Q16. Do ltx and AuK adopt the runtime SDK in M5?

The milestone says the studios are "brought up under it". It does not say they
record outputs.

**Recommendation:** "brought up" means install, build, launch, health and
`busy`. Adopt-and-record call sites for ltx and AuK go into the report as
machine-bound scripts, as M4 did for h3. They are not DoD items, because the
changes are in repositories outside this one.

---

### Defaults I will take unless told otherwise

- **Probing `busy`.** Every member that declares `busy` is probed, in parallel,
  only when a heavy conflict is being built. A probe never changes process or
  health state.
  - A group is `busy` if any member is.
  - Otherwise it is `unknown` if any member is unknown.
  - Otherwise it is `idle`.
- **Creating the environment.** `uv venv --python <version> <venv>`, with no
  `--seed`, since both studios install with uv rather than pip.
- **Tests.**
  - A fake `uv` script on `PATH` records argv and environment and writes a
    `pyvenv.cfg`, so the gate needs no network.
  - A real-`uv` test runs only when `HELM_REAL_UV=1` is set, and is listed as
    unverified otherwise.
  - A fake studio stands in for ltx's orphaned render: it starts a child with
    `setsid` and ignores SIGTERM for that child.
  - Tests use temp roots through the directories helper and never touch a
    models directory.
- **Descendant tracking (Q11 b).** On darwin, `kern.proc.all` through
  `golang.org/x/sys/unix`, which is already a direct dependency. On Linux,
  `/proc/*/stat`. No new module.

### Notes, no decision needed

- **Existing open entries this kickoff would resolve:** vendoring `uv` (for
  M5), `busy`'s contract, how a build step sees the environment, and Python's
  `[embedded]` extra.
- **Open entries it leans on but does not resolve:** `heavy` on a thin wrapper
  (ltx fits it: the model is held by the render child), and the pointer versus
  full manifest question for `studios/*.yaml`, which remain placeholders.
- **h3's `busy: /api/queue` does not conform to Q12 either.** Changing h3's
  manifest is only necessary if h3 is in the demo, and it is.
- **AuK keeps sessions in `web/sessions` inside its checkout** (M0), against
  R31. That is SDK adoption (Q16), not M5.
- **Machine-bound.** A real `uv` install of ltx (MLX, 71 GB of weights) and of
  AuK (torch on mps), the switch h3 → ltx → AuK with live `busy` answers, and
  proof that no render survives a switch (`ps -axo pid,pgid,sess,rss,command`
  after each one). None of this can run in the gate.

---

### Proposed expansion (draft, for Q1)

#### Reads

01 (R8–R13, R21, R25–R27), 02 §4 and §7, 04 §4, 05 §2, §5a and §9, 07 §7, 08;
`studios/*.yaml`; M0's report; `docs/decisions.md` in full.

#### Tasks

1. **Environment.**
   - Probe for `uv` (Q3).
   - Create the environment, keep or recreate it (Q4, Q6, Q7), and record it in
     `runtime_env`.
   - Uninstall removes it.
2. **Activation.** The environment variables of Q5 for build steps, processes
   and `exec` probes, and `{venv}` substitution. Remove M2's `{venv}` refusal
   and M3's `python:` refusal.
3. **Teardown that reaches detached descendants** (Q11 b).
4. **`busy`.** The contract (Q12), the probe, and conflict details with a
   confirm digest (Q13). Update the launcher contract, regenerate, and change
   the plain shelf's dialog text.
5. **`helm dev`** takes an existing environment (Q15).
6. **Manifests.**
   - ltx and AuK per Q9, Q10 and Q12, at upstream refs once those exist.
   - h3's `busy` path per Q12.
7. **Docs.** Amend 01, 02, 04, 05 and 07 per the answers, and add the schema's
   `busy` description.

#### File list

- `internal/install/**`, `internal/supervisor/**`, `internal/platform/**`
- `internal/api/**`, `api/openapi.yaml` (launcher operations only), and the
  generator's outputs
- `web/**`, `cmd/helm/**`
- `studios/auk-studio.yaml`, `studios/ltx-studio.yaml`, `studios/h3-studio.yaml`
- `schema/manifest.json` (the `busy` description only)
- `docs/design/01-prd.md`, `02-data-model.md`, `04-packages.md`,
  `05-sdk-and-custom-studios.md`, `07-platform-services.md`
- `docs/decisions.md`, and this report

#### Definition of Done

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

#### Review focus (carried forward)

- **One code path.** Any special case in Go for a specific studio means the
  schema is wrong.
- **Reproducibility.** Is the environment per-studio and reproducible from the
  manifest alone, and does anything read user uv config or pick a system
  interpreter?
- **Absent `uv`.** What happens when `uv` is absent?
- **Signalling.** Can teardown signal a process that is not a verified
  descendant? This is M2's identity rule, now applied beyond the group.
- **What the dialog says.** Can the dialog ever say "idle" when it does not
  know, or stop a studio whose state changed after the user read the dialog?
- **Tests.** Were the tests written from the contract? M4 found two daemon-only
  bugs that the first suite missed.
