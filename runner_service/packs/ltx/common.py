"""The ltx pack's runtime-neutral helpers, shared by its runtimes' modules (mlx.py; torch.py on NVIDIA): ffmpeg,
the distilled sigma tables and fixed values, the model generation's rules (image CRF, ancestral sampling, from the
transformer's `model_version`), the keyframe images' decode, H.264 round trip and frame placement, the media maps of
retake, extend and audio to video (and retake's audio window), and the checks that need no model library."""

import logging
import math
import shutil
import subprocess
from dataclasses import replace
from decimal import ROUND_HALF_UP, Decimal
from io import BytesIO
from pathlib import Path

import numpy as np
import yaml
from PIL import Image, ImageCms

import config
from core.graph import RunError

FFMPEG = shutil.which("ffmpeg")
if FFMPEG is None:  # checked when the runner loads the pack: it stops with this
    raise RuntimeError("the ltx pack needs ffmpeg on PATH to write MP4s (brew install ffmpeg)")

# ltx_pipelines_mlx.scheduler: LTX-2.5 distilled tables (8 and 3 steps; each ends at 0). Only the full tables are
# valid: the old --stageN-steps n kept table[:n + 1], which stops above sigma 0 with noise left in (note 11.6)
STAGE1_SIGMAS = [1.0, 0.99375, 0.9875, 0.98125, 0.975, 0.909375, 0.725, 0.421875, 0.0]
STAGE2_SIGMAS = [0.909375, 0.725, 0.421875, 0.0]
SIGMAS = {"stage1": STAGE1_SIGMAS, "stage2": STAGE2_SIGMAS}
# distilled.ANCESTRAL_NOISE_SEED_OFFSET / ANCESTRAL_STAGE_2_NOISE_SEED_OFFSET: each ancestral loop's noise seed is
# seed + its offset (upstream v1.4.0: both stages of a >= 2.5 distilled run are ancestral)
ANCESTRAL_SEED, ANCESTRAL_SEED_2 = 10000, 20000
MAX_TOKENS = 1024  # Gemma prompt length (left-padded; LTX2_GEMMA_MAX_LENGTH)
SAMPLE_RATE = 48000
RELAY_EPSILON, RELAY_STRENGTH = (
    1e-3,
    1.0,
)  # the old CLI's Prompt Relay defaults (fixed: contracts README, Video)
# upstream 0879826: Prompt Relay also gates audio_attn2 on the distilled paths at 1.0 (its epsilon: the video one)
RELAY_AUDIO_STRENGTH = 1.0
# plan 19 phase 4, Recipe Detail (DFR): the detailing IC-LoRA's fixed strength (upstream _DETAILING_LORA_STRENGTH) is
# the workflows' ltx/ic-lora strength; dfr_layout.SEGMENT_CANDIDATES; the slots' noise (helpers
# GENERATED_KEYFRAME_NOISE_SEED_OFFSET); above 30 fps DFR's RoPE fps snaps to 60 (dfr.conditioning_fps)
SEGMENTS = (24, 32)
SLOT_SEED = 20000
_STAGES = yaml.safe_load((Path(__file__).parent / "model.yaml").read_text())["stages"]
FRAMES = _STAGES["duration"]["params"]["frames"]
CANVAS_MAX = max(
    _STAGES["canvas"]["params"][s]["max"] for s in ("width", "height")
)  # the largest canvas side

# --- the model generation (ltx-pipelines constants.detect_params, distilled.should_use_ancestral_sampler)


def model_version(meta: dict) -> tuple[int, ...]:
    """constants.detect_model_version: the transformer header's `model_version` ("2.5.0") as numbers (a hyphen read
    as a dot, parsing stops at the first non-number); () when unset, which compares below every version."""
    out = []
    for part in str(meta.get("model_version") or "").replace("-", ".").split("."):
        if not part.isdigit():
            break
        out.append(int(part))
    return tuple(out)


def image_crf(version: tuple) -> int:
    """The H.264 CRF keyframe images are re-compressed at, as the generation was trained (LTX_2_4_PARAMS): 18 from
    2.4 on, 33 before (DEFAULT_IMAGE_CRF)."""
    return 18 if version >= (2, 4) else 33


def ancestral(version: tuple) -> bool:
    """Distilled sampling is ancestral Euler from 2.5 on (ANCESTRAL_SAMPLER_SINCE_VERSION), deterministic before."""
    return version >= (2, 5)


# --- keyframe images (media_io.load_conditioning_still, split: decode + the H.264 round trip once in `keyframes`,
# the resize and the VAE encode per sample at its grid)

_SRGB = ImageCms.createProfile("sRGB")


def decode_image(img: Image.Image) -> np.ndarray:
    """media_io.decode_image: EXIF orientation (3 / 6 / 8, a rotation) applied, alpha dropped, an embedded ICC
    profile converted to sRGB (a plain RGB conversion if it cannot be used) → RGB uint8 (H, W, 3). core.images
    already turned a JPEG upright (and dropped its tag), so it is not rotated twice."""
    rotation = {3: 180, 6: 270, 8: 90}.get(img.getexif().get(0x0112))
    if rotation:
        img = img.rotate(rotation, expand=True)
    icc = img.info.get("icc_profile")
    if img.mode == "RGBA":
        img = img.convert("RGB")
    elif img.mode == "LA":
        img = img.convert("L")
    if icc:
        try:
            img = ImageCms.profileToProfile(img, ImageCms.ImageCmsProfile(BytesIO(icc)), ImageCms.ImageCmsProfile(_SRGB),
                                            outputMode="RGB")  # fmt: skip
        except (ImageCms.PyCMSError, OSError, ValueError) as e:
            logging.getLogger(__name__).warning(
                "ltx: an image's ICC profile could not be used: %s", e
            )
            img = img.convert("RGB")
    else:
        img = img.convert("RGB")
    return np.array(img, dtype=np.uint8)


def crf_round_trip(img: Image.Image, crf: int) -> np.ndarray:
    """decode_image, then media_io.preprocess: one libx264 frame at `crf` (yuv420p, bilinear RGB <-> YUV and slice
    threads as PyAV hands libx264) and back; odd sides cropped to even, as upstream. CRF 0 or a side < 2: as decoded.
    The pixels depend on the ffmpeg/libx264 build."""
    a = decode_image(img)
    if crf == 0 or min(a.shape[:2]) < 2:
        return a
    h, w = a.shape[0] // 2 * 2, a.shape[1] // 2 * 2
    a = np.ascontiguousarray(a[:h, :w])
    enc = subprocess.run([FFMPEG, "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", f"{w}x{h}", "-r",
                          "1", "-i", "pipe:0", "-sws_flags", "bilinear", "-pix_fmt", "yuv420p", "-c:v", "libx264",
                          "-preset", "veryfast", "-crf", str(int(crf)), "-thread_type", "slice", "-frames:v", "1",
                          "-f", "mp4", "-movflags", "frag_keyframe+empty_moov", "pipe:1"],
                         input=a.tobytes(), capture_output=True, check=False, timeout=60)  # fmt: skip
    dec = subprocess.run([FFMPEG, "-v", "error", "-i", "pipe:0", "-frames:v", "1", "-sws_flags", "bilinear",
                          "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1"],
                         input=enc.stdout, capture_output=True, check=False, timeout=60)  # fmt: skip
    if enc.returncode or dec.returncode or len(dec.stdout) != h * w * 3:
        raise RunError(f"ffmpeg failed on a keyframe image: {(enc.stderr or dec.stderr).decode(errors='ignore')[-300:]}")  # fmt: skip
    return np.frombuffer(dec.stdout, np.uint8).reshape(h, w, 3).copy()


def audio_window(start: int, end: int, tokens: int, fps: float) -> tuple[int, int]:
    """retake.audio_window_tokens (upstream TemporalRegionMask): the audio tokens [a0, a1) whose time overlaps latent
    frames [start, end) (latent frame k covers pixel frames [max(0, 8k - 7), 8k + 1); audio token i mel frames
    [max(0, 4i - 3), 4i + 1) of 10 ms); (0, 0) when none does."""
    t0, t1 = max(0, 8 * start - 7) / fps, max(0, 8 * end - 7) / fps
    inside = [
        i for i in range(tokens) if max(0, 4 * i + 1) * 0.01 > t0 and max(0, 4 * i - 3) * 0.01 < t1
    ]
    return (inside[0], inside[-1] + 1) if inside else (0, 0)


def cover(rgb: np.ndarray, side: int) -> np.ndarray:
    """A round-tripped image no larger than it needs to cover any canvas of at most `side` x `side` (its short side
    at `side`, LANCZOS), so a cached Cond does not hold a 50 MP frame; one at most that size is returned as it is."""
    h, w, _ = rgb.shape
    if min(h, w) <= side:
        return rgb
    scale = side / min(h, w)
    size = (max(side, math.ceil(w * scale)), max(side, math.ceil(h * scale)))
    return np.asarray(Image.fromarray(rgb, mode="RGB").resize(size, Image.LANCZOS))


def keyframe_frames(cond, frames: int, fps: float) -> list[int]:
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


def dfr_canvas(frames: int) -> tuple[int, list[int]]:
    """dfr_layout.resolve_canvas: the clip (8k + 1 frames) padded to whole keyframe segments (24 or 32 frames: the
    least padding, the longer on a tie) and one generated keyframe slot per segment boundary → (canvas frames, the
    slots' pixel frames)."""
    content = frames - 1
    segment = min(SEGMENTS, key=lambda s: ((-content) % s, -s))
    padded = content + (-content) % segment
    return padded + 1, list(range(segment, padded + 1, segment))


def dfr_fps(fps: float) -> float:
    """dfr.conditioning_fps: the transformer's RoPE fps on the detail path (above 30 it snaps to 60)."""
    return 60.0 if fps > 30 else fps


def ic_lora(ctx, params: dict, inputs: dict) -> dict:
    """The Model with the variant's IC-LoRA (component ic_lora) as one adapter use at `strength`: core's adapter
    path attaches it to the transformer when a stage loads it (its file read in place, never through the LoRA
    library). Pure (graph.keys builds it without a run, ctx None); a missing file fails the run here."""
    from core import models
    from core.pack import LoraUse

    model = inputs["model"]
    found = model.files("ic_lora")
    if not found:
        if ctx is not None:  # studio's run check refuses this first (API 1.36, Optional add-ons)
            title = model.pack.component("ic_lora").get("addon", {}).get("title", "ic_lora")
            raise RunError(f"{ctx.node}: {title} not downloaded on host {config.HOST}")
        return {"model": model}
    f = found[0]
    use = LoraUse(f.stem, "component", f.stem, models.hashes.compute(f), f.stat().st_size,
                  float(params["strength"]), "adapter", path=str(f))  # fmt: skip
    return {"model": replace(model, loras=(*model.loras, use))}


def check_sample(params: dict, wired: set, models: dict | None = None) -> str | None:
    """Before anything runs: the role fixes which inputs the node takes (contracts/README.md, Video), the steps
    (its whole distilled table) and the model (distilled; stage 2 also the dev model with the distilled LoRA baked,
    ltx/distill, except for the detail pass, which is DFR's: distilled only)."""
    role, model = params.get("role"), (models or {}).get("model")
    if model is not None and params.get("detail") and model.variant != "distilled":
        return ".detail: needs model variant distilled"
    if (
        model is not None
        and model.variant != "distilled"
        and not (role == "stage2" and distilled_lora(model))
    ):
        return ": needs model variant distilled"
    if role == "stage1" and ("latent" in wired or not {"canvas", "duration"} <= wired):
        return ": stage 1 needs canvas and duration, not latent"
    if role == "stage2" and ("latent" not in wired or wired & {"canvas", "duration"}):
        return ": stage 2 needs latent, not canvas or duration"
    if role in SIGMAS and params.get("steps") != (n := len(SIGMAS[role]) - 1):
        return f".steps: must be one of {n}"
    return None


def _ffmpeg(*args: str) -> None:
    r = subprocess.run(
        [FFMPEG, "-v", "error", "-y", *args], capture_output=True, text=True, check=False
    )
    if r.returncode:
        raise RunError(f"ffmpeg failed: {r.stderr.strip()[-300:]}")


# utils/constants.DEFAULT_NEGATIVE_PROMPT: fixed, not user editable (a change re-encodes: it is in the pack's code hash)
NEGATIVE_PROMPT = (
    "blurry, out of focus, overexposed, underexposed, low contrast, washed out colors, excessive noise, "
    "grainy texture, poor lighting, flickering, motion blur, distorted proportions, unnatural skin tones, "
    "deformed facial features, asymmetrical face, missing facial features, extra limbs, disfigured hands, "
    "wrong hand count, artifacts around text, inconsistent perspective, camera shake, incorrect depth of "
    "field, background too sharp, background clutter, distracting reflections, harsh shadows, inconsistent "
    "lighting direction, color banding, cartoonish rendering, 3D CGI look, unrealistic materials, uncanny "
    "valley effect, incorrect ethnicity, wrong gender, exaggerated expressions, wrong gaze direction, "
    "mismatched lip sync, silent or muted audio, distorted voice, robotic voice, echo, background noise, "
    "off-sync audio, incorrect dialogue, added dialogue, repetitive speech, jittery movement, awkward "
    "pauses, incorrect timing, unnatural transitions, inconsistent framing, tilted camera, flat lighting, "
    "inconsistent tone, cinematic oversaturation, stylized filters, or AI artifacts."
)
MAX_FRAMES = (
    321  # ltx/duration's max: the longest clip these make (contracts check.py LTX_MAX_FRAMES)
)
SOURCE_RATE = 16000  # the audio VAE encoder's input rate


def rnd(x: float) -> int:
    """Half away from zero, exactly (contracts check.py rnd: Go math.Round)."""
    return int(Decimal(x).quantize(Decimal(1), rounding=ROUND_HALF_UP))


def ltx_floor(n: int) -> int:
    """A source's frames floored to 8k + 1 (k >= 1), as the old studio reads it."""
    return 8 * max(1, (n - 1) // 8) + 1


def retake_map(start: float, end: float, fps: float, frames: int) -> dict:
    """contracts check.py retake_map: the latent span [S, E) on whole frames (half up: the old UI's Math.round), the
    output's frames and its source_map; or {"error": the run's text}."""
    n, fs, fe = ltx_floor(frames), rnd(start * fps), rnd(end * fps)
    s, last = (fs + 4) // 8, (n - 1) // 8 + 1
    e = last if fe >= n else min(max((fe + 4) // 8, s + 1), last)
    if s >= e:
        return {"error": "retake.span: nothing to regenerate on the 8-frame grid"}
    return {"latent": [s, e], "frames": n,
            "map": {"offset_s": 0, "new_in_s": max(8 * s - 7, 0) / fps, "new_out_s": (8 * (e - 1) + 1) / fps}}  # fmt: skip


def extend_map(seconds: float, direction: str, fps: float, frames: int) -> dict:
    """contracts check.py extend_map: N = max(1, ceil(seconds * fps / 8)) latent frames after or before."""
    n = ltx_floor(frames)
    k = max(1, math.ceil(seconds * fps / 8 - 1e-9))
    total, add = n + 8 * k, 8 * k / fps
    if total > MAX_FRAMES:
        return {"error": f"extend.seconds: the clip would be {total} frames; at most {MAX_FRAMES}"}
    m = {"offset_s": 0, "new_in_s": n / fps, "new_out_s": total / fps} if direction == "after" else \
        {"offset_s": add, "new_in_s": 0, "new_out_s": add}  # fmt: skip
    return {"latent": k, "frames": total, "map": m}


def audio_map(in_s: float, out_s: float, fps: float) -> dict:
    """contracts check.py audio_map: the clip's frames = the part at fps floored to 8k + 1 (at most 321)."""
    f = min(MAX_FRAMES, (math.floor((out_s - in_s) * fps + 1e-6) - 1) // 8 * 8 + 1)
    if f < 9:
        return {"error": "audio.source: the part from in to out is shorter than 9 frames"}
    return {"frames": f, "map": {"offset_s": -in_s, "new_in_s": 0, "new_out_s": f / fps}}


def _failed(node: str, text: str) -> RunError:
    """A map's error names its node the way the contract's fixture does (`retake.span: …`): with this node's id."""
    return RunError(f"{node}.{text.split('.', 1)[1]}")


def distilled_lora(model) -> bool:
    """Whether the handle is the dev model with the pack's pre-fused distilled LoRA baked (ltx/distill)."""
    names = {Path(x["file"]).stem for x in model.pack.spec.get("lora", {}).get("prefused", []) if x["variant"] == "distilled"}  # fmt: skip
    return model.variant == "dev" and any(
        u.name in names and u.strength == 1 and u.mode == "bake" for u in model.loras
    )


def lora_block_prefused(pack) -> list[dict]:
    from core import pack as packs

    return (packs.lora_block(pack) or {}).get("prefused", [])


# extend_distilled.EXTEND_CARRY_PIXEL_FRAMES (upstream ChunkConfig.next_video_carry_frames): a distilled extend
# continues the source's last 25 frames (4 latent frames), pinned at the start of its new window
EXTEND_CARRY = 25


def is_distilled(model) -> bool:
    """A distilled transformer (the distilled LoRA fused in): retake, extend and audio to video run upstream's
    distilled paths on it (plan 19 23.4, phase 1). The dev variants' ids start with dev (model.yaml)."""
    return model is not None and not model.variant.startswith("dev")


def _needs_dev(params: dict, wired: set, models: dict | None = None) -> str | None:
    """Retake, extend, the guided stage 1 and distill run the dev transformer (the run's check, before loading)."""
    model = (models or {}).get("model")
    return ": needs model variant dev" if model is not None and model.variant != "dev" else None
