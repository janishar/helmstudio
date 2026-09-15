# M2 — Supervision — implementation report

## What was built

`internal/supervisor` turns a validated manifest into a resolved plan: ports assigned, placeholders substituted as shell-safe absolute paths, dependency order fixed. It spawns every member as the leader of its own process group and health-gates each with `path`, `tcp` or `exec` probes. It tears groups down in reverse with SIGTERM to the group, a grace period, then SIGKILL. It restarts only sidecars and workers, enforces one heavy group with its memory arithmetic, and persists pid, start time and pgid to schema v2. A restarted daemon re-adopts survivors whose identity still matches on all three. A process writes its output straight into its log file; the daemon tails that into a bounded ring buffer and an SSE stream. `cmd/helmstudio` now runs the daemon, and `web/` holds the unstyled shelf.

Work stopped at kickoff on fifteen design questions and again on the M1 store tests. It resumed on the human's answers: "recommendations for all", the DDL, and version-relative tests. All of them are recorded in `docs/decisions.md` under "2026-09-15 · M2 supervision".

## Review round 1

The review returned 1 BLOCKING, 6 SHOULD-FIX and 7 NOTE findings. I agreed with all of them. Four needed a decision from the human; I asked before changing code, and the human took the recommendation each time. Each fix below has a test, and each fix was mutation-checked: break the fix, and its test fails (8 of 8, after one test was strengthened).

- **BLOCKING, fixed by the human's decision: studios inherited the daemon's whole environment.**
  - A studio process and its `exec` probe now get only `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`, `LANG`, `LC_*`, `TERM` and `TZ`, plus the manifest's own `env`.
  - `TestStudiosDoNotInheritTheDaemonEnvironment` sets `HF_TOKEN` and `AWS_SECRET_ACCESS_KEY` and reads the real process's `env` and the probe's `env`.
  - Also checked with the real binary: `HF_TOKEN` set on the daemon did not reach the studio.
  - Cost: no studio can receive a token until the schema has a field to ask for one (raised as open).
  - The test fixtures used to pass the helper's path through an inherited variable. They now write the path into the manifest.
- **SHOULD-FIX, fixed: a studio re-adopted without its manifest was invisible and not counted as heavy.**
  - It is now listed by the API and the shelf with `manifest_loaded: false`, and can be stopped but not launched.
  - It counts as heavy, and the daemon log warns about it.
  - `TestReadoptWithoutManifestIsVisibleHeavyAndStoppable` kills a real daemon, re-adopts with no manifests loaded, is refused a second heavy launch, then stops the studio and checks by pgid.
  - Also reproduced with the real binary, by starting it on an empty `-studios` directory.
- **SHOULD-FIX, fixed: failures to record a process were logged and ignored.**
  - A launch's rows are written in one transaction before anything spawns; if that fails, the launch is refused.
  - Each spawn first claims its row as `starting` with no pid.
  - If writing pid, start time and pgid fails, the new group is killed at once.
  - Re-adoption starts only rows still `queued`. A claimed row with no pid is reported gone and not started a second time.
  - Tests use a store-failure hook: `TestSpawnThatCannotBeRecordedIsKilled` (asserts no process carries the launch's unique argument), `TestLaunchThatCannotBeRecordedStartsNothing` and `TestReadoptDoesNotRestartAnUnrecordedSpawn`.
  - **Still open:** the window between `fork` and the spawn write. A daemon killed inside it leaves an unrecorded process. The claim prevents a duplicate, but nothing can find the process.
- **SHOULD-FIX, fixed by the human's decision: grace is now 30 s.** The reviewer showed h3's own SIGTERM path can take over 20 s. A per-manifest grace is raised as open, alongside the nested-`Setpgid` finding.
- **SHOULD-FIX, fixed: survivors of a gone leader went unreported.**
  - `ReadoptReport.Survivors` and the daemon log name any process group whose recorded leader is gone but which still has members. Nothing is signalled.
  - `TestReadoptReportsSurvivorsOfAGoneLeader` covers it.
  - Whether to ever signal them is raised as open.
- **SHOULD-FIX, fixed by the human's decision: a worker crash tore down main.**
  - A worker that fails without a restart left is recorded failed, and main keeps running. A failed worker does not make the group read as failed.
  - A sidecar in the same position still fails the group.
  - `TestWorkerCrashLeavesMainRunning` covers it.
- **SHOULD-FIX, fixed: test gaps.**
  - The three cases the reviewer named are covered above.
  - The daemon-kill setup is now `orphanStudio`, whose cleanup kills the survivor's process group whatever the test does.
- **NOTE, fixed: a preempting launch was bound to the request context.** Once the running studio has been told to stop, the launch completes even if the caller disconnects. `TestPreemptingLaunchSurvivesTheCallerLeaving` covers it. `launchMu` is still held throughout, so `Shutdown` can wait up to grace + 30 s.
- **NOTE, fixed: `cwd` was checked lexically, and `local_path` could be relative.**
  - A relative `local_path` refuses the launch.
  - `cwd` must resolve through symlinks to a path inside the resolved checkout.
  - `TestWorkingDirectoryMustResolveInsideTheCheckout` covers both.
- **NOTE, fixed: `lsof` and `security` spawned without `Setpgid`.** `platform.RunInGroup` runs a short-lived helper as a group leader and kills the group on timeout. `lsof`, `security` and `exec` probes use it.
- **NOTE, recorded as open: a log grows without bound while no daemon is running.** The next daemon compacts it on its first read after re-adoption.
- **NOTE, recorded as open: there is no authentication until M9.** Any local process, including another account on the same Mac, can launch and stop studios. Host and Origin checks stop web pages, not local processes.
- **NOTE, recorded as open: viewer memory.** A viewer's buffer is 512 lines, about 8 MiB at the 16 KiB line limit, and viewers per process are uncapped.
- **NOTE, fixed by the human's decision: h3 had no `peak_ram_gb`.** `studios/h3-studio.yaml` now has `peak_ram_gb: 21`. This is outside the file list, with approval.

## Review round 2

The review returned 0 BLOCKING, 3 SHOULD-FIX and 1 NOTE finding. I agreed with all of them. The environment finding needed the human, who took both recommendations. Each fix has a test, and each was mutation-checked; all six were caught. One mutation first failed to recreate the bug, so it was redone to route a reused pid back through the survivor check, and that version is caught.

- **SHOULD-FIX, fixed: a restarted worker that failed before it was ready still tore down main.**
  - The failure paths in `gate`, the failed restart spawn and the initial spawn now go through `memberFailed`. For a worker it stops the process if it is still alive, marks its exit handled so it is not restarted again, records it failed, and the group carries on.
  - Exception: a worker another member `depends_on` fails the group, as a sidecar does. This is my judgement call, raised in the decision entry.
  - Tests: `TestRestartedWorkerThatNeverReadiesLeavesMainRunning` (a worker with `restart: on-failure` whose health flag is removed before the restart) and `TestWorkerThatIsDependedOnFailsTheGroup`.
- **SHOULD-FIX, fixed: a claimed-but-unrecorded sidecar was restarted at re-adoption.**
  - `procFromRow` marks such a member `noRestart`, and `memberExited` never restarts one.
  - A sidecar in that state fails the group; a worker is recorded failed.
  - `TestReadoptDoesNotRestartAClaimedSidecar` rebuilds the reviewer's reproduction: a live main with its real identity, plus a claimed `restart: on-failure` sidecar. It asserts no pid and no new log appear.
- **SHOULD-FIX, fixed by the human's decisions: ambient Hugging Face token through HOME, and the undocumented proxy and SSH cost.**
  - Studios get `HF_TOKEN_PATH` pointing at a per-studio path that is never created, plus `HF_HUB_DISABLE_IMPLICIT_TOKEN=1`. HOME and the HF cache are unchanged, and a manifest's `env` can override both.
  - The proxy variables now pass; `SSH_AUTH_SOCK` still does not.
  - Recorded costs: other secret files under HOME stay readable, and a build step can't clone over SSH through the user's agent.
  - `TestStudiosDoNotInheritTheDaemonEnvironment` now also checks the token path, that implicit tokens are off, that the proxy passes, and that the SSH agent does not.
  - Not verified against huggingface_hub itself, which is not installed here.
- **NOTE, fixed: false survivor reports.** Survivors are checked only when the recorded pid has no process at all. `TestReadoptReportsNoSurvivorsForARecycledPid` covers it.
- **Found while verifying round 2, fixed: a state was shown before it was recorded.**
  - A `-race` run failed `TestReadoptWithoutManifestIsVisibleHeavyAndStoppable`. The test daemon printed `running` from memory before the `running` row committed; the SIGKILL landed in that gap, so the studio was re-adopted as `starting`. A monitor-only group never probes, so it stayed `starting`.
  - `setState` and `setHealth` now write first and update memory second.
  - `TestStateIsRecordedBeforeItIsShown` slows writes and checks the row whenever Status shows `running`. Mutation-checked.
  - Three consecutive `-race` runs of `internal/supervisor` passed afterwards.

## Gate

    $ rm -rf bin && go clean -testcache && make gate      # after review round 1
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since b7b7544
    ok  	github.com/janishar/helmstudio/cmd/helm	0.571s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.455s
    ok  	github.com/janishar/helmstudio/internal/manifest	2.374s
    ok  	github.com/janishar/helmstudio/internal/platform	1.808s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	2.303s
    ok  	github.com/janishar/helmstudio/internal/supervisor	18.695s
    ?   	github.com/janishar/helmstudio/web	[no test files]
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

**macOS (Apple Silicon) only**, as decided in M1. `deps` reports go.mod "unchanged" because `golang.org/x/sys` moved from indirect to direct at the same version, and the check compares name and version facts. The move is recorded anyway.

Outside the gate:

- `go test -race ./internal/...` is clean.
- `go test -count=3 ./internal/supervisor ./internal/api` is clean, as a flakiness check. After review round 1, `-race` and `-count=2` of `internal/supervisor` are also clean, and no test helper process outlives the run.

### Deliberate bugs

Each was applied, run against its test, and reverted. All were caught.

| Bug introduced | Caught by |
|---|---|
| `Matches` ignores pgid | `TestReadoptRequiresPidStartTimeAndPgid` |
| `Matches` ignores start time | `TestReadoptRequiresPidStartTimeAndPgid` |
| `Setpgid = false` in `StartInGroup` | `TestLaunchOrdersByDependencyAndTeardownEmptiesEveryGroup` |
| SIGTERM to the pid, SIGKILL still to the group | the same test (grandchildren never received SIGTERM) |
| `main` counted as restartable | `TestCrashedMainFailsGroupAndIsNotRestarted` |
| health monitor kills the group on a failed probe | `TestFailingHealthNeverRestartsMain` |
| blocking send to a log viewer | `TestSlowViewerLosesLinesAndIsTold` (hangs; fails under `-timeout`) |
| Origin check disabled | `TestHostAndOriginChecks` |
| stop signals a recycled leader | `TestSignalGroupRefusesARecycledLeader` |
| retention removes a non-regular file | `TestLogRetentionKeepsLastRunsAndNeverFollowsSymlinks` |
| heavy check disabled | `TestSecondHeavyStudioIsRefusedWithArithmetic` |
| substituted values not shell-quoted | `TestSubstitutedPathsArriveAsOneArgument` |

The first mutation run exposed a gap. The "SIGTERM to the pid" bug passed, because the SIGKILL fallback still emptied the group. The grandchild helper now records receiving SIGTERM, and the test asserts it. The "stop signals a recycled leader" bug was also not caught by any test; `TestSignalGroupRefusesARecycledLeader` was added for it.

The M1 store tests edited in this milestone were re-checked the same way. Removing `foreign_keys = OFF`, skipping the backup, skipping `foreign_key_check`, or committing a failed migration each still fails its test.

### End to end with the real binary

This ran against a stand-in studio, `python3 -m http.server`, with a checkout path containing a space.

1. Launch: running, health passing, UI reachable.
2. Launch h3 alongside it: 409 with the arithmetic.
3. A POST with a cross-site `Origin`: 403.
4. `kill -9` the daemon. The studio kept serving.
5. Restart: the log says `re-adopted pyserve/studio pid 59022`. Status showed the same pid and port, and the log stream resumed.
6. Stop: the process group was empty afterwards.
7. SIGTERM the daemon: children terminated (R28).

The shelf was loaded in a browser. It listed both studios, showed elapsed time, health, pid and port, and streamed logs.

**Two real bugs were found this way and fixed:**

- **A shutdown stall.** An open SSE log stream held `http.Server.Shutdown` for up to two minutes, so studios were not stopped until the browser tab closed. Request contexts are now cancelled first, and shutdown with an open stream now completes in under 1.5 s.
- **A preemption that could not succeed.** Confirming preemption for a studio that cannot launch, such as h3 with no checkout, would have stopped the running studio and then failed. Launchability is now resolved first; covered in `TestSecondHeavyStudioIsRefusedWithArithmetic`.

## Design contradictions raised

I stopped at kickoff with these. The human answered "recommendations for all". Each is recorded as a decision entry.

1. **Re-adoption state had nowhere to live.**
   - 02 §4 and 06 §4 put `processes` in `helm.db`, but 02 had no DDL for it.
   - `docs/decisions.md` said "Needs the human before M2 kickoff: widen M2's list, or land the lifecycle migrations separately first."
   - **Told how to proceed:** DDL for `processes` and `log_files` added to 02 §4, and the list widened to `internal/store/**`.
2. **No studio root, `{data}` or `{models.x}` before install exists.** The DoD says "h3 launches from its manifest", but M2 precedes M3. **Told how to proceed:** `local_path` or `<data>/studios/<id>/src`, `<data>/studios/<id>/data`, and `<models>/<dest>`.
3. **`{paths.x}` does not exist.**
   - The milestone says "`{models.x}`, `{ports.x}`, `{paths.x}`".
   - The schema's `cmd` description lists `{port} {ports.<name>} {models.<name>} {models.selected} {root} {data} {venv}`.
   - **Told how to proceed:** the schema's set, applied in `cmd`, `env` and `health.exec`.
4. **Unquoted absolute paths break under the default `sh` on macOS.** "Application Support" contains a space. **Told how to proceed:** POSIX single-quoting.
5. **Refuse versus preempt.**
   - The DoD says "refused with the memory arithmetic".
   - R25 and 02 §7 say "on confirm, stops the first".
   - The arithmetic was undefined, and `:launch` has no confirm parameter.
   - **Told how to proceed:** refuse with 409, and preempt with `?preempt=true`.
6. **The shelf's API is not in `api/openapi.yaml`.** It has no error shapes and no live log path. **Told how to proceed:** implement the named paths, add a log SSE path, and record every addition for M4.
7. **`internal/manifest` was not on M2's list.** It exports only `Validate`. **Told how to proceed:** widen the list and add `Load`.
8. **Restart scope.** The brief says sidecars and workers; R27, 02 §7 and the schema say sidecars. **Told:** both.
9. **Log location.** 06 §4 says `studios/<id>/logs/` in one table and a separate logs root in another. **Told:** the logs root.
10. **`{ports.x}` without `depends_on`.** This was already open. **Told:** assign every port at launch.
11. **`busy` has no response contract.** **Told:** not called in M2.
12. **Unspecified numbers** for grace, backoff, ring size and slow consumers. **Told:** as proposed.
13. **07 §3 environment injection.** **Told:** deferred to M4.
14. **Host and Origin checks.** **Told:** in M2.
15. **Process start time on macOS needs `x/sys/unix`.** **Told:** use it, and record it.
16. **The M1 store tests pinned schema version 1.** Five of them failed once migration 2 existed, although no behaviour they check had changed. The brief forbids editing tests I did not write. **Told how to proceed:** make them version-relative (see "Files touched outside the milestone's list").

These were found while working and logged under "Open, not yet decided" without stopping. None blocks the DoD.

- **A studio that sets `Setpgid` on its own children takes them out of the group helmstudio signals.** This is the most important one. 08 lists h3c-studio doing exactly that as a pass for R26, and the brief calls it "the floor". But a render `h3` then leads its own group. It survives the SIGKILL escalation or a crash of h3studio, holding its memory, untracked. Options are in the entry.
- **A restartable sidecar that crashes before its first healthy probe** fails the group rather than restarting. 02 §7 restarts only from `running`.
- **Exit status 0 under `restart: on-failure`** is not in the design. A worker exiting 0 is treated as finished; a sidecar exiting 0 fails the group.
- **The `exit_reason` vocabulary has no value** for a sibling stopped because another member failed, or for a clean daemon shutdown.
- **`studios/h3-studio.yaml` has no `peak_ram_gb`.** 01 §13 and 08 say 21, so h3's arithmetic reads "undeclared".
- **`autostart: false` processes can never be started.** There is no API path for it.
- **Log compaction races the studio's own appends,** within a microsecond window.

## Judgement calls

- **Output goes to a file, not a pipe.** A child's stdout and stderr are the `O_APPEND` log file itself, and the daemon tails it.
  - A pipe dies with the daemon: after `kill -9`, the studio's next write raises SIGPIPE, which kills a Go or Python process by default. Re-adoption would then find a corpse.
  - It also settles backpressure for the studio, since a kernel append never waits on the daemon.
  - The daemon's cost is bounded. The tailer reads 256 KiB per tick and skips ahead, reporting the gap, when more than 4 MiB behind. The ring holds 2,000 lines or 1 MiB. Each viewer has a 512-line channel that drops and counts rather than blocks.
  - The cost is in-place compaction, and the race described above. Rejected: a pipe with a copier goroutine (dies with the daemon); a FIFO (same); `F_PUNCHHOLE` (filesystem-specific).
- **The compacted size.** Compaction triggers at 16 MiB and keeps 1 MiB of head plus the last 8 MiB, not the full cap. With hysteresis, a studio logging continuously triggers it about every 8 MiB of output rather than on every line.
- **One runner goroutine per group owns all state transitions.** Health monitors write only `health_state`, through a separate `UPDATE`, so the two writers never overwrite each other's columns. Members start sequentially.
- **The identity guard applies to every signal, not only at startup.** `signalGroup` requires pgid == pid. It then requires either that the leader still matches all three fields, or that no process holds the leader's pid.
  - The second case is what lets leftovers of a crashed member be swept. While any member of the old group lives, the kernel cannot reuse its id. A zombie reads as gone.
  - `internal/platform` refuses `kill` on group 0, 1 and the daemon's own group.
  - Rows found gone at re-adoption are marked `noSweep` and never signalled, as 02 §7 says.
- **A zombie counts as gone** in `IdentifyProcess`. It still occupies its group, so `GroupExists` includes it, and teardown waits for reaping.
- **A missing start time is never adopted.** A process that exits before its identity can be read gets `pid_start_time` NULL, and is never re-adopted or signalled by identity. Nothing survives to adopt.
- **Exit codes.** A signal-terminated exit is recorded as 128 + signal, so SIGKILL reads 137, matching R27's "Crashed · exit 137" chip. `oom` is never set.
- **Retention.** At launch, a studio keeps its four most recent finished runs, so five with the new one. Retention deletes only regular files under the logs root, found with `Lstat`, and refuses and logs anything else.
- **Monitor-only re-adoption.** If a studio's manifest is gone, no longer resolves, or now has a process the running group lacks, survivors are watched and stoppable, but not probed or restarted.
- **The `exec` health probe** runs in its own process group and is killed as a group on timeout. One attempt is bounded by `min(interval_s, 10 s)`.
- **The shelf's Origin rule.** A request with no `Origin` header is allowed, so `curl` works. `"null"` is refused.
- **ULIDs are hand-rolled** in about 20 lines, rather than adding a dependency.
- **`log_files` has more than was shown at approval.** It gained `studio_id`, `created_at` and a relative `path`, where the DDL shown to the human at kickoff was shorter. All three are in 02 §4 and the decision entry.
- **The environment allowlist's exact names** (review round 1) were chosen as the minimum a shell, a locale and a temp directory need, plus the proxy variables (round 2, the human's decision). `DISPLAY` and `SSH_AUTH_SOCK` are deliberately absent.
- **A worker another member `depends_on` fails the group** when it fails (round 2), as a sidecar does, even though workers are otherwise record-only.
- **An unmanaged re-adopted studio counts as heavy**, because `processes` has no `heavy` column to read back. A column would be a DDL change to 02.
- **`PortHolder` shells out to `lsof`.** There is no stdlib API for it. The platform test skips when `lsof` is absent, which applies to minimal Linux only; a missing tool is not a failing assertion.

## Could not verify

- **The machine-bound demo with h3 itself.** Nothing here ran h3, Metal or real weights. To demonstrate it on an Apple Silicon Mac:
  1. `git clone --recurse-submodules https://github.com/janishar/h3c-studio "$HOME/Library/Application Support/helmstudio/studios/h3-studio/src"`, check out `a6eb54f`, run `make -j8` in `h3c`, then `go build -o dist/h3studio .`
  2. Put or symlink the MiniMax-H3 checkpoint (with `FL2VA/`) at `~/Library/Application Support/helmstudio/models/MiniMax-H3`.
  3. `make build && ./bin/helmstudio`, then open http://127.0.0.1:8700, launch h3 studio, and generate a take.
  4. **Mid-render**, run `kill -9 $(pgrep -f bin/helmstudio)`. Confirm h3's UI still answers and the render finishes.
  5. Run `./bin/helmstudio` again. The log must say `re-adopted h3-studio/studio pid N`. The shelf must show the same pid, and its logs must continue.
  6. Stop. `ps -A -o pid,pgid | awk '$2==N'` must be empty.
  7. **Also run `ps -A -o pid,pgid,command | grep h3c/h3`**, both after a stop during a render and after `kill -9 N` of h3studio. This is the nested-`Setpgid` finding above, and I expect the second case to leave the render behind.
- **`--root {data}` reaching h3 as one argument** when the data root contains "Application Support". This is proven with a fake command, not with h3.
- **The Hugging Face token block against huggingface_hub itself.** It is not installed here. The test proves the variables reach the process, not that the library honours `HF_TOKEN_PATH` and `HF_HUB_DISABLE_IMPLICIT_TOKEN` in the version ltx and AuK pin. To check on the Mac: log in with `hf auth login`, launch ltx, and confirm a gated download from inside the studio is refused.
- **Memory.** The heavy arithmetic's numbers come from manifests and `hw.memsize`. Nothing measures what a studio actually holds.
- **Linux.** It compiles (`vet-linux`). `parseProcStat`, `/proc/meminfo`, `lsof` availability and group semantics there have never run.
- **Real sustained output.** A studio logging a megabyte a second for hours is covered only by unit tests of the tailer and compaction, not a long run.
- **`lsof` naming a holder owned by another user** without privileges. It may report nothing, and the message then says "a process lsof could not name".

## Dependencies added

- **`golang.org/x/sys` v0.47.0**, already in `go.mod` as indirect, is now a direct `require`. `unix.SysctlKinfoProc("kern.proc.pid")` gives the process start time in microseconds, and `unix.SysctlUint64("hw.memsize")` gives host memory. The stdlib's `syscall.Sysctl` returns only strings, and `ps -o lstart=` has one-second, locale-formatted resolution. `go.sum` is unchanged. Recorded in `docs/decisions.md`.

## Files touched outside the milestone's list

- **`internal/store/**`** was widened by the human at kickoff:
  - `migrations/0002_supervision.sql` and its registration in `migrate.go`.
  - **Edits to five M1 tests**, directed by the human: `TestMigrationsFromEmptyCreateSchemaV1`, `TestFailedMigrationLeavesVersionAndSchemaUntouched`, `TestExistingDataIsBackedUpBeforeMigrating`, `TestRebuildingAParentTableKeepsChildReferences` and `TestMigrationLeavingForeignKeyViolationsIsRolledBack`.
    - Appended test migrations use `LatestVersion()+1`, and expected versions follow.
    - The exact table and index lists gained v2's names and stay exact.
    - Each still catches the bug it was written for (mutation-checked above).
- **`internal/manifest/**`** was widened by the human at kickoff: `Load`, `EffectiveProcesses`, `Placeholders`, the completed types, and `load_test.go`.
- **`docs/design/02-data-model.md` §4** gained the `processes` and `log_files` DDL, approved by the human.
- **`go.mod`**: the x/sys line moved from indirect to direct.
- **`studios/h3-studio.yaml`**: `peak_ram_gb: 21`, approved by the human in review round 1.
- **`docs/agents/reports/02-supervision.md`**: this file.

## Decision-log entries appended

These are in `docs/decisions.md` under "2026-09-15 · M2 supervision" and are not repeated here, to keep one copy. There are twenty entries:

- DDL and schema v2
- `internal/manifest` widening
- root, data and model stand-ins
- the substitution set
- shell quoting
- heavy refusal and preemption
- API additions for M4
- restart scope
- log files
- port assignment at launch
- `busy`
- grace and timings
- no 07 §3 environment injection
- Host and Origin checks
- `x/sys`
- the signal guard
- re-adoption rules
- exit reasons
- sequential start and health semantics
- daemon manifest loading

"Open, not yet decided" gained eight "Raised in M2:" items, and "## Changes" gained one line.

Review round 1 added ten entries at the end of the M2 section:

- environment allowlist
- grace 30 s (supersedes 10 s)
- worker crash (supersedes the worker clause)
- recording before a process can be lost
- unmanaged re-adopted studios
- survivor reporting
- preemption detached from the caller
- `local_path` and resolved `cwd`
- `platform.RunInGroup`
- h3 `peak_ram_gb` (resolves its open entry)

It also added six "Raised in M2 review:" open items: a manifest field for secrets, per-manifest grace, signalling survivors, log growth while the daemon is down, no local authentication until M9, and viewer memory.

Review round 2 added five entries:

- the Hugging Face token block
- proxy variables pass, the SSH agent does not
- worker failure at any point, with the `depends_on` exception
- claimed members are never restarted
- survivors checked only when the pid has no process
- state written before it is shown `git diff main -- docs/decisions.md` shows no removed lines.

## Left undone

- **The h3 demo and everything under "Could not verify".**
- **Starting an `autostart: false` process on demand.** There is no contract path.
- **`services/*.yaml` (R29).** Long-lived processes belonging to no studio are not loaded. The supervisor takes any `Studio`, so this is loading work, not supervision work.
- **Calling the `busy` probe.** Its contract is open.
- **07 §3 environment, tokens and the stage directory.** These are M4.
- **The registry and local manifest sources (R2a).** The daemon reads one directory.
- **`install_state` on the `Studio` response.** It arrives with M3.
- **The six "Raised in M2 review" items** in the decision log. Each needs a design decision.
- **The unrecorded-process window** between `fork` and the spawn write.
- **Committing.** Per `implementer.md`, the work is uncommitted on `feat/supervision`.
