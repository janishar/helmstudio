"""Record golden MiniMax-H3-Image outputs from the old studio's own generation code.

The old studio (~/GenAI/MiniMax-H3-Image-Studio) is read-only. Its env is rebuilt from its own
lockfile in a scratch copy (nothing is written inside it; bytecode is not written either):

    S=<scratch>/env; cp MiniMax-H3-Image-Studio/{pyproject.toml,uv.lock,.python-version} $S/
    (cd $S && uv sync --locked --no-install-project)
    HF_HUB_OFFLINE=1 PYTHONDONTWRITEBYTECODE=1 $S/.venv/bin/python tests/golden/record_minimax.py --cases t2i-1024-s42

One case per process. A render case does what `minimax-h3-image PROMPT [--input A] --mp 1` does (generate.main,
generate.py:314-441, no warm worker): encode with the 51-layer int8 text encoder and free it, load the int8
transformer and the VAE, set the Euler scheduler, seed torch and a CPU generator with noise_seed, run the
vae_encoder/denoise/decode blocks. Thin wrappers only observe: the encoder state's tensors, the scheduler's input
noise and each step's latent, per-phase time and MPS driver peak. The enhance case calls enhance.enhance as the CLI's
--enhance does (no render after it). Writes <out>/<case>.png (or .txt) and <case>.json; an edit also copies its
input beside it. A guard exits the process if the MPS driver passes 52 GB.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import shutil
import sys
import threading
import time
from pathlib import Path

STUDIO = Path("~/GenAI/MiniMax-H3-Image-Studio")
sys.path.insert(0, str(STUDIO / "src"))

import diffusers  # noqa: E402
import torch  # noqa: E402
import transformers  # noqa: E402
from PIL import Image, ImageOps  # noqa: E402

from minimax_h3_image import generate as g  # noqa: E402

REPO = Path(__file__).resolve().parents[2]
GOLDEN = REPO / "runner_service" / "tests" / "golden"
QWEN = REPO / "tests" / "golden" / "qwen-image"
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
MODEL = MODELS / "MiniMax-H3-Image"
PE = MODELS / "MiniMax-H3-Image-PE"
BUDGET = 52e9

# --mp 1 is the smallest the CLI's --mp takes: 1024x1024 out, generated on the pipeline's 4 MP canvas (2048x2048)
# and resized down, so no slower or cheaper than the 4 MP default (README "Sizes"). Steps are fixed at 6.
CASES = [
    {"name": "t2i-1024-s42", "out": "minimax-h3-image-t2i", "seed": 42, "mp": 1.0, "sampler": "euler",
     "prompt": "A red fox sitting in a snowy pine forest at dawn, soft golden light, photograph"},
    {"name": "edit-1024-s42", "out": "minimax-h3-image-edit", "seed": 42, "mp": 1.0, "sampler": "euler",
     "refs": ["turbo-768-s42.png"],
     "prompt": "Keep the fox in <Picture 1> exactly as it is, but turn the snowy forest into a green summer meadow with wildflowers"},
    {"name": "enhance-t2i-s42", "out": "minimax-h3-image-enhance", "seed": 42, "think": False,
     "prompt": "a corgi playing guitar in the rain"},
]  # fmt: skip


class Phases:
    """Samples the MPS driver's allocation every 50 ms into the current phase's peak; exits past the budget."""

    def __init__(self) -> None:
        self.phase, self.peak, self.t = "start", {}, {}
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._run, daemon=True)

    def _run(self) -> None:
        while not self._stop.wait(0.05):
            now = torch.mps.driver_allocated_memory()
            self.peak[self.phase] = max(self.peak.get(self.phase, 0), now)
            if now > BUDGET:
                print(f"MPS driver at {now / 1e9:.1f} GB in {self.phase}: over the budget, exiting", flush=True)
                os._exit(3)

    def mark(self, phase: str) -> None:
        torch.mps.synchronize()
        self.t[phase] = time.perf_counter()
        self.phase = phase

    def __enter__(self):
        self.t["start"] = time.perf_counter()
        self._thread.start()
        return self

    def __exit__(self, *_):
        self.mark("end")
        self._stop.set()
        self._thread.join()

    def report(self) -> tuple[dict, dict]:
        names = list(self.t)
        timing = {a: round(self.t[b] - self.t[a], 1) for a, b in zip(names, names[1:])}
        timing["total"] = round(self.t["end"] - self.t["start"], 1)
        return timing, {k: round(v / 1e9, 2) for k, v in self.peak.items() if k != "end"}


def checksum(t: torch.Tensor) -> dict:
    f = t.detach().float().cpu().contiguous()
    return {"shape": list(t.shape), "dtype": str(t.dtype).removeprefix("torch."),
            "sha256": hashlib.sha256(f.numpy().tobytes()).hexdigest(),
            "mean": round(f.mean().item(), 6), "std": round(f.std().item(), 6)}  # fmt: skip


def state_summary(values: dict) -> dict:
    """Every tensor (or list of tensors / images) in a PipelineState, by name."""
    out = {}
    for k, v in values.items():
        if isinstance(v, torch.Tensor):
            out[k] = checksum(v)
        elif isinstance(v, list | tuple) and v and all(isinstance(x, torch.Tensor) for x in v):
            out[k] = [checksum(x) for x in v]
        elif isinstance(v, list | tuple) and v and all(isinstance(x, Image.Image) for x in v):
            out[k] = [{"mode": x.mode, "size": list(x.size)} for x in v]
        elif isinstance(v, int | float | str | bool) or v is None:
            out[k] = v
    return out


def versions(device: str) -> dict:
    return {"torch": torch.__version__, "diffusers": diffusers.__version__, "transformers": transformers.__version__,
            "python": platform.python_version(), "device": device,
            "machine": f"{platform.system()} {platform.release()} {platform.machine()}"}  # fmt: skip


def render(case: dict, out: Path, device: str) -> dict:
    from diffusers.modular_pipelines import MiniMaxH3ImageBlocks, MiniMaxH3ImageModularPipeline
    from diffusers.modular_pipelines import SequentialPipelineBlocks
    from transformers import Qwen2TokenizerFast, Qwen3VLProcessor

    refs = case.get("refs") or []
    # generate.py:316: exif_transpose, then RGB (an RGBA golden loses its alpha here)
    images = [ImageOps.exif_transpose(Image.open(QWEN / r)).convert("RGB") for r in refs] or None
    ratio = images[0].width / images[0].height if images else 1.0  # generate.py:355
    width, height = g.plan_size(ratio, case["mp"], None, None)
    blocks = MiniMaxH3ImageBlocks()

    def part(*names: str) -> MiniMaxH3ImageModularPipeline:
        sub = SequentialPipelineBlocks.from_blocks_dict({n: blocks.sub_blocks[n] for n in names})
        return MiniMaxH3ImageModularPipeline(blocks=sub, pretrained_model_name_or_path=str(MODEL))

    meta: dict = {}
    with Phases() as ph:
        ph.mark("encode_load")
        encoder = part("before_encode", "text_encoder")
        encoder.update_components(
            text_encoder=g.load_text_encoder(MODEL, device),
            tokenizer=Qwen2TokenizerFast.from_pretrained(MODEL / "tokenizer"),
            processor=Qwen3VLProcessor.from_pretrained(MODEL / "processor"),
        )
        ph.mark("encode")
        state = encoder(prompt=case["prompt"], image=images)
        ph.mark("encode_free")
        meta["encoder_state"] = state_summary(state.values)
        del encoder
        g.empty_cache(device)

        ph.mark("load")
        r = part("vae_encoder", "denoise", "decode")
        r.update_components(transformer=g.load_transformer(MODEL, device), vae=g.load_vae(MODEL, device))
        r.update_components(scheduler=g.load_scheduler(MODEL, case["sampler"]))
        seed = g.noise_seed(case["seed"], images)
        torch.manual_seed(seed)

        steps: list[dict] = []
        sched = r.scheduler
        step = sched.step

        def observed_step(model_output, timestep, sample, *a, **kw):
            if not steps:
                meta["initial_noise"] = checksum(sample)
            res = step(model_output, timestep, sample, *a, **kw)
            prev = res[0] if isinstance(res, tuple) else res.prev_sample
            steps.append({"timestep": round(float(timestep), 6), "velocity": checksum(model_output),
                          "latent": checksum(prev)})  # fmt: skip
            return res

        sched.step = observed_step
        def on_first(*_) -> None:  # a pre-hook's non-None return would replace the args
            ph.mark("denoise")
            first.remove()

        first = r.transformer.register_forward_pre_hook(on_first)
        last = {"n": 0}

        def after(*_):
            last["n"] += 1
            if last["n"] == g.STEPS:
                ph.mark("decode")

        hook = r.transformer.register_forward_hook(after)
        ph.mark("vae_encode" if images else "prepare")
        st = r(state=state, height=height, width=width, generator=torch.Generator().manual_seed(seed))
        hook.remove()
        image = st.get("images")[0]
        ph.mark("save")
        meta["render_state"] = state_summary({k: st.values.get(k) for k in (
            "height", "width", "output_height", "output_width", "latent_height", "latent_width",
            "num_reference_rows", "reference_images", "vision_images", "reference_latents", "timesteps")})  # fmt: skip
        meta["steps"] = steps
        meta["sigmas_used"] = [round(float(s), 8) for s in sched.sigmas]
        meta["timesteps_used"] = [round(float(t), 8) for t in sched.timesteps]
        meta["scheduler"] = dict(sched.config)
        meta["sample_sigmas_config"] = list(r.config.sample_sigmas)
        image.save(out / f"{case['name']}.png")
        del r, st, state, sched
        g.empty_cache(device)
    timing, peak = ph.report()
    meta = {
        **{k: case[k] for k in ("name", "seed", "mp", "sampler", "prompt")},
        "width": width, "height": height, "steps": g.STEPS, "precision": "int8 (transformer blocks, text-encoder LM layers)",
        "text_layers": g.TEXT_LAYERS, "generator": "torch.Generator('cpu').manual_seed(noise_seed); torch.manual_seed too",
        "noise_seed": seed, "output_mode": image.mode, "output_size": list(image.size),
        "png_sha256": hashlib.sha256((out / f"{case['name']}.png").read_bytes()).hexdigest(),
        **({"refs": [{"file": f"{r}", "source": f"tests/golden/qwen-image/{r}", "opened_as": "RGB",
                      "size": list(im.size), "sha256": hashlib.sha256((QWEN / r).read_bytes()).hexdigest()}
                     for r, im in zip(refs, images)]} if images else {}),  # fmt: skip
        **meta, "model_dir": str(MODEL), "versions": versions(device), "timing_s": timing, "peak_gb_mps_driver": peak,
    }  # fmt: skip
    for r in refs:
        shutil.copyfile(QWEN / r, out / f"input-{r}")
    return meta


def enhance_case(case: dict, out: Path, device: str) -> dict:
    from minimax_h3_image.enhance import enhance

    with Phases() as ph:
        ph.mark("enhance")  # load + prefill + generate; enhance() prints its own split to stderr
        prompt, ratio = enhance(case["prompt"], None, str(PE), case["seed"], case["think"], device, None)
    timing, peak = ph.report()
    (out / f"{case['name']}.txt").write_text(prompt + "\n")
    return {**{k: case[k] for k in ("name", "seed", "think", "prompt")}, "enhanced_prompt": prompt, "ratio": ratio,
            "reasoning_effort": "xhigh" if case["think"] else "low", "sampling": "do_sample, T=1.0, top_p=0.95, top_k=20, "
            "max_new_tokens=4096, torch.manual_seed(seed)", "pe_dir": str(PE), "versions": versions(device),
            "timing_s": timing, "peak_gb_mps_driver": peak}  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--cases", nargs="+", required=True, help="one case per process: " + ", ".join(c["name"] for c in CASES))
    ap.add_argument("--out", type=Path, help="default: runner_service/tests/golden/<the case's dir>")
    args = ap.parse_args()
    device = g.pick_device("mps")
    g.patch_mps_empty_cache()
    for case in [c for c in CASES if c["name"] in args.cases]:
        out = args.out or GOLDEN / case["out"]
        out.mkdir(parents=True, exist_ok=True)
        meta = (enhance_case if case["name"].startswith("enhance") else render)(case, out, device)
        (out / f"{case['name']}.json").write_text(json.dumps(meta, indent=2, default=str) + "\n")
        print(case["name"], json.dumps({"timing_s": meta["timing_s"], "peak_gb": meta["peak_gb_mps_driver"]}), flush=True)


if __name__ == "__main__":
    main()
