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
import logging
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
    EXTEND_CARRY,
    FFMPEG,
    FRAMES,
    MAX_FRAMES,
    MAX_TOKENS,
    NEGATIVE_PROMPT,
    RELAY_AUDIO_STRENGTH,
    RELAY_EPSILON,
    RELAY_STRENGTH,
    SAMPLE_RATE,
    SLOT_SEED,
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
    dfr_canvas,
    dfr_fps,
    distilled_lora,  # noqa: F401 (tests use it from here)
    extend_map,
    ic_lora,
    image_crf,
    is_distilled,
    keyframe_frames,
    latent_index,
    lora_block_prefused,
    ltx_floor,
    model_version,
    retake_map,
)

sys.path.append(str(Path(__file__).parent / "vendor"))  # the vendored model library: `import ltx_core_mlx`

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
    build_relay_mask_audio,
    distribute_segment_lengths,
    map_token_ranges,
)
from ltx_core_mlx.conditioning.types.keyframe_cond import VideoConditionByKeyframeIndex
from ltx_core_mlx.conditioning.types.keyframe_slots import (
    VideoGeneratedKeyframeSlots,
    extract_generated_keyframes,
)
from ltx_core_mlx.conditioning.types.latent_cond import (
    LatentState,
    TemporalRegionMask,
    VideoConditionByLatentIndex,
    apply_denoise_mask,
    noise_latent_state,
)
from ltx_core_mlx.conditioning.types.reference_video_cond import VideoConditionByReferenceLatent
from ltx_core_mlx.duration_head import load_duration_head
from ltx_core_mlx.guidance.nag import NAGConfig, NAGGuidance
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
log = logging.getLogger("ltx")


@dataclass(frozen=True)
class Cond:
    video: mx.array  # [1, 1024, 4096]
    audio: mx.array  # [1, 1024, 2048]
    beats: tuple = ()  # each beat's seconds (0 = an equal share of what the pinned ones leave)
    ranges: tuple = ()  # each beat's [start, end) columns of the encoded prompt (Prompt Relay)
    images: tuple = ()  # keyframe rows (H.264-round-tripped RGB uint8, seconds, strength, binding), unresolved
    # where the rows came from, for the run's error texts (`encode.beats`, `keyframes.images`); ponytail: a cached
    # Cond reused by a node of another id keeps the first id in these texts
    beats_at: str = ""
    images_at: str = ""
    # the negative prompt's, typed or the fixed one (encode-guided: CFG)
    neg_video: mx.array | None = None
    neg_audio: mx.array | None = None
    nag_video: mx.array | None = None  # encode.negative's (NAG on the samplers); None: off
    nag_audio: mx.array | None = None
    crf: int | None = None  # the keyframe images' CRF, from the encoding model's generation (common.image_crf)


@dataclass(frozen=True)
class Latent:
    video: mx.array  # tokens [1, F * H * W, 128]
    audio: mx.array  # tokens [1, T, 128]
    fhw: tuple[int, int, int]  # the video latent's frames, height, width
    frames: int  # pixel frames
    fps: float
    upscaled: bool = False  # at the canvas's full size: upscale refuses it (each pass doubles it)
    source_map: dict | None = None  # defs sourceMap of a clip made from a media take (the output reports it)
    sound: tuple | None = None  # audio to video: (the source file's bytes, in seconds), muxed by decode
    clip: bytes | None = None  # a source video's MP4 (encode-video): a distilled extend re-encodes it at half size
    guided: bool = False  # the dev guided stage 1 of audio to video made it: stage 2 refines deterministically
    # the detail pass (DFR, plan 19 phase 4): a stage 1 with `detail` padded the clip (`frames`, the clip's) to
    # `canvas` pixel frames (fhw is the canvas's) with generated keyframe slots at `slot_frames`, their content
    # (1, 128, K, H, W) here (upscale doubles it); stage 2 with `detail` conditions on them and trims the padding
    slots: mx.array | None = None
    slot_frames: tuple = ()
    canvas: int = 0


# --- weights: the official split files, renamed and converted as mlx-forge's ltx_25 recipe (vendored official_pack)


def _meta(path: Path) -> dict:
    return op.read_safetensors_header(path)[1]


def _version(model) -> tuple:
    """The transformer checkpoint's model_version (common.model_version), which picks the generation's rules."""
    return model_version(_meta(model.files("transformer")[0]))


def _dit_config(model) -> LTXModelConfig:
    meta = _meta(model.files("transformer")[0])
    return LTXModelConfig.from_checkpoint_config(op._embedded_config_payload(meta["config"], meta.get("model_version")))


def build(component: str, model):
    """The component's module, unloaded (its arrays are lazy and are replaced by the loader)."""
    if component == "text_encoder":
        raw = json.loads(_meta(model.files(component)[0])["gemma_config"])
        return Gemma4TextModel(Gemma4TextConfig.from_text_encoder_config(raw))
    if component == "connector":
        return GemmaFeaturesExtractorV2(double_precision_rope=_dit_config(model).double_precision_rope)
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
        return op.sanitize_transformer_key(key) if op.classify_dit_key(key) == "transformer" else None
    if component == "upscaler":
        return key
    if component == "vae":
        return op.sanitize_vae_decoder_key(key) if op.classify_vae_decoder_key(key, "_conv") else None
    if component == "vae_encoder":  # MLX hides _-prefixed attributes: _mean_of_means → mean_of_means
        ok = op.classify_vae_encoder_key(key, "_conv") and (name := op.sanitize_vae_encoder_key(key))
        return name.replace("._", ".") if ok else None
    if component == "audio_vae":
        if key.startswith("audio_vae.decoder."):
            return key.removeprefix("audio_vae.decoder.")
        if key.startswith("audio_vae.per_channel_statistics."):  # MLX hides _-prefixed attributes
            return op.sanitize_audio_vae_key(key).replace("._", ".")
        return None
    if component == "vocoder":
        return op.sanitize_vocoder_key(key) if op.classify_audio_key(key) == "vocoder" else None
    if component == "audio_encoder":  # blocks.AudioConditioner: audio_vae.encoder.* + the latent statistics
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


def _conditionings(ctx, model, cond: Cond, fhw, frames: int, fps: float, rope: float | None = None) -> list:
    """_orchestration.combined_image_conditionings at the grid fhw: frame 0 replaces latent frame 0 (its keyframe
    marker kept), any other frame appends its tokens (VideoConditionByKeyframeIndex), in row order; each encode is
    evaluated while the encoder is loaded (else it runs inside the first DiT step). `rope`: the positions' fps when
    it is not the clip's (the detail pass's dfr_fps)."""
    if not cond.images:
        return []
    at = keyframe_frames(cond, frames, fps)
    encoder = ctx.component(model, "vae_encoder")
    ctx.progress(1, 1, "encoding images")
    out = []
    for (rgb, _, strength, binding), frame in zip(cond.images, at, strict=True):
        latent = encoder.encode(to_pixels(rgb, fhw[1] * 32, fhw[2] * 32)[:, :, None, :, :])  # (1, 128, 1, H, W)
        tokens = latent.transpose(0, 2, 3, 4, 1).reshape(1, -1, 128)
        mx.eval(tokens)
        # API 1.39 binding (the fork's combined_image_conditionings): auto replaces at frame 0 and guides elsewhere
        if binding == "replace" or (binding == "auto" and frame == 0):
            out.append(VideoConditionByLatentIndex(frame_indices=[latent_index(frame)], clean_latent=tokens,
                                                   strength=strength))  # fmt: skip
        else:
            out.append(VideoConditionByKeyframeIndex(frame_idx=frame, keyframe_latent=tokens, spatial_dims=fhw,
                                                     frame_rate=rope or fps, strength=strength))  # fmt: skip
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
    beats (Prompt Relay) the encoded prompt is `prompt + "".join(" " + beat)` and each beat's token range is kept. A
    negative prompt (API 1.39) is encoded after it, for NAG (blank: none)."""
    model, text, beats = inputs["model"], inputs["prompt"], params["beats"]
    negative = params.get("negative", "")
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
            text, ranges = map_token_ranges(tokenizer, text, [b["text"] for b in beats], max_length=MAX_TOKENS)
        except ValueError as e:
            if "exceeds" in str(e):
                raise RunError(f"{ctx.node}.beats: the prompt and its beats are longer than {MAX_TOKENS} tokens") from e
            raise RunError(f"{ctx.node}.beats: {e}") from e
    tower = ctx.component(model, "text_encoder")
    extractor = ctx.component(model, "connector")
    ctx.check()
    ctx.progress(1, 1, "encoding")
    encoder = Gemma4TextEncoder(tower=tower, tokenizer=tokenizer)
    hidden, mask = encoder.encode_all_layers(text, max_length=MAX_TOKENS)
    video, audio = extractor(hidden, attention_mask=mask)
    mx.eval(video, audio)
    nag = {}
    # _base._encode_nag: one global prompt, materialised on its own (the GPU watchdog)
    if negative.strip():
        hidden, mask = encoder.encode_all_layers(negative, max_length=MAX_TOKENS)
        nv, na = extractor(hidden, attention_mask=mask)
        mx.eval(nv, na)
        nag = {"nag_video": nv, "nag_audio": na}
    return {"cond": Cond(video, audio, tuple(float(b["seconds"]) for b in beats), tuple(tuple(r) for r in ranges),
                         beats_at=f"{ctx.node}.beats", crf=image_crf(_version(model)), **nag)}  # fmt: skip


def keyframes(ctx, params: dict, inputs: dict) -> dict:
    """The rows onto the conditioning, unresolved (each sample places and encodes them at its grid); the decode and
    the H.264 round trip at the encoding model's CRF are done here, once for both stages."""
    crf = inputs["cond"].crf
    trips: dict[int, np.ndarray] = {}  # core.images.load gives each take one PIL image: round-trip it once
    for r in params["images"]:
        if id(r["image"]) not in trips:
            trips[id(r["image"])] = cover(crf_round_trip(r["image"], crf), CANVAS_MAX)
    rows = tuple((trips[id(r["image"])], float(r["seconds"]), float(r["strength"]), r.get("binding", "auto"))
                 for r in params["images"])  # fmt: skip
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
    found = model.files("duration_head")  # model.yaml: a component of the distilled variants
    if not found:
        raise RunError("auto length needs the model's duration_head (model.yaml) in the model folder")
    p = found[0]
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


def relay_audio_mask(cond: Cond, fhw, audio: LatentState, fps: float) -> mx.array | None:
    """_base._prompt_relay_audio_mask_builder at the distilled paths' strength (1.0, fixed): the additive audio→text
    bias (1, 1, Na, 1024), audio token i at i * F / Na latent frames; None without beats or for a frozen audio."""
    if not cond.ranges or audio.frozen:
        return None
    return build_relay_mask_audio(token_ranges=list(cond.ranges), segment_lengths=beat_lengths(cond, fhw[0], fps),
                                  num_audio_tokens=audio.latent.shape[1], latent_frames=fhw[0],
                                  num_text_tokens=cond.video.shape[1], epsilon=RELAY_EPSILON,
                                  strength=RELAY_AUDIO_STRENGTH)  # fmt: skip


def _f16(model) -> bool:
    """Whether the Model loads its transformer at compute float16 (a test's stand-in model has no compute)."""
    return getattr(model, "compute_of", lambda c: None)("transformer") == "float16"


def _dit(ctx, model) -> X0Model:
    """The transformer as an x0 model, its compute dtype set once at load (API 1.39: `compute` is in its residency
    key, so a resident float16 entry already has it)."""
    dit = ctx.component(model, "transformer")
    if _f16(model) and dit.compute_dtype is None:
        dit.set_compute_dtype(mx.float16)
    return X0Model(dit)


def _settled(ctx, model, x0_model: X0Model) -> None:
    """After a float16 denoise: upstream's overflow guard drops the compute dtype for good on a non-finite step,
    leaving float16-cast parameters under a float16 key; that entry goes (the next run reloads it) and the run says
    so in its progress."""
    if _f16(model) and x0_model.model.compute_dtype is None:
        log.warning(
            "%s: float16 overflowed; the transformer runs float32 activations until it reloads",
            ctx.node,
        )
        ctx.residency.release({model.key("transformer")})
        ctx.progress(1, 1, "float16 overflowed: float32 activations, reloads next run")


def noised_state(shape, conditionings, fhw, positions, seed: int, sigma: float, initial=None, scalar_blend=True):
    """helpers.create_noised_state. scalar_blend (`legacy_scalar_blend`, video in both stages and stage-1 audio):
    noise * sigma + clean * (1 - sigma) with a Python sigma on the plain grid first, then the conditionings (so
    conditioned tokens start clean even at strength < 1); else conditionings, then noise_latent_state's mask * sigma
    blend (stage-2 audio). The first latent frame is marked in `keyframes_mask`, an image at frame 0 included
    (the vendored VideoConditionByLatentIndex keeps it)."""
    latent = mx.zeros(shape, dtype=mx.bfloat16) if initial is None else initial
    mask = mx.ones((shape[0], shape[1], 1), dtype=mx.bfloat16)
    keyframes_ = first_frame_keyframes_mask(mask, fhw[1] * fhw[2]) if positions.shape[-1] == 3 else None
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
    # helpers._noise_generated_keyframe_slots: the slots (appended after the blend) blended too, from seed + 20000
    if (lay := state.generated_keyframe_layout) is not None:
        mx.random.seed(seed + SLOT_SEED)
        slot = state.latent[:, lay.token_slice]
        blended = mx.random.normal(slot.shape).astype(slot.dtype) * sigma + slot * (1.0 - sigma)
        state = replace(state, latent=mx.concatenate([state.latent[:, : lay.first_token], blended,
                                                      state.latent[:, lay.first_token + lay.num_tokens :]], axis=1))  # fmt: skip
    return state


def _uniform(mask: mx.array) -> bool:
    return bool(mx.all(mask == 1.0).item())


def _frozen(video: LatentState, audio: LatentState, batch: int) -> dict:
    """samplers._frozen_sigma_kwargs: a frozen stream (whole-stream conditioning) conditions the DiT on sigma 0, its
    prompt AdaLN and the other modality's cross-attention gate (upstream modality_from_latent_state)."""
    zero = mx.zeros((batch,), dtype=mx.bfloat16)
    return {k: zero for k, s in (("video_sigma", video), ("audio_sigma", audio)) if s.frozen}


def _denoise(ctx, x0_model, video: LatentState, audio: LatentState, cond: Cond, sigmas, phase: str,
             noise_seed: int | None, cross_mask=None, audio_mask=None):  # fmt: skip
    """samplers.denoise_loop (noise_seed None: deterministic Euler) or euler_ancestral_denoising_loop (eta 1,
    s_noise 1, the loop's noise drawn video then audio from mx.random.seed(noise_seed)), with a cancel point and a
    progress event per step. A conditioned state (mask not all 1) passes per-token timesteps mask * sigma, a frozen
    one sigma 0; the ancestral step re-applies the mask after its noise."""
    mx.eval(video.latent, video.clean_latent, audio.latent)  # _pre_denoise_flush: the GPU watchdog
    vx, ax = video.latent, audio.latent
    vdtype, adtype = vx.dtype, ax.dtype
    stepper = EulerAncestralDiffusionStep(eta=1.0, s_noise=1.0)
    v_uniform, a_uniform = _uniform(video.denoise_mask), _uniform(audio.denoise_mask)
    extra = {} if audio_mask is None else {"audio_cross_attention_mask": audio_mask}
    # NAG (upstream NAGConfig defaults); a frozen audio stream is not guided
    if cond.nag_video is not None:
        extra["nag"] = NAGGuidance.from_config(NAGConfig(), cond.nag_video, None if audio.frozen else cond.nag_audio)
    if noise_seed is not None:
        mx.random.seed(noise_seed)
    steps = list(itertools.pairwise(sigmas))
    for i, (sigma, sigma_next) in enumerate(steps):
        ctx.check()
        sig = mx.broadcast_to(mx.array([sigma], dtype=mx.bfloat16), (vx.shape[0],))
        kw = (
            _frozen(video, audio, vx.shape[0])
            | extra
            | ({} if cross_mask is None else {"video_cross_attention_mask": cross_mask})
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
    before the transformer loads (so are the rows' positions and the beats' lengths). `detail` (plan 19 phase 4) is
    DFRPipeline.generate_two_stage at spatial x1 without temporal rounds: stage 1 on the padded canvas with keyframe
    slots, stage 2 with the upscaled slots and (its Model carrying an IC-LoRA) `reference` as the IC reference,
    stage 1's audio kept and the padding trimmed."""
    model, cond, seed = inputs["model"], inputs["cond"], inputs["seed"]
    anc, detail = ancestral(_version(model)), bool(params.get("detail"))
    if params["role"] == "stage1":
        size, d = inputs["canvas"], inputs["duration"]
        fps = float(d["fps"])
        frames = predict_frames(model, cond, fps) if d["frames"] == "auto" else d["frames"]
        height, width = snap_output_dimensions(size["height"], size["width"], two_stage=True, warn=False)
        canvas, slots = dfr_canvas(frames) if detail else (frames, [])
        rope = dfr_fps(fps) if detail else fps
        fhw = compute_video_latent_shape(canvas, height // 2, width // 2)
        keyframe_frames(cond, frames, fps)  # a bad row or beat fails before anything loads
        beat_lengths(cond, fhw[0], fps)
        x0_model = _dit(ctx, model)
        audio_t = compute_audio_token_count(canvas, frame_rate=fps)
        conds = _conditionings(ctx, model, cond, fhw, frames, fps, rope) + _slots(slots, rope)
        video = noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), conds, fhw,
                             compute_video_positions(*fhw, frame_rate=rope), seed, 1.0)  # fmt: skip
        audio = noised_state((1, audio_t, 128), [], fhw, compute_audio_positions(audio_t), seed + 1, 1.0)
        mask = relay_mask(cond, fhw, video.latent.shape[1], fps)
        vx, ax = _denoise(ctx, x0_model, video, audio, cond, STAGE1_SIGMAS, "stage 1",
                          seed + ANCESTRAL_SEED if anc else None, mask, relay_audio_mask(cond, fhw, audio, fps))  # fmt: skip
        _settled(ctx, model, x0_model)
        return {"latent": _laid(Latent(vx, ax, fhw, frames, fps), video, slots, canvas)}
    latent: Latent = inputs["latent"]
    if detail != (latent.canvas > 0):
        raise RunError(f"{ctx.node}.detail: stage 1 and stage 2 take the detail pass together"
                       f" (this latent {'has' if latent.canvas else 'has no'} the detail layout)")  # fmt: skip
    fhw, sigmas = latent.fhw, STAGE2_SIGMAS
    rope = dfr_fps(latent.fps) if detail else latent.fps
    x0_model = _dit(ctx, model)
    conds = _conditionings(ctx, model, cond, fhw, latent.frames, latent.fps, rope)
    if detail:  # DFRPipeline: the upscaled slots, then the stage-1 latent as the IC reference
        conds += _slots(latent.slot_frames, rope, latent.slots) + _reference(model, inputs.get("reference"), rope)
    positions = compute_video_positions(*fhw, frame_rate=rope)
    video = noised_state(latent.video.shape, conds, fhw, positions, seed + 2, sigmas[0], latent.video)
    if latent.sound:  # audio to video: the source audio frozen (create_noised_state(frozen=True): sigma 0, mask 0)
        audio = _audio_state(latent.audio, mx.zeros((1, latent.audio.shape[1], 1), dtype=mx.bfloat16), frozen=True)
    else:
        audio = noised_state(latent.audio.shape, [], fhw, compute_audio_positions(latent.audio.shape[1]), seed + 2,
                             sigmas[0], latent.audio, scalar_blend=False)  # fmt: skip
    mask = relay_mask(cond, fhw, video.latent.shape[1], latent.fps)
    # the dev audio to video's stage 2 is deterministic (A2VidPipelineTwoStage); the distilled one ancestral
    # (A2VidDistilledPipeline: DistilledPipeline's stage 2 with the source audio frozen)
    noise = seed + ANCESTRAL_SEED_2 if anc and not latent.guided else None
    vx, ax = _denoise(ctx, x0_model, video, audio, cond, sigmas, "stage 2", noise, mask,
                      relay_audio_mask(cond, fhw, audio, latent.fps))  # fmt: skip
    _settled(ctx, model, x0_model)
    if detail:  # the padding trimmed: video to the clip's latent frames, stage 1's audio (as upstream ships it)
        keep = (latent.frames - 1) // 8 + 1
        n = compute_audio_token_count(latent.frames, frame_rate=latent.fps)
        return {"latent": replace(latent, video=vx[:, : keep * fhw[1] * fhw[2]], audio=latent.audio[:, :n],
                                  fhw=(keep, fhw[1], fhw[2]), slots=None, slot_frames=(), canvas=0)}  # fmt: skip
    return {"latent": replace(latent, video=vx, audio=ax)}


def _slots(frames, rope: float, initial=None) -> list:
    """The detail pass's VideoGeneratedKeyframeSlots at those pixel frames (none: [])."""
    return [VideoGeneratedKeyframeSlots(pixel_frame_indices=list(frames), frame_rate=rope,
                                        initial_keyframes=initial)] if frames else []  # fmt: skip


def _laid(latent: Latent, video: LatentState, slots: list, canvas: int) -> Latent:
    """A detail stage 1's Latent: the slots' content extracted (keyframe_slots.extract_generated_keyframes) and the
    canvas kept; else the Latent as it is."""
    if not slots:
        return latent
    lay, (_, h, w) = video.generated_keyframe_layout, latent.fhw
    found = extract_generated_keyframes(latent.video, lay, PATCH, (h, w))
    mx.eval(found)
    return replace(latent, slots=found, slot_frames=tuple(slots), canvas=canvas)


def ic_factor(model) -> int | None:
    """The `reference_downscale_factor` of the IC-LoRA the Model carries (its safetensors metadata), or None."""
    for u in model.loras:
        if u.path and "reference_downscale_factor" in (meta := _meta(Path(u.path))):
            return int(meta["reference_downscale_factor"])
    return None


def _reference(model, ref: Latent | None, rope: float) -> list:
    """iclora_utils.reference_conditioning_from_latent: the stage-1 latent's tokens at its own grid's positions, at
    the IC-LoRA's downscale factor, strength 1; [] without an IC-LoRA on the Model or a reference wired."""
    factor = ic_factor(model)
    if factor is None or ref is None:
        return []
    f, h, w = ref.fhw
    return [VideoConditionByReferenceLatent(reference_latent=ref.video[:, : f * h * w],
                                            reference_positions=compute_video_positions(f, h, w, frame_rate=rope),
                                            downscale_factor=factor, strength=1.0)]  # fmt: skip


def upscale(ctx, params: dict, inputs: dict) -> dict:
    """The video latent x2 in height and width: denormalised with the VAE's per-channel statistics, upsampled,
    renormalised (a missing upscaler fails the load: a random one would give a silent mosaic)."""
    model, latent = inputs["model"], inputs["latent"]
    # a second pass would double it past the canvas the run was admitted for (and past memory)
    if latent.upscaled:
        raise RunError(f"{ctx.node}: the latent is already upscaled (one upscale per latent)")
    ctx.progress(1, 1, "upscaling")
    tokens, fhw = _upsample(ctx, model, latent.video, latent.fhw)
    slots = latent.slots
    if slots is not None:  # the detail pass's keyframe slots, one call of their own (DFRPipeline)
        up, sfhw = _upsample(ctx, model, PATCH.patchify(slots)[0], slots.shape[2:])
        slots = PATCH.unpatchify(up, sfhw)
    return {"latent": replace(latent, video=tokens, fhw=fhw, upscaled=True, slots=slots)}


def _upsample(ctx, model, video: mx.array, fhw) -> tuple[mx.array, tuple]:
    """DistilledPipeline._upsample_latent on tokens: the first F * H * W (appended keyframe tokens dropped)."""
    upsampler = ctx.component(model, "upscaler")
    stats = ctx.component(model, "vae").per_channel_statistics
    f, h, w = fhw
    mean, std = stats.mean.reshape(1, 1, 1, 1, -1), stats.std.reshape(1, 1, 1, 1, -1)
    x = PATCH.unpatchify(video[:, : f * h * w, :], fhw).transpose(0, 2, 3, 4, 1)
    x = upsampler((x * std + mean).transpose(0, 4, 1, 2, 3)).transpose(0, 2, 3, 4, 1)
    x = ((x - mean) / std).transpose(0, 4, 1, 2, 3)
    mx.eval(x)
    tokens, _ = PATCH.patchify(x)
    mx.eval(tokens)
    return tokens, (f, h * 2, w * 2)


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
    if latent.canvas:  # a detail stage 1 straight into decode (no stage 2 to trim its canvas)
        raise RunError(f"{ctx.node}: the latent has the detail pass's padding; a stage 2 with detail trims it")
    vae = ctx.component(model, "vae")
    f, h, w = latent.fhw
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        tmp = Path(tmp)
        if audio and latent.sound:  # audio to video: the source part itself (a2vid_two_stage), never decoded
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
    if latent.source_map and "offset_s" in latent.source_map:  # made from a media take (README, Media inputs)
        clip["source_map"] = latent.source_map
    return {"video": clip}


# --- M7 pack 1: retake, extend, audio to video (ltx_pipelines_mlx.retake, a2vid_two_stage; ltx.md section 12)


def ltx2_schedule(steps: int, tokens: int) -> list[float]:
    """scheduler.ltx2_schedule: the token-count-adaptive shifted schedule (video tokens only), stretched to end at 0.1,
    then 0."""
    return dynamic_shift_schedule(steps, num_tokens=tokens, base_shift=0.95, max_shift=2.05, stretch=True, terminal=0.1)


def check_distill(params: dict, wired: set, models: dict | None = None) -> str | None:
    """The dev model, and the distilled LoRA (the optional add-on distilled_lora, API 1.40) is on this host: studio's
    run check refuses a Dev run without it first (contracts/README.md, Optional add-ons); the memory plans skip Dev's
    audio to video without it."""
    import config

    model = (models or {}).get("model")
    if model is not None and is_distilled(model):
        return None  # the identity: the LoRA is fused in already
    if why := _needs_dev(params, wired, models):
        return why
    if model is not None and not model.files("distilled_lora"):
        addon = model.pack.component("distilled_lora")["addon"]
        return f": {addon.get('short', addon['title'])} not downloaded on host {config.HOST}"
    return None


ENV = "mlx"  # retake, extend and the guided stage 1 run on Distilled and Dev (plan 19 23.4, phase 1)


def encode_guided(ctx, params: dict, inputs: dict) -> dict:
    """_base._encode_text_with_negative: the prompt, then the negative prompt (API 1.39: the typed one, blank = the
    fixed one), each materialised on its own (the GPU watchdog), with Gemma loaded once; then Gemma and the connector
    go (unload_after)."""
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
    # a distilled model runs without CFG: no negative prompt (retake --distilled: _encode_prompts, positive only)
    negative = params.get("negative", "") if params.get("negative", "").strip() else NEGATIVE_PROMPT
    for t in (text,) if is_distilled(model) else (text, negative):
        hidden, mask = encoder.encode_all_layers(t, max_length=MAX_TOKENS)
        video, audio = extractor(hidden, attention_mask=mask)
        mx.eval(video, audio)
        out += [video, audio]
    neg = dict(zip(("neg_video", "neg_audio"), out[2:]))
    return {"cond": Cond(out[0], out[1], **neg, crf=image_crf(_version(model)))}


def load_video_frames(path: Path, height: int, width: int, frames: int) -> mx.array:
    """ltx_core_mlx.utils.image.load_video_frames: the first `frames` frames as rgb24 at the source's own size
    (the same ffmpeg arguments: the pixels depend on them) → [-1, 1] (1, 3, F, H, W) bfloat16."""
    r = subprocess.run([FFMPEG, "-i", str(path), "-vframes", str(frames), "-s", f"{width}x{height}", "-pix_fmt",
                        "rgb24", "-f", "rawvideo", "-"], capture_output=True, check=False, timeout=600)  # fmt: skip
    if r.returncode:
        raise RunError(f"ffmpeg failed to read the source video: {r.stderr.decode(errors='ignore')[-300:]}")
    f = np.frombuffer(r.stdout, dtype=np.uint8).reshape(-1, height, width, 3).astype(np.float32) / 255.0 * 2.0 - 1.0
    return mx.array(f).transpose(3, 0, 1, 2)[None, ...].astype(mx.bfloat16)


def _audio_tokens(ctx, model, path: Path, frames: int, fps: float, start: float = 0.0):
    """The source's sound at 16 kHz stereo from `start` for frames / fps s (utils.audio.load_audio) through the audio
    VAE encoder (blocks.AudioConditioner) → (tokens [1, T, 128] float32, its encoded frames T), or None without
    sound."""
    data = load_audio(path, target_sample_rate=SOURCE_RATE, start_time=start, max_duration=frames / fps)
    if data is None:
        return None
    latent = encode_audio(data.waveform, data.sample_rate, ctx.component(model, "audio_encoder"), AudioProcessor())
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
            raise RunError(f"{where}: take {tid} has {info.num_frames} frames; this input takes at most {MAX_FRAMES}")
        frames = ltx_floor(info.num_frames)
        latent = _encode_clip(ctx.component(model, "vae_encoder"), path, info.height, info.width, frames)
        sound = _audio_tokens(ctx, model, path, frames, info.fps) if info.has_audio else None
    audio = sound[0] if sound else mx.zeros((1, 8, compute_audio_token_count(frames, frame_rate=info.fps), 16),
                                            dtype=mx.bfloat16)  # fmt: skip
    video, _ = PATCH.patchify(latent)
    tokens, _ = AudioPatchifier().patchify(audio)
    fhw = tuple(latent.shape[2:5])
    return {"latent": Latent(video, tokens, fhw, frames, info.fps, source_map={"take_id": tid}, clip=src["data"])}


def _encode_clip(encoder, path: Path, height: int, width: int, frames: int) -> mx.array:
    """retake.encode_video_tensor over the clip's first `frames` frames at height x width: tiled with
    TilingConfig.default() when it spans more than one tile, else at once; evaluated before the DiT loads."""
    pixels, tiling = load_video_frames(path, height, width, frames), TilingConfig.default()
    if len(prepare_tiles_for_encoding(pixels.shape, tiling)) > 1:
        # float32 blend buffers, back to bf16 (upstream's .to(dtype))
        latent = encoder.tiled_encode(pixels, tiling).astype(mx.bfloat16)
    else:
        latent = encoder.encode(pixels)
    mx.eval(latent)
    return latent


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
        raise RunError(f"{ctx.node}.source: out {end:.1f} is past the audio's end {start + seconds:.1f}")
    tokens, _ = AudioPatchifier().patchify(latent[:, :, :t, :])
    mx.eval(tokens)
    smap = {"take_id": src["take_id"]} | m["map"]
    return {"latent": Latent(None, tokens, None, frames, float(fps), source_map=smap, sound=(src["data"], start)),
            "duration": {"frames": frames, "fps": fps}}  # fmt: skip


def _guiders(params: dict, cond: Cond, audio_guided: bool):
    """LTX_2_3_PARAMS: video cfg / stg as set, rescale 0.7, modality 3, STG block 28; audio cfg 7 with the same
    (retake, extend) or no guidance (`MultiModalGuiderParams()`: audio to video's frozen audio)."""
    if cond.neg_video is None:
        raise RunError("the guided samplers need the prompt from ltx/encode-guided (the negative prompt)")
    video = MultiModalGuiderParams(cfg_scale=params["cfg"], stg_scale=params["stg"], rescale_scale=0.7,
                                   modality_scale=3.0, stg_blocks=[28])  # fmt: skip
    audio = MultiModalGuiderParams(cfg_scale=7.0, stg_scale=params["stg"], rescale_scale=0.7, modality_scale=3.0,
                                   stg_blocks=[28]) if audio_guided else MultiModalGuiderParams()  # fmt: skip
    return (create_multimodal_guider_factory(video, negative_context=cond.neg_video),
            create_multimodal_guider_factory(audio, negative_context=cond.neg_audio))  # fmt: skip


def _perturbed(kinds) -> BatchedPerturbationConfig:
    return BatchedPerturbationConfig(perturbations=[PerturbationConfig(perturbations=list(kinds))])


def _guided(ctx, x0_model, video: LatentState, audio: LatentState, cond: Cond, factories, sigmas, phase: str):
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
        vx0 = apply_denoise_mask(vg.calculate(cond_v, neg_v, ptb_v, mod_v), video.clean_latent, video.denoise_mask)
        ax0 = apply_denoise_mask(ag.calculate(cond_a, neg_a, ptb_a, mod_a), audio.clean_latent, audio.denoise_mask)
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
    audio `seed + 1`), or on a distilled model the distilled 8 steps without guidance (retake) → the whole clip's
    latent with its source map."""
    seed, cond, model = inputs["seed"], inputs["cond"], inputs["model"]
    distilled = is_distilled(model)
    factories = None if distilled else _guiders(params, cond, audio_guided=True)
    x0_model = _dit(ctx, model)
    video = noise_latent_state(video, sigma=1.0, seed=seed)
    audio = noise_latent_state(audio, sigma=1.0, seed=seed + 1)
    if distilled:  # RetakePipeline(distilled=True): the 8-step table, one pass per step, deterministic Euler
        vx, ax = _denoise(ctx, x0_model, video, audio, cond, STAGE1_SIGMAS, "distilled", None)
    else:
        sigmas = ltx2_schedule(params["steps"], fhw[0] * fhw[1] * fhw[2])
        vx, ax = _guided(ctx, x0_model, video, audio, cond, factories, sigmas, "guided")
    _settled(ctx, model, x0_model)
    return {"latent": Latent(vx, ax, fhw, frames, src.fps, source_map={"take_id": src.source_map["take_id"]} | smap)}


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
    if is_distilled(inputs.get("model")):
        return _extend_distilled(ctx, inputs, src, m)
    (f, h, w), k, after = src.fhw, m["latent"], params["direction"] == "after"
    t = src.audio.shape[1]
    extra = max(0, compute_audio_token_count(src.frames + 8 * k, frame_rate=src.fps) - t)
    new = (1, k * h * w, 128)

    def joined(old, new_part):  # source then new part (after), or new then source (before)
        return mx.concatenate([old, new_part] if after else [new_part, old], axis=1)

    vmask = joined(mx.zeros((1, src.video.shape[1], 1), dtype=mx.bfloat16), mx.ones((1, new[1], 1), dtype=mx.bfloat16))
    clean = joined(src.video, mx.zeros(new, dtype=mx.bfloat16))
    amask = joined(mx.zeros((1, t, 1), dtype=mx.bfloat16), mx.ones((1, extra, 1), dtype=mx.bfloat16))
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


def _pin(tokens: mx.array, per_frame: int) -> VideoConditionByLatentIndex:
    """extend_distilled._pin_prefix: `tokens` pinned over the first frames of a state (strength 1)."""
    return VideoConditionByLatentIndex(frame_indices=list(range(tokens.shape[1] // per_frame)), clean_latent=tokens,
                                       strength=1.0)  # fmt: skip


def _extend_distilled(ctx, inputs: dict, src: Latent, m: dict) -> dict:
    """ExtendDistilledPipeline.extend_from_video (after only: the graph check refuses before, as values_by): the
    source's last 25 frames (4 latent frames, re-encoded at half size for stage 1) and the audio tokens under them
    pinned at the start of a window of 4 + N latent frames, which DistilledPipeline's two stages generate (8 ancestral
    steps at half size, x2, 3 at full size); the window's new frames and audio are appended to the source's own."""
    model, cond, seed = inputs["model"], inputs["cond"], inputs["seed"]
    (f, h, w), k = src.fhw, m["latent"]
    if (h * 32) % 64 or (w * 32) % 64:
        raise RunError(f"Distilled extend needs sides on a 64 px grid (this take is {w * 32} × {h * 32}); pick Dev")
    if src.frames < EXTEND_CARRY:
        raise RunError(f"Distilled extend needs at least {EXTEND_CARRY} source frames (this take has {src.frames})")
    carry, fps, anc = (EXTEND_CARRY - 1) // 8 + 1, src.fps, ancestral(_version(model))
    carry_t = max(1, compute_audio_token_count(EXTEND_CARRY, frame_rate=fps))
    # the source audio spans the clip exactly (extend_distilled.fit_audio_latent: zero-padded or cut)
    t = compute_audio_token_count(src.frames, frame_rate=fps)
    audio = src.audio[:, :t, :]
    if audio.shape[1] < t:
        audio = mx.concatenate([audio, mx.zeros((1, t - audio.shape[1], 128), dtype=audio.dtype)], axis=1)
    ctx.progress(1, 1, "encoding the tail")
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        path = Path(tmp) / "source.mp4"
        path.write_bytes(src.clip)
        half = _encode_clip(ctx.component(model, "vae_encoder"), path, h * 16, w * 16, src.frames)
    video_half = PATCH.patchify(half[:, :, -carry:])[0]
    video_full, audio_carry = src.video[:, -carry * h * w :, :], audio[:, -carry_t:, :]
    window = 1 + 8 * (carry + k - 1)
    # stage 1: half size, the carry pinned (video seed, audio seed + 1, the legacy scalar blend)
    fhw1 = compute_video_latent_shape(window, h * 16, w * 16)
    at = compute_audio_token_count(window, frame_rate=fps)
    x0_model = _dit(ctx, model)
    video = noised_state((1, fhw1[0] * fhw1[1] * fhw1[2], 128), [_pin(video_half, fhw1[1] * fhw1[2])], fhw1,
                         compute_video_positions(*fhw1, frame_rate=fps), seed, 1.0)  # fmt: skip
    a_state = noised_state((1, at, 128), [_pin(audio_carry, 1)], (at, 1, 1), compute_audio_positions(at), seed + 1, 1.0)
    vx, ax = _denoise(ctx, x0_model, video, a_state, cond, STAGE1_SIGMAS, "stage 1",
                      seed + ANCESTRAL_SEED if anc else None)  # fmt: skip
    # stage 2: x2, the full-size carry pinned, renoised at sigma 0.909375 with seed + 2 (audio: the mask blend)
    up, fhw2 = _upsample(ctx, model, vx, fhw1)
    video = noised_state(up.shape, [_pin(video_full, fhw2[1] * fhw2[2])], fhw2,
                         compute_video_positions(*fhw2, frame_rate=fps), seed + 2, STAGE2_SIGMAS[0], up)  # fmt: skip
    a_state = noised_state(ax.shape, [_pin(audio_carry, 1)], (at, 1, 1), compute_audio_positions(at), seed + 2,
                           STAGE2_SIGMAS[0], ax, scalar_blend=False)  # fmt: skip
    vx, ax = _denoise(ctx, x0_model, video, a_state, cond, STAGE2_SIGMAS, "stage 2",
                      seed + ANCESTRAL_SEED_2 if anc else None)  # fmt: skip
    _settled(ctx, model, x0_model)
    new_v = vx[:, carry * h * w : fhw2[0] * h * w, :].astype(src.video.dtype)
    new_a = ax[:, carry_t:, :].astype(audio.dtype)
    out = Latent(mx.concatenate([src.video, new_v], axis=1), mx.concatenate([audio, new_a], axis=1),
                 (f + k, h, w), m["frames"], fps, source_map={"take_id": src.source_map["take_id"]} | m["map"])  # fmt: skip
    mx.eval(out.video, out.audio)
    return {"latent": out}


def sample_guided(ctx, params: dict, inputs: dict) -> dict:
    """a2vid_two_stage stage 1: half the canvas (64 px grid), pure noise (`seed`, the legacy scalar blend) with
    optional keyframe rows as i2v, the source audio frozen (mask 0: it steers the video through the A<->V cross
    attention, at sigma 0: frozen); video guided, audio guider off. RoPE at the clip's fps."""
    model, cond, seed, a = inputs["model"], inputs["cond"], inputs["seed"], inputs["audio"]
    size, d = inputs["canvas"], inputs["duration"]
    frames, fps = d["frames"], float(d["fps"])
    height, width = snap_output_dimensions(size["height"], size["width"], two_stage=True, warn=False)
    # plan 19 phase 4b, a2v --dfr (A2VidDFRPipeline): DFR's padded canvas with keyframe slots, the source audio
    # right-padded with zero tokens to it (a2vid_dfr.fit_audio_tokens)
    detail = bool(params.get("detail"))
    canvas, slots = dfr_canvas(frames) if detail else (frames, [])
    rope = dfr_fps(fps) if detail else fps
    fhw = compute_video_latent_shape(canvas, height // 2, width // 2)
    keyframe_frames(cond, frames, fps)  # a bad row fails before anything loads
    distilled = is_distilled(model)
    factories = None if distilled else _guiders(params, cond, audio_guided=False)
    x0_model = _dit(ctx, model)
    conds = _conditionings(ctx, model, cond, fhw, frames, fps, rope) + _slots(slots, rope)
    video = noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), conds, fhw,
                         compute_video_positions(*fhw, frame_rate=rope), seed, 1.0)  # fmt: skip
    tokens = a.audio
    if detail:
        n = compute_audio_token_count(canvas, frame_rate=fps)
        tokens = tokens[:, :n] if tokens.shape[1] >= n else \
            mx.concatenate([tokens, mx.zeros((1, n - tokens.shape[1], tokens.shape[2]), dtype=tokens.dtype)], axis=1)  # fmt: skip
    audio = _audio_state(tokens, mx.zeros((1, tokens.shape[1], 1), dtype=mx.bfloat16), frozen=True)
    if distilled:  # A2VidDistilledPipeline: DistilledPipeline's stage 1 (8 ancestral steps), the audio frozen
        noise = seed + ANCESTRAL_SEED if ancestral(_version(model)) else None
        vx, ax = _denoise(ctx, x0_model, video, audio, cond, STAGE1_SIGMAS, "stage 1", noise)
    else:
        sigmas = ltx2_schedule(params["steps"], fhw[0] * fhw[1] * fhw[2])
        vx, ax = _guided(ctx, x0_model, video, audio, cond, factories, sigmas, "guided")
    _settled(ctx, model, x0_model)
    out = Latent(vx, ax, fhw, frames, fps, source_map=a.source_map, sound=a.sound, guided=not distilled)
    return {"latent": _laid(out, video, slots, canvas)}


def distill(ctx, params: dict, inputs: dict) -> dict:
    """The dev model with the pack's pre-fused distilled LoRA as one bake row at strength 1 (the old
    `_fuse_distilled_lora`: dequantize, + the delta in float32, requantize): a second transformer entry in residency,
    reported with that LoRA. Pure: graph.keys builds it without a run (ctx None)."""
    from core.pack import LoraUse, place

    model = inputs["model"]
    if is_distilled(model):  # the LoRA is fused into the distilled transformer: a second fuse would apply it twice
        return {"model": model}
    spec = next(x for x in model.pack.spec["lora"]["prefused"] if x["variant"] == "distilled")
    # the shared index's id (its SHA-256), filled in at start (registry.fill_prefused); "" before: check_distill
    # fails such a run before anything loads
    lora_id = next((x["lora_id"] for x in lora_block_prefused(model.pack) if x["variant"] == "distilled"), "")
    f = place(model.pack.spec["variants"]["distilled"]["dir"]) / spec["file"]
    use = LoraUse(lora_id, "shared", f.stem, lora_id, f.stat().st_size if f.is_file() else 0, 1.0, "bake")
    return {"model": replace(model, loras=(*model.loras, use))}


def check_sample_guided(params: dict, wired: set, models: dict | None = None) -> str | None:
    """The detail pass is DFR's, on the distilled transformer only (a2v --dfr; board LtxFeatures frame 9)."""
    model = (models or {}).get("model")
    if model is not None and params.get("detail") and model.variant != "distilled":
        return ".detail: needs model variant distilled"
    return None


globals().update({"encode-guided": encode_guided, "encode-video": encode_video, "encode-audio": encode_audio_,
                  "sample-guided": sample_guided, "check_sample-guided": check_sample_guided,
                  "ic-lora": ic_lora})  # fmt: skip
