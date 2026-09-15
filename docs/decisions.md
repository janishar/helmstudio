# Decision log

Append-only. One line per decision: the date, the decision, and the reason. Never delete an entry — supersede it with a new one that says what it replaces.

Read it before starting on anything and add to it when you finish. Whatever is not written down here gets re-decided later, differently, by whoever picks the work up next — including you, six months from now.

## Format

    YYYY-MM-DD · area · decision — reason. [supersedes: entry]

---

## 2026-09-15 · design sprint frozen

- 2026-09-15 · daemon · **Go, stdlib-first, single binary on 127.0.0.1:8700** — matches h3c-studio and iris-studio; zero-install for users; subprocess supervision and reverse proxy are stdlib-shaped.
- 2026-09-15 · manifest · **`processes[]` replaces a single `run` block** — a studio is not always one server; an engine sidecar or a queue worker must be a data change, not a Go special case. `run:` survives as sugar for a single main process.
- 2026-09-15 · manifest · **manifest lives in the studio's own repo as `helmstudio.yaml`; the registry holds pointers** — two copies drift, one does not; a studio can announce a new build step without a helmstudio release.
- 2026-09-15 · manifest · **three resolution sources, first match wins: local file → in-repo → registry inline** — most repos will never ship a manifest, so anyone must be able to write one. A manifest is a description of a repo, not a file only its author can provide.
- 2026-09-15 · manifest · **`build[].shell` declared now, default `sh`** — a later Windows port must not invalidate every manifest ever written.
- 2026-09-15 · runtime · **`framework` and `backends` are separate fields** — PyTorch-on-cuda and PyTorch-on-mps are entirely different answers for a given machine. The host check runs against `backends`, and an unsupported backend **blocks** install rather than warning.
- 2026-09-15 · supervision · **one heavy group at a time** — unified memory is shared and finite; the arithmetic is stated before it is enforced.
- 2026-09-15 · supervision · **a `main` process is never auto-restarted, and never restarted on a failed health check alone** — an eight-minute generation must survive a slow probe.
- 2026-09-15 · supervision · **re-adopt survivors after a daemon restart, verifying pid + process start time + pgid** — `kill -9` on the daemon leaves children alive; checking pid alone eventually signals an unrelated process that inherited the number.
- 2026-09-15 · supervision · **health probes support path, tcp and exec** — a worker has no HTTP endpoint.
- 2026-09-15 · supervision · **`busy` probe is optional but recommended** — liveness is not occupancy. h3 studio may be running and holding nothing, or running and holding 21 GB; without this the switch dialog guesses. [evidence: h3 dry run]
- 2026-09-15 · storage · **one SQLite file for all metadata, directories for all bytes; never BLOBs** — the query surface is genuinely relational (gallery feeds, provenance graphs, reclaim set-differences, FTS); a 2 GB video in a row breaks Range streaming, backup and hardlink adoption. [supersedes: an earlier JSON-document recommendation]
- 2026-09-15 · storage · **driver `modernc.org/sqlite` behind `database/sql`** — pure Go keeps `CGO_ENABLED=0`, cross-compilation, CI and the Electron build trivial; swapping to the cgo driver later is one line.
- 2026-09-15 · storage · **WAL, `synchronous(NORMAL)`, `busy_timeout(5000)`, `foreign_keys(ON)`; one write connection, separate read pool** — `foreign_keys` is off by default in SQLite, and without it every `ON DELETE RESTRICT` is inert.
- 2026-09-15 · storage · **dedup is `UNIQUE(sha256)`; there is no stored reference count** — a counter drifts the first time a transaction is interrupted; the truth is derivable exactly.
- 2026-09-15 · storage · **media: content-addressed blob plus a human-readable hardlink under `library/`** — same inode, no copy; the CAS path gives integrity, the library path gives a person somewhere to look.
- 2026-09-15 · storage · **all studio state goes through the SDK; `sessions` is a platform table** — being the only studio whose author got atomic writes right is not a property that scales to ten. Standalone uses the same schema at `./.helm/helm.db`, which makes later adoption an import rather than a translation.
- 2026-09-15 · storage · **four OS-resolved roots (data, cache, logs) plus models; `library` defaults to `~/helmstudio`** — cache dirs are excluded from backup and may be purged, which is right for derived files and wrong for blobs. The library is the one root chosen for discoverability over convention.
- 2026-09-15 · weights · **managed or linked, same row, different `source`** — a 60 GB checkpoint you already have should not be downloaded twice.
- 2026-09-15 · weights · **no delete path ever follows a symlink; linked directories are read-only** — deleting a user's own checkpoint is unrecoverable.
- 2026-09-15 · weights · **absolute paths substituted into commands; nothing symlinked into a studio checkout** — a symlink inside the checkout collides with `git pull` on update.
- 2026-09-15 · packages · **three packages, one-way dependency: `helm-css` → `helm-runtime-sdk` → `helm-ui-sdk`** — none of the four studios has a front-end toolchain, and release cadences genuinely differ.
- 2026-09-15 · packages · **only the runtime SDK knows the wire format; components receive a client, never construct one** — an API change then regenerates one package and leaves components untouched.
- 2026-09-15 · timeline · **framework owns the document and the export pipeline; `helm-ui-sdk` ships the editor** — a sequence made from four studios is nobody's studio document and must survive uninstalling half of it.
- 2026-09-15 · timeline · **conform to a declared target, with a stream-copy fast path when legal** — a run of takes from one studio is the common case and should cost seconds.
- 2026-09-15 · delivery · **browser at 127.0.0.1:8700 is the reference implementation; Electron is a window plus native chrome** — no feature may be reachable only from the app.
- 2026-09-15 · delivery · **single machine only** — loopback alone is not sufficient: `Host` and `Origin` checks plus a `SameSite=Strict` cookie close the browser CSRF hole.
- 2026-09-15 · scope · **macOS Apple Silicon at launch; Linux is a short hop, Windows is a real port** — Windows has no process groups or signals and `build[].run` assumes a POSIX shell.

## Open, not yet decided

- ffmpeg licensing: LGPL + videotoolbox (leaning) vs GPL + x264. **Gates M8 and what can be bundled in a signed .dmg.**
- Dedup headline: check whether the four manifests share a Hugging Face repo before promising a number.
- Reverse proxy: add `--base-path` to the four studios, or keep own-origin tabs.
- Vendoring `uv`: bundle a pinned binary vs detect and instruct.
- Weight verification: size + etag (leaning) vs SHA256 behind an explicit Verify action.

---

## Changes

<!-- date · what was built · what it changed about the decisions above -->
