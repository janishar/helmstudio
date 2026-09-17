# Storage on SQLite

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

You are right, and this supersedes the JSON-document recommendation in the data model. Once the daemon stopped being a supervisor and became a data plane — assets, a gallery, provenance edges, tags, and per-studio domain records — the calculus flipped. Here is the decision, the schema, and the API shape that follows from it.

## 1 · The call

Yes

**One SQLite file, `~/.helmstudio/helm.db`, for all metadata** — daemon lifecycle state, the KV namespaces, the asset index, the gallery, provenance, tags, and each studio's own records. The daemon is the only process that opens it. Studios reach it through HTTP endpoints the SDK wraps.**

Not in the database

**Bytes.** Model weights, generated media and log output stay as files on disk, content-addressed where appropriate. A 2 GB video is never a BLOB column, and adoption stays a hardlink.**

That division is the whole design: SQLite is very good at the thing this product now has a lot of — small rows you need to filter, join, count and paginate — and actively bad at the thing it also has a lot of, which is gigabytes of immutable media. Putting the index in a database and the bytes on the filesystem is not a compromise; it is the correct shape.

## 2 · Why the recommendation changed

The earlier argument against a database was sound for the problem as it stood: the daemon's store was a few dozen kilobytes of lifecycle state, the only high-frequency write was download progress that could be recovered by `stat`-ing a `.part` file, and every query was a map lookup. Against that, a database is ceremony.

The platform changes all three inputs. A gallery is not a map lookup — it is "the last 50 video items from this studio, tagged `café`, newest first, joined to their assets for thumbnails, with a cursor". Provenance is a graph you traverse in both directions: what did this render come from, and what has ever been made from this reference image. Asset reclaim is a set difference across two tables. Prompt search is full text. Per-studio records need filtering on fields helmstudio does not know the names of. Every one of those is a line of SQL or thirty lines of hand-written Go index maintenance, and the Go version has to be kept correct against every future field.

The test I would apply to any storage decision here

Count the secondary indexes the features need. At one or two, hand-maintained maps are cheaper than a dependency. The platform needs at least eight — items by studio, by kind, by date, by session, by asset; inputs by asset; tags by tag; records by collection plus whatever each studio declares — and it needs them consistent across multi-table writes. That is the point at which you are either using a database or slowly writing a worse one.

## 3 · Which driver

| Driver                    | Gives                                                                                                               | Costs                                                                                            | Verdict                                                                                                   |
|---------------------------|---------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------|
| `modernc.org/sqlite`      | Pure Go. `CGO_ENABLED=0`, so cross-compilation, CI and the Electron build stay trivial. Same SQL, same file format. | ~8–10 MB of binary, a slower cold build, and a large machine-translated codebase to trust.       | **Use this.** The workload is a few hundred writes an hour; raw driver speed is irrelevant.               |
| `mattn/go-sqlite3`        | The C library itself: fastest, most battle-tested, smallest binary delta.                                           | cgo — a C toolchain in every build, no trivial cross-compile, more friction in CI and packaging. | The swap if profiling ever demands it. Everything sits behind `database/sql`, so it is a one-line change. |
| `zombiezen.com/go/sqlite` | A nicer low-level API over modernc, good for tight loops.                                                           | Not `database/sql`, so the driver stops being swappable.                                         | No. Swappability is worth more here than ergonomics.                                                      |

Note what this does to the stdlib-only principle: it bends, deliberately, and only for the daemon. It was a good rule when the daemon's job was to spawn processes; it is the wrong rule for a component that now owns a user's entire generation history. The studios stay as they are — h3 studio remains Go stdlib with no third-party dependency, because nothing about this reaches into them.

## 4 · Where the line sits

| Data                                                                           | Lives in                   | Why                                                                                                         |
|--------------------------------------------------------------------------------|----------------------------|-------------------------------------------------------------------------------------------------------------|
| Installations, step runs, processes, jobs, model artifacts, bindings, settings | `helm.db`                  | One transaction boundary, one backup, one migration path. Two persistence mechanisms is a tax paid forever. |
| KV namespaces, records, asset index, gallery, inputs, tags                     | `helm.db`                  | This is the relational part. It is why the database is here.                                                |
| **Images, audio, video**                                                       | **Directories on disk**    | Never a BLOB column. Layout below.                                                                          |
| Derived thumbs, posters, waveforms                                             | `assets/derived/<sha256>/` | Regenerable. Safe to delete, so they must sit outside the thing you back up.                                |
| Model weights                                                                  | `models/<dest>/`           | Unchanged. The database indexes them; it never holds them.                                                  |
| Build and run logs                                                             | `studios/<id>/logs/*.log`  | Append-only files. Subprocess stdout must never be a transaction.                                           |
| Download progress                                                              | Nowhere                    | Still derived from `.part` file lengths. A database does not make a pointless write worth making.           |

### Why media is never a BLOB, even though SQLite allows it

SQLite genuinely is faster than the filesystem for blobs under roughly 100 KB, which is why the question is worth answering rather than waving away. It stops being true immediately at this scale. A row must be materialised in memory to be read, so serving a 2 GB video means reading 2 GB into the process; `Range` requests — which is how a studio scrubs a timeline — become manual offset arithmetic instead of `http.ServeFile`; every write inflates the WAL and every `VACUUM INTO` backup copies the whole library; hardlink adoption becomes a full copy through the driver; and a person can no longer open the file in QuickLook, drag it into Final Cut, or find it in Finder at all. The database should stay small enough to snapshot in under a second, which means it holds the index and nothing that grows with the media.

### Where the root actually is

Everything lives in one tree, `~/.helmstudio/`, as drawn below, on every platform — the same shape `helm dev` keeps in a studio's `./.helm/`. No path is hardcoded: five roots resolve separately through a directories helper, so any one of them can be moved on its own.

| Root        | Holds                                      | Default                                               |
|-------------|--------------------------------------------|-------------------------------------------------------|
| **data**    | `helm.db`, studios, assets/blobs, stage    | `~/.helmstudio`                                       |
| **cache**   | derived thumbs, proxies, fetched manifests | `~/.helmstudio/cache`                                 |
| **logs**    | build and run logs                         | `~/.helmstudio/logs`                                  |
| **library** | the readable media tree                    | `~/.helmstudio/library`, and settable — see below     |
| **models**  | weights                                    | `~/.helmstudio/models`, and settable                  |

(Amended 2026-09-17, by the human's decision: every root defaults in `~/.helmstudio`. This replaces the operating systems' conventions — `~/Library/Application Support`, `~/Library/Caches` and `~/Library/Logs` on macOS, the XDG directories on Linux, `%LOCALAPPDATA%` on Windows — and `~/helmstudio` for the library, and gives up what they bought: the cache is no longer excluded from Time Machine or purgeable by the OS, and the library sits in a hidden directory, unless each is moved.)

The cache is still a root of its own because nothing in it is irreplaceable: moved to `~/Library/Caches/helmstudio`, it gets back the OS's backup exclusion and purging without touching the blobs beside it. Models are a separate root because a 200 GB collection often belongs on an external drive.

The library stays settable, on purpose

`library/` holds the human-readable hardlinks to everything a person has generated, and they have to be able to find it. It defaults to `~/.helmstudio/library`, and it is settable, so it can live wherever that person browses.

Resolution order is the same for every root — the specific override variable, then `HELMSTUDIO_HOME`, then the stored setting (library and models), then the default: `~/.helmstudio` for data, and a directory named for the root inside the resolved data root for every other. `HELMSTUDIO_HOME=<dir>` is that same tree at `<dir>`, which is what lets a test, a CI run and `helm dev` each work in an isolated tree without touching the user's. In Go this needs no dependency: `os.UserHomeDir` covers it. Data kept at the earlier defaults is neither moved nor adopted: no released helmstudio used them.

### The layout

    ~/.helmstudio/
    ├── helm.db                          # all state. ~ tens of MB at 100k items
    ├── assets/
    │   ├── blobs/7c/1f/7c1fa9…e2.mp4    # the bytes, once, named by content hash
    │   └── derived/7c1fa9…e2/
    │       ├── thumb-320.jpg            # regenerable, excluded from backup
    │       └── poster.jpg
    ├── library/                         # what a human opens in Finder
    │   └── h3-studio/2026-09/
    │       └── cafe-window-drift.mp4    # hardlink → the blob above. Same inode, no copy.
    ├── models/MiniMax-H3/…              # weights, untouched
    ├── stage/h3-studio/<group_run>/     # studio writes here, daemon adopts, then clears
    └── studios/h3-studio/{src,logs}/

**Two paths, one copy.** The blob path is named by `sha256`, which is what makes writes idempotent, dedup a `UNIQUE` constraint rather than an algorithm, and corruption detectable. The library path is what the user actually browses, organised by studio and month with the title they gave it. They are the same inode, created with `os.Link` — so the readable tree costs nothing, and deleting either one cannot lose data while the other exists. Keep the real extension on the blob too, so `open` and QuickLook behave when someone digs down there.**

| Rule                                                                                 | Reason                                                                                             |
|--------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------|
| Two-level hex fan-out (`7c/1f/`)                                                     | No directory ever holds 100,000 entries; Finder and `readdir` both stay usable.                    |
| Store the path *relative* to the assets root, plus the hash — never an absolute path | Moving the library to an external drive stays a settings change instead of a rewrite of every row. |
| Write to `<name>.tmp`, fsync, then rename into place                                 | A crash leaves a temp file swept at boot, never a half-written asset that hashes to a lie.         |
| Hardlink, never copy, when adopting from `stage/`                                    | An 18 MB take and a 2 GB render both cost two inode operations.                                    |
| User imports are `pinned = 1`                                                        | Deleting the reference photo someone dragged in is far worse than keeping a stale render.          |

### The filesystem will drift, so plan for it

Two files-and-rows systems disagree eventually, usually because a person moved something in Finder — and a creative tool must not treat that as corruption. `helmstudio fsck` reconciles both directions and is worth building at the same time as the store, not after the first bug report.

- **Row with no file.** Mark the asset `missing` rather than deleting the row: the gallery shows the item greyed with its metadata and prompt intact, offers Locate, and regenerates the thumbnail if the file comes back. Losing the record of what you made is worse than losing the file.
- **File with no row.** Left alone by default and reported as reclaimable bytes; never deleted silently, because a stray file under a user's library is far more likely to be theirs than garbage.
- **Hash mismatch on verify.** The file changed underneath us. Flag it, keep both facts, and let the user decide — an automatic re-download is fine for weights and wrong for a render that cannot be regenerated.
- **Backup is two things:** `helm.db` (via `VACUUM INTO`) and `assets/blobs/`. `derived/` and `stage/` are excluded by definition, which is most of the reason they are separate directories.

## 5 · Schema

The platform half, in full. Timestamps are Unix milliseconds; ids are ULIDs, so primary-key order is insertion order and most listings need no sort at all.

    -- ---------- studio state ----------
    CREATE TABLE kv (
      studio_id  TEXT NOT NULL,
      ns         TEXT NOT NULL,
      key        TEXT NOT NULL,
      doc        TEXT NOT NULL,                -- JSON
      etag       TEXT NOT NULL,
      updated_at INTEGER NOT NULL,
      PRIMARY KEY (studio_id, ns, key)
    ) WITHOUT ROWID;

    -- a document store for data helmstudio should not model: h3 takes, AuK reference clips, iris presets
    CREATE TABLE records (
      id         TEXT PRIMARY KEY,
      studio_id  TEXT NOT NULL,
      collection TEXT NOT NULL,
      doc        TEXT NOT NULL,                -- JSON, shape owned by the studio
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL,
      deleted_at INTEGER
    );
    CREATE INDEX idx_records_scan ON records(studio_id, collection, created_at DESC)
      WHERE deleted_at IS NULL;

    -- ---------- assets ----------
    CREATE TABLE assets (
      id           TEXT PRIMARY KEY,
      sha256       TEXT NOT NULL UNIQUE,     -- dedup is a unique constraint, not an algorithm
      kind         TEXT NOT NULL CHECK (kind IN ('image','video','audio','other')),
      mime         TEXT NOT NULL,
      bytes        INTEGER NOT NULL,
      width        INTEGER, height INTEGER, duration_s REAL,
      origin_studio TEXT,
      pinned       INTEGER NOT NULL DEFAULT 0,   -- user imports are pinned; renders are not
      created_at   INTEGER NOT NULL,
      last_used_at INTEGER
    );

    CREATE TABLE derived (
      asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
      variant  TEXT NOT NULL,                 -- thumb-320 | poster | waveform-800
      path     TEXT NOT NULL,
      bytes    INTEGER NOT NULL,
      PRIMARY KEY (asset_id, variant)
    ) WITHOUT ROWID;

    -- ---------- gallery ----------
    CREATE TABLE items (
      id         TEXT PRIMARY KEY,
      studio_id  TEXT NOT NULL,
      session    TEXT,
      kind       TEXT NOT NULL,
      asset_id   TEXT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
      title      TEXT,
      params     TEXT NOT NULL DEFAULT '{}',    -- seed, steps, model, lora, prompt…
      starred    INTEGER NOT NULL DEFAULT 0,
      created_at INTEGER NOT NULL,
      deleted_at INTEGER
    );
    CREATE INDEX idx_items_feed   ON items(created_at DESC)             WHERE deleted_at IS NULL;
    CREATE INDEX idx_items_studio ON items(studio_id, created_at DESC)  WHERE deleted_at IS NULL;
    CREATE INDEX idx_items_kind   ON items(kind, created_at DESC)       WHERE deleted_at IS NULL;
    CREATE INDEX idx_items_asset  ON items(asset_id);

    -- provenance: the edge that only a central store can record
    CREATE TABLE item_inputs (
      item_id  TEXT NOT NULL REFERENCES items(id)  ON DELETE CASCADE,
      asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
      role     TEXT NOT NULL,                 -- first_frame | last_frame | reference | audio_bed
      PRIMARY KEY (item_id, asset_id, role)
    ) WITHOUT ROWID;
    CREATE INDEX idx_inputs_asset ON item_inputs(asset_id);

    CREATE TABLE tags (
      item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
      tag     TEXT NOT NULL,
      PRIMARY KEY (item_id, tag)
    ) WITHOUT ROWID;
    CREATE INDEX idx_tags_tag ON tags(tag, item_id);

    -- prompt search, which is the feature people actually ask a gallery for
    CREATE VIRTUAL TABLE items_fts USING fts5(title, prompt, content='', tokenize='porter unicode61');

    -- cross-studio handoff
    CREATE TABLE inbox (
      id          TEXT PRIMARY KEY,
      to_studio   TEXT NOT NULL,
      from_studio TEXT NOT NULL,
      item_id     TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
      role        TEXT,
      created_at  INTEGER NOT NULL,
      consumed_at INTEGER
    );
    CREATE INDEX idx_inbox_pending ON inbox(to_studio, created_at) WHERE consumed_at IS NULL;

Two details worth defending. **Dedup is a `UNIQUE` constraint on `sha256`** — the insert either succeeds or tells you the asset already exists, and there is no separate dedup code path to get wrong. And **there is no stored reference count**; a counter drifts the first time a transaction is interrupted, whereas the truth is derivable exactly, as the reclaim query in §7 shows.

## 6 · The records API

This is the part SQLite makes cheap enough to offer. Typed endpoints stay for the shared concepts — assets, gallery, sessions — because those are what enable cross-studio features. Alongside them, every studio gets a generic document collection for the domain data helmstudio has no business modelling: h3's takes with their anchors, AuK's reference library, iris's presets.

| Endpoint                                                                                         | Does                                                                     |
|--------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------|
| POST /records/{collection}                                          | Insert a JSON document, returns id and etag.                             |
| GET /records/{collection}/{id}                                       | Fetch one.                                                               |
| PUT · PATCH /records/{collection}/{id} | Replace, or JSON merge patch. `If-Match` on the etag; `409` on conflict. |
| DELETE /records/{collection}/{id}                                   | Soft delete, so an undo is possible and a sync never sees a gap.         |
| GET /records/{collection}?where=…&order=…&limit=…&cursor=…           | Filtered, ordered, cursor-paginated query over declared fields.          |

The filter language is small and closed, and it never accepts SQL. A clause is `field:op:value`, operators limited to `eq ne lt lte gt gte in contains exists`, at most eight clauses, every value bound as a parameter:

    # "my last 20 takes at seed 42 that finished"
    GET /records/takes?where=seed:eq:42&where=state:eq:done&order=created_at:desc&limit=20

    # compiles to, with every value bound:
    SELECT id, doc FROM records
    WHERE studio_id = ?1 AND collection = ?2 AND deleted_at IS NULL
      AND json_extract(doc, '$.seed')  = ?3
      AND json_extract(doc, '$.state') = ?4
    ORDER BY created_at DESC LIMIT ?5;

**Indexes are declared in the manifest**, which keeps the principle intact — a studio that needs a fast lookup adds a line of YAML, not a Go change:**

    storage:
      collections:
        - { name: takes,      index: [seed, model, session, state], fts: [prompt] }
        - { name: references, index: [kind] }
      quota: { records: 100000, kv_bytes: 8388608 }

At install the daemon creates the matching expression indexes, scoped so one studio's index never scans another's rows:

    CREATE INDEX idx_rec_h3_seed ON records(json_extract(doc,'$.seed'))
      WHERE studio_id = 'h3-studio' AND deleted_at IS NULL;

Quotas matter more than they look: a studio in a retry loop can write a million rows, and a local app that silently eats a disk is the failure users never forgive. The cap is per studio, enforced on write, and surfaced as a `429` with the number in it.

## 7 · The queries that pay for the decision

Each of these is a feature the earlier design would have needed hand-written index maintenance to support.

    -- reclaim: every asset nothing points at. The entire garbage collector.
    SELECT a.id, a.bytes FROM assets a
    WHERE a.pinned = 0
      AND NOT EXISTS (SELECT 1 FROM items i       WHERE i.asset_id = a.id AND i.deleted_at IS NULL)
      AND NOT EXISTS (SELECT 1 FROM item_inputs x WHERE x.asset_id = a.id);

    -- the gallery feed, any scope, with its thumbnail, in one round trip
    SELECT i.id, i.title, i.kind, i.created_at, a.sha256, d.path AS thumb
    FROM items i
    JOIN assets a  ON a.id = i.asset_id
    LEFT JOIN derived d ON d.asset_id = a.id AND d.variant = 'thumb-320'
    WHERE i.deleted_at IS NULL AND (?1 IS NULL OR i.studio_id = ?1)
    ORDER BY i.created_at DESC LIMIT 50;

    -- provenance, downstream: everything ever made from this reference image
    WITH RECURSIVE lineage(item_id) AS (
      SELECT item_id FROM item_inputs WHERE asset_id = ?1
      UNION
      SELECT x.item_id FROM item_inputs x JOIN items i ON i.id = lineage.item_id
       JOIN lineage ON x.asset_id = i.asset_id
    )
    SELECT * FROM lineage;

    -- "that café video, whatever studio made it"
    SELECT i.* FROM items_fts f JOIN items i ON i.rowid = f.rowid
    WHERE items_fts MATCH 'café NEAR window' ORDER BY rank LIMIT 25;

    -- disk page: who holds the bytes
    SELECT i.studio_id, count(*) AS n, sum(a.bytes) AS total
    FROM items i JOIN assets a ON a.id = i.asset_id
    WHERE i.deleted_at IS NULL GROUP BY 1 ORDER BY total DESC;

The recursive provenance query is the clearest case. In a hand-rolled index it is a graph traversal you write, test and maintain; here it is eight lines that were always going to be free once the edges were rows.

## 8 · The Go recipe

The whole concurrency story, given that the daemon is the sole owner: one connection for writes, a pool for reads, WAL so readers never block the writer.

    dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
           "&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"

    // one writer: serialises every mutation without a single mutex in your code
    w, _ := sql.Open("sqlite", dsn); w.SetMaxOpenConns(1)

    // many readers: the UI polls, the gallery paginates, the supervisor checks
    r, _ := sql.Open("sqlite", dsn); r.SetMaxOpenConns(max(4, runtime.NumCPU()))

| Pragma                | Why                                                                                                                                                  |
|-----------------------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| `journal_mode(WAL)`   | Readers and the writer proceed concurrently. Without it the gallery blocks every time a render is recorded.                                          |
| `synchronous(NORMAL)` | With WAL this is durable across process crashes and loses at most the last transaction on a power cut — the right trade for a local creative tool.   |
| `busy_timeout(5000)`  | Turns a transient lock into a wait instead of an error. With one writer it should never fire; when it does, something is wrong and you want to know. |
| `foreign_keys(ON)`    | Off by default in SQLite. The `ON DELETE RESTRICT` that stops you deleting an asset a gallery item still points at does nothing without this.        |

**Performance, so nobody worries about the wrong thing.** A loopback HTTP round trip is a few tenths of a millisecond; a WAL insert is tens of microseconds; a generation takes minutes. The metadata path is four orders of magnitude away from mattering, and media bytes never traverse the API at all — they are hardlinked in. The only rule worth enforcing is to batch: recording an item, its inputs and its tags is one transaction, not three round trips.**

## 9 · Migrations and backup

`PRAGMA user_version` holds the schema number. On startup the daemon compares it to the compiled-in version and runs the ordered migrations, each inside one transaction so a failure leaves the version untouched and the daemon refuses to start rather than running half-migrated. A version *higher* than the binary's is also a refusal, so a downgrade fails loudly instead of corrupting.

    // consistent snapshot without stopping writers — one line, no external tool
    _, err := db.Exec(`VACUUM INTO ?`, backupPath)  // helm.db.bak.7

Take that snapshot before the first migration of a session, and on a schedule if you want cheap insurance: the result is a single self-contained file the user can copy, and recovery is `mv`. It is also the answer to "how do I move my library to a new Mac" — one file plus the `assets/` directory.

## 10 · Why studios do not open the database themselves

It is the obvious shortcut and it is a trap. Four reasons, in descending order of how much they will hurt.

- **Isolation disappears.** The capability model is enforced by the daemon deciding what a token may touch. A studio with the file handle can read every other studio's records, and the manifest's permission list becomes decoration.
- **The schema becomes a public API.** The moment four studios in three languages hold queries against `items`, you cannot rename a column without breaking installed software you do not control. Behind HTTP, the schema stays an implementation detail you can change on a Tuesday.
- **Multi-process WAL across languages and versions.** Python's bundled SQLite, Go's modernc build and the C library are different versions with different defaults, all writing one file over whatever filesystem the user's models directory happens to be on. It mostly works, which is the worst property a data-integrity mechanism can have.
- **No validation, no quota, no events.** Direct writers skip the checks, skip the limits, and skip the SSE notification that makes the launcher's gallery update live when a studio records a render.

The standalone fallback is the one exception, and it is safe precisely because it is not shared: a studio running without the daemon opens its own `./.helm/helm.db`, alone, with the same schema — so adoption later is an import, not a merge.

## 11 · What it costs, honestly

| Cost                               | Size                              | Mitigation                                                                                                                 |
|------------------------------------|-----------------------------------|----------------------------------------------------------------------------------------------------------------------------|
| Binary grows                       | ~8–10 MB with modernc             | Irrelevant next to a 16 GB checkpoint, and the Electron bundle dwarfs it anyway.                                           |
| Cold build slows                   | Tens of seconds the first time    | Cached thereafter. CI stays `CGO_ENABLED=0`.                                                                               |
| A dependency to trust              | One, large, machine-translated    | It is a translation of the most tested database in existence, pinned by version, behind `database/sql` so it is swappable. |
| A principle bends                  | "stdlib-only Go"                  | Scope it explicitly to the daemon. The studios keep the rule; say so in the PRD rather than letting it erode quietly.      |
| Schema migrations become real work | Every release with a shape change | Far less than hand-maintaining eight indexes and a graph traversal, which was the alternative.                             |

What this supersedes

The data model's storage section recommended a single JSON document with atomic rename, and the platform document described three stores. Both now collapse to one rule everywhere: **SQLite for metadata, directories for bytes.** That includes the standalone case — a studio running with no daemon uses the embedded provider against its own `./.helm/helm.db` with the identical schema and migration sequence, which is precisely what makes later adoption an import rather than a translation. There is no second storage shape anywhere in the system.
