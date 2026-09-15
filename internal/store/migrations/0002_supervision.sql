-- Schema v2: the process and log tables supervision persists across a daemon
-- restart, verbatim from docs/design/02-data-model.md §4 (added for M2).

CREATE TABLE processes (
  id TEXT PRIMARY KEY,
  studio_id TEXT NOT NULL,
  group_run_id TEXT NOT NULL,
  spec_name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('main','sidecar','worker','oneshot')),
  pid INTEGER, pid_start_time INTEGER, pgid INTEGER,   -- all three verified before adopting or signalling
  port INTEGER,
  command TEXT NOT NULL,                -- the fully substituted argv, as a JSON array
  state TEXT NOT NULL CHECK (state IN ('queued','starting','running','stopping','exited','failed')),
  health_state TEXT,                    -- unknown | passing | failing
  exit_reason TEXT CHECK (exit_reason IN ('clean','crashed','health_timeout','killed_by_user','preempted','oom')),
  exit_code INTEGER,
  started_at INTEGER, exited_at INTEGER);
CREATE INDEX idx_proc_live ON processes(state) WHERE state IN ('starting','running','stopping');
CREATE INDEX idx_proc_studio ON processes(studio_id, started_at DESC);

CREATE TABLE log_files (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,            -- relative to the logs root, never absolute
  kind TEXT NOT NULL CHECK (kind IN ('run','build')),
  owner_kind TEXT NOT NULL CHECK (owner_kind IN ('process','step_run')),
  owner_id TEXT NOT NULL,
  studio_id TEXT NOT NULL,
  truncated INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL);
CREATE INDEX idx_logs_studio ON log_files(studio_id, created_at DESC);
