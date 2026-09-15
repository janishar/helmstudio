# M1 — Foundation

**Effort:** 5–6 days. **Machine-bound demo:** Keychain round-trip.

The layer everything else sits on: where files live, where state lives, and the
gate that keeps both honest.

## Reads

`docs/design/02-data-model.md`, `06-storage.md`, `07-platform-services.md`.

## Tasks

### 1. Repository skeleton

`cmd/helmstudio`, `internal/{platform,store,supervisor,install,weights,api}`,
`test/`. Empty packages with a doc comment are fine; the point is that the next
milestone has somewhere obvious to put things.

### 2. Platform seams

Every OS-specific decision behind an interface, with a macOS implementation and
a Linux one. Nothing else in the tree may call `runtime.GOOS`. The Linux
implementation is not aspiration — the gate runs there.

### 3. Directories helper

Four OS-resolved roots — data, cache, logs — plus models, and `library`
defaulting to `~/helmstudio`.

Cache is excluded from backup and may be purged by the OS at any moment. That
is right for derived files and catastrophic for blobs; the helper is what makes
that distinction impossible to get wrong by accident. The library root is the
one chosen for discoverability over convention: it is where a person looks.

Every path in the system comes from this helper. A test that writes outside a
temp root is a bug in the test.

### 4. Store — schema v1

SQLite via `modernc.org/sqlite` behind `database/sql`. Pure Go keeps
`CGO_ENABLED=0` and makes cross-compilation, CI and the Electron build
trivial; moving to the cgo driver later is one line.

On open: WAL, `synchronous(NORMAL)`, `busy_timeout(5000)`,
`foreign_keys(ON)`. **`foreign_keys` is off by default in SQLite** — without
it every `ON DELETE RESTRICT` in the schema is inert and the constraint you
think you have does not exist.

One write connection. A separate read pool. Migrations are forward-only,
numbered, and applied in a transaction.

Prove each pragma with a test that queries it back. A comment asserting WAL is
not evidence.

### 5. Secrets

Keychain on macOS, an explicit fallback elsewhere. Behind a platform seam. The
round-trip is machine-bound — it cannot pass in CI, and must not be reported as
if it did.

### 6. The gate

`make gate`: `gofmt -l` · `go vet ./...` · dependency diff · `go test ./...` on
macOS and Linux · `helm validate studios/*.yaml`.

The dependency diff fails when `go.mod` changed without a matching
`docs/decisions.md` entry. See `docs/agents/gate.md`.

## Files you may touch

    Makefile
    cmd/helmstudio/**
    internal/platform/**, internal/store/**
    .github/** (or the CI config the repo adopts)
    go.mod, go.sum
    docs/decisions.md  (append only)

## Definition of done

- `make gate` green on macOS and Linux.
- A test opens the store and reads back WAL, `foreign_keys`, `busy_timeout`
  and `synchronous`.
- A test proves `ON DELETE RESTRICT` actually restricts.
- Migrations apply from empty and are idempotent on re-run.
- No path in the tree is constructed outside the directories helper.
- No `runtime.GOOS` outside `internal/platform`.

## Review focus

- Are WAL and `foreign_keys(ON)` proven by a test, or asserted in a comment?
- Does any path escape the directories helper? Grep for path joins against a
  home directory.
- Is the single-writer discipline actually enforced, or a convention a future
  milestone will break silently?
- Does the dependency-diff check fail on a real `go.mod` change? Try one.
- Does a test leave anything behind outside its temp root?
