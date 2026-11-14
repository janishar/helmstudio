"""The qwen_image pack against the old studio's goldens (tests/golden/qwen-image at the repo root): runs the pack's
qwen-image workflow in-process (no NATS: graph.run with a real residency manager on the GPU) with a golden's
settings and compares the PNG with tests/golden/compare.py. Loads real weights: never alongside another process
that holds a model.

    uv run --extra torch python tests/test_qwen_golden.py [case ...] [--checks] [--out DIR]
    uv run --extra torch pytest -m gpu -s tests/test_qwen_golden.py

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
            self.ran.append(ctx.node)
            return fn(ctx, params, inputs)

        return run

    def nodes(self, case: dict) -> dict:
        """The workflow with a golden's settings."""
        nodes = json.loads(json.dumps(self.workflow["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        nodes["canvas"]["params"] |= {"width": case["width"], "height": case["height"]}
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


def case(bench: Bench, name: str, out_dir: Path | None = None) -> dict:
    settings = json.loads((GOLDEN / "qwen-image" / f"{name}.json").read_text())
    t = time.monotonic()
    r = bench.run(bench.nodes(settings))
    r["total_s"] = round(time.monotonic() - t, 1)
    r["psnr"], r["compare"] = compare(GOLDEN / "qwen-image" / f"{name}.png", r["png"])
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


@pytest.mark.gpu
def test_turbo_768_matches_the_golden(bench):
    r = case(bench, CASES[0])
    print(r["compare"], r["s"], r["peak_gb"])
    assert r["psnr"] >= 30


@pytest.mark.gpu
def test_seed_change_cache_cancel_and_unload(bench):
    case(bench, CASES[0])  # warm, and the cache holds encode
    c = checks(bench)
    print(c)
    assert c["reseed_ran"] == ["seed", "sample", "decode", "output"]
    assert c["cancel_steps_after_request"] in ([], [3])  # at most the step that was running
    assert c["resident_after_unload"] == []
    assert c["after_unload_gb"] < 1.0


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=CASES[:1], choices=CASES)
    ap.add_argument("--checks", action="store_true")
    ap.add_argument("--out", type=Path)
    args = ap.parse_args()
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    b = Bench()
    failed = False
    for name in args.cases:
        r = case(b, name, args.out)
        failed |= r["psnr"] < 30
        print(name, r["compare"], "s:", r["s"], "total:", r["total_s"], "peak_gb:", r["peak_gb"],
              "resident:", [(e["component"], e["variant"], e["gb"]) for e in b.residency.state()["resident"]],
              flush=True)  # fmt: skip
    if args.checks:
        print("checks:", checks(b), flush=True)
    b.residency.unload()
    sys.exit(1 if failed else 0)
