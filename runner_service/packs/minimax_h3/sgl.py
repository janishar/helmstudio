"""The base-fp8 variant's generate (NVIDIA hosts, docs/plan/17-nvidia-quantized.md phase 2): MiniMax H3 by SGLang Diffusion
(sglang 0.5.23 in its own venv, SGLANG; Dockerfile.cuda stage `sglang`) as the run's child server (core.child.serve,
killed after the run, as zonos3-server: nothing stays in VRAM), on 127.0.0.1. One server serves one partition
(--model-variant): fl2va for text and frames to video, ref2va for references; the variant's Comfy-Org pruned FP8 DiT
and int8 ConvRot text encoder are weight overrides over the official skeleton (formats detected from the files), the
text encoder layerwise offloaded (one 96 GB card) on torch_sdpa (SM120).

The request (POST /v1/videos, SGLang's canonical MiniMax H3 request): task, conditions (first / last frame →
keyframes at frame 0 / -1; image, audio, video references in h3's argv order: images, audios, videos, each numbered
per kind as the prompt's <Picture n>, <Audio n>, <Video n>), target {short_edge, aspect_ratio, duration_seconds =
frames / 24}, seed, num_inference_steps; API 1.37 `quality` (exact; lossless, SGLang's default, is sent as nothing),
`flow_shift` and `audio_flow_shift` only when they differ from the model's 12 and 3, so a default run's body is
unchanged (plan 20 24.3 A5; unverified on the VM). SGLang derives the canvas from short edge and aspect (adapt_shape_v1: shape()
here) and the frames from the duration (4-15 s, snapped up to 5 + 17k), so only canvases and lengths it can make
exactly are accepted (cuda_error). API 1.41: `lora: turbo` starts the server with `--lora-path` the add-on file
native.lora_file picked for the partition (Turbo LoRA for fl2va, Turbo Ref LoRA for ref2va; one server per run, so
no /set_lora) as an SGLang-ready copy (ready_lora: alpha folded into lora_B, GOTCHAS.md); SGLang's default
`--lora-merge-mode auto` keeps it unmerged on the FP8 weights (unverified on the VM). Polled every POLL s: `progress` (0-99, the denoise) → phase sampling (phase id
sample; the server's start is encode, and once the denoise reaches 99, decode_video: SGLang reports nothing of its
decode); cancel kills the server (SGLang has no abort). Its MP4 (H.264 + AAC 32 kHz, 24 fps) is the output."""

import hashlib
import json
import math
import os
import re
import socket
import time
import urllib.error
import urllib.request
from pathlib import Path

from core import child, media, models
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
# the model's flow shifts (model_index.json _minimax_h3.sigma_shift_scales): generate.flow_shift / audio_flow_shift
FLOW_SHIFT, AUDIO_FLOW_SHIFT = 12.0, 3.0


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
            images: list[Path], audios: list[Path], videos: list[Path], *, quality: str = "lossless",
            flow_shift: float = FLOW_SHIFT, audio_flow_shift: float = AUDIO_FLOW_SHIFT) -> dict:  # fmt: skip
    """The /v1/videos body; first / last are PNG paths or None; quality and the shifts only when not the default."""
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
    body = {"prompt": prompt, "task": task, "conditions": conditions,
            "target": target(task, size["width"], size["height"], frames),
            "seed": seed, "num_inference_steps": steps}  # fmt: skip
    if quality != "lossless":
        body["quality"] = quality
    if flow_shift != FLOW_SHIFT:
        body["flow_shift"] = flow_shift
    if audio_flow_shift != AUDIO_FLOW_SHIFT:
        body["audio_flow_shift"] = audio_flow_shift
    return body


def argv(model, partition: str, port: int, tmp: Path, lora: Path | None = None) -> list[str]:
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
            "--output-path", str(tmp / "out"), "--input-save-path", str(tmp / "in"),
            *(["--lora-path", str(lora)] if lora else [])]  # fmt: skip


def ready_lora(src: Path) -> Path:
    """The LoRA as SGLang should load it: each module's lora_B times alpha / rank (alpha: its `.alpha`, else the
    metadata's, else the rank; h3_lora.c's), no `.alpha` tensors or metadata alpha, so SGLang's scale is rank / rank
    at lora_scale 1. Its fused qkv cannot take per-module `.alpha` (a 3-element .item()), and it would truncate a
    fractional alpha. Written once (tmp + rename) to models.SGLANG_COPIES next to the file, keyed by its size and
    mtime; the add-on's delete removes it with its file."""
    st = src.stat()
    key = hashlib.sha256(f"{st.st_size}:{st.st_mtime_ns}".encode()).hexdigest()[:12]
    out = src.parent / models.SGLANG_COPIES / f"{src.stem}-{key}{src.suffix}"
    if out.is_file():
        return out
    from safetensors import safe_open  # the torch env only (CUDA hosts)
    from safetensors.torch import load_file, save_file

    with safe_open(str(src), "pt") as f:
        meta = f.metadata() or {}
    t = load_file(str(src))
    file_alpha = float(meta.get("alpha") or 0)
    for kb in [k for k in t if ".lora_B." in k]:
        prefix, rank = kb.split(".lora_B.")[0], t[kb.replace(".lora_B.", ".lora_A.")].shape[0]
        a = t.pop(f"{prefix}.alpha", None)
        alpha = float(a) if a is not None else file_alpha if file_alpha > 0 else rank
        t[kb] = (t[kb].float() * (alpha / rank)).to(t[kb].dtype)
    t = {k: v for k, v in t.items() if not k.endswith(".alpha")}
    meta = {k: v for k, v in meta.items() if k not in ("alpha", "lora_alpha", "network_alpha")}
    out.parent.mkdir(parents=True, exist_ok=True)
    for old in out.parent.glob(f"{src.stem}-*{src.suffix}"):  # an older copy of a replaced file
        old.unlink(missing_ok=True)
    tmp = out.with_name(f".{out.name}.{os.getpid()}.tmp")
    save_file(t, str(tmp), metadata=meta)
    tmp.replace(out)
    return out


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
            ctx.progress(p, 100, "sampling", "sample")
            if (
                p >= 99
            ):  # ponytail: the denoise's last report taken as the decode's start; SGLang names no decode
                ctx.progress(1, 1, "decoding", "decode_video")
            sent, seen = p, time.monotonic()
        if time.monotonic() - seen > IDLE_S:
            raise RunError(f"{ctx.node}: SGLang made no progress for {IDLE_S} s")
        time.sleep(POLL)


def generate(ctx, model, body: dict, out: Path, tmp: Path, lora: Path | None = None) -> None:
    """Start the partition's server (with `lora`: its LoRA), run `body`, write its MP4 to `out`."""
    if not SGLANG.is_file():
        raise RunError(
            f"{ctx.node}: SGLang is not installed (the runner-cuda-native image, Dockerfile.cuda target native)"
        )
    partition = "ref2va" if body["task"] == "ref2va" else "fl2va"
    ctx.progress(1, 1, "starting sglang", "encode")  # a phase without steps: 1 of 1
    lora = ready_lora(lora) if lora else None
    with child.serve(ctx, argv(model, partition, free_port(), tmp, lora), cwd=tmp, name="sglang",
                     ready=READY, ready_s=READY_S) as port:  # fmt: skip
        job = json.loads(call(ctx.node, port, "/v1/videos", body))["id"]
        wait(ctx, port, job)
        out.write_bytes(call(ctx.node, port, f"/v1/videos/{job}/content", timeout=600))
