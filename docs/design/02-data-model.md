# Data Model

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

One SQLite file holds every row; directories hold every byte. The database is the index for a lifecycle (installs, processes, jobs, weights) and a platform (state, records, assets, gallery, timelines) — small enough to snapshot in under a second, and never holding a video.

## 1 · What is stored, and what is derived

Three questions decide whether something is a row. Does the daemon need it after a restart? Can it be re-derived cheaply from a manifest or a probe? Would losing it lose the user's work? Anything answering "no, yes, no" is derived, which is why the catalogue itself has no table — studios, their build steps and their process specs are parsed from `studios/*.yaml` at startup, and a manifest digest on the installation records which version a checkout was built from.

**Derived: Studio**

Parsed from the manifest. A table would be a stale copy of a file the daemon already reads.

**Derived: ProcessSpec**

From `processes[]`. The *instance* is stored; the spec is read.

**Derived: PortLease**

In-memory allocator plus a `net.Listen` probe. `processes.port` already records what was used.

**Derived: download progress**

`stat` the `.part` files. A database does not make a pointless write worth making.

## 2 · Four clusters, joined at three places

Three joins carry the product's claims. `studio_model_bindings` makes "two studios, one download" true. `process_artifacts` makes "which model is this process holding" answerable while it runs. `item_inputs` makes provenance a graph — the thing no studio could record alone, because the earlier link happened somewhere else.

## 3 · ER diagram

``` mermaid
erDiagram
    INSTALLATIONS ||--o{ STEP_RUNS : records
    INSTALLATIONS ||--o{ JOBS : "is worked by"
    INSTALLATIONS ||--o{ PROCESSES : spawns
    INSTALLATIONS ||--o{ STUDIO_MODEL_BINDINGS : binds
    MODEL_ARTIFACTS ||--o{ MODEL_FILES : contains
    MODEL_ARTIFACTS ||--o{ STUDIO_MODEL_BINDINGS : "is shared through"
    PROCESSES ||--o{ PROCESS_ARTIFACTS : holds
    MODEL_ARTIFACTS ||--o{ PROCESS_ARTIFACTS : "is held by"
    PROCESSES ||--o| LOG_FILES : "streams to"
    STEP_RUNS ||--o| LOG_FILES : "writes to"
    ASSETS ||--o{ DERIVED : "renders to"
    ASSETS ||--o{ ITEMS : "is shown as"
    ASSETS ||--o{ ITEM_INPUTS : "is input to"
    ITEMS ||--o{ ITEM_INPUTS : "derives from"
    ITEMS ||--o{ TAGS : "is tagged"
    SESSIONS ||--o{ ITEMS : groups
    SESSIONS ||--o{ RECORDS : scopes
    ITEMS ||--o| INBOX : "is handed off as"
    TIMELINES ||--o{ ITEMS : "exports to"

    INSTALLATIONS {
        string studio_id PK
        string manifest_digest
        string root_path UK
        string commit_sha
        string remote_sha
        string install_state
        json runtime_env
        json selected_weights
        json last_failure
        int64 size_bytes
    }
    STEP_RUNS {
        ulid id PK
        string studio_id FK
        ulid job_id FK
        int step_index
        string step_name
        text command
        string state
        int exit_code
        ulid log_file_id FK
    }
    PROCESSES {
        ulid id PK
        string studio_id FK
        ulid group_run_id
        string spec_name
        string role
        int pid
        int64 pid_start_time
        int pgid
        int port
        text command
        string state
        string health_state
        string exit_reason
    }
    PROCESS_ARTIFACTS {
        ulid process_id FK
        ulid artifact_id FK
        string placeholder
    }
    JOBS {
        ulid id PK
        string kind
        string studio_id FK
        string state
        string subject_kind
        string subject_id
        int64 progress_num
        int64 progress_den
        json last_error
    }
    LOG_FILES {
        ulid id PK
        string path UK
        string kind
        string owner_kind
        string owner_id
        bool truncated
    }
    MODEL_ARTIFACTS {
        ulid id PK
        string hf_repo
        string revision
        string source
        string local_path UK
        string external_path
        string realpath
        string state
        int64 total_bytes
        int64 verified_at
    }
    MODEL_FILES {
        ulid id PK
        ulid artifact_id FK
        string rel_path
        int64 size_bytes
        string etag
        string state
    }
    STUDIO_MODEL_BINDINGS {
        string studio_id FK
        ulid artifact_id FK
        string placeholder
        bool selected
    }
    SESSIONS {
        ulid id PK
        string studio_id FK
        string name
        text state
        string etag
        int64 created_at
        int64 opened_at
        int64 deleted_at
    }
    KV {
        string studio_id PK
        string ns PK
        string key PK
        text doc
        string etag
    }
    RECORDS {
        ulid id PK
        string studio_id FK
        string collection
        text doc
        int64 created_at
        int64 deleted_at
    }
    ASSETS {
        ulid id PK
        string sha256 UK
        string kind
        string mime
        int64 bytes
        int width
        int height
        real duration_s
        string blob_path
        string library_path
        string origin_studio
        bool pinned
        string state
    }
    DERIVED {
        ulid asset_id FK
        string variant
        string path
        int64 bytes
    }
    ITEMS {
        ulid id PK
        string studio_id FK
        ulid asset_id FK
        ulid timeline_id FK
        ulid session_id FK
        string kind
        string title
        text params
        bool starred
        int64 created_at
        int64 deleted_at
    }
    ITEM_INPUTS {
        ulid item_id FK
        ulid asset_id FK
        string role
    }
    TAGS {
        ulid item_id FK
        string tag
    }
    TIMELINES {
        ulid id PK
        string name
        json target
        json tracks
        int revision
        int64 updated_at
    }
    INBOX {
        ulid id PK
        string to_studio
        string from_studio
        ulid item_id FK
        int64 consumed_at
    }
    SETTINGS {
        string key PK
        json value
    }
```

`studio_id` is the primary key of `installations`: a studio has at most one checkout on a machine, which is the constraint that removes all ambiguity about which build is live. It is a declared foreign key only on `step_runs` and `studio_model_bindings`, which cascade with an uninstall; everywhere else it is a plain column, because history outlives the checkout (amended 2026-09-15, M3).

## 4 · Lifecycle tables

### installations

One row per studio materialised on this machine, keyed by `studio_id`.

| Field                                             | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                   |
|---------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `manifest_digest`                                 | What this checkout was built from. A mismatch against the loaded manifest means "rebuild needed" — a quieter signal than an upstream change.                                                                                                                                                                                                                                                                                            |
| `commit_sha` · `remote_sha` · `remote_checked_at` | Local HEAD and the upstream tip. `update_available` is `remote_sha != commit_sha`; without both fields the question is unanswerable.                                                                                                                                                                                                                                                                                                    |
| `install_state`                                   | The canonical vocabulary from the PRD.                                                                                                                                                                                                                                                                                                                                                                                                  |
| `runtime_env`                                     | Resolved at the end of a successful build: venv path, python version, go version, tool versions, and the detected versions of any framework the manifest names — `torch 2.6.0`, `mlx 0.31.2`, the Metal version. This is what the studio page shows and what a "why is this slower than the README" question starts from. The declared `runtime` block itself is not stored: it is read from the manifest like everything else derived. (Amended 2026-09-15, M5, Q7: for a studio with `python`, keys `venv`, `python` (the full version), `uv` (its `--version`), and the installed version of the distribution `runtime.framework` maps to — `torch`, `mlx`, `jax` or `onnxruntime`; nothing for `native-kernel`, `ggml` or `other`.) |
| `selected_weights`                                | Placeholder → artifact id for `selectable` weights. This is how iris studio remembers which checkpoint to launch with.                                                                                                                                                                                                                                                                                                                  |
| `last_failure`                                    | `{phase, step_index, exit_code, log_file_id, message}`. The failure block renders straight from this. (Amended 2026-09-15, M5, Q7: code `env_failed` means creating the Python environment failed; it has no `step_index`, and its log is owned by the install job.)                                                                                                                                                                  |
| `size_bytes`                                      | Checkout plus build output. Excludes weights, which belong to the cache.                                                                                                                                                                                                                                                                                                                                                                |

    -- added 2026-09-15 for M3 (docs/decisions.md, "M3 install and weights"). remote_checked_at is
    -- named by this section's prose but missing from the ER list; created_at and updated_at are new.
    -- root_path is absolute: <data>/studios/<id>/src when helmstudio cloned it, or the manifest's
    -- local_path. Uninstall removes a root only when it is the former.
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

### processes

One row per spawned process, not per studio — which is what makes a multi-process studio representable. `group_run_id` ties one launch together so the UI can say "the group is starting" while only the sidecar is up.

| Field                             | Notes                                                                                                                                                                         |
|-----------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `spec_name` · `role`              | Which `processes[]` entry, and whether `main`, `sidecar`, `worker` or `oneshot`.                                                                                              |
| `pid` · `pid_start_time` · `pgid` | All three, all verified before adopting or signalling after a daemon restart. Storing `pid` alone eventually means SIGKILL to an unrelated process that inherited the number. |
| `command`                         | The fully substituted argv. Reproducing a failure starts with reading what actually ran.                                                                                      |
| `state` · `health_state`          | Separate, because healthy-then-unresponsive is a different thing from exited.                                                                                                 |
| `exit_reason`                     | `clean | crashed | health_timeout | killed_by_user | preempted | oom`. The UI's sentence is chosen from this, never guessed from a code.                                      |

**Indexes:** `idx_proc_live` over `state ∈ (starting, running, stopping)` for the top-bar indicator and the startup sweep; `idx_proc_studio` by `(studio_id, started_at DESC)` for history. No restart counter on a `main` process — it never auto-restarts, because a health probe must never be able to kill an eight-minute generation.**

    -- added 2026-09-15 for M2 (docs/decisions.md, "M2 supervision"). started_at, exited_at and
    -- exit_code are used by this section's prose and indexes but were missing from the ER list.
    -- studio_id has no REFERENCES installations(studio_id), deliberately: process history outlives an
    -- uninstall (amended 2026-09-15, M3; see "processes, log_files and jobs deliberately have no
    -- foreign key" below).
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

### jobs, step_runs, log_files

`jobs` carries `kind` (`install | build | download | update | export | uninstall`), `studio_id` — present even when the subject is an artifact, because "what is this studio downloading" is a per-studio query — plus progress and `last_error`. `step_runs` is one row per executed build step, unique on `(studio_id, job_id, step_index)`, which is what lets Retry resume from the first step that has not succeeded; a `running` row found at startup becomes `interrupted`, never silently `failed`, because the distinction changes what Retry should do. `log_files` is an index row pointing at an append-only file; bytes never enter the database, and retention is `os.Remove` on the oldest five per studio, with `truncated` making head-and-tail elision honest in the UI.

    -- added 2026-09-15 for M2 (docs/decisions.md, "M2 supervision"). created_at is not in the ER
    -- list; retention ("the oldest five per studio") needs an order. studio_id is denormalised from
    -- the owner so retention is one query per studio rather than a join per owner kind.
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

    -- added 2026-09-15 for M3 (docs/decisions.md, "M3 install and weights"). The state vocabularies
    -- are new; so are created_at, started_at and finished_at. jobs.studio_id and step_runs.job_id
    -- carry no cascade: job history outlives an uninstall. step_runs belong to one installation and
    -- go with it. When an installation's commit or manifest digest changes, its step_runs are
    -- deleted, so "the first step that has not succeeded" is read from the rows that remain.
    -- step_runs.pid, pid_start_time and pgid (amended 2026-09-15, M3 review): a step leads its own
    -- process group and outlives a daemon killed with SIGKILL; the next daemon's sweep stops a
    -- survivor whose three fields still match, as re-adoption verifies processes, before recording
    -- the step interrupted. install_state 'auth_required' (same amendment): a gated weight waits for
    -- a token without the install having failed (R18).
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

`processes`, `log_files` and `jobs` deliberately have no foreign key to `installations`: uninstall deletes the installation row, and the history of what ran must survive it, as the gallery and timelines will.

**Studio-reported jobs** (amended 2026-09-15, M4; `docs/decisions.md` "M4 API and SDK", Q24). A studio's own long work — a render — runs inside the studio, so the daemon cannot cancel it or write its log. It is a `task` job the studio creates and updates through the API; cancellation is a request the studio honours. Schema v4 rebuilds both tables:

    -- jobs: 'task' added to kind; cancel_requested_at records a cancel the studio has not acted on yet.
    CREATE TABLE jobs (
      id TEXT PRIMARY KEY,
      kind TEXT NOT NULL CHECK (kind IN ('install','build','download','update','export','uninstall','task')),
      studio_id TEXT,
      state TEXT NOT NULL CHECK (state IN ('queued','running','succeeded','failed','cancelled','interrupted')),
      subject_kind TEXT, subject_id TEXT,
      progress_num INTEGER, progress_den INTEGER,
      last_error TEXT,                      -- JSON
      created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER,
      cancel_requested_at INTEGER);
    -- idx_jobs_studio and idx_jobs_live are recreated unchanged.

    -- log_files: a task job's lines are appended by the daemon on the studio's behalf.
    CREATE TABLE log_files (
      id TEXT PRIMARY KEY,
      path TEXT NOT NULL UNIQUE,            -- relative to the logs root, never absolute
      kind TEXT NOT NULL CHECK (kind IN ('run','build','task')),
      owner_kind TEXT NOT NULL CHECK (owner_kind IN ('process','step_run','job')),
      owner_id TEXT NOT NULL,
      studio_id TEXT NOT NULL,
      truncated INTEGER NOT NULL DEFAULT 0,
      created_at INTEGER NOT NULL);
    -- idx_logs_studio is recreated unchanged.

### model_artifacts, model_files, studio_model_bindings

One artifact per Hugging Face `(repo, revision)`, whichever studio declared it first. A binding is one studio's use of it under one placeholder, with the file allow-list that weight declared. Two weights of one repo with different `files` — h3's FL2VA and Ref2VA — are one artifact and two bindings, and the second binding downloads only the files it adds. **There is no stored reference count:** an artifact's references are its binding rows, counted when read, and an artifact with none is reclaimable when it is managed.

    -- added 2026-09-15 for M3 (docs/decisions.md, "M3 install and weights"). Supersedes the ER
    -- list's ref_count and the 'orphaned' state. commit_sha, last_used_at, created_at and
    -- bindings.files are new. local_path is <dest>, relative to the models root, which is settable.
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

`ON DELETE RESTRICT` on the binding means the database itself refuses to delete an artifact a studio still uses, behind the reclaim query.

## 5 · Platform tables

    -- sessions: every studio has this concept, so the platform owns it rather than each
    -- studio inventing a directory convention. This is what replaces sessions/*/setting.json.
    CREATE TABLE sessions (
      id TEXT PRIMARY KEY,
      studio_id TEXT NOT NULL,
      name TEXT NOT NULL,
      state TEXT NOT NULL DEFAULT '{}',   -- the studio's own shape: prompt, seed, dims, refs…
      etag TEXT NOT NULL,                 -- optimistic concurrency for two open tabs
      created_at INTEGER NOT NULL, opened_at INTEGER, deleted_at INTEGER);
    CREATE UNIQUE INDEX uq_session_name ON sessions(studio_id, name) WHERE deleted_at IS NULL;
    CREATE INDEX idx_session_recent ON sessions(studio_id, opened_at DESC) WHERE deleted_at IS NULL;

    -- singleton studio state: UI preferences, last opened session, panel widths
    CREATE TABLE kv (
      studio_id TEXT, ns TEXT, key TEXT,
      doc TEXT NOT NULL, etag TEXT NOT NULL, updated_at INTEGER NOT NULL,
      PRIMARY KEY (studio_id, ns, key)) WITHOUT ROWID;

    -- domain documents helmstudio deliberately does not model
    CREATE TABLE records (
      id TEXT PRIMARY KEY, studio_id TEXT NOT NULL, collection TEXT NOT NULL,
      doc TEXT NOT NULL, created_at INTEGER, updated_at INTEGER, deleted_at INTEGER);
    CREATE INDEX idx_rec_scan ON records(studio_id, collection, created_at DESC)
      WHERE deleted_at IS NULL;
    -- expression indexes created at install from the manifest's storage.collections
    -- CREATE INDEX idx_rec_h3_seed ON records(json_extract(doc,'$.seed')) WHERE studio_id='h3-studio';

    -- media index. Bytes are in directories; these are pointers plus facts.
    CREATE TABLE assets (
      id TEXT PRIMARY KEY,
      sha256 TEXT NOT NULL UNIQUE,        -- dedup is a constraint, not an algorithm
      kind TEXT NOT NULL CHECK (kind IN ('image','video','audio','other')),
      mime TEXT NOT NULL, bytes INTEGER NOT NULL,
      width INTEGER, height INTEGER, duration_s REAL, fps REAL,
      blob_path TEXT NOT NULL,              -- relative to the assets root, never absolute
      library_path TEXT,                    -- the human-readable hardlink
      origin_studio TEXT, pinned INTEGER NOT NULL DEFAULT 0,
      state TEXT NOT NULL DEFAULT 'ready',  -- ready | missing | corrupt
      created_at INTEGER, last_used_at INTEGER);

    CREATE TABLE derived (
      asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
      variant TEXT NOT NULL,                -- thumb-320 | poster | waveform-800 | proxy-h264
      path TEXT NOT NULL, bytes INTEGER NOT NULL,
      PRIMARY KEY (asset_id, variant)) WITHOUT ROWID;

    -- the gallery
    CREATE TABLE items (
      id TEXT PRIMARY KEY, studio_id TEXT NOT NULL,
      session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
      kind TEXT NOT NULL,
      asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
      timeline_id TEXT REFERENCES timelines(id),   -- set when this item is an export
      title TEXT, params TEXT NOT NULL DEFAULT '{}',
      starred INTEGER NOT NULL DEFAULT 0,
      created_at INTEGER NOT NULL, deleted_at INTEGER);
    CREATE INDEX idx_items_feed   ON items(created_at DESC)            WHERE deleted_at IS NULL;
    CREATE INDEX idx_items_studio ON items(studio_id, created_at DESC) WHERE deleted_at IS NULL;
    CREATE INDEX idx_items_asset  ON items(asset_id);

    -- provenance edges: the reason a central store exists
    CREATE TABLE item_inputs (
      item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
      asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
      role TEXT NOT NULL,                   -- first_frame | last_frame | reference | audio_bed | clip
      PRIMARY KEY (item_id, asset_id, role)) WITHOUT ROWID;
    CREATE INDEX idx_inputs_asset ON item_inputs(asset_id);

    CREATE TABLE tags (
      item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
      tag TEXT NOT NULL, PRIMARY KEY (item_id, tag)) WITHOUT ROWID;
    CREATE VIRTUAL TABLE items_fts USING fts5(title, prompt, content='', tokenize='porter unicode61');

    -- sequences: framework-owned, edited by the launcher or by helm-timeline
    CREATE TABLE timelines (
      id TEXT PRIMARY KEY, name TEXT NOT NULL,
      target TEXT NOT NULL,                  -- {width,height,fps,sample_rate}
      tracks TEXT NOT NULL,                  -- clips reference asset ids; never copies
                                             -- amended M4 (review #5): [{kind, clips: [{asset_id, …}]}];
                                             -- a clip names its asset as asset_id, and M8 keeps that key
      revision INTEGER NOT NULL DEFAULT 1,  -- optimistic concurrency + undo
      created_at INTEGER, updated_at INTEGER);

    CREATE TABLE inbox (
      id TEXT PRIMARY KEY, to_studio TEXT NOT NULL, from_studio TEXT NOT NULL,
      item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
      role TEXT, created_at INTEGER, consumed_at INTEGER);
    CREATE INDEX idx_inbox_pending ON inbox(to_studio, created_at) WHERE consumed_at IS NULL;

**Schema v4 platform changes** (amended 2026-09-15, M4; `docs/decisions.md` "M4 API and SDK", Q6, Q9, Q17, Q18, and the M4 first review):

    -- per-launch studio tokens. Only the hash is stored: the token itself is a secret (§11).
    -- A token outlives a daemon restart with its group, and is revoked when the group stops.
    CREATE TABLE studio_tokens (
      token_sha256 TEXT PRIMARY KEY,
      studio_id TEXT NOT NULL,
      group_run_id TEXT,                    -- NULL for a token helm dev mints outside a group
      capabilities TEXT NOT NULL,           -- JSON array, copied from the manifest at mint time
      created_at INTEGER NOT NULL, revoked_at INTEGER);
    CREATE INDEX idx_tokens_group ON studio_tokens(group_run_id) WHERE revoked_at IS NULL;

    -- which studios have adopted or uploaded an asset. Dedup gives two studios one assets row
    -- and origin_studio names only the first; this is what lets the second read what it adopted.
    -- It is an access grant, not a reclaim reference.
    CREATE TABLE studio_assets (
      studio_id TEXT NOT NULL,
      asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
      created_at INTEGER NOT NULL,
      PRIMARY KEY (studio_id, asset_id)) WITHOUT ROWID;
    CREATE INDEX idx_studio_assets_asset ON studio_assets(asset_id);

    -- records gain an etag: 06 §6 returns and checks one. The UPDATE gives every existing row a
    -- fresh value, so no record is left with the empty default.
    ALTER TABLE records ADD COLUMN etag TEXT NOT NULL DEFAULT '';
    UPDATE records SET etag = lower(hex(randomblob(16)));

    -- first_referenced_at: set, once, when an item or item input first names the asset. Reclaim
    -- takes only assets that were referenced once and are not now (review #3): an asset adopted and
    -- never recorded — a crash between adopt and gallery add, a frame kept for later — is never
    -- reclaimed, because its blob is the only copy. Existing referenced assets are backfilled.
    ALTER TABLE assets ADD COLUMN first_referenced_at INTEGER;
    UPDATE assets SET first_referenced_at = COALESCE(created_at, 0)
     WHERE EXISTS (SELECT 1 FROM items i WHERE i.asset_id = assets.id)
        OR EXISTS (SELECT 1 FROM item_inputs x WHERE x.asset_id = assets.id);

    -- items_fts replaced: the v1 table indexed a prompt column items does not have, contentless,
    -- joined on a rowid VACUUM may renumber. prompt holds params.prompt; the row is written,
    -- replaced and deleted in the same transaction as its item.
    DROP TABLE items_fts;
    CREATE VIRTUAL TABLE items_fts USING fts5(item_id UNINDEXED, title, prompt, tokenize='porter unicode61');

A studio may read an asset it has a `studio_assets` row for, one its own items or item inputs reference, one an item in its inbox references, or any asset when it holds `gallery.read_all`. Blobs are written read-only (mode 0444). `:adopt` never copies. It takes a file from one of two places (amended by the M4 first review, #4, superseding Q10's stage only):

- **The caller's stage directory.** The stage entry is unlinked afterwards.
- **The studio's own `{data}` directory.** The studio's file stays where it is, as 08 describes for h3's takes. Because it is the blob's inode, it becomes read-only too.

**What read-only costs** (review #14). Every link to a blob shares its 0444 mode, so the library file and a `{data}` file cannot be edited in place, and Finder cannot write tags or other extended attributes to them. Renaming or deleting a link still works: that needs write permission on the directory, not the file. A studio that chmods its link and writes to it changes the blob, and a later Verify marks the asset `corrupt`.

**Managed and linked weights are the same row with a different `source`.** A managed artifact owns the bytes under `models/<dest>`; a linked one has that path as a symlink to a directory the user already had, with `external_path` as they typed it and `realpath` as it resolved. Everything downstream — bindings, `{models.x}` substitution, the disk view, the launch check — reads one field and behaves identically, which is the point of putting the symlink in the cache rather than special-casing paths at spawn time. Two rules make it safe: a linked directory is read-only to helmstudio, and no delete path ever follows a symlink.**

All studio state goes through the SDK

A studio does not keep its own store. Session settings, domain documents and preferences are written through `sessions`, `records` and `kv`, and the provider decides where the bytes land — `helm.db` under the daemon, `./.helm/helm.db` standalone. That is what makes one backup, one migration path and one crash-safety implementation possible instead of one per studio, and it is why `sessions` is a table here rather than a convention each author reinvents. Bytes still stay in directories; this is about rows.

**No stored reference count on an asset.** A counter drifts the first time a transaction is interrupted; the truth is derivable exactly from `items`, `item_inputs` and `timelines.tracks`. An exported sequence holds its footage through the `item_inputs` rows on its export; a sequence not yet exported holds it through the asset ids its tracks name, which the reclaim query in §8 reads (amended 2026-09-15, M4, Q12: an unexported sequence has no `item_inputs`, and R48 says a timeline's reference counts).**

## 6 · Media on disk

    ~/.helmstudio/
    ├── helm.db                          # every row above
    ├── assets/
    │   ├── blobs/7c/1f/7c1fa9…e2.mp4    # bytes, once, named by sha256, real extension kept
    │   └── derived/7c1fa9…e2/{thumb-320.jpg, poster.jpg, proxy.mp4}
    ├── library/h3-studio/2026-09/cafe-window-drift.mp4   # hardlink — same inode, no copy
    ├── models/MiniMax-H3/…              # weights
    ├── stage/<studio>/<group_run>/      # studio writes here; adopted by hardlink; cleared on stop
    └── studios/<id>/{src,logs}/

No path in this document is hardcoded

The tree is written as `~/.helmstudio/` for readability, but four roots resolve separately through an OS-appropriate directories helper — data, cache, logs and the user-visible library — plus models as a fifth. `settings` stores a root only when the user overrides it, so an absent key means "use the OS default" and a settings row copied between machines still resolves correctly. `assets.blob_path` being relative to the assets root is what makes all of this work without rewriting rows.

Two paths, one copy. The content-addressed path gives idempotent writes, dedup as a uniqueness constraint and detectable corruption; the library path gives a person a tree they can browse, drag from and back up. `assets.blob_path` is stored relative to the assets root so moving the library to an external drive is a settings change rather than a rewrite of every row. Backup is exactly two things: `helm.db` via `VACUUM INTO`, and `assets/blobs/` — `derived/` and `stage/` are regenerable by definition, which is most of why they are separate directories.

`helmstudio fsck` reconciles both directions, because a person will move something in Finder and that is not corruption. A row with no file sets `assets.state = 'missing'` and the gallery keeps the item, greyed, with its prompt and params intact — losing the record of what you made is worse than losing the file. A file with no row is reported as reclaimable and never deleted silently.

## 7 · State machines

### installations.install_state

| From               | Event                   | To                 | Side effect                                                        |
|--------------------|-------------------------|--------------------|--------------------------------------------------------------------|
| —                  | install requested       | `cloning`          | Job(install); root created                                         |
| `cloning`          | clone ok                | `cloned`           | `commit_sha`, submodule shas written                               |
| `cloning`          | git error               | `failed_clone`     | `last_failure`; root left for inspection                           |
| `cloned`           | build starts            | `building`         | `step_runs` created `pending`; for a studio with `python`, its environment is made or kept first, not as a step (amended M5) |
| `building`         | all steps succeed       | `built`            | `runtime_env` resolved                                             |
| `building`         | step exits non-zero     | `failed_build`     | step `failed`; `last_failure` carries the log id                   |
| `building`         | environment not made    | `failed_build`     | no step runs; `last_failure` code `env_failed`, no step index (amended M5) |
| `building`         | daemon killed           | `failed_build`     | startup sweep stops a verified surviving step, then marks it `interrupted` (amended M3) |
| `failed_build`     | retry                   | `building`         | resume at the first non-succeeded index                            |
| `built`            | weights declared        | `fetching_weights` | bindings created; a download Job per missing artifact              |
| `fetching_weights` | all required ready      | `ready`            | Launch becomes available                                           |
| `fetching_weights` | required download fails | `failed_weights`   | artifact stays resumable; nothing deleted                          |
| `fetching_weights` | HF 401/403              | `auth_required`    | token prompt; nothing failed; survives a restart (amended M3)      |
| `auth_required`    | install, retry or fetch with a working token | `fetching_weights` → `ready` | resumes the remaining downloads (amended M3) |
| `ready`            | remote ref moved        | `update_available` | non-destructive; still launchable                                  |
| any                | uninstall               | `removing`         | group stopped, bindings deleted, checkout removed (amended M3: no ref counts) |

### model_artifacts.state

| From          | Event                                | To              | Side effect                                                                                                                                           |
|---------------|--------------------------------------|-----------------|-------------------------------------------------------------------------------------------------------------------------------------------------------|
| —             | binding declared, no match           | `declared`      | row created, `source = 'managed'`; the binding is created with the file listing (amended M3: no `ref_count`)                                          |
| `declared`    | user points at an existing directory | `linked`        | `source = 'linked'`; `external_path` and `realpath` written; `models/<dest>` created as a symlink; declared files checked for presence and rough size |
| `linked`      | target unreadable or gone            | `missing`       | launch refuses with the expected path; Relink or Download offered; install state untouched                                                            |
| `missing`     | volume remounted, files present      | `linked`        | `verified_at` refreshed                                                                                                                               |
| `linked`      | unlink                               | row deleted     | **symlink removed only.** The user's directory is never deleted, and reclaim excludes linked rows entirely                                            |
| —             | binding declared, match exists       | unchanged       | a second binding row only — **the dedup path**; files that binding adds are fetched (amended M3)                                                       |
| `declared`    | listing fetched                      | `downloading`   | file rows and total bytes written                                                                                                                     |
| `declared`    | HF 401/403                           | `auth_required` | token prompt; install not failed                                                                                                                      |
| `downloading` | network error                        | `interrupted`   | `.part` kept; Job retries with backoff                                                                                                                |
| `interrupted` | retry or restart                     | `downloading`   | URL re-resolved; range-resume from the `.part` length                                                                                                 |
| `downloading` | all files complete                   | `ready`         | `.part` renamed; sizes and etags checked                                                                                                              |
| `ready`       | last binding deleted                 | unchanged       | not deleted; a managed artifact with no binding is eligible for reclaim (amended M3: no `orphaned` state, no counter)                                  |

### processes and the group

| From       | Event                                  | To         | Side effect                                                                   |
|------------|----------------------------------------|------------|-------------------------------------------------------------------------------|
| —          | launch requested                       | `queued`   | one `group_run_id` for every member                                           |
| `queued`   | heavy slot free, dependencies ready    | `starting` | port assigned, argv substituted, own pgid                                     |
| `starting` | health passes                          | `running`  | dependents leave `queued`; artifacts' `last_used_at` touched                  |
| `starting` | budget exhausted                       | `failed`   | `health_timeout`; group torn down in reverse                                  |
| `running`  | health check fails                     | `running`  | health degraded. **No restart** — a long generation must survive a slow probe |
| `running`  | `main` exits unexpectedly              | `failed`   | group stopped; last 200 lines surfaced; Restart offered, never automatic      |
| `running`  | `sidecar` exits, `restart: on-failure` | `starting` | backoff, max three in ten minutes, then the group fails                       |
| `running`  | another heavy group confirmed          | `stopping` | `preempted`; the new group waits for ports to clear                           |
| `running`  | restart, identity matches              | `running`  | re-adopted; survivors are not orphans                                         |
| `running`  | restart, process gone or mismatched    | `exited`   | swept as `crashed`; nothing is signalled                                      |

### assets.state

| From      | Event                                      | To          | Side effect                                                         |
|-----------|--------------------------------------------|-------------|---------------------------------------------------------------------|
| —         | adopt or upload, hash unseen               | `ready`     | blob hardlinked in, library link made, derived queued               |
| —         | adopt, hash exists                         | unchanged   | staged file unlinked. **Dedup as a uniqueness constraint**          |
| `ready`   | fsck finds no file                         | `missing`   | items stay, greyed, with params intact; Locate offered              |
| `missing` | file returns, hash matches                 | `ready`     | derived regenerated                                                 |
| `ready`   | explicit Verify mismatches                 | `corrupt`   | flagged; never auto-refetched, since a render cannot be regenerated |
| `ready`   | referenced once, no live item or input or timeline clip references it now, not pinned (amended M4 first review) | reclaimable | listed with bytes and the soft-deleted items it takes with it; deleted only on an explicit action |

## 8 · The queries that pay for a database

    -- reclaim never touches a linked directory: source='linked' is excluded everywhere
    -- amended M3: references are binding rows, never a stored count
    SELECT a.id, a.local_path FROM model_artifacts a
    WHERE a.source = 'managed'
      AND NOT EXISTS (SELECT 1 FROM studio_model_bindings b WHERE b.artifact_id = a.id);   -- unlinking a linked one removes the symlink only

    -- reclaim: every asset nothing points at. The entire garbage collector.
    -- amended M4 (Q12): a timeline's clips count, exported or not.
    -- amended M4 first review (#1, #3, #5): only a live item's inputs count; an asset never
    -- referenced is never reclaimed; a clip names its asset as asset_id.
    SELECT a.id, a.bytes FROM assets a
    WHERE a.pinned = 0
      AND a.first_referenced_at IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM items i WHERE i.asset_id = a.id AND i.deleted_at IS NULL)
      AND NOT EXISTS (SELECT 1 FROM item_inputs x JOIN items i ON i.id = x.item_id
                      WHERE x.asset_id = a.id AND i.deleted_at IS NULL)
      AND NOT EXISTS (SELECT 1 FROM timelines t, json_each(t.tracks) tr, json_each(tr.value, '$.clips') c
                      WHERE json_extract(c.value, '$.asset_id') = a.id);

    -- executing it, in one transaction, against exactly the previewed set. Soft-deleted items still
    -- hold items.asset_id and item_inputs, both ON DELETE RESTRICT, so every soft-deleted item that
    -- names a reclaimed asset — as its own asset or as an input — is hard-deleted first. Its inputs,
    -- tags and inbox rows cascade, and its items_fts row is deleted with it. Until reclaim, a soft
    -- delete loses nothing; reclaim is where it becomes permanent, and the preview lists those items.
    CREATE TEMP TABLE doomed AS /* the query above */ …;
    DELETE FROM items_fts WHERE item_id IN (SELECT i.id FROM items i WHERE i.deleted_at IS NOT NULL AND
      (i.asset_id IN (SELECT id FROM doomed) OR EXISTS (SELECT 1 FROM item_inputs x
        WHERE x.item_id = i.id AND x.asset_id IN (SELECT id FROM doomed))));
    DELETE FROM items WHERE deleted_at IS NOT NULL AND
      (asset_id IN (SELECT id FROM doomed) OR EXISTS (SELECT 1 FROM item_inputs x
        WHERE x.item_id = items.id AND x.asset_id IN (SELECT id FROM doomed)));
    DELETE FROM assets WHERE id IN (SELECT id FROM doomed);   -- derived and studio_assets cascade
    -- then, after commit: the blob, its derived directory, and its library file only when that
    -- path is still the blob's inode; with the library hardlink left, deleting the blob frees nothing

    -- the gallery feed at any scope, with its thumbnail, in one round trip
    SELECT i.id, i.title, i.kind, i.created_at, a.blob_path, d.path AS thumb
    FROM items i JOIN assets a ON a.id = i.asset_id
    LEFT JOIN derived d ON d.asset_id = a.id AND d.variant = 'thumb-320'
    WHERE i.deleted_at IS NULL AND (?1 IS NULL OR i.studio_id = ?1)
    ORDER BY i.created_at DESC LIMIT 50;

    -- downstream provenance: everything ever made from this reference image
    -- amended M4 (Q16): the earlier form referenced lineage inside a subquery, which SQLite refuses
    WITH RECURSIVE lineage(item_id) AS (
      SELECT item_id FROM item_inputs WHERE asset_id = ?1
      UNION
      SELECT x.item_id FROM lineage l
        JOIN items i ON i.id = l.item_id
        JOIN item_inputs x ON x.asset_id = i.asset_id)
    SELECT item_id FROM lineage;

    -- "that café video, whatever studio made it"
    -- amended M4 (Q17): items_fts carries item_id, not a rowid join; and FTS5 spells proximity
    -- NEAR(a b) — 'café NEAR window' is FTS4 syntax, which FTS5 reads as three words and never matches
    SELECT i.* FROM items_fts f JOIN items i ON i.id = f.item_id
    WHERE items_fts MATCH 'NEAR(café window)' AND i.deleted_at IS NULL ORDER BY rank LIMIT 25;

    -- a studio's own records, from the closed filter language — values always bound
    SELECT id, doc FROM records
    WHERE studio_id = ?1 AND collection = ?2 AND deleted_at IS NULL
      AND json_extract(doc,'$.seed') = ?3
    ORDER BY created_at DESC LIMIT ?4;

The recursive provenance query is the clearest case for the engine: in a hand-rolled index it is a graph traversal you write, test and maintain; here it was free the moment the edges became rows.

## 9 · Engine, pragmas, migrations

**Driver: `modernc.org/sqlite`** — pure Go, so `CGO_ENABLED=0` keeps cross-compilation, CI and the Electron build trivial — behind `database/sql`, so swapping to the cgo driver is a one-line change if profiling ever demands it. The stdlib-only rule is scoped explicitly to the studios, not the daemon; a component that owns a user's entire generation history has outgrown it.**

    dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
           "&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
    w, _ := sql.Open("sqlite", dsn); w.SetMaxOpenConns(1)   // every mutation serialises, no app mutex
    r, _ := sql.Open("sqlite", dsn); r.SetMaxOpenConns(max(4, runtime.NumCPU()))

| Pragma                | Why                                                                                                                                       |
|-----------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| `journal_mode(WAL)`   | Readers and the writer proceed concurrently; without it the gallery blocks every time a render is recorded.                               |
| `synchronous(NORMAL)` | Durable across process crashes; loses at most the last transaction on a power cut. The right trade for a local creative tool.             |
| `busy_timeout(5000)`  | A transient lock waits instead of erroring. With one writer it should never fire — when it does, something is wrong and you want to know. |
| `foreign_keys(ON)`    | Off by default in SQLite. Without it, every `ON DELETE RESTRICT` protecting an asset a gallery item references does nothing.              |

**Migrations** use `PRAGMA user_version` with an ordered list of functions, each in one transaction, so a failure leaves the version untouched and the daemon refuses to start rather than running half-migrated; a version higher than the binary's is also refused, so a downgrade fails loudly. The embedded provider inside a standalone studio carries **the same DDL and the same version sequence**, which is what makes later adoption an import rather than a merge — and the daemon refuses to import a schema newer than it knows, telling the user to update rather than silently discarding fields.**

`VACUUM INTO` takes a consistent single-file backup without stopping writers, and is also the honest answer to "how do I move my library to a new Mac": that file plus `assets/blobs/`.

## 10 · Worked examples

### Two studios, one weight

h3 studio installs first and declares `MiniMaxAI/MiniMax-H3`. No artifact matches `(hf_repo, revision)`, so one is created with a binding at placeholder `h3`. A later studio declaring the same repo and revision creates only a second binding; nothing it already has downloads. Uninstalling h3 studio cascades its binding, leaves one binding, and leaves every byte in place. (Amended M3: the count is the binding rows, never a stored number.)

### A three-process studio launching

One launch mints a `group_run_id` and writes two rows: `engine` (`sidecar`, `heavy`, port 8781) enters `starting`; `studio` (`main`, `depends_on: [engine]`) sits `queued` until the engine's health passes. The on-demand `batch` worker gets a row only when started, sharing the same group. Stop walks the group in reverse. The top bar shows one entry, because the group is the unit the user thinks in.

### A chained render, end to end

iris studio adopts a still: an `assets` row, a blob, a library hardlink, a thumbnail queued, and an `items` row with its prompt in `params`. The user sends it to h3 studio — an `inbox` row. h3 uses it as a first frame and records its take with one `item_inputs` row at role `first_frame`. Later both clips land in a sequence: a `timelines` row whose `tracks` reference the asset ids. Export creates a Job, adopts the output as a new asset, and writes an `items` row with `timeline_id` set and one `item_inputs` row per clip — so the lineage from a prompt in one studio to a finished sequence is a single recursive query.

## 11 · What we deliberately do not store

- **A studio's working bytes.** Its scratch files and in-progress media stay in its own directories until it adopts them. Its state — sessions, settings, domain documents — goes through the SDK into `sessions`, `kv` and `records` (amended 2026-09-15, M4, Q30: this line used to keep sessions and prompts in the studio's own directories, against R31 and §5).
- **Log bytes.** Append-only files with an index row.
- **Media and weight bytes, and per-chunk progress.** The filesystem is the source of truth; progress is `stat`.
- **Secrets.** The Hugging Face token is in the Keychain; the row records only that one exists and when.
- **Telemetry.** No usage counts, no generation counts, no timings beyond what a running job needs to show its own progress.
- **Anything the Electron shell knows.** Window bounds, update channel and the menu-bar preference live in the app's own user data; a second settings store would be a second source of truth.
