"""The ltx pack against the old studio's goldens (tests/golden/ltx-*, recorded by tests/golden/record_ltx.py at the
repo root): runs the pack's workflows in-process (no NATS: graph.run with a real residency manager on the GPU) with a
golden's settings — text to video, image to video and keyframes (`ltx-image-to-video`), Prompt Relay beats, a baked
synthetic LoRA (rebuilt byte for byte by tests/golden/ltx_inputs.py) — then compares
- every recorded point (sha256 of the float32 bytes: equal only on the golden's mlx; else mean / std): the prompt
  embeddings, each stage's input state (noise, clean latent, denoise / keyframe masks, positions, relay mask) and
  output tokens, the upscaled latent, the keyframe image latents per stage, the LoRA-fused weights,
- the video: every decoded frame (rgb24) against the golden MP4's, per-frame PSNR (mean and worst), plus the first,
  middle and last frame PNGs; frame count, size, fps and codecs from ffprobe,
- the audio: the WAV before AAC (same sample count; log-mel spectrogram mean |diff| in dB; waveform SNR),
and reports per node its time and MLX peak memory (reset at each node). `auto-duration` runs only Gemma and the
DurationHead on the golden's prompts. Loads real weights: never alongside another process that holds a model (make
runners-stop first).

    uv run --project envs/mlx python tests/test_ltx_golden.py [case ...] [--reseed] [--out DIR]
    uv run --project envs/mlx pytest -m gpu -s tests/test_ltx_golden.py

--reseed (after the cases): the first case again with seed + 1 must not re-run encode."""

import argparse
import asyncio
import dataclasses
import hashlib
import json
import shutil
import subprocess
import sys
import tempfile
import time
import wave
from pathlib import Path

import numpy as np
import pytest

GOLDENS = Path(__file__).resolve().parent / "golden"
DIRS = {"t2v": "ltx-text-to-video", "i2v": "ltx-image-to-video", "kf": "ltx-image-to-video",
        "beats": "ltx-prompt-beats", "lora": "ltx-lora", "retake": "ltx-retake", "extend": "ltx-extend",
        "a2v": "ltx-audio-to-video", "nag": "ltx-nag", "detail": "ltx-detail"}  # fmt: skip
CASES = ["t2v-448x256-49f-s42", "t2v-512x320-57f-s7", "i2v-448x256-49f-s42", "kf-448x256-49f-s42",
         "beats-448x256-49f-s42", "lora-448x256-49f-s42", "lora-adapter-448x256-49f-s42", "nag-448x256-49f-s42",
         "auto-duration"]  # fmt: skip
# M7 pack 1 (the dev transformer, guided; 2-5 min each): `python tests/test_ltx_golden.py retake-… extend-… a2v-…`
DEV_CASES = ["retake-448x256-49f-s42", "extend-448x256-49f+8-s42", "a2v-448x256-49f-s42"]
# plan 19 23.10 phase 1 (the distilled transformer, no guidance; ~30 s each): recorded from ltx-2-studio main e6e15b7's
# `retake | extend | a2v --distilled` (tests/golden/record_ltx.py DISTILLED_CASES); matched on frames and the WAV
DISTILLED_CASES = ["retake-distilled-448x256-49f-s42", "extend-distilled-448x256-49f+48-s42",
                   "a2v-distilled-448x256-49f-s42"]  # fmt: skip
# plan 19 phases 4 and 4b, Recipe Detail (DFR; fork main e6e15b7 with LTX2_LORA_MODE=unfused, as the detailer is an
# adapter here): text to video at 49 frames (no padding) and 57 (padded to 65), audio to video at 49 and 57; matched as
# the distilled ones (DISTILLED_BAR_DB)
DETAIL_CASES = ["detail-448x256-49f-s42", "detail-512x320-57f-s7", "a2v-detail-448x256-49f-s42",
                "a2v-detail-448x256-57f-s42"]  # fmt: skip
DEV = ("retake", "extend", "a2v")
INPUTS = (
    Path(__file__).resolve().parents[2] / "tests" / "golden"
)  # ltx_inputs.py: the synthetic LoRA


def golden_dir(name: str) -> Path:
    return GOLDENS / DIRS[name.split("-")[0]]


def detailed(nodes: dict) -> None:
    """The workflow's Recipe: Detail (its `detail` node on, both samples' detail pass)."""
    nodes["detail"].pop("bypass", None)
    for s in ("sample1", "sample2"):
        nodes[s]["params"]["detail"] = True


def unbypassed(nodes: dict, schemas: dict) -> dict:
    """The graph without its bypassed nodes, each consumer wired to the input the node passes through, as studio
    splices a run (Recipe Two-stage's `detail`)."""
    for nid in [n for n, x in nodes.items() if x.get("bypass")]:
        x = nodes.pop(nid)
        schema = schemas[x["kind"]]
        types = {i["name"]: i["type"] for i in schema["inputs"]}
        through = {
            o["name"]: x["in"][next(i for i, t in types.items() if t == o["type"])]
            for o in schema["outputs"]
        }
        for n in nodes.values():
            n["in"] = {p: through.get(r.partition(".")[2], r) if r.partition(".")[0] == nid else r
                       for p, r in n.get("in", {}).items()}  # fmt: skip
    return nodes


# On the goldens' mlx (0.32.2) every latent must be bit-exact (measured: it is, PSNR inf, WAV identical). On another
# mlx the port is just as faithful but kernels differ (0.32.4: masked SDPA at head_dim 256 in Gemma, the compiled
# nn.silu, bf16 matmuls with a ragged M; GOTCHAS.md) and the 8-step ancestral stage 1 amplifies the 0.3-1 % change of
# the first DiT call to 10-19 % of the final latent. Bars per case, just under what 0.32.4 measured (mean / worst
# frame dB, log-mel dB): 448x256x49 23.09 / 18.72 / 1.133; 512x320x57 32.46 / 30.78 / 0.834. A new mlx may drift
# differently: re-measure and re-set these, after checking the 0.32.2 run is still bit-exact.
# Re-set 2026-11-22 after the 0.18 sync (goldens re-recorded on ltx-2-studio 4b2f609; the pack bit-exact on 0.32.2
# against every one of them, PSNR inf, WAV equal): stage 2 is ancestral too now, which grows the 0.32.4 drift.
# Measured: t2v 448 23.48 / 18.75 / 1.267, 512 32.42 / 30.75 / 0.867; i2v 37.92 / 32.05 / 1.536; kf 43.54 / 42.69 /
# 1.513; beats 23.01 / 16.65 / 1.472; LoRA 22.64 / 18.49 / 2.140.
BARS = {
    "t2v-448x256-49f-s42": {"video_mean_db": 22.5, "video_worst_db": 18.0, "logmel_db": 1.35},
    "t2v-512x320-57f-s7": {"video_mean_db": 31.5, "video_worst_db": 30.0, "logmel_db": 0.95},
    # stage B, 0.32.4 measured 2026-11-15 (mean / worst dB, log-mel dB): the images anchor the clip, so i2v / kf
    # drift little (re-recorded 2026-11-20 with the frame-0 keyframe marker, GOTCHAS: 41.74 / 38.98 / 1.466;
    # 43.45 / 42.60 / 1.489); beats 23.25 / 16.53 / 1.526; the LoRA clip
    # 26.91 / 23.73 / 1.154 (its fused weights are bit-equal on 0.32.4 too: quantize / dequantize did not change)
    "i2v-448x256-49f-s42": {"video_mean_db": 37.5, "video_worst_db": 31.5, "logmel_db": 1.6},
    "kf-448x256-49f-s42": {"video_mean_db": 42.5, "video_worst_db": 41.5, "logmel_db": 1.6},
    "beats-448x256-49f-s42": {"video_mean_db": 22.5, "video_worst_db": 16.0, "logmel_db": 1.65},
    "lora-448x256-49f-s42": {"video_mean_db": 22.0, "video_worst_db": 18.0, "logmel_db": 2.25},
    # plan 19 phase 2 (2026-11-22, recorded on ltx-2-studio main e6e15b7; bit-exact on 0.32.2): beats re-recorded
    # with the audio relay on, 0.32.4 25.37 / 21.60 / 1.001 (its bar above holds); NAG at float16 25.23 / 18.44 / 1.048
    "nag-448x256-49f-s42": {"video_mean_db": 24.5, "video_worst_db": 18.0, "logmel_db": 1.15},
    # plan 19 phase 3 (2026-11-22, fork main e6e15b7 with LTX2_LORA_MODE=unfused; bit-exact on 0.32.2): the synthetic
    # LoRA as an adapter, 0.32.4 24.67 / 23.31 / 1.242
    "lora-adapter-448x256-49f-s42": {
        "video_mean_db": 24.0,
        "video_worst_db": 22.5,
        "logmel_db": 1.35,
    },
}


def stats(a) -> dict:
    import mlx.core as mx

    x = np.array(a.astype(mx.float32))
    return {"mean": round(float(x.mean()), 6), "std": round(float(x.std()), 6),
            "sha256_f32": hashlib.sha256(x.tobytes()).hexdigest()}  # fmt: skip


def opt(a) -> dict | None:
    return None if a is None else stats(a)


class Bench:
    """The mlx registry and node implementations (recording what ran, its time, MLX peak and the handles' stats;
    each sampler loop's input state and output; the keyframe image latents; the LoRA-fused weights), the cache and
    the residency of one runner, and the inputs a run fetches (golden PNGs as image takes, the synthetic LoRA)."""

    def __init__(self, out: Path) -> None:
        import mlx.core as mx

        from core import graph, registry
        from core import loaders_mlx as lm
        from core.residency import Residency, ram_gb
        from packs.ltx import mlx as ltx

        self.mx, self.graph, self.lm, self.ltx, self.out = mx, graph, lm, ltx, out
        values, impls, packs = registry.build("mlx")
        self.schemas = registry.node_schemas(values)
        self.pack = next(p for p in packs if p.family == "ltx")
        self.workflows = {w["id"]: w for w in self.pack.workflows}
        self.raw_impls = impls
        self.impls = {k: dataclasses.replace(n, fn=self._record(n.fn)) for k, n in impls.items()}
        self.residency = Residency(ram_gb() - 12, free=lm.free, measure=lm.measure_gb)
        self.cache = graph.Cache()
        self.residency.on_short.append(self.cache.shed)
        self.wav: Path | None = None
        self.files: dict[str, Path] = {}  # object key → local file (image takes, the LoRA)
        self.stages, self.images, self.fused, self.loads = [], [], {}, {}
        self.held = 0  # the node's peak before a load or loop inside it reset MLX's counter (bytes)
        save, denoise, conds, load, guided = (
            ltx._save_wav,
            ltx._denoise,
            ltx._conditionings,
            lm.load_component,
            ltx._guided,
        )

        def save_and_keep(waveform, path):  # the WAV before AAC, for the audio compare
            save(waveform, path)
            if self.wav:
                shutil.copy(path, self.wav)

        def loop(
            ctx, x0, video, audio, cond, sigmas, phase, noise_seed, cross_mask=None, audio_mask=None
        ):
            row = {"video_in": stats(video.latent), "audio_in": stats(audio.latent),
                   "video_clean_in": stats(video.clean_latent), "video_denoise_mask": stats(video.denoise_mask),
                   "video_keyframes_mask": opt(video.keyframes_mask), "video_positions_stats": stats(video.positions),
                   "video_cross_attention_mask": opt(cross_mask), "audio_cross_attention_mask": opt(audio_mask),
                   "video_tokens_total": video.latent.shape[1]}  # fmt: skip
            self.reset_peak()  # the loop's own peak (the image encode before it is in the node's)
            vx, ax = denoise(
                ctx, x0, video, audio, cond, sigmas, phase, noise_seed, cross_mask, audio_mask
            )
            row |= {"video_out_tokens": stats(vx), "audio_out_tokens": stats(ax),
                    "loop_peak_gib": round(mx.get_peak_memory() / 2**30, 2)}  # fmt: skip
            self.stages.append(row)
            return vx, ax

        def guided_loop(ctx, x0, video, audio, cond, factories, sigmas, phase):
            row = {"video_in": stats(video.latent), "audio_in": stats(audio.latent),
                   "video_clean_in": stats(video.clean_latent), "video_denoise_mask": stats(video.denoise_mask),
                   "video_keyframes_mask": opt(video.keyframes_mask), "video_positions_stats": stats(video.positions),
                   "audio_clean_in": stats(audio.clean_latent), "audio_denoise_mask": stats(audio.denoise_mask),
                   "audio_positions": stats(audio.positions), "sigmas": [float(x) for x in sigmas],
                   "video_tokens_total": video.latent.shape[1]}  # fmt: skip
            self.reset_peak()
            vx, ax = guided(ctx, x0, video, audio, cond, factories, sigmas, phase)
            row |= {"video_out_tokens": stats(vx), "audio_out_tokens": stats(ax),
                    "loop_peak_gib": round(mx.get_peak_memory() / 2**30, 2)}  # fmt: skip
            self.stages.append(row)
            return vx, ax

        def conditionings(*a):
            items = conds(*a)
            self.images.append([stats(getattr(c, "keyframe_latent", None) if hasattr(c, "keyframe_latent")
                                      else c.clean_latent) for c in items])  # fmt: skip
            return items

        def load_component(model, component, ctx):
            before, t = mx.get_active_memory(), time.monotonic()
            self.reset_peak()
            module = load(model, component, ctx)
            self.loads[component] = {"s": round(time.monotonic() - t, 2),
                                     "peak_over_gib": round((mx.get_peak_memory() - before) / 2**30, 2),
                                     "kept_gib": round((mx.get_active_memory() - before) / 2**30, 2)}  # fmt: skip
            if model.baked(component):  # the fused weights, read when compared
                from mlx.utils import tree_flatten

                self.fused = dict(tree_flatten(module.parameters()))
            return module

        ltx._save_wav, ltx._denoise, ltx._conditionings, ltx._guided = (
            save_and_keep,
            loop,
            conditionings,
            guided_loop,
        )
        lm.load_component = load_component

    def reset_peak(self) -> None:
        """Resets MLX's peak counter for a load's or loop's own peak, keeping the node's peak so far."""
        self.held = max(self.held, self.mx.get_peak_memory())
        self.mx.reset_peak_memory()

    def _record(self, fn):
        def run(ctx, params, inputs):
            if ctx is None:  # graph.keys calls a model node with no ctx for its fingerprint
                return fn(ctx, params, inputs)
            mx, t = self.mx, time.monotonic()
            mx.reset_peak_memory()
            self.held = 0
            outs = fn(ctx, params, inputs)
            peak = max(self.held, mx.get_peak_memory())  # the node's, across its resets
            row = {"s": round(time.monotonic() - t, 2), "peak_gib": round(peak / 2**30, 2),
                   "active_after_gib": round(mx.get_active_memory() / 2**30, 2)}  # fmt: skip
            for v in outs.values():
                if hasattr(v, "audio") and hasattr(v.audio, "dtype"):  # Cond / Latent
                    row["handle"] = v
                    row["video"], row["audio"] = opt(v.video), stats(v.audio)
                    if getattr(v, "neg_video", None) is not None:  # encode-guided
                        row["neg_video"], row["neg_audio"] = stats(v.neg_video), stats(v.neg_audio)
                    if (
                        getattr(v, "nag_video", None) is not None
                    ):  # encode with a negative prompt (NAG)
                        row["nag_video"], row["nag_audio"] = stats(v.nag_video), stats(v.nag_audio)
            self.rows[ctx.node] = row
            return outs

        return run

    def image(self, path: Path) -> dict:
        """A golden PNG as a resolved Image value (an image take of the owner `golden`)."""
        from PIL import Image

        key = f"u/golden/takes/{path.name}"
        self.files[key] = path
        data = path.read_bytes()
        with Image.open(path) as im:
            w, h = im.size
        return {"take_id": "00000000-0000-4000-8000-000000000001", "key": key, "content_type": "image/png",
                "width": w, "height": h, "bytes": len(data), "etag": hashlib.md5(data).hexdigest()}  # fmt: skip

    def take(self, path: Path, **facts) -> dict:
        """A golden file as a resolved media value (defs media) of the owner `golden`."""
        key = f"u/golden/takes/{path.name}"
        self.files[key] = path
        data = path.read_bytes()
        return {"take_id": "00000000-0000-4000-8000-000000000002", "key": key, "bytes": len(data),
                "etag": hashlib.md5(data).hexdigest()} | facts  # fmt: skip

    def dev_nodes(self, case: dict) -> dict:
        """ltx-retake / ltx-extend / ltx-audio-to-video with the golden's settings: the t2v golden MP4 as the
        source video (the span = its latent [start, end) as seconds on whole frames), or the tone WAV as the audio
        part from 0 for the clip's frames (studio snaps audio to the millisecond)."""
        kind = case["kind"]
        wf = {"retake": "ltx-retake", "extend": "ltx-extend", "a2v": "ltx-audio-to-video"}[kind]
        nodes = json.loads(json.dumps(self.workflows[wf]["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        nodes["model"]["params"]["precision"] = f"int{case['quantize_on_load']}"
        # the templates default to Distilled from API 1.39: a dev golden asks for Dev
        nodes["model"]["params"]["variant"] = (
            "distilled" if "-distilled-" in case["name"] or case.get("dfr") else "dev"
        )
        if case.get("dfr"):
            detailed(nodes)
        if kind == "a2v":
            f = GOLDENS / "ltx-audio-to-video" / case["inputs"]["audio"]["file"]
            out = round(case["frames"] / case["fps"], 3)
            nodes["audio"]["params"]["source"] = self.take(f, kind="audio", content_type="audio/wav", has_audio=True,
                                                           duration_s=case["inputs"]["audio"]["seconds"],
                                                           **{"in": case["audio_start"], "out": out})  # fmt: skip
            nodes["canvas"]["params"] |= {"width": case["width"], "height": case["height"]}
            del nodes["keyframes"]  # bypassed: studio splices it out, its consumers take its input
            for n in nodes.values():
                n["in"] = {
                    p: "encode.cond" if r == "keyframes.cond" else r
                    for p, r in n.get("in", {}).items()
                }
            return nodes
        src = GOLDENS.parent.parent.parent / case["inputs"]["video"]["file"]
        meta = (case.get("source") or {}).get("meta") or {
            "width": 448,
            "height": 256,
            "frame_rate": 24.0,
            "num_frames": 49,
        }
        fps, n = meta["frame_rate"], meta["num_frames"]
        nodes["source"]["params"]["source"] = self.take(src, kind="video", content_type="video/mp4", has_audio=True,
                                                        width=meta["width"], height=meta["height"], fps=fps,
                                                        frames=n, duration_s=n / fps)  # fmt: skip
        if kind == "retake":
            nodes["retake"]["params"] |= {"span": {"start": 8 * case["start"] / fps, "end": 8 * case["end"] / fps},
                                          "audio": case["regenerate_audio"]}  # fmt: skip
        else:
            nodes["extend"]["params"] |= {"seconds": round(8 * case["extend_frames"] / fps - 0.03, 2),
                                          "direction": case["direction"]}  # fmt: skip
        return nodes

    def prefuse(self) -> None:
        """registry.fill_prefused from this host's shared LoRA index (the runner does it at start), so ltx/distill
        finds the distilled LoRA; the library's hashes are cached (no 8.9 GB re-hash)."""
        import config
        from core import lora, registry
        from core import pack as packs

        self.library = lora.Library(config.MODELS, config.CACHE)
        self.library.scan(registry.lora_blocks([self.pack]))
        block, root = packs.lora_block(self.pack), packs.models_root()
        block["prefused"] = [{"lora_id": self.library.files[f], "variant": x["variant"]}
                             for x in self.pack.spec["lora"]["prefused"]
                             if (f := root / self.pack.spec["variants"][x["variant"]]["dir"] / x["file"]) in self.library.files]  # fmt: skip

    def lora(self, case: dict) -> dict:
        """The synthetic LoRA (ltx_inputs.synthetic_lora, rebuilt from the transformer header) as a shared LoRA row."""
        sys.path.insert(0, str(INPUTS))
        import ltx_inputs

        f = self.out / "ltx-synthetic-lora-r8.safetensors"
        dit = self.pack_file("diffusion_models/ltx-2.5-22b-distilled-transformer-bf16.safetensors")
        meta = ltx_inputs.synthetic_lora(f, dit)
        assert meta["sha256"] == case["lora"]["sha256"], "the synthetic LoRA is not the golden's"
        self.files[meta["sha256"]] = f
        value = {"lora_id": meta["sha256"], "source": "shared", "name": f.stem, "sha256": meta["sha256"],
                 "bytes": meta["bytes"]}  # fmt: skip
        return {"lora": value, "strength": case["lora_strength"],
                "mode": "adapter" if case.get("lora_mode") == "unfused" else "bake"}  # fmt: skip

    def pack_file(self, rel: str) -> Path:
        from core.pack import models_root

        return models_root() / "ltx-2.5" / rel

    def nodes(self, case: dict) -> dict:
        kind = case["name"].split("-")[0]
        wf = "ltx-image-to-video" if kind in ("i2v", "kf") else "ltx-text-to-video"
        nodes = json.loads(json.dumps(self.workflows[wf]["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        nodes["canvas"]["params"] |= {"width": case["width"], "height": case["height"]}
        nodes["duration"]["params"] |= {"frames": case["frames"], "fps": round(case["fps"])}
        nodes["model"]["params"]["precision"] = f"int{case['quantize_on_load']}"
        for im in (
            case.get("images") or []
        ):  # frame f is at f / fps seconds: round(s * fps) gives f back
            nodes["keyframes"]["params"]["images"].append({"image": self.image(golden_dir(case["name"]) / im["file"]),
                                                           "seconds": im["frame_idx"] / case["fps"],
                                                           "strength": im["strength"]})  # fmt: skip
        for sg in (
            case.get("segments") or []
        ):  # beats in latent frames → seconds (round(s * fps / 8) gives them)
            nodes["encode"]["params"]["beats"].append({"text": sg["text"],
                                                       "seconds": sg["length"] * 8 / case["fps"] if sg["length"] else 0})  # fmt: skip
        if case.get("lora"):
            nodes["lora"]["params"]["rows"] = [self.lora(case)]
        if case.get(
            "negative_prompt"
        ):  # plan 19 phase 2: NAG, at the Fast preset's float16 compute
            nodes["encode"]["params"]["negative"] = case["negative_prompt"]
        if case.get("compute"):
            nodes["model"]["params"]["compute"] = case["compute"]
        if case.get("dfr"):
            detailed(nodes)
        return nodes

    def ctx(self, progress=None, output=None):
        g = self.graph

        async def fetch(key, limit, etag):
            return self.files[key].read_bytes()

        async def none(*a, **k):
            pass

        ctx = g.Ctx(progress=progress or none, output=output or none, check=lambda: None, owner="golden",
                    residency=self.residency, device=self.lm.device())  # fmt: skip
        ctx.fetch = fetch
        files = self.files

        library = getattr(self, "library", None)

        class Loras:  # the host's LoRA library: the synthetic LoRA by sha256, else the shared index
            async def path(self, use, owner, progress=None):
                return files.get(use.sha256) or library.shared[use.sha256]

        ctx.loras = Loras()
        return ctx

    def run(self, nodes: dict, wav: Path | None = None) -> dict:
        g = self.graph
        nodes = unbypassed(nodes, self.schemas)
        for n in (
            nodes.values()
        ):  # studio merges the schema's defaults in (a template may leave one out: compute)
            n["params"] = {p["name"]: p["default"] for p in self.schemas[n["kind"]]["params"] if "default" in p} | n.get("params", {})  # fmt: skip
        assert not g.validate(nodes, self.schemas), g.validate(nodes, self.schemas)
        nodes, _ = g.resolve(nodes, self.schemas)
        self.rows, self.wav, out = {}, wav, {"mp4": None, "phases": []}
        self.stages, self.images, self.fused = [], [], {}

        async def progress(node, step, of, phase, eta):
            out["phases"].append((node, phase, step, of))

        async def output(index, data, **meta):
            out["mp4"], out["meta"] = data, meta

        ctx = self.ctx(progress, output)
        t = time.monotonic()
        try:
            asyncio.run(g.run(nodes, self.impls, self.schemas, ctx, self.cache))
        finally:
            self.residency.done()
        out["total_s"] = round(time.monotonic() - t, 1)
        out["nodes"] = self.rows
        out["resident_gb"] = round(self.residency.used_gb, 2)
        return out

    def variant(self, which: str) -> dict:
        """auto: the clip has the predicted frames and the output reports them as resolved.params; noaudio: no
        audio track and the frames of the golden (bit-exact on its mlx: the DiT generates audio either way)."""
        name = "t2v-448x256-49f-s42"
        case = json.loads((golden_dir(name) / f"{name}.json").read_text())
        nodes = self.nodes(case)
        if which == "auto":
            nodes["duration"]["params"]["frames"] = "auto"
        else:
            nodes["decode"]["params"]["audio"] = False
        r = self.run(nodes)
        mp4 = self.out / f"{which}.mp4"
        mp4.write_bytes(r["mp4"])
        got, probe = frames(mp4)
        res = {"total_s": r["total_s"], "frames": len(got), "streams": [s["codec_type"] for s in probe],
               "resolved": r["meta"].get("resolved"), "nodes": {k: v["s"] for k, v in r["nodes"].items() if v["s"]}}  # fmt: skip
        if which == "auto":
            res["pass"] = len(got) == 65 and res["resolved"] == {"params": {"duration.frames": 65}}
        else:
            want, _ = frames(golden_dir(name) / f"{name}.mp4")
            per = [psnr(a, b) for a, b in zip(got, want, strict=True)]
            res["video_worst_db"] = min(per)
            res["pass"] = res["streams"] == ["video"] and (min(per) == float("inf") or case["versions"]["mlx"] != self.mx.__version__)  # fmt: skip
        return res

    def durations(self) -> dict:
        """DurationHead on the auto-duration golden's prompts: the embeddings and the raw seconds (bit-exact on the
        golden's mlx), and the frames per setting (the old studio's, clamped by our max 321: contracts auto_frames)."""
        from core.pack import ModelRef

        g = json.loads((GOLDENS / "ltx-auto-duration" / "auto-duration.json").read_text())
        ltx, model = self.ltx, ModelRef(self.pack, "distilled", "int8")
        ctx = self.ctx()
        ctx.progress = lambda *a, **k: None  # no event loop here
        ctx.node, ctx.impl = "encode", self.raw_impls["ltx/encode"]
        t, rows, ok = time.monotonic(), [], True
        self.mx.reset_peak_memory()
        for row in g["rows"]:
            cond = ltx.encode(ctx, {"beats": []}, {"model": model, "prompt": row["prompt"]})["cond"]
            p = self.pack_file(ltx.DURATION_HEAD)
            seconds = float(ltx._duration_head(p, p.stat().st_size, p.stat().st_mtime_ns)(
                video_tokens=cond.video, audio_tokens=cond.audio).item())  # fmt: skip
            got = {"embeds_exact": stats(cond.video)["sha256_f32"] == row["video_embeds"]["sha256_f32"],
                   "seconds": [seconds, row["predictions"][0]["raw_seconds"]], "frames": []}  # fmt: skip
            for pr in row["predictions"]:
                spec = ltx.FRAMES | {"auto": {"min_s": pr["min_s"], "max_s": pr["max_s"]}}
                got["frames"].append(
                    [self.graph.auto_frames(seconds, pr["fps"], spec), pr["frames"]]
                )
            # on the golden's mlx exact; on another the seconds move by a bf16 step or so (0.32.4: 2.92 vs 2.89 s),
            # which may cross a grid point: one step (8 frames) of slack
            exact = g["versions"]["mlx"] == self.mx.__version__
            got["frames_ok"] = all(
                abs(a - min(b, 321)) <= (0 if exact else 8) for a, b in got["frames"]
            )
            ok &= got["frames_ok"] and abs(seconds / row["predictions"][0]["raw_seconds"] - 1) <= (
                1e-6 if exact else 0.02
            )
            rows.append(got)
        self.residency.unload()
        return {"rows": rows, "s": round(time.monotonic() - t, 1),
                "peak_gib": round(self.mx.get_peak_memory() / 2**30, 2), "pass": bool(ok)}  # fmt: skip


def frames(mp4: Path) -> tuple[np.ndarray, dict]:
    """Every frame as rgb24 [n, h, w, 3], and the ffprobe of the file's streams."""
    probe = json.loads(subprocess.run(
        ["ffprobe", "-v", "error", "-show_entries",
         "stream=codec_type,codec_name,profile,pix_fmt,width,height,r_frame_rate,nb_frames,sample_rate,channels",
         "-of", "json", str(mp4)], check=True, capture_output=True, text=True).stdout)["streams"]  # fmt: skip
    v = next(s for s in probe if s["codec_type"] == "video")
    raw = subprocess.run(["ffmpeg", "-v", "error", "-i", str(mp4), "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
                         check=True, capture_output=True).stdout  # fmt: skip
    return np.frombuffer(raw, np.uint8).reshape(-1, v["height"], v["width"], 3), probe


def psnr(a: np.ndarray, b: np.ndarray) -> float:
    mse = float(np.mean((a.astype(np.float64) - b.astype(np.float64)) ** 2))
    return float("inf") if mse == 0 else 10 * np.log10(255**2 / mse)


def read_wav(path: Path) -> np.ndarray:
    with wave.open(str(path)) as w:
        return (
            np.frombuffer(w.readframes(w.getnframes()), np.int16).reshape(-1, w.getnchannels())
            / 32768.0
        )


def logmel(
    x: np.ndarray, sr: int = 48000, n_fft: int = 2048, hop: int = 480, mels: int = 128
) -> np.ndarray:
    """Log-mel power spectrogram in dB of a mono signal (Hann window, HTK mel scale)."""
    win = np.hanning(n_fft)
    pad = np.pad(x, n_fft // 2, mode="reflect")
    n = 1 + (len(pad) - n_fft) // hop
    spec = (
        np.abs(
            np.fft.rfft(np.stack([pad[i * hop : i * hop + n_fft] * win for i in range(n)]), axis=1)
        )
        ** 2
    )
    mel = lambda f: 2595 * np.log10(1 + f / 700)
    pts = 700 * (10 ** (np.linspace(mel(0), mel(sr / 2), mels + 2) / 2595) - 1)
    bins = np.fft.rfftfreq(n_fft, 1 / sr)
    fb = np.zeros((mels, len(bins)))
    for m in range(mels):
        lo, mid, hi = pts[m : m + 3]
        fb[m] = np.clip(np.minimum((bins - lo) / (mid - lo), (hi - bins) / (hi - mid)), 0, None)
    return 10 * np.log10(np.maximum(spec @ fb.T, 1e-10))


def compare(name: str, r: dict, out: Path, bench: Bench) -> dict:
    GOLDEN = golden_dir(name)
    g = json.loads((GOLDEN / f"{name}.json").read_text())
    mp4 = out / f"{name}.mp4"
    mp4.write_bytes(r["mp4"])
    got, probe = frames(mp4)
    want, gprobe = frames(GOLDEN / f"{name}.mp4")
    per = [psnr(a, b) for a, b in zip(got, want, strict=True)] if got.shape == want.shape else []
    picks = {k: v["index"] for k, v in g["frames_png"].items()}
    from PIL import Image

    pngs = {k: round(psnr(got[i], np.asarray(Image.open(GOLDEN / f"{name}-{k}.png").convert("RGB"))), 2)
            for k, i in picks.items()} if per else {}  # fmt: skip
    a, b = read_wav(out / f"{name}.wav"), read_wav(GOLDEN / f"{name}.wav")
    audio = {"samples": [a.shape[0], b.shape[0]]}
    if a.shape == b.shape:
        audio["logmel_db"] = round(float(np.mean([np.abs(logmel(a[:, c]) - logmel(b[:, c])).mean()
                                                   for c in range(a.shape[1])])), 3)  # fmt: skip
        noise = float(np.sum((a - b) ** 2))
        audio["snr_db"] = (
            round(10 * np.log10(float(np.sum(b**2)) / noise), 2) if noise else float("inf")
        )
    stages = g["stages"]
    points = {  # the golden's recorded points vs this run's handles (a node that was a cache hit has none)
        "embeds": (g["prompt_embeds"]["video"], "encode", "video"),
        "stage1": (stages[0]["video_out_tokens"], "sample1", "video"),
        "stage1_audio": (stages[0]["audio_out_tokens"], "sample1", "audio"),
        "upscaled": (stages[1]["video_upscaled"], "upscale", "video"),
        "stage2": (stages[1]["video_out_tokens"], "sample2", "video"),
        "stage2_audio": (stages[1]["audio_out_tokens"], "sample2", "audio"),
    }
    if g.get("images"):  # the golden's stage-2 clean latent is the upscaled one after conditioning
        del points["upscaled"]
    if g.get("nag"):  # the negative prompt's embeddings
        points["nag_embeds"] = (g["nag"]["embeds"]["video"], "encode", "nag_video")
    latents = {k: (w, r["nodes"][n][x]) for k, (w, n, x) in points.items() if n in r["nodes"]}
    for i, (wrow, hrow) in enumerate(
        zip(stages, bench.stages, strict=True), 1
    ):  # each loop's state
        for k in ("video_in", "audio_in", "video_clean_in", "video_denoise_mask", "video_keyframes_mask",
                  "video_positions_stats", "video_cross_attention_mask", "audio_cross_attention_mask"):  # fmt: skip
            if k in wrow:
                latents[f"loop{i}.{k}"] = (wrow[k], hrow[k])
    for i, row in enumerate(
        g.get("image_conditionings") or [], 1
    ):  # the keyframe image latents per stage
        for j, (it, have) in enumerate(zip(row["items"], bench.images[i - 1], strict=True), 1):
            latents[f"images{i}.{j}"] = (it["latent"], have)
    for m, w in ((g.get("lora") or {}).get("weights") or {}).items():  # the fused weights
        for part, wst in w.items():
            latents[f"lora.{m}.{part}"] = (wst, stats(bench.fused[f"{m}.{part}"]))
    same = lambda w, h: (w is None and h is None) or (w is not None and h is not None and w["sha256_f32"] == h["sha256_f32"])  # fmt: skip
    lat = {k: {"exact": same(w, h)} | ({"mean": [w["mean"], h["mean"]], "std": [w["std"], h["std"]]} if w and h else {})
           for k, (w, h) in latents.items()}  # fmt: skip
    streams = lambda p: [(s["codec_type"], s["codec_name"], s.get("profile"), s.get("pix_fmt"), s.get("width"),
                          s.get("height"), s.get("r_frame_rate"), s.get("nb_frames"), s.get("sample_rate"),
                          s.get("channels")) for s in p]  # fmt: skip
    res = {
        "video": {
            "frames": [len(got), len(want)],
            "mean_db": round(float(np.mean(per)), 2) if per else None,
            "worst_db": round(float(np.min(per)), 2) if per else None,
            "pngs_db": pngs,
            "same_streams": streams(probe) == streams(gprobe),
        },
        "audio": audio,
        "latents": lat,
    }
    if g["versions"]["mlx"] == r["mlx"]:  # the golden's own stack: bit-exact or fail
        res["pass"] = (
            bool(per) and all(v["exact"] for v in lat.values()) and min(per) == float("inf")
        )
        return res
    if name not in BARS:  # not measured on this mlx yet: report only
        res["pass"] = False
        return res
    bars = BARS[name]
    res["pass"] = bool(per and res["video"]["mean_db"] >= bars["video_mean_db"]
                       and res["video"]["worst_db"] >= bars["video_worst_db"] and res["video"]["same_streams"]
                       and audio.get("logmel_db", 99) <= bars["logmel_db"])  # fmt: skip
    return res


# MLX 0.32.4 against the M7 dev goldens (recorded on 0.32.2): bars set just under what it measured (GOTCHAS.md).
# Measured 2026-11-19 (mean / worst frame dB, log-mel dB): retake 36.06 / 34.50 / 0.248; extend 36.13 / 26.65 / 0.331
# (the 8 new frames drift most: its worst is the last frame); a2v 24.50 / 20.79 / 0.000 (the source audio is kept
# as is, so its WAV is identical; the 8-step ancestral stage 1 amplifies the kernel drift as in t2v). Re-set
# 2026-11-22 after the 0.18 sync (re-recorded on 4b2f609, bit-exact on 0.32.2): retake 35.28 / 33.90 / 0.321; extend
# 35.36 / 28.09 / 0.340; a2v 27.88 / 24.17 / 0.000.
DEV_BARS: dict = {
    "retake-448x256-49f-s42": {"video_mean_db": 35.0, "video_worst_db": 33.5, "logmel_db": 0.35},
    "extend-448x256-49f+8-s42": {"video_mean_db": 35.0, "video_worst_db": 26.0, "logmel_db": 0.45},
    "a2v-448x256-49f-s42": {"video_mean_db": 24.0, "video_worst_db": 20.0, "logmel_db": 0.1},
}


def compare_dev(name: str, r: dict, out: Path, bench: Bench) -> dict:
    """A dev golden (retake, extend, a2v): every recorded point (prompt and negative embeddings, the source's video
    and audio latents, each loop's input state, sigmas and outputs, the final latents), the frames and the WAV."""
    import mlx.core as mx
    from ltx_core_mlx.components.patchifiers import AudioPatchifier
    from PIL import Image

    g = json.loads((golden_dir(name) / f"{name}.json").read_text())
    ltx, nodes = bench.ltx, r["nodes"]
    mp4 = out / f"{name}.mp4"
    mp4.write_bytes(r["mp4"])
    got, probe = frames(mp4)
    want, gprobe = frames(golden_dir(name) / f"{name}.mp4")
    per = [psnr(a, b) for a, b in zip(got, want, strict=True)] if got.shape == want.shape else []
    pngs = {k: round(psnr(got[v["index"]], np.asarray(Image.open(golden_dir(name) / v["file"]).convert("RGB"))), 2)
            for k, v in g["frames_png"].items()} if per else {}  # fmt: skip
    a, b = read_wav(out / f"{name}.wav"), read_wav(golden_dir(name) / g["wav"]["file"])
    audio = {"samples": [a.shape[0], b.shape[0]]}
    if a.shape == b.shape:
        audio["logmel_db"] = round(float(np.mean([np.abs(logmel(a[:, c]) - logmel(b[:, c])).mean()
                                                   for c in range(a.shape[1])])), 3)  # fmt: skip
        noise = float(np.sum((a - b) ** 2))
        audio["snr_db"] = (
            round(10 * np.log10(float(np.sum(b**2)) / noise), 2) if noise else float("inf")
        )
    points = {}
    if "encode" in nodes:
        for k in ("video", "audio", "neg_video", "neg_audio"):
            points[f"embeds.{k}"] = (g["prompt_embeds"][k], nodes["encode"][k])
    unpatch = lambda h: ltx.PATCH.unpatchify(h.video[:, : h.fhw[0] * h.fhw[1] * h.fhw[2], :], h.fhw)
    if g.get("source") and "source" in nodes:
        h = nodes["source"]["handle"]
        points["source.video"] = (g["source"]["video_latent"], stats(unpatch(h)))
        points["source.audio"] = (
            g["source"]["audio_latent"],
            stats(AudioPatchifier().unpatchify(h.audio)),
        )
    last = nodes.get("retake") or nodes.get("extend") or nodes.get("sample2")
    if last:
        points["final.video"] = (g["final_video_latent"], stats(unpatch(last["handle"])))
        if g.get("final_audio_latent"):
            points["final.audio"] = (
                g["final_audio_latent"],
                stats(AudioPatchifier().unpatchify(last["handle"].audio)),
            )
    keys = {("video_in", "latent"): "video_in", ("video_in", "clean_latent"): "video_clean_in",
            ("video_in", "denoise_mask"): "video_denoise_mask", ("video_in", "keyframes_mask"): "video_keyframes_mask",
            ("video_in", "positions"): "video_positions_stats", ("audio_in", "latent"): "audio_in",
            ("audio_in", "clean_latent"): "audio_clean_in", ("audio_in", "denoise_mask"): "audio_denoise_mask",
            ("audio_in", "positions"): "audio_positions"}  # fmt: skip
    sig = []
    for i, (wl, hl) in enumerate(zip(g["loops"], bench.stages, strict=True), 1):
        for (part, k), ours in keys.items():
            if ours in hl:
                points[f"loop{i}.{part}.{k}"] = (wl[part][k], hl[ours])
        points[f"loop{i}.video_out"] = (wl["video_out"], hl["video_out_tokens"])
        points[f"loop{i}.audio_out"] = (wl["audio_out"], hl["audio_out_tokens"])
        if "sigmas" in hl:
            sig.append(max(abs(x - y) for x, y in zip(wl["sigmas"], hl["sigmas"], strict=True)))
    same = lambda w, h: (w is None and h is None) or (w is not None and h is not None and w["sha256_f32"] == h["sha256_f32"])  # fmt: skip
    lat = {k: {"exact": same(w, h)} | ({"mean": [w["mean"], h["mean"]], "std": [w["std"], h["std"]]} if w and h else {})
           for k, (w, h) in points.items()}  # fmt: skip
    res = {"video": {"frames": [len(got), len(want)], "mean_db": round(float(np.mean(per)), 2) if per else None,
                     "worst_db": round(float(np.min(per)), 2) if per else None, "pngs_db": pngs,
                     "same_streams": [s["codec_type"] for s in probe] == [s["codec_type"] for s in gprobe]},
           "audio": audio, "sigmas_max_diff": max(sig, default=None), "latents": lat}  # fmt: skip
    if g["versions"]["mlx"] == mx.__version__:
        res["pass"] = bool(per) and all(v["exact"] for v in lat.values()) and min(per) == float("inf") and \
            audio.get("snr_db") == float("inf")  # fmt: skip
        return res
    bars = DEV_BARS.get(name)
    res["pass"] = bool(bars and per and res["video"]["mean_db"] >= bars["video_mean_db"]
                       and res["video"]["worst_db"] >= bars["video_worst_db"] and res["video"]["same_streams"]
                       and audio.get("logmel_db", 99) <= bars["logmel_db"])  # fmt: skip
    return res


# On the goldens' mlx (0.32.2) every frame >= 40 dB from the fork's render (the plan's bar, 23.8; measured
# 2026-11-22: bit-exact, PSNR inf and the WAV equal, all three). On another mlx the bars sit just under what 0.32.4
# measured (mean / worst frame dB, log-mel dB): retake 35.45 / 33.51 / 0.407; extend 28.34 / 17.11 / 0.898 (the new
# part drifts most, ancestral); a2v 22.48 / 20.89 / 0.000 (the source audio is kept).
DISTILLED_BAR_DB = 40.0
DISTILLED_BARS = {
    "retake-distilled-448x256-49f-s42": {
        "video_mean_db": 35.0,
        "video_worst_db": 33.0,
        "logmel_db": 0.45,
    },
    "extend-distilled-448x256-49f+48-s42": {
        "video_mean_db": 28.0,
        "video_worst_db": 16.5,
        "logmel_db": 0.95,
    },
    "a2v-distilled-448x256-49f-s42": {
        "video_mean_db": 22.0,
        "video_worst_db": 20.5,
        "logmel_db": 0.1,
    },
    # plan 19 phases 4 and 4b (2026-11-22, fork main e6e15b7 with LTX2_LORA_MODE=unfused; bit-exact on 0.32.2, PSNR
    # inf and the WAV equal, all four); 0.32.4 measured 25.49 / 22.68 / 0.816, 28.17 / 26.94 / 0.881 (the padded
    # 57-frame canvas), a2v 25.70 / 24.56 / 0.000 and 26.30 / 25.20 / 0.000 (padded)
    "detail-448x256-49f-s42": {"video_mean_db": 25.0, "video_worst_db": 22.0, "logmel_db": 0.9},
    "detail-512x320-57f-s7": {"video_mean_db": 27.5, "video_worst_db": 26.5, "logmel_db": 0.95},
    "a2v-detail-448x256-49f-s42": {"video_mean_db": 25.0, "video_worst_db": 24.0, "logmel_db": 0.1},
    "a2v-detail-448x256-57f-s42": {"video_mean_db": 25.5, "video_worst_db": 24.5, "logmel_db": 0.1},
}


def compare_distilled(name: str, r: dict, out: Path) -> dict:
    """A distilled golden: every frame's PSNR against the fork's MP4 and the WAV (log-mel, SNR)."""
    import mlx.core as mx

    g = json.loads((golden_dir(name) / f"{name}.json").read_text())
    mp4 = out / f"{name}.mp4"
    mp4.write_bytes(r["mp4"])
    got, probe = frames(mp4)
    want, gprobe = frames(golden_dir(name) / f"{name}.mp4")
    per = [psnr(a, b) for a, b in zip(got, want, strict=True)] if got.shape == want.shape else []
    a, b = read_wav(out / f"{name}.wav"), read_wav(golden_dir(name) / g["wav"]["file"])
    audio = {"samples": [a.shape[0], b.shape[0]]}
    if a.shape == b.shape:
        audio["logmel_db"] = round(float(np.mean([np.abs(logmel(a[:, c]) - logmel(b[:, c])).mean()
                                                   for c in range(a.shape[1])])), 3)  # fmt: skip
        noise = float(np.sum((a - b) ** 2))
        audio["snr_db"] = (
            round(10 * np.log10(float(np.sum(b**2)) / noise), 2) if noise else float("inf")
        )
    worst = float(np.min(per)) if per else None
    return {"video": {"frames": [len(got), len(want)], "mean_db": round(float(np.mean(per)), 2) if per else None,
                      "worst_db": round(worst, 2) if per else None,
                      "same_streams": [s["codec_type"] for s in probe] == [s["codec_type"] for s in gprobe]},
            "audio": audio, "fork": {"total_s": g["timing_s"]["total_wall"], "memory": g["memory"]},
            "pass": bool(per) and bool(worst >= DISTILLED_BAR_DB if g["versions"]["mlx"] == mx.__version__ else
                                   (b := DISTILLED_BARS[name])["video_mean_db"] <= np.mean(per)
                                   and b["video_worst_db"] <= worst and audio.get("logmel_db", 99) <= b["logmel_db"])}  # fmt: skip


def main(argv=None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=CASES)
    ap.add_argument("--reseed", action="store_true")
    ap.add_argument("--out", type=Path, default=None)
    args = ap.parse_args(argv)
    out = args.out or Path(tempfile.mkdtemp(prefix="ltx-golden-"))
    out.mkdir(parents=True, exist_ok=True)
    import mlx.core as mx

    bench, ok, report = Bench(out), True, {"mlx": mx.__version__}
    for name in args.cases:
        if name == "auto-duration":
            report[name] = bench.durations()
            ok &= report[name]["pass"]
            print(name, json.dumps(report[name]), flush=True)
            continue
        if name in (
            "auto",
            "noaudio",
        ):  # the first t2v case with frames "auto" (65 predicted) / decode.audio off
            report[name] = bench.variant(name)
            ok &= report[name]["pass"]
            print(name, json.dumps(report[name]), flush=True)
            continue
        case = json.loads((golden_dir(name) / f"{name}.json").read_text())
        dev = name.split("-")[0] in DEV
        if dev and not hasattr(bench, "library"):
            bench.prefuse()
        r = bench.run(bench.dev_nodes(case) if dev else bench.nodes(case), wav=out / f"{name}.wav")
        r["mlx"] = mx.__version__
        if "-distilled-" in name or name in DETAIL_CASES:
            c = compare_distilled(name, r, out)
        else:
            c = compare_dev(name, r, out, bench) if dev else compare(name, r, out, bench)
        c["loops"] = [{k: v for k, v in x.items() if k == "loop_peak_gib"} for x in bench.stages]
        c["loads"], bench.loads = bench.loads, {}
        bench.fused = {}  # drop the baked weights it held for the compare
        ok &= c["pass"]
        for v in r["nodes"].values():
            v.pop("handle", None)  # the handles were for the compare
        report[name] = {"total_s": r["total_s"], "nodes": {k: {x: v[x] for x in ("s", "peak_gib", "active_after_gib")}
                        for k, v in r["nodes"].items()}, "resident_gb_after": r["resident_gb"],
                        "phases": sorted({f"{n}:{p}" for n, p, _, _ in r["phases"]}), **c}  # fmt: skip
        print(name, json.dumps(report[name]), flush=True)
    if args.reseed:
        case = json.loads((golden_dir(args.cases[0]) / f"{args.cases[0]}.json").read_text())
        nodes = bench.nodes(case)
        nodes["seed"]["params"]["seed"] = case["seed"] + 1
        r = bench.run(nodes)
        report["reseed"] = {
            "ran": list(r["nodes"]),
            "total_s": r["total_s"],
            "nodes": {k: {x: v[x] for x in ("s", "peak_gib")} for k, v in r["nodes"].items()},
        }
        ok &= "encode" not in r["nodes"]
        print("reseed", json.dumps(report["reseed"]), flush=True)
    bench.residency.unload()
    report["after_unload_gib"] = round(bench.lm.measure_gb(), 2)
    (out / "report.json").write_text(json.dumps(report, indent=2, default=str) + "\n")
    print(
        "report", out / "report.json", "after unload", report["after_unload_gib"], "GiB", flush=True
    )
    return 0 if ok else 1


@pytest.mark.gpu
def test_ltx_goldens():
    pytest.importorskip("mlx")
    assert main([*CASES, "auto", "noaudio", "--reseed"]) == 0


@pytest.mark.gpu
def test_ltx_distilled_goldens():
    """Distilled retake, extend and audio to video (plan 19 phase 1): every frame >= 40 dB from the fork's."""
    pytest.importorskip("mlx")
    assert main(DISTILLED_CASES) == 0


@pytest.mark.gpu
def test_ltx_detail_goldens():
    """Recipe Detail on text and audio to video (plan 19 phases 4, 4b): every frame >= 40 dB from the fork's."""
    pytest.importorskip("mlx")
    assert main(DETAIL_CASES) == 0


@pytest.mark.gpu
def test_ltx_dev_goldens():
    """Retake, extend and audio to video: bit-exact on the goldens' mlx, within DEV_BARS on another."""
    pytest.importorskip("mlx")
    assert main(DEV_CASES) == 0


if __name__ == "__main__":
    sys.path.insert(
        0, str(Path(__file__).resolve().parents[1])
    )  # runner_service/, as pytest's pythonpath
    sys.exit(main())
