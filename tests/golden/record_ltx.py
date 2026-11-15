"""Record golden LTX-2.5 distilled two-stage text-to-video (+ audio) outputs from the old studio's own code.

The old studio (~/GenAI/ltx-2-studio) is read-only and ships no .venv, so its env is rebuilt from
its own lockfile in a scratch copy (nothing is written inside it):

    S=<scratch>/studio; rsync -a --exclude __pycache__ ltx-2-studio/{pyproject.toml,uv.lock,README.md,LICENSE,packages} $S/
    (cd $S && UV_PROJECT_ENVIRONMENT=$S/.venv uv sync --locked --no-dev)
    LTX_MLX_CACHE_DIR=<scratch>/vcache $S/.venv/bin/python tests/golden/record_ltx.py --cases t2v-448x256-49f-s42

One case per process (as the CLI runs): DistilledPipeline built and driven exactly as `ltx-2-mlx generate
--distilled --quantize-on-load 8` does (cli._cmd_generate), with thin wrappers that only observe: the prompt
embeddings, each stage's input noise / output latent, the upscaled latent, the final latents, the waveform before
AAC, per-phase time and MLX peak memory. Writes <out>/<case>.mp4, .wav, -first/-mid/-last.png and .json.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import resource
import subprocess
import sys
import tempfile
import time
from pathlib import Path

os.environ.setdefault("LTX_MLX_CACHE_DIR", str(Path(tempfile.gettempdir()) / "ltx-virtual-packs"))
os.environ["LTX_MLX_QUANTIZE_ON_LOAD"] = "8"  # what the CLI sets from --quantize-on-load (default 8)

import mlx.core as mx
import numpy as np
from ltx_pipelines_mlx import distilled as distilled_mod
from ltx_pipelines_mlx.utils import blocks
from ltx_pipelines_mlx.utils._orchestration import save_waveform

REPO = Path(__file__).resolve().parents[2]
OUT = REPO / "runner_service" / "tests" / "golden" / "ltx-text-to-video"
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
GEMMA_DEFAULT = "mlx-community/gemma-3-12b-it-4bit"  # cli.DEFAULT_GEMMA; unused on LTX-2.5 (Gemma 4 ships in the pack)

CASES = [
    {"name": "t2v-448x256-49f-s42", "width": 448, "height": 256, "frames": 49, "fps": 24.0, "seed": 42,
     "prompt": "A golden retriever puppy runs across a sunny park lawn toward the camera, barking happily, "
               "birds chirping in the trees behind it"},
    {"name": "t2v-512x320-57f-s7", "width": 512, "height": 320, "frames": 57, "fps": 25.0, "seed": 7,
     "prompt": "Ocean waves crash against black volcanic rocks at sunset, white spray in the air, "
               "seagulls crying overhead and the wind roaring"},
]  # fmt: skip


def stats(a: mx.array) -> dict:
    """Shape, dtype and moments of an array, plus sha256 of its float32 bytes (bit-exact only on the same stack)."""
    x = np.array(a.astype(mx.float32))
    return {"shape": list(a.shape), "dtype": str(a.dtype).removeprefix("mlx.core."),
            "mean": round(float(x.mean()), 6), "std": round(float(x.std()), 6),
            "min": round(float(x.min()), 6), "max": round(float(x.max()), 6),
            "abs_mean": round(float(np.abs(x).mean()), 6),
            "sha256_f32": hashlib.sha256(x.tobytes()).hexdigest()}  # fmt: skip


def gib(n: int) -> float:
    return round(n / 1024**3, 2)


class Phases:
    """Wall time, MLX peak and active memory per phase (peak reset at each phase start)."""

    def __init__(self) -> None:
        self.rows: dict[str, dict] = {}

    def wrap(self, name: str, fn):
        def run(*a, **kw):
            mx.reset_peak_memory()
            t = time.perf_counter()
            out = fn(*a, **kw)
            row = self.rows.setdefault(name, {"s": 0.0, "peak_gib": 0.0, "calls": 0})
            row["s"] = round(row["s"] + time.perf_counter() - t, 2)
            row["peak_gib"] = max(row["peak_gib"], gib(mx.get_peak_memory()))
            row["active_after_gib"] = gib(mx.get_active_memory())
            row["calls"] += 1
            return out

        return run


def ffprobe(path: Path) -> dict:
    r = subprocess.run(["ffprobe", "-v", "error", "-show_entries",
                        ("stream=index,codec_type,codec_name,profile,pix_fmt,width,height,r_frame_rate,nb_frames,"
                         "sample_rate,channels,duration:format=duration,bit_rate"), "-of", "json", str(path)],
                       check=True, capture_output=True, text=True)  # fmt: skip
    return json.loads(r.stdout)


def frames(mp4: Path, out: Path, name: str, n: int) -> dict:
    """First, middle and last decoded frames as PNG (rgb24); returns {which: index}."""
    picks = {"first": 0, "mid": (n - 1) // 2, "last": n - 1}
    for which, i in picks.items():
        subprocess.run(["ffmpeg", "-v", "error", "-y", "-i", str(mp4), "-vf", f"select=eq(n\\,{i})", "-fps_mode", "passthrough",
                        "-frames:v", "1", "-pix_fmt", "rgb24", str(out / f"{name}-{which}.png")], check=True)  # fmt: skip
    return picks


def record(case: dict, out: Path) -> dict:
    model = MODELS / "ltx-2.5"
    ph = Phases()
    seen: dict = {"stages": []}

    # cli._cmd_generate, --distilled branch
    t0 = time.perf_counter()
    pipe = distilled_mod.DistilledPipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_memory=True,
                                           low_ram_streaming=False, tile_count=None)  # fmt: skip
    pipe.verbose = True
    pipe.stepwise = None
    pipe.generate_audio = True
    pipe.video_decoder = "conv"
    pipe.diffvae_tile = None
    t_init = time.perf_counter() - t0

    # Observers (call through unchanged)
    enc = pipe.prompt_encoder
    enc.load = ph.wrap("text_encoder_load", enc.load)
    raw_encode = enc.encode

    def encode(prompt):
        v, a = raw_encode(prompt)
        mx.eval(v, a)
        seen["video_embeds"], seen["audio_embeds"] = stats(v), stats(a)
        return v, a

    enc.encode = ph.wrap("text_encode", encode)
    pipe.load = ph.wrap("dit_vae_encoder_upsampler_load", pipe.load)
    raw_loop = pipe._run_denoise_loop

    def loop(**kw):
        n = len(seen["stages"]) + 1
        vs, as_ = kw["video_state"], kw["audio_state"]
        row = {"stage": n, "sigmas": [float(s) for s in kw["sigmas"]], "ancestral": kw["ancestral"],
               "loop_noise_seed": kw["seed"] + distilled_mod.ANCESTRAL_NOISE_SEED_OFFSET if kw["ancestral"] else None,
               "video_in": stats(vs.latent), "audio_in": stats(as_.latent),
               "video_positions": list(vs.positions.shape), "audio_positions": list(as_.positions.shape)}  # fmt: skip
        if n == 2:  # clean_latent of stage 2 is the upscaled, renormalised stage-1 latent (and stage-1 audio)
            row["video_upscaled"], row["audio_from_stage1"] = stats(vs.clean_latent), stats(as_.clean_latent)
        res = ph.wrap(f"stage{n}_denoise", raw_loop)(**kw)
        mx.eval(res.video_latent, res.audio_latent)
        row["video_out_tokens"], row["audio_out_tokens"] = stats(res.video_latent), stats(res.audio_latent)
        seen["stages"].append(row)
        return res

    pipe._run_denoise_loop = loop
    raw_two = pipe.generate_two_stage

    def two(**kw):
        v, a = raw_two(**kw)
        seen["final_video_latent"], seen["final_audio_latent"] = stats(v), stats(a)
        return v, a

    pipe.generate_two_stage = ph.wrap("generate_two_stage_total", two)
    pipe._load_decoders = ph.wrap("decoders_load", pipe._load_decoders)
    pipe._decode_and_save_video = ph.wrap("decode_video_audio_mux", pipe._decode_and_save_video)
    raw_audio_call = blocks.AudioDecoder.__call__
    wav = out / f"{case['name']}.wav"

    def audio_call(self, latent):
        w = raw_audio_call(self, latent)
        mx.eval(w)
        seen["waveform"] = stats(w)
        save_waveform(w, str(wav), sample_rate=48000)  # the same writer the pipeline uses for its temp WAV
        return w

    blocks.AudioDecoder.__call__ = ph.wrap("audio_decode", audio_call)

    mp4 = out / f"{case['name']}.mp4"
    mx.reset_peak_memory()
    pipe.generate_and_save(prompt=case["prompt"], output_path=str(mp4), height=case["height"], width=case["width"],
                           num_frames=case["frames"], frame_rate=case["fps"], seed=case["seed"], images=None,
                           generated_keyframes=0)  # fmt: skip
    total = time.perf_counter() - t0
    probe = ffprobe(mp4)
    vstream = next(s for s in probe["streams"] if s["codec_type"] == "video")
    picks = frames(mp4, out, case["name"], int(vstream["nb_frames"]))
    ru = resource.getrusage(resource.RUSAGE_SELF)
    meta = {
        **case,
        "pipeline": "DistilledPipeline (ltx-2-mlx generate --distilled)",
        "argv_equivalent": f"ltx-2-mlx generate --distilled --model {model} --quantize-on-load 8 "
        f"--width {case['width']} --height {case['height']} --frames {case['frames']} --frame-rate {case['fps']:g} "
        f"--seed {case['seed']} --prompt '...' -o {case['name']}.mp4",
        "quantize_on_load": 8, "quant_group_size": 64, "low_ram": False, "tiling": None, "video_decoder": "conv",
        "generate_audio": True, "stage1_steps": None, "stage2_steps": None, "is_ltx25": pipe._is_25,
        "noise_seeds": {"stage1_video": case["seed"], "stage1_audio": case["seed"] + 1,
                        "stage1_ancestral_loop": case["seed"] + distilled_mod.ANCESTRAL_NOISE_SEED_OFFSET,
                        "stage2_video_and_audio": case["seed"] + 2},
        "prompt_embeds": {"video": seen.get("video_embeds"), "audio": seen.get("audio_embeds")},
        "stages": seen["stages"],
        "final_video_latent": seen.get("final_video_latent"),
        "final_audio_latent": seen.get("final_audio_latent"),
        "waveform": seen.get("waveform"), "wav": {"sample_rate": 48000, "format": "PCM s16le", "file": wav.name},
        "frames_png": {k: {"index": v, "file": f"{case['name']}-{k}.png"} for k, v in picks.items()},
        "mp4": {"file": mp4.name, "bytes": mp4.stat().st_size, "ffprobe": probe,
                "sha256": hashlib.sha256(mp4.read_bytes()).hexdigest()},
        "timing_s": {"pipeline_init": round(t_init, 2), **{k: v["s"] for k, v in ph.rows.items()},
                     "total_wall": round(total, 1)},
        "memory": {"phases": ph.rows, "mlx_peak_gib_whole_run": max(v["peak_gib"] for v in ph.rows.values()),
                   "process_max_rss_gib": gib(ru.ru_maxrss)},  # macOS ru_maxrss is bytes
        "env": {k: v for k, v in os.environ.items() if k.startswith(("LTX2_", "LTX_MLX_", "LTX_MODEL"))},
        "versions": {"mlx": mx.__version__, "python": platform.python_version(), "numpy": np.__version__,
                     "ffmpeg": subprocess.run(["ffmpeg", "-version"], check=True, capture_output=True, text=True).stdout.split("\n")[0],
                     "ltx_pipelines_mlx": "0.15.4 (ltx-2-studio lockfile)",
                     "machine": f"{platform.system()} {platform.release()} {platform.machine()}",
                     "metal": {k: v for k, v in mx.device_info().items() if k in ("architecture", "memory_size")}},
    }  # fmt: skip
    meta["env"]["LTX_MLX_CACHE_DIR"] = "<scratch>"
    (out / f"{case['name']}.json").write_text(json.dumps(meta, indent=2, default=str) + "\n")
    print(case["name"], json.dumps({"timing_s": meta["timing_s"], "peak_gib": meta["memory"]["mlx_peak_gib_whole_run"],
                                    "rss_gib": meta["memory"]["process_max_rss_gib"]}), flush=True)  # fmt: skip
    return meta


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--cases", nargs="*", help="case names (default: all; run one per process to keep peaks clean)")
    ap.add_argument("--out", type=Path, default=OUT)
    args = ap.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    for case in [c for c in CASES if not args.cases or c["name"] in args.cases]:
        record(case, args.out)


if __name__ == "__main__":
    sys.exit(main())
