# M1 — Foundation — implementation report

## What was built

`internal/platform` holds every OS-specific decision. It has the directories
helper (five roots plus the database, lock, backup and stage paths), the
secret store (the macOS Keychain, with an explicit refusal elsewhere) and an
exclusive file lock. Darwin, Linux and "unsupported" implementations are chosen
by build constraints, so nothing in the tree reads `runtime.GOOS`.

`internal/store` opens `helm.db` with the four pragmas on every connection. It
enforces a single writer, and runs forward-only migrations whose version 1 is
`docs/design/02-data-model.md` §5, verbatim.

`make gate` gains a dependency diff, a platform-boundary check and a Linux
type-check, and now builds `bin/helm` before validating.

Work stopped at kickoff on four design questions. It resumed on the human's
answers, which are recorded in `docs/decisions.md` under
"2026-09-15 · M1 foundation".

This report was revised after a peer review, which found 1 BLOCKING,
6 SHOULD-FIX and 6 NOTE issues. What changed is under "Review round 1" below.
I disagreed with one SHOULD-FIX, the nested `Update`. The human chose option
(a), now implemented; see "Review round 2".

## Review round 1

- **BLOCKING, fixed: a table-rebuild migration silently lost data.**
  `Migration.Up` ran inside a transaction on a connection with
  `foreign_keys(ON)`, where `PRAGMA foreign_keys = OFF` is a no-op. SQLite's
  documented create/copy/drop/rename procedure therefore fired `ON DELETE`
  actions on the `DROP`. Each migration now runs on one dedicated connection:
  foreign keys off before `BEGIN`, `PRAGMA foreign_key_check` before `COMMIT`
  (any violation rolls back and names the rows), and on again afterwards. New
  tests:
  - `TestRebuildingAParentTableKeepsChildReferences` rebuilds `sessions` and
    asserts `items.session_id` survives. It then asserts `ON DELETE SET NULL`
    still fires, proving enforcement is back and the foreign key still points
    at the rebuilt table.
  - `TestMigrationLeavingForeignKeyViolationsIsRolledBack` checks a violating
    migration is refused and leaves version 1.

  Mutation-checked, each reverted after its run:
  - dropping the OFF switch fails both new tests;
  - dropping the check fails the violation test;
  - dropping the restore fails the rebuild test, the writer pragma test and
    the RESTRICT test.
- **SHOULD-FIX, disagreed; resolved in round 2: a nested `Update` hangs**
  (`store.go`, `Update`). The review's reproduction calls the inner `Update`
  with `context.Background()`. The proposed fix, a marker on the context with
  an error on re-entry, only catches an inner call that reuses the outer
  context, so it would not catch the reproduced case. Go has no supported way
  to tell re-entry from the same goroutine apart from a legitimate concurrent
  `Update` on another goroutine; both must wait for the one connection. As the
  review asked, I stopped on this item rather than work around it. Options
  for a decision:
  - **(a)** Change the signature to `Update(ctx, func(ctx context.Context, tx *sql.Tx) error)`,
    mark the context it passes in, and error on re-entry with that context.
    This catches the idiomatic case but not an inner call using a fresh
    context, and that limit would be documented.
  - **(b)** Keep today's behaviour and documentation.
  - **(c)** Something that ties re-entry to goroutine identity, which I would
    reject as unsupported and fragile.

  I'd pick (a), with the limit stated plainly, if the partial guard is judged
  worth having.
- **SHOULD-FIX, fixed: the dependency diff let four real changes through.**
  It now compares name-and-version facts, including `go`, `toolchain` and
  each `replace` target. Both name and version must appear as whole tokens in
  one added line. Probes in a scratch clone now fail, as expected:
  - a same-branch `modernc.org/sqlite` v1.58.0 → v1.59.0;
  - `go 1.28.0` plus `toolchain go1.28.1`;
  - a new `modernc.org/lib` (no longer passing on `modernc.org/libc`);
  - a `replace` block for `gopkg.in/yaml.v3`;
  - a `replace` of the already-named `github.com/google/uuid` to an unnamed
    target;
  - the `gopkg.in/yaml.v3` bump.

  The baseline passes. On `main` the base is `HEAD`, so the check cannot see
  a dependency committed straight to `main`. That branch-only scope is now
  documented in the Makefile and the decision entry rather than fixed.
- **SHOULD-FIX, fixed: `boundaries` missed OS decisions and home lookups.** It
  now also matches:
  - OS-suffixed file names;
  - OS or `unix` terms in `//go:build` and `// +build` lines;
  - `user.Current(`;
  - `Getenv` or `LookupEnv` of `HOME`, `USERPROFILE` or `XDG_…`;
  - `$HOME` and `${HOME}`.

  Probes that now fail: `os.LookupEnv("HOME")`, `user.Current()`,
  `os.ExpandEnv("$HOME/x")`, `probe_darwin.go`, `probe_linux_arm64_test.go`,
  `//go:build darwin` and `//go:build !windows && unix`. A `//go:build machine`
  file named `foo_json.go` still passes. The two scripts moved into Make
  `define` blocks exported with `$(value …)`, so they read as plain bash.
- **SHOULD-FIX, escalated: a library on another volume breaks hardlinks.**
  Raised under "Open, not yet decided". Not resolved in code; M1 hardlinks
  nothing.
- **SHOULD-FIX, recorded and escalated: weights land in Time Machine.** The
  directories entry now records that cost. Backup exclusion (`tmutil` or the
  exclude xattr, behind a darwin seam) is raised as open for M3.
- **SHOULD-FIX, escalated: M2 is blocked twice.** M2's file list has no
  `internal/store/**`. Raised under "Open, not yet decided" for the human
  before M2 kickoff, and cross-referenced from the lifecycle-tables entry.
- **NOTE, addressed: backups.**
  - Retention is raised as open.
  - The `backup` doc comment and the backup decision entry now give the
    restore procedure: stop, delete `-wal` and `-shm`, then move the snapshot.
- **NOTE, addressed: `Reader` doc comment.** It now says `query_only` stops
  accidents, not intent, and that reads through `Reader` inside `Update` don't
  see that transaction's writes.
- **NOTE, acknowledged: Linux is compile-only.** `vet-linux` covers arm64 only.
  Landing M1 means the human explicitly accepts the waiver of the DoD row
  "`make gate` green on macOS and Linux".
- **NOTE, addressed: one place for open questions.** M1's open questions moved
  from "Raised in M1, not decided" into the top-level "Open, not yet decided"
  section. Each is prefixed "Raised in M1:". No pre-existing line of the log
  was altered: `git diff main -- docs/decisions.md` shows no removed lines.
- **NOTEs needing no change:** `security -i` passing exit status through, and
  the RESTRICT control and four-reader pragma checks.

## Review round 2

- **Nested `Update`, option (a), chosen by the human.**
  - `Update`'s callback is now `func(ctx context.Context, tx *sql.Tx) error`.
  - The ctx it receives carries a marker whose value is the `*Store`.
  - `Update` called with that ctx, or one derived from it, returns
    `ErrNestedUpdate` immediately.
  - The fresh-context gap is stated in `Update`'s doc comment and in a new
    decision entry: an inner call with `context.Background()` or the outer ctx
    still blocks forever.
  - Every test call site moved to the new signature.
- **Tests.**
  - `TestNestedUpdateWithPassedContextFailsPromptly` checks the inner call
    returns `ErrNestedUpdate` in under a second, and that the outer
    transaction is still usable and commits. Its base ctx has a 5 s deadline,
    so a broken guard fails the test instead of hanging the binary.
  - `TestMarkedContextDoesNotBlockAnotherStore` checks an `Update` on a
    second store inside the first store's callback succeeds.
  - The existing `TestConcurrentUpdatesSerialise` (20 goroutines, read then
    write) covers concurrent Updates on separate goroutines still
    serialising.
- **Mutation-checked**, each reverted after its run:
  - with the guard removed, the nested test fails after its 5 s deadline with
    `context deadline exceeded`;
  - a marker that refuses any marked ctx, rather than only its own store's,
    fails the two-store test;
  - a guard written as a per-store busy flag, which would also refuse
    concurrent Updates, fails the concurrency test.
- **Linux waiver.** The human accepted it, and the Linux test legs stay out of
  the gate. The earlier NOTE about landing needing that acceptance is settled.

## Gate

    $ rm -rf bin && go clean -testcache && make gate      # after review round 2
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: every go.mod change since e8920ff is recorded in docs/decisions.md
    ok  	github.com/janishar/helmstudio/cmd/helm	0.540s
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/manifest	1.040s
    ok  	github.com/janishar/helmstudio/internal/platform	1.554s
    ?   	github.com/janishar/helmstudio/internal/platform/platformtest	[no test files]
    ok  	github.com/janishar/helmstudio/internal/store	1.777s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

**macOS (Apple Silicon) only.** The DoD asks for "`make gate` green on macOS
and Linux". The human directed that the Linux and Windows test legs be ignored
and that the docs say so, so `go test` has not run on Linux. `vet-linux`
type-checks the Linux build and executes nothing. `go test -race ./internal/...`
was also clean, but it is not part of the gate.

Checks run by hand, outside the gate:

- **Deliberate bugs in `store.go`.** Each one was reverted after its run, and
  each was caught:
  - dropping `foreign_keys(ON)` fails the pragma tests and the RESTRICT test;
  - dropping `query_only` fails `TestReaderCannotWrite`;
  - a writer pool of 4 fails `TestWriterIsOneConnection`;
  - dropping `synchronous` fails both pragma tests;
  - skipping the lock fails `TestSecondOpenIsRefused`.
- **The dependency diff on a real `go.mod` change**, in a scratch clone of this
  tree:
  - M1's `go.mod` with M1's decision entries passes;
  - bumping `gopkg.in/yaml.v3` to v3.0.2 fails and names the module;
  - adding a `replace (gopkg.in/yaml.v3 => ../yaml)` block fails and names the
    module.
- **The boundaries check.** A temporary untracked `cmd/helmstudio` file using
  `runtime.GOOS` and `os.UserHomeDir()` made it fail on both lines. The file
  was then removed.
- **No writes to the real roots.** After the gate, none of
  `~/Library/Application Support/helmstudio`, `~/Library/Caches/helmstudio`,
  `~/Library/Logs/helmstudio` or `~/helmstudio` exists.

## Design contradictions raised

I stopped before writing code and raised these. The human decided 1–4 as
recommended, except the permissions in item 4.

1. **Schema v1 has no single source.**
   - `docs/design/00-index.md` says 02 has "17 tables with full DDL". 02 §3's
     ER diagram has 20 tables, and 10 of them have DDL.
   - 06 §5 ("The platform half, in full") and 02 §5 disagree on `items`
     (`session TEXT` vs `session_id … REFERENCES sessions(id) ON DELETE SET NULL`,
     plus `timeline_id`).
   - They also disagree on `assets` (`fps`, `blob_path`, `library_path` and
     `state` are only in 02), on `sessions` and `timelines` (only in 02), on
     `created_at` nullability, and on index names and sets.

   **Told how to proceed:** 02 §5 verbatim. The lifecycle tables wait for their
   DDL to be written into 02.
2. **The directories helper was under-specified.**
   - No models or stage location, and no names for the override variables.
   - "The stored setting" can't apply to the data root.
   - 02 §4 says weights "belong to the cache", which the milestone calls
     purgeable "at any moment".

   **Told how to proceed:** the proposal now recorded in `docs/decisions.md`.
3. **The secrets fallback.** The milestone says "an explicit fallback
   elsewhere", and 01-prd §14 lists "the secret store" as unbuilt Linux work.
   **Told how to proceed:** an explicit error, with no plaintext file.
4. **The milestone contradicts its own file list.** Task 1 lists
   `internal/{supervisor,install,weights,api}` and `test/`, but "Files you may
   touch" lists only `internal/platform/**` and `internal/store/**`.
   **Told how to proceed:** permission to create them was not given, so they
   were not created.

Found and logged under "Raised in M1, not decided", without stopping (none
blocks M1):

- 02 §8's provenance query fails with `circular reference: lineage`.
- `items_fts` indexes a `prompt` column that `items` doesn't have, and joins on
  a rowid that `VACUUM` may renumber.
- 07 §4 and §8 still describe asset reference counts and JSON state files.
- `model_artifacts.ref_count` is a stored counter.
- The milestone and the decision log say "Four OS-resolved roots — data, cache,
  logs —" but name three.
- Schema v1 has no `settings` table for the stored-root hook to read.

## Judgement calls

- **Single-writer enforcement beyond the recipe.** 06 §8 caps the writer at one
  connection. I added three things on top of that:
  - a `query_only(1)` read pool;
  - `_txlock=immediate` on the writer;
  - a non-blocking `flock` on `helm.db.lock`, behind a new
    `platform.LockExclusive` seam.

  I rejected leaving it as convention, because review focus asks exactly this.
  The lock is a separate file, not `helm.db` itself, so it never interacts
  with SQLite's own `fcntl` locks. Cost: `Update` must not nest (documented),
  and a second `Open` in one process fails rather than sharing the store.
- **The pre-migration backup.** 06 §9 says to "take that snapshot before the
  first migration of a session", but the milestone doesn't list it.
  - Implemented as `VACUUM INTO helm.db.bak.<from-version>`, via a `.tmp` name
    and a rename, replacing any earlier snapshot at the same version.
  - Rejected refusing when a backup already exists, because a single failed
    migration would then block every later start.
  - Skipped for an empty database.
- **The DSN is a percent-encoded `file:` URI.** "Application Support" contains
  a space, and a `?` or `#` in a directory name would otherwise end the path.
  `TestPathsThatNeedEscaping` covers it.
- **Pragmas are asserted on four reader connections held open at once.**
  Checking one connection would pass even if the pragmas had been applied to
  the first connection only.
- **The RESTRICT test has a control.** The same delete on the same file, over a
  connection without `foreign_keys`, succeeds. Without that control, the
  RESTRICT test could pass for some reason unrelated to the pragma.
- **Keychain.** `/usr/bin/security -i` reads its command from stdin, and `-X`
  carries the value hex-encoded. Exit status 44 maps to `ErrSecretNotFound`.
  Names are restricted to `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`, and values to
  printable ASCII of at most 512 bytes. The 512-byte limit is a guess at a
  safe line length for `security -i`; it is not measured.
- **Linux defaults.** An empty or relative `XDG_*` value is ignored in favour
  of its spec fallback (`~/.local/share`, `~/.cache`, `~/.local/state`), as the
  XDG Base Directory spec says to. Both OS conventions are pure functions,
  tested on macOS.
- **Root permissions.** `Ensure` creates data, cache and logs as `0700`, and
  library and models as `0755`, since a person browses those two.
- **Dependency-diff semantics** (revised in review round 1).
  - The base is the merge-base with `main`, with uncommitted changes counted.
  - Indirect modules count, and so do `go` and `toolchain` lines and `replace`
    targets.
  - A change counts as recorded if one added line of `docs/decisions.md` names
    it and its version as whole tokens.
  - Remaining limits: it checks branches only (on `main` the base is `HEAD`),
    and any line naming the pair satisfies it, whatever the line says.
- **`boundaries` is a text match** (widened in review round 1). The full list
  of what it matches is in the decision entry. A path built from some other
  absolute string is not caught.
- **Migrations run with foreign keys off** (review round 1). The alternative,
  handing `Up` a connection and letting each migration manage the pragma, was
  rejected: it would move the same trap to every future migration author.
- **`cmd/helmstudio` exits 1 with "not implemented yet".** The gate's `build`
  step needs a main package, and anything more would be M2's work.
- **`validate` now depends on `build`.** This closes the stale-binary gap
  called out in M0's report.

## Could not verify

- **Keychain round-trip (the machine-bound demo).** The fake-runner tests prove
  argv never carries the secret, and that the command line and exit-code
  mapping are what the code intends. They do not prove that `security -i`
  accepts that line, or that `-X` round-trips through `-w`. To demonstrate it,
  run this on an Apple Silicon Mac, logged in, with the login keychain
  unlocked:

      go test -tags machine -run TestKeychainRoundTrip -v ./internal/platform

  It writes, reads, replaces and deletes `helmstudio.test`/`roundtrip` in the
  login keychain. The value used contains quotes and a backslash. Also watch
  for a Keychain access prompt: none is expected, because the item's creator
  is `security` itself.
- **Linux.** No test has run there. The gate proves only that the Linux build
  type-checks (`vet-linux`). In particular, `linuxDefaults` against a real
  environment, `flock` on Linux, and case-sensitive path behaviour are
  unverified.
- **Windows.** Out of scope. It compiles to explicit refusals (`other.go`,
  `lock_other.go`), which were type-checked once by hand and are not in the
  gate.
- **`VACUUM INTO` backups of a large database, and the lock on network or
  external volumes** (flock semantics vary with the filesystem).

## Dependencies added

- **`modernc.org/sqlite` v1.58.0.** The SQLite driver behind `database/sql`.
  The milestone requires it, and it was decided on 2026-09-15; the standard
  library has no SQLite. It pulls in, indirectly: `modernc.org/libc` v1.75.6,
  `modernc.org/mathutil` v1.7.1, `modernc.org/memory` v1.12.1,
  `github.com/dustin/go-humanize` v1.0.1, `github.com/google/uuid` v1.6.0,
  `github.com/mattn/go-isatty` v0.0.24, `github.com/ncruces/go-strftime`
  v1.0.0, `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec,
  `golang.org/x/sys` v0.47.0. All are named in `docs/decisions.md`.
  Downloading it was approved by the human.

## Files touched outside the milestone's list

- **`docs/agents/gate.md`.** One paragraph saying the Linux and Windows test
  legs are not run, pointing at the decision entry. Added because the human
  asked for it to be mentioned in the docs, and because the table there would
  otherwise claim a Linux run that does not happen.
- **`docs/agents/reports/01-foundation.md`** (this file), which
  `implementer.md` requires.
- **`docs/decisions.md`.** Listed as append-only. Lines were inserted as a new
  "2026-09-15 · M1 foundation" section before "## Changes", as "Raised in M1:"
  items at the end of "Open, not yet decided", and as one line under
  "## Changes". No line that existed before M1 was altered.

## Decision-log entries appended

See `docs/decisions.md`, section "2026-09-15 · M1 foundation". It has twelve
entries: schema v1, lifecycle tables deferred, directories, secrets,
single-writer enforcement, the nested-`Update` guard (round 2), pre-migration
backup, migrations with foreign keys off, dependencies, gate host-only,
dependency-diff semantics, and boundaries.

The top-level "Open, not yet decided" section has eleven "Raised in M1:" items:
- the seven from the first pass;
- four added in review round 1: library across volumes, backup exclusion for
  `models`/`stage`, M2's file list lacking `internal/store/**`, and backup
  retention.

There is also one line under "## Changes". None is repeated here verbatim, to
keep one copy.

## Left undone

- **Skeleton packages** `internal/{supervisor,install,weights,api}` and
  `test/`. They are outside the file list, and permission was not given.
- **DoD "`make gate` green on macOS and Linux"** is met on macOS only, by the
  human's direction. No CI workflow was added.
- **The lifecycle tables.** They are blocked on their DDL existing in 02.
- **Stored root settings** are a hook with nothing behind it until a
  `settings` table exists.
- **Adopting an existing `~/.helmstudio`.** Deferred by decision.
- **Committing.** Per `implementer.md`, the implementer does not commit. The
  work is uncommitted on `feat/foundation`.
