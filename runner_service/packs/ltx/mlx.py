"""Stage functions of the ltx pack (runtime mlx): LTX-2.5 distilled two-stage text-to-video and image-to-video
(keyframes) with audio, auto length and Prompt Relay beats, a port of ltx-2-studio's
`DistilledPipeline.generate_two_stage` + `decode_and_save_video` (ltx-pipelines-mlx 0.15.4) over its model library
ltx-core-mlx, vendored in vendor/ (MIT, VENDORED.md). The order of every noise draw, the seeds, the sigma tables, the
blends and the conditioning order are the old code's, so the same mlx version gives the same bytes
(docs/reference-notes/ltx.md sections 4, 6 and 11; GOTCHAS.md).

Handles: Conditioning is `Cond` (video [1, 1024, 4096] and audio [1, 1024, 2048] prompt embeddings, the beats with
their token ranges, the keyframe rows unresolved: each sample resolves them at its own grid); Latent is `Latent`
(video tokens at (F, H, W) latent size, appended keyframe tokens after them, and audio tokens, together). A Video
value is the encoded clip {mp4: bytes, poster: PIL image (frame 0), frames, fps, has_audio}, which the core `output`
node stores."""

import functools
import itertools
import json
import math
import shutil
import subprocess
import sys
import tempfile
import wave
from dataclasses import dataclass, replace
from pathlib import Path

import mlx.core as mx
import numpy as np
import yaml
from PIL import Image

from core.graph import RunError, auto_frames

sys.path.append(
    str(Path(__file__).parent / "vendor")
)  # the vendored model library: `import ltx_core_mlx`

from ltx_core_mlx.components.diffusion_steps import EulerAncestralDiffusionStep
from ltx_core_mlx.components.patchifiers import (
    AudioPatchifier,
    VideoLatentPatchifier,
    compute_video_latent_shape,
    snap_output_dimensions,
)
from ltx_core_mlx.conditioning.mask_utils import first_frame_keyframes_mask
from ltx_core_mlx.conditioning.prompt_relay import (
    build_relay_mask,
    distribute_segment_lengths,
    map_token_ranges,
)
from ltx_core_mlx.conditioning.types.keyframe_cond import VideoConditionByKeyframeIndex
from ltx_core_mlx.conditioning.types.latent_cond import (
    LatentState,
    VideoConditionByLatentIndex,
    apply_denoise_mask,
    noise_latent_state,
)
from ltx_core_mlx.duration_head import load_duration_head
from ltx_core_mlx.loader import official_pack as op
from ltx_core_mlx.model.audio_vae.audio_vae import AudioVAEDecoder
from ltx_core_mlx.model.audio_vae.bwe import VocoderWithBWE
from ltx_core_mlx.model.transformer.model import LTXModel, LTXModelConfig, X0Model
from ltx_core_mlx.model.upsampler.model import LatentUpsampler
from ltx_core_mlx.model.video_vae.video_vae import VideoDecoder, VideoEncoder
from ltx_core_mlx.text_encoders.gemma.encoders.gemma4_encoder import Gemma4TextEncoder
from ltx_core_mlx.text_encoders.gemma.feature_extractor import GemmaFeaturesExtractorV2
from ltx_core_mlx.text_encoders.gemma.gemma4 import Gemma4TextModel
from ltx_core_mlx.text_encoders.gemma.gemma4_config import Gemma4TextConfig
from ltx_core_mlx.utils.positions import (
    compute_audio_positions,
    compute_audio_token_count,
    compute_video_positions,
)
from mlx_arsenal.diffusion import euler_step

FFMPEG = shutil.which("ffmpeg")
if FFMPEG is None:  # checked when the runner loads the pack: it stops with this
    raise RuntimeError("the ltx pack needs ffmpeg on PATH to write MP4s (brew install ffmpeg)")

# ltx_pipelines_mlx.scheduler: LTX-2.5 distilled tables (8 and 3 steps; each ends at 0). Only the full tables are
# valid: the old --stageN-steps n kept table[:n + 1], which stops above sigma 0 with noise left in (note 11.6)
STAGE1_SIGMAS = [1.0, 0.99375, 0.9875, 0.98125, 0.975, 0.909375, 0.725, 0.421875, 0.0]
STAGE2_SIGMAS = [0.909375, 0.725, 0.421875, 0.0]
SIGMAS = {"stage1": STAGE1_SIGMAS, "stage2": STAGE2_SIGMAS}
ANCESTRAL_SEED = (
    10000  # distilled.ANCESTRAL_NOISE_SEED_OFFSET: the stage-1 loop's noise seed is seed + this
)
MAX_TOKENS = 1024  # Gemma prompt length (left-padded; LTX2_GEMMA_MAX_LENGTH)
SAMPLE_RATE = 48000
PATCH = VideoLatentPatchifier()
IMAGE_CRF = (
    33  # media_io.DEFAULT_IMAGE_CRF: keyframe images go through H.264 as the training data did
)
RELAY_EPSILON, RELAY_STRENGTH = (
    1e-3,
    1.0,
)  # the old CLI's Prompt Relay defaults (fixed: contracts README, Video)
DURATION_HEAD = (
    "model_patches/ltx-2.5-duration-head-bf16.safetensors"  # DurationHead (auto length), 3.8 MB
)
_STAGES = yaml.safe_load((Path(__file__).parent / "model.yaml").read_text())["stages"]
FRAMES = _STAGES["duration"]["params"]["frames"]
CANVAS_MAX = max(
    _STAGES["canvas"]["params"][s]["max"] for s in ("width", "height")
)  # the largest canvas side


@dataclass(frozen=True)
class Cond:
    video: mx.array  # [1, 1024, 4096]
    audio: mx.array  # [1, 1024, 2048]
    beats: tuple = ()  # each beat's seconds (0 = an equal share of what the pinned ones leave)
    ranges: tuple = ()  # each beat's [start, end) columns of the encoded prompt (Prompt Relay)
    images: tuple = ()  # keyframe rows (H.264-round-tripped RGB uint8, seconds, strength), unresolved
    # where the rows came from, for the run's error texts (`encode.beats`, `keyframes.images`); ponytail: a cached
    # Cond reused by a node of another id keeps the first id in these texts
    beats_at: str = ""
    images_at: str = ""


@dataclass(frozen=True)
class Latent:
    video: mx.array  # tokens [1, F * H * W, 128]
    audio: mx.array  # tokens [1, T, 128]
    fhw: tuple[int, int, int]  # the video latent's frames, height, width
    frames: int  # pixel frames
    fps: float
    upscaled: bool = False  # at the canvas's full size: upscale refuses it (each pass doubles it)


# --- weights: the official split files, renamed and converted as mlx-forge's ltx_25 recipe (vendored official_pack)


def _meta(path: Path) -> dict:
    return op.read_safetensors_header(path)[1]


def _dit_config(model) -> LTXModelConfig:
    meta = _meta(model.files("transformer")[0])
    return LTXModelConfig.from_checkpoint_config(
        op._embedded_config_payload(meta["config"], meta.get("model_version"))
    )


def build(component: str, model):
    """The component's module, unloaded (its arrays are lazy and are replaced by the loader)."""
    if component == "text_encoder":
        raw = json.loads(_meta(model.files(component)[0])["gemma_config"])
        return Gemma4TextModel(Gemma4TextConfig.from_text_encoder_config(raw))
    if component == "connector":
        return GemmaFeaturesExtractorV2(
            double_precision_rope=_dit_config(model).double_precision_rope
        )
    if component == "transformer":
        return LTXModel(_dit_config(model))
    if component == "upscaler":
        return LatentUpsampler.from_config(json.loads(_meta(model.files(component)[0])["config"]))
    if component == "vae":
        return VideoDecoder()
    if component == "vae_encoder":
        return VideoEncoder()
    if component == "audio_vae":
        return AudioVAEDecoder()
    if component == "vocoder":
        return VocoderWithBWE()
    raise ValueError(f"ltx: unknown component {component}")


def rename(component: str, key: str) -> str | None:
    """An official checkpoint key → the component module's name for it; None = not this component's."""
    if component == "text_encoder":
        return key.removeprefix("model.") if key.startswith("model.") else None
    if component == "connector":
        if op.classify_dit_key(key) == "connector":
            return f"connector.{op.sanitize_connector_key(key)}"
        return f"connector.{key}" if key.startswith("text_embedding_projection.") else None
    if component == "transformer":
        return (
            op.sanitize_transformer_key(key) if op.classify_dit_key(key) == "transformer" else None
        )
    if component == "upscaler":
        return key
    if component == "vae":
        return (
            op.sanitize_vae_decoder_key(key) if op.classify_vae_decoder_key(key, "_conv") else None
        )
    if (
        component == "vae_encoder"
    ):  # MLX hides _-prefixed attributes: _mean_of_means → mean_of_means
        ok = op.classify_vae_encoder_key(key, "_conv") and (
            name := op.sanitize_vae_encoder_key(key)
        )
        return name.replace("._", ".") if ok else None
    if component == "audio_vae":
        if key.startswith("audio_vae.decoder."):
            return key.removeprefix("audio_vae.decoder.")
        if key.startswith("audio_vae.per_channel_statistics."):  # MLX hides _-prefixed attributes
            return op.sanitize_audio_vae_key(key).replace("._", ".")
        return None
    if component == "vocoder":
        return op.sanitize_vocoder_key(key) if op.classify_audio_key(key) == "vocoder" else None
    return None


def convert(component: str, name: str, value: mx.array) -> mx.array:
    """PyTorch conv layouts → MLX channels-last (VAEs, vocoder, upscaler)."""
    if component == "upscaler":
        return op.transpose_upscaler_weight(name, value, component)
    if component == "vae":
        return op.maybe_transpose(name, value, "vae_decoder_conv")
    if component == "vae_encoder":
        return op.maybe_transpose(name, value, "vae_encoder_conv")
    if component == "audio_vae":  # the recipe sees the key with its decoder. prefix
        key = name if name.startswith("per_channel_statistics.") else f"decoder.{name}"
        return op.maybe_transpose(key, value, "audio_vae")
    if component == "vocoder":
        return op.maybe_transpose(name, value, "vocoder")
    return value


@functools.lru_cache(maxsize=1)
def _tokenizer(path: Path, size: int, mtime: int):
    """Gemma's tokenizer from the assets stored as U8 tensors in the text encoder file (read once; not weights)."""
    from transformers import AutoTokenizer

    with tempfile.TemporaryDirectory() as tmp:
        op._extract_text_encoder_assets(path, Path(tmp))
        return AutoTokenizer.from_pretrained(tmp)


# --- keyframe images (media_io.load_image_and_preprocess, split: the H.264 round trip once in `keyframes`, the
# resize and the VAE encode per sample at its grid)


def crf_round_trip(img: Image.Image, crf: int = IMAGE_CRF) -> np.ndarray:
    """media_io.decode_image + preprocess: RGB uint8 (alpha dropped) through one libx264 frame at `crf` and back
    (odd sides padded, then cropped back). The pixels depend on the ffmpeg/libx264 build (yuv444p here)."""
    a = np.asarray(img.convert("RGB"), dtype=np.uint8)
    h, w, _ = a.shape
    ph, pw = h + (h & 1), w + (w & 1)
    if (ph, pw) != (h, w):
        a = np.pad(a, ((0, ph - h), (0, pw - w), (0, 0)))
    enc = subprocess.run([FFMPEG, "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", f"{pw}x{ph}",
                          "-r", "1", "-i", "pipe:0", "-c:v", "libx264", "-preset", "veryfast", "-crf", str(crf),
                          "-frames:v", "1", "-f", "mp4", "-movflags", "frag_keyframe+empty_moov", "pipe:1"],
                         input=a.tobytes(), capture_output=True, check=False, timeout=60)  # fmt: skip
    dec = subprocess.run([FFMPEG, "-v", "error", "-i", "pipe:0", "-frames:v", "1", "-pix_fmt", "rgb24", "-f",
                          "rawvideo", "pipe:1"], input=enc.stdout, capture_output=True, check=False, timeout=60)  # fmt: skip
    if enc.returncode or dec.returncode or len(dec.stdout) != ph * pw * 3:
        raise RunError(f"ffmpeg failed on a keyframe image: {(enc.stderr or dec.stderr).decode(errors='ignore')[-300:]}")  # fmt: skip
    return np.frombuffer(dec.stdout, np.uint8).reshape(ph, pw, 3)[:h, :w].copy()


def cover(rgb: np.ndarray, side: int) -> np.ndarray:
    """A round-tripped image no larger than it needs to cover any canvas of at most `side` x `side` (its short side
    at `side`, LANCZOS), so a cached Cond does not hold a 50 MP frame; one at most that size is returned as it is."""
    h, w, _ = rgb.shape
    if min(h, w) <= side:
        return rgb
    scale = side / min(h, w)
    size = (max(side, math.ceil(w * scale)), max(side, math.ceil(h * scale)))
    return np.asarray(Image.fromarray(rgb, mode="RGB").resize(size, Image.LANCZOS))


def to_pixels(rgb: np.ndarray, height: int, width: int) -> mx.array:
    """media_io.resize_and_center_crop (scale to cover, ceil, LANCZOS, centre crop) → [-1, 1] BCHW bfloat16."""
    image = Image.fromarray(rgb, mode="RGB")
    scale = max(height / image.height, width / image.width)
    new_w, new_h = math.ceil(image.width * scale), math.ceil(image.height * scale)
    image = image.resize((new_w, new_h), Image.LANCZOS)
    left, top = (new_w - width) // 2, (new_h - height) // 2
    f = np.asarray(image.crop((left, top, left + width, top + height)), dtype=np.float32) / 255.0
    return mx.array(f * 2.0 - 1.0).transpose(2, 0, 1)[None, ...].astype(mx.bfloat16)


def keyframe_frames(cond: Cond, frames: int, fps: float) -> list[int]:
    """Each row's pixel frame min(round(s·fps), frames − 1) (contracts README, Video: keyframes), or RunError with
    the contract's text for a row past the clip or on a frame an earlier row has (rows from 1)."""
    out, seen = [], {}
    for i, (_, seconds, _) in enumerate(cond.images, 1):
        f, where = round(seconds * fps), f"{cond.images_at}[{i}].seconds"
        if f > frames:
            raise RunError(f"{where}: frame {f} is past the clip's last frame {frames - 1}")
        f = min(f, frames - 1)
        if f in seen:
            raise RunError(f"{where}: frame {f} is already used by row {seen[f]}")
        seen[f] = i
        out.append(f)
    return out


def _conditionings(ctx, model, cond: Cond, fhw, frames: int, fps: float) -> list:
    """_orchestration.combined_image_conditionings at the grid fhw: frame 0 replaces latent frame 0
    (VideoConditionByLatentIndex), any other frame appends its tokens (VideoConditionByKeyframeIndex), in row order."""
    if not cond.images:
        return []
    at = keyframe_frames(cond, frames, fps)
    encoder = ctx.component(model, "vae_encoder")
    ctx.progress(1, 1, "encoding images")
    out = []
    for (rgb, _, strength), frame in zip(cond.images, at, strict=True):
        latent = encoder.encode(
            to_pixels(rgb, fhw[1] * 32, fhw[2] * 32)[:, :, None, :, :]
        )  # (1, 128, 1, H, W)
        tokens = latent.transpose(0, 2, 3, 4, 1).reshape(1, -1, 128)
        out.append(VideoConditionByLatentIndex(frame_indices=[0], clean_latent=tokens, strength=strength) if frame == 0
                   else VideoConditionByKeyframeIndex(frame_idx=frame, keyframe_latent=tokens, spatial_dims=fhw,
                                                      frame_rate=fps, strength=strength))  # fmt: skip
    return out


# --- stages


def canvas(ctx, params: dict, inputs: dict) -> dict:
    """width x height floored to the 64 px grid (two-stage: stage 1 runs at half)."""
    return {"canvas": {"width": params["width"] // 64 * 64, "height": params["height"] // 64 * 64}}


def duration(ctx, params: dict, inputs: dict) -> dict:
    """frames may be "auto": stage 1 resolves it from the prompt (DurationHead)."""
    return {"duration": {"frames": params["frames"], "fps": params["fps"]}}


def encode(ctx, params: dict, inputs: dict) -> dict:
    """Gemma 4 (all 49 hidden states, the last one post-norm) → the connector → video and audio embeddings. With
    beats (Prompt Relay) the encoded prompt is `prompt + "".join(" " + beat)` and each beat's token range is kept."""
    model, text, beats = inputs["model"], inputs["prompt"], params["beats"]
    if not text.strip():
        raise RunError(f"{ctx.node}: the prompt is empty")
    for i, b in enumerate(beats, 1):
        if not b["text"].strip():
            raise RunError(f"{ctx.node}.beats[{i}].text: the beat is empty")
    f = model.files("text_encoder")[0]
    tokenizer = _tokenizer(f, f.stat().st_size, f.stat().st_mtime_ns)
    ranges = ()
    if beats:  # _base._prompt_relay_setup
        try:
            text, ranges = map_token_ranges(
                tokenizer, text, [b["text"] for b in beats], max_length=MAX_TOKENS
            )
        except ValueError as e:
            if "exceeds" in str(e):
                raise RunError(f"{ctx.node}.beats: the prompt and its beats are longer than {MAX_TOKENS} tokens") from e  # fmt: skip
            raise RunError(f"{ctx.node}.beats: {e}") from e
    tower = ctx.component(model, "text_encoder")
    extractor = ctx.component(model, "connector")
    ctx.check()
    ctx.progress(1, 1, "encoding")
    encoder = Gemma4TextEncoder(tower=tower, tokenizer=tokenizer)
    hidden, mask = encoder.encode_all_layers(text, max_length=MAX_TOKENS)
    video, audio = extractor(hidden, attention_mask=mask)
    mx.eval(video, audio)
    return {"cond": Cond(video, audio, tuple(float(b["seconds"]) for b in beats), tuple(tuple(r) for r in ranges),
                         beats_at=f"{ctx.node}.beats")}  # fmt: skip


def keyframes(ctx, params: dict, inputs: dict) -> dict:
    """The rows onto the conditioning, unresolved (each sample places and encodes them at its grid); the H.264 round
    trip is done here, once for both stages."""
    trips: dict[
        int, np.ndarray
    ] = {}  # core.images.load gives each take one PIL image: round-trip it once
    for r in params["images"]:
        if id(r["image"]) not in trips:
            trips[id(r["image"])] = cover(crf_round_trip(r["image"]), CANVAS_MAX)
    rows = tuple((trips[id(r["image"])], float(r["seconds"]), float(r["strength"])) for r in params["images"])  # fmt: skip
    if not rows:
        raise RunError(f"{ctx.node}.images: at least 1 items")
    return {"cond": replace(inputs["cond"], images=rows, images_at=f"{ctx.node}.images")}


def check_sample(params: dict, wired: set) -> str | None:
    """Before anything runs: the role fixes which inputs the node takes (contracts/README.md, Video) and the steps
    (its whole distilled table)."""
    role = params.get("role")
    if role == "stage1" and ("latent" in wired or not {"canvas", "duration"} <= wired):
        return ": stage 1 needs canvas and duration, not latent"
    if role == "stage2" and ("latent" not in wired or wired & {"canvas", "duration"}):
        return ": stage 2 needs latent, not canvas or duration"
    if role in SIGMAS and params.get("steps") != (n := len(SIGMAS[role]) - 1):
        return f".steps: must be one of {n}"
    return None


@functools.lru_cache(maxsize=1)
def _duration_head(path: Path, size: int, mtime: int):
    """The DurationHead (3.8 MB, bf16), loaded once; ponytail: outside the residency manager, it is that small."""
    head = load_duration_head(path)
    mx.eval(head.parameters())
    return head


def predict_frames(model, cond: Cond, fps: float) -> int:
    """Auto length: the DurationHead's seconds from the prompt embeddings (blocks.DurationPredictor) on the frames
    grid (core.graph.auto_frames: clamped to auto min_s..max_s and the slider's max)."""
    p = model.folder("transformer") / DURATION_HEAD
    if not p.is_file():
        raise RunError(f"auto length needs {DURATION_HEAD} in the model folder")
    seconds = _duration_head(p, p.stat().st_size, p.stat().st_mtime_ns)(video_tokens=cond.video, audio_tokens=cond.audio)  # fmt: skip
    return auto_frames(float(seconds.item()), fps, FRAMES)


def beat_lengths(cond: Cond, latent_frames: int, fps: float) -> list[int]:
    """Each beat's latent frames: seconds → max(1, round(s·fps/8)), 0 = an equal share of what the pinned ones
    leave (prompt_relay.distribute_segment_lengths); RunError for a beat left with none (beats from 1)."""
    if not cond.beats:
        return []
    pinned = [max(1, round(s * fps / 8)) if s else None for s in cond.beats]
    lengths = distribute_segment_lengths(len(pinned), latent_frames, pinned if any(n is not None for n in pinned) else None)  # fmt: skip
    if 0 in lengths:
        raise RunError(f"{cond.beats_at}[{lengths.index(0) + 1}]: no frames left for this beat (the clip has {latent_frames} latent frames)")  # fmt: skip
    return lengths


def relay_mask(cond: Cond, fhw, tokens: int, fps: float) -> mx.array | None:
    """_base._prompt_relay_mask_builder for one stage: the additive video→text bias (1, 1, tokens, 1024) over the F
    latent frames (appended keyframe tokens attend freely); None without beats."""
    if not cond.ranges:
        return None
    lengths = beat_lengths(cond, fhw[0], fps)
    return build_relay_mask(token_ranges=list(cond.ranges), segment_lengths=lengths, num_video_tokens=tokens,
                            tokens_per_frame=fhw[1] * fhw[2], latent_frames=fhw[0],
                            num_text_tokens=cond.video.shape[1], epsilon=RELAY_EPSILON, strength=RELAY_STRENGTH)  # fmt: skip


def noised_state(
    shape, conditionings, fhw, positions, seed: int, sigma: float, initial=None, scalar_blend=True
):
    """helpers.create_noised_state. scalar_blend (`legacy_scalar_blend`, video in both stages and stage-1 audio):
    noise * sigma + clean * (1 - sigma) with a Python sigma on the plain grid first, then the conditionings (so
    conditioned tokens start clean even at strength < 1); else conditionings, then noise_latent_state's mask * sigma
    blend (stage-2 audio). Frame-0 replacement drops `keyframes_mask` (VideoConditionByLatentIndex.apply): copied
    for parity with the goldens, though the reference marks frame 0 always (GOTCHAS: a probable port bug)."""
    latent = mx.zeros(shape, dtype=mx.bfloat16) if initial is None else initial
    mask = mx.ones((shape[0], shape[1], 1), dtype=mx.bfloat16)
    keyframes_ = (
        first_frame_keyframes_mask(mask, fhw[1] * fhw[2]) if positions.shape[-1] == 3 else None
    )
    state = LatentState(latent=latent, clean_latent=latent, denoise_mask=mask, positions=positions,
                        keyframes_mask=keyframes_)  # fmt: skip
    if not scalar_blend:
        for c in conditionings:
            state = c.apply(state, fhw)
        return noise_latent_state(state, sigma=sigma, seed=seed)
    mx.random.seed(seed)
    noise = mx.random.normal(state.clean_latent.shape).astype(mx.bfloat16)
    state = replace(state, latent=noise * sigma + state.clean_latent * (1.0 - sigma))
    for c in conditionings:
        state = c.apply(state, fhw)
    return state


def _uniform(mask: mx.array) -> bool:
    return bool(mx.all(mask == 1.0).item())


def _denoise(ctx, x0_model, video: LatentState, audio: LatentState, cond: Cond, sigmas, phase: str,
             noise_seed: int | None, cross_mask=None):  # fmt: skip
    """samplers.denoise_loop (noise_seed None: deterministic Euler) or euler_ancestral_denoising_loop (eta 1,
    s_noise 1, the loop's noise drawn video then audio from mx.random.seed(noise_seed)), with a cancel point and a
    progress event per step. A conditioned state (mask not all 1) passes per-token timesteps mask * sigma; the
    ancestral step re-applies the mask after its noise."""
    mx.eval(video.latent, video.clean_latent, audio.latent)  # _pre_denoise_flush: the GPU watchdog
    vx, ax = video.latent, audio.latent
    vdtype, adtype = vx.dtype, ax.dtype
    stepper = EulerAncestralDiffusionStep(eta=1.0, s_noise=1.0)
    v_uniform, a_uniform = _uniform(video.denoise_mask), _uniform(audio.denoise_mask)
    if noise_seed is not None:
        mx.random.seed(noise_seed)
    steps = list(itertools.pairwise(sigmas))
    for i, (sigma, sigma_next) in enumerate(steps):
        ctx.check()
        sig = mx.broadcast_to(mx.array([sigma], dtype=mx.bfloat16), (vx.shape[0],))
        kw = {} if cross_mask is None else {"video_cross_attention_mask": cross_mask}
        if not v_uniform:
            kw["video_timesteps"] = (video.denoise_mask * sigma).squeeze(-1)
        if not a_uniform:
            kw["audio_timesteps"] = (audio.denoise_mask * sigma).squeeze(-1)
        vx0, ax0 = x0_model(
            video_latent=vx, audio_latent=ax, sigma=sig, video_text_embeds=cond.video,
            audio_text_embeds=cond.audio, video_positions=video.positions, audio_positions=audio.positions,
            video_attention_mask=video.attention_mask, video_keyframes_mask=video.keyframes_mask,
            audio_attention_mask=audio.attention_mask, **kw,
        )  # fmt: skip
        vx0 = apply_denoise_mask(vx0, video.clean_latent, video.denoise_mask)
        ax0 = apply_denoise_mask(ax0, audio.clean_latent, audio.denoise_mask)
        if noise_seed is None:
            vx, ax = euler_step(vx, vx0, sigma, sigma_next), euler_step(ax, ax0, sigma, sigma_next)
        elif sigma_next == 0:  # terminal: the x0 prediction
            vx, ax = vx0.astype(vdtype), ax0.astype(adtype)
        else:
            vn = mx.random.normal(vx.shape).astype(mx.float32)
            an = mx.random.normal(ax.shape).astype(mx.float32)
            vnext = stepper.step(sample=vx.astype(mx.float32), denoised_sample=vx0, sigmas=sigmas, step_index=i,
                                 noise=vn)  # fmt: skip
            anext = stepper.step(sample=ax.astype(mx.float32), denoised_sample=ax0, sigmas=sigmas, step_index=i,
                                 noise=an)  # fmt: skip
            vx = apply_denoise_mask(vnext, video.clean_latent, video.denoise_mask).astype(vdtype)
            ax = apply_denoise_mask(anext, audio.clean_latent, audio.denoise_mask).astype(adtype)
        mx.eval(vx, ax)
        ctx.progress(i + 1, len(steps), phase)
        if noise_seed is not None and sigma_next == 0:
            break
    mx.clear_cache()
    return vx, ax


def sample(ctx, params: dict, inputs: dict) -> dict:
    """stage1: from the seed's noise at half the canvas, ancestral Euler; stage2: the (upscaled) latent renoised at
    sigma 0.909375 with seed + 2 (video and audio alike), deterministic Euler. Audio is denoised in both. Keyframe
    rows are encoded at the stage's grid, beats become its relay mask; an "auto" length is resolved in stage 1,
    before the transformer loads (so are the rows' positions and the beats' lengths)."""
    model, cond, seed = inputs["model"], inputs["cond"], inputs["seed"]
    if params["role"] == "stage1":
        size, d = inputs["canvas"], inputs["duration"]
        fps = float(d["fps"])
        frames = predict_frames(model, cond, fps) if d["frames"] == "auto" else d["frames"]
        height, width = snap_output_dimensions(
            size["height"], size["width"], two_stage=True, warn=False
        )
        fhw = compute_video_latent_shape(frames, height // 2, width // 2)
        keyframe_frames(cond, frames, fps)  # a bad row or beat fails before anything loads
        beat_lengths(cond, fhw[0], fps)
        x0_model = X0Model(ctx.component(model, "transformer"))
        audio_t = compute_audio_token_count(frames, frame_rate=fps)
        video = noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), _conditionings(ctx, model, cond, fhw, frames, fps), fhw,
                             compute_video_positions(*fhw, frame_rate=fps), seed, 1.0)  # fmt: skip
        audio = noised_state(
            (1, audio_t, 128), [], fhw, compute_audio_positions(audio_t), seed + 1, 1.0
        )
        mask = relay_mask(cond, fhw, video.latent.shape[1], fps)
        vx, ax = _denoise(ctx, x0_model, video, audio, cond, STAGE1_SIGMAS, "stage 1", seed + ANCESTRAL_SEED,
                          mask)  # fmt: skip
        return {"latent": Latent(vx, ax, fhw, frames, fps)}
    latent: Latent = inputs["latent"]
    fhw, sigmas = latent.fhw, STAGE2_SIGMAS
    x0_model = X0Model(ctx.component(model, "transformer"))
    conds = _conditionings(ctx, model, cond, fhw, latent.frames, latent.fps)
    positions = compute_video_positions(*fhw, frame_rate=latent.fps)
    video = noised_state(
        latent.video.shape, conds, fhw, positions, seed + 2, sigmas[0], latent.video
    )
    audio = noised_state(latent.audio.shape, [], fhw, compute_audio_positions(latent.audio.shape[1]), seed + 2,
                         sigmas[0], latent.audio, scalar_blend=False)  # fmt: skip
    mask = relay_mask(cond, fhw, video.latent.shape[1], latent.fps)
    vx, ax = _denoise(ctx, x0_model, video, audio, cond, sigmas, "stage 2", None, mask)
    return {"latent": replace(latent, video=vx, audio=ax)}


def upscale(ctx, params: dict, inputs: dict) -> dict:
    """The video latent x2 in height and width: denormalised with the VAE's per-channel statistics, upsampled,
    renormalised (a missing upscaler fails the load: a random one would give a silent mosaic)."""
    model, latent = inputs["model"], inputs["latent"]
    # a second pass would double it past the canvas the run was admitted for (and past memory)
    if latent.upscaled:
        raise RunError(f"{ctx.node}: the latent is already upscaled (one upscale per latent)")
    upsampler = ctx.component(model, "upscaler")
    stats = ctx.component(model, "vae").per_channel_statistics
    ctx.progress(1, 1, "upscaling")
    f, h, w = latent.fhw
    mean, std = stats.mean.reshape(1, 1, 1, 1, -1), stats.std.reshape(1, 1, 1, 1, -1)
    x = PATCH.unpatchify(latent.video[:, : f * h * w, :], latent.fhw).transpose(0, 2, 3, 4, 1)
    x = upsampler((x * std + mean).transpose(0, 4, 1, 2, 3)).transpose(0, 2, 3, 4, 1)
    x = ((x - mean) / std).transpose(0, 4, 1, 2, 3)
    mx.eval(x)
    tokens, _ = PATCH.patchify(x)
    mx.eval(tokens)
    return {"latent": replace(latent, video=tokens, fhw=(f, h * 2, w * 2), upscaled=True)}


def _save_wav(waveform: mx.array, path: Path) -> None:
    """_orchestration.save_waveform: (1, channels, samples) → 16-bit PCM WAV at 48 kHz."""
    wav = waveform[0].T
    pcm = mx.contiguous((mx.clip(wav.astype(mx.float32), -1.0, 1.0) * 32767).astype(mx.int16))
    mx.eval(pcm)
    with wave.open(str(path), "w") as wf:
        wf.setnchannels(wav.shape[1])
        wf.setsampwidth(2)
        wf.setframerate(SAMPLE_RATE)
        wf.writeframes(memoryview(pcm))


def _ffmpeg(*args: str) -> None:
    r = subprocess.run(
        [FFMPEG, "-v", "error", "-y", *args], capture_output=True, text=True, check=False
    )
    if r.returncode:
        raise RunError(f"ffmpeg failed: {r.stderr.strip()[-300:]}")


def decode(ctx, params: dict, inputs: dict) -> dict:
    """Audio (VAE decoder → BigVGAN + BWE, 48 kHz stereo) then video (conv VAE, streamed into ffmpeg: H.264 yuv420p
    CRF 18 + AAC), then remuxed with +faststart (a container change only) and frame 0 read back as the poster.
    `audio` off (the old --no-audio): no audio decode and no audio track; the frames are the same."""
    model, latent, audio = inputs["model"], inputs["latent"], params["audio"]
    vae = ctx.component(model, "vae")
    f, h, w = latent.fhw
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        tmp = Path(tmp)
        if audio:
            audio_vae, vocoder = ctx.component(model, "audio_vae"), ctx.component(model, "vocoder")
            ctx.progress(1, 1, "decoding audio")
            waveform = vocoder(audio_vae.decode(AudioPatchifier().unpatchify(latent.audio)))
            mx.eval(waveform)
            _save_wav(waveform, tmp / "audio.wav")
            del waveform
            mx.clear_cache()
        ctx.check()
        ctx.progress(1, 1, "decoding video")
        video = PATCH.unpatchify(latent.video[:, : f * h * w, :], latent.fhw)
        vae.decode_and_stream(video, str(tmp / "raw.mp4"), frame_rate=latent.fps,
                              audio_path=str(tmp / "audio.wav") if audio else None)  # fmt: skip
        ctx.check()
        ctx.progress(1, 1, "writing mp4")
        _ffmpeg(
            "-i",
            str(tmp / "raw.mp4"),
            "-c",
            "copy",
            "-movflags",
            "+faststart",
            str(tmp / "out.mp4"),
        )
        _ffmpeg(
            "-i",
            str(tmp / "out.mp4"),
            "-frames:v",
            "1",
            "-pix_fmt",
            "rgb24",
            str(tmp / "poster.png"),
        )
        with Image.open(tmp / "poster.png") as im:
            poster = im.copy()
        data = (tmp / "out.mp4").read_bytes()
    clip = {
        "mp4": data,
        "poster": poster,
        "frames": latent.frames,
        "fps": latent.fps,
        "has_audio": audio,
    }
    return {"video": clip}
