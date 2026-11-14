"""Direct-to-GPU safetensors loading for torch (docs/plan/03-runtime.md section 6.3), imported only by the torch
env (torch is the optional extra "torch").

A component is built on the meta device by the pack (`build(component, model)`), then filled one tensor at a
time: on MPS safetensors reads each tensor straight into a GPU buffer (safe_open(device="mps", backend="pread"),
no CPU copy); elsewhere it goes through safetensors' own device path. Each tensor passes the hook chain
rename (the pack's `rename(component, key)`, optional) → cast (floating tensors to the component's dtype) →
quantize (a Linear matched by the component's `quantize` globs at precision int8 becomes an Int8Linear: int8
weight, one bf16 scale per output row, quantized on the device as the tensor arrives). The int8 technique is the
streaming loader of the earlier studios, made generic."""

import gc
import time

import torch
import torch.nn.functional as F
from torch import nn

from core import pack as packs
from core.graph import RunError

DTYPES = {"bf16": torch.bfloat16, "fp16": torch.float16, "fp32": torch.float32}
# Rows at or below this use torch's int8 kernel, which reads the int8 weight as is (token-at-a-time decoding);
# bigger batches dequantize and use a bf16 matmul.
SMALL_BATCH = 8
PROGRESS_EVERY = 1.0  # seconds between load progress events

if torch.backends.mps.is_available():
    # torch 2.14+: mps.empty_cache() waits on the GPU while holding the GIL, which non_blocking copies need ->
    # deadlock. synchronize() first releases it.
    # ponytail: a global patch; drop it once torch's empty_cache releases the GIL itself
    _empty_cache = torch.mps.empty_cache

    def _safe_empty_cache() -> None:
        torch.mps.synchronize()
        _empty_cache()

    torch.mps.empty_cache = _safe_empty_cache


def device() -> str:
    """mps, cuda or cpu: the first one present."""
    if torch.backends.mps.is_available():
        return "mps"
    return "cuda" if torch.cuda.is_available() else "cpu"


def free() -> None:
    """Give memory released by dropped modules back to the system (after the residency manager evicts)."""
    gc.collect()
    if torch.backends.mps.is_available():
        torch.mps.empty_cache()  # synchronizes first (above)
    elif torch.cuda.is_available():
        torch.cuda.empty_cache()


def measure_gb() -> float | None:
    """What the device really holds: MPS driver allocation (torch's own counter misses safetensors' buffers)."""
    if torch.backends.mps.is_available():
        return torch.mps.driver_allocated_memory() / 2**30
    if torch.cuda.is_available():
        return torch.cuda.memory_reserved() / 2**30
    return None


class Int8Linear(nn.Module):
    """nn.Linear with an int8 weight and one bf16 scale per output row."""

    def __init__(self, linear: nn.Linear) -> None:
        super().__init__()
        self.in_features, self.out_features = linear.in_features, linear.out_features
        shape = (self.out_features, self.in_features)
        self.register_buffer("qweight", torch.empty(shape, dtype=torch.int8, device="meta"))
        self.register_buffer(
            "scale", torch.empty(self.out_features, dtype=torch.bfloat16, device="meta")
        )
        self.bias = linear.bias
        # the MPS int8 kernel needs both sides in multiples of 32
        self.kernel = self.in_features % 32 == 0 and self.out_features % 32 == 0

    @torch.no_grad()
    def quantize(self, weight: torch.Tensor) -> None:
        # the scale is rounded to bf16 first, so dequantizing is exactly qweight * scale
        scale = (weight.abs().amax(dim=1).float() / 127).clamp(min=1e-12).to(torch.bfloat16)
        q = (weight.float() / scale.float()[:, None]).round_().clamp_(-127, 127)
        self.qweight, self.scale = q.to(torch.int8), scale

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        shape = x.shape
        small = x.numel() // self.in_features <= SMALL_BATCH
        if small and self.kernel and x.dtype == torch.bfloat16:
            out = torch._weight_int8pack_mm(
                x.reshape(-1, self.in_features), self.qweight, self.scale
            )
            out = out.reshape(*shape[:-1], self.out_features)
            return out if self.bias is None else out + self.bias.to(out.dtype)
        weight = self.qweight.to(x.dtype) * self.scale.to(x.dtype)[:, None]
        return F.linear(x, weight, None if self.bias is None else self.bias.to(x.dtype))


def tensors(path, dev: str):
    """(key, tensor on `dev`) for each tensor of one safetensors file, one at a time."""
    from safetensors import safe_open

    kw = {"backend": "pread"} if dev == "mps" else {}
    with safe_open(str(path), "pt", device=dev, **kw) as f:
        for key in f.keys():  # noqa: SIM118 (safe_open has no __iter__)
            yield key, f.get_tensor(key)


def load(
    module: nn.Module,
    files: list,
    *,
    dev: str,
    dtype: torch.dtype,
    quantize=lambda name: False,
    rename=lambda key: key,
    check=lambda: None,
    progress=lambda done, total: None,
) -> nn.Module:
    """Fill `module` (parameters on meta) from `files`. `quantize(module name)` picks the Linears stored int8;
    `rename(key)` maps a checkpoint key to the module's name for it, or None to skip it. `check` runs before each
    tensor (a cancel point); `progress(done, total)` after each."""
    for parent_name, parent in list(module.named_modules()):
        for child_name, child in list(parent.named_children()):
            name = f"{parent_name}.{child_name}" if parent_name else child_name
            if type(child) is nn.Linear and quantize(name):
                setattr(parent, child_name, Int8Linear(child))
    modules = dict(module.named_modules())
    total = sum(len(packs.header(p)) for p in files)
    done = 0
    for path in files:
        for key, tensor in tensors(path, dev):
            check()
            name = rename(key)
            if name is not None:
                owner_name, _, attr = name.rpartition(".")
                owner = modules.get(owner_name)
                if owner is None:
                    raise KeyError(f"{path.name}: {key} has no place in {type(module).__name__}")
                if tensor.is_floating_point() and tensor.dtype != dtype:
                    tensor = tensor.to(dtype)
                if isinstance(owner, Int8Linear) and attr == "weight":
                    owner.quantize(tensor)
                elif attr in owner._parameters:
                    owner._parameters[attr] = nn.Parameter(tensor, requires_grad=False)
                else:
                    owner._buffers[attr] = tensor
            del tensor
            done += 1
            progress(done, total)
    missing = [n for n, p in module.named_parameters() if p.is_meta]
    missing += [n for n, b in module.named_buffers() if b.is_meta]
    if missing:
        raise KeyError(f"{type(module).__name__}: not in the checkpoint: {', '.join(missing[:8])}")
    # buffers made in __init__ (rotary frequencies) were built on the CPU
    for m in module.modules():
        for attr, buf in m._buffers.items():
            if buf is not None and buf.device.type != torch.device(dev).type:
                m._buffers[attr] = buf.to(dev)
    return module.eval().requires_grad_(False)


def load_component(model, component: str, ctx):
    """Load one component of a Model handle (core.pack.ModelRef) for ctx's run: the pack builds it on meta, the
    loader fills it, reporting progress with phase "loading <component>"."""
    spec, precision = model.pack.component(component), model.precision_of(component)
    files = [p for p in model.files(component) if p.suffix == ".safetensors"]
    if not files:
        raise RunError(f"{component}: no weights for {model.owner(component)} in the models folder")
    meta = model.pack.module.build(component, model)
    rename = getattr(model.pack.module, "rename", None)
    phase, last = f"loading {component}", [0.0]

    def progress(done: int, total: int) -> None:
        now = time.monotonic()
        if done == total or now - last[0] >= PROGRESS_EVERY:
            last[0] = now
            ctx.progress(done, total, phase)

    return load(
        meta,
        files,
        dev=ctx.device,
        dtype=DTYPES[spec.get("dtype", "bf16")],
        quantize=lambda name: packs.quantized(spec, precision, name),
        rename=(lambda key: rename(component, key)) if rename else (lambda key: key),
        check=ctx.check,
        progress=progress,
    )
