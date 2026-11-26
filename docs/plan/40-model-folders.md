# helmstudio plan — Model folders: several folders per host, in priority order

[← Plan index](../README.md)

Status: **approved, building (owner, 2026-11-27: "mac default, ask which copy, approved build it")**, release list
[0.1.0-alpha.6](../release/0.1.0-alpha.6.md) item 7. Earlier: "links now, B in alpha.6", "add option to modify this list
in models page", "if a model is duplicate in t7 in the model list feature set priority option as well to pick first
from priority if not then look other place". Section §46.

## 46. Model folders

### 46.1 Board

[ModelFolders](../mock/ModelFolders.dc.html) (styled: the boards server on 8455), frames:

1. The Models page: a **Folders · <host>** strip (each folder's place, name, path, free space; the Downloads tag),
   a folder tag on each host chip (`Mac`, `T9`), `also on T9` when a second copy exists, **Copies on mac** in the side
   panel (used, spare).
2. **Edit…**: the list in a card: drag to reorder, the Downloads radio, Remove (✕), Add a path or **Browse…** (Mac app).
3. A drive unplugged: `T9 not connected` instead of missing (3a); falling back to the next folder that has it (3b).
4. Download to host…: a **Folder** pick (downloads folder first and picked; an unplugged one greyed).
5. Right-click menus: a folder row (Move up/down, Use for downloads, Open in Finder, Remove…), Remove…, the model
   card gains Show in Finder and Copies on mac.
6. Delete from host… for a model in two folders: which copy (each with space back and free after, the used one
   marked), Both, the other copy unplugged.

Owner's answers: downloads go to the Mac folder by default (the first folder, today's `HELMSTUDIO_MODELS`); Delete asks
which copy.

### 46.2 Corrections from the scout

- The runner has one root: `config.MODELS` (`HELMSTUDIO_MODELS`, default `~/GenAI/models`), read through
  `core.pack.models_root()` by about 25 call sites: `core/pack.py` (`ModelRef.folder`, `owns_copy`,
  `component_files`), `core/models.py` (`addon_folder`, `addon_dest`, `folder`, `addon_entry`'s `dir`, `delete_in`),
  `core/download.py` (`checked`, `plan`, `disk_free`), `core/registry.py:359` (pre-fused files),
  `messaging/models.py` (LoRA delete), `packs/ltx/mlx.py:1354`, and `main.py` (`lora.Library`, `looks.entries`,
  `models.disk` for the hosts key). Every path is `root / <dir> / ...`, so the change is one resolver, not 25 edits.
- `delete_in` refuses a base that is a link or outside the root. The owner's T9 models are today **links** inside
  `~/GenAI/models` (row 7's "links now"): once T9 is a folder of its own, those links are removed by hand (a step in
  the owner's note at release), never by a delete job.
- The hosts key has one `models_disk` (root, free, total, writable); studio's `ModelHost` copies it
  (`models_root`, `free_bytes`, `total_bytes`) and the 409 `models root on <host> is read-only` reads its `writable`.
- Host settings (plan 35 pattern): studio's `host_settings` table is the truth, written to the KV `host_settings`
  key `<host>`; every env's runner of that host watches it and applies between runs (`messaging/settings.py`). The
  KV value carries a new field only when set, so an older runner (whose schema refuses unknown fields) keeps working.
- Messages never carry a path (`cmd.model.job`: "never a token, a URL, a path or bytes"), so a job names a folder by
  an id, not its path.
- The Mac app already has a native folder dialog (`desktop/src/main.js` `choose-folder`, for the setup's models
  folder), exposed only to the setup window; the studio window's bridge (`studio-preload.js`, `helmDesktop`) has
  only the spelling calls.
- Contract today: API **1.76.0**; plan 38's contract phase (1.77.0) has **not** started (`contracts/` has no edits).
  This plan takes the next minor after it: **1.78.0** (if plan 38's lands later, whichever lands second renumbers).
- Migrations: the last is `00054`; plan 38 may take `00055`; this plan takes the next free number when it lands.

### 46.3 Design

#### Contract (API 1.78.0; contracts first, `make contracts-check`, then `make gen` per service)

- A folder's **id**: the first 12 hex of the SHA-256 of its path as stored (studio and runner compute it alike;
  nothing to store). Its **name**: `/Volumes/<X>/…` → `X` (`ALI T9` shown as the volume name), else `Mac` on macOS,
  else the host name; the runner reports it.
- `openapi.yaml`:
  - `PUT /v1/studio/admin/hosts/{host}/folders` (admin, like `/pins`): body `ModelFoldersPut {folders: [{path}]
    (1–8, order = priority), downloads: <index>}`; 200 `HostSettings`. Paths: absolute or `~/…`, ≤ 400 chars, no
    `..`, no duplicates. Studio asks the host's supervising runner `req.runner.folders.check` first: 422 `<path> is
    not a folder on <host>`, `<path> is not writable on <host>` (downloads folder only; a read-only folder may be
    listed), `downloads must be a connected folder`; 503 `<host> is offline`.
  - `HostSettings` gains `folders: [{id, path}] | null` (null = the runner's `HELMSTUDIO_MODELS`, today's behaviour)
    and `downloads: id | null`.
  - `ModelHost` gains `folders: [{id, name, root, connected, free_bytes, total_bytes, writable, downloads}]` in
    priority order (the old `models_root`/`free_bytes`/`total_bytes` stay: the downloads folder's).
  - `ModelVariantState` gains `unplugged` (its only copies are in folders not connected; not runnable).
  - `ModelVariantHost` and the add-on's host gain `folder: id | null` (the copy used) and `copies: [{folder, state,
    bytes, freed_bytes}]` (every folder with files of it; `freed_bytes` = what deleting that copy gives back, shared
    parts another variant there loads excluded).
  - `ModelDownload` gains `folder: id` (absent = the downloads folder, else the next connected one).
  - `DELETE /v1/studio/admin/models/{model}` gains `folder` (repeatable query). Absent with one copy = that copy;
    absent with two or more: 409 `<title> is in <n> folders on <host>; pick one`. 409 `<folder name> is not
    connected on <host>`. The loaded / in-use checks apply only when the used copy is in the delete.
- `messages/cmd.model.job` `data.folders: [id]` (delete) and `data.folder: id` (download); absent = today's single
  root. New `req.runner.folders.check` (`{paths}` → `[{path, connected, writable, free_bytes, total_bytes}]`).
- `registry.schema.json`: the hosts key gains `models_disks` (as `ModelHost.folders`); `models_disk` stays (the
  downloads folder). Models key variants and add-ons gain `folder` and `copies`; presence gains `unplugged`. KV
  `host_settings` value gains optional `folders: [path]` and `downloads: index`.
- contracts/README.md: "Model folders" (the resolver rule, ids, unplugged).

#### Data (studio)

- Migration: `host_settings.folders jsonb null`, `host_settings.downloads int null`. The KV value carries them only
  when set (plan 35's rule).

#### Runner

- `config.ROOTS`: the ordered list; default `[HELMSTUDIO_MODELS]` (one item, so nothing changes without a setting).
  `Settings.apply` replaces it from the KV value and starts a rescan.
- One resolver in `core/pack.py`: `models_root()` stays (= the downloads root, for writes), and `place(rel) -> Path`
  returns `<root>/<rel>` for the **first connected root that has a complete copy** of that folder (weights present,
  no download marker), else the downloads root. A root is connected when its path is an existing directory; a
  missing one is skipped, **never created** (`/Volumes/ALI T9` would otherwise appear on the system disk). Resolution
  is per model folder (`dir`), so a variant in T9 still loads parts it shares from the owner's folder on Mac.
- Every read site above goes through `place`; downloads go to the chosen root (`folder` id, else downloads, else
  the next connected) and skip shared components already present in any connected root.
- Scan: the models key reports each variant's `copies` per connected root; a cache file
  (`CACHE/folders-seen.json`, folder id → dirs seen) remembers what an unplugged root held, so its models show
  `unplugged`, not missing. The 60 s rescan notices a plug or unplug.
- Delete: `delete_in(base)` per named folder, with today's guards (`inside` that root, never a link base).
- Hosts key: `models_disks` from `models.disk()` per root (unplugged: `connected: false`, no sizes).
- LoRA library and looks scan every connected root's `loras/` (`lora.Library` takes the list).
- `req.runner.folders.check` on the supervising runner (as `req.runner.plugins.key`).

#### Studio

- `PUT …/folders`, the check request, the KV write; `GET /v1/studio/models` folds `folders`, `folder`, `copies`
  and `unplugged`; download and delete take `folder(s)`, checked against the host's list; the read-only 409 reads
  the chosen folder's `writable`.

#### Web (per board frame)

- 1: `components/models/models.tsx`: the Folders strip (shown when the host has more than one folder; All hosts:
  one strip per such host), the folder tag and `also on <name>` on `HostChip`, Copies in `VariantPanel`.
- 2: a new `components/models/folders.tsx`: Edit… card (drag and keyboard reorder, Downloads radio, Remove with its
  confirm, Add a path, Browse… when `window.helmDesktop?.chooseFolder`).
- 3: `unplugged` state on chips (`<name> not connected`), fallback note.
- 4: `DownloadDialog` gains the Folder pick and `Into`.
- 5: the shared menu (`components/context-menu.tsx`): the folder row menu; the model card menu gains Show in Finder
  (Mac app, host on this Mac) and Copies on <host>.
- 6: `DeleteDialog` gains the Copy pick (used marked and picked first, Both, unplugged greyed) and the "loads from
  <name> next" line.
- Design system: the folder strip chip, the folder row, the copy/folder pick and the Downloads tag go into the
  helmstudio Design System artifact and `docs/design-system/` together.

#### Desktop (Mac app)

- `studio-preload.js` `helmDesktop.chooseFolder(current)` and `reveal(path)`; `main.js` handlers gated to the studio
  window (the `fromStudio` check), reusing `choose-folder`'s `showOpenDialog` (`openDirectory`, `createDirectory`)
  and `shell.showItemInFolder`. Reveal only a path inside a folder of the local host's list.

### 46.4 Phases

| Phase | Work | Files |
|---|---|---|
| P1 | Contract 1.78.0 | `contracts/openapi.yaml`, `contracts/messages/cmd.model.job.schema.json`, new `contracts/messages/req.runner.folders.check.schema.json`, `contracts/registry.schema.json`, `contracts/README.md`, fixtures under `contracts/fixtures/` |
| P2 | Runner: roots, resolver, scan, unplugged, download/delete by folder, check request | `runner_service/config.py`, `core/pack.py`, `core/models.py`, `core/download.py`, `core/registry.py`, `core/lora.py`, `core/looks.py`, `messaging/models.py`, `messaging/settings.py`, `main.py`, `packs/ltx/mlx.py`, `gen/`, new `tests/test_model_folders.py` |
| P3 | Studio: migration, PUT folders, check, models fold, download/delete folder | `studio_service/migrations/000NN_host_folders.sql`, `api/studio/hosts_admin.go`, `api/studio/models.go`, `api/studio/dto/models.go`, `registry/registry.go`, `gen/`, tests beside them |
| P4 | Web frames 1–6, menus, design system | `web_service/components/models/models.tsx`, new `components/models/folders.tsx`, `components/context-menu.tsx`, `lib/`, `gen/api.ts`, tests; `docs/design-system/` and the artifact |
| P5 | Mac app Browse and reveal | `desktop/src/main.js`, `desktop/src/ui/studio-preload.js` |
| P6 | Contract tests, full suite, release row | `contract_tests/tests/test_models.py`, `test_hosts_admin.py`, `test_hosts.py`; `docs/release/0.1.0-alpha.6.md` |

P2 and P3 run in parallel after P1; P4 needs P3; P5 is independent after P1.

### 46.5 Tests

- Runner unit (`tests/test_model_folders.py`), two temp roots A and B and a third path that does not exist
  (unplugged): order A,B with the model in both → loads from A; A removed → from B; the model only in the missing
  root → `unplugged` (from `folders-seen`), never `missing`, and the missing root is not created; a variant in B
  with its shared parts in A loads both; download with `folder` B writes only under B; delete with `folders: [A]`
  leaves B's copy and the model then resolves to B; the default (no setting) is the single `HELMSTUDIO_MODELS` root.
- Studio: the PUT's validation (paths, duplicates, downloads index, check failures, offline 503), the KV value only
  when set, the delete 409 with two copies and no folder.
- Web: the strip, the Edit card, the Delete copy pick, the Download folder pick.
- Contract tests touched: `test_hosts_admin.py` (PUT folders on the fake host, check 422), `test_models.py`
  (download to a folder, delete by folder, the two-copies 409), `test_hosts.py` (`models_disks`). The fake runner
  answers `req.runner.folders.check` and reports two folders.

### 46.6 Risks

- Creating a root by accident on the system disk when a drive is unplugged: the resolver and download never
  `mkdir` a root, only folders under a connected one (a unit test).
- The owner's live links into T9: tests never touch `~/GenAI/models` or `/Volumes/ALI T9`; after the build the
  owner adds T9 as a folder, then removes the links by hand.
- A runner in a container sees only mounted paths: a folder not mounted shows not connected (NVIDIA hosts run
  native runners, so this is the Linux compose case only); noted in the guide.
- Older runners (< 1.78) ignore `folders` only because studio leaves the field out unless set; a host with a mix
  of old and new runners shows the old ones' single root.
- Unplugging mid-run: a run already loading from T9 fails as a read error; the next run falls back. Not handled
  beyond that.
- Plan 38 and Magic edit `contracts/openapi.yaml` too: rebase the contract commit on theirs and renumber if needed.
