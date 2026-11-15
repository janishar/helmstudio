"""Deterministic inputs for the LTX-2.5 stage-B goldens: the conditioning images and the synthetic LoRA.

numpy + Pillow + stdlib only, so the port's tests can rebuild them byte for byte (record_ltx.py records the sha256
of each). Nothing here loads weights; `synthetic_lora` reads only the transformer file's safetensors header.
"""

from __future__ import annotations

import hashlib
import json
import struct
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

# Real module names of the LTX-2.5 transformer in the LoRA file's ComfyUI naming (`diffusion_model.` + the official
# key without `model.diffusion_model.`): every block's video self-attention, video FF and one audio projection, plus
# two non-block Linears (float, not quantized at load) that exercise the `linear_1` rename and the float fuse path.
LORA_BLOCK_MODULES = ("attn1.to_q", "attn1.to_k", "attn1.to_v", "attn1.to_out.0", "ff.net.0.proj", "ff.net.2",
                      "audio_attn1.to_q")  # fmt: skip
LORA_OTHER_MODULES = ("adaln_single.emb.timestep_embedder.linear_1", "patchify_proj")
LORA_RANK, LORA_SEED, LORA_A_STD = 8, 20261115, 0.05
LORA_DELTA_STD = 0.002  # per element of B @ A in the blocks: ~5 % of a weight's std, ~3 int8 steps (group 64)
LORA_OTHER_SCALE = 0.25  # the non-block deltas are 4x smaller


def lora_modules(blocks: int = 48) -> list[str]:
    return [f"transformer_blocks.{i}.{m}" for i in range(blocks) for m in LORA_BLOCK_MODULES] + list(LORA_OTHER_MODULES)


def test_image(path: Path, variant: int = 0, width: int = 640, height: int = 400) -> str:
    """A colour gradient with a red disc and a blue square (variant 1 swaps their sides), saved as PNG.
    640x400 is 16:10, so the 448x256 goldens exercise the resize and the centre crop. Returns sha256 of the RGB."""
    y, x = np.mgrid[0:height, 0:width].astype(np.float32)
    rgb = np.stack([x / (width - 1) * 255, y / (height - 1) * 255, (1 - x / (width - 1)) * 160 + 40], -1)
    im = Image.fromarray(rgb.astype(np.uint8), "RGB")
    d = ImageDraw.Draw(im)
    disc, square = (60, 120, 200, 260), (380, 80, 520, 220)
    if variant:
        disc, square = (440, 140, 580, 280), (90, 60, 230, 200)
    d.ellipse(disc, fill=(230, 40, 40))
    d.rectangle(square, fill=(30, 60, 220))
    im.save(path, format="PNG")
    return hashlib.sha256(np.asarray(im).tobytes()).hexdigest()


def _header(path: Path) -> dict:
    with open(path, "rb") as f:
        n = struct.unpack("<Q", f.read(8))[0]
        return json.loads(f.read(n))


def _bf16(x: np.ndarray) -> bytes:
    """float32 -> bfloat16 bytes, round to nearest even (finite inputs)."""
    u = np.ascontiguousarray(x, np.float32).view(np.uint32).astype(np.uint64)
    return ((u + 0x7FFF + ((u >> 16) & 1)) >> 16).astype(np.uint16).tobytes()


def synthetic_lora(path: Path, transformer_file: Path) -> dict:
    """Write a rank-8 LoRA (ComfyUI keys `diffusion_model.<module>.lora_{A,B}.weight`, BF16, no alpha tensors) for
    `lora_modules()`, shapes from the transformer header. One PCG64 stream: for each module in order, A then B.
    Returns {file, sha256, bytes, rank, modules, keys, seed, a_std, b_std, other_scale}."""
    hdr = _header(transformer_file)
    rng = np.random.default_rng(LORA_SEED)
    b_std = LORA_DELTA_STD / (np.sqrt(LORA_RANK) * LORA_A_STD)
    tensors: dict[str, tuple[list[int], bytes]] = {}
    for m in lora_modules():
        out_f, in_f = hdr[f"model.diffusion_model.{m}.weight"]["shape"]
        scale = 1.0 if m.startswith("transformer_blocks.") else LORA_OTHER_SCALE
        a = rng.standard_normal((LORA_RANK, in_f), dtype=np.float32) * np.float32(LORA_A_STD)
        b = rng.standard_normal((out_f, LORA_RANK), dtype=np.float32) * np.float32(b_std * scale)
        tensors[f"diffusion_model.{m}.lora_A.weight"] = ([LORA_RANK, in_f], _bf16(a))
        tensors[f"diffusion_model.{m}.lora_B.weight"] = ([out_f, LORA_RANK], _bf16(b))
    meta = {"lora_rank": str(LORA_RANK), "lora_alpha": str(LORA_RANK), "note": "synthetic golden LoRA (random)"}
    header, off = {"__metadata__": meta}, 0
    for k in sorted(tensors):
        shape, data = tensors[k]
        header[k] = {"dtype": "BF16", "shape": shape, "data_offsets": [off, off + len(data)]}
        off += len(data)
    h = json.dumps(header, separators=(",", ":")).encode()
    h += b" " * (-len(h) % 8)
    with open(path, "wb") as f:
        f.write(struct.pack("<Q", len(h)) + h)
        for k in sorted(tensors):
            f.write(tensors[k][1])
    data = Path(path).read_bytes()
    return {"file": Path(path).name, "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
            "rank": LORA_RANK, "modules": len(lora_modules()), "keys": len(tensors), "seed": LORA_SEED,
            "a_std": LORA_A_STD, "b_std": float(b_std), "other_scale": LORA_OTHER_SCALE}  # fmt: skip


if __name__ == "__main__":  # self-check: bf16 rounding and determinism of the image
    assert _bf16(np.array([1.0, 1.00390625, -2.5], np.float32)) == np.array([0x3F80, 0x3F80, 0xC020], np.uint16).tobytes()
    import tempfile

    with tempfile.TemporaryDirectory() as t:
        assert test_image(Path(t) / "a.png") == test_image(Path(t) / "b.png")
    print("ok")
