"""Stage functions of the ltx pack on torch (runtime torch, devices cuda: the NVIDIA variants distilled-fp8 and
dev-int8, docs/plan/17-nvidia-quantized.md 21.6): mlx.py's stages, ported step for step over Lightricks' ltx-core (the
library ltx-core-mlx was ported from), vendored in vendor/ltx_core (Apache-2.0, VENDORED.md). The sigma tables, the
seeds and the order of every noise draw, the blends, the conditioning order, the guiders and the decode are mlx.py's;
the noise comes from a CPU torch generator, so the bytes are not the Mac's (tests/test_ltx_torch.py ties the two
ports together on tiny weights).

Weights (model.yaml): ltx-core's own module names and key rules (`PARTS`), after `_community` maps the community
files onto the official names: unsloth's FP8 transformer (diffusers names, torchao per-row FP8 → the core Fp8Linear;
the layers it keeps bf16 stay dense) and its bf16 connector shards (the text projection included); DmitryDB's int8
ConvRot text encoder and dev transformer (comfy_quant int8_tensorwise, convrot, group 256). Each of those int8
weights is dequantized with its scale by `tensor_hook` and requantized by the core loader into an Int8Linear whose
input is rotated (`rotated`): the same int8 values (the scale is the amax one, so they round back exactly); a layer
the file keeps bf16 is quantized at load, unrotated. The upscaler, VAEs, vocoder and duration head are the official
files. A transformer without the LTX config in its header (unsloth's) takes transformer-config.json, the official
files' own (byte-equal in the official dev, distilled and DmitryDB files).

Not on NVIDIA yet, refused before anything loads: LoRA rows (no bake into FP8 or ConvRot weights; adapters later) and
Prompt Relay beats (ltx-core has no Prompt Relay at the pin; the duration head, for Auto length, it has).
`ltx/distill` hands stage 2 the dev variant's `distill_from` (dev-int8 → distilled-fp8: the dev transformer with the
distilled LoRA fused at 1, as unsloth ships it) instead of baking the LoRA.

Handles: mlx.py's Cond and Latent with torch tensors on the run's device."""

import functools
import itertools
import json
import math
import subprocess
import sys
import tempfile
import wave
from dataclasses import dataclass, replace
from fractions import Fraction
from pathlib import Path

import accelerate
import numpy as np
import torch
from PIL import Image
from safetensors import safe_open

from core import media
from core import pack as packs
from core.graph import RunError, auto_frames
from packs.ltx.common import (  # the runtime-neutral part
    ANCESTRAL_SEED,
    ANCESTRAL_SEED_2,
    CANVAS_MAX,
    FFMPEG,
    FRAMES,
    MAX_FRAMES,
    NEGATIVE_PROMPT,
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
    cover,
    crf_round_trip,
    extend_map,
    ic_lora,
    image_crf,
    keyframe_frames,
    ltx_floor,
    model_version,
    retake_map,
)
from packs.ltx.common import (
    check_sample as _check_sample,
)

sys.path.append(str(Path(__file__).parent / "vendor"))  # the vendored model library: `import ltx_core`

from ltx_core.components.diffusion_steps import EulerAncestralDiffusionStep
from ltx_core.components.guiders import MultiModalGuiderParams, create_multimodal_guider_factory
from ltx_core.components.patchifiers import AudioPatchifier, VideoLatentPatchifier, get_pixel_coords
from ltx_core.duration_head.model_configurator import (
    DURATION_HEAD_KEY_OPS,
    DurationHeadConfigurator,
)
from ltx_core.guidance.perturbations import (
    BatchedPerturbationConfig,
    Perturbation,
    PerturbationConfig,
    PerturbationType,
)
from ltx_core.loader.module_ops import apply_module_ops
from ltx_core.model.audio_vae import (
    AUDIO_VAE_DECODER_COMFY_KEYS_FILTER,
    AUDIO_VAE_ENCODER_COMFY_KEYS_FILTER,
    VOCODER_COMFY_KEYS_FILTER,
    AudioDecoderConfigurator,
    AudioEncoderConfigurator,
    VocoderConfigurator,
    decode_audio,
    encode_audio,
)
from ltx_core.model.transformer import (
    LTXV_MODEL_COMFY_RENAMING_MAP,
    LTXModelConfigurator,
    Modality,
    TokenTimesteps,
    X0Model,
)
from ltx_core.model.transformer.model_configurator import transformer_checkpoint_selection_ops
from ltx_core.model.upsampler import LatentUpsamplerConfigurator
from ltx_core.model.video_vae import (
    VAE_DECODER_COMFY_KEYS_FILTER,
    VAE_ENCODER_COMFY_KEYS_FILTER,
    TileSizeConfig,
    VideoDecoderConfigurator,
    VideoEncoderConfigurator,
)
from ltx_core.text_encoders.gemma import (
    EMBEDDINGS_PROCESSOR_KEY_OPS,
    EmbeddingsProcessorConfigurator,
    GemmaTextEncoderConfigurator,
    get_gemma_ops,
)
from ltx_core.text_encoders.gemma.encoders.encoder_configurator import (
    _build_gemma4_unified_llm_key_ops,
)
from ltx_core.types import VIDEO_SCALE_FACTORS, Audio, AudioLatentShape, VideoLatentShape

ENV = "torch"
BF16 = torch.bfloat16
PATCH, AUDIO = VideoLatentPatchifier(patch_size=1), AudioPatchifier(patch_size=1)
CONFIG = Path(__file__).parent / "transformer-config.json"
NO_RELAY = "Prompt Relay (beats) is not available on NVIDIA hosts yet"
NO_DETAIL = "the detail pass is Mac only for now"  # API 1.39, plan 19 phase 4: sample.detail (DFR) on NVIDIA
NO_NAG = "NAG is Mac only for now"  # API 1.39: encode.negative (ltx-core has no NAG at the pin); `compute` is ignored
NO_LORA = ": LoRAs are not available on the NVIDIA (fp8 / int8) variants yet"


@dataclass(frozen=True)
class Cond:
    video: torch.Tensor  # [1, 1024, 4096]
    audio: torch.Tensor  # [1, 1024, 2048]
    images: tuple = ()  # keyframe rows (H.264-round-tripped RGB uint8, seconds, strength, binding), unresolved
    images_at: str = ""  # where the rows came from, for the run's error texts (no beats: check_encode)
    neg_video: torch.Tensor | None = None  # the fixed negative prompt's (encode-guided: CFG)
    neg_audio: torch.Tensor | None = None
    crf: int | None = None  # the keyframe images' CRF, from the encoding model's generation (common.image_crf)


@dataclass(frozen=True)
class Latent:
    video: torch.Tensor  # tokens [1, F * H * W, 128], appended keyframe tokens after them
    audio: torch.Tensor  # tokens [1, T, 128]
    fhw: tuple[int, int, int]
    frames: int
    fps: float
    upscaled: bool = False
    source_map: dict | None = None
    sound: tuple | None = None  # audio to video: (the source file's bytes, in seconds), muxed by decode


# --- weights


def _gemma4_key_ops():
    """ltx-core's Gemma 4 unified key rules for the Comfy-flat layout (`model.layers.*`) of the official and the
    DmitryDB text encoder files (get_gemma_ops picks the same from the file: checked in build)."""
    return _build_gemma4_unified_llm_key_ops(comfy_flat=True)


# LTX-2.5's transformer files hold the connector too: the embeddings processor's (use_embeddings_connector)
CONNECTOR_OUT = transformer_checkpoint_selection_ops({"transformer": {"use_embeddings_connector": True}})
# component → (ltx-core's configurator, its key rules): what ltx-pipelines builds each component from
PARTS = {
    "transformer": (LTXModelConfigurator, (LTXV_MODEL_COMFY_RENAMING_MAP, CONNECTOR_OUT)),
    "connector": (EmbeddingsProcessorConfigurator, (EMBEDDINGS_PROCESSOR_KEY_OPS,)),
    "text_encoder": (GemmaTextEncoderConfigurator, (_gemma4_key_ops(),)),
    "upscaler": (LatentUpsamplerConfigurator, ()),
    "vae": (VideoDecoderConfigurator, (VAE_DECODER_COMFY_KEYS_FILTER,)),
    "vae_encoder": (VideoEncoderConfigurator, (VAE_ENCODER_COMFY_KEYS_FILTER,)),
    "audio_vae": (AudioDecoderConfigurator, (AUDIO_VAE_DECODER_COMFY_KEYS_FILTER,)),
    "vocoder": (VocoderConfigurator, (VOCODER_COMFY_KEYS_FILTER,)),
    "audio_encoder": (AudioEncoderConfigurator, (AUDIO_VAE_ENCODER_COMFY_KEYS_FILTER,)),
}
# unsloth's transformer: diffusers' names back to the official ones (the inverse of diffusers'
# convert_ltx2_transformer_to_diffusers, plus LTX-2.5's prompt adaLN pair), segment by segment
UNSLOTH = {"proj_in": "patchify_proj", "audio_proj_in": "audio_patchify_proj",
           "av_cross_attn_video_scale_shift": "av_ca_video_scale_shift_adaln_single",
           "av_cross_attn_video_a2v_gate": "av_ca_a2v_gate_adaln_single",
           "av_cross_attn_audio_scale_shift": "av_ca_audio_scale_shift_adaln_single",
           "av_cross_attn_audio_v2a_gate": "av_ca_v2a_gate_adaln_single",
           "video_a2v_cross_attn_scale_shift_table": "scale_shift_table_a2v_ca_video",
           "audio_a2v_cross_attn_scale_shift_table": "scale_shift_table_a2v_ca_audio",
           "norm_q": "q_norm", "norm_k": "k_norm", "time_embed": "adaln_single", "audio_time_embed": "audio_adaln_single",
           "prompt_adaln": "prompt_adaln_single", "audio_prompt_adaln": "audio_prompt_adaln_single"}  # fmt: skip
# unsloth's connector shards (diffusers LTX2TextConnectors) → the official transformer file's connectors and the text
# encoder file's projection
CONNECTOR = (("video_text_proj_in.", "text_embedding_projection.video_aggregate_embed."),
             ("audio_text_proj_in.", "text_embedding_projection.audio_aggregate_embed."),
             ("video_connector.", "model.diffusion_model.video_embeddings_connector."),
             ("audio_connector.", "model.diffusion_model.audio_embeddings_connector."))  # fmt: skip
CONNECTOR_SEGMENTS = {
    "transformer_blocks": "transformer_1d_blocks",
    "norm_q": "q_norm",
    "norm_k": "k_norm",
}
CONVROT = {
    "format": "int8_tensorwise",
    "convrot": True,
    "convrot_groupsize": 256,
}  # comfy_quant: what is read
CONVROT_GROUP = CONVROT["convrot_groupsize"]


def _community(component: str, key: str) -> str | None:
    """A community file's key → the official file's name for it (an official key as it is); DmitryDB's comfy_quant
    tags and int8 scales are read by tensor_hook, not loaded."""
    if key.endswith((".comfy_quant", ".weight_scale")):
        return None
    if component == "transformer" and not key.startswith("model."):  # unsloth
        key = key.removeprefix("unsloth_root::")
        for suffix, attr in (("._weight_qdata", ".weight"), ("._weight_scale", ".scale")):
            if key.endswith(suffix):
                key = key.removesuffix(suffix) + attr
        return "model.diffusion_model." + ".".join(UNSLOTH.get(s, s) for s in key.split("."))
    if component == "connector":
        for old, new in CONNECTOR:
            if key.startswith(old):
                return new + ".".join(CONNECTOR_SEGMENTS.get(s, s) for s in key.removeprefix(old).split("."))
    return key


@functools.cache
def rename(component: str, key: str) -> str | None:
    """A checkpoint key → the ltx-core module's name for it (None: not this component's), through ltx-core's own key
    rules; a rule with a value operation is a one-key rename there (vocoder.) or a tie (lm_head: dropped in build),
    so the first key it emits is the name."""
    key = _community(component, key)
    for ops in PARTS[component][1]:
        if key is None:
            return None
        key = ops.apply_to_key(key)
        if key is not None and (op := ops._value_operation(key)) is not None:
            key = next(iter(op.apply_abstract(key, {key: torch.empty(0, device="meta")})))
    return key


def metadata(path: Path) -> dict:
    """A safetensors file's __metadata__, JSON values parsed (ltx-core's CheckpointReader.read_checkpoint_metadata)."""
    out = {}
    with safe_open(str(path), "pt") as f:
        for k, v in (f.metadata() or {}).items():
            try:
                out[k] = json.loads(v)
            except ValueError:
                out[k] = v
    return out


def _dit_meta(model) -> dict:
    """The transformer's LTX metadata (config, model_version, gemma_source_checkpoint), which the connector shares:
    its file's own, else the pack's copy (unsloth's file carries none; so does a host without the file yet)."""
    files = model.files("transformer")
    meta = metadata(files[0]) if files else {}
    return meta if "config" in meta else json.loads(CONFIG.read_text())


def _version(model) -> tuple:
    """The transformer's model_version (common.model_version), which picks the generation's rules."""
    return model_version(_dit_meta(model))


def build(component: str, model):
    """The component, its parameters on the meta device (buffers real, on the CPU: the loader moves them)."""
    configurator = PARTS[component][0]
    if component in ("text_encoder", "connector"):
        te = str(model.files("text_encoder")[0])
        configurator = configurator.with_gemma_model_path(te)
    meta = _dit_meta(model) if component in ("transformer", "connector") else metadata(model.files(component)[0])
    with accelerate.init_empty_weights(include_buffers=False):
        module = configurator.from_metadata(meta)
    if component == "text_encoder":
        key_ops, ops = get_gemma_ops(te)
        if key_ops.name != PARTS[component][1][0].name:
            raise RunError(f"{Path(te).name}: a Gemma layout this pack does not read ({key_ops.name})")
        module = apply_module_ops(module, ops)  # rotary buffers, embed scale, tokenizer, processor
        # encode reads the inner model's hidden states only (its tied lm_head would stay on meta)
        module.model.lm_head = torch.nn.Identity()
    return module


def _int8(component: str, model) -> dict:
    """module → (file, its scale key) of each int8 weight of this component the files tag with comfy_quant
    (DmitryDB's), after checking every tag is the ConvRot layout this pack reads; {} for the other files."""
    return _tags(component, tuple((p, p.stat().st_mtime_ns) for p in model.files(component)))


@functools.lru_cache(maxsize=4)
def _tags(component: str, files: tuple) -> dict:
    out = {}
    for path, _ in files:
        keys = [k for k in packs.header(path) if k.endswith(".comfy_quant")]
        if not keys:
            continue
        with safe_open(str(path), "pt") as f:
            tags = [json.loads(bytes(f.get_tensor(k).tolist())) for k in keys]
        if any({k: t.get(k) for k in CONVROT} != CONVROT for t in tags):
            raise RunError(f"{path.name}: not the int8 ConvRot (group {CONVROT_GROUP}) layout this pack reads")
        for k in keys:
            base = k.removesuffix(".comfy_quant")
            if (name := rename(component, f"{base}.weight")) is not None:  # the dev file's connector is not the DiT's
                out[name.removesuffix(".weight")] = (path, f"{base}.weight_scale")
    return out


def rotated(component: str, model):
    """The ConvRot group of a Linear stored int8 ConvRot (core Int8Linear `rot_group`), per tensor: a layer the file
    keeps bf16 has none."""
    found = _int8(component, model)
    return lambda name: CONVROT_GROUP if name in found else None


def tensor_hook(component: str, model):
    """DmitryDB's int8 weights → float32 q · scale (the core loader then quantizes them back to the same int8 values
    into a rotated Int8Linear); None for the other files."""
    found = _int8(component, model)
    if not found:
        return None
    spec, precision = model.pack.component(component), model.precision_of(component)
    handles = {p: safe_open(str(p), "pt") for p, _ in found.values()}

    def hook(name: str, tensor: torch.Tensor) -> torch.Tensor:
        if tensor.dtype != torch.int8:
            return tensor
        module = name.removesuffix(".weight")
        if not packs.quantized(spec, precision, module):  # a plain Linear of rotated values: wrong
            raise RunError(f"{module}: an int8 ConvRot weight outside the {component}'s quantize globs")
        path, key = found[module]
        return tensor.float() * handles[path].get_tensor(key).to(tensor.device).float()

    return hook


# --- states: mlx.py's LatentState as torch tensors, RoPE positions as ltx-core's [start, end) bounds


@dataclass(frozen=True)
class State:
    latent: torch.Tensor  # tokens (B, T, C)
    clean: torch.Tensor
    mask: torch.Tensor  # the denoise mask (B, T, 1): 1 generate, 0 keep
    positions: torch.Tensor  # (B, 3, T, 2) video, (B, 1, T, 2) audio
    keyframes: torch.Tensor | None = None  # the keyframe marker (B, T, 1): the first latent frame
    frozen: bool = False  # the whole stream is conditioning: the DiT sees sigma 0 for it (LatentState.frozen)


def video_positions(f: int, h: int, w: int, fps: float, device) -> torch.Tensor:
    """Pixel bounds with the causal fix, time in seconds (VideoLatentTools.create_initial_state)."""
    bounds = PATCH.get_patch_grid_bounds(VideoLatentShape(1, 128, f, h, w), device)
    pos = get_pixel_coords(bounds, VIDEO_SCALE_FACTORS, causal_fix=True).float()
    pos[:, 0] /= fps
    return pos


def keyframe_positions(frame: int, h: int, w: int, fps: float, device) -> torch.Tensor:
    """An appended image's tokens at pixel frame `frame`, one pixel frame wide (VideoConditionByKeyframeIndex)."""
    bounds = PATCH.get_patch_grid_bounds(VideoLatentShape(1, 128, 1, h, w), device)
    pos = get_pixel_coords(bounds, VIDEO_SCALE_FACTORS, causal_fix=frame == 0).float()
    pos[:, 0] += frame
    pos[:, 0, ..., 1:] = pos[:, 0, ..., :1] + 1
    pos[:, 0] /= fps
    return pos


def audio_positions(t: int, device) -> torch.Tensor:
    return AUDIO.get_patch_grid_bounds(AudioLatentShape(1, 8, t, 16), device)


def audio_tokens(frames: int, fps: float) -> int:
    """utils.positions.compute_audio_token_count: 25 audio latents a second."""
    return round(frames / fps * 25)


def first_frame(mask: torch.Tensor, tokens_per_frame: int) -> torch.Tensor:
    out = torch.zeros_like(mask)
    out[:, :tokens_per_frame] = 1
    return out


def randn(shape, generator: torch.Generator, device) -> torch.Tensor:
    """Standard normal noise from a CPU generator (device independent), float32 on `device`."""
    return torch.randn(shape, generator=generator).to(device)


def seeded(seed: int) -> torch.Generator:
    return torch.Generator().manual_seed(seed)


def noise_state(state: State, sigma: float, seed: int) -> State:
    """noise_latent_state: noise · mask · sigma + clean · (1 − mask · sigma)."""
    noise = randn(state.clean.shape, seeded(seed), state.clean.device).to(state.clean.dtype)
    scaled = state.mask * sigma
    return replace(state, latent=(noise * scaled + state.clean * (1.0 - scaled)).to(state.clean.dtype))


def noised_state(shape, conditionings, fhw, positions, seed, sigma, initial=None, scalar_blend=True) -> State:
    """mlx.py noised_state (helpers.create_noised_state): the scalar blend on the plain grid first, then the
    conditionings (video in both stages, stage-1 audio); else conditionings then noise_state (stage-2 audio)."""
    device = positions.device
    latent = torch.zeros(shape, dtype=BF16, device=device) if initial is None else initial
    mask = torch.ones((shape[0], shape[1], 1), dtype=torch.float32, device=device)
    marker = first_frame(mask, fhw[1] * fhw[2]) if positions.shape[1] == 3 else None
    state = State(latent, latent, mask, positions, marker)
    if not scalar_blend:
        for c in conditionings:
            state = c(state, fhw)
        return noise_state(state, sigma, seed)
    noise = randn(shape, seeded(seed), device).to(BF16)
    state = replace(state, latent=noise * sigma + state.clean * (1.0 - sigma))
    for c in conditionings:
        state = c(state, fhw)
    return state


def latent_index_cond(tokens: torch.Tensor, strength: float):
    """An image at frame 0 replaces latent frame 0 (VideoConditionByLatentIndex, the keyframe marker kept)."""

    def apply(s: State, fhw) -> State:
        n = fhw[1] * fhw[2]
        latent, clean, mask = s.latent.clone(), s.clean.clone(), s.mask.clone()
        latent[:, :n], clean[:, :n], mask[:, :n] = tokens, tokens, 1.0 - strength
        return replace(s, latent=latent, clean=clean, mask=mask)

    return apply


def keyframe_cond(tokens: torch.Tensor, frame: int, fps: float, strength: float):
    """An image at any other frame appends its tokens (VideoConditionByKeyframeIndex as mlx.py's: no attention mask,
    unmarked)."""

    def apply(s: State, fhw) -> State:
        mask = torch.full((1, tokens.shape[1], 1), 1.0 - strength, device=tokens.device)
        return State(torch.cat([s.latent, tokens], 1), torch.cat([s.clean, tokens], 1), torch.cat([s.mask, mask], 1),
                     torch.cat([s.positions, keyframe_positions(frame, fhw[1], fhw[2], fps, tokens.device)], 2),
                     None if s.keyframes is None else torch.cat([s.keyframes, torch.zeros_like(mask)], 1))  # fmt: skip

    return apply


def to_pixels(rgb: np.ndarray, height: int, width: int, device) -> torch.Tensor:
    """media_io SdrStill.tensor: resize_and_center_crop (scaled to cover, sides rounded up, interpolate bilinear
    align_corners False on float32 on the device, centre crop), then normalize_images x / 127.5 - 1 → (1, 3, H, W)
    bf16."""
    h, w = rgb.shape[:2]
    scale = max(height / h, width / w)
    new_h, new_w = math.ceil(h * scale), math.ceil(w * scale)
    x = torch.tensor(rgb, dtype=torch.float32, device=device).permute(2, 0, 1)[None]
    x = torch.nn.functional.interpolate(x, size=(new_h, new_w), mode="bilinear", align_corners=False)
    top, left = (new_h - height) // 2, (new_w - width) // 2
    return (x[:, :, top : top + height, left : left + width] / 127.5 - 1.0).to(device, BF16)


def _conditionings(ctx, model, cond: Cond, fhw, frames: int, fps: float) -> list:
    """mlx.py _conditionings: each row encoded at the grid fhw, frame 0 in place, any other appended, in row order."""
    if not cond.images:
        return []
    at = keyframe_frames(cond, frames, fps)
    encoder = ctx.component(model, "vae_encoder")
    ctx.progress(1, 1, "encoding images")
    out = []
    for (rgb, _, strength, _), frame in zip(cond.images, at, strict=True):  # auto only (check_keyframes)
        tokens = PATCH.patchify(encoder(to_pixels(rgb, fhw[1] * 32, fhw[2] * 32, ctx.device)[:, :, None]))
        out.append(latent_index_cond(tokens, strength) if frame == 0 else keyframe_cond(tokens, frame, fps, strength))
    return out


# --- the transformer: one pass, the samplers


def _uniform(mask: torch.Tensor) -> bool:
    return bool((mask == 1).all())


def blend(x0: torch.Tensor, s: State) -> torch.Tensor:
    """apply_denoise_mask: the prediction where generated, the clean latent where kept."""
    return x0 * s.mask + s.clean * (1.0 - s.mask)


def euler(x: torch.Tensor, x0: torch.Tensor, sigma: float, sigma_next: float) -> torch.Tensor:
    """mlx_arsenal euler_step: x + (σ' − σ)(x − x0)/σ (in float32)."""
    return (x.float() + (sigma_next - sigma) * (x.float() - x0.float()) / sigma).to(x.dtype)


def _pass(x0_model, vx, ax, video: State, audio: State, sigma: float, vctx, actx, kinds=None):
    """One X0Model pass: sigma in bf16 as mlx.py passes it; per-token timesteps mask · sigma for a conditioned state;
    a frozen state's sigma 0 (helpers.modality_from_latent_state); `kinds`: the perturbations (STG, modality
    isolation) of this pass."""
    sig = torch.full((vx.shape[0],), sigma, dtype=BF16, device=vx.device)

    def modality(x, s: State, context, keyframes=None) -> Modality:
        own = torch.zeros_like(sig) if s.frozen else sig
        steps = own[:, None] if _uniform(s.mask) else (s.mask * sigma).squeeze(-1)
        return Modality(latent=x, sigma=own, timesteps=TokenTimesteps(steps), positions=s.positions,
                        context=context, keyframes_mask=keyframes)  # fmt: skip

    perturbations = None if kinds is None else BatchedPerturbationConfig(
        [PerturbationConfig(list(kinds))], x0_model.num_blocks, vx.device, vx.dtype)  # fmt: skip
    return x0_model(modality(vx, video, vctx, video.keyframes), modality(ax, audio, actx), perturbations)


def _denoise(
    ctx,
    x0_model,
    video: State,
    audio: State,
    cond: Cond,
    sigmas,
    phase: str,
    noise_seed: int | None,
):
    """mlx.py _denoise: deterministic Euler (noise_seed None) or the ancestral loop (eta 1, its noise drawn video
    then audio from one generator seeded noise_seed), a cancel point and a progress event per step."""
    vx, ax = video.latent, audio.latent
    stepper, table = EulerAncestralDiffusionStep(), torch.tensor(sigmas)
    gen = None if noise_seed is None else seeded(noise_seed)
    steps = list(itertools.pairwise(sigmas))
    for i, (sigma, sigma_next) in enumerate(steps):
        ctx.check()
        vx0, ax0 = _pass(x0_model, vx, ax, video, audio, sigma, cond.video, cond.audio)
        vx0, ax0 = blend(vx0, video), blend(ax0, audio)
        if gen is None:
            vx, ax = euler(vx, vx0, sigma, sigma_next), euler(ax, ax0, sigma, sigma_next)
        elif sigma_next == 0:  # terminal: the x0 prediction
            vx, ax = vx0.to(vx.dtype), ax0.to(ax.dtype)
        else:
            vn, an = randn(vx.shape, gen, vx.device), randn(ax.shape, gen, ax.device)
            vnext = stepper.step(vx.float(), vx0, table, i, noise=vn)
            anext = stepper.step(ax.float(), ax0, table, i, noise=an)
            vx, ax = blend(vnext, video).to(vx.dtype), blend(anext, audio).to(ax.dtype)
        ctx.progress(i + 1, len(steps), phase)
        if gen is not None and sigma_next == 0:
            break
    return vx, ax


def _guiders(params: dict, cond: Cond, audio_guided: bool):
    """mlx.py _guiders (LTX_2_3_PARAMS)."""
    if cond.neg_video is None:
        raise RunError("the guided samplers need the prompt from ltx/encode-guided (the negative prompt)")
    video = MultiModalGuiderParams(cfg_scale=params["cfg"], stg_scale=params["stg"], rescale_scale=0.7,
                                   modality_scale=3.0, stg_blocks=[28])  # fmt: skip
    audio = MultiModalGuiderParams(cfg_scale=7.0, stg_scale=params["stg"], rescale_scale=0.7, modality_scale=3.0,
                                   stg_blocks=[28]) if audio_guided else MultiModalGuiderParams()  # fmt: skip
    return (create_multimodal_guider_factory(video, negative_context=cond.neg_video),
            create_multimodal_guider_factory(audio, negative_context=cond.neg_audio))  # fmt: skip


def _guided(ctx, x0_model, video: State, audio: State, cond: Cond, factories, sigmas, phase: str):
    """mlx.py _guided (samplers.guided_denoise_loop): per step the conditioned pass, then as the guiders ask the
    negative-prompt, STG (block 28's self-attention skipped) and modality (both A<->V cross attentions skipped)
    passes, one after another; each guider combines them (its rescale over all tokens, as mlx.py), x0 is blended
    with the clean latent by the mask, then a deterministic Euler step."""
    vx, ax = video.latent, audio.latent
    steps = list(itertools.pairwise(sigmas))
    for i, (sigma, sigma_next) in enumerate(steps):
        ctx.check()
        vg, ag = (f.build_from_sigma(sigma) for f in factories)
        run = functools.partial(_pass, x0_model, vx, ax, video, audio, sigma)  # (vctx, actx[, kinds])
        cond_v, cond_a = run(cond.video, cond.audio)
        neg_v = neg_a = ptb_v = ptb_a = mod_v = mod_a = 0.0
        if vg.do_unconditional_generation() or ag.do_unconditional_generation():
            neg_v, neg_a = run(vg.negative_context if vg.negative_context is not None else cond.video,
                               ag.negative_context if ag.negative_context is not None else cond.audio)  # fmt: skip
        if vg.do_perturbed_generation() or ag.do_perturbed_generation():
            kinds = ([Perturbation(type=PerturbationType.SKIP_VIDEO_SELF_ATTN, blocks=vg.params.stg_blocks)]
                     if vg.do_perturbed_generation() else []) + \
                    ([Perturbation(type=PerturbationType.SKIP_AUDIO_SELF_ATTN, blocks=ag.params.stg_blocks)]
                     if ag.do_perturbed_generation() else [])  # fmt: skip
            ptb_v, ptb_a = run(cond.video, cond.audio, kinds)
        if vg.do_isolated_modality_generation() or ag.do_isolated_modality_generation():
            mod_v, mod_a = run(cond.video, cond.audio, [Perturbation(type=PerturbationType.SKIP_A2V_CROSS_ATTN, blocks=None),
                                                        Perturbation(type=PerturbationType.SKIP_V2A_CROSS_ATTN, blocks=None)])  # fmt: skip
        vx0 = blend(vg.calculate(cond_v, neg_v, ptb_v, mod_v), video)
        ax0 = blend(ag.calculate(cond_a, neg_a, ptb_a, mod_a), audio)
        vx, ax = euler(vx, vx0, sigma, sigma_next), euler(ax, ax0, sigma, sigma_next)
        ctx.progress(i + 1, len(steps), phase)
    return vx, ax


def ltx2_schedule(steps: int, tokens: int) -> list[float]:
    """mlx.py ltx2_schedule (mlx_arsenal dynamic_shift_schedule: base 0.95 at 1024 tokens, max 2.05 at 4096,
    stretched to end at 0.1, then 0)."""
    sigmas = np.linspace(1.0, 0.0, steps + 1)
    slope = (2.05 - 0.95) / (4096 - 1024)
    shift = math.exp(tokens * slope + 0.95 - slope * 1024)
    nz = sigmas != 0
    sigmas[nz] = shift / (shift + (1.0 / sigmas[nz] - 1.0))
    one_minus = 1.0 - sigmas[nz]
    if (scale := one_minus[-1] / (1.0 - 0.1)) != 0:
        sigmas[nz] = 1.0 - one_minus / scale
    return sigmas.tolist()


# --- stages


def canvas(ctx, params: dict, inputs: dict) -> dict:
    return {"canvas": {"width": params["width"] // 64 * 64, "height": params["height"] // 64 * 64}}


def duration(ctx, params: dict, inputs: dict) -> dict:
    return {"duration": {"frames": params["frames"], "fps": params["fps"]}}


def _encode(ctx, model, texts: list[str]) -> list:
    """Gemma 4 (all hidden states, left-padded to 1024 tokens) → the connector → (video, audio) embeddings each."""
    tower = ctx.component(model, "text_encoder")
    connector = ctx.component(model, "connector")
    ctx.check()
    ctx.progress(1, 1, "encoding")
    out = []
    for t in texts:
        hidden, mask = tower.encode([t])[0]
        o = connector.process_hidden_states(hidden, mask, padding_side="left")
        out.append((o.video_encoding, o.audio_encoding))
    return out


def check_encode(params: dict, wired: set, models: dict | None = None) -> str | None:
    if params.get("beats"):
        return f".beats: {NO_RELAY}"
    return f".negative: {NO_NAG}" if params.get("negative", "").strip() else None


@torch.no_grad()
def encode(ctx, params: dict, inputs: dict) -> dict:
    if not inputs["prompt"].strip():
        raise RunError(f"{ctx.node}: the prompt is empty")
    if why := check_encode(params, set()):
        raise RunError(f"{ctx.node}{why}")
    ((video, audio),) = _encode(ctx, inputs["model"], [inputs["prompt"]])
    return {"cond": Cond(video, audio, crf=image_crf(_version(inputs["model"])))}


@torch.no_grad()
def encode_guided(ctx, params: dict, inputs: dict) -> dict:
    """The prompt, then the negative prompt (API 1.39: the typed one, blank = the fixed one), with Gemma loaded once."""
    if not inputs["prompt"].strip():
        raise RunError(f"{ctx.node}: the prompt is empty")
    negative = params.get("negative", "") if params.get("negative", "").strip() else NEGATIVE_PROMPT
    (v, a), (nv, na) = _encode(ctx, inputs["model"], [inputs["prompt"], negative])
    return {"cond": Cond(v, a, neg_video=nv, neg_audio=na, crf=image_crf(_version(inputs["model"])))}


def check_keyframes(params: dict, wired: set, models: dict | None = None) -> str | None:
    """API 1.39 bindings (plan 19 phase 5) are Mac only: a non-auto row is refused before anything loads."""
    for i, r in enumerate(params.get("images") or [], 1):
        if r.get("binding", "auto") != "auto":
            return f".images[{i}].binding: Replace and Guide are Mac only for now"
    return None


def keyframes(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py keyframes: the rows onto the conditioning, the decode and the H.264 round trip done here, once."""
    trips: dict[int, np.ndarray] = {}
    for r in params["images"]:
        if id(r["image"]) not in trips:
            trips[id(r["image"])] = cover(crf_round_trip(r["image"], inputs["cond"].crf), CANVAS_MAX)
    if why := check_keyframes(params, set()):
        raise RunError(f"{ctx.node}{why}")
    rows = tuple((trips[id(r["image"])], float(r["seconds"]), float(r["strength"]), "auto") for r in params["images"])
    if not rows:
        raise RunError(f"{ctx.node}.images: at least 1 items")
    return {"cond": replace(inputs["cond"], images=rows, images_at=f"{ctx.node}.images")}


@functools.lru_cache(maxsize=1)
def _duration_head(path: Path, size: int, mtime: int, device: str):
    """The DurationHead (3.8 MB), loaded once; ponytail: outside the residency manager, it is that small."""
    from core import loaders_torch

    with accelerate.init_empty_weights(include_buffers=False):
        head = DurationHeadConfigurator.from_metadata(metadata(path))
    return loaders_torch.load(head, [path], dev=device, dtype=None, rename=DURATION_HEAD_KEY_OPS.apply_to_key)


def predict_frames(model, cond: Cond, fps: float) -> int:
    """Auto length: the DurationHead's seconds from the prompt embeddings on the frames grid."""
    found = model.files("duration_head")  # model.yaml: a component of the distilled variants
    if not found:
        raise RunError("auto length needs the model's duration_head (model.yaml) in the model folder")
    p = found[0]
    head = _duration_head(p, p.stat().st_size, p.stat().st_mtime_ns, str(cond.video.device))
    seconds = head(video_tokens=cond.video, audio_tokens=cond.audio)
    return auto_frames(float(seconds.float().item()), fps, FRAMES)


@torch.no_grad()
def sample(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py sample: stage 1 from the seed's noise at half the canvas; stage 2 the (upscaled) latent renoised at
    0.909375 with seed + 2; both ancestral from 2.5 on (seed + 10000, + 20000: distilled.py), else deterministic;
    audio to video's stage 2 deterministic with the source audio frozen (a2vid_two_stage)."""
    model, cond, seed = inputs["model"], inputs["cond"], inputs["seed"]
    dev, anc = ctx.device, ancestral(_version(model))
    if params["role"] == "stage1":
        size, d = inputs["canvas"], inputs["duration"]
        fps = float(d["fps"])
        frames = predict_frames(model, cond, fps) if d["frames"] == "auto" else d["frames"]
        fhw = (
            (frames + 7) // 8,
            size["height"] // 64 * 64 // 2 // 32,
            size["width"] // 64 * 64 // 2 // 32,
        )
        keyframe_frames(cond, frames, fps)  # a bad row fails before anything loads
        x0_model = X0Model(ctx.component(model, "transformer"))
        t = audio_tokens(frames, fps)
        video = noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), _conditionings(ctx, model, cond, fhw, frames, fps),
                             fhw, video_positions(*fhw, fps, dev), seed, 1.0)  # fmt: skip
        audio = noised_state((1, t, 128), [], fhw, audio_positions(t, dev), seed + 1, 1.0)
        vx, ax = _denoise(ctx, x0_model, video, audio, cond, STAGE1_SIGMAS, "stage 1",
                          seed + ANCESTRAL_SEED if anc else None)  # fmt: skip
        return {"latent": Latent(vx, ax, fhw, frames, fps)}
    latent: Latent = inputs["latent"]
    fhw, sigmas = latent.fhw, STAGE2_SIGMAS
    x0_model = X0Model(ctx.component(model, "transformer"))
    conds = _conditionings(ctx, model, cond, fhw, latent.frames, latent.fps)
    video = noised_state(latent.video.shape, conds, fhw, video_positions(*fhw, latent.fps, dev), seed + 2,
                         sigmas[0], latent.video)  # fmt: skip
    if latent.sound:  # audio to video: the source audio frozen (sigma 0, mask 0)
        audio = _audio_state(latent.audio, torch.zeros((1, latent.audio.shape[1], 1), device=dev), frozen=True)
    else:
        audio = noised_state(latent.audio.shape, [], fhw, audio_positions(latent.audio.shape[1], dev), seed + 2,
                             sigmas[0], latent.audio, scalar_blend=False)  # fmt: skip
    noise = seed + ANCESTRAL_SEED_2 if anc and not latent.sound else None
    vx, ax = _denoise(ctx, x0_model, video, audio, cond, sigmas, "stage 2", noise)
    return {"latent": replace(latent, video=vx, audio=ax)}


@torch.no_grad()
def upscale(ctx, params: dict, inputs: dict) -> dict:
    """The video latent x2 in height and width: denormalised with the VAE's statistics, upsampled, renormalised."""
    model, latent = inputs["model"], inputs["latent"]
    if latent.upscaled:
        raise RunError(f"{ctx.node}: the latent is already upscaled (one upscale per latent)")
    upsampler = ctx.component(model, "upscaler")
    stats = ctx.component(model, "vae").per_channel_statistics
    ctx.progress(1, 1, "upscaling")
    f, h, w = latent.fhw
    x = PATCH.unpatchify(latent.video[:, : f * h * w], VideoLatentShape(1, 128, f, h, w))
    x = stats.normalize(upsampler(stats.un_normalize(x)))
    return {"latent": replace(latent, video=PATCH.patchify(x), fhw=(f, h * 2, w * 2), upscaled=True)}


def _save_wav(waveform: np.ndarray, path: Path) -> None:
    """(channels, samples) float → 16-bit PCM WAV at 48 kHz."""
    pcm = np.ascontiguousarray((np.clip(waveform.T, -1.0, 1.0) * 32767).astype(np.int16))
    with wave.open(str(path), "w") as wf:
        wf.setnchannels(pcm.shape[1])
        wf.setsampwidth(2)
        wf.setframerate(SAMPLE_RATE)
        wf.writeframes(pcm.tobytes())


def _pcm(path: Path, rate: int, start: float = 0.0, seconds: float | None = None) -> np.ndarray | None:
    """utils.audio.load_audio: ffmpeg → float32 stereo PCM (channels, samples) at `rate`, or None without sound."""
    cmd = [FFMPEG, *(["-ss", str(start)] if start > 0 else []), "-i", str(path),
           *(["-t", str(seconds)] if seconds is not None else []),
           "-f", "f32le", "-acodec", "pcm_f32le", "-ac", "2", "-ar", str(rate), "-vn", "-"]  # fmt: skip
    try:
        r = subprocess.run(cmd, capture_output=True, timeout=60, check=False)
    except subprocess.TimeoutExpired:
        return None
    if r.returncode or not r.stdout:
        return None
    return np.frombuffer(r.stdout, dtype=np.float32).reshape(-1, 2).T.copy()


@torch.no_grad()
def decode(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py decode: audio (VAE decoder → BigVGAN + BWE, 48 kHz stereo; or the source part for audio to video),
    then the conv VAE's frames streamed into ffmpeg (H.264 yuv420p CRF 18 + AAC), remuxed with +faststart, frame 0
    and the last frame read back."""
    model, latent, audio = inputs["model"], inputs["latent"], params["audio"]
    vae = ctx.component(model, "vae")
    f, h, w = latent.fhw
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        tmp = Path(tmp)
        if audio and latent.sound:
            (tmp / "source").write_bytes(latent.sound[0])
            seconds = latent.frames / latent.fps
            data = _pcm(tmp / "source", SAMPLE_RATE, latent.sound[1], seconds)
            if data is None:
                raise RunError(f"{ctx.node}: the source audio could not be read again")
            _save_wav(data[:, : int(seconds * SAMPLE_RATE)], tmp / "audio.wav")
        elif audio:
            audio_vae, vocoder = ctx.component(model, "audio_vae"), ctx.component(model, "vocoder")
            ctx.progress(1, 1, "decoding audio")
            tokens = AUDIO.unpatchify(latent.audio, AudioLatentShape(1, 8, latent.audio.shape[1], 16))
            _save_wav(decode_audio(tokens, audio_vae, vocoder).waveform.cpu().numpy(), tmp / "audio.wav")
        ctx.check()
        ctx.progress(1, 1, "decoding video")
        video = PATCH.unpatchify(latent.video[:, : f * h * w], VideoLatentShape(1, 128, f, h, w))
        # build_ffmpeg_command of the vendored MLX VAE: the same arguments, so the same encode
        cmd = [FFMPEG, "-y", "-f", "rawvideo", "-vcodec", "rawvideo", "-s", f"{w * 32}x{h * 32}", "-pix_fmt", "rgb24",
               "-r", str(latent.fps), "-i", "-", *(["-i", str(tmp / "audio.wav"), "-c:a", "aac"] if audio else []),
               "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", str(tmp / "raw.mp4")]  # fmt: skip
        with open(tmp / "ffmpeg.log", "wb") as log:  # a file: a full stderr pipe would stall the frames
            proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=log)
            try:  # ponytail: an untiled decode (the RTX PRO 6000's 96 GB); the vendored temporal tiling if it refuses
                for chunk in vae.decode_video(video):  # [f, h, w, c] in [0, 1]
                    proc.stdin.write((chunk.float() * 255).to(torch.uint8).cpu().numpy().tobytes())
            finally:
                proc.stdin.close()
                if proc.wait():
                    err = (tmp / "ffmpeg.log").read_text(errors="ignore")
                    raise RunError(f"ffmpeg failed: {err.strip()[-300:]}")
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
        try:
            info = media.probe(tmp / "out.mp4")
            media.frame_png(tmp / "out.mp4", info["frames"] - 1, tmp / "last.png")
        except media.MediaError as e:
            raise RunError(f"reading back the mp4 failed: {e}") from None
        with Image.open(tmp / "last.png") as im:
            last = im.copy()
        data = (tmp / "out.mp4").read_bytes()
    clip = {"mp4": data, "poster": poster, "last": last, "frames": latent.frames, "fps": latent.fps,
            "has_audio": audio, "stream": info["stream"]}  # fmt: skip
    if latent.source_map and "offset_s" in latent.source_map:
        clip["source_map"] = latent.source_map
    return {"video": clip}


# --- retake, extend, audio to video


def load_video_frames(path: Path, height: int, width: int, frames: int, device) -> torch.Tensor:
    """mlx.py load_video_frames: the first `frames` frames as rgb24 → [-1, 1] (1, 3, F, H, W) bf16."""
    r = subprocess.run([FFMPEG, "-i", str(path), "-vframes", str(frames), "-s", f"{width}x{height}", "-pix_fmt",
                        "rgb24", "-f", "rawvideo", "-"], capture_output=True, check=False, timeout=600)  # fmt: skip
    if r.returncode:
        raise RunError(f"ffmpeg failed to read the source video: {r.stderr.decode(errors='ignore')[-300:]}")
    f = np.frombuffer(r.stdout, dtype=np.uint8).reshape(-1, height, width, 3).astype(np.float32) / 255.0 * 2.0 - 1.0
    return torch.from_numpy(f).permute(3, 0, 1, 2)[None].to(device, BF16)


def _audio_latent(ctx, model, path: Path, frames: int, fps: float, start: float = 0.0):
    """The source's sound at 16 kHz stereo from `start` for frames / fps s through the audio VAE encoder → (latent
    (1, 8, T, 16), its seconds), or None without sound."""
    pcm = _pcm(path, SOURCE_RATE, start, frames / fps)
    if pcm is None:
        return None
    sound = Audio(torch.from_numpy(pcm)[None], SOURCE_RATE)
    return encode_audio(sound, ctx.component(model, "audio_encoder")), pcm.shape[-1] / SOURCE_RATE


@torch.no_grad()
def encode_video(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py encode_video: probed, floored to 8k + 1 frames at its own size and fps, through the conv VAE encoder
    tiled with TileSizeConfig.default() (helpers.video_latent_from_file); its sound through the audio VAE encoder, or
    a zero latent without sound."""
    model, src = inputs["model"], params["source"]
    where, tid = f"{ctx.node}.source", src["take_id"]
    ctx.progress(1, 1, "encoding")
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        path = Path(tmp) / "source.mp4"
        path.write_bytes(src["data"])
        try:
            info = media.probe(path)
        except media.MediaError as e:
            raise RunError(f"{where}: take {tid} is not a readable video") from e
        if info["video"] is None:
            raise RunError(f"{where}: take {tid} is not a readable video")
        width, height, n = info["video"]["width"], info["video"]["height"], info["frames"]
        fps = float(Fraction(info["video"].get("r_frame_rate", "24/1")))
        if width % 32 or height % 32:
            raise RunError(f"{where}: take {tid} is {width} × {height}; this input needs sides on a 32 px grid")
        if n > MAX_FRAMES:
            raise RunError(f"{where}: take {tid} has {n} frames; this input takes at most {MAX_FRAMES}")
        frames = ltx_floor(n)
        latent = ctx.component(model, "vae_encoder").tiled_encode(
            load_video_frames(path, height, width, frames, ctx.device), TileSizeConfig.default()
        )
        sound = _audio_latent(ctx, model, path, frames, fps) if info["audio"] is not None else None
    t = audio_tokens(frames, fps)
    audio = sound[0] if sound else torch.zeros((1, 8, t, 16), dtype=BF16, device=ctx.device)
    return {"latent": Latent(PATCH.patchify(latent), AUDIO.patchify(audio), tuple(latent.shape[2:5]), frames, fps,
                             source_map={"take_id": tid})}  # fmt: skip


@torch.no_grad()
def encode_audio_(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py encode_audio_: the part in..out at 16 kHz stereo → the audio VAE encoder, cut to the clip's tokens."""
    model, src, fps = inputs["model"], params["source"], params["fps"]
    start, end = src.get("in", 0), src.get("out", src["duration_s"])
    m = audio_map(start, end, fps)
    if "error" in m:
        raise _failed(ctx.node, m["error"])
    frames, t = m["frames"], audio_tokens(m["frames"], fps)
    ctx.progress(1, 1, "encoding")
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        path = Path(tmp) / "source"
        path.write_bytes(src["data"])
        sound = _audio_latent(ctx, model, path, frames, fps, start)
    if sound is None:
        raise RunError(f"{ctx.node}.source: take {src['take_id']} has no sound")
    latent, seconds = sound
    if latent.shape[2] < t:
        raise RunError(f"{ctx.node}.source: out {end:.1f} is past the audio's end {start + seconds:.1f}")
    smap = {"take_id": src["take_id"]} | m["map"]
    return {"latent": Latent(None, AUDIO.patchify(latent[:, :, :t]), None, frames, float(fps), source_map=smap,
                             sound=(src["data"], start)),
            "duration": {"frames": frames, "fps": fps}}  # fmt: skip


def _audio_state(tokens: torch.Tensor, mask: torch.Tensor, frozen: bool = False) -> State:
    return State(tokens, tokens, mask, audio_positions(tokens.shape[1], tokens.device), frozen=frozen)


def _edit(ctx, params, inputs, video: State, audio: State, src: Latent, fhw, frames, smap) -> dict:
    """mlx.py _edit: the guided loop over the whole clip, noised at sigma 1 inside the masks (video seed, audio
    seed + 1)."""
    seed, cond = inputs["seed"], inputs["cond"]
    factories = _guiders(params, cond, audio_guided=True)
    x0_model = X0Model(ctx.component(inputs["model"], "transformer"))
    video, audio = noise_state(video, 1.0, seed), noise_state(audio, 1.0, seed + 1)
    sigmas = ltx2_schedule(params["steps"], fhw[0] * fhw[1] * fhw[2])
    vx, ax = _guided(ctx, x0_model, video, audio, cond, factories, sigmas, "guided")
    return {"latent": Latent(vx, ax, fhw, frames, src.fps, source_map={"take_id": src.source_map["take_id"]} | smap)}


@torch.no_grad()
def retake(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py retake: latent frames [S, E) regenerated, the rest clean context, audio over the tokens that overlap
    the span in time (common.audio_window) unless `audio` is off (then frozen)."""
    src: Latent = inputs["latent"]
    span = params["span"]
    m = retake_map(span["start"], span["end"], src.fps, src.frames)
    if "error" in m:
        raise _failed(ctx.node, m["error"])
    (s, e), (f, h, w) = m["latent"], src.fhw
    dev = src.video.device
    mask = torch.zeros((1, f * h * w, 1), device=dev)
    mask[:, s * h * w : e * h * w] = 1
    video = State(src.video, src.video, mask, video_positions(f, h, w, src.fps, dev), first_frame(mask, h * w))
    t = src.audio.shape[1]
    amask = torch.zeros((1, t, 1), device=dev)
    if params["audio"]:
        a0, a1 = audio_window(s, e, t, src.fps)
        amask[:, a0:a1] = 1
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


@torch.no_grad()
def extend(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py extend: N new latent frames of zeros after or before the clean source, audio grown the same way."""
    src: Latent = inputs["latent"]
    m = extend_map(params["seconds"], params["direction"], src.fps, src.frames)
    if "error" in m:
        raise _failed(ctx.node, m["error"])
    (f, h, w), k, after = src.fhw, m["latent"], params["direction"] == "after"
    dev, t = src.video.device, src.audio.shape[1]
    extra = max(0, audio_tokens(src.frames + 8 * k, src.fps) - t)

    def joined(old, new):  # source then new part (after), or new then source (before)
        return torch.cat([old, new] if after else [new, old], 1)

    vmask = joined(
        torch.zeros((1, src.video.shape[1], 1), device=dev),
        torch.ones((1, k * h * w, 1), device=dev),
    )
    clean = joined(src.video, torch.zeros((1, k * h * w, 128), dtype=src.video.dtype, device=dev))
    amask = joined(torch.zeros((1, t, 1), device=dev), torch.ones((1, extra, 1), device=dev))
    aclean = joined(src.audio, torch.zeros((1, extra, 128), dtype=src.audio.dtype, device=dev))
    video = State(clean, clean, vmask, video_positions(f + k, h, w, src.fps, dev), first_frame(vmask, h * w))
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


@torch.no_grad()
def sample_guided(ctx, params: dict, inputs: dict) -> dict:
    """mlx.py sample_guided (audio to video stage 1): half the canvas, pure noise with optional keyframe rows, the
    source audio frozen (mask 0, sigma 0), video guided, audio guider off; RoPE at the clip's fps."""
    model, cond, seed, a = inputs["model"], inputs["cond"], inputs["seed"], inputs["audio"]
    size, d = inputs["canvas"], inputs["duration"]
    frames, fps = d["frames"], float(d["fps"])
    fhw = (
        (frames + 7) // 8,
        size["height"] // 64 * 64 // 2 // 32,
        size["width"] // 64 * 64 // 2 // 32,
    )
    keyframe_frames(cond, frames, fps)
    factories = _guiders(params, cond, audio_guided=False)
    x0_model = X0Model(ctx.component(model, "transformer"))
    video = noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), _conditionings(ctx, model, cond, fhw, frames, fps), fhw,
                         video_positions(*fhw, fps, ctx.device), seed, 1.0)  # fmt: skip
    audio = _audio_state(a.audio, torch.zeros((1, a.audio.shape[1], 1), device=a.audio.device), frozen=True)
    sigmas = ltx2_schedule(params["steps"], fhw[0] * fhw[1] * fhw[2])
    vx, ax = _guided(ctx, x0_model, video, audio, cond, factories, sigmas, "guided")
    return {"latent": Latent(vx, ax, fhw, frames, fps, source_map=a.source_map, sound=a.sound)}


def distill(ctx, params: dict, inputs: dict) -> dict:
    """The dev variant's stage-2 model: its `distill_from` (model.yaml: dev-int8 → distilled-fp8, the dev transformer
    with the distilled LoRA fused at 1), not a bake. Pure (graph.keys builds it without a run)."""
    model = inputs["model"]
    return {"model": replace(model, variant=_distill_from(model))}


def _distill_from(model) -> str | None:
    return model.pack.spec["variants"][model.variant].get("distill_from")


# --- the checks before anything loads: common's, with each NVIDIA variant as the official one it stands for (one
# with `distill_from` is the dev model, else the distilled one); no LoRA rows on these weights


def _as_official(check):
    def run(params: dict, wired: set, models: dict | None = None) -> str | None:
        model = (models or {}).get("model")
        if model is not None:
            if model.loras:
                return NO_LORA
            dev = bool(_distill_from(model))
            models = {**models, "model": replace(model, variant="dev" if dev else "distilled")}
        return check(params, wired, models)

    return run


def _no_detail(check):
    def run(params: dict, wired: set, models: dict | None = None) -> str | None:
        return f".detail: {NO_DETAIL}" if params.get("detail") else check(params, wired, models)

    return run


check_sample = _no_detail(_as_official(_check_sample))
check_retake = check_extend = _as_official(_needs_dev)
globals()["check_sample-guided"] = _no_detail(check_retake)


def check_distill(params: dict, wired: set, models: dict | None = None) -> str | None:
    """The dev model, and its `distill_from` variant's (distilled) transformer on this host."""
    if why := check_retake(params, wired, models):
        return why
    model = (models or {}).get("model")
    if model is not None:
        source = _distill_from(model)
        ref = source and packs.ModelRef(model.pack, source, model.precision)
        if not ref or not ref.files("transformer"):
            return f": no live host for env {ENV} has the distilled LoRA"  # the contract's text (both paths)
    return None


globals().update({"encode-guided": encode_guided, "encode-video": encode_video, "encode-audio": encode_audio_,
                  "sample-guided": sample_guided, "ic-lora": ic_lora})  # fmt: skip
