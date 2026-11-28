"""Model weights on this host's disk (contracts/README.md "Models and model jobs"): what the models key says per
variant (its folder's manifest, components, presence), the hash index, the host's disk and machine, and delete.

- **Hashes**: a file downloaded through huggingface_hub has its Hub LFS SHA-256 recorded in its sidecar
  `<dir>/.cache/huggingface/download/<path>.metadata` (commit, etag, time): trusted while the file is not newer than
  the sidecar. Any other file is hashed once, in the background (`Hashes.start`, never at start), and kept in
  `<cache>/model-hashes-<env>.json` by path, size and mtime, as the LoRA index does. A recorded file changed since
  is re-hashed; a different hash makes the variant `mismatch`.
- **Presence**: present = every component's globs match in the folder it loads from and no download is unfinished
  there (MARKER: written when a download starts, removed when it succeeds) and nothing mismatches; partial = some of
  its own files are there; missing = none; mismatch wins over both.
- **Delete** removes a variant's own files inside `<root>/<dir>` (never through a symlink), keeping a shared
  component another variant still loads.
- **One folder, two variants** (LTX: the dev transformer beside the distilled files, read in place): a variant with
  `shares` whose `dir` is the first variant's owns only its components' files there (`own_globs`); the first owns
  the rest.
- **Shares** load from the first variant, or from the variant `shares_from` names (API 1.33: turbo-fp8 from
  base-fp8): core.pack.share_owner.
- **Optional add-ons** (API 1.36, contracts/README.md "Optional add-ons"): a component with `addon` counts towards no
  variant (presence, components, files, bytes, delete); it has its own entry in the model's `addons`, its own
  download and delete. Its folder is its own `dir`, else the first variant's (then only its globs are its files)."""

import contextlib
import hashlib
import importlib
import json
import logging
import os
import re
import shutil
import struct
import sys
import threading
from fnmatch import fnmatchcase
from pathlib import Path

from core import pack as packs

log = logging.getLogger("models")

SIDECARS = Path(".cache/huggingface/download")
MARKER = Path(".cache/helmstudio/download.json")
SGLANG_COPIES = Path(".cache/helmstudio/sglang")  # minimax_h3 sgl.ready_lora: <stem>-<key12><suffix> per file
SHA256 = re.compile(r"^[0-9a-f]{64}$")
MAX_FILES = 4096  # registry: files[] maxItems


def sha256_file(path: Path, check=lambda: None) -> str:
    """The file's SHA-256; `check()` runs every 64 MB (a cancel point)."""
    h = hashlib.sha256()
    with path.open("rb") as f:
        while chunk := f.read(1 << 26):
            check()
            h.update(chunk)
    return h.hexdigest()


def recorded(root: Path, rel: str, st: os.stat_result) -> tuple[str | None, bool]:
    """(the SHA-256 the download sidecar recorded, whether the file is unchanged since it was written)."""
    try:
        _commit, etag, at = (root / SIDECARS / f"{rel}.metadata").read_text().split("\n")[:3]
    except OSError, ValueError:
        return None, False
    if not SHA256.match(etag.strip()):  # a git blob id (a small non-LFS file): not a SHA-256
        return None, False
    try:
        fresh = st.st_mtime <= float(at) + 1
    except ValueError:
        fresh = False
    return etag.strip(), fresh


class Hashes:
    """path → SHA-256 of model files, kept by (size, mtime_ns). Unknown files queue; `start()` hashes them in a
    background thread, `drain()` now (tests)."""

    def __init__(self, path: Path | None = None) -> None:
        self.path, self.lock = path, threading.Lock()
        self.known: dict[str, list] = {}  # str(path) → [size, mtime_ns, sha256]
        self.queue: dict[str, None] = {}  # ordered set of paths to hash
        self.wake = threading.Event()
        if path:
            try:
                self.known = json.loads(path.read_text())
            except OSError, ValueError:
                pass

    def get(self, path: Path, st: os.stat_result | None = None) -> str | None:
        """The kept hash of `path` as it is now, or None (then queued)."""
        st = st or path.stat()
        with self.lock:
            k = self.known.get(str(path))
            if k and k[:2] == [st.st_size, st.st_mtime_ns]:
                return k[2]
            self.queue[str(path)] = None
        self.wake.set()
        return None

    def put(self, path: Path, sha: str) -> None:
        """Keep `sha` as the hash of `path` as it is now (a download checked it)."""
        st = path.stat()
        with self.lock:
            self.known[str(path)] = [st.st_size, st.st_mtime_ns, sha]
            self.queue.pop(str(path), None)
        self.save()

    def compute(self, path: Path, check=lambda: None) -> str:
        """The hash of `path` now: kept, else computed (and kept)."""
        if (sha := self.get(path)) is None:
            sha = sha256_file(path, check)
            self.put(path, sha)
        return sha

    def drain(self) -> None:
        """Hash every queued file now."""
        while True:
            with self.lock:
                p = next(iter(self.queue), None)
            if p is None:
                return
            try:
                self.compute(Path(p))
            except OSError:  # gone meanwhile
                with self.lock:
                    self.queue.pop(p, None)
            except Exception:  # never end the hashes thread
                log.exception("hashing %s failed", p)
                with self.lock:
                    self.queue.pop(p, None)

    def save(self) -> None:
        if not self.path:
            return
        try:
            self.path.parent.mkdir(parents=True, exist_ok=True)
            with self.lock:
                text = json.dumps(self.known)
            tmp = self.path.with_suffix(".tmp")
            tmp.write_text(text)
            tmp.replace(self.path)
        except OSError:
            log.warning("could not keep model hashes in %s", self.path, exc_info=True)

    def start(self) -> None:
        """Hash queued files in a daemon thread, one at a time (a rescan publishes what it learnt)."""

        def loop() -> None:
            while True:
                self.wake.wait()
                self.wake.clear()
                self.drain()

        threading.Thread(target=loop, name="hashes", daemon=True).start()


hashes = Hashes()  # main replaces it with one kept on disk and started


def is_safe(rel: str) -> bool:
    """A relative path that stays inside its folder: no absolute path, `..`, backslash, control character or
    `.cache` (the sidecars)."""
    parts = rel.split("/")  # .cache in any case: macOS APFS folds case
    return bool(rel) and not rel.startswith("/") and "\\" not in rel and parts[0].casefold() != ".cache" \
        and not any(ord(c) < 32 for c in rel) and all(p not in ("", ".", "..") for p in parts)  # fmt: skip


def inside(root: Path, path: Path) -> bool:
    """Whether `path`, links resolved, is inside `root`."""
    return path.resolve().is_relative_to(root.resolve())


def linked(base: Path, rel: str) -> bool:
    """Whether `base` or a folder between it and `base/rel` is a symlink (a write there lands in another folder)."""
    p = base
    for part in ["", *rel.split("/")[:-1]]:
        p = p / part
        if p.is_symlink():
            return True
    return False


def own_files(folder: Path) -> list[Path]:
    """The files of a variant's folder, relative and sorted: not links, nothing under a dot folder or dot file."""
    if not folder.is_dir():
        return []
    return sorted(
        p.relative_to(folder)
        for p in folder.rglob("*")
        if p.is_file() and not p.is_symlink() and not any(x.startswith(".") for x in p.relative_to(folder).parts)
    )


def globs(pack, component: str, variant: str | None = None) -> list[str]:
    return packs.component_globs(pack, variant or next(iter(pack.spec["variants"])), component)


def component_of(pack, rel: str, variant: str | None = None) -> str | None:
    """The first component (model.yaml order) whose glob (the variant's) matches `rel`; None for configs."""
    return next((c for c in pack.spec.get("components", {})
                 if any(fnmatchcase(rel, g) for g in globs(pack, c, variant))), None)  # fmt: skip


def own_globs(pack, vid: str) -> list[str] | None:
    """The globs of a variant's own files when its folder is another variant's too: its components it does not
    share. None: the whole folder is its own (a folder of its own, or the first variant's)."""
    v, root = pack.spec["variants"][vid], folder(pack, vid)
    if not v.get("shares") or not any(x != vid and folder(pack, x) == root for x in pack.spec["variants"]):
        return None
    return [g for c in packs.needed(pack, vid) if c not in v["shares"] for g in globs(pack, c, vid)]


def addon_dir(pack, component: str) -> str:
    """An add-on's model folder: the component's own `dir`, else the first variant's."""
    own = pack.component(component).get("dir")
    return own or pack.spec["variants"][next(iter(pack.spec["variants"]))]["dir"]


def addon_folder(pack, component: str) -> Path:
    return packs.place(addon_dir(pack, component))


def addon_dest(pack, component: str) -> Path:
    """Where a download of the add-on puts the repo's files: model.yaml `addon.source.dir` under the models root
    (LTX's detailer: the repo's root file into ltx-2.5/loras, a folder inside the add-on's), else its folder."""
    d = pack.component(component)["addon"].get("source", {}).get("dir")
    return packs.place(d) if d else addon_folder(pack, component)


def addon_globs_in(pack, root: Path) -> list[str]:
    """The globs of the add-ons without a `dir` of their own whose files sit in `root` (a variant's folder): never
    that variant's files."""
    return [g for c in packs.addons(pack) if not pack.component(c).get("dir") and addon_folder(pack, c) == root
            for g in globs(pack, c)]  # fmt: skip


def own_folder(pack, component: str) -> bool:
    """Whether the add-on's folder is its own alone: a `dir` of its own that no other add-on names (API 1.41: MiniMax
    H3's Turbo LoRA and Turbo Ref LoRA share MiniMax-H3-Turbo, so each goes by its own file there)."""
    own = pack.component(component).get("dir")
    return bool(own) and not any(c != component and pack.component(c).get("dir") == own for c in packs.addons(pack))


def marked(pack, component: str, root: Path) -> bool:
    """An unfinished download's marker in `root` that counts against the add-on: any, unless the folder is shared
    with another add-on, where only one naming it (its `variant`, download.fetch) does."""
    try:
        text = (root / MARKER).read_text()
    except OSError:
        return False
    if own_folder(pack, component) or not pack.component(component).get("dir"):
        return True
    try:
        return json.loads(text).get("variant") in (component, None)
    except ValueError:
        return True


def addon_files(pack, component: str) -> list[Path]:
    """An add-on's files, relative to its folder: the whole folder when it is its own alone, else its globs there."""
    files = own_files(addon_folder(pack, component))
    if own_folder(pack, component):
        return files
    return [f for f in files if any(fnmatchcase(f.as_posix(), g) for g in globs(pack, component))]


def variant_files(pack, vid: str) -> list[Path]:
    """own_files of the variant's folder that are its own: those of its `own_globs`, or, for a folder another
    variant also uses, those none of the others' own globs match."""
    files, root = own_files(folder(pack, vid)), folder(pack, vid)
    match = lambda rel, gs: any(fnmatchcase(rel.as_posix(), g) for g in gs)
    if (mine := own_globs(pack, vid)) is not None:
        return [f for f in files if match(f, mine)]
    theirs = [g for x in pack.spec["variants"] if x != vid and folder(pack, x) == root for g in own_globs(pack, x) or []]  # fmt: skip
    theirs += addon_globs_in(pack, root)
    return [f for f in files if not match(f, theirs)]


def folder(pack, variant: str) -> Path:
    return packs.place(pack.spec["variants"][variant]["dir"])


def manifest(pack, variant: str, addon: str | None = None) -> tuple[list[dict], list[str]]:
    """(files [{path, bytes, sha256?, component?}], mismatch [path]) of the variant's own folder (with `addon`:
    of that add-on's files)."""
    if addon:
        root, rels = addon_folder(pack, addon), addon_files(pack, addon)
    else:
        root, rels = folder(pack, variant), variant_files(pack, variant)
    files, mismatch = [], []
    for rel in rels[:MAX_FILES]:
        p, r = root / rel, rel.as_posix()
        st = p.stat()
        rec, fresh = recorded(root, r, st)
        sha = rec if fresh else hashes.get(p, st)
        if rec and sha and sha != rec:
            mismatch.append(r)
        item = {"path": r, "bytes": st.st_size} | ({"sha256": sha} if sha else {})
        if c := component_of(pack, r, variant):
            item["component"] = c
        files.append(item)
    return files, mismatch


def device(pack) -> str:
    """This runner's device for the pack's runtime, as the registry names devices (MLX's Metal `gpu` is mps). In an
    env without the runtime's framework (a torch pack read in the mlx env's tests) the host's own stands in, so
    `runs_here` never imports torch there."""
    try:
        d = importlib.import_module(f"core.loaders_{pack.runtime}").device()
    except ModuleNotFoundError:
        d = "mps" if sys.platform == "darwin" else "cpu"
    return "mps" if d == "gpu" else d


def runs_here(pack, vid: str) -> bool:
    """Whether the variant runs on this runner's device (API 1.33 model.yaml `variants.<id>.devices`: a community
    FP8 variant is [cuda]); one that does not is never published here, so it is never offered or sent here."""
    # and on its runtime's devices (a native binary is built per device; ltx's torch runtime is NVIDIA's only)
    want = (
        pack.spec["variants"][vid].get("devices"),
        pack.spec["runtimes"][pack.runtime].get("devices"),
    )
    return all(not w or device(pack) in w for w in want)


# a safetensors dtype as a precision (API 1.34 component `stored`); U8 (NVFP4) and U32 (MLX packed: int4 or int8) say nothing
DTYPES = {
    "BF16": "bf16",
    "F16": "fp16",
    "F32": "fp32",
    "F8_E4M3": "fp8",
    "F8_E5M2": "fp8",
    "I8": "int8",
}
_dtypes: dict = {}


def stored_dtype(files: list[Path]) -> str | None:
    """The dtype most of the files' tensor bytes are stored in (their safetensors headers), as a precision; None
    when none is a readable safetensors file or that dtype is not one (a GGUF, NVFP4, MLX packed)."""
    by: dict[str, int] = {}
    for p in files:
        if p.suffix != ".safetensors":
            continue
        try:
            st = p.stat()
            k = (p, st.st_size, st.st_mtime_ns)
            if k not in _dtypes:
                _dtypes[k] = [(t["dtype"], t["data_offsets"][1] - t["data_offsets"][0]) for t in packs.header(p).values()]  # fmt: skip
        except OSError, ValueError, KeyError, TypeError, struct.error:
            continue
        for d, n in _dtypes[k]:
            by[d] = by.get(d, 0) + n
    return DTYPES.get(max(by, key=by.get)) if by else None


def variant_entry(pack, vid: str) -> dict:
    """One variant of the models key (registry.schema.json `models[].variants[]`)."""
    v = pack.spec["variants"][vid]
    ref = packs.ModelRef(pack, vid, "bf16")
    files, mismatch = manifest(pack, vid)
    comps, ok, unfinished = [], True, (folder(pack, vid) / MARKER).exists()
    found_by = {
        c: ref.files(c) for c in packs.needed(pack, vid)
    }  # an add-on is no variant's (addon_entry), nor a component scoped to other variants
    # a file several components read (LTX: vae and vae_encoder, the connector inside the transformer and text
    # encoder files) counts once, for the component listing the fewest files (the first on a tie): bytes add up
    counted: dict = {}
    for c, found in sorted(found_by.items(), key=lambda kv: len(kv[1])):
        for f in found:
            counted.setdefault(f, c)
    for c, found in found_by.items():
        owner = ref.owner(c)
        ok = ok and bool(found)
        unfinished = unfinished or (folder(pack, owner) / MARKER).exists()
        item = {"component": c, "from_variant": owner, "precisions": packs.component_precisions(pack, vid, c)}
        if d := stored_dtype(found):
            item["stored"] = d
        if found:
            item["bytes"] = sum(f.stat().st_size for f in found if counted[f] == c)
        if owner == vid and c in v.get("shares", []):
            item["separate"] = True
        comps.append(item)
    if mismatch:
        presence = "mismatch"
    elif ok and not unfinished:
        presence = "present"
    else:
        presence = "partial" if files else "missing"
    out = {"id": vid, "presence": presence}
    if files:
        out["bytes"] = sum(f["bytes"] for f in files)
    out["precisions"] = packs.variant_precisions(pack, vid)
    out |= {"title": v.get("title", vid), "dir": v["dir"]}
    for k in ("source", "sources", "devices", "distill_from", "help"):
        if k in v:
            out[k] = v[k]
    if comps:
        out["components"] = comps
    if files:
        out["files"] = files
    if mismatch:
        out["mismatch"] = mismatch[:64]
    return out


def addon_entry(pack, c: str) -> dict:
    """One add-on of the models key (registry.schema.json `models[].addons[]`): presence as a variant's, over its
    own files only."""
    spec = pack.component(c)
    files, mismatch = manifest(pack, next(iter(pack.spec["variants"])), addon=c)
    root = addon_folder(pack, c)
    if mismatch:
        presence = "mismatch"
    elif addon_weights(pack, c) and not marked(pack, c, root) and not marked(pack, c, addon_dest(pack, c)):
        presence = "present"
    else:
        presence = "partial" if files else "missing"
    out = {"component": c, "title": spec["addon"]["title"]}
    # API 1.40: the controls' name, the Models card's mono line and the task (references or not) that needs it
    out |= {k: spec["addon"][k] for k in ("short", "tag", "refs", "help") if k in spec["addon"]}
    out |= {"dir": addon_dir(pack, c), "presence": presence}
    if files:
        out["bytes"] = sum(f["bytes"] for f in files)
    if "source" in spec["addon"]:  # its `dir` is model.yaml's (addon_dest), not the contract's
        out["source"] = {k: v for k, v in spec["addon"]["source"].items() if k != "dir"}
    if "variants" in spec:  # API 1.40: only these variants' runs need it
        out["variants"] = list(spec["variants"])
    if mismatch:
        out["mismatch"] = mismatch[:64]
    return out


def listed_addons(pack) -> list[str]:
    """The add-ons this runner lists (API 1.40): those a variant it runs needs (the component's `variants`, absent:
    every variant). LTX's are the Mac's, so the NVIDIA runner lists none."""
    return [c for c in packs.addons(pack)
            if any(runs_here(pack, v) for v in pack.component(c).get("variants", pack.spec["variants"]))]  # fmt: skip


def addon_weights(pack, c: str) -> list[Path]:
    """The add-on's weight files as a load reads them (its globs in its folder)."""
    return packs.component_files(pack, next(iter(pack.spec["variants"])), c)


def entry(pack) -> dict:
    """The pack's `models` entry: id, family, title, media, runtimes, variants (as on disk now), lora."""
    spec = pack.spec
    out = {"id": spec["id"], "family": pack.family, "title": spec["title"]}
    if "variants" in spec:
        if "media" in spec:
            out["media"] = spec["media"]
        out["runtimes"] = [{"runtime": r, "env": x["env"]} | ({"devices": x["devices"]} if "devices" in x else {})
                           for r, x in spec["runtimes"].items()]  # fmt: skip
        out["variants"] = [
            with_copies(lambda v=vid: variant_entry(pack, v), spec["variants"][vid]["dir"],
                        lambda v=vid: variant_freed(pack, v))
            for vid in spec["variants"] if runs_here(pack, vid)
        ]  # fmt: skip
        if (extra := listed_addons(pack)) and out["variants"]:
            out["addons"] = [with_copies(lambda x=c: addon_entry(pack, x), addon_dir(pack, c), None) for c in extra]
        # API 1.39: the components a non-default compute keys apart
        if compute := packs.compute_spec(pack):
            out["compute"] = {"components": compute["components"]}
    if block := packs.lora_block(pack):
        out["lora"] = block
    return out


def licence_text(pack, lid: str) -> str:
    return (pack.dir / "licences" / f"{lid}.md").read_text()


def licences(loaded) -> list[dict]:
    """The models key's `licences`: each licence a variant's source names, once (title and url from model.yaml
    `licences`, the text from licences/<id>.md)."""
    out: dict[str, dict] = {}
    for p in loaded:
        for v in p.spec.get("variants", {}).values():
            if (lid := v.get("source", {}).get("licence")) and lid not in out:
                meta = p.spec.get("licences", {}).get(lid, {})
                out[lid] = {"id": lid, "title": meta.get("title", lid)} | (
                    {"url": meta["url"]} if "url" in meta else {}) | {"text": licence_text(p, lid)}  # fmt: skip
    return list(out.values())


# --- model folders (API 1.78, contracts/README.md "Model folders")

seen: dict[str, list[str]] = {}  # folder id → its model folders when last connected (CACHE/folders-seen.json)


def refresh_seen() -> None:
    """Note what each connected folder of the list holds now (its top-level folders), kept across restarts, so an
    unplugged drive's models show `unplugged`, not missing. Called by every models scan."""
    import config

    global seen
    if not config.ROOTS:  # one folder: nothing can be unplugged
        return
    path = config.CACHE / "folders-seen.json"
    if not seen:
        try:
            seen = json.loads(path.read_text())
        except OSError, ValueError:
            seen = {}
    now = dict(seen)
    for text, root in packs.roots():
        if root.is_dir():
            with contextlib.suppress(OSError):
                now[packs.folder_id(text)] = sorted(x.name for x in root.iterdir() if x.is_dir() and not x.name.startswith("."))  # fmt: skip
    if now != seen:
        seen = now
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(json.dumps(seen))
        except OSError:
            log.warning("could not keep %s", path, exc_info=True)


def matches(rel: str, only: list[str]) -> bool:
    """delete_in's selection: a folder prefix ending in / or a glob."""
    return any(rel.startswith(k) if k.endswith("/") else fnmatchcase(rel, k) for k in only)


def variant_freed(pack, vid: str) -> int:
    """What deleting this copy of the variant gives back (call within a strict rooted scope): its own files, but the
    shared components another variant in the same folder still loads."""
    _kept, only = kept_components(pack, {vid})
    sel = only if (own := own_globs(pack, vid)) is None else own
    root = folder(pack, vid)
    return sum((root / f).stat().st_size for f in variant_files(pack, vid) if sel is None or matches(f.as_posix(), sel))


def with_copies(make, d: str, freed) -> dict:
    """`make()` (a variant's or add-on's entry as the resolver reads it) with, once a folder list is set, `folder`
    (the copy a run loads) and `copies` (every folder of the list with files of it; unplugged ones from `seen`); a
    variant missing everywhere connected but seen on an unplugged folder is `unplugged`."""
    import config

    out = make()
    if not config.ROOTS:
        return out
    copies, top = [], d.split("/")[0]
    for text, root in packs.roots():
        fid = packs.folder_id(text)
        if not root.is_dir():
            if top in seen.get(fid, ()):
                copies.append({"folder": fid, "state": "unplugged"})
            continue
        # a link is not a copy (and is never deleted)
        if not (root / d).exists() or (root / d).is_symlink():
            continue
        with packs.rooted(root, strict=False):
            e = make()
        if e["presence"] == "missing":
            continue
        with packs.rooted(root):
            gives = freed() if freed else e.get("bytes", 0)
        copies.append(
            {
                "folder": fid,
                "state": e["presence"],
                "bytes": e.get("bytes", 0),
                "freed_bytes": gives,
            }
        )
    if out["presence"] == "missing" and copies:  # only unplugged copies
        out["presence"] = "unplugged"
    elif out["presence"] != "missing" and (r := packs.root_of(packs.place(d))):
        out["folder"] = packs.folder_id(r[0])
    if copies:
        out["copies"] = copies
    return out


def folder_name(root: Path) -> str:
    """`/Volumes/<X>/…` → X, else Mac on macOS, else the host name."""
    from config import HOST

    if root.parts[:2] == ("/", "Volumes") and len(root.parts) > 2:
        return root.parts[2][:80]
    return "Mac" if sys.platform == "darwin" else HOST


def disks() -> list[dict]:
    """The hosts key's `models_disks`: each folder of the list in order."""
    down, out = packs.downloads_root(), []
    for text, root in packs.roots():
        item = {
            "id": packs.folder_id(text),
            "name": folder_name(root),
            "root": packs.shown(root)[:400],
        }
        if root.is_dir():
            u = shutil.disk_usage(root)
            item |= {"connected": True, "free_bytes": u.free, "total_bytes": u.total, "writable": os.access(root, os.W_OK)}  # fmt: skip
        else:
            item |= {"connected": False, "writable": False}
        out.append(item | {"downloads": root == down})
    return out


def check_folders(paths) -> dict:
    """req.runner.folders.check's reply: what each path is here now (never created)."""
    out = []
    for text in paths if isinstance(paths, list) else []:
        if not isinstance(text, str) or not text.startswith(("/", "~/")) or len(text) > 400:
            continue
        p = Path(text).expanduser()
        item = {"path": text, "connected": p.is_dir(), "drive": p.parts[:2] == ("/", "Volumes")}
        if item["connected"]:
            u = shutil.disk_usage(p)
            item |= {
                "writable": os.access(p, os.W_OK),
                "free_bytes": u.free,
                "total_bytes": u.total,
            }
        else:
            item["writable"] = False
        out.append(item)
    return {"folders": out[:8]}


# --- the host


def disk(root: Path) -> dict:
    """The hosts key's `models_disk`: the volume of the models root (or its nearest existing parent)."""
    p = root
    while not p.exists() and p != p.parent:
        p = p.parent
    u = shutil.disk_usage(p)
    return {
        "root": packs.shown(root)[:400],
        "free_bytes": u.free,
        "total_bytes": u.total,
        "writable": os.access(p, os.W_OK),
    }  # false on a read-only bind mount: no model jobs here


def machine(device: str, ram_gb: float) -> dict:
    """The hosts key's `machine`."""
    mac = sys.platform == "darwin"
    accel = "cuda" if device == "cuda" else "metal" if mac and device in ("mps", "gpu") else "cpu"
    packaging = "container" if Path("/.dockerenv").exists() else "native"
    return {"os": "macos" if mac else "linux", "accel": accel, "packaging": packaging, "ram_gb": round(ram_gb, 1)}


# --- delete


class InUse(Exception):
    """A delete was refused: the message is the one-line reason."""


def kept_components(pack, deleted: set[str]) -> tuple[list[str], list[str] | None]:
    """(kept components, what to delete of the lender's folder: None = all, else the folders or globs of its
    components not kept) when deleting the variants `deleted` (one, or all). A component is kept when another
    variant not deleted shares it from a deleted one (packs.share_owner: its lender) and still has files of its
    own; then only the lender's other components' files go (the folder's configs, processor and scheduler stay:
    the sharing variant reads them)."""
    kept: list[str] = []
    owner = None
    for vid, v in pack.spec["variants"].items():
        lender = packs.share_owner(pack, vid)
        if vid not in deleted and lender in deleted and v.get("shares") and variant_files(pack, vid):
            owner = lender
            kept += [c for c in v["shares"] if c not in kept and not packs.owns_copy(pack, vid, c)]
    if not kept:
        return [], None
    # a folder shared with a kept variant: only these files (its own ones stay); else their folders
    same = any(folder(pack, v) == folder(pack, owner) for v in pack.spec["variants"] if v not in deleted)
    only = [g if same else (g.rsplit("/", 1)[0] + "/" if "/" in g else g)
            for c in pack.spec["components"] if c not in kept for g in globs(pack, c, owner)]  # fmt: skip
    return kept, only


def linked_from_outside(root: Path, base: Path) -> set[str]:
    """The files of `base` (relative) that a link elsewhere under the models root resolves to: another model reads
    them (MiniMax-H3-Image/text_encoder links into MiniMax-H3/Ref2VA), so a delete of `base` keeps them."""
    b = base.resolve()
    out: set[str] = set()
    for dirpath, dirnames, filenames in os.walk(root, followlinks=False):
        d = Path(dirpath)
        if d == base:
            dirnames[:] = []
            continue
        for name in filenames + dirnames:
            p = d / name
            if not p.is_symlink():
                continue
            t = p.resolve()
            if not t.is_relative_to(b):
                continue
            targets = [f for f in t.rglob("*") if f.is_file()] if t.is_dir() else [t]
            out |= {f.relative_to(b).as_posix() for f in targets}
    return out


def delete_variant(pack, vid: str, only: list[str] | None = None) -> tuple[int, int]:
    """Remove the variant's files under its folder (`only`: just those under these folder prefixes ending in /,
    or matching these globs), sidecars with them, then the empty folders. Never follows a link: a link is removed,
    not its target; a file another model's link resolves to stays. An add-on's files in the folder stay (it is
    deleted on its own). Returns (files removed, bytes freed)."""
    return delete_in(folder(pack, vid), only, addon_globs_in(pack, folder(pack, vid)))


def delete_addon(pack, c: str) -> tuple[int, int]:
    """Remove an add-on's files (its whole folder when it is its own alone, else its globs in its folder)."""
    return delete_in(addon_folder(pack, c), None if own_folder(pack, c) else globs(pack, c), [])


def delete_in(base: Path, only: list[str] | None, addon: list[str]) -> tuple[int, int]:
    """delete_variant's work in `base`, keeping the files `addon`'s globs match."""
    root = (packs.root_of(base) or ("", packs.models_root()))[1]
    if not base.exists():
        return 0, 0
    if base.is_symlink() or not inside(root, base) or base.resolve() == root.resolve():
        raise InUse(f"{os.path.relpath(base, root)} is a link or outside the models root: remove it by hand")

    # API 1.78: a link in any folder of the list keeps what it points to (one in ~/GenAI/models into the T9's copy)
    lent = set().union(*(linked_from_outside(r, base) for r in {root, *packs.connected()}))

    def kept(rel: str) -> bool:
        side, copies = SIDECARS.as_posix() + "/", SGLANG_COPIES.as_posix() + "/"
        if rel.startswith(side):  # a sidecar goes with its file
            rel = re.sub(r"\.(metadata|lock)$", "", rel[len(side) :])
        elif rel.startswith(copies):  # so does an SGLang-ready copy
            rel = re.sub(r"-[0-9a-f]{12}(\.[^.]+)$", r"\1", rel[len(copies) :])
        if rel in lent or any(fnmatchcase(rel, g) for g in addon):
            return True
        if only is None:
            return False
        return not any(rel.startswith(k) if k.endswith("/") else fnmatchcase(rel, k) for k in only)

    files = freed = 0
    for dirpath, dirnames, filenames in os.walk(base, topdown=False, followlinks=False):
        d = Path(dirpath)
        for name in filenames + [n for n in dirnames if (d / n).is_symlink()]:
            p = d / name
            rel = p.relative_to(base).as_posix()
            if kept(rel):
                continue
            size, link = p.lstat().st_size, p.is_symlink()
            p.unlink()
            if not link and not rel.startswith(".cache/"):  # sidecars and markers are not the model's
                files, freed = files + 1, freed + size
        for name in dirnames:
            with_empty = d / name
            if not with_empty.is_symlink() and with_empty.is_dir() and not any(with_empty.iterdir()):
                with_empty.rmdir()
    if not any(base.iterdir()):
        base.rmdir()
    return files, freed
