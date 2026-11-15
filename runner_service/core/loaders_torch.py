"""Direct-to-GPU safetensors loading for torch (docs/plan/03-runtime.md section 6.3), imported only by the torch
env (torch is the optional extra "torch").

A component is built on the meta device by the pack (`build(component, model)`), then filled one tensor at a
time: on MPS safetensors reads each tensor straight into a GPU buffer (safe_open(device="mps", backend="pread"),
no CPU copy); elsewhere it goes through safetensors' own device path. Each tensor passes the hook chain
rename (the pack's `rename(component, key)`, optional) → cast (floating tensors to the component's dtype) →
LoRA bake (rows of mode bake: W + s * B @ A in fp32, cast back) → quantize (a Linear matched by the component's `quantize` globs at precision int8 becomes an Int8Linear: int8
weight, one bf16 scale per output row, quantized on the device as the tensor arrives). The int8 technique is the
streaming loader of the earlier studios, made generic.

LoRA adapters (LoraLinear, `activate`) wrap plain and int8 Linears of a resident component: y = base(x) + s * B(A x)
with s a tensor, so strength changes and swaps never reload the base."""

import gc
import logging
import time

import torch
import torch.nn.functional as F
from torch import nn

from core import pack as packs
from core.graph import RunError

log = logging.getLogger("loader")
DTYPES = {"bf16": torch.bfloat16, "fp16": torch.float16, "fp32": torch.float32}
# Rows at or below this use torch's int8 kernel, which reads the int8 weight as is (token-at-a-time decoding);
# bigger batches dequantize and use a bf16 matmul.
SMALL_BATCH = 8
PROGRESS_EVERY = 1.0  # seconds between load progress events
# GB of tensors read between giving the allocator's cache back during a load: without it MPS keeps every bf16 source
# buffer a quantized load frees (measured: 30.7 GB on the device while the 9.3 GB int8 Qwen text encoder loaded)
FREE_EVERY_GB = 1.0

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


class LoraLinear(nn.Module):
    """A Linear (nn.Linear or Int8Linear) with LoRA adapters: y = base(x) + sum s * B(A x). Each adapter's scale `s`
    is a 0-d tensor on the device, so a strength change is a write into it, never a reload. The base module is
    untouched: removing the last adapter puts it back as it was (activate)."""

    def __init__(self, base: nn.Module) -> None:
        super().__init__()
        self.base = base
        self.in_features, self.out_features = base.in_features, base.out_features
        self.adapters: dict[
            str, list
        ] = {}  # sha256 → [A [r, in], B [out, r], s]; not in the state dict

    @property
    def weight(
        self,
    ):  # code that reads a Linear's weight (its dtype, device) still finds the base's
        return getattr(self.base, "weight", None)

    @property
    def bias(self):
        return self.base.bias

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        out = self.base(x)
        for a, b, s in self.adapters.values():
            out = out + (s * F.linear(F.linear(x.to(a.dtype), a), b)).to(out.dtype)
        return out


def linears(module: nn.Module) -> list[str]:
    """The names of the Linear layers of `module` a LoRA may patch (nn.Linear, Int8Linear, an adapted one)."""
    return list(_linears(module))


def _linears(module: nn.Module) -> dict[str, nn.Module]:
    """name → the Linear (a LoraLinear, not its inner base)."""
    out = {}
    for name, m in module.named_modules():
        if isinstance(m, (nn.Linear, Int8Linear, LoraLinear)) and not name.endswith(".base"):
            out[name] = m
    return out


def lora_tensors(model, use, path, shapes: dict, dev: str, dtype: torch.dtype) -> dict:
    """module → (A, B, alpha / rank) of one LoRA file normalised against the model's lora block, on `dev` at
    `dtype`. RunError with the contract's text when a key does not apply (each unapplied key is logged)."""
    from core import lora

    block = model.pack.spec["lora"]
    block = {"modules": list(shapes), "renames": block.get("renames", []), "ignore": block.get("ignore", [])}  # fmt: skip
    try:
        tensors, _ = lora.read_header(path, block["ignore"])
    except lora.LoraFileError as e:
        raise RunError(f"LoRA {use.name}: {e}") from e
    res = lora.normalise(use.name, tensors, block, model.pack.family, shapes)
    for k in res["unapplied"]:
        log.warning("LoRA %s: %s does not apply to %s", use.name, k, model.pack.family)
    if res["error"]:
        raise RunError(res["error"])
    log.info("LoRA %s (%s keys): %d of %d keys applied, %d ignored", use.name, res["key_style"], res["applied"],
             res["total"], res["ignored"])  # fmt: skip
    from safetensors import safe_open

    out = {}
    with safe_open(str(path), "pt", device="cpu") as f:
        for module, g in res["groups"].items():
            a, b = f.get_tensor(g["A"]), f.get_tensor(g["B"])
            factor = float(f.get_tensor(g["alpha"]).float().reshape(-1)[0]) / a.shape[0] if g["alpha"] else 1.0  # fmt: skip
            out[module] = (a.to(dev, dtype), b.to(dev, dtype), factor)
    return out


def _dtype(m: nn.Module) -> torch.dtype:
    """The compute dtype of a Linear: its weight's, an Int8Linear's scale dtype (bf16)."""
    m = m.base if isinstance(m, LoraLinear) else m
    return m.scale.dtype if isinstance(m, Int8Linear) else m.weight.dtype


def _device(m: nn.Module) -> torch.device:
    m = m.base if isinstance(m, LoraLinear) else m
    return m.scale.device if isinstance(m, Int8Linear) else m.weight.device


def activate(model, component: str, module: nn.Module, ctx) -> float:
    """Make `module` carry exactly `model`'s adapter LoRAs for `component`: detach the extra, attach the missing
    (read from the LoRA's file, fetched for an own one), set every scale to strength * alpha / rank. Returns the
    adapters' GB. The base weights are never written."""
    want = model.adapters(component)
    state = module.__dict__.setdefault("_lora_adapters", {})  # sha256 → {module name: factor}
    lins = _linears(module)
    for sha in [s for s in state if s not in want]:
        for name in state.pop(sha):
            wrapper = lins[name]
            del wrapper.adapters[sha]
            if not wrapper.adapters:  # the plain Linear again, exactly as it was
                parent, _, child = name.rpartition(".")
                setattr(module.get_submodule(parent) if parent else module, child, wrapper.base)
    lins = _linears(module)
    for sha, (use, _) in want.items():
        if sha in state:
            continue
        ctx.check()
        path = ctx.lora_path(use)
        shapes = {n: (m.in_features, m.out_features) for n, m in lins.items()}
        first = next(iter(lins.values()))
        deltas = lora_tensors(model, use, path, shapes, _device(first), _dtype(first))
        state[sha] = {}
        for name, (a, b, factor) in deltas.items():
            m = lins[name]
            if not isinstance(m, LoraLinear):
                parent, _, child = name.rpartition(".")
                m = lins[name] = LoraLinear(m)
                setattr(module.get_submodule(parent) if parent else module, child, m)
            m.adapters[sha] = [a, b, torch.zeros((), dtype=torch.float32, device=a.device)]
            state[sha][name] = factor
    total = 0
    for sha, (_, strength) in want.items():
        for name, factor in state[sha].items():
            a, b, s = lins[name].adapters[sha]
            s.fill_(strength * factor)
            total += a.numel() * a.element_size() + b.numel() * b.element_size()
    return total / 2**30


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
    bake=None,
    check=lambda: None,
    progress=lambda done, total: None,
) -> nn.Module:
    """Fill `module` (parameters on meta) from `files`. `quantize(module name)` picks the Linears stored int8;
    `rename(key)` maps a checkpoint key to the module's name for it, or None to skip it. `bake` (module name →
    [(A, B, s)], LoRA bake) merges W + sum s * (B @ A) in fp32 into those Linears' weights after the cast and before
    quantization. `check` runs before each tensor (a cancel point); `progress(done, total)` after each."""
    bake, baked = bake or {}, set()
    for parent_name, parent in list(module.named_modules()):
        for child_name, child in list(parent.named_children()):
            name = f"{parent_name}.{child_name}" if parent_name else child_name
            if type(child) is nn.Linear and quantize(name):
                setattr(parent, child_name, Int8Linear(child))
    modules = dict(module.named_modules())
    total = sum(len(packs.header(p)) for p in files)
    done, read = 0, 0
    for path in files:
        for key, tensor in tensors(path, dev):
            check()
            read += tensor.numel() * tensor.element_size()
            name = rename(key)
            if name is not None:
                owner_name, _, attr = name.rpartition(".")
                owner = modules.get(owner_name)
                if owner is None:
                    raise KeyError(f"{path.name}: {key} has no place in {type(module).__name__}")
                if tensor.is_floating_point() and tensor.dtype != dtype:
                    tensor = tensor.to(dtype)
                if attr == "weight" and owner_name in bake:
                    merged = tensor.float()
                    for a, b, s in bake[owner_name]:
                        merged += s * (b.float() @ a.float())
                    tensor = merged.to(dtype)
                    baked.add(owner_name)
                if isinstance(owner, Int8Linear) and attr == "weight":
                    owner.quantize(tensor)
                elif attr in owner._parameters:
                    owner._parameters[attr] = nn.Parameter(tensor, requires_grad=False)
                else:
                    owner._buffers[attr] = tensor
            del tensor
            if read >= FREE_EVERY_GB * 2**30:
                free()  # the freed source buffers go back now, not after the load
                read = 0
            done += 1
            progress(done, total)
    if unbaked := sorted(bake.keys() - baked):
        raise KeyError(
            f"{type(module).__name__}: LoRA modules without a weight: {', '.join(unbaked[:8])}"
        )
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
    bake: dict[str, list] = {}
    if baked := model.baked(
        component
    ):  # LoRA bake: the deltas, on the device, before the first tensor
        shapes = {n: (m.in_features, m.out_features) for n, m in _linears(meta).items()}
        for use in baked:
            path = ctx.lora_path(use)
            deltas = lora_tensors(model, use, path, shapes, ctx.device, torch.float32)
            for name, (a, b, factor) in deltas.items():
                bake.setdefault(name, []).append((a, b, use.strength * factor))
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
        bake=bake,
        check=ctx.check,
        progress=progress,
    )
