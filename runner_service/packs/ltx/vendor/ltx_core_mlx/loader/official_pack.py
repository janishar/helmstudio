"""Run official Lightricks LTX-2.5 checkpoints without a pre-converted pack.

The rest of this package loads mlx-forge packs: split, renamed, channels-last
safetensors files next to a few JSON sidecars. This module lets a directory of
the *official* upstream files (as downloaded from ``Lightricks/LTX-2.5``) stand
in for such a pack, converting weights in memory at load time.

How it fits in without touching the loaders:

* :func:`resolve_official_model_dir` recognises an official layout and builds a
  *virtual pack* directory under ``<repo>/.cache/virtual-packs/``. It holds only
  the small files the runtime reads from disk (``embedded_config.json``,
  ``text_encoder_config.json``, tokenizer assets, upscaler configs,
  ``quantize_config.json``) plus one placeholder per pack weight file.
* A placeholder is a valid, header-only safetensors file: every converted key
  name is listed as a zero-length tensor, and ``__metadata__`` records the
  official source file and component. Existence checks, globbing and
  header-key validation throughout the pipelines therefore see a normal pack.
* :func:`load_split_safetensors` hands placeholders to :func:`load_virtual`,
  which mmaps the official file, renames keys, transposes conv weights and
  (optionally) quantizes, exactly as ``mlx-forge convert ltx-2.5`` would.

Nothing converted is written to disk. The key-renaming, transposition and
quantization rules below are vendored from mlx-forge
(https://github.com/dgrauet/mlx-forge, Apache-2.0: ``recipes/ltx_25.py``,
``recipes/ltx_25_text_encoder.py``, ``transpose.py``, ``quantize.py``) so the
result matches a converted pack key for key.
"""

from __future__ import annotations

import functools
import gc
import hashlib
import json
import math
import os
import shutil
import struct
import sys
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import mlx.core as mx

# Bump when the conversion rules change, so stale virtual packs are rebuilt.
VIRTUAL_PACK_FORMAT = 2

VIRTUAL_METADATA_KEY = "ltx_mlx_virtual_source"
QUANTIZE_ENV = "LTX_MLX_QUANTIZE_ON_LOAD"
CACHE_ENV = "LTX_MLX_CACHE_DIR"
DEFAULT_QUANTIZE_BITS = 8
GROUP_SIZE = 64

_materialize = getattr(mx, "eval")  # noqa: B009 -- mx.eval is the MLX graph materialiser

# ---------------------------------------------------------------------------
# Official files, located by name anywhere under the model directory
# ---------------------------------------------------------------------------

OFFICIAL_FILENAMES: dict[str, str] = {
    "transformer_distilled": "ltx-2.5-22b-distilled-transformer-bf16.safetensors",
    "transformer_dev": "ltx-2.5-22b-dev-transformer-bf16.safetensors",
    "text_encoder": "gemma4-12b-with-proj-ltx-2.5-bf16.safetensors",
    "video_vae_conv": "ltx-2.5-video-vae-conv-bf16.safetensors",
    "video_vae_av": "ltx-2.5-video-vae-bf16.safetensors",
    "audio_vae": "ltx-2.5-audio-vae-bf16.safetensors",
    "spatial_upscaler": "ltx-2.5-latent-spatial-upscaler-x2-bf16-1.0.safetensors",
    "temporal_upscaler": "ltx-2.5-latent-temporal-upscaler-x2-bf16-1.0.safetensors",
    "duration_head": "ltx-2.5-duration-head-bf16.safetensors",
    # Stage 2 of the dev pipelines fuses it; linked into the pack as-is (no conversion).
    "distilled_lora": "ltx-2.5-22b-distilled-lora-450-bf16.safetensors",
}

_REQUIRED_ROLES = ("text_encoder", "video_vae_conv", "audio_vae")

#: U8 tensor key -> the sidecar file it becomes (mlx-forge ``ASSET_FILENAMES``).
TEXT_ENCODER_ASSETS: dict[str, str] = {
    "tokenizer_json": "tokenizer.json",
    "hf_asset__tokenizer_config.json": "tokenizer_config.json",
    "hf_asset__chat_template.jinja": "chat_template.jinja",
    "hf_asset__generation_config.json": "generation_config.json",
    "hf_asset__processor_config.json": "processor_config.json",
}


@dataclass(frozen=True)
class OfficialSources:
    """Resolved paths of the official files found under a model directory."""

    root: Path
    files: dict[str, Path]

    def get(self, role: str) -> Path | None:
        return self.files.get(role)


# ---------------------------------------------------------------------------
# Safetensors header helpers
# ---------------------------------------------------------------------------


@functools.lru_cache(maxsize=64)
def _read_header_cached(path: str, size: int, mtime_ns: int) -> tuple[dict, dict]:
    with open(path, "rb") as f:
        n = struct.unpack("<Q", f.read(8))[0]
        # A corrupt or non-safetensors file can claim a header bigger than itself, and
        # reading it blindly asks for that much memory: eight junk bytes decode to a
        # length near 2**63, which raises MemoryError rather than the ValueError callers
        # like virtual_source_info expect.
        if n > max(size - 8, 0):
            raise ValueError(f"{path}: safetensors header length {n} exceeds the file ({size} bytes)")
        header = json.loads(f.read(n))
    metadata = header.pop("__metadata__", None) or {}
    return header, metadata


def read_safetensors_header(path: str | Path) -> tuple[dict, dict]:
    """Return ``(tensors, metadata)`` from a safetensors header without reading tensor data."""
    st = os.stat(path)
    return _read_header_cached(str(path), st.st_size, st.st_mtime_ns)


def _header_is_complete(path: Path) -> bool:
    with open(path, "rb") as f:
        n = struct.unpack("<Q", f.read(8))[0]
    tensors, _ = read_safetensors_header(path)
    end = max((v["data_offsets"][1] for v in tensors.values()), default=0)
    return 8 + n + end == path.stat().st_size


# ---------------------------------------------------------------------------
# Conversion rules (vendored from mlx-forge recipes/ltx_25.py)
# ---------------------------------------------------------------------------

_CONNECTOR_STACKS = ("video_embeddings_connector.", "audio_embeddings_connector.")


def classify_dit_key(key: str) -> str | None:
    if not key.startswith("model.diffusion_model."):
        return None
    suffix = key[len("model.diffusion_model.") :]
    if suffix.startswith(_CONNECTOR_STACKS):
        return "connector"
    return "transformer"


def sanitize_transformer_key(key: str) -> str | None:
    k = key.replace("model.diffusion_model.", "")
    k = k.replace(".to_out.0.", ".to_out.")
    k = k.replace(".ff.net.0.proj.", ".ff.proj_in.")
    k = k.replace(".ff.net.2.", ".ff.proj_out.")
    k = k.replace(".audio_ff.net.0.proj.", ".audio_ff.proj_in.")
    k = k.replace(".audio_ff.net.2.", ".audio_ff.proj_out.")
    k = k.replace(".linear_1.", ".linear1.")
    k = k.replace(".linear_2.", ".linear2.")
    return k


def sanitize_connector_key(key: str) -> str | None:
    return key.replace("model.diffusion_model.", "")


def classify_vae_encoder_key(key: str, suffix: str) -> str | None:
    """Route an encoder key to ``vae_encoder{suffix}`` (mlx-forge ``classify_video_vae_key``).

    ``suffix`` is ``"_conv"`` or ``"_av"`` and comes from the pack file entry: the key
    alone cannot say which of the two official video VAE files it came from, and neither
    component is ever named plainly.

    mlx-forge routes per_channel_statistics to the decoder and then copies them into the
    encoder (``_share_video_vae_statistics``). Here each component carries its own rule,
    so both classifiers claim the statistics directly -- same result, no second pass.
    """
    if key.startswith("encoder.") or key.startswith("per_channel_statistics."):
        return f"vae_encoder{suffix}"
    return None


def classify_vae_decoder_key(key: str, suffix: str) -> str | None:
    """Route a decoder key to ``vae_decoder{suffix}``; see :func:`classify_vae_encoder_key`."""
    if key.startswith("decoder.") or key.startswith("per_channel_statistics."):
        return f"vae_decoder{suffix}"
    return None


def sanitize_vae_decoder_key(key: str) -> str | None:
    if key.startswith("per_channel_statistics."):
        if "mean-of-means" in key:
            return "per_channel_statistics.mean"
        if "std-of-means" in key:
            return "per_channel_statistics.std"
        return None
    if key.startswith("decoder."):
        return key[len("decoder.") :]
    return None


def sanitize_vae_encoder_key(key: str) -> str | None:
    if key.startswith("per_channel_statistics."):
        if "mean-of-means" in key:
            return "per_channel_statistics._mean_of_means"
        if "std-of-means" in key:
            return "per_channel_statistics._std_of_means"
        return None
    if key.startswith("encoder."):
        return key[len("encoder.") :]
    return None


def classify_audio_key(key: str) -> str | None:
    if key.startswith("audio_vae."):
        return "audio_vae"
    if key.startswith("vocoder."):
        return "vocoder"
    return None


def sanitize_audio_vae_key(key: str) -> str | None:
    if not key.startswith("audio_vae."):
        return None
    suffix = key[len("audio_vae.") :]
    if suffix.startswith("per_channel_statistics."):
        if "mean-of-means" in suffix:
            return "per_channel_statistics._mean_of_means"
        if "std-of-means" in suffix:
            return "per_channel_statistics._std_of_means"
        return None
    if suffix.startswith("decoder.") or suffix.startswith("encoder."):
        return suffix
    return None


def sanitize_vocoder_key(key: str) -> str | None:
    if not key.startswith("vocoder."):
        return None
    k = key[len("vocoder.") :]
    if k.startswith("vocoder."):
        k = k[len("vocoder.") :]
    return k


def sanitize_duration_head_key(key: str) -> str | None:
    if key.startswith("duration_head."):
        return key[len("duration_head.") :]
    return None


def sanitize_text_encoder_key(key: str) -> str | None:
    if key in TEXT_ENCODER_ASSETS:
        return None
    return key


def transpose_conv(weight: mx.array, *, is_conv_transpose: bool = False) -> mx.array:
    """PyTorch conv layout -> MLX channels-last (mlx-forge ``transpose.py``)."""
    if weight.ndim == 5:
        return mx.transpose(weight, (0, 2, 3, 4, 1))
    if weight.ndim == 4:
        return mx.transpose(weight, (0, 2, 3, 1))
    if weight.ndim == 3:
        if is_conv_transpose:
            return mx.transpose(weight, (1, 2, 0))
        return mx.transpose(weight, (0, 2, 1))
    return weight


def _is_conv_buffer(key: str, ndim: int) -> bool:
    if ndim < 3:
        return False
    suffix = key.rsplit(".", 1)[-1]
    return suffix == "filter" or suffix.endswith("_basis")


def maybe_transpose(key: str, value: mx.array, component: str) -> mx.array:
    if component in ("transformer", "connector", "text_encoder"):
        return value
    if _is_conv_buffer(key, value.ndim):
        return transpose_conv(value)
    is_conv = ("conv" in key.lower() or (component == "vocoder" and "ups" in key)) and "weight" in key
    if not is_conv:
        return value
    return transpose_conv(value, is_conv_transpose=component == "vocoder" and "ups" in key)


def transpose_upscaler_weight(key: str, value: mx.array, component: str) -> mx.array:
    if value.ndim >= 3 and key.endswith(".weight"):
        return transpose_conv(value)
    return value


def ltx25_should_quantize(key: str, ndim: int) -> bool:
    """Only transformer_blocks Linear weights (mlx-forge ``ltx25_should_quantize``)."""
    bare_key = key.replace("transformer.", "", 1)
    return (
        "transformer_blocks" in bare_key
        and bare_key.endswith(".weight")
        and ndim == 2
        and not bare_key.endswith(".scales")
        and not bare_key.endswith(".biases")
    )


_GEMMA_UNQUANTISED_PREFIXES = (
    "vision_model.",
    "text_embedding_projection.",
    "audio_projector.",
    "multi_modal_projector.",
)

_GEMMA_QUANTISED_SUFFIXES = (
    ".self_attn.q_proj.weight",
    ".self_attn.k_proj.weight",
    ".self_attn.v_proj.weight",
    ".self_attn.o_proj.weight",
    ".mlp.gate_proj.weight",
    ".mlp.up_proj.weight",
    ".mlp.down_proj.weight",
)


def should_quantize_gemma(key: str, ndim: int) -> bool:
    """Gemma-4 attention/MLP Linear weights only (mlx-forge ``should_quantize_gemma``)."""
    bare_key = key.replace("text_encoder.", "", 1)
    if bare_key.startswith(_GEMMA_UNQUANTISED_PREFIXES):
        return False
    if bare_key.endswith((".scales", ".biases")):
        return False
    if ndim != 2:
        return False
    return bare_key.endswith(_GEMMA_QUANTISED_SUFFIXES)


def _identity(key: str) -> str | None:
    return key


@dataclass(frozen=True)
class ComponentRule:
    """How one pack component is carved out of an official file."""

    source_role: str
    classify: Callable[[str], str | None]
    sanitize: Callable[[str], str | None]
    transform: Callable[[str, mx.array, str], mx.array]
    should_quantize: Callable[[str, int], bool] | None = None


def _no_transform(key: str, value: mx.array, component: str) -> mx.array:
    return value


COMPONENT_RULES: dict[str, ComponentRule] = {
    "transformer": ComponentRule(
        "transformer", classify_dit_key, sanitize_transformer_key, _no_transform, ltx25_should_quantize
    ),
    "connector": ComponentRule("transformer", classify_dit_key, sanitize_connector_key, _no_transform),
    "text_encoder": ComponentRule(
        "text_encoder",
        lambda key: None if key in TEXT_ENCODER_ASSETS else "text_encoder",
        sanitize_text_encoder_key,
        _no_transform,
        should_quantize_gemma,
    ),
    "vae_encoder_conv": ComponentRule(
        "video_vae_conv",
        functools.partial(classify_vae_encoder_key, suffix="_conv"),
        sanitize_vae_encoder_key,
        maybe_transpose,
    ),
    "vae_decoder_conv": ComponentRule(
        "video_vae_conv",
        functools.partial(classify_vae_decoder_key, suffix="_conv"),
        sanitize_vae_decoder_key,
        maybe_transpose,
    ),
    # The AV file's two halves: a conv encoder and the NADiffusionDecoder. Same
    # sanitizers as the conv pair (mlx-forge reuses them too); never quantized, like
    # every other VAE -- load_diffusion_decoder loads strictly into an unquantized
    # module and then casts to bf16. maybe_transpose is a no-op on the decoder half,
    # whose tensors are all rank <= 2, and does the real work on the encoder's convs.
    "vae_encoder_av": ComponentRule(
        "video_vae_av",
        functools.partial(classify_vae_encoder_key, suffix="_av"),
        sanitize_vae_encoder_key,
        maybe_transpose,
    ),
    "vae_decoder_av": ComponentRule(
        "video_vae_av",
        functools.partial(classify_vae_decoder_key, suffix="_av"),
        sanitize_vae_decoder_key,
        maybe_transpose,
    ),
    "audio_vae": ComponentRule("audio_vae", classify_audio_key, sanitize_audio_vae_key, maybe_transpose),
    "vocoder": ComponentRule("audio_vae", classify_audio_key, sanitize_vocoder_key, maybe_transpose),
    "duration_head": ComponentRule(
        "duration_head",
        lambda key: "duration_head" if key.startswith("duration_head.") else None,
        sanitize_duration_head_key,
        _no_transform,
    ),
    "spatial_upscaler_x2_v1_0": ComponentRule(
        "spatial_upscaler", lambda key: "spatial_upscaler_x2_v1_0", _identity, transpose_upscaler_weight
    ),
    "temporal_upscaler_x2_v1_0": ComponentRule(
        "temporal_upscaler", lambda key: "temporal_upscaler_x2_v1_0", _identity, transpose_upscaler_weight
    ),
}

#: Pack file name -> (component, source role override). The transformer files
#: take their source from the variant named in the file.
_PACK_FILES: tuple[tuple[str, str, str], ...] = (
    ("transformer.safetensors", "transformer", "transformer_distilled"),
    ("transformer-distilled.safetensors", "transformer", "transformer_distilled"),
    ("transformer-dev.safetensors", "transformer", "transformer_dev"),
    ("connector.safetensors", "connector", "transformer_distilled|transformer_dev"),
    ("text_encoder.safetensors", "text_encoder", "text_encoder"),
    ("vae_encoder_conv.safetensors", "vae_encoder_conv", "video_vae_conv"),
    ("vae_decoder_conv.safetensors", "vae_decoder_conv", "video_vae_conv"),
    ("vae_encoder_av.safetensors", "vae_encoder_av", "video_vae_av"),
    ("vae_decoder_av.safetensors", "vae_decoder_av", "video_vae_av"),
    ("audio_vae.safetensors", "audio_vae", "audio_vae"),
    ("vocoder.safetensors", "vocoder", "audio_vae"),
    ("duration_head.safetensors", "duration_head", "duration_head"),
    ("spatial_upscaler_x2_v1_0.safetensors", "spatial_upscaler_x2_v1_0", "spatial_upscaler"),
    ("temporal_upscaler_x2_v1_0.safetensors", "temporal_upscaler_x2_v1_0", "temporal_upscaler"),
)


@dataclass(frozen=True)
class ConvertedEntry:
    source_key: str
    stored_key: str
    dtype: str
    shape: tuple[int, ...]
    quantize: bool


def converted_entries(source: Path, component: str, bits: int | None) -> list[ConvertedEntry]:
    """Map an official file's header to the component's pack keys, without loading tensors."""
    rule = COMPONENT_RULES[component]
    tensors, _ = read_safetensors_header(source)
    entries: list[ConvertedEntry] = []
    for key, info in tensors.items():
        if rule.classify(key) != component:
            continue
        new_key = rule.sanitize(key)
        if new_key is None:
            continue
        stored_key = f"{component}.{new_key}"
        shape = tuple(info["shape"])
        quantize = (
            bits is not None
            and rule.should_quantize is not None
            and rule.should_quantize(stored_key, len(shape))
            and shape[-1] % GROUP_SIZE == 0
        )
        entries.append(ConvertedEntry(key, stored_key, info["dtype"], shape, quantize))
    return entries


# ---------------------------------------------------------------------------
# Discovery and virtual pack construction
# ---------------------------------------------------------------------------


def find_official_sources(root: str | Path) -> OfficialSources | None:
    """Locate official LTX-2.5 files by name under ``root``.

    Returns ``None`` when ``root`` holds no official transformer, so any other
    directory (a converted pack, a HF snapshot) is left alone.

    Raises:
        FileNotFoundError: A transformer was found but a required component is missing.
        ValueError: A found file is truncated (incomplete download).
    """
    root = Path(root).resolve()
    if not root.is_dir():
        return None

    wanted = {name: role for role, name in OFFICIAL_FILENAMES.items()}
    found: dict[str, list[Path]] = {}
    for dirpath, dirnames, filenames in os.walk(root, followlinks=True):
        dirnames[:] = sorted(d for d in dirnames if not d.startswith("."))
        for name in filenames:
            role = wanted.get(name)
            if role is not None:
                found.setdefault(role, []).append(Path(dirpath) / name)

    if "transformer_distilled" not in found and "transformer_dev" not in found:
        return None

    files: dict[str, Path] = {}
    for role, paths in found.items():
        paths.sort(key=lambda p: (len(p.relative_to(root).parts), str(p)))
        if len(paths) > 1:
            others = ", ".join(str(p) for p in paths[1:])
            print(
                f"[official-weights] multiple copies of {paths[0].name}; using {paths[0]} (ignoring {others})",
                file=sys.stderr,
            )
        files[role] = paths[0]

    missing = [OFFICIAL_FILENAMES[r] for r in _REQUIRED_ROLES if r not in files]
    if missing:
        raise FileNotFoundError(f"Official LTX-2.5 files missing under {root}: {', '.join(missing)}")
    for path in files.values():
        if not _header_is_complete(path):
            raise ValueError(f"{path} is truncated (incomplete download?); re-download it.")
    return OfficialSources(root=root, files=files)


def quantize_bits_from_env() -> int | None:
    value = os.environ.get(QUANTIZE_ENV, str(DEFAULT_QUANTIZE_BITS)).strip().lower()
    if value in ("none", "0", "off", "bf16"):
        return None
    if value in ("4", "8"):
        return int(value)
    raise ValueError(f"{QUANTIZE_ENV} must be one of 8, 4, none; got {value!r}")


def _default_cache_root() -> Path:
    override = os.environ.get(CACHE_ENV)
    if override:
        return Path(override).expanduser()
    for parent in Path(__file__).resolve().parents:
        if (parent / "uv.lock").exists() and (parent / "packages").is_dir():
            return parent / ".cache" / "virtual-packs"
    return Path.home() / ".cache" / "ltx-2-studio" / "virtual-packs"


def _sidecar_name(sources: OfficialSources, bits: int | None) -> str:
    digest = hashlib.sha256()
    digest.update(f"format={VIRTUAL_PACK_FORMAT};bits={bits};group={GROUP_SIZE}".encode())
    for role in sorted(sources.files):
        path = sources.files[role]
        st = path.stat()
        digest.update(f"|{role}={path}:{st.st_size}:{st.st_mtime_ns}".encode())
    label = f"int{bits}" if bits else "bf16"
    return f"ltx-2.5-{label}-{digest.hexdigest()[:12]}"


def _write_placeholder(path: Path, entries: list[ConvertedEntry], metadata: dict[str, str], bits: int | None) -> None:
    """Write a header-only safetensors file listing ``entries`` as zero-length tensors."""
    header: dict[str, object] = {"__metadata__": metadata}
    for entry in entries:
        if entry.quantize:
            base = entry.stored_key.removesuffix(".weight")
            header[entry.stored_key] = {"dtype": "U32", "shape": [0], "data_offsets": [0, 0]}
            header[f"{base}.scales"] = {"dtype": entry.dtype, "shape": [0], "data_offsets": [0, 0]}
            header[f"{base}.biases"] = {"dtype": entry.dtype, "shape": [0], "data_offsets": [0, 0]}
        else:
            header[entry.stored_key] = {"dtype": entry.dtype, "shape": [0], "data_offsets": [0, 0]}
    payload = json.dumps(header, separators=(",", ":")).encode()
    payload += b" " * (-len(payload) % 8)
    with open(path, "wb") as f:
        f.write(struct.pack("<Q", len(payload)))
        f.write(payload)


def _embedded_config_payload(raw_config: str, model_version: str | None) -> dict:
    """Upstream DiT config plus mlx-forge's two consumer-driven touches."""
    config = json.loads(raw_config)
    norm_type = config.get("transformer", {}).get("text_encoder_norm_type")
    if isinstance(norm_type, str):
        config["transformer"]["text_encoder_norm_type"] = norm_type.lower()
    if model_version:
        config["model_version"] = model_version
    return config


def _extract_text_encoder_assets(source: Path, out_dir: Path) -> None:
    tensors, _ = read_safetensors_header(source)
    missing = [key for key in TEXT_ENCODER_ASSETS if key not in tensors]
    if missing:
        raise ValueError(f"{source.name} is missing embedded tokenizer assets: {', '.join(sorted(missing))}")
    with open(source, "rb") as f:
        data_start = 8 + struct.unpack("<Q", f.read(8))[0]
        for key, filename in TEXT_ENCODER_ASSETS.items():
            begin, end = tensors[key]["data_offsets"]
            f.seek(data_start + begin)
            (out_dir / filename).write_bytes(f.read(end - begin))


def build_virtual_pack(sources: OfficialSources, bits: int | None, cache_root: Path | None = None) -> Path:
    """Build (or reuse) the virtual pack directory for ``sources``."""
    cache_root = cache_root or _default_cache_root()
    final = cache_root / _sidecar_name(sources, bits)
    if (final / ".complete").exists():
        return final

    cache_root.mkdir(parents=True, exist_ok=True)
    staging = cache_root / f"{final.name}.tmp-{os.getpid()}"
    shutil.rmtree(staging, ignore_errors=True)
    staging.mkdir()

    transformer_source = sources.get("transformer_distilled") or sources.get("transformer_dev")
    assert transformer_source is not None
    _, dit_meta = read_safetensors_header(transformer_source)
    if not dit_meta.get("config"):
        raise ValueError(f"{transformer_source.name} carries no 'config' metadata")
    (staging / "embedded_config.json").write_text(
        json.dumps(_embedded_config_payload(dit_meta["config"], dit_meta.get("model_version")), indent=2)
    )

    text_encoder_source = sources.files["text_encoder"]
    _, te_meta = read_safetensors_header(text_encoder_source)
    if te_meta.get("gemma_config"):
        (staging / "text_encoder_config.json").write_text(json.dumps(json.loads(te_meta["gemma_config"]), indent=2))
    _extract_text_encoder_assets(text_encoder_source, staging)

    for role, component in (
        ("spatial_upscaler", "spatial_upscaler_x2_v1_0"),
        ("temporal_upscaler", "temporal_upscaler_x2_v1_0"),
    ):
        source = sources.get(role)
        if source is None:
            continue
        _, meta = read_safetensors_header(source)
        if meta.get("config"):
            (staging / f"{component}_config.json").write_text(
                json.dumps({"config": json.loads(meta["config"])}, indent=2)
            )

    if bits is not None:
        quantize_config = {
            "quantization": {
                "bits": bits,
                "group_size": GROUP_SIZE,
                "components": {
                    "transformer": "transformer_blocks Linear weights",
                    "text_encoder": "Gemma-4 attention and MLP Linear weights",
                },
            }
        }
        (staging / "quantize_config.json").write_text(json.dumps(quantize_config, indent=2))

    for filename, component, roles in _PACK_FILES:
        source = next((sources.get(r) for r in roles.split("|") if sources.get(r) is not None), None)
        if source is None:
            continue
        entries = converted_entries(source, component, bits)
        _, source_meta = read_safetensors_header(source)
        metadata = {k: source_meta[k] for k in ("model_version", "config") if source_meta.get(k)}
        metadata[VIRTUAL_METADATA_KEY] = json.dumps(
            {
                "format": VIRTUAL_PACK_FORMAT,
                "source": str(source),
                "component": component,
                "bits": bits,
                "group_size": GROUP_SIZE,
            }
        )
        _write_placeholder(staging / filename, entries, metadata, bits)

    lora = sources.get("distilled_lora")
    if lora is not None:
        os.symlink(lora, staging / OFFICIAL_FILENAMES["distilled_lora"])

    (staging / "SOURCES.json").write_text(
        json.dumps({role: str(p) for role, p in sorted(sources.files.items())}, indent=2)
    )
    (staging / ".complete").write_text("")
    try:
        staging.rename(final)
    except OSError:
        # Another process finished first; its pack is equivalent.
        shutil.rmtree(staging, ignore_errors=True)
    return final


@functools.lru_cache(maxsize=16)
def _resolve_cached(root: str, bits: int | None) -> str:
    sources = find_official_sources(root)
    if sources is None:
        return root
    pack = build_virtual_pack(sources, bits)
    label = f"int{bits} quantize-on-load" if bits else "bf16"
    print(f"[official-weights] {sources.root} -> virtual pack {pack} ({label})", file=sys.stderr)
    return str(pack)


def resolve_official_model_dir(model_dir: str | Path) -> Path:
    """Return a virtual pack for an official-layout directory, or ``model_dir`` unchanged."""
    path = Path(model_dir)
    if not path.is_dir():
        return path
    return Path(_resolve_cached(str(path.resolve()), quantize_bits_from_env())) if _looks_official(path) else path


def _looks_official(path: Path) -> bool:
    # Cheap pre-check so converted packs never pay for a directory walk.
    if (path / "embedded_config.json").exists() or (path / "transformer.safetensors").exists():
        return False
    transformer_names = (OFFICIAL_FILENAMES["transformer_distilled"], OFFICIAL_FILENAMES["transformer_dev"])
    for _dirpath, dirnames, filenames in os.walk(path, followlinks=True):
        dirnames[:] = [d for d in dirnames if not d.startswith(".")]
        if any(name in filenames for name in transformer_names):
            return True
    return False


# ---------------------------------------------------------------------------
# Loading
# ---------------------------------------------------------------------------


def virtual_source_info(path: str | Path) -> dict | None:
    """The virtual-source record of a placeholder file, or ``None`` for a real weight file."""
    path = Path(path)
    if path.suffix != ".safetensors" or not path.is_file():
        return None
    try:
        _, metadata = read_safetensors_header(path)
    except (OSError, ValueError, struct.error):
        return None
    raw = metadata.get(VIRTUAL_METADATA_KEY)
    return json.loads(raw) if raw else None


#: Bytes per safetensors dtype, for sizing a component without reading its tensors.
_DTYPE_BYTES: dict[str, int] = {
    "BOOL": 1, "U8": 1, "I8": 1, "F8_E4M3": 1, "F8_E5M2": 1,
    "U16": 2, "I16": 2, "F16": 2, "BF16": 2,
    "U32": 4, "I32": 4, "F32": 4,
    "U64": 8, "I64": 8, "F64": 8,
}  # fmt: skip


def virtual_component_nbytes(path: str | Path) -> int | None:
    """Bytes a placeholder's component occupies once converted, or ``None`` if not virtual.

    A placeholder lists its tensors with ``shape: [0]``, so its own file size says
    nothing about the weights it stands for -- a 31 KB header can front a gigabyte.
    Callers that charge a component against a memory budget need the real figure;
    this reads the official source's header and sums the converted entries, without
    loading a tensor.
    """
    info = virtual_source_info(path)
    if info is None:
        return None
    source = Path(info["source"])
    if not source.exists():
        return None
    total = 0
    for entry in converted_entries(source, info["component"], info["bits"]):
        count = math.prod(entry.shape)
        if entry.quantize:
            # int4/int8 payload plus its fp scales and biases, one pair per group.
            bits = info["bits"] or 8
            groups = count // GROUP_SIZE
            total += count * bits // 8 + 2 * groups * _DTYPE_BYTES.get(entry.dtype, 2)
        else:
            total += count * _DTYPE_BYTES.get(entry.dtype, 2)
    return total


def is_virtual_file(path: str | Path) -> bool:
    return virtual_source_info(path) is not None


def load_virtual(path: str | Path, prefix: str | None = None) -> dict[str, mx.array]:
    """Convert a placeholder's component from its official file, like ``load_split_safetensors``.

    Keys outside ``prefix`` are dropped *before* any transform or quantization,
    so loading e.g. only ``text_encoder.text_embedding_projection.`` never
    quantizes the whole Gemma tower.
    """
    info = virtual_source_info(path)
    if info is None:
        raise ValueError(f"{path} is not a virtual pack file")
    source = Path(info["source"])
    component = info["component"]
    bits = info["bits"]
    if not source.exists():
        raise FileNotFoundError(f"Official weights for {Path(path).name} moved or deleted: {source}")

    rule = COMPONENT_RULES[component]
    entries = [e for e in converted_entries(source, component, bits) if not prefix or e.stored_key.startswith(prefix)]
    if not entries:
        return {}

    raw = mx.load(str(source))
    to_keep: dict[str, mx.array] = {}
    to_quantize: dict[str, mx.array] = {}
    for entry in entries:
        new_key = entry.stored_key[len(component) + 1 :]
        value = rule.transform(new_key, raw[entry.source_key], component)
        (to_quantize if entry.quantize else to_keep)[entry.stored_key] = value
    del raw

    if to_quantize:
        # mlx-forge quantize_weights: materialise kept tensors first (quantize
        # work can evict lazy mmap-backed buffers), then quantize one tensor at a
        # time, dropping each source as soon as its replacement exists.
        _materialize(*to_keep.values())
        result = dict(to_keep)
        to_keep.clear()
        total = len(to_quantize)
        print(
            f"[official-weights] quantizing {component} ({total} Linear weights) to int{bits}...",
            file=sys.stderr,
            flush=True,
        )
        for key in list(to_quantize):
            weight = to_quantize.pop(key)
            _materialize(weight)
            q_weight, scales, biases = mx.quantize(weight, bits=bits, group_size=GROUP_SIZE)
            _materialize(q_weight, scales, biases)
            base = key.removesuffix(".weight")
            result[key] = q_weight
            result[f"{base}.scales"] = scales
            result[f"{base}.biases"] = biases
            del weight, q_weight, scales, biases
        gc.collect()
    else:
        result = to_keep

    if not prefix:
        return result
    return {key[len(prefix) :]: value for key, value in result.items()}
