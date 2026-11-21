"""Stage functions of the ltx pack (runtime mlx): LTX-2.5 distilled two-stage text-to-video and image-to-video
(keyframes) with audio, auto length and Prompt Relay beats, a port of ltx-2-studio's
`DistilledPipeline.generate_two_stage` + `decode_and_save_video` (ltx-pipelines-mlx 0.18.0) over its model library
ltx-core-mlx, vendored in vendor/ (MIT, VENDORED.md). The order of every noise draw, the seeds, the sigma tables, the
blends and the conditioning order are the old code's, so the same mlx version gives the same bytes
(docs/reference-notes/ltx.md sections 4, 6 and 11; GOTCHAS.md).

M7 pack 1 adds the dev (guided) pipelines of ltx-2-studio, ported the same way: retake and extend
(`RetakePipeline.retake` / `.extend`, one guided stage over the whole source clip) and audio to video
(`A2VidPipelineTwoStage`: a guided stage 1 with the source audio frozen, then the distilled LoRA baked into the dev
transformer for stage 2), over `guided_denoise_loop` (`_guided`); docs/reference-notes/ltx.md section 12.

Handles: Conditioning is `Cond` (video [1, 1024, 4096] and audio [1, 1024, 2048] prompt embeddings, the beats with
their token ranges, the keyframe rows unresolved: each sample resolves them at its own grid; from encode-guided also
the negative prompt's embeddings); Latent is `Latent` (video tokens at (F, H, W) latent size, appended keyframe tokens
after them, and audio tokens, together; from a media input also the source's map and, for audio to video, the source
sound the MP4 carries). A Video value is the encoded clip {mp4: bytes, poster: PIL image (frame 0), frames, fps,
has_audio[, source_map]}, which the core `output` node stores."""

import functools
import itertools
import json
import math
import subprocess
import sys
import tempfile
import wave
from dataclasses import dataclass, replace
from pathlib import Path

import mlx.core as mx
import numpy as np
from PIL import Image

from core import media
from core.graph import RunError, auto_frames
from packs.ltx.common import (  # the runtime-neutral part
    ANCESTRAL_SEED,
    ANCESTRAL_SEED_2,
    CANVAS_MAX,
    DURATION_HEAD,
    FFMPEG,
    FRAMES,
    MAX_FRAMES,
    MAX_TOKENS,
    NEGATIVE_PROMPT,
    RELAY_EPSILON,
    RELAY_STRENGTH,
    SAMPLE_RATE,
    SOURCE_RATE,
    STAGE1_SIGMAS,
    STAGE2_SIGMAS,
    _failed,
    _ffmpeg,
    _needs_dev,
    ancestral,
    audio_map,
    audio_window,
    check_sample,  # noqa: F401 (the sample stage's check)
    cover,
    crf_round_trip,
    distilled_lora,  # noqa: F401 (tests use it from here)
    extend_map,
    image_crf,
    keyframe_frames,
    lora_block_prefused,
    ltx_floor,
    model_version,
    retake_map,
)

sys.path.append(
    str(Path(__file__).parent / "vendor")
)  # the vendored model library: `import ltx_core_mlx`

from ltx_core_mlx.components.diffusion_steps import EulerAncestralDiffusionStep
from ltx_core_mlx.components.guiders import MultiModalGuiderParams, create_multimodal_guider_factory
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
    TemporalRegionMask,
    VideoConditionByLatentIndex,
    apply_denoise_mask,
    noise_latent_state,
)
from ltx_core_mlx.duration_head import load_duration_head
from ltx_core_mlx.guidance.perturbations import (
    BatchedPerturbationConfig,
    Perturbation,
    PerturbationConfig,
    PerturbationType,
)
from ltx_core_mlx.loader import official_pack as op
from ltx_core_mlx.model.audio_vae import AudioProcessor, AudioVAEEncoder, encode_audio
from ltx_core_mlx.model.audio_vae.audio_vae import AudioVAEDecoder
from ltx_core_mlx.model.audio_vae.bwe import VocoderWithBWE
from ltx_core_mlx.model.transformer.model import LTXModel, LTXModelConfig, X0Model
from ltx_core_mlx.model.upsampler.model import LatentUpsampler
from ltx_core_mlx.model.video_vae.tiling import TilingConfig, prepare_tiles_for_encoding
from ltx_core_mlx.model.video_vae.video_vae import VideoDecoder, VideoEncoder
from ltx_core_mlx.text_encoders.gemma.encoders.gemma4_encoder import Gemma4TextEncoder
from ltx_core_mlx.text_encoders.gemma.feature_extractor import GemmaFeaturesExtractorV2
from ltx_core_mlx.text_encoders.gemma.gemma4 import Gemma4TextModel
from ltx_core_mlx.text_encoders.gemma.gemma4_config import Gemma4TextConfig
from ltx_core_mlx.utils.audio import load_audio
from ltx_core_mlx.utils.ffmpeg import probe_video_info
from ltx_core_mlx.utils.positions import (
    compute_audio_positions,
    compute_audio_token_count,
    compute_video_positions,
)
from mlx_arsenal.diffusion import dynamic_shift_schedule, euler_step

PATCH = VideoLatentPatchifier()


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
    neg_video: mx.array | None = None  # the fixed negative prompt's (encode-guided: CFG)
    neg_audio: mx.array | None = None
    crf: int | None = (
        None  # the keyframe images' CRF, from the encoding model's generation (common.image_crf)
    )


@dataclass(frozen=True)
class Latent:
    video: mx.array  # tokens [1, F * H * W, 128]
    audio: mx.array  # tokens [1, T, 128]
    fhw: tuple[int, int, int]  # the video latent's frames, height, width
    frames: int  # pixel frames
    fps: float
    upscaled: bool = False  # at the canvas's full size: upscale refuses it (each pass doubles it)
    source_map: dict | None = (
        None  # defs sourceMap of a clip made from a media take (the output reports it)
    )
    sound: tuple | None = (
        None  # audio to video: (the source file's bytes, in seconds), muxed by decode
    )


# --- weights: the official split files, renamed and converted as mlx-forge's ltx_25 recipe (vendored official_pack)


def _meta(path: Path) -> dict:
    return op.read_safetensors_header(path)[1]


def _version(model) -> tuple:
    """The transformer checkpoint's model_version (common.model_version), which picks the generation's rules."""
    return model_version(_meta(model.files("transformer")[0]))


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
    if component == "audio_encoder":
        return AudioVAEEncoder()
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
    if (
        component == "audio_encoder"
    ):  # blocks.AudioConditioner: audio_vae.encoder.* + the latent statistics
        if key.startswith("audio_vae.encoder."):
            return key.removeprefix("audio_vae.encoder.")
        if key.startswith("audio_vae.per_channel_statistics."):
            return op.sanitize_audio_vae_key(key).replace("._", ".")
        return None
    return None


def convert(component: str, name: str, value: mx.array) -> mx.array:
    """PyTorch conv layouts → MLX channels-last (VAEs, vocoder, upscaler)."""
    if component == "upscaler":
        return op.transpose_upscaler_weight(name, value, component)
    if component == "vae":
        return op.maybe_transpose(name, value, "vae_decoder_conv")
    if component == "vae_encoder":
        return op.maybe_transpose(name, value, "vae_encoder_conv")
    if component in (
        "audio_vae",
        "audio_encoder",
    ):  # the recipe sees the key with its decoder. / encoder. prefix
        part = "decoder" if component == "audio_vae" else "encoder"
        key = name if name.startswith("per_channel_statistics.") else f"{part}.{name}"
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


def _bilinear(x: np.ndarray, size: int, axis: int) -> np.ndarray:
    """One axis as torch interpolate(mode="bilinear", align_corners=False): half-pixel centres, the source coordinate
    clamped at 0, no antialias (media_io._bilinear_resize_axis, float32)."""
    n = x.shape[axis]
    src = np.maximum((np.arange(size, dtype=np.float32) + np.float32(0.5)) * np.float32(n / size) - np.float32(0.5),
                     np.float32(0.0))  # fmt: skip
    i0 = np.minimum(np.floor(src).astype(np.int64), n - 1)
    i1 = np.minimum(i0 + 1, n - 1)
    shape = [1] * x.ndim
    shape[axis] = size
    lam = (src - i0).astype(np.float32).reshape(shape)
    return np.take(x, i0, axis=axis) * (np.float32(1.0) - lam) + np.take(x, i1, axis=axis) * lam


def resize_and_center_crop(rgb: np.ndarray, height: int, width: int) -> np.ndarray:
    """media_io.resize_and_center_crop: scaled to cover (sides rounded up), bilinear on float32, centre crop →
    (height, width, 3) float32 in [0, 255]."""
    h, w = rgb.shape[:2]
    scale = max(height / h, width / w)
    new_h, new_w = math.ceil(h * scale), math.ceil(w * scale)
    f = _bilinear(_bilinear(rgb.astype(np.float32), new_h, 0), new_w, 1)
    top, left = (new_h - height) // 2, (new_w - width) // 2
    return f[top : top + height, left : left + width]


def to_pixels(rgb: np.ndarray, height: int, width: int) -> mx.array:
    """resize_and_center_crop, then x / 127.5 - 1 (normalize_images) → BCHW bfloat16."""
    f = resize_and_center_crop(rgb, height, width) / np.float32(127.5) - np.float32(1.0)
    return mx.array(f).transpose(2, 0, 1)[None, ...].astype(mx.bfloat16)


def _conditionings(ctx, model, cond: Cond, fhw, frames: int, fps: float) -> list:
    """_orchestration.combined_image_conditionings at the grid fhw: frame 0 replaces latent frame 0 (its keyframe
    marker kept), any other frame appends its tokens (VideoConditionByKeyframeIndex), in row order; each encode is
    evaluated while the encoder is loaded (else it runs inside the first DiT step)."""
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
        mx.eval(tokens)
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
                         beats_at=f"{ctx.node}.beats", crf=image_crf(_version(model)))}  # fmt: skip


def keyframes(ctx, params: dict, inputs: dict) -> dict:
    """The rows onto the conditioning, unresolved (each sample places and encodes them at its grid); the decode and
    the H.264 round trip at the encoding model's CRF are done here, once for both stages."""
    crf = inputs["cond"].crf
    trips: dict[
        int, np.ndarray
    ] = {}  # core.images.load gives each take one PIL image: round-trip it once
    for r in params["images"]:
        if id(r["image"]) not in trips:
            trips[id(r["image"])] = cover(crf_round_trip(r["image"], crf), CANVAS_MAX)
    rows = tuple((trips[id(r["image"])], float(r["seconds"]), float(r["strength"])) for r in params["images"])  # fmt: skip
    if not rows:
        raise RunError(f"{ctx.node}.images: at least 1 items")
    return {"cond": replace(inputs["cond"], images=rows, images_at=f"{ctx.node}.images")}


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
    blend (stage-2 audio). The first latent frame is marked in `keyframes_mask`, an image at frame 0 included
    (the vendored VideoConditionByLatentIndex keeps it)."""
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


def _frozen(video: LatentState, audio: LatentState, batch: int) -> dict:
    """samplers._frozen_sigma_kwargs: a frozen stream (whole-stream conditioning) conditions the DiT on sigma 0, its
    prompt AdaLN and the other modality's cross-attention gate (upstream modality_from_latent_state)."""
    zero = mx.zeros((batch,), dtype=mx.bfloat16)
    return {k: zero for k, s in (("video_sigma", video), ("audio_sigma", audio)) if s.frozen}


def _denoise(ctx, x0_model, video: LatentState, audio: LatentState, cond: Cond, sigmas, phase: str,
             noise_seed: int | None, cross_mask=None):  # fmt: skip
    """samplers.denoise_loop (noise_seed None: deterministic Euler) or euler_ancestral_denoising_loop (eta 1,
    s_noise 1, the loop's noise drawn video then audio from mx.random.seed(noise_seed)), with a cancel point and a
    progress event per step. A conditioned state (mask not all 1) passes per-token timesteps mask * sigma, a frozen
    one sigma 0; the ancestral step re-applies the mask after its noise."""
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
        kw = _frozen(video, audio, vx.shape[0]) | (
            {} if cross_mask is None else {"video_cross_attention_mask": cross_mask}
        )
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
    """stage1: from the seed's noise at half the canvas; stage2: the (upscaled) latent renoised at sigma 0.909375
    with seed + 2 (video and audio alike). Both ancestral Euler from 2.5 on (noise seed + 10000, + 20000), else
    deterministic (common.ancestral). Audio is denoised in both, except audio to video's (a latent carrying its
    source sound): there the source audio stays frozen and stage 2 is deterministic, as A2VidPipelineTwoStage. Keyframe
    rows are encoded at the stage's grid, beats become its relay mask; an "auto" length is resolved in stage 1,
    before the transformer loads (so are the rows' positions and the beats' lengths)."""
    model, cond, seed = inputs["model"], inputs["cond"], inputs["seed"]
    anc = ancestral(_version(model))
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
        vx, ax = _denoise(ctx, x0_model, video, audio, cond, STAGE1_SIGMAS, "stage 1",
                          seed + ANCESTRAL_SEED if anc else None, mask)  # fmt: skip
        return {"latent": Latent(vx, ax, fhw, frames, fps)}
    latent: Latent = inputs["latent"]
    fhw, sigmas = latent.fhw, STAGE2_SIGMAS
    x0_model = X0Model(ctx.component(model, "transformer"))
    conds = _conditionings(ctx, model, cond, fhw, latent.frames, latent.fps)
    positions = compute_video_positions(*fhw, frame_rate=latent.fps)
    video = noised_state(
        latent.video.shape, conds, fhw, positions, seed + 2, sigmas[0], latent.video
    )
    if latent.sound:  # audio to video: the source audio frozen (create_noised_state(frozen=True): sigma 0, mask 0)
        audio = _audio_state(
            latent.audio, mx.zeros((1, latent.audio.shape[1], 1), dtype=mx.bfloat16), frozen=True
        )
    else:
        audio = noised_state(latent.audio.shape, [], fhw, compute_audio_positions(latent.audio.shape[1]), seed + 2,
                             sigmas[0], latent.audio, scalar_blend=False)  # fmt: skip
    mask = relay_mask(cond, fhw, video.latent.shape[1], latent.fps)
    noise = seed + ANCESTRAL_SEED_2 if anc and not latent.sound else None
    vx, ax = _denoise(ctx, x0_model, video, audio, cond, sigmas, "stage 2", noise, mask)
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


def decode(ctx, params: dict, inputs: dict) -> dict:
    """Audio (VAE decoder → BigVGAN + BWE, 48 kHz stereo) then video (conv VAE, streamed into ffmpeg: H.264 yuv420p
    CRF 18 + AAC), then remuxed with +faststart (a container change only) and frame 0 read back as the poster.
    `audio` off (the old --no-audio): no audio decode and no audio track; the frames are the same. A latent made
    from source audio (audio to video) carries that audio instead: re-read at 48 kHz from its `in`, trimmed to the
    clip (int(frames / fps · 48000) samples), never the generated one."""
    model, latent, audio = inputs["model"], inputs["latent"], params["audio"]
    vae = ctx.component(model, "vae")
    f, h, w = latent.fhw
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        tmp = Path(tmp)
        if (
            audio and latent.sound
        ):  # audio to video: the source part itself (a2vid_two_stage), never decoded
            (tmp / "source").write_bytes(latent.sound[0])
            seconds = latent.frames / latent.fps
            data = load_audio(tmp / "source", target_sample_rate=SAMPLE_RATE, start_time=latent.sound[1],
                              max_duration=seconds)  # fmt: skip
            if data is None:
                raise RunError(f"{ctx.node}: the source audio could not be read again")
            _save_wav(data.waveform[:, :, : int(seconds * SAMPLE_RATE)], tmp / "audio.wav")
        elif audio:
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
        try:  # the last frame (New shot's "start from the previous clip's last frame") and the stream facts
            info = media.probe(tmp / "out.mp4")
            media.frame_png(tmp / "out.mp4", info["frames"] - 1, tmp / "last.png")
        except media.MediaError as e:
            raise RunError(f"reading back the mp4 failed: {e}") from None
        with Image.open(tmp / "last.png") as im:
            last = im.copy()
        data = (tmp / "out.mp4").read_bytes()
    clip = {
        "mp4": data,
        "poster": poster,
        "last": last,
        "frames": latent.frames,
        "fps": latent.fps,
        "has_audio": audio,
        "stream": info["stream"],
    }
    if (
        latent.source_map and "offset_s" in latent.source_map
    ):  # made from a media take (README, Media inputs)
        clip["source_map"] = latent.source_map
    return {"video": clip}


# --- M7 pack 1: retake, extend, audio to video (ltx_pipelines_mlx.retake, a2vid_two_stage; ltx.md section 12)


def ltx2_schedule(steps: int, tokens: int) -> list[float]:
    """scheduler.ltx2_schedule: the token-count-adaptive shifted schedule (video tokens only), stretched to end at 0.1,
    then 0."""
    return dynamic_shift_schedule(steps, num_tokens=tokens, base_shift=0.95, max_shift=2.05, stretch=True,
                                  terminal=0.1)  # fmt: skip


def check_distill(params: dict, wired: set, models: dict | None = None) -> str | None:
    """The dev model, and the pre-fused distilled LoRA is on this host (studio dispatches such a run only to a host
    whose models key lists it: README, Media inputs; the memory plans skip audio to video without it)."""
    from core.pack import models_root

    model = (models or {}).get("model")
    if why := _needs_dev(params, wired, models):
        return why
    spec = model.pack.spec if model is not None else {}
    files = [models_root() / spec["variants"][x["variant"]]["dir"] / x["file"]
             for x in spec.get("lora", {}).get("prefused", []) if x["variant"] == "distilled"]  # fmt: skip
    if model is not None and not any(f.is_file() for f in files):
        return f": no live host for env {ENV} has the distilled LoRA"
    return None


check_retake = check_extend = _needs_dev
globals()["check_sample-guided"] = _needs_dev
ENV = "mlx"


def encode_guided(ctx, params: dict, inputs: dict) -> dict:
    """_base._encode_text_with_negative: the prompt, then the fixed negative prompt, each materialised on its own
    (the GPU watchdog), with Gemma loaded once; then Gemma and the connector go (unload_after)."""
    model, text = inputs["model"], inputs["prompt"]
    if not text.strip():
        raise RunError(f"{ctx.node}: the prompt is empty")
    f = model.files("text_encoder")[0]
    encoder = Gemma4TextEncoder(tower=ctx.component(model, "text_encoder"),
                                tokenizer=_tokenizer(f, f.stat().st_size, f.stat().st_mtime_ns))  # fmt: skip
    extractor = ctx.component(model, "connector")
    ctx.check()
    ctx.progress(1, 1, "encoding")
    out = []
    for t in (text, NEGATIVE_PROMPT):
        hidden, mask = encoder.encode_all_layers(t, max_length=MAX_TOKENS)
        video, audio = extractor(hidden, attention_mask=mask)
        mx.eval(video, audio)
        out += [video, audio]
    return {
        "cond": Cond(
            out[0], out[1], neg_video=out[2], neg_audio=out[3], crf=image_crf(_version(model))
        )
    }


def load_video_frames(path: Path, height: int, width: int, frames: int) -> mx.array:
    """ltx_core_mlx.utils.image.load_video_frames: the first `frames` frames as rgb24 at the source's own size
    (the same ffmpeg arguments: the pixels depend on them) → [-1, 1] (1, 3, F, H, W) bfloat16."""
    r = subprocess.run([FFMPEG, "-i", str(path), "-vframes", str(frames), "-s", f"{width}x{height}", "-pix_fmt",
                        "rgb24", "-f", "rawvideo", "-"], capture_output=True, check=False, timeout=600)  # fmt: skip
    if r.returncode:
        raise RunError(
            f"ffmpeg failed to read the source video: {r.stderr.decode(errors='ignore')[-300:]}"
        )
    f = (
        np.frombuffer(r.stdout, dtype=np.uint8).reshape(-1, height, width, 3).astype(np.float32)
        / 255.0
        * 2.0
        - 1.0
    )
    return mx.array(f).transpose(3, 0, 1, 2)[None, ...].astype(mx.bfloat16)


def _audio_tokens(ctx, model, path: Path, frames: int, fps: float, start: float = 0.0):
    """The source's sound at 16 kHz stereo from `start` for frames / fps s (utils.audio.load_audio) through the audio
    VAE encoder (blocks.AudioConditioner) → (tokens [1, T, 128] float32, its encoded frames T), or None without
    sound."""
    data = load_audio(
        path, target_sample_rate=SOURCE_RATE, start_time=start, max_duration=frames / fps
    )
    if data is None:
        return None
    latent = encode_audio(
        data.waveform, data.sample_rate, ctx.component(model, "audio_encoder"), AudioProcessor()
    )
    mx.eval(latent)
    return latent, data.waveform.shape[-1] / SOURCE_RATE


def encode_video(ctx, params: dict, inputs: dict) -> dict:
    """retake._encode_source_video: probed (its real size, frames and fps re-checked against the input's caps),
    frames floored to 8k + 1 at its own size and fps → the conv VAE encoder (retake.encode_video_tensor: tiled with
    TilingConfig.default() when it spans more than one tile, else at once), evaluated before the DiT loads; its sound
    at 16 kHz → the audio VAE encoder, or the old zero latent for a source without sound."""
    model, src = inputs["model"], params["source"]
    where, tid = f"{ctx.node}.source", src["take_id"]
    ctx.progress(1, 1, "encoding")
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        path = Path(tmp) / "source.mp4"
        path.write_bytes(src["data"])
        try:
            info = probe_video_info(str(path))
        except RuntimeError as e:
            raise RunError(f"{where}: take {tid} is not a readable video") from e
        if info.width % 32 or info.height % 32:
            raise RunError(f"{where}: take {tid} is {info.width} × {info.height}; this input needs sides on a 32 px grid")  # fmt: skip
        if info.num_frames > MAX_FRAMES:
            raise RunError(f"{where}: take {tid} has {info.num_frames} frames; this input takes at most {MAX_FRAMES}")  # fmt: skip
        frames = ltx_floor(info.num_frames)
        pixels = load_video_frames(path, info.height, info.width, frames)
        encoder, tiling = ctx.component(model, "vae_encoder"), TilingConfig.default()
        if len(prepare_tiles_for_encoding(pixels.shape, tiling)) > 1:  # fmt: skip
            # float32 blend buffers, back to bf16 (upstream's .to(dtype))
            latent = encoder.tiled_encode(pixels, tiling).astype(mx.bfloat16)
        else:
            latent = encoder.encode(pixels)
        mx.eval(latent)
        del pixels
        sound = _audio_tokens(ctx, model, path, frames, info.fps) if info.has_audio else None
    audio = sound[0] if sound else mx.zeros((1, 8, compute_audio_token_count(frames, frame_rate=info.fps), 16),
                                            dtype=mx.bfloat16)  # fmt: skip
    video, _ = PATCH.patchify(latent)
    tokens, _ = AudioPatchifier().patchify(audio)
    fhw = tuple(latent.shape[2:5])
    return {"latent": Latent(video, tokens, fhw, frames, info.fps, source_map={"take_id": tid})}


def encode_audio_(ctx, params: dict, inputs: dict) -> dict:
    """a2vid_two_stage: the part in..out (frames = the part at fps floored to 8k + 1) at 16 kHz stereo → the audio
    VAE encoder, cut to the clip's audio tokens; the source file is kept for decode, which muxes it."""
    model, src, fps = inputs["model"], params["source"], params["fps"]
    start, end = src.get("in", 0), src.get("out", src["duration_s"])
    m = audio_map(start, end, fps)
    if "error" in m:
        raise _failed(ctx.node, m["error"])
    frames, t = m["frames"], compute_audio_token_count(m["frames"], fps)
    ctx.progress(1, 1, "encoding")
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        path = Path(tmp) / "source"
        path.write_bytes(src["data"])
        sound = _audio_tokens(ctx, model, path, frames, fps, start)
    if sound is None:
        raise RunError(f"{ctx.node}.source: take {src['take_id']} has no sound")
    latent, seconds = sound
    if latent.shape[2] < t:  # the file is shorter than its declared length
        raise RunError(
            f"{ctx.node}.source: out {end:.1f} is past the audio's end {start + seconds:.1f}"
        )
    tokens, _ = AudioPatchifier().patchify(latent[:, :, :t, :])
    mx.eval(tokens)
    smap = {"take_id": src["take_id"]} | m["map"]
    return {"latent": Latent(None, tokens, None, frames, float(fps), source_map=smap, sound=(src["data"], start)),
            "duration": {"frames": frames, "fps": fps}}  # fmt: skip


def _guiders(params: dict, cond: Cond, audio_guided: bool):
    """LTX_2_3_PARAMS: video cfg / stg as set, rescale 0.7, modality 3, STG block 28; audio cfg 7 with the same
    (retake, extend) or no guidance (`MultiModalGuiderParams()`: audio to video's frozen audio)."""
    if cond.neg_video is None:
        raise RunError(
            "the guided samplers need the prompt from ltx/encode-guided (the negative prompt)"
        )
    video = MultiModalGuiderParams(cfg_scale=params["cfg"], stg_scale=params["stg"], rescale_scale=0.7,
                                   modality_scale=3.0, stg_blocks=[28])  # fmt: skip
    audio = MultiModalGuiderParams(cfg_scale=7.0, stg_scale=params["stg"], rescale_scale=0.7, modality_scale=3.0,
                                   stg_blocks=[28]) if audio_guided else MultiModalGuiderParams()  # fmt: skip
    return (create_multimodal_guider_factory(video, negative_context=cond.neg_video),
            create_multimodal_guider_factory(audio, negative_context=cond.neg_audio))  # fmt: skip


def _perturbed(kinds) -> BatchedPerturbationConfig:
    return BatchedPerturbationConfig(perturbations=[PerturbationConfig(perturbations=list(kinds))])


def _guided(
    ctx, x0_model, video: LatentState, audio: LatentState, cond: Cond, factories, sigmas, phase: str
):
    """samplers.guided_denoise_loop (no TeaCache, no tap): per step the conditioned pass, then as the guiders ask the
    negative-prompt pass (CFG), the STG pass (self-attention of block 28 skipped) and the modality pass (both A<->V
    cross attentions skipped), run one after another; each guider combines them, x0 is blended with the clean latent
    by the mask, then a deterministic Euler step. A cancel point and a progress event per step."""
    mx.eval(video.latent, video.clean_latent, audio.latent)  # _pre_denoise_flush: the GPU watchdog
    vx, ax = video.latent, audio.latent
    v_uniform, a_uniform = _uniform(video.denoise_mask), _uniform(audio.denoise_mask)
    steps = list(itertools.pairwise(sigmas))
    for i, (sigma, sigma_next) in enumerate(steps):
        ctx.check()
        vg, ag = (f.build_from_sigma(sigma) for f in factories)
        base = {"video_latent": vx, "audio_latent": ax,
                "sigma": mx.broadcast_to(mx.array([sigma], dtype=mx.bfloat16), (vx.shape[0],)),
                "video_positions": video.positions, "audio_positions": audio.positions,
                "video_attention_mask": video.attention_mask, "video_keyframes_mask": video.keyframes_mask,
                "audio_attention_mask": audio.attention_mask, **_frozen(video, audio, vx.shape[0])}  # fmt: skip
        if not v_uniform:
            base["video_timesteps"] = (video.denoise_mask * sigma).squeeze(-1)
        if not a_uniform:
            base["audio_timesteps"] = (audio.denoise_mask * sigma).squeeze(-1)
        text = {"video_text_embeds": cond.video, "audio_text_embeds": cond.audio}
        cond_v, cond_a = x0_model(**base, **text)
        neg_v = neg_a = ptb_v = ptb_a = mod_v = mod_a = 0.0
        if vg.do_unconditional_generation() or ag.do_unconditional_generation():
            neg = {"video_text_embeds": vg.negative_context if vg.negative_context is not None else cond.video,
                   "audio_text_embeds": ag.negative_context if ag.negative_context is not None else cond.audio}  # fmt: skip
            neg_v, neg_a = x0_model(**base, **neg, video_cross_attention_mask=None)
        if vg.do_perturbed_generation() or ag.do_perturbed_generation():
            kinds = ([Perturbation(type=PerturbationType.SKIP_VIDEO_SELF_ATTN, blocks=vg.params.stg_blocks)]
                     if vg.do_perturbed_generation() else []) + \
                    ([Perturbation(type=PerturbationType.SKIP_AUDIO_SELF_ATTN, blocks=ag.params.stg_blocks)]
                     if ag.do_perturbed_generation() else [])  # fmt: skip
            ptb_v, ptb_a = x0_model(**base, **text, perturbations=_perturbed(kinds))
        if vg.do_isolated_modality_generation() or ag.do_isolated_modality_generation():
            kinds = [Perturbation(type=PerturbationType.SKIP_A2V_CROSS_ATTN, blocks=None),
                     Perturbation(type=PerturbationType.SKIP_V2A_CROSS_ATTN, blocks=None)]  # fmt: skip
            mod_v, mod_a = x0_model(**base, **text, perturbations=_perturbed(kinds))
        vx0 = apply_denoise_mask(
            vg.calculate(cond_v, neg_v, ptb_v, mod_v), video.clean_latent, video.denoise_mask
        )
        ax0 = apply_denoise_mask(
            ag.calculate(cond_a, neg_a, ptb_a, mod_a), audio.clean_latent, audio.denoise_mask
        )
        vx, ax = euler_step(vx, vx0, sigma, sigma_next), euler_step(ax, ax0, sigma, sigma_next)
        mx.eval(vx, ax)
        ctx.progress(i + 1, len(steps), phase)
    mx.clear_cache()
    return vx, ax


def _audio_state(tokens, mask, frozen: bool = False) -> LatentState:
    return LatentState(latent=tokens, clean_latent=tokens, denoise_mask=mask,
                       positions=compute_audio_positions(tokens.shape[1]), frozen=frozen)  # fmt: skip


def _edit(
    ctx,
    params: dict,
    inputs: dict,
    video: LatentState,
    audio: LatentState,
    src: Latent,
    fhw,
    frames,
    smap,
):
    """The guided loop of retake / extend over the whole clip (noised at sigma 1 inside the masks, video `seed`,
    audio `seed + 1`) → the whole clip's latent with its source map."""
    seed, cond = inputs["seed"], inputs["cond"]
    factories = _guiders(params, cond, audio_guided=True)
    x0_model = X0Model(ctx.component(inputs["model"], "transformer"))
    video = noise_latent_state(video, sigma=1.0, seed=seed)
    audio = noise_latent_state(audio, sigma=1.0, seed=seed + 1)
    sigmas = ltx2_schedule(params["steps"], fhw[0] * fhw[1] * fhw[2])
    vx, ax = _guided(ctx, x0_model, video, audio, cond, factories, sigmas, "guided")
    return {
        "latent": Latent(
            vx, ax, fhw, frames, src.fps, source_map={"take_id": src.source_map["take_id"]} | smap
        )
    }


def retake(ctx, params: dict, inputs: dict) -> dict:
    """RetakePipeline.retake: the latent frames [S, E) of the span (retake_map) regenerated, the rest clean context
    (a hard 0/1 mask), the first latent frame marked as a keyframe; audio regenerated over the tokens that overlap
    the span in time (common.audio_window) unless `audio` is off (then frozen: mask 0, sigma 0; re-encoded, not
    muxed)."""
    src: Latent = inputs["latent"]
    span = params["span"]
    m = retake_map(span["start"], span["end"], src.fps, src.frames)
    if "error" in m:
        raise _failed(ctx.node, m["error"])
    (s, e), (f, h, w) = m["latent"], src.fhw
    mask = TemporalRegionMask(s, e).create_mask(f, h * w)
    video = LatentState(latent=src.video, clean_latent=src.video, denoise_mask=mask,
                        positions=compute_video_positions(f, h, w, frame_rate=src.fps),
                        keyframes_mask=first_frame_keyframes_mask(mask, h * w))  # fmt: skip
    t = src.audio.shape[1]
    amask = mx.zeros((1, t, 1), dtype=mx.bfloat16)
    if params["audio"]:
        a0, a1 = audio_window(s, e, t, src.fps)
        amask = amask.at[:, a0:a1, :].add(mx.ones((1, a1 - a0, 1), dtype=mx.bfloat16))
    return _edit(
        ctx,
        params,
        inputs,
        video,
        _audio_state(src.audio, amask, frozen=not params["audio"]),
        src,
        src.fhw,
        src.frames,
        m["map"],
    )


def extend(ctx, params: dict, inputs: dict) -> dict:
    """RetakePipeline.extend: N new latent frames (extend_map) of zeros after or before the clean source (mask 1 on
    the new tokens only), audio grown to the new length the same way; positions for the whole new clip at the
    source's fps."""
    src: Latent = inputs["latent"]
    m = extend_map(params["seconds"], params["direction"], src.fps, src.frames)
    if "error" in m:
        raise _failed(ctx.node, m["error"])
    (f, h, w), k, after = src.fhw, m["latent"], params["direction"] == "after"
    t = src.audio.shape[1]
    extra = max(0, compute_audio_token_count(src.frames + 8 * k, frame_rate=src.fps) - t)
    new = (1, k * h * w, 128)

    def joined(old, new_part):  # source then new part (after), or new then source (before)
        return mx.concatenate([old, new_part] if after else [new_part, old], axis=1)

    vmask = joined(mx.zeros((1, src.video.shape[1], 1), dtype=mx.bfloat16), mx.ones((1, new[1], 1), dtype=mx.bfloat16))  # fmt: skip
    clean = joined(src.video, mx.zeros(new, dtype=mx.bfloat16))
    amask = joined(
        mx.zeros((1, t, 1), dtype=mx.bfloat16), mx.ones((1, extra, 1), dtype=mx.bfloat16)
    )
    aclean = joined(src.audio, mx.zeros((1, extra, 128), dtype=mx.bfloat16))
    video = LatentState(latent=clean, clean_latent=clean, denoise_mask=vmask,
                        positions=compute_video_positions(f + k, h, w, frame_rate=src.fps),
                        keyframes_mask=first_frame_keyframes_mask(vmask, h * w))  # fmt: skip
    return _edit(
        ctx,
        params,
        inputs,
        video,
        _audio_state(aclean, amask),
        src,
        (f + k, h, w),
        m["frames"],
        m["map"],
    )


def sample_guided(ctx, params: dict, inputs: dict) -> dict:
    """a2vid_two_stage stage 1: half the canvas (64 px grid), pure noise (`seed`, the legacy scalar blend) with
    optional keyframe rows as i2v, the source audio frozen (mask 0: it steers the video through the A<->V cross
    attention, at sigma 0: frozen); video guided, audio guider off. RoPE at the clip's fps."""
    model, cond, seed, a = inputs["model"], inputs["cond"], inputs["seed"], inputs["audio"]
    size, d = inputs["canvas"], inputs["duration"]
    frames, fps = d["frames"], float(d["fps"])
    height, width = snap_output_dimensions(
        size["height"], size["width"], two_stage=True, warn=False
    )
    fhw = compute_video_latent_shape(frames, height // 2, width // 2)
    keyframe_frames(cond, frames, fps)  # a bad row fails before anything loads
    factories = _guiders(params, cond, audio_guided=False)
    x0_model = X0Model(ctx.component(model, "transformer"))
    video = noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), _conditionings(ctx, model, cond, fhw, frames, fps), fhw,
                         compute_video_positions(*fhw, frame_rate=fps), seed, 1.0)  # fmt: skip
    audio = _audio_state(
        a.audio, mx.zeros((1, a.audio.shape[1], 1), dtype=mx.bfloat16), frozen=True
    )
    sigmas = ltx2_schedule(params["steps"], fhw[0] * fhw[1] * fhw[2])
    vx, ax = _guided(ctx, x0_model, video, audio, cond, factories, sigmas, "guided")
    return {"latent": Latent(vx, ax, fhw, frames, fps, source_map=a.source_map, sound=a.sound)}


def distill(ctx, params: dict, inputs: dict) -> dict:
    """The dev model with the pack's pre-fused distilled LoRA as one bake row at strength 1 (the old
    `_fuse_distilled_lora`: dequantize, + the delta in float32, requantize): a second transformer entry in residency,
    reported with that LoRA. Pure: graph.keys builds it without a run (ctx None)."""
    from core.pack import LoraUse, models_root

    model = inputs["model"]
    spec = next(x for x in model.pack.spec["lora"]["prefused"] if x["variant"] == "distilled")
    # the shared index's id (its SHA-256), filled in at start (registry.fill_prefused); "" before: check_distill
    # fails such a run before anything loads
    lora_id = next(
        (x["lora_id"] for x in lora_block_prefused(model.pack) if x["variant"] == "distilled"), ""
    )
    f = models_root() / model.pack.spec["variants"]["distilled"]["dir"] / spec["file"]
    use = LoraUse(
        lora_id, "shared", f.stem, lora_id, f.stat().st_size if f.is_file() else 0, 1.0, "bake"
    )
    return {"model": replace(model, loras=(*model.loras, use))}


globals().update({"encode-guided": encode_guided, "encode-video": encode_video, "encode-audio": encode_audio_,
                  "sample-guided": sample_guided})  # fmt: skip
