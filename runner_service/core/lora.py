"""LoRAs (docs/plan/03-runtime.md section 6.4; contracts/README.md "LoRAs", "LoRA normaliser"), runtime-free:

- the normaliser: a safetensors header (key → {dtype, shape}) and one family's lora block → canonical
  module → (A, B, alpha) groups, applied / ignored / unapplied keys and the contract's error texts. No key is
  silently dropped: a file fits when at least one key applies and every other key is ignored;
- the file checks (contracts "LoRA upload checks") the shared index skips files with;
- the host's library: shared LoRAs indexed from <models>/loras/ and <models>/<model>/loras/ (header + one SHA-256,
  kept by path, size and mtime), and own LoRAs fetched from the owner's prefix into an LRU cache
  (<cache>/loras/<sha256>.safetensors), size and hash checked.

The torch adapter and bake are in core/loaders_torch.py (one wrapper per runtime)."""

import hashlib
import json
import logging
import os
import re
import struct
from datetime import UTC, datetime
from fnmatch import fnmatchcase
from pathlib import Path

from core.graph import RunError

log = logging.getLogger("lora")

SUFFIXES = {  # style → suffix → role
    "kohya": {".lora_down.weight": "A", ".lora_up.weight": "B", ".alpha": "alpha"},
    "comfyui": {
        ".lora_A.weight": "A",
        ".lora_B.weight": "B",
        ".lora_down.weight": "A",
        ".lora_up.weight": "B",
        ".alpha": "alpha",
    },
    "peft": {
        ".lora_A.weight": "A",
        ".lora_B.weight": "B",
        ".lora.down.weight": "A",
        ".lora.up.weight": "B",
        ".alpha": "alpha",
    },
}
KOHYA_PREFIXES, COMFY_PREFIX = ("lora_unet_", "lora_transformer_"), "diffusion_model."
DTYPES = {"F32", "F16", "BF16"}
MAX_HEADER, MAX_TENSORS, MAX_RANK = 8388608, 20000, 4096


# an IC-LoRA (Lightricks' LTX-2.5 IC-LoRAs) conditions on a reference video: never a stack row (upload checks)
IC_LORA = "reference_downscale_factor"
IC_LORA_REASON = "an IC-LoRA (it needs a reference video), not a stack LoRA"


class LoraFileError(ValueError):
    """The file is not an acceptable LoRA; the message is the contract's reason."""


# --- the normaliser


def expand(modules: list[str]) -> list[str]:
    """Each entry with one `{a..b}` range expands to a..b inclusive."""
    out = []
    for m in modules:
        r = re.search(r"\{(\d+)\.\.(\d+)\}", m)
        out += [m[: r.start()] + str(i) + m[r.end() :] for i in range(int(r[1]), int(r[2]) + 1)] if r else [m]
    return out


def compress(names) -> list[str]:
    """The inverse of `expand`: names that differ only in their first numeric segment over a contiguous range
    become one `{a..b}` entry; sorted."""
    groups: dict[tuple, list[int]] = {}
    plain = []
    for n in set(names):
        parts = n.split(".")
        i = next((i for i, p in enumerate(parts) if p.isdigit()), None)
        if i is None:
            plain.append(n)
        else:
            groups.setdefault((".".join(parts[:i]), ".".join(parts[i + 1 :])), []).append(int(parts[i]))
    out = plain
    for (pre, post), nums in groups.items():
        nums.sort()
        if len(nums) > 1 and nums == list(range(nums[0], nums[-1] + 1)):
            out.append(".".join(x for x in (pre, f"{{{nums[0]}..{nums[-1]}}}", post) if x))
        else:
            out += [".".join(x for x in (pre, str(k), post) if x) for k in nums]
    return sorted(out)


def style(keys) -> str | None:
    """The key style: the first rule (kohya, comfyui, peft) that matches any key, or None."""
    if any(k.startswith(KOHYA_PREFIXES) for k in keys):
        return "kohya"
    if any(k.startswith(COMFY_PREFIX) for k in keys):
        return "comfyui"
    if any(k.endswith(tuple(SUFFIXES["peft"])) for k in keys):
        return "peft"
    return None


def parse(key: str, key_style: str) -> tuple[str, str] | None:
    """(module part, role A | B | alpha), or None: not a key of that style."""
    if key_style == "kohya":
        pre = next((p for p in KOHYA_PREFIXES if key.startswith(p)), None)
        if pre is None:
            return None
        rest = key[len(pre) :]
    elif key_style == "comfyui":
        if not key.startswith(COMFY_PREFIX):
            return None
        rest = key[len(COMFY_PREFIX) :]
    else:
        rest = key.removeprefix("base_model.model.").removeprefix("transformer.")
    suf = next((x for x in SUFFIXES[key_style] if rest.endswith(x)), None)
    return (rest[: -len(suf)], SUFFIXES[key_style][suf]) if suf and len(rest) > len(suf) else None


def _matches(rename: dict, part: str) -> bool:
    """A rename applies when its `from` starts the module part (`at: start`, the default) or ends it (`at: end`)."""
    f = rename["from"]
    return part.endswith(f) if rename.get("at") == "end" else part.startswith(f)


def normalise(name: str, tensors: dict, block: dict, family: str, shapes: dict | None = None) -> dict:
    """Apply a file's header ({key: {dtype, shape}}) to one family's lora block ({modules, renames, ignore}).
    `shapes` (module → (in_features, out_features)), when given, also requires A [rank, in] and B [out, rank] to
    fit the model's Linear. Answers key_style, rank, total, applied, ignored, unapplied (sorted keys), error (None
    when it fits) and groups: module → {"A": key, "B": key, "alpha": key or None} of the applied ones."""
    keys = sorted(k for k in tensors if k != "__metadata__")
    ks, mods = style(keys), expand(block["modules"])
    resolve = {m.replace(".", "_"): m for m in mods} if ks == "kohya" else {m: m for m in mods}
    ignored, unapplied, parts = [], [], {}
    for k in keys:
        if any(fnmatchcase(k, g) for g in block["ignore"]):
            ignored.append(k)
        elif not (pr := parse(k, ks) if ks else None):
            unapplied.append(k)
        else:
            part, role = pr
            r = next((r for r in block["renames"] if _matches(r, part)), None)
            if r:
                n = len(r["from"])
                part = part[:-n] + r["to"] if r.get("at") == "end" else r["to"] + part[n:]
            parts.setdefault(part, {}).setdefault(role, []).append(k)
    applied, rank, groups = [], None, {}
    for part, roles in parts.items():
        a, b, al = roles.get("A", []), roles.get("B", []), roles.get("alpha", [])
        ok = len(a) == len(b) == 1 and len(al) <= 1 and set(roles) <= {"A", "B", "alpha"}
        if ok:
            ta, tb = tensors[a[0]], tensors[b[0]]
            ok = (
                len(ta["shape"]) == len(tb["shape"]) == 2
                and ta["shape"][0] == tb["shape"][1] >= 1
                and all(tensors[k]["dtype"] in DTYPES for k in a + b + al)
                and all(tensors[k]["shape"] in ([], [1]) for k in al)
            )
        if ok:
            rank = max(rank or 0, ta["shape"][0])
        module = resolve.get(part)
        if ok and module and shapes is not None:
            ok = (ta["shape"][1], tb["shape"][0]) == tuple(shapes.get(module, ()))
        group = sorted(k for ks_ in roles.values() for k in ks_)
        if ok and module:
            applied += group
            groups[module] = {"A": a[0], "B": b[0], "alpha": al[0] if al else None}
        else:
            unapplied += group
    unapplied.sort()
    total, error = len(keys), None
    if not applied:
        error = f"LoRA {name}: 0 of {total} keys apply to {family} ({ks or 'unrecognised'} keys); is it for another model?"  # fmt: skip
    elif unapplied:
        n = len(unapplied)
        error = f"LoRA {name}: {n} of {total} keys do not apply to {family}: {', '.join(unapplied[:3])}"
        error += f" and {n - 3} more" if n > 3 else ""
    return {
        "key_style": ks,
        "rank": rank,
        "total": total,
        "applied": len(applied),
        "ignored": len(ignored),
        "unapplied": unapplied,
        "error": error,
        "groups": groups,
    }


def licence(meta: dict) -> str | None:
    v = (meta.get("modelspec.license") or meta.get("license") or "").strip()
    return v[:200] or None


# --- the file checks


def read_header(path: Path, ignore=()) -> tuple[dict, dict]:
    """(tensors {key: {dtype, shape, data_offsets}}, __metadata__) of a LoRA file, after the contract's upload
    checks in their order (LoraFileError with the reason). `ignore`: every published family's ignore globs."""
    size = path.stat().st_size
    with path.open("rb") as f:
        head = f.read(8)
        if head[:4] == b"PK\x03\x04" or (len(head) > 1 and head[0] == 0x80 and 2 <= head[1] <= 5):
            raise LoraFileError("pickle checkpoints are not accepted; convert it to safetensors")
        n = struct.unpack("<Q", head)[0] if len(head) == 8 else 0
        if n < 2 or 8 + n > size:
            raise LoraFileError("not a safetensors file")
        if n > MAX_HEADER:
            raise LoraFileError("safetensors header is larger than 8 MB")
        raw = f.read(n)
    try:
        h = json.loads(raw.decode())
        ok = isinstance(h, dict)
    except UnicodeDecodeError, ValueError, RecursionError:  # RecursionError: deeply nested JSON
        ok = False
    meta = h.pop("__metadata__", {}) if ok else {}
    ok = ok and isinstance(meta, dict) and all(isinstance(v, str) for v in meta.values())
    ok = ok and all(
        isinstance(t, dict)
        and t.keys() == {"dtype", "shape", "data_offsets"}
        and isinstance(t["dtype"], str)
        and isinstance(t["shape"], list)
        and all(isinstance(d, int) and d >= 0 for d in t["shape"])
        and isinstance(t["data_offsets"], list)
        and len(t["data_offsets"]) == 2
        and all(isinstance(o, int) for o in t["data_offsets"])
        for t in h.values()
    )
    if not ok:
        raise LoraFileError("not a safetensors file")
    if len(h) > MAX_TENSORS:
        raise LoraFileError(f"more than {MAX_TENSORS} tensors")
    keys, data = sorted(h), size - 8 - n
    spans = sorted((h[k]["data_offsets"], k) for k in keys)
    bad, end = [], 0
    for (lo, hi), k in spans:
        if lo < end or lo > hi or hi > data:
            bad.append(k)
        end = max(end, hi)
    if bad:
        raise LoraFileError(f"tensor {min(bad)} lies outside the file")
    for k in keys:
        if h[k]["dtype"] not in DTYPES:
            raise LoraFileError(f"tensor {k} has dtype {h[k]['dtype']}; LoRAs must be F32, F16 or BF16")
    ks = style(keys)
    if ks is None:
        raise LoraFileError("no LoRA keys found (is it a full model?)")
    parts: dict[str, dict] = {}
    for k in keys:
        if any(fnmatchcase(k, g) for g in ignore):
            continue
        if (pr := parse(k, ks)) is None:
            raise LoraFileError(f"{k} is not a LoRA weight")
        parts.setdefault(pr[0], {})[pr[1]] = k
    for part, roles in sorted(parts.items(), key=lambda x: min(x[1].values())):
        if "A" in roles and "B" not in roles:
            raise LoraFileError(f"{part}: lora_B is missing")
        if "B" in roles and "A" not in roles:
            raise LoraFileError(f"{part}: lora_A is missing")
        if "A" in roles:
            a, b = h[roles["A"]]["shape"], h[roles["B"]]["shape"]
            if len(a) == len(b) == 2 and a[0] != b[1]:
                raise LoraFileError(f"{part}: lora_A and lora_B ranks differ")
    ranks = [h[r["A"]]["shape"][0] for r in parts.values() if "A" in r and h[r["A"]]["shape"]]
    if not ranks:  # alpha keys only, or every key ignored
        raise LoraFileError("no LoRA keys found (is it a full model?)")
    if max(ranks) > MAX_RANK:
        raise LoraFileError(f"rank {max(ranks)} is over {MAX_RANK}")
    return {k: h[k] for k in keys}, meta


def sha256_file(path: Path) -> str:
    with path.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


# --- the host's library


class Library:
    """This host's LoRAs: the shared index (registry `<host>.<env>.loras`) and the cache of own LoRAs.
    `blocks`: family → its registry lora block (the families this runner's packs publish)."""

    def __init__(
        self,
        models: Path | None,
        cache: Path,
        *,
        store=None,
        host: str = "",
        cache_gb: float = 20.0,
    ):
        self.models, self.dir, self.store, self.host = models, cache / "loras", store, host
        self.cap = int(cache_gb * 2**30)
        self.shared: dict[str, Path] = {}  # sha256 → file
        # every indexed file → its sha256 (one LoRA may have several names)
        self.files: dict[Path, str] = {}
        self.entries: list[dict] = []  # the registry value's `loras`

    def scan(self, blocks: dict[str, dict]) -> list[dict]:
        """Index every *.safetensors under <models>/loras/ and <models>/*/loras/ (not below them); a file that
        fails the checks is logged and left out. Hashes are kept in <cache>/loras/shared.json by path, size and
        mtime, so a restart rehashes nothing."""
        known_path = self.dir / "shared.json"
        try:
            known = json.loads(known_path.read_text())
        except OSError, ValueError:
            known = {}
        from core import pack as packs

        # API 1.78: every connected model folder of the list when `models` is None
        tops = [self.models] if self.models else packs.connected()
        files = sorted({f for m in tops for g in ("loras/*.safetensors", "*/loras/*.safetensors") for f in m.glob(g)})
        ignore = [g for b in blocks.values() for g in b["ignore"]]
        shared, files_, entries, kept = (
            {},
            {},
            [],
            {},
        )  # swapped in at the end: a run may read them meanwhile
        for p in files:
            try:
                tensors, meta = read_header(p, ignore)
                # the last upload check; not in read_header: ltx/ic-lora loads these files
                if IC_LORA in meta:
                    raise LoraFileError(IC_LORA_REASON)
            except Exception as e:  # noqa: BLE001 (one bad file never stops the scan)
                log.warning("shared LoRA %s left out: %s", p, e)
                continue
            st = p.stat()
            stamp = [st.st_size, st.st_mtime_ns]
            if known.get(str(p), [None])[:2] == stamp:
                sha = known[str(p)][2]
            else:
                log.info("hashing shared LoRA %s (%.1f GB)", p, st.st_size / 2**30)
                sha = sha256_file(p)
            kept[str(p)] = [*stamp, sha]
            files_[p] = sha
            if sha in shared:
                continue  # the same file twice on this host is one LoRA
            # family detection: this runner's packs are one env, so the fake-last rule leaves name order
            fits = [f for f in sorted(blocks) if normalise(p.stem, tensors, blocks[f], f)["error"] is None]
            res = normalise(p.stem, tensors, _NO_BLOCK, "")  # style and rank need no family
            shared[sha] = p
            entries.append(
                {
                    "id": sha,
                    "name": p.stem[:80],
                    "family": fits[0] if fits else "unknown",
                    "rank": res["rank"] or 1,
                    "bytes": st.st_size,
                    "key_style": res["key_style"],
                    "licence": licence(meta),
                    "modified_at": datetime.fromtimestamp(st.st_mtime, UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
                }
            )
        self.shared, self.files, self.entries = shared, files_, entries
        try:
            self.dir.mkdir(parents=True, exist_ok=True)
            known_path.write_text(json.dumps(kept))
        except OSError:
            log.warning("could not keep the shared LoRA hashes in %s", known_path, exc_info=True)
        log.info("indexed %d shared LoRAs", len(self.entries))
        return self.entries

    async def path(self, use, owner: str, progress=None) -> Path:
        """The local file of a LoRA of a run (a core.pack.LoraUse): a shared one from the index, an own one from the
        cache, fetched first (only under the owner's u/<owner>/loras/, size and SHA-256 checked) when missing.
        `await progress(phase)` before a fetch."""
        if use.source == "shared":
            if (p := self.shared.get(use.sha256)) is None:
                raise RunError(f"LoRA {use.name} is not on host {self.host}")
            return p
        if not (use.key or "").startswith(f"u/{owner}/loras/"):
            raise RunError(f"LoRA {use.name} is not the run owner's")
        dest = self.dir / f"{use.sha256}.safetensors"
        if dest.is_file() and dest.stat().st_size == use.bytes:
            os.utime(dest)  # most recently used
            return dest
        if progress:
            await progress(f"fetching LoRA {use.name}")
        self.dir.mkdir(parents=True, exist_ok=True)
        self._evict(use.bytes)
        part = dest.with_suffix(f".part{os.getpid()}")
        try:
            got = await self.store.download(use.key, part, use.bytes)
            if got != (use.bytes, use.sha256):
                raise RunError(f"LoRA {use.name} is gone")
            part.replace(dest)
        finally:
            part.unlink(missing_ok=True)  # a failed or cancelled fetch leaves nothing behind
        log.info("fetched own LoRA %s to the cache (%d bytes, SHA-256 verified)", use.name, use.bytes)
        return dest

    def _evict(self, room: int) -> None:
        """Delete least recently used cached LoRAs until `room` more bytes fit under the cap."""
        for p in self.dir.glob("*.part*"):  # left by a runner that died mid-fetch; ponytail: one fetch at a time
            p.unlink(missing_ok=True)
        files = sorted(self.dir.glob("*.safetensors"), key=lambda p: p.stat().st_mtime)
        used = sum(p.stat().st_size for p in files)
        for p in files:
            if used + room <= self.cap:
                break
            used -= p.stat().st_size
            p.unlink(missing_ok=True)
            log.info("LoRA cache: dropped %s", p.name)


_NO_BLOCK = {"modules": [], "renames": [], "ignore": []}
