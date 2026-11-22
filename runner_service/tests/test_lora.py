"""core/lora.py: the normaliser against the contract's LoRA fixtures and a property test per key style (no key is
silently dropped), the file checks, and the host library (shared index, own-LoRA cache)."""

import hashlib
import json
import random
from pathlib import Path

import pytest
from conftest import CONTRACTS, FakeStore, safetensors_file

from core import lora, registry
from core.graph import RunError
from core.pack import LoraUse

FIXTURES = CONTRACTS / "fixtures/loras"
MODELS = json.loads((CONTRACTS / "fixtures/registry/qwen-image.models.valid.json").read_text())
BLOCK, FAMILY = MODELS["models"][0]["lora"], MODELS["models"][0]["family"]
LTX_BLOCK = json.loads((CONTRACTS / "fixtures/registry/ltx.models.valid.json").read_text())[
    "models"
][0]["lora"]
BLOCKS = {FAMILY: BLOCK, "ltx": LTX_BLOCK}


@pytest.mark.parametrize("path", sorted(FIXTURES.glob("*.json")), ids=lambda p: p.stem)
def test_contract_lora_fixtures(path):
    fx = json.loads(path.read_text())
    family = fx.get("family", FAMILY)  # the block a fixture is checked against (default qwen-image)
    block = BLOCKS[family] | ({"ignore": fx["ignore"]} if "ignore" in fx else {})
    got = lora.normalise(fx["name"], fx["tensors"], block, family)
    got.pop("groups")
    got["family"] = family if got["error"] is None else "unknown"
    got["licence"] = lora.licence(fx.get("metadata", {}))
    assert got == fx["expect"]


def test_compress_is_the_inverse_of_expand():
    names = lora.expand(BLOCK["modules"])
    assert lora.compress(names) == BLOCK["modules"]
    odd = ["a.0.x", "a.2.x", "b.1", "c"]  # not contiguous, one alone: kept as they are
    assert lora.compress(odd) == ["a.0.x", "a.2.x", "b.1", "c"]
    assert lora.expand(lora.compress(odd)) == odd


# --- the property: a LoRA made from the model's own module names applies 100 %; any key removed, renamed or added
# is reported, never dropped

SHAPES = {m: (64, 96) for m in lora.expand(BLOCK["modules"])}


def header(style: str, modules, rank=4, alpha=True) -> dict:
    out = {}
    for m in modules:
        if style == "kohya":
            base, a, b = "lora_unet_" + m.replace(".", "_"), ".lora_down.weight", ".lora_up.weight"
        else:
            base = ("diffusion_model." if style == "comfyui" else "transformer.") + m
            a, b = ".lora_A.weight", ".lora_B.weight"
        fin, fout = SHAPES[m]
        out[base + a] = {"dtype": "BF16", "shape": [rank, fin]}
        out[base + b] = {"dtype": "BF16", "shape": [fout, rank]}
        if alpha:
            out[base + ".alpha"] = {"dtype": "F32", "shape": []}
    return out


@pytest.mark.parametrize("style", ["peft", "kohya", "comfyui"])
def test_every_key_of_a_model_lora_applies(style):
    rng = random.Random(style)
    mods = rng.sample(sorted(SHAPES), 40)
    h = header(style, mods)
    res = lora.normalise("x", h, BLOCK, FAMILY, SHAPES)
    assert (res["key_style"], res["applied"], res["total"], res["error"]) == (
        style,
        len(h),
        len(h),
        None,
    )
    assert set(res["groups"]) == set(mods)  # canonical module names, whatever the style

    for _ in range(20):
        k = rng.choice(sorted(h))
        removed = {x: v for x, v in h.items() if x != k}
        renamed = removed | {k.replace("attn", "atn") if "attn" in k else "x" + k: h[k]}
        added = h | {k.rsplit(".", 2)[0] + ".lora_mid.weight": h[k]}
        # an alpha is optional: without it the file is another valid LoRA (s = strength), not a dropped key
        for broken in (renamed, added) if k.endswith(".alpha") else (removed, renamed, added):
            res = lora.normalise("x", broken, BLOCK, FAMILY, SHAPES)
            assert res["error"] and res["unapplied"], (k, res)
            assert res["applied"] + res["ignored"] + len(res["unapplied"]) == res["total"]


def comfy_name(module: str) -> str:
    """An LTX module's name in a ComfyUI file: the official key's part, as the old renaming map undid it."""
    r = next(
        (r for r in LTX_BLOCK["renames"] if module.endswith(r["to"])), None
    )  # audio_ff's come before ff's
    return module[: -len(r["to"])] + r["from"] if r else module


def test_every_key_of_an_ltx_lora_applies_and_none_is_dropped():
    """The property on the LTX transformer's own module names (the canonical block, which test_mlx checks against
    the model built from the header): a ComfyUI file over any of them applies 100 % through the `at: end` renames,
    and a key removed, renamed or added is reported, never dropped (the old studio ignored unmatched keys)."""
    rng = random.Random("ltx")
    mods = lora.expand(LTX_BLOCK["modules"])
    shapes = dict.fromkeys(mods, (64, 96))
    pick = (
        rng.sample(mods, 60)
        + [m for m in mods if m.endswith(("to_out", "proj_in", "proj_out", "linear1"))][:12]
    )
    h = {}
    for m in pick:
        base = "diffusion_model." + comfy_name(m)
        h[base + ".lora_A.weight"] = {"dtype": "BF16", "shape": [4, 64]}
        h[base + ".lora_B.weight"] = {"dtype": "BF16", "shape": [96, 4]}
    res = lora.normalise("x", h, LTX_BLOCK, "ltx", shapes)
    assert (res["key_style"], res["applied"], res["error"]) == ("comfyui", len(h), None)
    assert set(res["groups"]) == set(pick)
    for _ in range(20):
        k = rng.choice(sorted(h))
        removed = {x: v for x, v in h.items() if x != k}
        renamed = removed | {
            k.replace("transformer_blocks.", "blocks.") if "blocks." in k else "x" + k: h[k]
        }
        added = h | {k.rsplit(".", 2)[0] + ".lora_mid.weight": h[k]}
        for broken in (removed, renamed, added):
            res = lora.normalise("x", broken, LTX_BLOCK, "ltx", shapes)
            assert res["error"] and res["unapplied"], k
            assert res["applied"] + res["ignored"] + len(res["unapplied"]) == res["total"]
    kohya = {f"lora_unet_{k.removeprefix('diffusion_model.').removesuffix(suf).replace('.', '_')}{to}": v
             for k, v in h.items() for suf, to in ((".lora_A.weight", ".lora_down.weight"),
                                                  (".lora_B.weight", ".lora_up.weight")) if k.endswith(suf)}  # fmt: skip
    res = lora.normalise(
        "k", kohya, LTX_BLOCK, "ltx", shapes
    )  # `_0` / `net_2` parts match no rename: refused
    assert (
        res["key_style"] == "kohya"
        and res["error"]
        and any("to_out_0" in k for k in res["unapplied"])
    )


def test_a_shape_that_does_not_fit_the_linear_is_unapplied():
    h = header("peft", ["img_in"])
    res = lora.normalise("x", h, BLOCK, FAMILY, {"img_in": (64, 95)})
    assert res["applied"] == 0 and "0 of 3 keys apply to qwen-image (peft keys)" in res["error"]


# --- the file checks (contracts "LoRA upload checks")


def write(path, raw: bytes):
    path.write_bytes(raw)
    return path


@pytest.mark.parametrize(
    ("tensors", "reason"),
    [
        ({"transformer.img_in.lora_A.weight": ("I8", [4, 8])},
         "tensor transformer.img_in.lora_A.weight has dtype I8; LoRAs must be F32, F16 or BF16"),
        ({"model.weight": ("BF16", [4, 8])}, "no LoRA keys found (is it a full model?)"),
        ({"transformer.img_in.lora_A.weight": ("BF16", [4, 8]), "extra": ("BF16", [1])},
         "extra is not a LoRA weight"),
        ({"transformer.img_in.lora_A.weight": ("BF16", [4, 8])}, "img_in: lora_B is missing"),
        ({"transformer.img_in.lora_A.weight": ("BF16", [4, 8]),
          "transformer.img_in.lora_B.weight": ("BF16", [8, 2])}, "img_in: lora_A and lora_B ranks differ"),
    ],
)  # fmt: skip
def test_file_checks(tmp_path, tensors, reason):
    safetensors_file(tmp_path / "x.safetensors", tensors)
    with pytest.raises(lora.LoraFileError) as e:
        lora.read_header(tmp_path / "x.safetensors")
    assert str(e.value) == reason


def test_file_checks_refuse_pickles_and_non_safetensors(tmp_path):
    for raw, reason in [
        (b"PK\x03\x04rest", "pickle checkpoints are not accepted; convert it to safetensors"),
        (b"\x80\x02rest....", "pickle checkpoints are not accepted; convert it to safetensors"),
        ((500).to_bytes(8, "little") + b"{}", "not a safetensors file"),
        ((4).to_bytes(8, "little") + b"[1] ", "not a safetensors file"),
    ]:
        with pytest.raises(lora.LoraFileError, match=reason):
            lora.read_header(write(tmp_path / "x.safetensors", raw))


# --- the host library


def test_shared_index_is_on_contract_and_hashes_each_file_once(tmp_path, monkeypatch):
    fx = json.loads((FIXTURES / "comfyui.json").read_text())
    models, cache = tmp_path / "models", tmp_path / "cache"
    shapes = {k: (t["dtype"], t["shape"]) for k, t in fx["tensors"].items()}
    safetensors_file(models / "loras" / "ink-wash.safetensors", shapes)
    safetensors_file(models / "Qwen-Image-2.1" / "loras" / "ink-wash-2.safetensors", shapes | {
        "diffusion_model.img_in.lora_A.weight": ("BF16", [4, 64]),
        "diffusion_model.img_in.lora_B.weight": ("BF16", [3072, 4])})  # fmt: skip
    safetensors_file(models / "loras" / "full-model.safetensors", {"w.weight": ("BF16", [2, 2])})
    (models / "loras" / "deep").mkdir()
    safetensors_file(models / "loras" / "deep" / "below.safetensors", shapes)  # not recursive
    hashed = []
    real = lora.sha256_file
    monkeypatch.setattr(lora, "sha256_file", lambda p: hashed.append(p.name) or real(p))
    lib = lora.Library(models, cache)
    entries = lib.scan({FAMILY: BLOCK, "zz-other": BLOCK | {"modules": ["nothing"]}})
    assert sorted(e["name"] for e in entries) == ["ink-wash", "ink-wash-2"]
    one = next(e for e in entries if e["name"] == "ink-wash")
    data = (models / "loras" / "ink-wash.safetensors").read_bytes()
    assert one["id"] == hashlib.sha256(data).hexdigest() and one["bytes"] == len(data)
    assert (one["family"], one["key_style"], one["rank"]) == (
        FAMILY,
        "comfyui",
        fx["expect"]["rank"],
    )
    assert registry.scanned_value("loras", entries)["loras"] == entries  # on contract
    assert lora.Library(models, cache).scan({FAMILY: BLOCK}) == entries
    assert sorted(hashed) == [
        "ink-wash-2.safetensors",
        "ink-wash.safetensors",
    ]  # the restart rehashed nothing


def use(**kw) -> LoraUse:
    data = b"lora-bytes"
    lora_id = "2c4e6a8b-1d3f-4a5b-9c7d-0e2f4a6b8c10"
    base = {"lora_id": lora_id, "source": "own", "name": "mine", "sha256": hashlib.sha256(data).hexdigest(),
            "bytes": len(data), "strength": 1.0, "mode": "adapter", "key": f"u/o1/loras/{lora_id}.safetensors"}  # fmt: skip
    return LoraUse(**(base | kw))


async def test_own_lora_is_fetched_once_checked_and_cached(tmp_path):
    store = FakeStore()
    u = use()
    store.objects[u.key] = (b"lora-bytes", "application/octet-stream")
    lib, told = lora.Library(tmp_path, tmp_path / "cache", store=store, host="mac"), []

    async def progress(phase):
        told.append(phase)

    p = await lib.path(u, "o1", progress)
    assert p.read_bytes() == b"lora-bytes" and p.name == f"{u.sha256}.safetensors"
    assert await lib.path(u, "o1", progress) == p
    assert store.reads == [u.key] and told == [
        "fetching LoRA mine"
    ]  # the second use is a cache hit
    with pytest.raises(RunError, match="LoRA mine is not the run owner's"):
        await lib.path(u, "o2")
    store.objects[u.key] = (b"other-bytes", "x")
    with pytest.raises(RunError, match="LoRA mine is gone"):
        await lib.path(use(sha256="0" * 64, bytes=11), "o1")
    with pytest.raises(RunError, match="LoRA ink is not on host mac"):
        await lib.path(use(source="shared", name="ink", key=None), "o1")


def test_own_lora_cache_drops_the_least_recently_used(tmp_path):
    lib = lora.Library(tmp_path, tmp_path / "cache", cache_gb=25 / 2**30)
    lib.dir.mkdir(parents=True)
    for i, name in enumerate("abc"):
        f = lib.dir / f"{name}.safetensors"
        f.write_bytes(b"x" * 10)
        import os

        os.utime(f, (i, i))
    lib._evict(10)  # 30 bytes held, cap 25: a (oldest) and b go
    assert sorted(p.stem for p in lib.dir.glob("*.safetensors")) == ["c"]


def test_alpha_only_or_deeply_nested_files_are_refused_and_never_stop_the_scan(tmp_path):
    models = tmp_path / "models"
    safetensors_file(
        models / "loras" / "alpha.safetensors", {"transformer.img_in.alpha": ("BF16", [])}
    )
    nested = b"[" * 200000
    write(models / "loras" / "nested.safetensors", len(nested).to_bytes(8, "little") + nested)
    with pytest.raises(lora.LoraFileError, match=r"no LoRA keys found \(is it a full model\?\)"):
        lora.read_header(models / "loras" / "alpha.safetensors")
    with pytest.raises(lora.LoraFileError, match="not a safetensors file"):
        lora.read_header(models / "loras" / "nested.safetensors")
    assert lora.Library(models, tmp_path / "cache").scan({FAMILY: BLOCK}) == []


def test_an_ic_lora_is_left_out_of_the_index_but_still_reads(tmp_path, caplog):
    """plan 19 23.4 phase 3: a file whose metadata has reference_downscale_factor (Lightricks' IC-LoRAs) is no stack
    LoRA; the header still reads (ltx/ic-lora loads it as a component)."""
    fx = json.loads((FIXTURES / "ltx-comfyui.json").read_text())
    models = tmp_path / "models"
    shapes = {k: (t["dtype"], t["shape"]) for k, t in fx["tensors"].items()}
    safetensors_file(models / "ltx-2.5" / "loras" / "style.safetensors", shapes)
    ic = models / "ltx-2.5" / "loras" / "ic.safetensors"
    safetensors_file(ic, shapes)
    raw = ic.read_bytes()
    n = int.from_bytes(raw[:8], "little")
    header = json.loads(raw[8 : 8 + n]) | {"__metadata__": {"reference_downscale_factor": "2"}}
    blob = json.dumps(header).encode()
    ic.write_bytes(len(blob).to_bytes(8, "little") + blob + raw[8 + n :])
    entries = lora.Library(models, tmp_path / "cache").scan(BLOCKS)
    assert [(e["name"], e["family"]) for e in entries] == [("style", "ltx")]
    assert (
        f"{ic} left out: an IC-LoRA (it needs a reference video), not a stack LoRA" in caplog.text
    )
    assert lora.read_header(ic)[1] == {"reference_downscale_factor": "2"}


async def test_a_failed_fetch_leaves_no_part_file_and_stale_ones_are_dropped(tmp_path):
    class Broken(FakeStore):
        async def download(self, key, dest, limit):
            Path(dest).write_bytes(b"half")
            raise ConnectionResetError

    lib = lora.Library(tmp_path, tmp_path / "cache", store=Broken())
    with pytest.raises(ConnectionResetError):
        await lib.path(use(), "o1")
    assert list(lib.dir.iterdir()) == []
    (lib.dir / f"{'0' * 64}.part999").write_bytes(b"x" * 100)  # a runner that died mid-fetch
    store = FakeStore()
    store.objects[use().key] = (b"lora-bytes", "application/octet-stream")
    lib.store = store
    await lib.path(use(), "o1")
    assert [p.name for p in lib.dir.iterdir()] == [f"{use().sha256}.safetensors"]
