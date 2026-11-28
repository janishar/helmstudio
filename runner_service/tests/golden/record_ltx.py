"""Record golden LTX-2.5 distilled two-stage text-to-video (+ audio) outputs from the old studio's own code.

The old studio (~/GenAI/ltx-2-studio) is read-only and ships no .venv, so its env is rebuilt from
its own lockfile in a scratch copy (nothing is written inside it):

    S=<scratch>/studio; rsync -a --exclude __pycache__ ltx-2-studio/{pyproject.toml,uv.lock,README.md,LICENSE,packages} $S/
    (cd $S && UV_PROJECT_ENVIRONMENT=$S/.venv uv sync --locked --no-dev)
    LTX_MLX_CACHE_DIR=<scratch>/vcache $S/.venv/bin/python runner_service/tests/golden/record_ltx.py --cases t2v-448x256-49f-s42

One case per process (as the CLI runs): DistilledPipeline built and driven exactly as `ltx-2-mlx generate
--distilled --quantize-on-load 8` does (cli._cmd_generate), with thin wrappers that only observe: the prompt
embeddings, each stage's input noise / output latent, the upscaled latent, the final latents, the waveform before
AAC, per-phase time and MLX peak memory. Writes <out>/<case>.mp4, .wav, -first/-mid/-last.png and .json.

Stage B cases (M4) add, still through the CLI's own arguments: `--image PATH FRAME_IDX STRENGTH` rows (first frame =
image-to-video, later frames = keyframes; the PNGs are made by ltx_inputs.test_image and saved beside the golden),
`--segment TEXT [LEN]` Prompt Relay beats, and `--lora PATH 1.0` with ltx_inputs.synthetic_lora (written to --scratch,
never to the repo; its sha256 is in the JSON). Extra observations: each preprocessed image and its encoded latent per
stage, the denoise / keyframe masks and appended tokens per stage, the relay token ranges and mask, the LoRA keys
fused and three fused weights. `--cases auto-duration` runs only Gemma + the DurationHead (no DiT) on a few prompts.

M7 cases (DEV_CASES, only when named in --cases): `ltx-2-mlx retake | extend | a2v` on the dev transformer, through
the pipelines' own entry points (RetakePipeline.retake_from_video / extend_from_video + cli._decode_and_save;
A2VidPipelineTwoStage.generate_and_save). Retake and extend read the t2v golden's MP4 in place; a2v's input WAV is
made by ltx_inputs.test_tones and saved beside the golden. Extra observations: the source latents, the positive and
negative embeddings, each loop's states (latent / clean / denoise mask / keyframes mask / positions), guider params
and sigmas, the distilled-LoRA fuse phase. A memory guard exits the process if MLX active memory passes 46 GiB.

Plan 19 phases 4 / 4b (Recipe Detail): `detail-*` (CASES, `generate --dfr`) and `a2v-detail-*` (DISTILLED_CASES,
`a2v --dfr`), with LTX2_LORA_MODE=unfused and the local detailing IC-LoRA (`--detailing-lora`, never downloaded).
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

import ltx_inputs
import mlx.core as mx
import numpy as np
from ltx_pipelines_mlx import distilled as distilled_mod
from ltx_pipelines_mlx.utils import _orchestration, blocks, media_io
from ltx_pipelines_mlx.utils._orchestration import save_waveform
from ltx_pipelines_mlx.utils.constants import detect_params

REPO = Path(__file__).resolve().parents[3]
STUDIO = "0.18.0 (ltx-2-studio main 4b2f609, upstream ltx-2-mlx 747b30e)"
GOLDEN = Path(__file__).resolve().parent
OUT = GOLDEN / "ltx-text-to-video"
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
GEMMA_DEFAULT = "mlx-community/gemma-3-12b-it-4bit"  # cli.DEFAULT_GEMMA; unused on LTX-2.5 (Gemma 4 ships in the pack)

CASES = [
    {"name": "t2v-448x256-49f-s42", "width": 448, "height": 256, "frames": 49, "fps": 24.0, "seed": 42,
     "prompt": "A golden retriever puppy runs across a sunny park lawn toward the camera, barking happily, "
               "birds chirping in the trees behind it"},
    {"name": "t2v-512x320-57f-s7", "width": 512, "height": 320, "frames": 57, "fps": 25.0, "seed": 7,
     "prompt": "Ocean waves crash against black volcanic rocks at sunset, white spray in the air, "
               "seagulls crying overhead and the wind roaring"},
    # --- stage B (M4): one 448x256x49 @24 seed-42 clip per feature
    {"name": "i2v-448x256-49f-s42", "out": "ltx-image-to-video", "width": 448, "height": 256, "frames": 49,
     "fps": 24.0, "seed": 42, "images": [{"variant": 0, "frame_idx": 0, "strength": 1.0}],
     "prompt": "A red ball rolls slowly to the right across a smooth pastel gradient floor while a blue cube spins "
               "in place, soft electronic hum"},
    {"name": "kf-448x256-49f-s42", "out": "ltx-image-to-video", "width": 448, "height": 256, "frames": 49,
     "fps": 24.0, "seed": 42,
     "images": [{"variant": 0, "frame_idx": 0, "strength": 1.0}, {"variant": 1, "frame_idx": 48, "strength": 1.0}],
     "prompt": "A red ball and a blue cube slide past each other and swap places on a smooth pastel gradient "
               "floor, soft electronic hum"},
    {"name": "beats-448x256-49f-s42", "out": "ltx-prompt-beats", "width": 448, "height": 256, "frames": 49,
     "fps": 24.0, "seed": 42, "relay_epsilon": 1e-3, "relay_strength": 1.0, "relay_audio_strength": 1.0,
     "studio": "ltx-2-studio main e6e15b7",  # plan 19 phase 2: audio relay on (upstream 0879826's distilled default)
     "segments": [{"text": "the puppy sits still and looks at the camera", "length": 3},
                  {"text": "the puppy jumps up and runs away to the left, barking", "length": None}],
     "prompt": "A golden retriever puppy on a sunny park lawn, birds chirping in the trees behind it"},
    {"name": "lora-448x256-49f-s42", "out": "ltx-lora", "width": 448, "height": 256, "frames": 49, "fps": 24.0,
     "seed": 42, "lora_strength": 1.0,
     "prompt": "A golden retriever puppy runs across a sunny park lawn toward the camera, barking happily, "
               "birds chirping in the trees behind it"},
    # plan 19 phase 3: the same synthetic LoRA as a run-time adapter (LTX2_LORA_MODE=unfused: upstream's attach_loras)
    {"name": "lora-adapter-448x256-49f-s42", "out": "ltx-lora", "width": 448, "height": 256, "frames": 49,
     "fps": 24.0, "seed": 42, "lora_strength": 1.0, "lora_mode": "unfused", "studio": "ltx-2-studio main e6e15b7",
     "prompt": "A golden retriever puppy runs across a sunny park lawn toward the camera, barking happily, "
               "birds chirping in the trees behind it"},
    # plan 19 phase 2: the Fast preset with a negative prompt, float16 compute (LTX2_COMPUTE_DTYPE) and NAG (the CLI's
    # --negative-prompt on --distilled, NAGConfig() defaults) in one render, on fork main e6e15b7
    {"name": "nag-448x256-49f-s42", "out": "ltx-nag", "width": 448, "height": 256, "frames": 49, "fps": 24.0,
     "seed": 42, "compute": "float16", "negative_prompt": "blurry, cartoon, text, watermark, people",
     "studio": "ltx-2-studio main e6e15b7",
     "prompt": "A golden retriever puppy runs across a sunny park lawn toward the camera, barking happily, "
               "birds chirping in the trees behind it"},
    # plan 19 phase 4: Recipe Detail, `ltx-2-mlx generate --dfr` (DFRPipeline: spatial x1, no temporal rounds, the conv
    # decoder) with the local detailing IC-LoRA attached as run-time adapters (LTX2_LORA_MODE=unfused), as helmstudio
    # attaches it; fork main e6e15b7
    {"name": "detail-448x256-49f-s42", "out": "ltx-detail", "width": 448, "height": 256, "frames": 49, "fps": 24.0,
     "seed": 42, "dfr": True, "lora_mode": "unfused", "studio": "ltx-2-studio main e6e15b7",
     "prompt": "A golden retriever puppy runs across a sunny park lawn toward the camera, barking happily, "
               "birds chirping in the trees behind it"},
    # the larger size: 57 frames pad to a 65-frame canvas (slots at 32 and 64), at 25 fps
    {"name": "detail-512x320-57f-s7", "out": "ltx-detail", "width": 512, "height": 320, "frames": 57, "fps": 25.0,
     "seed": 7, "dfr": True, "lora_mode": "unfused", "studio": "ltx-2-studio main e6e15b7",
     "prompt": "Ocean waves crash against black volcanic rocks at sunset, white spray in the air, "
               "seagulls crying overhead and the wind roaring"},
]  # fmt: skip
# the detailing IC-LoRA (Lightricks/LTX-2.5-22b-IC-LoRA-Pixel-Spatial-Upscaler), read in place: never downloaded
DETAILER = MODELS / "ltx-2.5" / "loras" / "ltx-2.5-22b-ic-lora-pixel-spatial-upscaler-x2-1.0.safetensors"

# DurationHead only (Gemma + connector + head; no DiT): the two t2v prompts (their embeddings are in those goldens)
# plus a short and a long scene, at two frame rates and the UI's and the CLI's default bounds.
DURATION_PROMPTS = [CASES[0]["prompt"], CASES[1]["prompt"],
                    "A door slams shut.",
                    ("A slow cinematic tracking shot follows an old fisherman as he walks the length of a long wooden "
                     "pier at dawn, stops to watch the boats leave the harbour one by one, then turns and walks back "
                     "while gulls circle and the bells of a distant church ring out")]  # fmt: skip
DURATION_SETTINGS = [{"fps": 24.0, "min_s": 1.0, "max_s": 20.0}, {"fps": 24.0, "min_s": 1.0, "max_s": 8.0},
                     {"fps": 25.0, "min_s": 1.0, "max_s": 20.0}]  # fmt: skip


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


def mask_stats(m) -> dict | None:
    return None if m is None else stats(m)


def record(case: dict, out: Path, scratch: Path) -> dict:
    model = MODELS / "ltx-2.5"
    ph = Phases()
    seen: dict = {"stages": []}

    # Stage B inputs, built before the pipeline (as the CLI parses its arguments)
    from ltx_core_mlx.conditioning.prompt_relay import PromptRelayInput
    from ltx_pipelines_mlx.utils.args import ImageConditioningInput

    images, image_meta = None, []
    crf = detect_params(model / "diffusion_models" / "ltx-2.5-22b-distilled-transformer-bf16.safetensors").default_image_crf  # fmt: skip
    for i, im in enumerate(case.get("images", [])):
        png = out / f"{case['name']}-image{i}.png"
        sha = ltx_inputs.test_image(png, variant=im["variant"])
        image_meta.append({**im, "file": png.name, "rgb_sha256": sha, "source_size": [640, 400],
                           "png_sha256": hashlib.sha256(png.read_bytes()).hexdigest(), "crf": crf})  # fmt: skip
        images = (images or []) + [
            ImageConditioningInput.at(str(png), im["frame_idx"], im["strength"])
        ]  # --image, auto binding
    relay = None
    if case.get("segments"):
        lengths = [s["length"] for s in case["segments"]]
        relay = PromptRelayInput(local_prompts=[s["text"] for s in case["segments"]],
                                 segment_lengths=lengths if any(n is not None for n in lengths) else None,
                                 epsilon=case["relay_epsilon"], strength=case["relay_strength"],
                                 audio_strength=case["relay_audio_strength"])  # fmt: skip
    lora_meta = None
    if "lora_strength" in case:
        lora_path = scratch / "ltx-synthetic-lora-r8.safetensors"
        lora_meta = ltx_inputs.synthetic_lora(lora_path, model / "diffusion_models" / "ltx-2.5-22b-distilled-transformer-bf16.safetensors")  # fmt: skip
        lora_meta["strength"] = case["lora_strength"]

    if case.get("compute"):  # what the CLI's users set: the pipelines apply it at the DiT load
        os.environ["LTX2_COMPUTE_DTYPE"] = case["compute"]
    if case.get("lora_mode"):  # read by the pipeline's __init__ (lora_mode_from_env)
        os.environ["LTX2_LORA_MODE"] = case["lora_mode"]
    nag = None
    if case.get("negative_prompt"):  # cli._build_nag_config with no overrides: NAGConfig() (audio too)
        from ltx_core_mlx.guidance.nag import NAGConfig

        nag = NAGConfig()

    # cli._cmd_generate, --distilled branch
    t0 = time.perf_counter()
    if case.get("dfr"):  # cli._cmd_generate, --dfr branch (spatial 1, temporal 0)
        from ltx_pipelines_mlx.dfr import DFRPipeline

        pipe = DFRPipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_memory=True,
                           low_ram_streaming=False, tile_count=None, detailing_lora=str(DETAILER))  # fmt: skip
    else:
        pipe = distilled_mod.DistilledPipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_memory=True,
                                               low_ram_streaming=False, tile_count=None)  # fmt: skip
    pipe.verbose = True
    pipe.stepwise = None
    pipe.generate_audio = True
    pipe.video_decoder = "conv"
    pipe.diffvae_tile = None
    if lora_meta:
        pipe._pending_loras = [(str(lora_path), case["lora_strength"])]  # what cli._cmd_generate sets from --lora
    t_init = time.perf_counter() - t0

    # Stage B observers (call through unchanged)
    raw_prep = media_io.load_image_and_preprocess

    def prep(path, height, width, crf):
        t = raw_prep(path, height, width, crf=crf)
        seen.setdefault("image_preprocess", []).append({"file": Path(path).name, "height": height, "width": width,
                                                        "crf": crf, "pixels": stats(t)})  # fmt: skip
        return t

    media_io.load_image_and_preprocess = prep
    raw_cond = _orchestration.combined_image_conditionings

    def cond(imgs, **kw):
        items = raw_cond(imgs, **kw)
        row = {"enc_h": kw["enc_h"], "enc_w": kw["enc_w"], "spatial_dims": list(kw["spatial_dims"]), "items": []}
        for it in items:
            if hasattr(it, "keyframe_latent"):
                row["items"].append({"type": "VideoConditionByKeyframeIndex", "frame_idx": it.frame_idx,
                                     "strength": it.strength, "latent": stats(it.keyframe_latent),
                                     "positions": stats(it.keyframe_positions)})  # fmt: skip
            else:
                row["items"].append({"type": "VideoConditionByLatentIndex", "frame_indices": it.frame_indices,
                                     "strength": it.strength, "latent": stats(it.clean_latent)})  # fmt: skip
        seen.setdefault("image_conditionings", []).append(row)
        return items

    _orchestration.combined_image_conditionings = ph.wrap("image_conditioning", cond)
    raw_relay = pipe._prompt_relay_setup

    def relay_setup(prompt, prompt_relay):
        combined, ranges = raw_relay(prompt, prompt_relay)
        if ranges is not None:
            seen["prompt_relay"] = {"encoded_prompt": combined, "token_ranges": [list(r) for r in ranges]}
        return combined, ranges

    pipe._prompt_relay_setup = relay_setup
    fused: dict = {"quantized": 0, "float": 0}
    if lora_meta:
        from ltx_core_mlx.loader import fuse_loras

        for name, kind in (("_fuse_delta_with_quantized", "quantized"), ("_fuse_delta_with_float", "float")):
            raw_f = getattr(fuse_loras, name)

            def counted(*a, _raw=raw_f, _kind=kind, **kw):
                fused[_kind] += 1
                return _raw(*a, **kw)

            setattr(fuse_loras, name, counted)

    # Observers (call through unchanged)
    enc = pipe.prompt_encoder
    enc.load = ph.wrap("text_encoder_load", enc.load)
    raw_encode = enc.encode

    def encode(prompt):
        v, a = raw_encode(prompt)
        mx.eval(v, a)
        if "video_embeds" in seen:  # the NAG negative prompt, encoded after the prompt
            seen["nag_embeds"] = {"video": stats(v), "audio": stats(a)}
        else:
            seen["video_embeds"], seen["audio_embeds"] = stats(v), stats(a)
        return v, a

    enc.encode = ph.wrap("text_encode", encode)
    raw_load = pipe.load

    def load():
        raw_load()
        if lora_meta and not case.get("lora_mode") and "lora_fused_weights" not in seen:
            from mlx.utils import tree_flatten

            p = dict(tree_flatten(pipe.dit.parameters()))
            seen["lora_fused_weights"] = {
                k: {s: stats(p[f"{k}.{s}"]) for s in ("weight", "scales", "biases") if f"{k}.{s}" in p}
                for k in ("transformer_blocks.0.attn1.to_q", "transformer_blocks.47.ff.proj_out", "patchify_proj",
                          "adaln_single.emb.timestep_embedder.linear1")
            }  # fmt: skip

    pipe.load = ph.wrap("dit_vae_encoder_upsampler_load", load)
    raw_loop = pipe._run_denoise_loop

    def loop(**kw):
        n = len(seen["stages"]) + 1
        vs, as_ = kw["video_state"], kw["audio_state"]
        row = {"stage": n, "sigmas": [float(s) for s in kw["sigmas"]], "ancestral": kw["ancestral"],
               "loop_noise_seed": kw["seed"] + kw["noise_seed_offset"] if kw["ancestral"] else None,
               "video_in": stats(vs.latent), "audio_in": stats(as_.latent),
               "video_positions": list(vs.positions.shape), "audio_positions": list(as_.positions.shape),
               "video_tokens_total": vs.latent.shape[1], "video_clean_in": stats(vs.clean_latent),
               "video_denoise_mask": stats(vs.denoise_mask), "video_keyframes_mask": mask_stats(vs.keyframes_mask),
               "video_attention_mask": None if vs.attention_mask is None else list(vs.attention_mask.shape),
               "video_positions_stats": stats(vs.positions),
               "video_cross_attention_mask": mask_stats(kw["video_cross_attention_mask"]),
               "audio_cross_attention_mask": mask_stats(kw.get("audio_cross_attention_mask"))}  # fmt: skip
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
                           num_frames=case["frames"], frame_rate=case["fps"], seed=case["seed"], images=images,
                           generated_keyframes=0, **({"prompt_relay": relay} if relay else {}),
                           **({"negative_prompt": case["negative_prompt"], "nag": nag} if nag else {}))  # fmt: skip
    total = time.perf_counter() - t0
    probe = ffprobe(mp4)
    vstream = next(s for s in probe["streams"] if s["codec_type"] == "video")
    picks = frames(mp4, out, case["name"], int(vstream["nb_frames"]))
    ru = resource.getrusage(resource.RUSAGE_SELF)
    extra = "".join(f" --image {m['file']} {m['frame_idx']} {m['strength']:g}" for m in image_meta)
    for sg in case.get("segments", []):
        extra += f" --segment '{sg['text']}'" + (f" {sg['length']}" if sg["length"] is not None else "")
    if relay:
        extra += (f" --relay-epsilon {case['relay_epsilon']:g} --relay-strength {case['relay_strength']:g}"
                  f" --relay-audio-strength {case['relay_audio_strength']:g}")  # fmt: skip
    if lora_meta:
        extra += f" --lora {lora_meta['file']} {case['lora_strength']:g}"
    if nag:
        extra += " --negative-prompt '...'"
    mode = f"--dfr --detailing-lora {DETAILER.name}" if case.get("dfr") else "--distilled"
    env = f"LTX2_COMPUTE_DTYPE={case['compute']} " if case.get("compute") else ""
    env += f"LTX2_LORA_MODE={case['lora_mode']} " if case.get("lora_mode") else ""
    meta = {
        **case,
        "pipeline": "DFRPipeline (ltx-2-mlx generate --dfr)" if case.get("dfr") else
        "DistilledPipeline (ltx-2-mlx generate --distilled)",
        "argv_equivalent": f"{env}ltx-2-mlx generate {mode} --model {model} --quantize-on-load 8 "
        f"--width {case['width']} --height {case['height']} --frames {case['frames']} --frame-rate {case['fps']:g} "
        f"--seed {case['seed']} --prompt '...'{extra} -o {case['name']}.mp4",
        "images": image_meta or None, "image_preprocess": seen.get("image_preprocess"),
        "image_conditionings": seen.get("image_conditionings"), "prompt_relay": seen.get("prompt_relay"),
        "lora": None if not lora_meta else {**lora_meta, "fused_quantized": fused["quantized"],
                                            "fused_float": fused["float"], "weights": seen.get("lora_fused_weights")},
        "quantize_on_load": 8, "quant_group_size": 64, "low_ram": False, "tiling": None, "video_decoder": "conv",
        "generate_audio": True, "stage1_steps": None, "stage2_steps": None, "is_ltx25": pipe._is_25,
        "noise_seeds": {"stage1_video": case["seed"], "stage1_audio": case["seed"] + 1,
                        "stage1_ancestral_loop": case["seed"] + distilled_mod.ANCESTRAL_NOISE_SEED_OFFSET,
                        "stage2_video_and_audio": case["seed"] + 2,
                        "stage2_ancestral_loop": case["seed"] + distilled_mod.ANCESTRAL_STAGE_2_NOISE_SEED_OFFSET},
        "prompt_embeds": {"video": seen.get("video_embeds"), "audio": seen.get("audio_embeds")},
        "nag": None if not nag else {**nag.__dict__, "embeds": seen.get("nag_embeds")},
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
                     "ltx_pipelines_mlx": case.get("studio", STUDIO),
                     "machine": f"{platform.system()} {platform.release()} {platform.machine()}",
                     "metal": {k: v for k, v in mx.device_info().items() if k in ("architecture", "memory_size")}},
    }  # fmt: skip
    meta["env"]["LTX_MLX_CACHE_DIR"] = "<scratch>"
    (out / f"{case['name']}.json").write_text(json.dumps(meta, indent=2, default=str) + "\n")
    print(case["name"], json.dumps({"timing_s": meta["timing_s"], "peak_gib": meta["memory"]["mlx_peak_gib_whole_run"],
                                    "rss_gib": meta["memory"]["process_max_rss_gib"]}), flush=True)  # fmt: skip
    return meta


# --- M7: the dev pipelines (dev transformer + CFG/STG/modality guidance), as `ltx-2-mlx retake|extend|a2v` run them.
# Source of retake/extend: the t2v golden's MP4 (448x256, 49 f @24, AAC), read in place (its sha256 goes in the JSON).
T2V_SOURCE = GOLDEN / "ltx-text-to-video" / "t2v-448x256-49f-s42.mp4"
DEV_CASES = [
    {"name": "retake-448x256-49f-s42", "out": "ltx-retake", "kind": "retake", "seed": 42, "start": 2, "end": 5,
     "regenerate_audio": True,
     "prompt": "A golden retriever puppy stops on a sunny park lawn, sits down and wags its tail, panting, "
               "birds chirping in the trees behind it"},
    {"name": "extend-448x256-49f+8-s42", "out": "ltx-extend", "kind": "extend", "seed": 42, "extend_frames": 1,
     "direction": "after",
     "prompt": "The golden retriever puppy keeps running toward the camera on the sunny park lawn and barks, "
               "birds chirping in the trees behind it"},
    {"name": "a2v-448x256-49f-s42", "out": "ltx-audio-to-video", "kind": "a2v", "seed": 42, "width": 448,
     "height": 256, "frames": 49, "fps": 24.0, "audio_start": 0.0,
     "prompt": "A musician plays a glowing synthesizer on a dark stage, lights pulsing in time with each note"},
]  # fmt: skip
MEMORY_GUARD_GIB = 46.0  # abort the case (os._exit) before MLX active memory reaches the 52 GB runner budget


def memory_guard(limit_gib: float = MEMORY_GUARD_GIB) -> None:
    """Background thread: exit the process at once if MLX active memory passes limit_gib (never swap the Mac)."""
    import threading

    def watch():
        while True:
            a = mx.get_active_memory() / 1024**3
            if a > limit_gib:
                print(f"MEMORY GUARD: MLX active {a:.1f} GiB > {limit_gib} GiB; aborting", file=sys.stderr, flush=True)
                os._exit(3)
            time.sleep(0.2)

    threading.Thread(target=watch, daemon=True).start()


def state_stats(st) -> dict:
    return {"latent": stats(st.latent), "clean_latent": stats(st.clean_latent), "denoise_mask": stats(st.denoise_mask),
            "keyframes_mask": mask_stats(st.keyframes_mask), "positions": stats(st.positions),
            "tokens": st.latent.shape[1]}  # fmt: skip


def record_dev(case: dict, out: Path) -> dict:
    """retake / extend (RetakePipeline, one stage, dev + guidance) or a2v (A2VidPipelineTwoStage) as the CLI runs them."""
    from ltx_pipelines_mlx import a2vid_two_stage as a2v_mod
    from ltx_pipelines_mlx import cli
    from ltx_pipelines_mlx import retake as retake_mod

    model = MODELS / "ltx-2.5"
    ph, seen = Phases(), {"loops": []}
    kind = case["kind"]
    memory_guard()
    t0 = time.perf_counter()
    inputs: dict = {}
    if kind == "a2v":
        wav_in = out / f"{case['name']}-audio.wav"
        inputs["audio"] = {"file": wav_in.name, "sha256": ltx_inputs.test_tones(wav_in), "seconds": 3.0,
                           "rate": 48000, "channels": 2, "what": "ltx_inputs.test_tones"}  # fmt: skip
        pipe = a2v_mod.A2VidPipelineTwoStage(
            model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_ram_streaming=False
        )
        mod = a2v_mod
    else:
        inputs["video"] = {"file": str(T2V_SOURCE.relative_to(REPO)),
                           "sha256": hashlib.sha256(T2V_SOURCE.read_bytes()).hexdigest()}  # fmt: skip
        pipe = retake_mod.RetakePipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_ram_streaming=False)
        mod = retake_mod
    pipe.verbose = True
    pipe.stepwise = None
    t_init = time.perf_counter() - t0

    # Observers (call through unchanged)
    raw_text = pipe._encode_text_with_negative

    def text(prompt, negative_prompt=None):
        r = raw_text(prompt, negative_prompt)
        seen["embeds"] = dict(zip(("video", "audio", "neg_video", "neg_audio"), (stats(x) for x in r)))
        return r

    pipe._encode_text_with_negative = ph.wrap("text_encoder_load_encode_pos_neg", text)
    pipe._load_dev_transformer = ph.wrap("dev_transformer_load", pipe._load_dev_transformer)
    if hasattr(pipe, "_encode_source_video"):
        raw_src = pipe._encode_source_video

        def src(path):
            v, a, meta = raw_src(path)
            mx.eval(v, a)
            seen["source"] = {"meta": meta.__dict__, "video_latent": stats(v), "audio_latent": stats(a)}
            return v, a, meta

        pipe._encode_source_video = ph.wrap("source_encode_video_audio", src)
    if kind == "a2v":
        raw_enc = mod.encode_audio

        def enc(*a, **kw):
            r = raw_enc(*a, **kw)
            mx.eval(r)
            seen["audio_latent_encoded"] = stats(r)
            return r

        mod.encode_audio = ph.wrap("audio_encode", enc)
        pipe._fuse_distilled_lora = ph.wrap("distilled_lora_fuse", pipe._fuse_distilled_lora)
        pipe._load_upsampler = ph.wrap("upsampler_load", pipe._load_upsampler)
        raw_save = pipe._save_waveform

        def save(w, path, sample_rate=48000):
            seen["output_waveform"] = stats(w)
            save_waveform(w, str(out / f"{case['name']}.wav"), sample_rate=sample_rate)
            return raw_save(w, path, sample_rate=sample_rate)

        pipe._save_waveform = save
        raw_dec = pipe.video_decoder_block.decode_and_stream

        def dec(latent, *a, **kw):
            seen["final_video_latent"] = stats(latent)
            return raw_dec(latent, *a, **kw)

        pipe.video_decoder_block.decode_and_stream = ph.wrap("decode_video_mux", dec)
        pipe._load_decoders = ph.wrap("decoders_load", pipe._load_decoders)

    def observe_loop(name, raw):
        def loop(**kw):
            n = len(seen["loops"]) + 1
            row = {"loop": n, "sampler": name, "sigmas": [round(float(s), 6) for s in kw["sigmas"]],
                   "video_in": state_stats(kw["video_state"]), "audio_in": state_stats(kw["audio_state"])}  # fmt: skip
            for k in ("video_guider_factory", "audio_guider_factory"):
                f = kw.get(k)
                if f is not None:
                    row[k.replace("_factory", "")] = {"params": f.params(1.0).__dict__,
                                                       "negative_context": f.negative_context is not None}  # fmt: skip
            res = ph.wrap(f"loop{n}_{name}", raw)(**kw)
            mx.eval(res.video_latent, res.audio_latent)
            row["video_out"], row["audio_out"] = stats(res.video_latent), stats(res.audio_latent)
            seen["loops"].append(row)
            return res

        return loop

    mod.guided_denoise_loop = observe_loop("guided_euler", mod.guided_denoise_loop)
    if kind == "a2v":
        mod.denoise_loop = observe_loop("euler", mod.denoise_loop)
    raw_audio_call = blocks.AudioDecoder.__call__
    wav = out / f"{case['name']}.wav"

    def audio_call(self, latent):
        seen["final_audio_latent"] = stats(latent)
        w = raw_audio_call(self, latent)
        mx.eval(w)
        seen["waveform"] = stats(w)
        save_waveform(w, str(wav), sample_rate=48000)
        return w

    blocks.AudioDecoder.__call__ = ph.wrap("audio_decode", audio_call)

    mp4 = out / f"{case['name']}.mp4"
    mx.reset_peak_memory()
    if kind == "a2v":  # cli._cmd_a2v
        pipe.generate_and_save(prompt=case["prompt"], output_path=str(mp4), audio_path=str(wav_in),
                               height=case["height"], width=case["width"], num_frames=case["frames"],
                               frame_rate=case["fps"], seed=case["seed"], images=None,
                               audio_start_time=case["audio_start"])  # fmt: skip
        argv = (f"ltx-2-mlx a2v --model {model} --quantize-on-load 8 --width {case['width']} --height {case['height']} "
                f"--frames {case['frames']} --frame-rate {case['fps']:g} --seed {case['seed']} --audio {wav_in.name} "
                f"--prompt '...' -o {mp4.name}")  # fmt: skip
    else:  # cli._cmd_retake / _cmd_extend, then cli._decode_and_save
        if kind == "retake":
            v, a = pipe.retake_from_video(prompt=case["prompt"], video_path=str(T2V_SOURCE), start_frame=case["start"],
                                          end_frame=case["end"], seed=case["seed"],
                                          regenerate_audio=case["regenerate_audio"])  # fmt: skip
            argv = f"ltx-2-mlx retake --video {T2V_SOURCE.name} --start {case['start']} --end {case['end']}"
        else:
            v, a = pipe.extend_from_video(prompt=case["prompt"], video_path=str(T2V_SOURCE),
                                          extend_frames=case["extend_frames"], direction=case["direction"],
                                          seed=case["seed"])  # fmt: skip
            argv = (f"ltx-2-mlx extend --video {T2V_SOURCE.name} --extend-frames {case['extend_frames']} "
                    f"--direction {case['direction']}")  # fmt: skip
        argv = f"{argv} --model {model} --quantize-on-load 8 --seed {case['seed']} --prompt '...' -o {mp4.name}"
        seen["final_video_latent"], seen["final_audio_latent_out"] = stats(v), stats(a)
        pipe._load_decoders = ph.wrap("decoders_load", pipe._load_decoders)
        pipe._decode_and_save_video = ph.wrap("decode_video_audio_mux", pipe._decode_and_save_video)
        cli._decode_and_save(pipe, v, a, argparse.Namespace(output=str(mp4)))
    total = time.perf_counter() - t0
    probe = ffprobe(mp4)
    vstream = next(s for s in probe["streams"] if s["codec_type"] == "video")
    picks = frames(mp4, out, case["name"], int(vstream["nb_frames"]))
    ru = resource.getrusage(resource.RUSAGE_SELF)
    meta = {
        **case,
        "pipeline": {"retake": "RetakePipeline.retake_from_video", "extend": "RetakePipeline.extend_from_video",
                     "a2v": "A2VidPipelineTwoStage.generate_and_save"}[kind],
        "argv_equivalent": argv, "inputs": inputs, "quantize_on_load": 8, "quant_group_size": 64, "low_ram": False,
        "noise_seeds": ({"stage1_video": case["seed"], "stage2_video_and_audio": case["seed"] + 2}
                        if kind == "a2v" else {"video": case["seed"], "audio": case["seed"] + 1}),
        "source": seen.get("source"), "audio_latent_encoded": seen.get("audio_latent_encoded"),
        "prompt_embeds": seen.get("embeds"), "loops": seen["loops"],
        "final_video_latent": seen.get("final_video_latent"),
        "final_audio_latent": seen.get("final_audio_latent") or seen.get("final_audio_latent_out"),
        "waveform": seen.get("waveform") or seen.get("output_waveform"),
        "wav": {"sample_rate": 48000, "format": "PCM s16le", "file": wav.name,
                "what": "the input audio trimmed to the video (a2v muxes the original, not a decode)" if kind == "a2v"
                else "the decoded waveform before AAC"},
        "frames_png": {k: {"index": v_, "file": f"{case['name']}-{k}.png"} for k, v_ in picks.items()},
        "mp4": {"file": mp4.name, "bytes": mp4.stat().st_size, "ffprobe": probe,
                "sha256": hashlib.sha256(mp4.read_bytes()).hexdigest()},
        "timing_s": {"pipeline_init": round(t_init, 2), **{k: r["s"] for k, r in ph.rows.items()},
                     "total_wall": round(total, 1)},
        "memory": {"phases": ph.rows, "mlx_peak_gib_whole_run": max(r["peak_gib"] for r in ph.rows.values()),
                   "process_max_rss_gib": gib(ru.ru_maxrss), "guard_gib": MEMORY_GUARD_GIB},
        "env": {k: v_ for k, v_ in os.environ.items() if k.startswith(("LTX2_", "LTX_MLX_", "LTX_MODEL"))},
        "versions": {"mlx": mx.__version__, "python": platform.python_version(), "numpy": np.__version__,
                     "ffmpeg": subprocess.run(["ffmpeg", "-version"], check=True, capture_output=True, text=True).stdout.split("\n")[0],
                     "ltx_pipelines_mlx": STUDIO,
                     "machine": f"{platform.system()} {platform.release()} {platform.machine()}"},
    }  # fmt: skip
    meta["env"]["LTX_MLX_CACHE_DIR"] = "<scratch>"
    (out / f"{case['name']}.json").write_text(json.dumps(meta, indent=2, default=str) + "\n")
    print(case["name"], json.dumps({"timing_s": meta["timing_s"], "peak_gib": meta["memory"]["mlx_peak_gib_whole_run"],
                                    "rss_gib": meta["memory"]["process_max_rss_gib"]}), flush=True)  # fmt: skip
    return meta


# --- plan 19 23.10 phase 1: the distilled retake, extend and audio to video, as `ltx-2-mlx retake --distilled`,
# `extend --distilled` and `a2v --distilled` run them (fork main e6e15b7): the same sources and prompts as DEV_CASES;
# extend adds 6 latent frames (2 s at 24 fps, the node's default). Observed: time, MLX peak and process RSS, the
# final latents, the decoded waveform before AAC (a2v: the source muxed, as the dev case).
DISTILLED_CASES = [
    {**DEV_CASES[0], "name": "retake-distilled-448x256-49f-s42"},
    {**DEV_CASES[1], "name": "extend-distilled-448x256-49f+48-s42", "extend_frames": 6},
    {**DEV_CASES[2], "name": "a2v-distilled-448x256-49f-s42"},
    # plan 19 phase 4b: `LTX2_LORA_MODE=unfused ltx-2-mlx a2v --dfr` (A2VidDFRPipeline: the frozen source audio
    # zero-padded to DFR's canvas, the detailer as adapters)
    {**DEV_CASES[2], "name": "a2v-detail-448x256-49f-s42", "dfr": True, "lora_mode": "unfused"},
    # 57 frames: the audio padded with zero tokens to the 65-frame canvas
    {**DEV_CASES[2], "name": "a2v-detail-448x256-57f-s42", "frames": 57, "dfr": True, "lora_mode": "unfused"},
]  # fmt: skip
STUDIO_MAIN = "ltx-2-studio main e6e15b7"


def record_distilled(case: dict, out: Path) -> dict:
    """One distilled case through the CLI's own pipeline classes and calls (cli._cmd_retake / _cmd_extend_distilled /
    _cmd_a2v with --distilled)."""
    from ltx_pipelines_mlx import cli

    model, kind = MODELS / "ltx-2.5", case["kind"]
    memory_guard()
    seen: dict = {}
    wav, mp4 = out / f"{case['name']}.wav", out / f"{case['name']}.mp4"
    raw_audio_call = blocks.AudioDecoder.__call__

    def audio_call(self, latent):
        seen["final_audio_latent"] = stats(latent)
        w = raw_audio_call(self, latent)
        mx.eval(w)
        save_waveform(w, str(wav), sample_rate=48000)
        return w

    blocks.AudioDecoder.__call__ = audio_call
    inputs: dict = {}
    mx.reset_peak_memory()
    t0 = time.perf_counter()
    if case.get("lora_mode"):  # read by the pipeline's __init__ (lora_mode_from_env)
        os.environ["LTX2_LORA_MODE"] = case["lora_mode"]
    if kind == "a2v":
        from ltx_pipelines_mlx.a2vid_distilled import A2VidDistilledPipeline

        wav_in = out / f"{case['name']}-audio.wav"
        inputs["audio"] = {"file": wav_in.name, "sha256": ltx_inputs.test_tones(wav_in), "seconds": 3.0,
                           "rate": 48000, "channels": 2, "what": "ltx_inputs.test_tones"}  # fmt: skip
        if case.get("dfr"):
            from ltx_pipelines_mlx.a2vid_dfr import A2VidDFRPipeline

            pipe = A2VidDFRPipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_ram_streaming=False,
                                    detailing_lora=str(DETAILER))  # fmt: skip
        else:
            pipe = A2VidDistilledPipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_ram_streaming=False)
        raw_save = pipe._save_waveform

        def save(w, path, sample_rate=48000):
            save_waveform(w, str(wav), sample_rate=sample_rate)
            return raw_save(w, path, sample_rate=sample_rate)

        pipe._save_waveform = save
        pipe.generate_and_save(prompt=case["prompt"], output_path=str(mp4), audio_path=str(wav_in),
                               height=case["height"], width=case["width"], num_frames=case["frames"],
                               frame_rate=case["fps"], seed=case["seed"], images=None,
                               audio_start_time=case["audio_start"])  # fmt: skip
        flag = f"--dfr --detailing-lora {DETAILER.name}" if case.get("dfr") else "--distilled"
        env = f"LTX2_LORA_MODE={case['lora_mode']} " if case.get("lora_mode") else ""
        argv = (f"{env}ltx-2-mlx a2v {flag} --model {model} --quantize-on-load 8 --width {case['width']} "
                f"--height {case['height']} --frames {case['frames']} --frame-rate {case['fps']:g} "
                f"--seed {case['seed']} --audio {wav_in.name} --prompt '...' -o {mp4.name}")  # fmt: skip
    else:
        inputs["video"] = {"file": str(T2V_SOURCE.relative_to(REPO)),
                           "sha256": hashlib.sha256(T2V_SOURCE.read_bytes()).hexdigest()}  # fmt: skip
        if kind == "retake":
            from ltx_pipelines_mlx.retake import RetakePipeline

            pipe = RetakePipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_ram_streaming=False,
                                  distilled=True)  # fmt: skip
            v, a = pipe.retake_from_video(prompt=case["prompt"], video_path=str(T2V_SOURCE), start_frame=case["start"],
                                          end_frame=case["end"], seed=case["seed"],
                                          regenerate_audio=case["regenerate_audio"])  # fmt: skip
            argv = f"ltx-2-mlx retake --distilled --video {T2V_SOURCE.name} --start {case['start']} --end {case['end']}"
        else:
            from ltx_pipelines_mlx.extend_distilled import ExtendDistilledPipeline

            pipe = ExtendDistilledPipeline(model_dir=str(model), gemma_model_id=GEMMA_DEFAULT, low_ram_streaming=False)
            v, a = pipe.extend_from_video(prompt=case["prompt"], video_path=str(T2V_SOURCE),
                                          extend_frames=case["extend_frames"], seed=case["seed"], images=None)  # fmt: skip
            argv = f"ltx-2-mlx extend --distilled --video {T2V_SOURCE.name} --extend-frames {case['extend_frames']}"
        argv = f"{argv} --model {model} --quantize-on-load 8 --seed {case['seed']} --prompt '...' -o {mp4.name}"
        mx.eval(v, a)
        seen["final_video_latent"], seen["final_audio_latent_out"] = stats(v), stats(a)
        cli._decode_and_save(pipe, v, a, argparse.Namespace(output=str(mp4)))
    total = time.perf_counter() - t0
    probe = ffprobe(mp4)
    vstream = next(s for s in probe["streams"] if s["codec_type"] == "video")
    picks = frames(mp4, out, case["name"], int(vstream["nb_frames"]))
    ru = resource.getrusage(resource.RUSAGE_SELF)
    meta = {**case, "argv_equivalent": argv, "inputs": inputs, "quantize_on_load": 8, "low_ram": False,
            "final_video_latent": seen.get("final_video_latent"),
            "final_audio_latent": seen.get("final_audio_latent") or seen.get("final_audio_latent_out"),
            "wav": {"sample_rate": 48000, "file": wav.name,
                    "what": "the input audio trimmed to the video (a2v muxes it)" if kind == "a2v"
                    else "the decoded waveform before AAC"},
            "frames_png": {k: {"index": v_, "file": f"{case['name']}-{k}.png"} for k, v_ in picks.items()},
            "mp4": {"file": mp4.name, "bytes": mp4.stat().st_size, "ffprobe": probe,
                    "sha256": hashlib.sha256(mp4.read_bytes()).hexdigest()},
            "timing_s": {"total_wall": round(total, 1)},
            "memory": {"mlx_peak_gib_whole_run": gib(mx.get_peak_memory()),
                       "process_max_rss_gib": gib(ru.ru_maxrss), "guard_gib": MEMORY_GUARD_GIB},
            "versions": {"mlx": mx.__version__, "python": platform.python_version(), "ltx_pipelines_mlx": STUDIO_MAIN,
                         "machine": f"{platform.system()} {platform.release()} {platform.machine()}"}}  # fmt: skip
    (out / f"{case['name']}.json").write_text(json.dumps(meta, indent=2, default=str) + "\n")
    print(case["name"], json.dumps({"timing_s": meta["timing_s"], "memory": meta["memory"]}), flush=True)
    return meta


def record_duration(out: Path) -> dict:
    """DurationHead predictions (cli: no --frames, --auto-duration MIN:MAX) for DURATION_PROMPTS; no DiT is loaded."""
    from ltx_pipelines_mlx.utils.types import AutoDuration

    t0 = time.perf_counter()
    pipe = distilled_mod.DistilledPipeline(model_dir=str(MODELS / "ltx-2.5"), gemma_model_id=GEMMA_DEFAULT,
                                           low_memory=True, low_ram_streaming=False, tile_count=None)  # fmt: skip
    pred = pipe._duration_predictor
    raw_head, last = pred._head, {}

    def head(**kw):
        s_ = raw_head(**kw)
        mx.eval(s_)
        last["seconds"] = float(s_.item())
        return s_

    pred._head = head
    mx.reset_peak_memory()
    pipe._load_text_encoder()
    t_load = time.perf_counter() - t0
    rows = []
    for prompt in DURATION_PROMPTS:
        t = time.perf_counter()
        v, a = pipe._encode_text(prompt)
        mx.eval(v, a)
        t_enc = time.perf_counter() - t
        row = {"prompt": prompt, "video_embeds": stats(v), "audio_embeds": stats(a), "encode_s": round(t_enc, 2),
               "predictions": []}  # fmt: skip
        for st in DURATION_SETTINGS:
            n = pipe._resolve_num_frames(AutoDuration(st["min_s"], st["max_s"]), video_encoding=v, audio_encoding=a,
                                         frame_rate=st["fps"])  # fmt: skip
            row["predictions"].append({**st, "raw_seconds": round(last["seconds"], 6), "frames": n})
        rows.append(row)
    meta = {
        "what": "DurationPredictor (blocks.py:791) on the connector outputs; frames = clamp(round(s*fps), "
        "round(min*fps), round(max*fps)) then floor to 8k+1 (snapped up if below min)",
        "head_file": "model_patches/ltx-2.5-duration-head-bf16.safetensors", "rows": rows,
        "timing_s": {"text_encoder_load": round(t_load, 2), "total_wall": round(time.perf_counter() - t0, 1)},
        "memory": {"mlx_peak_gib_whole_run": gib(mx.get_peak_memory()),
                   "process_max_rss_gib": gib(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss)},
        "versions": {"mlx": mx.__version__, "python": platform.python_version(), "numpy": np.__version__},
    }  # fmt: skip
    (out / "auto-duration.json").write_text(json.dumps(meta, indent=2) + "\n")
    print("auto-duration", json.dumps([[p["frames"] for p in r["predictions"]] for r in rows]), meta["timing_s"],
          meta["memory"], flush=True)  # fmt: skip
    return meta


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--cases", nargs="*", help="case names or auto-duration (default: all CASES; one per process)")
    ap.add_argument("--out", type=Path, default=None, help="output dir (default: the case's golden dir)")
    ap.add_argument("--scratch", type=Path, default=Path(tempfile.gettempdir()), help="where the synthetic LoRA goes")
    args = ap.parse_args()
    if args.cases and "auto-duration" in args.cases:
        out = args.out or GOLDEN / "ltx-auto-duration"
        out.mkdir(parents=True, exist_ok=True)
        record_duration(out)
    for case in [c for c in CASES if not args.cases or c["name"] in args.cases]:
        out = args.out or GOLDEN / case.get("out", "ltx-text-to-video")
        out.mkdir(parents=True, exist_ok=True)
        record(case, out, args.scratch)
    for case in [c for c in DEV_CASES if args.cases and c["name"] in args.cases]:
        out = args.out or GOLDEN / case["out"]
        out.mkdir(parents=True, exist_ok=True)
        record_dev(case, out)
    for case in [c for c in DISTILLED_CASES if args.cases and c["name"] in args.cases]:
        out = args.out or GOLDEN / case["out"]
        out.mkdir(parents=True, exist_ok=True)
        record_distilled(case, out)


if __name__ == "__main__":
    sys.exit(main())
