"""The minimax_h3_image pack against the old studio's goldens (runner_service/tests/golden/minimax-h3-image-*,
recorded by tests/golden/record_minimax.py on torch 2.14.1 + transformers 5.22.2): runs the pack's minimax-h3-image
workflow in-process (graph.run with a real residency manager on the GPU, test_qwen_golden.Bench) with a golden's
settings and compares the PNG with tests/golden/compare.py (bar 30 dB across stacks), or the enhancer's text. Loads
real weights (25-31 GB each, never two at a time): never alongside another process that holds a model.

    uv run --extra torch python tests/test_minimax_golden.py [t2i-1024-s42 edit-1024-s42 enhance-t2i-s42] [--checks]
    uv run --extra torch pytest -m gpu -s tests/test_minimax_golden.py

--checks (after the cases): a seed change re-runs sample and decode, not encode; the transformer + VAE stay resident
and the text encoder is not (unload_after).

Measured 2026-11-19 (M-series 64 GB, torch 2.15 MPS, int8; phys_footprint peaks per node, 50 ms sampling): see
packs/minimax_h3_image/GOTCHAS.md."""

import argparse
import json
import sys
import time
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))

from test_qwen_golden import Bench, compare, ref_value

GOLDEN = HERE / "golden"
CASES = {"t2i-1024-s42": "minimax-h3-image-t2i", "edit-1024-s42": "minimax-h3-image-edit",
         "enhance-t2i-s42": "minimax-h3-image-enhance"}  # fmt: skip
BAR = 30
# the repo's stack: <case>.torch-2.15.{png,txt} is the old studio's own code (record_minimax.py) run on it, which the
# pack matches byte for byte (the old stack's goldens: t2i 35.1 dB, edit bit-exact, enhance another text)
STACK = "torch-2.15"


def settings(name: str) -> dict:
    return json.loads((GOLDEN / CASES[name] / f"{name}.json").read_text())


class MiniMaxBench(Bench):
    def __init__(self) -> None:
        super().__init__()
        self.workflow = next(
            w for p in self.packs for w in p.workflows if w["id"] == "minimax-h3-image"
        )

    def nodes(self, case: dict) -> dict:
        """The workflow with a golden's settings: --mp 1 (1024 × 1024, rendered on the 4 MP canvas)."""
        nodes = json.loads(json.dumps(self.workflow["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        side = round(1024 * case.get("mp", 4) ** 0.5)
        nodes["canvas"]["params"] |= {"width": side, "height": side, "mode": "auto"}
        nodes["encode"]["params"]["refs"] = [
            {"image": ref_value(i, r["source"].removeprefix("tests/golden/"))} for i, r in enumerate(case.get("refs", []), 1)
        ]  # fmt: skip
        nodes["sample"]["params"]["sampler"] = case.get("sampler", "euler")
        # enhance is bypassed: spliced out as studio does (its outputs rewired to the pass-through inputs)
        del nodes["enhance"]
        nodes["encode"]["in"]["prompt"], nodes["sample"]["in"]["canvas"] = (
            "prompt.text",
            "canvas.canvas",
        )
        return nodes

    def enhance_nodes(self, case: dict) -> dict:
        """prompt → enhance (not bypassed) → answer: the enhancer alone, as the recorder's enhance case."""
        nodes = json.loads(json.dumps(self.workflow["nodes"]))
        keep = {k: nodes[k] for k in ("prompt", "seed", "canvas", "enhance")}
        keep["prompt"]["params"]["text"] = case["prompt"]
        keep["seed"]["params"]["seed"] = case["seed"]
        keep["enhance"].pop("bypass")
        keep["enhance"]["params"]["think"] = case["think"]
        keep["answer"] = {"kind": "answer", "params": {}, "in": {"text": "enhance.text"}}
        return keep


def case(bench: MiniMaxBench, name: str, out_dir: Path | None = None) -> dict:
    s = settings(name)
    t = time.monotonic()
    same = GOLDEN / CASES[name] / f"{name}.{STACK}.{'txt' if name.startswith('enhance') else 'png'}"
    if name.startswith("enhance"):
        r = bench.run(bench.enhance_nodes(s))
        r["text"] = r["texts"].get("enhance.text") or ""
        # sampled text: equal only on the same torch + transformers (the old stack's, or the old code's run here)
        old, here = (
            r["text"] == s["enhanced_prompt"],
            same.is_file() and r["text"] + "\n" == same.read_text(),
        )
        r["ok"] = old or here
        r["compare"] = f"text equal to the old stack's: {old}, to the old code's on {STACK}: {here}"
        if out_dir:
            (out_dir / f"{name}.txt").write_text(r["text"] + "\n")
    else:
        r = bench.run(bench.nodes(s))
        r["psnr"], r["compare"] = compare(GOLDEN / CASES[name] / f"{name}.png", r["png"])
        exact = r["png"] == (GOLDEN / CASES[name] / f"{name}.png").read_bytes() or (same.is_file() and r["png"] == same.read_bytes())  # fmt: skip
        r["ok"] = r["psnr"] >= BAR and (exact or not same.is_file())
        r["compare"] += f" | bit-exact (old stack or the old code on {STACK}): {exact}"
        if out_dir:
            (out_dir / f"{name}.png").write_bytes(r["png"])
    r["total_s"] = round(time.monotonic() - t, 1)
    return r


def checks(bench: MiniMaxBench) -> dict:
    """After t2i-1024-s42: a seed change runs sample and decode only, the image model stays, the encoders do not."""
    nodes = bench.nodes(settings("t2i-1024-s42"))
    nodes["seed"]["params"]["seed"] = 43
    reseed = bench.run(nodes)
    return {"reseed_ran": reseed["ran"], "reseed_s": reseed["s"],
            "resident": [e["component"] for e in bench.residency.state()["resident"]]}  # fmt: skip


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "MiniMax-H3-Image").is_dir():
        pytest.skip("MiniMax-H3-Image is not in the models folder")
    b = MiniMaxBench()
    yield b
    b.residency.unload()


@pytest.mark.gpu
@pytest.mark.parametrize("name", list(CASES))
def test_matches_the_golden(bench, name):
    r = case(bench, name)
    print(name, r["compare"], r["s"], r["peak_gb"])
    assert r["ok"]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=["t2i-1024-s42"], choices=list(CASES))
    ap.add_argument("--checks", action="store_true")
    ap.add_argument("--out", type=Path)
    args = ap.parse_args()
    b = MiniMaxBench()
    failed = False
    for name in args.cases:
        r = case(b, name, args.out)
        failed |= not r["ok"]
        print(name, r["compare"], "s:", r["s"], "total:", r["total_s"], "peak_gb:", r["peak_gb"],
              "resident:", [(e["component"], e["gb"]) for e in b.residency.state()["resident"]], flush=True)  # fmt: skip
        if name.startswith("enhance") and not r["ok"]:
            print("enhanced text:", r["text"], flush=True)
    if args.checks:
        print("checks:", checks(b), flush=True)
    b.residency.unload()
    sys.exit(1 if failed else 0)
