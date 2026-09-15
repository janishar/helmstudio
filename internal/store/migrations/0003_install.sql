-- Schema v3: installations, jobs, build steps and the weights cache, verbatim
-- from docs/design/02-data-model.md §4 (added for M3).

CREATE TABLE installations (
  studio_id TEXT PRIMARY KEY,
  manifest_digest TEXT NOT NULL,        -- sha256 of the manifest's canonical JSON
  root_path TEXT NOT NULL UNIQUE,
  commit_sha TEXT, remote_sha TEXT, remote_checked_at INTEGER,
  install_state TEXT NOT NULL CHECK (install_state IN ('cloning','cloned','building','built',
    'fetching_weights','auth_required','ready','update_available','failed_clone','failed_build','failed_weights','removing')),
  runtime_env TEXT, selected_weights TEXT, last_failure TEXT,   -- JSON
  size_bytes INTEGER,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('install','build','download','update','export','uninstall')),
  studio_id TEXT,
  state TEXT NOT NULL CHECK (state IN ('queued','running','succeeded','failed','cancelled','interrupted')),
  subject_kind TEXT, subject_id TEXT,
  progress_num INTEGER, progress_den INTEGER,
  last_error TEXT,                      -- JSON
  created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER);
CREATE INDEX idx_jobs_studio ON jobs(studio_id, created_at DESC);
CREATE INDEX idx_jobs_live ON jobs(state) WHERE state IN ('queued','running');

CREATE TABLE step_runs (
  id TEXT PRIMARY KEY,
  studio_id TEXT NOT NULL REFERENCES installations(studio_id) ON DELETE CASCADE,
  job_id TEXT NOT NULL REFERENCES jobs(id),
  step_index INTEGER NOT NULL, step_name TEXT NOT NULL,
  command TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending','running','succeeded','skipped','failed','interrupted','cancelled')),
  exit_code INTEGER,
  pid INTEGER, pid_start_time INTEGER, pgid INTEGER,   -- verified before a surviving step is signalled
  log_file_id TEXT REFERENCES log_files(id) ON DELETE SET NULL,
  started_at INTEGER, finished_at INTEGER,
  UNIQUE (studio_id, job_id, step_index));

CREATE TABLE model_artifacts (
  id TEXT PRIMARY KEY,
  hf_repo TEXT NOT NULL,
  revision TEXT NOT NULL,               -- as declared, e.g. "main"
  commit_sha TEXT,                      -- the revision resolved at listing; downloads pin to it
  source TEXT NOT NULL CHECK (source IN ('managed','linked')),
  local_path TEXT NOT NULL UNIQUE,
  external_path TEXT, realpath TEXT,    -- linked only
  state TEXT NOT NULL CHECK (state IN ('declared','downloading','interrupted','auth_required','ready','linked','missing')),
  total_bytes INTEGER, verified_at INTEGER, last_used_at INTEGER,
  created_at INTEGER NOT NULL,
  UNIQUE (hf_repo, revision));

CREATE TABLE model_files (
  id TEXT PRIMARY KEY,
  artifact_id TEXT NOT NULL REFERENCES model_artifacts(id) ON DELETE CASCADE,
  rel_path TEXT NOT NULL, size_bytes INTEGER NOT NULL, etag TEXT,
  state TEXT NOT NULL CHECK (state IN ('pending','complete')),  -- partial progress is the .part length
  UNIQUE (artifact_id, rel_path));

CREATE TABLE studio_model_bindings (
  studio_id TEXT NOT NULL REFERENCES installations(studio_id) ON DELETE CASCADE,
  artifact_id TEXT NOT NULL REFERENCES model_artifacts(id) ON DELETE RESTRICT,
  placeholder TEXT NOT NULL,
  files TEXT,                           -- JSON allow-list this binding needs; NULL is the whole repo
  selected INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (studio_id, placeholder));
CREATE INDEX idx_bind_artifact ON studio_model_bindings(artifact_id);
