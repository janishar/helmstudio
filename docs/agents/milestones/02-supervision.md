# M2 — Supervision

**Effort:** 7–8 days.
**Machine-bound demo:** launch h3, `kill -9` the daemon, watch it re-adopt.

Process supervision is where a framework is judged. A user whose eight-minute
generation was killed by a slow health probe does not file a bug; they stop
using it.

## Reads

`docs/design/01-prd.md`, `07-platform-services.md`, `08-h3-dry-run.md`,
`schema/manifest.json`.

## Tasks

### 1. Manifest loader

Load, validate (reuse `internal/manifest` from M0 — do not write a second
validator), resolve into a runnable plan.

### 2. Port allocation

Honour `prefer` and `fixed`. A `fixed` port already in use is a clear error
naming what holds it, not a retry loop.

### 3. Substitution

`{models.x}`, `{ports.x}`, `{paths.x}` into commands and environment.
**Absolute paths, substituted into commands. Nothing is symlinked into a studio
checkout** — a symlink inside the checkout collides with `git pull` on update
and produces a failure the user cannot interpret.

### 4. Spawn

`Setpgid: true` on every spawn, without exception. Dependency-ordered start.
Teardown in reverse order: SIGTERM to the group, grace period, then SIGKILL.

A process group is the only way to be sure a studio's children die with it.
h3c-studio already sets `Setpgid` at all three of its spawn sites; the
framework matching that is the floor, not an achievement.

### 5. Health

Three probe kinds: `path`, `tcp`, `exec`. A worker has no HTTP endpoint, which
is why `exec` exists.

**A `main` process is never auto-restarted, and never restarted on a failed
health check alone.** A slow probe during a long generation must not kill the
generation. `restart: on-failure` applies to sidecars and workers.

The optional `busy` probe is how the switch dialog stops guessing: liveness is
not occupancy.

### 6. Re-adoption

After a daemon restart, survivors are re-adopted by verifying **all three** of
pid, process start time, and pgid.

Checking pid alone eventually signals an unrelated process that inherited the
number — on a long-running machine this is not hypothetical, and the
consequence is the daemon killing something that has nothing to do with it.

Persist what is needed for this across a `kill -9`. The daemon does not get a
chance to write on the way down.

### 7. One heavy group at a time

Enforce it. Unified memory is shared and finite. State the arithmetic to the
user before enforcing it — a refusal a user cannot understand reads as a bug.

### 8. Logs and events

Per-process ring buffer, persisted tail, SSE stream. Backpressure is a design
question, not an afterthought: a studio that logs a megabyte a second must not
take the daemon with it.

### 9. Plain shelf

The minimum UI that lists studios, starts and stops them, and shows logs.
Unstyled — M6 owns design. Resist styling it; the tokens do not exist yet and
anything you invent now becomes a migration later.

## Files you may touch

    internal/supervisor/**, internal/platform/**
    cmd/helmstudio/**
    internal/api/** (only what the shelf needs)
    web/** (plain shelf)
    docs/decisions.md  (append only)

## Definition of done

- h3 launches from its manifest and is reachable.
- `kill -9` the daemon; restart; the studio is re-adopted, not orphaned and not
  killed.
- Teardown leaves no orphan — assert by pgid, not by `ps` and eyeball.
- A failing health check on a `main` process does not restart it. There is a
  test for this.
- A `fixed` port conflict produces an error naming the holder.
- Starting a second heavy studio is refused with the memory arithmetic.
- `make gate` green.

## Review focus

- Does re-adoption check **all three** of pid, start time and pgid? Two is a
  finding.
- Is there any code path where a failed health check restarts a `main`
  process — including indirectly, through a generic restart policy?
- Is `Setpgid` set on every spawn site, or most of them?
- On teardown, is SIGTERM sent to the **group** or the process?
- What happens when re-adoption state is stale — the pid is gone entirely?
- Does the log buffer bound its memory, and what does it do when the consumer
  is slower than the producer?
- Substitution: is any path relative, or anything symlinked into a checkout?
