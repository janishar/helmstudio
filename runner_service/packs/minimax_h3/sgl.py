"""The base-fp8 variant's generate (NVIDIA hosts, docs/plan/17-nvidia-quantized.md phase 2): MiniMax H3 by SGLang Diffusion
(sglang 0.5.23 in its own venv, SGLANG; Dockerfile.cuda stage `sglang`) as the run's child server (core.child.serve,
killed after the run, as zonos3-server: nothing stays in VRAM), on 127.0.0.1. One server serves one partition
(--model-variant): fl2va for text and frames to video, ref2va for references; the variant's Comfy-Org pruned FP8 DiT
and int8 ConvRot text encoder are weight overrides over the official skeleton (formats detected from the files), the
text encoder layerwise offloaded (one 96 GB card) on torch_sdpa (SM120).

The request (POST /v1/videos, SGLang's canonical MiniMax H3 request): task, conditions (first / last frame →
keyframes at frame 0 / -1; image, audio, video references in h3's argv order: images, audios, videos, each numbered
per kind as the prompt's <Picture n>, <Audio n>, <Video n>), target {short_edge, aspect_ratio, duration_seconds =
frames / 24}, seed, num_inference_steps. SGLang derives the canvas from short edge and aspect (adapt_shape_v1: shape()
here) and the frames from the duration (4-15 s, snapped up to 5 + 17k), so only canvases and lengths it can make
exactly are accepted (cuda_error). Polled every POLL s: `progress` (0-99, the denoise) → phase sampling; cancel
kills the server (SGLang has no abort). Its MP4 (H.264 + AAC 32 kHz, 24 fps) is the output."""

import json
import math
import re
import socket
import time
import urllib.error
import urllib.request
from pathlib import Path

from core import child, media
from core.graph import RunError

SGLANG = Path("/opt/sglang/bin/sglang")  # Dockerfile.cuda
# uvicorn's start line (use_colors: the URL may be wrapped in ANSI codes); SGLang starts it after the weights loaded
READY = re.compile(r"Uvicorn running on (?:\x1b\[[0-9;]*m)?http://127\.0\.0\.1:(\d+)")
READY_S = (
    1800  # cold: ~70 GB of weights from disk plus JIT kernels; ponytail: flat, measure on the VM
)
POLL = 1.0
REQUEST_S = 60
IDLE_S = 1800  # no progress change for this long fails the run (the decode reports none)
FPS = 24
# SGLang's MiniMax H3 limits (minimax_h3/constants.py, task_profiles.py, resolved_plan.py at 0.5.23)
MIN_FRAMES, MAX_FRAMES = 107, 345  # 4-15 s on the 5 + 17k grid
RATIOS = (
    "21:9",
    "16:9",
    "4:3",
    "1:1",
    "3:4",
    "9:16",
)  # t2va and ref2va; fl2va takes any W:H (1:4 to 4:1)
MAX_PIXELS = 768 * 1344


def shape(short: int, rw: int, rh: int) -> tuple[int, int]:
    """SGLang's adapt_shape_v1: the canvas of a short edge and an aspect ratio (nearest 32, area capped)."""
    r = rw / rh
    w, h = (short * r, short) if r >= 1 else (short, short / r)
    if w * h > MAX_PIXELS:
        s = math.sqrt(MAX_PIXELS / (w * h))
        w, h = w * s, h * s
    return max(32, round(w / 32) * 32), max(32, round(h / 32) * 32)


def target(task: str, width: int, height: int, frames: int) -> dict:
    """The request's target for a w × h canvas, or RunError's text as ValueError when SGLang cannot make it."""
    if not MIN_FRAMES <= frames <= MAX_FRAMES:
        raise ValueError(
            f"the Base fp8 variant makes {MIN_FRAMES} to {MAX_FRAMES} frames (4 to 15 s), not {frames}"
        )
    short, g = min(width, height), math.gcd(width, height)
    ratios = RATIOS if task != "fl2va" else (f"{width // g}:{height // g}",)
    for ratio in ratios:
        if shape(short, *map(int, ratio.split(":"))) == (width, height):
            return {"short_edge": short, "aspect_ratio": ratio, "duration_seconds": frames / FPS}
    raise ValueError(f"the Base fp8 variant makes text-to-video and references only at {', '.join(RATIOS)} "
                     f"(the canvas is {width} × {height}; 864 × 480 is 16:9)")  # fmt: skip


def request(prompt: str, seed: int, steps: int, size: dict, frames: int, first, last,
            images: list[Path], audios: list[Path], videos: list[Path]) -> dict:  # fmt: skip
    """The /v1/videos body; first / last are PNG paths or None."""
    refs = (
        [("image", p) for p in images]
        + [("audio", p) for p in audios]
        + [("video", p) for p in videos]
    )
    keys = [(0, first), (-1, last)]
    task = "ref2va" if refs else "fl2va" if first or last else "t2va"
    conditions = [
        {"type": "image", "role": "keyframe", "uri": str(p), "frame_index": i} for i, p in keys if p
    ]
    conditions += [{"type": kind, "role": "reference", "uri": str(p)} for kind, p in refs]
    return {"prompt": prompt, "task": task, "conditions": conditions,
            "target": target(task, size["width"], size["height"], frames),
            "seed": seed, "num_inference_steps": steps}  # fmt: skip


def argv(model, partition: str, port: int, tmp: Path) -> list[str]:
    transformer = model.files(f"transformer_{partition}")[0]
    (te,) = model.files("text_encoder")
    # --backend sglang --pipeline-class-name: SGLang picks its native H3 pipeline only for a folder named MiniMax-H3
    # (else the root's diffusers model_index.json sends it to the diffusers fallback, which cannot load H3)
    return [str(SGLANG), "serve", "--backend", "sglang", "--pipeline-class-name", "MiniMaxH3Pipeline",
            "--model-path", str(model.folder().resolve()), "--model-variant", partition,
            "--component-weights-paths.transformer", str(transformer.resolve()),
            "--component-weights-paths.text_encoder", str(te.resolve()),
            "--layerwise-offload-components", "text_encoder",
            "--component-attention-backends", "text_encoder=torch_sdpa",
            "--warmup-mode", "off", "--host", "127.0.0.1", "--port", str(port),
            "--output-path", str(tmp / "out"), "--input-save-path", str(tmp / "in")]  # fmt: skip


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def call(
    node: str, port: int, path: str, body: dict | None = None, timeout: float = REQUEST_S
) -> bytes:
    """One HTTP call to the server; an HTTP error fails the run with its `detail`."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}{path}", data, {"Content-Type": "application/json"}
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.read()
    except urllib.error.HTTPError as e:
        raw = e.read().decode(errors="replace")
        try:
            msg = json.loads(raw).get("detail") or raw
        except ValueError, AttributeError:
            msg = raw
        msg = msg if isinstance(msg, str) else json.dumps(msg)
        raise RunError(
            f"{node}: SGLang refused the request ({e.code}): {media.last_line(msg)}"
        ) from None
    except (TimeoutError, urllib.error.URLError) as e:
        raise RunError(f"{node}: SGLang could not be reached ({getattr(e, 'reason', e)})") from None


def wait(ctx, port: int, job: str) -> None:
    """Poll the job until completed (progress forwarded), failed (its message) or silent for IDLE_S."""
    sent, seen = 0, time.monotonic()
    while True:
        ctx.check()
        v = json.loads(call(ctx.node, port, f"/v1/videos/{job}"))
        if v["status"] == "completed":
            return
        if v["status"] == "failed":
            why = (v.get("error") or {}).get("message") or "no reason given"
            raise RunError(f"{ctx.node}: SGLang failed: {media.last_line(why)}")
        if (p := int(v.get("progress") or 0)) > sent:
            ctx.progress(p, 100, "sampling")
            sent, seen = p, time.monotonic()
        if time.monotonic() - seen > IDLE_S:
            raise RunError(f"{ctx.node}: SGLang made no progress for {IDLE_S} s")
        time.sleep(POLL)


def generate(ctx, model, body: dict, out: Path, tmp: Path) -> None:
    """Start the partition's server, run `body`, write its MP4 to `out`."""
    if not SGLANG.is_file():
        raise RunError(
            f"{ctx.node}: SGLang is not installed (the runner-cuda-native image, Dockerfile.cuda target native)"
        )
    partition = "ref2va" if body["task"] == "ref2va" else "fl2va"
    ctx.progress(1, 1, "starting sglang")  # a phase without steps: 1 of 1
    with child.serve(ctx, argv(model, partition, free_port(), tmp), cwd=tmp, name="sglang",
                     ready=READY, ready_s=READY_S) as port:  # fmt: skip
        job = json.loads(call(ctx.node, port, "/v1/videos", body))["id"]
        wait(ctx, port, job)
        out.write_bytes(call(ctx.node, port, f"/v1/videos/{job}/content", timeout=600))
