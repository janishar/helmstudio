"""The qwen_image pack against the old studio's goldens (tests/golden/qwen-image at the repo root): runs the pack's
qwen-image workflow in-process (no NATS: graph.run with a real residency manager on the GPU) with a golden's
settings and compares the PNG with tests/golden/compare.py. Loads real weights: never alongside another process
that holds a model.

    uv run --extra torch python tests/test_qwen_golden.py [case ...] [--checks] [--out DIR]
    uv run --extra torch python tests/test_qwen_golden.py edit-e1 edit-e2 edit-e3     # the edit goldens
    uv run --extra torch pytest -m gpu -s tests/test_qwen_golden.py

An edit case (tests/golden/qwen-image-edit) feeds its references through the core's image path: each ref file is a
resolved Image value under the owner "golden", read by ctx.fetch from the golden folder and decoded by core.images.

--checks (after the cases): a second run with only the seed changed must not re-run encode; a cancel mid-sampling
must stop within one step; after-run policy "unload" must give the memory back."""

import argparse
import asyncio
import dataclasses
import json
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

import pytest

GOLDEN = Path(__file__).resolve().parents[2] / "tests" / "golden"
CASES = ["turbo-768-s42", "turbo-1024-s7", "turbo-1344x768-s123", "base-768-s42"]
EDITS = [
    "edit-e1",
    "edit-e2",
    "edit-e3",
]  # turbo int8; e1/e2 auto size (1 and 2 refs), e3 a fixed 1024²


def folder(name: str) -> Path:
    return GOLDEN / ("qwen-image-edit" if name.startswith("edit-") else "qwen-image")


def ref_value(n: int, file: str) -> dict:
    """A resolved Image value (defs image) for a golden file (path relative to tests/golden)."""
    import hashlib

    from PIL import Image

    data = (GOLDEN / file).read_bytes()
    with Image.open(GOLDEN / file) as im:
        w, h = im.size
    return {"take_id": f"00000000-0000-4000-8000-{n:012d}", "key": f"u/golden/{file}",
            "content_type": "image/png", "width": w, "height": h, "bytes": len(data),
            "etag": hashlib.sha256(data).hexdigest()[:32]}  # fmt: skip


async def fetch(key: str, limit: int, etag: str | None = None) -> bytes | None:
    """ctx.fetch for the goldens: u/golden/<file> is tests/golden/<file>."""
    path = GOLDEN / key.removeprefix("u/golden/")
    return path.read_bytes()[: limit + 1] if path.is_file() else None


class Bench:
    """The registry, node implementations (recording which nodes ran), cache and residency of one runner."""

    def __init__(self) -> None:
        from core import graph, registry
        from core import loaders_torch as lt
        from core.residency import Residency, ram_gb

        self.lt, self.graph = lt, graph
        values, impls, packs = registry.build("torch")
        self.schemas = registry.node_schemas(values)
        self.workflow = next(w for p in packs for w in p.workflows if w["id"] == "qwen-image")
        self.ran: list[str] = []
        self.impls = {k: dataclasses.replace(n, fn=self._record(k, n.fn)) for k, n in impls.items()}
        self.residency = Residency(ram_gb() - 12, free=lt.free, measure=lt.measure_gb)
        self.cache = graph.Cache()
        self.residency.on_short.append(self.cache.clear)

    def _record(self, kind: str, fn):
        def run(ctx, params, inputs):
            if ctx is not None:  # graph.keys calls a model node with no ctx for its fingerprint
                self.ran.append(ctx.node)
            return fn(ctx, params, inputs)

        return run

    def nodes(self, case: dict) -> dict:
        """The workflow with a golden's settings."""
        nodes = json.loads(json.dumps(self.workflow["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        if (
            case["width"] and case["height"]
        ):  # an edit without a size is sized from its last reference
            nodes["canvas"]["params"] |= {"width": case["width"], "height": case["height"]}
            nodes["canvas"]["params"]["mode"] = "fixed" if case.get("refs") else "auto"
        refs = case.get("refs", [])
        nodes["encode"]["params"]["refs"] = [
            {"image": ref_value(i, r["file"])} for i, r in enumerate(refs, 1)
        ]
        nodes["model"]["params"] |= {"variant": case["variant"], "precision": case["precision"]}
        nodes["encode"]["params"]["transparent"] = case["transparent"]
        nodes["sample"]["params"]["steps"] = case["steps_arg"]
        nodes["decode"]["params"]["tiled"] = "on" if case["tiled"] else "auto"
        return nodes

    def run(self, nodes: dict, *, cancel_after_step: int | None = None, cancel_delay: float = 0):
        """Run one graph. Returns {png, ran, s (per node), peak_gb (per node), steps, cancelled}."""
        g, lt = self.graph, self.lt
        assert not g.validate(nodes, self.schemas)
        nodes, _ = g.resolve(nodes, self.schemas)
        self.ran = []
        out = {"png": None, "s": {}, "peak_gb": {}, "steps": [], "cancelled": None}
        flag: dict = {}

        async def progress(node, step, of, phase, eta):
            if phase == "sampling":
                out["steps"].append((step, time.monotonic()))
                if step == cancel_after_step and "at" not in flag:
                    threading.Timer(
                        cancel_delay, lambda: flag.setdefault("at", time.monotonic())
                    ).start()

        async def output(index, data, **meta):
            out["png"] = data

        def check():
            if "at" in flag:
                raise g.Cancelled

        ctx = g.Ctx(progress=progress, output=output, check=check, owner="golden",
                    residency=self.residency, device=lt.device())  # fmt: skip
        ctx.fetch = fetch
        stop, last = threading.Event(), [None, time.monotonic()]

        def sample_memory():  # ponytail: 50 ms sampling misses shorter spikes
            while not stop.wait(0.05):
                node, gb = ctx.node, lt.measure_gb()
                out["peak_gb"][node] = max(out["peak_gb"].get(node, 0), gb)
                if node != last[0]:
                    if last[0]:
                        out["s"][last[0]] = round(time.monotonic() - last[1], 1)
                    last[:] = [node, time.monotonic()]

        sampler = threading.Thread(target=sample_memory)
        sampler.start()
        try:
            asyncio.run(g.run(nodes, self.impls, self.schemas, ctx, self.cache))
        except g.Cancelled:
            out["cancelled"] = flag["at"]
        finally:
            stop.set()
            sampler.join()
            if last[0]:
                out["s"][last[0]] = round(time.monotonic() - last[1], 1)
            self.residency.done()
        out["ran"] = list(self.ran)
        out["peak_gb"] = {k: round(v, 2) for k, v in out["peak_gb"].items()}
        return out


def compare(golden: Path, png: bytes) -> tuple[float, str]:
    """(PSNR in dB, compare.py's line) of a run's PNG against a golden."""
    with tempfile.NamedTemporaryFile(suffix=".png") as f:
        f.write(png)
        f.flush()
        r = subprocess.run([sys.executable, str(GOLDEN / "compare.py"), str(golden), f.name],
                           check=False, capture_output=True, text=True)  # fmt: skip
    line = r.stdout.strip()
    return float(line.split()[0].split("=")[1]), line


# CUDA goldens (<case>.cuda.png, recorded 2026-11-16 on an RTX PRO 6000 Blackwell, driver 595.91, torch 2.15 + CUDA
# 13.2): bit-identical across processes and through the runner, so their bar is tight; another GPU, driver or torch
# may need them recorded again (packs/qwen_image/GOTCHAS.md). The MPS goldens keep 30 dB on every device (CUDA: 31-49).
CUDA_BAR = 50


def case(bench: Bench, name: str, out_dir: Path | None = None) -> dict:
    settings = json.loads((folder(name) / f"{name}.json").read_text())
    t = time.monotonic()
    r = bench.run(bench.nodes(settings))
    r["total_s"] = round(time.monotonic() - t, 1)
    r["psnr"], r["compare"] = compare(folder(name) / f"{name}.png", r["png"])
    if bench.lt.device() == "cuda" and (cuda := folder(name) / f"{name}.cuda.png").is_file():
        r["psnr_cuda"], cuda_line = compare(cuda, r["png"])
        r["compare"] += f" | cuda golden {cuda_line}"
    if out_dir:
        (out_dir / f"{name}.png").write_bytes(r["png"])
    return r


def checks(bench: Bench) -> dict:
    """Cache hit on a seed change, cancel within one step, memory back after unload (after case 1 ran)."""
    lt, res = bench.lt, bench.residency
    settings = json.loads((GOLDEN / "qwen-image" / f"{CASES[0]}.json").read_text())
    nodes = bench.nodes(settings)
    nodes["seed"]["params"]["seed"] = 43
    reseed = bench.run(nodes)
    nodes["seed"]["params"]["seed"] = 44
    cancel = bench.run(nodes, cancel_after_step=2, cancel_delay=0.5)
    after_cancel = [
        s for s, at in cancel["steps"] if cancel["cancelled"] and at > cancel["cancelled"]
    ]
    step_s = cancel["steps"][1][1] - cancel["steps"][0][1]
    loaded = lt.measure_gb()
    res.after_run("unload")
    return {
        "reseed_ran": reseed["ran"],
        "cancel_steps_done": [s for s, _ in cancel["steps"]],
        "cancel_steps_after_request": after_cancel,
        "step_s": round(step_s, 2),
        "loaded_gb": round(loaded, 2),
        "after_unload_gb": round(lt.measure_gb(), 2),
        "resident_after_unload": res.state()["resident"],
    }


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "Qwen-Image-2.1-Turbo").is_dir():
        pytest.skip("Qwen-Image-2.1-Turbo is not in the models folder")
    b = Bench()
    yield b
    b.residency.unload()


def test_empty_prompt_fails_before_any_load():
    pytest.importorskip("torch")
    from core.graph import RunError
    from packs.qwen_image import torch as qwen

    with pytest.raises(RunError, match="empty prompt"):
        qwen.encode(None, {"transparent": True}, {"prompt": " \n", "model": None})


def test_auto_tiles_only_above_2_5_mp():
    pytest.importorskip("torch")
    from packs.qwen_image.torch import tiles

    assert not tiles("auto", 1344, 768) and not tiles("auto", 1536, 1536)
    assert tiles("auto", 2048, 2048) and tiles("on", 256, 256) and not tiles("off", 4096, 4096)


def test_turbo_processor_gives_the_same_ids_as_base():
    """encode reads base's processor for both variants (one cache identity); Turbo's copy must tokenize alike."""
    pytest.importorskip("torch")
    from core import pack
    from packs.qwen_image.torch import TEMPLATE, processor

    root = pack.models_root()
    if not (root / "Qwen-Image-2.1-Turbo/processor").is_dir():
        pytest.skip("the Qwen-Image-2.1 folders are not there")
    base, turbo = processor(root / "Qwen-Image-2.1"), processor(root / "Qwen-Image-2.1-Turbo")
    assert base[1] == turbo[1]
    for text in ['A neon sign that reads "QWEN"', "  héllo\n\twörld 123 ", "日本語のテキスト 🦊"]:
        ids = [p(text=[TEMPLATE.format(text)]).input_ids for p, _ in (base, turbo)]
        assert ids[0] == ids[1]


def edit_goldens() -> list[dict]:
    return [
        json.loads(p.read_text()) for p in sorted((GOLDEN / "qwen-image-edit").glob("edit-*.json"))
    ]


def test_edit_prompt_template_for_0_1_2_references():
    pytest.importorskip("torch")
    from packs.qwen_image.torch import TEMPLATE, prompt_text

    slot = "<image{}><|vision_start|><|image_pad|><|vision_end|>"
    assert prompt_text("a fox", 0) == TEMPLATE.format("a fox")  # text-to-image: unchanged
    assert prompt_text("edit <image1>", 1) == TEMPLATE.format(slot.format(1) + "edit <image1>")
    two = prompt_text("x", 2)
    assert two == TEMPLATE.format(
        f"{slot.format(1)} {slot.format(2)}x"
    )  # pipeline_qwenimage21.py:259-261
    assert two.count("<|image_pad|>") == 2


def test_edit_references_are_prepared_like_the_pipeline():
    pytest.importorskip("torch")
    from diffusers.image_processor import VaeImageProcessor
    from diffusers.pipelines.qwenimage21.pipeline_qwenimage21 import calculate_dimensions
    from PIL import Image

    from packs.qwen_image.torch import output_size, reference

    pre = VaeImageProcessor(vae_scale_factor=16, vae_latent_channels=64)
    img = Image.new("RGBA", (300, 150), (255, 0, 0, 0))  # fully transparent red
    seen, pixels = reference(img, pre)
    w, h, _ = calculate_dimensions(1024 * 1024, 2.0)
    assert seen.size == (w, h) and seen.mode == "RGB" and seen.getpixel((5, 5)) == (255, 255, 255)
    assert tuple(pixels.shape) == (1, 4, 1, h, w) and pixels.min() >= -1 and pixels.max() <= 1
    assert reference(Image.new("P", (64, 64)), pre)[1].shape[1] == 4  # any mode → RGBA for the VAE
    canvas = {"mode": "auto", "width": 1000, "height": 520}
    assert output_size(canvas, []) == (992, 512)  # text-to-image: width × height, floored to 32
    assert output_size(canvas, [Image.new("RGB", (10, 10)), img]) == (w, h)  # the last reference
    assert output_size(canvas | {"mode": "fixed"}, [img]) == (992, 512)


def test_edit_noise_seed_and_size_match_the_goldens():
    """No weights: the core's decode of each golden ref and the pack's noise seed and size rule give what the old
    studio recorded."""
    pytest.importorskip("torch")
    from core import images
    from packs.qwen_image.torch import noise_seed, output_size

    cases = edit_goldens()
    if not cases:
        pytest.skip("no edit goldens in tests/golden/qwen-image-edit yet")
    for c in cases:
        refs = [images.decode((GOLDEN / r["file"]).read_bytes(), "t") for r in c["refs"]]
        assert [[im.mode, list(im.size)] for im in refs] == [
            [r["mode"], r["size"]] for r in c["refs"]
        ]
        assert noise_seed(c["seed"], refs) == c["noise_seed"]
        mode = "fixed" if c["width"] and c["height"] else "auto"
        canvas = {"mode": mode, "width": c["width"] or 1024, "height": c["height"] or 1024}
        assert list(output_size(canvas, refs)) == c["output_size_actual"]


@pytest.mark.gpu
@pytest.mark.parametrize("name", EDITS)
def test_edit_matches_the_golden(bench, name):
    if not (folder(name) / f"{name}.png").is_file():
        pytest.skip(f"{name} is not recorded yet")
    r = case(bench, name)
    print(name, r["compare"], r["s"], r["peak_gb"])
    assert r["psnr"] >= 30 and r.get("psnr_cuda", CUDA_BAR) >= CUDA_BAR


@pytest.mark.gpu
def test_turbo_768_matches_the_golden(bench):
    r = case(bench, CASES[0])
    print(r["compare"], r["s"], r["peak_gb"])
    assert r["psnr"] >= 30 and r.get("psnr_cuda", CUDA_BAR) >= CUDA_BAR


@pytest.mark.gpu
def test_seed_change_cache_cancel_and_unload(bench):
    case(bench, CASES[0])  # warm, and the cache holds encode
    c = checks(bench)
    print(c)
    # nodes with a Model output (model, lora) always run (graph.run never caches them); encode is the cache hit
    assert "encode" not in c["reseed_ran"] and c["reseed_ran"][-3:] == [
        "sample",
        "decode",
        "output",
    ]
    # at most the step that was running (MPS: step 3; CUDA's 0.15 s steps run on through the 0.5 s delay)
    assert len(c["cancel_steps_after_request"]) <= 1
    assert c["resident_after_unload"] == []
    # CUDA: measure_gb counts the context (the device's used memory at start, other processes' too)
    context = bench.lt.context_gb() if bench.lt.device() == "cuda" else 0
    assert c["after_unload_gb"] - context < 1.0


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=CASES[:1], choices=CASES + EDITS)
    ap.add_argument("--checks", action="store_true")
    ap.add_argument("--out", type=Path)
    args = ap.parse_args()
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    b = Bench()
    failed = False
    for name in args.cases:
        r = case(b, name, args.out)
        failed |= r["psnr"] < 30 or r.get("psnr_cuda", CUDA_BAR) < CUDA_BAR
        print(name, r["compare"], "s:", r["s"], "total:", r["total_s"], "peak_gb:", r["peak_gb"],
              "resident:", [(e["component"], e["variant"], e["gb"]) for e in b.residency.state()["resident"]],
              flush=True)  # fmt: skip
    if args.checks:
        print("checks:", checks(b), flush=True)
    b.residency.unload()
    sys.exit(1 if failed else 0)
