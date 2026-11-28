"""Synthetic LoRA files (no LoRA is downloaded, ever): small-rank random A/B tensors for given module names, in one of
the three key styles of contracts/README.md "LoRA normaliser".

    uv run --extra torch python tests/make_lora.py out.safetensors --style kohya --rank 8 \\
        --model ~/GenAI/models/Qwen-Image-2.1 [--match 'transformer_blocks.*.attn.*'] [--alpha 8]

`--model` reads the transformer's module names and Linear shapes from its safetensors headers only (2-D `.weight`
tensors), never its weights. In tests: `make_lora(path, {module: (in, out)}, rank=, style=)` and
`from_fixture(fixture, path)` (a contracts/fixtures/loras/*.json header with random values)."""

import argparse
import json
import struct
import sys
from fnmatch import fnmatchcase
from pathlib import Path

import torch
from safetensors.torch import save_file

DTYPES = {"BF16": torch.bfloat16, "F16": torch.float16, "F32": torch.float32}


def keys(module: str, style: str) -> tuple[str, str, str]:
    """(A key, B key, alpha key) of one module in `style`."""
    if style == "kohya":
        base = "lora_unet_" + module.replace(".", "_")
        return f"{base}.lora_down.weight", f"{base}.lora_up.weight", f"{base}.alpha"
    base = ("diffusion_model." if style == "comfyui" else "transformer.") + module
    return f"{base}.lora_A.weight", f"{base}.lora_B.weight", f"{base}.alpha"


def make_lora(
    path: Path,
    modules: dict[str, tuple[int, int]],
    *,
    rank: int = 4,
    style: str = "peft",
    alpha: float | None = None,
    dtype: torch.dtype = torch.float32,
    seed: int = 0,
    metadata: dict | None = None,
) -> dict[str, tuple[torch.Tensor, torch.Tensor]]:
    """Write a LoRA for `modules` (name → (in_features, out_features)); returns module → (A [rank, in], B [out,
    rank]) as written. B is random too (a trained LoRA's is), so every delta is non-zero."""
    g = torch.Generator().manual_seed(seed)
    tensors, out = {}, {}
    for m, (fan_in, fan_out) in modules.items():
        a = (torch.randn(rank, fan_in, generator=g) / fan_in**0.5).to(dtype)
        b = (torch.randn(fan_out, rank, generator=g) / rank**0.5).to(dtype)
        ka, kb, kalpha = keys(m, style)
        tensors[ka], tensors[kb] = a, b
        if alpha is not None:
            tensors[kalpha] = torch.tensor(float(alpha))
        out[m] = (a, b)
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    save_file(tensors, str(path), metadata=metadata)
    return out


def from_fixture(fx: dict, path: Path, seed: int = 0) -> None:
    """A contracts/fixtures/loras/*.json header as a real file: random values, `values` for the alphas."""
    g = torch.Generator().manual_seed(seed)
    tensors = {}
    for k, t in fx["tensors"].items():
        if k in fx.get("values", {}):
            tensors[k] = torch.full(t["shape"], float(fx["values"][k]), dtype=DTYPES[t["dtype"]])
        else:
            tensors[k] = torch.randn(t["shape"], generator=g).to(DTYPES[t["dtype"]])
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    save_file(tensors, str(path), metadata=fx.get("metadata") or None)


def model_linears(folder: Path, component: str = "transformer") -> dict[str, tuple[int, int]]:
    """module → (in, out) of every 2-D `.weight` in a model component's safetensors headers (no weights read)."""
    out = {}
    for p in sorted((folder / component).glob("*.safetensors")):
        with p.open("rb") as f:
            (n,) = struct.unpack("<Q", f.read(8))
            header = json.loads(f.read(n))
        for k, t in header.items():
            if k.endswith(".weight") and len(t.get("shape", [])) == 2:
                out[k.removesuffix(".weight")] = (t["shape"][1], t["shape"][0])
    return out


def main(argv=None) -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("out", type=Path)
    ap.add_argument("--model", type=Path, required=True, help="a model folder (its transformer/ headers)")
    ap.add_argument("--component", default="transformer")
    ap.add_argument("--style", choices=["peft", "kohya", "comfyui"], default="peft")
    ap.add_argument("--rank", type=int, default=4)
    ap.add_argument("--alpha", type=float)
    ap.add_argument("--match", action="append", help="fnmatch globs over module names (default: all)")
    ap.add_argument("--seed", type=int, default=0)
    a = ap.parse_args(argv)
    mods = model_linears(a.model.expanduser(), a.component)
    if a.match:
        mods = {m: s for m, s in mods.items() if any(fnmatchcase(m, g) for g in a.match)}
    if not mods:
        sys.exit("no modules matched")
    make_lora(a.out, mods, rank=a.rank, style=a.style, alpha=a.alpha, dtype=torch.bfloat16, seed=a.seed)
    print(f"{a.out}: {len(mods)} modules, rank {a.rank}, {a.style} keys, {a.out.stat().st_size} bytes")


if __name__ == "__main__":
    main()
