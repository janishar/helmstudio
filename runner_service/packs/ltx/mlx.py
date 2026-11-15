"""Stage functions of the ltx pack (runtime mlx): LTX-2.5 distilled two-stage text-to-video with audio, a port of
ltx-2-studio's `DistilledPipeline.generate_two_stage` + `decode_and_save_video` (ltx-pipelines-mlx 0.15.4) over its
model library ltx-core-mlx, vendored in vendor/ (MIT, VENDORED.md). The order of every noise draw, the seeds, the
sigma tables and the blends are the old code's, so the same mlx version gives the same bytes
(docs/reference-notes/ltx.md sections 4 and 6; GOTCHAS.md).

Handles: Conditioning is `Cond` (video [1, 1024, 4096] and audio [1, 1024, 2048] prompt embeddings); Latent is
`Latent` (video tokens at (F, H, W) latent size and audio tokens, together). A Video value is the encoded clip
{mp4: bytes, poster: PIL image (frame 0), frames, fps, has_audio}, which the core `output` node stores."""

import functools
import itertools
import json
import shutil
import subprocess
import sys
import tempfile
import wave
from dataclasses import dataclass, replace
from pathlib import Path

import mlx.core as mx
from PIL import Image

from core.graph import RunError

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
from ltx_core_mlx.conditioning.types.latent_cond import (
    LatentState,
    apply_denoise_mask,
    noise_latent_state,
)
from ltx_core_mlx.loader import official_pack as op
from ltx_core_mlx.model.audio_vae.audio_vae import AudioVAEDecoder
from ltx_core_mlx.model.audio_vae.bwe import VocoderWithBWE
from ltx_core_mlx.model.transformer.model import LTXModel, LTXModelConfig, X0Model
from ltx_core_mlx.model.upsampler.model import LatentUpsampler
from ltx_core_mlx.model.video_vae.video_vae import VideoDecoder
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

# ltx_pipelines_mlx.scheduler: LTX-2.5 distilled tables (8 and 3 steps; each ends at 0)
STAGE1_SIGMAS = [1.0, 0.99375, 0.9875, 0.98125, 0.975, 0.909375, 0.725, 0.421875, 0.0]
STAGE2_SIGMAS = [0.909375, 0.725, 0.421875, 0.0]
ANCESTRAL_SEED = (
    10000  # distilled.ANCESTRAL_NOISE_SEED_OFFSET: the stage-1 loop's noise seed is seed + this
)
MAX_TOKENS = 1024  # Gemma prompt length (left-padded; LTX2_GEMMA_MAX_LENGTH)
SAMPLE_RATE = 48000
PATCH = VideoLatentPatchifier()


@dataclass(frozen=True)
class Cond:
    video: mx.array  # [1, 1024, 4096]
    audio: mx.array  # [1, 1024, 2048]


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


# --- stages


def canvas(ctx, params: dict, inputs: dict) -> dict:
    """width x height floored to the 64 px grid (two-stage: stage 1 runs at half)."""
    return {"canvas": {"width": params["width"] // 64 * 64, "height": params["height"] // 64 * 64}}


def duration(ctx, params: dict, inputs: dict) -> dict:
    return {"duration": {"frames": params["frames"], "fps": params["fps"]}}


def encode(ctx, params: dict, inputs: dict) -> dict:
    """Gemma 4 (all 49 hidden states, the last one post-norm) → the connector → video and audio embeddings."""
    model, text = inputs["model"], inputs["prompt"]
    if not text.strip():
        raise RunError(f"{ctx.node}: the prompt is empty")
    tower = ctx.component(model, "text_encoder")
    extractor = ctx.component(model, "connector")
    ctx.check()
    ctx.progress(1, 1, "encoding")
    f = model.files("text_encoder")[0]
    encoder = Gemma4TextEncoder(
        tower=tower, tokenizer=_tokenizer(f, f.stat().st_size, f.stat().st_mtime_ns)
    )
    hidden, mask = encoder.encode_all_layers(text, max_length=MAX_TOKENS)
    video, audio = extractor(hidden, attention_mask=mask)
    mx.eval(video, audio)
    return {"cond": Cond(video, audio)}


def check_sample(params: dict, wired: set) -> str | None:
    """Before anything runs: the role fixes which inputs the node takes (contracts/README.md, Video)."""
    if params.get("role") == "stage1" and (
        "latent" in wired or not {"canvas", "duration"} <= wired
    ):
        return ": stage 1 needs canvas and duration, not latent"
    if params.get("role") == "stage2":
        if "latent" not in wired or wired & {"canvas", "duration"}:
            return ": stage 2 needs latent, not canvas or duration"
        if params.get("steps", 0) > len(STAGE2_SIGMAS) - 1:
            return ".steps: stage 2 has 3 steps at most"
    return None


def _state(shape, fhw, positions, seed: int, sigma: float, initial=None, scalar_blend: bool = True):
    """helpers.create_noised_state without conditionings. scalar_blend (`legacy_scalar_blend`): noise * sigma +
    clean * (1 - sigma) with a Python sigma; else noise_latent_state's mask * sigma blend (stage-2 audio)."""
    latent = mx.zeros(shape, dtype=mx.bfloat16) if initial is None else initial
    mask = mx.ones((shape[0], shape[1], 1), dtype=mx.bfloat16)
    keyframes = (
        first_frame_keyframes_mask(mask, fhw[1] * fhw[2]) if positions.shape[-1] == 3 else None
    )
    state = LatentState(latent=latent, clean_latent=latent, denoise_mask=mask, positions=positions,
                        keyframes_mask=keyframes)  # fmt: skip
    if not scalar_blend:
        return noise_latent_state(state, sigma=sigma, seed=seed)
    mx.random.seed(seed)
    noise = mx.random.normal(state.clean_latent.shape).astype(mx.bfloat16)
    return replace(state, latent=noise * sigma + state.clean_latent * (1.0 - sigma))


def _denoise(ctx, x0_model, video: LatentState, audio: LatentState, cond: Cond, sigmas, phase: str,
             noise_seed: int | None):  # fmt: skip
    """samplers.denoise_loop (noise_seed None: deterministic Euler) or euler_ancestral_denoising_loop (eta 1,
    s_noise 1, the loop's noise drawn video then audio from mx.random.seed(noise_seed)), with a cancel point and a
    progress event per step. Uniform denoise masks only (no conditioning in text-to-video)."""
    mx.eval(video.latent, video.clean_latent, audio.latent)  # _pre_denoise_flush: the GPU watchdog
    vx, ax = video.latent, audio.latent
    vdtype, adtype = vx.dtype, ax.dtype
    stepper = EulerAncestralDiffusionStep(eta=1.0, s_noise=1.0)
    if noise_seed is not None:
        mx.random.seed(noise_seed)
    steps = list(itertools.pairwise(sigmas))
    for i, (sigma, sigma_next) in enumerate(steps):
        ctx.check()
        sig = mx.broadcast_to(mx.array([sigma], dtype=mx.bfloat16), (vx.shape[0],))
        vx0, ax0 = x0_model(
            video_latent=vx, audio_latent=ax, sigma=sig, video_text_embeds=cond.video,
            audio_text_embeds=cond.audio, video_positions=video.positions, audio_positions=audio.positions,
            video_attention_mask=video.attention_mask, video_keyframes_mask=video.keyframes_mask,
            audio_attention_mask=audio.attention_mask,
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
    sigma 0.909375 with seed + 2 (video and audio alike), deterministic Euler. Audio is denoised in both."""
    model, cond, seed = inputs["model"], inputs["cond"], inputs["seed"]
    x0_model = X0Model(ctx.component(model, "transformer"))
    if params["role"] == "stage1":
        size, d = inputs["canvas"], inputs["duration"]
        frames, fps = d["frames"], float(d["fps"])
        height, width = snap_output_dimensions(
            size["height"], size["width"], two_stage=True, warn=False
        )
        fhw = compute_video_latent_shape(frames, height // 2, width // 2)
        audio_t = compute_audio_token_count(frames, frame_rate=fps)
        video = _state((1, fhw[0] * fhw[1] * fhw[2], 128), fhw, compute_video_positions(*fhw, frame_rate=fps), seed,
                       1.0)  # fmt: skip
        audio = _state((1, audio_t, 128), fhw, compute_audio_positions(audio_t), seed + 1, 1.0)
        sigmas = STAGE1_SIGMAS[: params["steps"] + 1]
        vx, ax = _denoise(
            ctx, x0_model, video, audio, cond, sigmas, "stage 1", seed + ANCESTRAL_SEED
        )
        return {"latent": Latent(vx, ax, fhw, frames, fps)}
    latent: Latent = inputs["latent"]
    fhw, sigmas = latent.fhw, STAGE2_SIGMAS[: params["steps"] + 1]
    positions = compute_video_positions(*fhw, frame_rate=latent.fps)
    video = _state(latent.video.shape, fhw, positions, seed + 2, sigmas[0], latent.video)
    audio = _state(latent.audio.shape, fhw, compute_audio_positions(latent.audio.shape[1]), seed + 2, sigmas[0],
                   latent.audio, scalar_blend=False)  # fmt: skip
    vx, ax = _denoise(ctx, x0_model, video, audio, cond, sigmas, "stage 2", None)
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
    CRF 18 + AAC), then remuxed with +faststart (a container change only) and frame 0 read back as the poster."""
    model, latent = inputs["model"], inputs["latent"]
    vae, audio_vae, vocoder = (ctx.component(model, c) for c in ("vae", "audio_vae", "vocoder"))
    f, h, w = latent.fhw
    ctx.progress(1, 1, "decoding audio")
    waveform = vocoder(audio_vae.decode(AudioPatchifier().unpatchify(latent.audio)))
    mx.eval(waveform)
    with tempfile.TemporaryDirectory(prefix="ltx-") as tmp:
        tmp = Path(tmp)
        _save_wav(waveform, tmp / "audio.wav")
        del waveform
        mx.clear_cache()
        ctx.check()
        ctx.progress(1, 1, "decoding video")
        video = PATCH.unpatchify(latent.video[:, : f * h * w, :], latent.fhw)
        vae.decode_and_stream(
            video, str(tmp / "raw.mp4"), frame_rate=latent.fps, audio_path=str(tmp / "audio.wav")
        )
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
        "has_audio": True,
    }
    return {"video": clip}
