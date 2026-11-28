"""contracts/check.py, LoRA normaliser (README, "LoRA normaliser").
Moved out of check.py as is; check.py re-exports every name."""

import json
import re
from fnmatch import fnmatchcase

from check_core import CANONICAL, HERE, fail

# ---------------------------------------------------------------- LoRA normaliser (README, "LoRA normaliser")
LORA_SUFFIXES = {  # style → suffix → role
    "kohya": {".lora_down.weight": "A", ".lora_up.weight": "B", ".alpha": "alpha"},
    "comfyui": {".lora_A.weight": "A", ".lora_B.weight": "B", ".lora_down.weight": "A", ".lora_up.weight": "B",
                ".alpha": "alpha"},
    "peft": {".lora_A.weight": "A", ".lora_B.weight": "B", ".lora.down.weight": "A", ".lora.up.weight": "B",
             ".alpha": "alpha"},
}
KOHYA_PREFIXES, COMFY_PREFIX = ("lora_unet_", "lora_transformer_"), "diffusion_model."
LORA_DTYPES = {"F32", "F16", "BF16"}


def expand_modules(modules: list[str]) -> list[str]:
    """Each entry with one `{a..b}` range expands to a..b inclusive."""
    out = []
    for m in modules:
        r = re.search(r"\{(\d+)\.\.(\d+)\}", m)
        out += [m[:r.start()] + str(i) + m[r.end():] for i in range(int(r[1]), int(r[2]) + 1)] if r else [m]
    return out


def lora_style(keys: list[str]) -> str | None:
    if any(k.startswith(KOHYA_PREFIXES) for k in keys):
        return "kohya"
    if any(k.startswith(COMFY_PREFIX) for k in keys):
        return "comfyui"
    if any(k.endswith(tuple(LORA_SUFFIXES["peft"])) for k in keys):
        return "peft"
    return None


def lora_parse(key: str, style: str) -> tuple[str, str] | None:
    """(module part, role A | B | alpha), or None: not a key of that style."""
    if style == "kohya":
        pre = next((p for p in KOHYA_PREFIXES if key.startswith(p)), None)
        if pre is None:
            return None
        rest = key[len(pre):]
    elif style == "comfyui":
        if not key.startswith(COMFY_PREFIX):
            return None
        rest = key[len(COMFY_PREFIX):]
    else:
        rest = key.removeprefix("base_model.model.").removeprefix("transformer.")
    suf = next((x for x in LORA_SUFFIXES[style] if rest.endswith(x)), None)
    return (rest[:-len(suf)], LORA_SUFFIXES[style][suf]) if suf and len(rest) > len(suf) else None


def normalise(name: str, tensors: dict, spec: dict, fam: str) -> dict:
    """Apply a file's header ({key: {dtype, shape}}) to one family's lora block. Answers style, rank, total,
    applied, ignored, unapplied (sorted keys), error (None when it fits)."""
    keys = sorted(k for k in tensors if k != "__metadata__")
    style, mods = lora_style(keys), expand_modules(spec["modules"])
    resolve = {m.replace(".", "_"): m for m in mods} if style == "kohya" else {m: m for m in mods}
    ignored, unapplied, parts = [], [], {}
    for k in keys:
        if any(fnmatchcase(k, g) for g in spec["ignore"]):
            ignored.append(k)
        elif not (pr := lora_parse(k, style) if style else None):
            unapplied.append(k)
        else:
            part, role = pr
            end = lambda r: r.get("at", "start") == "end"
            r = next((r for r in spec["renames"] if (part.endswith if end(r) else part.startswith)(r["from"])), None)
            if r:
                part = part[:-len(r["from"])] + r["to"] if end(r) else r["to"] + part[len(r["from"]):]
            parts.setdefault(part, {}).setdefault(role, []).append(k)
    applied, rank = [], None
    for part, roles in parts.items():
        a, b, al = roles.get("A", []), roles.get("B", []), roles.get("alpha", [])
        ok = len(a) == len(b) == 1 and len(al) <= 1 and set(roles) <= {"A", "B", "alpha"}
        if ok:
            ta, tb = tensors[a[0]], tensors[b[0]]
            ok = (len(ta["shape"]) == len(tb["shape"]) == 2 and ta["shape"][0] == tb["shape"][1] >= 1
                  and all(tensors[k]["dtype"] in LORA_DTYPES for k in a + b + al)
                  and all(tensors[k]["shape"] in ([], [1]) for k in al))
        if ok:
            rank = max(rank or 0, ta["shape"][0])
        group = sorted(k for ks in roles.values() for k in ks)
        (applied if ok and part in resolve else unapplied).extend(group)
    unapplied.sort()
    total, error = len(keys), None
    if not applied:
        error = f"LoRA {name}: 0 of {total} keys apply to {fam} ({style or 'unrecognised'} keys); is it for another model?"
    elif unapplied:
        n = len(unapplied)
        error = f"LoRA {name}: {n} of {total} keys do not apply to {fam}: {', '.join(unapplied[:3])}"
        error += f" and {n - 3} more" if n > 3 else ""
    return {"key_style": style, "rank": rank, "total": total, "applied": len(applied), "ignored": len(ignored),
            "unapplied": unapplied, "error": error}


def lora_licence(meta: dict) -> str | None:
    v = (meta.get("modelspec.license") or meta.get("license") or "").strip()
    return v[:200] or None


def check_loras() -> None:
    """fixtures/loras/*.json: header-only LoRA files run through the reference normaliser against the canonical
    lora block of the fixture's `family` (default qwen-image; an optional `ignore` replaces its ignore list)."""
    blocks = {}
    for prefix in CANONICAL[1:]:
        if not (f := HERE / "fixtures" / "registry" / f"{prefix}models.valid.json").exists():
            continue  # server: no models of its own
        m = json.loads(f.read_text())["models"][0]
        if "lora" not in m:
            continue  # a family without LoRAs (llm)
        blocks[m["family"]] = spec = m["lora"]
        mods = expand_modules(spec["modules"])
        if len({m.replace(".", "_") for m in mods}) != len(mods):
            fail(f"{prefix}models lora block: module names are not unique with . read as _ (kohya)")
        if spec["modules"] != sorted(spec["modules"]):
            fail(f"{prefix}models lora block: modules are not sorted")
    for p in sorted((HERE / "fixtures" / "loras").glob("*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/loras/{p.name}"
        fam = fx.get("family", "qwen-image")
        s = blocks[fam] | ({"ignore": fx["ignore"]} if "ignore" in fx else {})
        got = normalise(fx["name"], fx["tensors"], s, fam)
        got["family"] = fam if got["error"] is None else "unknown"
        got["licence"] = lora_licence(fx.get("metadata", {}))
        bad = [k for k in fx.get("values", {}) if k not in fx["tensors"]]
        if bad or got != fx["expect"]:
            fail(f"{name}: got {json.dumps(got)}, want {json.dumps(fx['expect'])}" + (f"; values for no tensor {bad}" if bad else ""))
        else:
            print(f"ok   {name}")


def detect_family(name: str, tensors: dict, families: dict[str, tuple[list[str], dict]]) -> str:
    """The family (family → (its envs, its lora block)) whose block the file fits: one a non-fake env publishes
    before one only env fake publishes, then by name; `unknown` when none fits."""
    order = sorted(families, key=lambda f: (all(e == "fake" for e in families[f][0]), f))
    return next((f for f in order if normalise(name, tensors, families[f][1], f)["error"] is None), "unknown")


def check_lora_detection() -> None:
    """fixtures/loras/detect/families.json: family detection over the published families of each case."""
    fx = json.loads((HERE / "fixtures" / "loras" / "detect" / "families.json").read_text())
    blocks = {f: (v["envs"], json.loads((HERE / "fixtures" / v["block"]).read_text())["models"][0]["lora"])
              for f, v in fx["families"].items()}
    for c in fx["cases"]:
        lora = json.loads((HERE / "fixtures" / "loras" / c["lora"]).read_text())
        got = detect_family(lora["name"], lora["tensors"], {f: blocks[f] for f in c["families"]})
        if got != c["family"]:
            fail(f"fixtures/loras/detect/families.json: {c['lora']} over {c['families']}: got {got}, want {c['family']}")
    print("ok   fixtures/loras/detect/families.json")
