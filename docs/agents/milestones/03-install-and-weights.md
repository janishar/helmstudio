# M3 — Install and weights

**Effort:** 8–9 days.
**Machine-bound demo:** clean install; interrupt and resume a download; link an
existing model directory.

This milestone writes to the user's disk and deletes from it. It is the one
where a mistake is not recoverable.

## Reads

`docs/design/01-prd.md`, `06-storage.md`, `docs/decisions.md` (weights).

## Tasks

### 1. Install pipeline

Clone at a pinned ref, run `build[]` steps in the declared shell (`sh` by
default), report progress per step, and resume after interruption. Idempotent:
running install twice is not an error and does not redo finished work.

A build step's failure surfaces its output. A user cannot act on "build
failed".

### 2. Hugging Face downloader

Concurrent, resumable, progress-reporting.

**A 403 on resume is an expired signed URL, not corruption.** Re-request and
continue. Treating it as corruption throws away 50 GB and the user's afternoon,
and it will happen on every large download.

Weight verification is an open decision — size + etag (leaning) versus SHA256
behind an explicit Verify action. Implement the leaning, keep the seam, and do
not close the decision yourself.

### 3. Linked weights

Managed and linked are the same row with a different `source`. A 60 GB
checkpoint the user already has is not downloaded again.

- Linked directories are **read-only** to helmstudio. Every one of them.
- **No delete path ever follows a symlink.** Not reclaim, not uninstall, not
  cleanup, not a test.
- A linked directory that disappears becomes state `missing`, not an error and
  not a re-download.

### 4. Reclaim

Set-difference against what is actually referenced. Dedup is `UNIQUE(sha256)`;
**there is no stored reference count** — a counter drifts the first time a
transaction is interrupted, and the truth is derivable exactly.

Reclaim shows what it will delete and what that frees, and requires
confirmation. It never touches a models directory. No test invokes it against
anything but a temp root.

### 5. Uninstall

Remove a studio, leave its weights unless explicitly asked, never touch linked
weights at all.

## Files you may touch

    internal/install/**, internal/weights/**, internal/store/**
    cmd/helmstudio/**
    internal/api/** (install and weight endpoints)
    docs/decisions.md  (append only)

## Definition of done

- h3 installs from a clean state with weights.
- A download interrupted at 60% resumes at 60%.
- A 403 mid-download resumes rather than restarting. There is a test with a
  fake 403.
- A linked weight directory is never written to. There is a test that asserts
  the directory is unchanged.
- A test proves no delete path follows a symlink — build the symlink, run every
  delete path, assert the target survives.
- Reclaim's preview matches what it deletes exactly.
- `make gate` green.

## Review focus

- **Can any delete path follow a symlink?** Read every one. This is the finding
  that matters most in this milestone.
- Is 403-on-resume treated as an expired URL rather than corruption?
- Is any reference count stored anywhere, including a cache "for performance"?
- Does resume verify what it already has before appending to it?
- Is a linked directory opened for write anywhere, even to touch an mtime?
- Does uninstall have a path to a models directory at all?
- Does a partial install leave state that blocks a retry?
