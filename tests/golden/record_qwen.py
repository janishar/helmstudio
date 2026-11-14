"""Record golden Qwen Image 2.1 outputs from the old studio's own generation code.

Run with the old studio's venv (read-only; nothing there is edited):

    ~/GenAI/qwen-image-2.1-studio/.venv/bin/python tests/golden/record_qwen.py
    ... record_qwen.py --cases turbo-768-s42 --out /tmp/repeat     # one case, elsewhere

Each case writes <out>/<case>.png and <case>.json. One process, one pipeline at a time: every case loads the
pipeline afresh exactly as the studio's CLI does (generate.main), runs, and frees it before the next.
"""

from __future__ import annotations

import argparse
import gc
import json
import os
import platform
import sys
import threading
import time
from pathlib import Path

STUDIO = Path("~/GenAI/qwen-image-2.1-studio")
sys.path.insert(0, str(STUDIO / "src"))

import diffusers
import torch
import transformers
from qwen_image_2_1 import generate as g

MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
VARIANT_DIR = {"base": "Qwen-Image-2.1", "turbo": "Qwen-Image-2.1-Turbo"}

# The base case is last: it loads the 31 GB checkpoint (int8 on the device).
CASES = [
    {"name": "turbo-768-s42", "variant": "turbo", "precision": "int8", "width": 768, "height": 768, "seed": 42, "steps": 40,
         "prompt": "A red fox sitting in a snowy pine forest at dawn, soft golden light, photograph"},
    {"name": "turbo-1024-s7", "variant": "turbo", "precision": "int8", "width": 1024, "height": 1024, "seed": 7, "steps": 40,
         "prompt": 'A neon shop sign on a brick wall that reads "QWEN", rainy night street, reflections'},
    {"name": "turbo-1344x768-s123", "variant": "turbo", "precision": "int8", "width": 1344, "height": 768, "seed": 123, "steps": 40,
         "prompt": "A wide watercolor landscape of terraced rice fields under a stormy sky, a small farmhouse"},
    {"name": "base-768-s42", "variant": "base", "precision": "int8", "width": 768, "height": 768, "seed": 42, "steps": 40,
         "prompt": "A red fox sitting in a snowy pine forest at dawn, soft golden light, photograph"},
]  # fmt: skip


class PeakMemory:
    """Samples the MPS driver's allocation every 100 ms; MPS has no max_memory_allocated."""

    def __init__(self) -> None:
        self.peak, self._stop = 0, threading.Event()
        self._t = threading.Thread(target=self._run, daemon=True)

    def _run(self) -> None:
        while not self._stop.wait(0.1):
            self.peak = max(self.peak, torch.mps.driver_allocated_memory())

    def __enter__(self):
        self._t.start()
        return self

    def __exit__(self, *_):
        self._stop.set()
        self._t.join()


def gb(n: int) -> float:
    return round(n / 1e9, 2)


def record(case: dict, out: Path, device: str) -> dict:
    model = MODELS / VARIANT_DIR[case["variant"]]
    with PeakMemory() as mem:
        t0 = time.perf_counter()
        pipe = g.load_pipe(str(model), device, int8=case["precision"] == "int8")
        g.free_text_encoder_after_encoding(pipe, device)
        loaded = torch.mps.driver_allocated_memory()
        t1 = time.perf_counter()
        steps_done: list[float] = []

        def on_step(p, i, t, kw):
            steps_done.append(time.perf_counter())
            return kw

        image = pipe(
            prompt=case["prompt"], image=None, width=case["width"], height=case["height"],
            num_inference_steps=case["steps"],
            generator=torch.Generator(device="cpu").manual_seed(g.noise_seed(case["seed"], None)),
            callback_on_step_end=on_step,
        ).images[0]  # fmt: skip
        t2 = time.perf_counter()
        sched = pipe.scheduler
        meta = {
            **{k: case[k] for k in ("variant", "precision", "width", "height", "seed", "prompt")},
            "steps_arg": case["steps"],
            "steps_run": pipe._num_timesteps,
            "negative_prompt": None,
            "true_cfg_scale": 1.0,
            "transparent": False,
            "tiled": False,
            "use_kv_cache": True,
            "output_resolution": 1024,
            "generator": "torch.Generator('cpu').manual_seed(seed)",
            "sigmas_arg": pipe.config.get(
                "sample_sigmas"
            ),  # Turbo's baked 8; None -> linspace(1, 1/n, n)
            "sigmas_used": [round(float(s), 6) for s in sched.sigmas],
            "timesteps_used": [round(float(t), 4) for t in sched.timesteps],
            "scheduler": dict(sched.config),
            "model_dir": str(model),
            "output_mode": image.mode,
            "output_size": list(image.size),
            "device": device,
            "versions": {
                "torch": torch.__version__,
                "diffusers": diffusers.__version__,
                "transformers": transformers.__version__,
                "python": platform.python_version(),
                "machine": f"{platform.system()} {platform.release()} {platform.machine()}",
            },
        }
        del pipe, sched
        gc.collect()
        g.empty_cache(device)
    first = steps_done[0] - t1 if steps_done else 0  # encode + first step
    meta["timing_s"] = {
        "load": round(t1 - t0, 1),
        "encode_plus_first_step": round(first, 1),
        "per_step": round((steps_done[-1] - steps_done[0]) / max(len(steps_done) - 1, 1), 2),
        "decode": round(t2 - steps_done[-1], 1),
        "total": round(t2 - t0, 1),
    }
    meta["memory_gb"] = {"after_load": gb(loaded), "peak_mps_driver": gb(mem.peak)}
    image.save(out / f"{case['name']}.png")
    (out / f"{case['name']}.json").write_text(json.dumps(meta, indent=2, default=str) + "\n")
    print(
        case["name"],
        json.dumps({"timing_s": meta["timing_s"], "memory_gb": meta["memory_gb"]}),
        flush=True,
    )
    return meta


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--cases", nargs="*", help="case names (default: all)")
    ap.add_argument("--out", type=Path, default=Path(__file__).parent / "qwen-image")
    args = ap.parse_args()
    cases = [c for c in CASES if not args.cases or c["name"] in args.cases]
    device = g.pick_device("mps")
    g.patch_mps_empty_cache()
    args.out.mkdir(parents=True, exist_ok=True)
    for case in cases:
        record(case, args.out, device)


if __name__ == "__main__":
    main()
