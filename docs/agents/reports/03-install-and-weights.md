# M3 — Install and weights — implementation report

## What was built

`internal/install` takes a studio from listed to ready and back:

- It clones at a pinned ref and runs `build[]` step by step, each step in its own process group with a log file.
- Retry resumes from the first step that has not succeeded.
- Cancelling kills the step's group.
- A startup sweep records work a dead daemon left mid-flight.
- Uninstall removes only the checkout helmstudio cloned.

`internal/weights` is the model cache:

- A Hugging Face downloader that is resumable per file, re-resolves a 403 from a signed URL instead of restarting, checks size and etag, and checks free disk first.
- Linked directories behind a symlink at `<models>/<dest>`, which are never written to and become `missing` when their directory disappears.
- Reclaim, whose preview is exactly what it deletes.

Around those: schema v3, launch reading installations instead of M2's stand-ins, and install, job and weights endpoints in `internal/api`, wired into `cmd/helmstudio`.

Work stopped at kickoff on 25 design questions. The human answered "recommendations for all". Every answer and every judgement call made since is in `docs/decisions.md` under "2026-09-15 · M3 install and weights".

## Review round 1

The review (helmstudio-b6) returned 2 BLOCKING, 6 SHOULD-FIX and 5 NOTE. I agreed with all of them; there was no disagreement. Items 1, 3, 5 and 6 each contained a decision, which I put to the human; the human took the recommendation each time. Every fix has a test, and every fix was mutation-checked (22 mutations). The four first missed were test weaknesses, and those tests were strengthened until caught; one more miss is redundant protection, noted in the table.

| # | Finding | What changed | Test |
|---|---|---|---|
| 1 | BLOCKING: a build step outlives `kill -9` of the daemon | **Human: record identity, stop the survivor.** `step_runs` gains `pid`, `pid_start_time`, `pgid` (02 §4, schema v3 amended in place since it has not shipped). A step starts with `StartInGroup`, and its identity is written immediately; if that write fails the step is killed. The sweep, which runs before the API serves, stops a survivor behind M2's guard (pgid == pid, and either the identity matches or no process has the pid): SIGTERM, 30 s grace, SIGKILL. Then it marks the step interrupted. Re-checked with the real binary: restart logs "stopping build step … left running by a helmstudio that was killed", and the `sleep` is gone. | `TestSweepStopsABuildStepThatOutlivedAKilledDaemon` kills a real helper daemon mid-step; `TestSweepNeverSignalsARecycledPid` |
| 2 | BLOCKING: reclaim deletes a nested artifact | Download and link refuse a dest that is, contains or sits inside another's; link checks before creating any directory or symlink. Reclaim stops before deleting anything if another artifact lies under an item. | `TestNestedDestsAreRefusedAndReclaimNeverDeletesAnotherArtifact` |
| 3 | DELETE contract | **Human: DELETE with a confirm.** `GET /models/{id}` carries a one-item `reclaim` preview. `DELETE /models/{id}` unlinks a linked artifact and reclaims an unused download only with `?confirm=`. Otherwise 409 `confirm_required`/`preview_changed` with the preview, or `in_use` naming the studios. | `TestDeleteModelNeedsTheConfirmOfItsPreview` |
| 4 | Missing linked drive fails the install | The job fails with `linked_missing`, naming the path; `install_state` and `last_failure` are untouched. | `TestMissingLinkedDriveLeavesInstallStateUntouched` |
| 5 | Gated weight state | **Human: new state `auth_required`** (01 §5, 02 §4 and §7). The sweep leaves it alone. A successful `:fetch` of a required weight, or an install, finishes the install once every required weight resolves. | `TestGatedWeightAsksForATokenWithoutFailingTheInstall` (now through a sweep), `TestFetchingTheWaitedOnWeightFinishesTheInstall` |
| 6 | h3's shared artifact: partial links are dead ends | **Human: required weights in, optional ones named.** A link must hold every non-optional weight of that repository the studio declares; optional ones it lacks come back as `unobtainable`. Every refusal that would suggest unlink or reclaim while a studio is bound now names the uninstall first. | `TestLinkRequiresEveryRequiredWeightOfTheRepository`, `TestLinkWeightChecksTheStudiosOtherWeights`, `TestRefusalsNeverOfferARefusedStep` |
| 7 | Etag compared only after a full download | Compared from the redirect's or the 200's headers (or a HEAD for a complete `.part`) before any byte is written; a mismatch fails at once naming both, with no truncation. | `TestETagMismatchFailsBeforeDownloading` (mid-file and complete `.part` both untouched) |
| 8 | `ref` reaches git as an option | A `ref` or `repo` starting with `-` is blocked before anything is recorded; `--end-of-options` before the remote URL, the ref and the checkout target. | `TestRefCannotBeAGitOption` (the `--upload-pack` command never runs, even past the check) |
| 9 | Stale git locks after a cancel | The failure names the lock file and the recovery; helmstudio does not delete locks, since a git may survive a crash. | `TestStaleGitLockNamesTheRecovery` |
| 10 | 02 §4 comment | Updated to say `processes.studio_id` deliberately has no foreign key. | — |
| 11 | iris downloads five whole checkpoints | Put in front of the human, and recorded as open. Not decided. | — |
| 12 | Retention through a symlinked log directory | Build-log and M2's run-log retention remove through `os.Root` on the logs root. Build-log retention removes only `build-<ulid>-<nn>.log`. | `TestBuildLogRetentionNeverFollowsASymlinkedDirectory`, `TestLogRetentionNeverFollowsASymlinkedStudioLogDirectory` |
| 13 | Same-host redirects treated as the CDN | Added to "Could not verify" and recorded as open; not changed. | — |

One mutation was missed with no test added. Removing only `logs.Lstat` from run-log retention is still refused by `logs.Remove` through `os.Root`. Replacing both is caught.

## Gate

    $ rm -rf bin && go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since 4040870
    ok  	github.com/janishar/helmstudio/cmd/helm	0.563s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api	2.132s
    ok  	github.com/janishar/helmstudio/internal/install	5.812s
    ok  	github.com/janishar/helmstudio/internal/manifest	1.241s
    ok  	github.com/janishar/helmstudio/internal/platform	1.780s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	2.282s
    ok  	github.com/janishar/helmstudio/internal/supervisor	26.581s
    ok  	github.com/janishar/helmstudio/internal/weights	2.546s
    ?   	github.com/janishar/helmstudio/internal/weights/hubtest	[no test files]
    ?   	github.com/janishar/helmstudio/web	[no test files]
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

This is macOS on Apple Silicon only, as decided in M1. Outside the gate, `go test -race -count=1` of `internal/supervisor`, `internal/install`, `internal/weights`, `internal/api`, `internal/store` and `internal/manifest` is clean.

### The Definition of Done, test by test

| DoD | Evidence |
|---|---|
| h3 installs from a clean state with weights | **Not verified**: machine-bound (see "Could not verify"). The same pipeline is proven with a local git repository and a fake Hub in `TestInstallFromCleanStateIsIdempotent`, and with the real binary below. |
| A download interrupted at 60% resumes at 60% | `TestInterruptedDownloadResumesWhereItStopped`: the connection stalls at exactly 60% and the fetch is cancelled. A fresh service, standing in for a restarted daemon, sends one `Range: bytes=629452-` and gets a 206. The file ends byte-identical. |
| A 403 mid-download resumes rather than restarting | `TestExpiredSignedURLResumesInsteadOfRestarting`: the connection drops at 50%, then the next two signed URLs answer 403. Every retry asks for `bytes=262144-`, there are four resolves, the artifact ends `ready` and never `auth_required`, and the bytes are correct. `TestHubRefusalIsAuthRequired` covers the other 403, from huggingface.co itself. |
| A linked weight directory is never written to | `TestLinkedDirectoryIsNeverWrittenTo` snapshots path, mode, size, mtime and content hash, then runs link, relink, bind, a refused bind, launch resolution, list, a refused unlink, reclaim and unlink. The snapshot is identical afterwards. `TestUninstallRemovesTheCheckoutAndNothingElse` and the HTTP test check the same through uninstall and the API. |
| No delete path follows a symlink | `TestNoDeletePathFollowsASymlink` (install) plants symlinks to a sentinel directory: inside a checkout, as a checkout, inside a download, inside a linked directory, as the studio directory itself, and as an old build log. It then runs uninstall ×3, reclaim, unlink and build-log retention. `TestNoWeightsDeletePathFollowsASymlink` (weights) adds a download directory replaced by a symlink and a `.part` that is a symlink. M2's `TestLogRetentionKeepsLastRunsAndNeverFollowsSymlinks` covers run logs. |
| Reclaim's preview matches what it deletes | `TestReclaimDeletesExactlyItsPreview` checks that the returned set equals the preview, bound and linked artifacts are excluded, and a stale or changed confirm deletes nothing. The HTTP test checks the same over the wire. |
| `make gate` green | Above. |

### Deliberate bugs

Each was applied, run against its test, and reverted.

| Bug introduced | Caught by |
|---|---|
| a 403 from the signed URL read as auth-required | `TestExpiredSignedURL…` |
| a 403 from the signed URL restarts the file from zero | `TestExpiredSignedURL…` |
| resume ignores the partial file | `TestInterruptedDownload…` |
| `Content-Range` not checked before appending | `TestResumeRefusesAServerSendingTheWrongRange`, added after this mutation was first **missed** |
| etag not verified | `TestFinishedFileIsCheckedAgainstTheListing` |
| reclaim follows a symlinked download directory | `TestNoWeightsDeletePathFollowsASymlink` |
| unlink removes inside the target | `TestNoWeightsDeletePath…`, `TestLinkedDirectoryIsNeverWritten…` |
| link writes a marker into the linked directory | `TestLinkedDirectoryIsNeverWritten…` |
| a missing link is not detected at launch | `TestLinkedDirectoryThatDisappearsIsMissing`, **missed** until the test read the row instead of `Get`, which marks missing on its own |
| reclaim includes linked artifacts / referenced ones / ignores confirm | `TestReclaim…` |
| disk check skipped | `TestDownloadThatWouldNotFitIsRefused` |
| a second binding re-downloads existing files | `TestTwoWeightsOfOneRepo…`, `TestSecondStudio…` |
| uninstall removes the recorded root whatever it is | `TestUninstallNeverRemovesALocalPathCheckout` |
| uninstall follows a symlinked checkout / studio directory | `TestNoDeletePathFollowsASymlink` |
| build-log retention follows a symlink | `TestNoDeletePathFollowsASymlink` |
| uninstall deletes the weights / the studio's data | `TestUninstallRemovesTheCheckout…` |
| retry re-runs every step | `TestFailedStep…`, `TestSweep…` |
| optional weights downloaded at install | `TestOptionalWeightIsFetchedOnlyWhenAskedFor` |
| sweep marks a running step failed, not interrupted | `TestSweep…` |
| a build step run without its own process group | `TestCancelKillsTheStepGroupAndKeepsWork` (the child `sleep` survives) |
| a step failure without its output | `TestFailedStep…` |
| tools not probed / backends not checked | `TestMissingTool…`, `TestHostMismatch…` |
| a gated weight fails the install | `TestGatedWeightAsksForATokenWithoutFailingTheInstall` |
| launch allowed from `failed_build` | `TestFailedStep…`, after adding a launch assertion; **missed** first |
| weight refusals not passed to substitution | `TestSubstitutedPaths…`, `TestUnresolvable…` |

Two mutations were missed and left without a test, because under the code as written neither can cause harm.

- **Removing the `root != managed` guard in `removeCheckout`.** Removal only ever targets the managed path, so the guard is defence in depth. The mutation that does remove the recorded root is caught (above).
- **Sending a `ready` studio back through the build phase.** It re-runs no step, because the resume rule skips them all.

### End to end with the real binary

This ran in an isolated `HELMSTUDIO_HOME` with a space in its path. The studio was cloned from a local repository at a pinned commit.

1. Linked a weight before install; the response was `linked`, unverified, `ref_count 0`.
2. `:install` answered 202. `kill -9` of the daemon while step 2 slept.
3. Restart: the studio read `failed_build` with an `interrupted` failure; the job read `interrupted`, with step `quick` `succeeded` and step `slow` `interrupted`. `:launch` was refused, naming `failed_build`.
4. `:retry` ran only step 2. `GET /jobs/{id}/logs` streamed `step`, its lines and `end`. The studio read `ready`, with `commit_sha` and `runtime_env` recorded.
5. `:launch` ran, with `{models.base}` substituted as the linked directory's real path.
6. `:uninstall` while running: the studio stopped, `src` was removed, and `data` was kept. The artifact stayed, `ref_count 0`.
7. The reclaim preview was empty (the only artifact is linked). `DELETE /models/{id}` answered 204; the symlink was gone and the user's file intact.
8. A cross-origin `:install` was refused (403); SIGTERM shut down cleanly.

**This run found a problem (step 2): the build step survived `kill -9`.** It was fixed in review round 1 (#1), and the same run now shows the survivor stopped on restart.

## Design contradictions raised

Stopped at kickoff with 25; the human answered "recommendations for all". The questions, with both sides quoted, are in this file's git history and summarised in `docs/decisions.md`. The ones that changed a design document or a decision:

1. **No DDL for the six lifecycle tables.** Written into 02 §4 and shipped as schema v3.
2. **A stored weight reference count.** The brief and 06 §5 conflict with 02's `ref_count`. The count is removed, and 02 §3, §7, §8 and §10 are amended.
3. **"Dedup is `UNIQUE(sha256)`" under weights.** That is the asset rule. M3's reclaim is weights only.
4. **Two weights at one `(repo, revision)`** (open since M0). Resolved: one artifact, bindings with their own files.
5. **Uninstall "unless explicitly asked"** against R67, the brief's own review focus and the API outline. Resolved: uninstall never touches models.
6. **`studio_id` "a foreign key everywhere else"** against history surviving uninstall. 02 §3 amended.
7. **The file list.** Widened to `internal/manifest`, `internal/supervisor`, `internal/platform` and 02.

Found while building and recorded as open in `docs/decisions.md` ("Raised in M3"); none blocks the DoD:

- **A build step outlives a daemon killed with `kill -9`.** Resolved in review round 1 (#1) by the human's decision.
- **R16, R19 and `api/openapi.yaml` still describe a reference count.** 01 and the OpenAPI outline were not M3's to change; the field is served, computed from bindings.
- **`files` glob semantics exist only in code and the log.** The schema description needs them.
- **Linking over a failed download needs an uninstall and a reclaim first.**
- **There is no way to set the Hugging Face token** except with `security` by hand.
- **Not written by M3:** `update_available`/`remote_sha`/`:update`, `process_artifacts`, `last_used_at`.

## Judgement calls

All are in `docs/decisions.md` with reasons. The ones a reviewer should read first:

- **Departing from Q20, `If-Range` was not used.** Resume appends only when the 206's `Content-Range` starts at the `.part` length and names the listed size, and every URL is pinned to a commit. The CDN's ETag is not the listing's etag, so `If-Range` would have restarted every resumed file.
  - A `.part` longer than the listed size is discarded.
  - The etag is compared with the listing before any byte is written (review round 1, #7); a mismatch fails at once.
  - The `Verifier` seam receives the file path, for a later SHA256 check.
- **An optional weight is bound when it is fetched, not at install** (departs from Q6's "bound but not downloaded"). A binding then always means its files were listed, so launch can tell "not fetched" from "incomplete" without a new column.
- **`verified_at` is set only when a managed download completes.** It stays NULL for linked artifacts, including after a remount, which departs from 02 §7's "refreshed". A `stat` is not a verification, and R14a says the UI must not imply one.
- **`DELETE /models/{id}` unlinks linked artifacts only.** Superseded in review round 1 (#3): it now reclaims an unused download with a confirm.
- **Uninstall refuses when `<data>/studios/<id>` is not a real directory.** Found by reviewing the delete paths: `os.Root` opened on a symlinked studio directory would remove `src` inside the target.
- **Every write and removal under the models root goes through `os.Root`** (stdlib since Go 1.25), not hand-written `openat`/`unlinkat`. Probed first in scratch code: `RemoveAll` removes symlinks as links, and writes through an escaping symlink are refused. On darwin it also refuses writing through a symlink that stays inside the root, which is stricter than needed.
- **`last_failure` gains a `code`.** The gated-weight state was superseded in review round 1 (#5) by `auth_required`.
- **Build logs live at `<logs>/studios/<id>/build-<job>-<nn>.log`, and the last five install jobs are kept.** Clone output has no log file, because `log_files.owner_kind` has no clone owner; git's last 20 lines go into the failure message.
- **One job per studio at a time.**
  - Install on a running studio is refused.
  - Uninstall cancels a running install or download first.
  - State changes happen under `supervisor.HoldLaunches`, so no launch lands between the running check and the state change.
  - Cost: during a preempting launch's wait, an install request waits too.
- **A changed manifest digest restarts the install at the clone phase and deletes the studio's `step_runs`,** so every step runs again. A changed commit does the same for the build.
- **`size_gb` is decimal (10⁹ bytes) in the 90% linked-size check.**
- **`internal/weights/hubtest` is a non-test package** shared by three packages' tests. `go list -deps ./cmd/helmstudio` confirms neither it nor `testing` is in the daemon.
- **`store.NewID` duplicates the supervisor's unexported ULID** rather than editing the supervisor to export it.
- **In the supervisor test helper, a second studio sharing a checkout is recorded at a symlink alias,** because `root_path` is unique. Several M2 tests register many studios at one `local_path`.

## Could not verify

Nothing here touched a model, Metal, real weights or huggingface.co.

- **h3 installing from a clean state with weights.** On an Apple Silicon Mac with about 70 GB free:
  1. `make build && ./bin/helmstudio`
  2. `curl -X POST http://127.0.0.1:8700/api/v1/studios/h3-studio:install`
  3. Watch it: `curl -N http://127.0.0.1:8700/api/v1/jobs/<id>/logs` for the clone with submodules and `make -j8` in `h3c`, then `GET /jobs?studio=h3-studio` for the `fl2va` download (62 GB; `ref2va` is optional and not fetched).
  4. Expect `install_state: ready`, then `:launch` and a generated take.
- **Interrupt and resume a real download.** Mid-download, `kill -9` the daemon, restart it, and confirm `GET /models` shows `interrupted` with `bytes_on_disk` where it stopped. Retry with `:retry`, and confirm with `ls -l …/models/MiniMax-H3/FL2VA/*.part` that the partial file grows from its old length rather than from zero.
- **A real 403 from an expired signed URL.** Hugging Face's URLs last hours; the only way to see one is a download long enough to outlive one, or a pause (`kill -STOP` the daemon for longer than the URL lifetime, then `kill -CONT`). The log must show continued bytes, not a restart or `auth_required`.
- **The real Hub's shapes.** The following are written from documented behaviour, not observed:
  - The tree API's `lfs.oid`/`lfs.size` fields and `Link` pagination.
  - `X-Linked-Etag` on redirects.
  - Xet-backed repositories redirecting to a `cas-bridge` host that honours `Range`.
  - The etag the resolve endpoint reports equalling the listing's.

  If `X-Linked-Etag` differs, every file now fails before any bytes move (review round 1, #7), naming both values. The first real download proves or disproves it.
- **Same-host redirects from huggingface.co** (review round 1, #13), such as a renamed repository. They are treated as the CDN: the token is dropped, a 401 there reads as an expired URL, and the tree API does not follow them. Not observed against the real Hub.
- **Linking an existing MiniMax-H3 checkout.** `POST /studios/h3-studio/weights/fl2va:link {"path": "/path/to/MiniMax-H3"}` must succeed if `FL2VA/**` holds at least 55.8 GB (90% of 62). A Hugging Face cache snapshot (file symlinks to blobs) should also pass, since file symlinks count by their target. Then unplug or rename the directory: `GET /models` must show `missing`, and `:launch` must refuse naming the path.
- **The Keychain token path.** Store a token with `security add-generic-password -s helmstudio -a huggingface-token -w`, then install a gated repository.
- **Submodules.** No test clones a repository with submodules. git ≥ 2.38 blocks `file://` submodules without configuration the tests would have to set in the user's git config. h3 exercises this in step 1 above.
- **The window between `fork` and the identity write.** A daemon killed inside it leaves a step nothing can find, as M2 accepted for processes.
- **Linux.** It compiles (`vet-linux`). `statfs` on Linux and group kills of build steps there have not run.

## Dependencies added

None. `go.mod` is unchanged; `golang.org/x/sys` was already direct (M2) and now also provides `Statfs`.

## Files touched outside the milestone's list

M3's list is `internal/install/**`, `internal/weights/**`, `internal/store/**`, `cmd/helmstudio/**`, `internal/api/**` and `docs/decisions.md` (append only).

- **Widened at kickoff by the human (Q7):**
  - `internal/manifest/**`: new fields, `Digest` and a test.
  - `internal/supervisor/**`: launch reads installations and weights; `RestrictedEnv`, `Running`, `HoldLaunches`; the `StudioRoot` stand-in removed.
  - `internal/platform/**`: `FreeDiskBytes`, `HostOS`, `HostArch`, `HostBackends` and a test.
  - `docs/design/02-data-model.md`: the DDL, and the amendments to §3, §7, §8 and §10.
- **Approved by the human in review round 1:** `docs/design/01-prd.md` §5 gains the `auth_required` state (#5); 02 §4 gains `step_runs.pid/pid_start_time/pgid` and `auth_required` (#1, #5).
- **Tests I did not write, edited:**
  - M2's `internal/supervisor/helper_test.go`: `recordInstall`, called from `env.manifest` and the helper daemon.
  - `plan_test.go`: the `resolvePlan` call site.
  - `review_test.go`: the `resolvePlan` call site, and one `recordInstall` for a manifest loaded outside the helper.
  - M2's `internal/api/api_test.go`: installation rows, one root per studio.
  - Approved by the human for M2's tests (Q8).
  - **M1's `TestMigrationsFromEmptyCreateSchemaV1` in `internal/store/store_test.go`** was not named in Q8. It pins the exact version and table and index lists, so schema v3 had to change it. The change follows the human's M2 direction for the same test: version 3, the new names added, the lists still exact. I am naming it because it needs the human's sign-off.
- **`docs/agents/reports/03-install-and-weights.md`**: this file.
- **`web/` (the shelf) was not changed.** It has no install, link or reclaim controls; M3 is operable through the API only. For an uninstalled studio the shelf shows "No checkout at <where install will clone>".

## Decision-log entries appended

These are in `docs/decisions.md` under "2026-09-15 · M3 install and weights" and are not repeated here, to keep one copy. There are 34 entries:

- Q1–Q25's decisions (several combined into one entry, some carrying the judgement calls made while building them)
- the failure `code`
- auth-required state
- build logs
- one job per studio
- API additions for M4
- the token's Keychain account
- `hubtest` and `store.NewID`

Review round 1 appended eleven entries at the end of that section.

"Open, not yet decided" gained six "Raised in M3:" items and three "Raised in M3 review:" items (iris's download size, same-host redirects, the fork-to-write window), and "## Changes" gained one line. `git diff main -- docs/decisions.md` shows no removed lines.

## Left undone

- **h3's machine-bound demo and everything under "Could not verify".**
- **`update_available`, `remote_sha`, `:update`, the R14b model roots scan, the token prompt, and SHA256 `:verify`.** Weight verification is an open decision; the seam exists.
- **Asset reclaim.** M4.
- **Install controls on the shelf.** `web/` is outside the list.
- **Committing.** Per `implementer.md`, the work is uncommitted on `feat/install-and-weights`.
