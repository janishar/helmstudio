"""Direct-to-GPU safetensors loading for torch (docs/plan/03-runtime.md section 6.3), imported only by the torch
env (torch is the optional extra "torch").

A component is built on the meta device by the pack (`build(component, model)`), then filled one tensor at a
time: on MPS safetensors reads each tensor straight into a GPU buffer (safe_open(device="mps", backend="pread"),
no CPU copy); on CUDA each tensor is read into one pinned host buffer and copied to the device with non_blocking
(`staged`: one tensor of host memory at a time, the stated exception to "no CPU copy", checked against the host-RAM
budget); on the CPU through safetensors. Each tensor passes the hook chain
rename (the pack's `rename(component, key)`, optional; a key it drops is never read) → the pack's tensor hook
(`tensor_hook(component, model)`, optional: MiniMax's NVFP4 dequant) → cast (floating tensors to the component's
dtype; `dtype: stored` keeps each tensor's own) →
LoRA bake (rows of mode bake: W + s * B @ A in fp32, cast back) → quantize (a Linear matched by the component's `quantize` globs at precision int8 becomes an Int8Linear: int8
weight, one bf16 scale per output row, quantized on the device as the tensor arrives). The int8 technique is the
streaming loader of the earlier studios, made generic.

LoRA adapters (LoraLinear, `activate`) wrap plain and int8 Linears of a resident component: y = base(x) + s * B(A x)
with s a tensor, so strength changes and swaps never reload the base."""

import gc
import json
import logging
import re
import struct
import time
from functools import cache
from pathlib import Path

import torch
import torch.nn.functional as F
from torch import nn

import config
from core import pack as packs
from core.graph import RunError
from core.residency import footprint_gb, host_device, ram_gb, rss_gb

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


@cache
def device() -> str:
    """mps, cuda or cpu: the first one present, picked once per process; RUNNER_DEVICE forces one of those present."""
    present = [d for d, ok in (("mps", torch.backends.mps.is_available()),
                               ("cuda", torch.cuda.is_available()), ("cpu", True)) if ok]  # fmt: skip
    want = config.DEVICE or present[0]
    if want not in present:
        raise ValueError(
            f"RUNNER_DEVICE={want} is not present here (present: {', '.join(present)})"
        )
    return want


def free() -> None:
    """Give memory released by dropped modules back to the system (after the residency manager evicts)."""
    gc.collect()
    if device() == "mps":
        torch.mps.empty_cache()  # synchronizes first (above)
    elif device() == "cuda":
        torch.cuda.synchronize()
        torch.cuda.empty_cache()
        getattr(torch._C, "_host_emptyCache", lambda: None)()  # the pinned staging buffers too


@cache
def context_gb() -> float:
    """CUDA: the device's used memory just after this process created its context, before any tensor (measured
    once: main calls it at start)."""
    free_b, total = torch.cuda.mem_get_info()
    return (total - free_b) / 2**30


def measure_gb() -> float | None:
    """What the process really holds: on MPS its physical footprint (the driver allocation, safetensors' buffers
    torch's own counter misses, and the CPU side); on CUDA the allocator's reservation + the context; on the CPU
    its resident set."""
    if device() == "mps":
        fp = footprint_gb()
        return fp if fp is not None else torch.mps.driver_allocated_memory() / 2**30
    if device() == "cuda":
        return torch.cuda.memory_reserved() / 2**30 + context_gb()
    return rss_gb()


def total_gb() -> float:
    """The memory the budget is cut from: VRAM on CUDA, else RAM."""
    return torch.cuda.mem_get_info()[1] / 2**30 if device() == "cuda" else ram_gb()


def host_ram() -> dict:
    """The hosts key's `host_ram` (CUDA: host RAM is the second limit): RAM - RUNNER_RAM_RESERVE_GB, and the RSS."""
    return {
        "budget_gb": round(ram_gb() - config.RAM_RESERVE_GB, 1),
        "used_gb": round(rss_gb() or 0, 1),
    }


@cache
def _driver() -> str | None:
    """The NVIDIA driver version (the container runtime mounts /proc/driver/nvidia)."""
    try:
        text = Path("/proc/driver/nvidia/version").read_text()
    except OSError:
        return None
    m = re.search(r"Kernel Module(?: for \S+)?\s+(\d+(?:\.\d+)+)", text)
    return m.group(1)[:40] if m else None


def device_info() -> dict:
    """The hosts key's `device`, measured at each write."""
    torch_v = f"torch {torch.__version__.split('+')[0]}"
    if device() != "cuda":
        return host_device(device()) | {"runtime": f"{torch_v} · {device().upper()}"}
    free_b, total = torch.cuda.mem_get_info()
    major, minor = torch.cuda.get_device_capability(0)
    out = {"kind": "cuda", "name": torch.cuda.get_device_name(0)[:80], "memory_total_gb": round(total / 2**30, 1),
           "memory_used_gb": round((total - free_b) / 2**30, 1), "capability": f"{major}.{minor}",
           "runtime": f"{torch_v} · CUDA {torch.version.cuda}"}  # fmt: skip
    return out | ({"driver": d} if (d := _driver()) else {})


def dtype_of(name: str) -> torch.dtype | None:
    """A component's dtype here (None for `stored`: no cast): bf16 loads as fp16 on CUDA below compute capability
    8.0 (no native bf16)."""
    if name == "stored":
        return None
    if name == "bf16" and device() == "cuda" and torch.cuda.get_device_capability(0) < (8, 0):
        name = "fp16"
    return DTYPES[name]


@cache
def _int8_kernel(device_type: str) -> bool:
    """Whether torch has its int8 weight-only matmul kernel on this device type (CPU and MPS; CUDA per build)."""
    return torch._C._dispatch_has_kernel_for_dispatch_key(
        "aten::_weight_int8pack_mm", device_type.upper()
    )


@cache
def hadamard(n: int, device: str, dtype: torch.dtype) -> torch.Tensor:
    """The normalized block Hadamard matrix of ConvRot (n = 4^k): kron(H4, ..., H4) / sqrt(n), symmetric and
    orthogonal (H @ H = I)."""
    h4 = torch.tensor(
        [[1, 1, 1, -1], [1, 1, -1, 1], [1, -1, 1, 1], [-1, 1, 1, 1]], dtype=torch.float64
    )
    h = torch.ones(1, 1, dtype=torch.float64)
    while h.shape[0] < n:
        h = torch.kron(h, h4)
    if h.shape[0] != n:
        raise ValueError(f"ConvRot group {n} is not a power of 4")
    return (h / n**0.5).to(device=device, dtype=dtype)


class Int8Linear(nn.Module):
    """nn.Linear with an int8 weight and one bf16 scale per output row. `rot_group` (ConvRot, API 1.33: a community
    int8 build): the stored weight's input axis was rotated offline per group of 256 by the block Hadamard matrix H
    (W_rot = W @ H per group), so the input is rotated the same way first: (x H)(W H)^T = x W^T."""

    def __init__(self, linear: nn.Linear, rot_group: int | None = None) -> None:
        super().__init__()
        self.in_features, self.out_features = linear.in_features, linear.out_features
        if rot_group and self.in_features % rot_group:
            raise ValueError(
                f"ConvRot: {self.in_features} input features are not a multiple of {rot_group}"
            )
        self.rot_group = rot_group
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
        if g := self.rot_group:
            x = (
                x.reshape(-1, self.in_features // g, g) @ hadamard(g, str(x.device), x.dtype)
            ).reshape(shape)
        small = x.numel() // self.in_features <= SMALL_BATCH
        if small and self.kernel and x.dtype == torch.bfloat16 and _int8_kernel(x.device.type):
            out = torch._weight_int8pack_mm(
                x.reshape(-1, self.in_features), self.qweight, self.scale
            )
            out = out.reshape(*shape[:-1], self.out_features)
            return out if self.bias is None else out + self.bias.to(out.dtype)
        weight = self.qweight.to(x.dtype) * self.scale.to(x.dtype)[:, None]
        return F.linear(x, weight, None if self.bias is None else self.bias.to(x.dtype))


E4M3_MAX = 448.0
FP8_MIN_ROWS = 16  # an input with fewer rows takes the dequant path (Fp8Linear.takes_scaled_mm)
# whether torch._scaled_mm works here: decided at the first CUDA forward of the process (RUNNER_FP8_MATMUL)
_scaled_mm_ok: bool | None = None


class Fp8Linear(nn.Module):
    """nn.Linear with a stored float8 e4m3 weight [out, in] and an fp32 scale per output row [out, 1] (a community
    FP8 build, API 1.33: w = qweight * scale; a plain FP8 cast has no scale: ones). On CUDA it multiplies in FP8
    with torch._scaled_mm, the input quantized per row on the fly (the checkpoints' recipe); when that is refused
    (once per process, logged) and on any other device it dequantizes one layer at a time to the input's dtype."""

    def __init__(self, linear: nn.Linear) -> None:
        super().__init__()
        self.in_features, self.out_features = linear.in_features, linear.out_features
        shape = (self.out_features, self.in_features)
        self.register_buffer(
            "qweight", torch.empty(shape, dtype=torch.float8_e4m3fn, device="meta")
        )
        # ones on the CPU (a plain FP8 cast has no scale); load() moves it with the rest
        self.register_buffer("scale", torch.ones(self.out_features, 1))
        self.bias = linear.bias

    def dequantized(self, dtype: torch.dtype) -> torch.Tensor:
        return self.qweight.to(dtype) * self.scale.to(dtype)

    def takes_scaled_mm(self, x: torch.Tensor) -> bool:
        """FP8 matmul on CUDA for shapes it takes, unless RUNNER_FP8_MATMUL=dequant or the kernel was refused. An
        input of fewer than FP8_MIN_ROWS rows (the adaLN modulations and timestep MLPs: one row per image) is
        dequantized: an FP8 error there shifts every token of the image (measured on the VM: +2 to +11 dB against
        the int8 golden at the same speed); use_fast_accum makes no difference to the row-wise kernel on sm_120."""
        fits = self.in_features % 16 == 0 and self.out_features % 16 == 0
        rows = x.numel() // self.in_features
        return (x.is_cuda and fits and rows >= FP8_MIN_ROWS and _scaled_mm_ok is not False
                and config.FP8_MATMUL != "dequant")  # fmt: skip

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        global _scaled_mm_ok
        bias = None if self.bias is None else self.bias.to(x.dtype)
        if self.takes_scaled_mm(x):
            try:
                x2 = x.reshape(-1, self.in_features)
                xs = (x2.abs().amax(-1, keepdim=True).float() / E4M3_MAX).clamp_(min=1e-12)
                xq = (x2.float() / xs).clamp_(-E4M3_MAX, E4M3_MAX).to(torch.float8_e4m3fn)
                out = torch._scaled_mm(xq, self.qweight.t(), scale_a=xs, scale_b=self.scale.t(),
                                       out_dtype=x.dtype, use_fast_accum=True)  # fmt: skip
                _scaled_mm_ok = True
                out = out.reshape(*x.shape[:-1], self.out_features)
                return out if bias is None else out + bias
            except torch.OutOfMemoryError:
                raise  # out of memory is not a missing kernel: keep the FP8 matmul
            except RuntimeError as e:
                if _scaled_mm_ok:  # it worked before: a real failure, not a missing kernel
                    raise
                _scaled_mm_ok = False
                log.warning(
                    "fp8: row-wise torch._scaled_mm refused here (%s); dequantizing per layer", e
                )
        return F.linear(x, self.dequantized(x.dtype), bias)


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
    """The names of the Linear layers of `module` a LoRA may patch (nn.Linear, Int8Linear, Fp8Linear, an adapted
    one)."""
    return list(_linears(module))


def _linears(module: nn.Module) -> dict[str, nn.Module]:
    """name → the Linear (a LoraLinear, not its inner base)."""
    out = {}
    for name, m in module.named_modules():
        linear = isinstance(m, (nn.Linear, Int8Linear, Fp8Linear, LoraLinear))
        if linear and not name.endswith(".base"):
            out[name] = m
    return out


def lora_tensors(model, use, path, shapes: dict, dev: str, dtype: torch.dtype) -> dict:
    """module → (A, B, alpha / rank) of one LoRA file normalised against the model's lora block, on `dev` at
    `dtype`. RunError with the contract's text when a key does not apply (each unapplied key is logged)."""
    from core import lora

    block = model.pack.spec["lora"]
    block = {"modules": list(shapes), "renames": block.get("renames", []), "ignore": block.get("ignore", [])}  # fmt: skip
    try:
        header, _ = lora.read_header(path, block["ignore"])
    except lora.LoraFileError as e:
        raise RunError(f"LoRA {use.name}: {e}") from e
    res = lora.normalise(use.name, header, block, model.pack.family, shapes)
    for k in res["unapplied"]:
        log.warning("LoRA %s: %s does not apply to %s", use.name, k, model.pack.family)
    if res["error"]:
        raise RunError(res["error"])
    log.info("LoRA %s (%s keys): %d of %d keys applied, %d ignored", use.name, res["key_style"], res["applied"],
             res["total"], res["ignored"])  # fmt: skip
    want = {k for g in res["groups"].values() for k in (g["A"], g["B"], g["alpha"]) if k}
    got = dict(
        tensors(path, dev, want)
    )  # straight to the device (CUDA: through the pinned staging buffer)
    out = {}
    for module, g in res["groups"].items():
        a, b = got[g["A"]], got[g["B"]]
        factor = float(got[g["alpha"]].float().reshape(-1)[0]) / a.shape[0] if g["alpha"] else 1.0
        out[module] = (a.to(dtype), b.to(dtype), factor)
    return out


def _dtype(m: nn.Module) -> torch.dtype:
    """The compute dtype of a Linear: its weight's; an Int8Linear's or Fp8Linear's is bf16 (fp16 where bf16 is not
    native)."""
    m = m.base if isinstance(m, LoraLinear) else m
    return dtype_of("bf16") if isinstance(m, (Int8Linear, Fp8Linear)) else m.weight.dtype


def _device(m: nn.Module) -> torch.device:
    m = m.base if isinstance(m, LoraLinear) else m
    return m.scale.device if isinstance(m, (Int8Linear, Fp8Linear)) else m.weight.device


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


# safetensors dtype names → torch
ST_DTYPES = {"F64": torch.float64, "F32": torch.float32, "F16": torch.float16, "BF16": torch.bfloat16,
             "I64": torch.int64, "I32": torch.int32, "I16": torch.int16, "I8": torch.int8, "U8": torch.uint8,
             "BOOL": torch.bool, "F8_E4M3": torch.float8_e4m3fn, "F8_E5M2": torch.float8_e5m2}  # fmt: skip


def tensors(path, dev: str, keys: set | None = None):
    """(key, tensor on `dev`) for each tensor of one safetensors file (`keys`: only those), one at a time."""
    kind = torch.device(dev).type
    if kind == "cuda":
        yield from staged(path, dev, keys)
        return
    from safetensors import safe_open

    kw = {"backend": "pread"} if kind == "mps" else {}
    with safe_open(str(path), "pt", device=kind, **kw) as f:  # one device each: mps, cpu
        for key in f.keys():  # noqa: SIM118 (safe_open has no __iter__)
            if keys is None or key in keys:
                yield key, f.get_tensor(key)


def staging_gb(size: int, pin: bool) -> float:
    """What a staging buffer of `size` bytes holds in host RAM: pinned, torch's CachingHostAllocator rounds a block up
    to the next power of two (a 1.24 GB tensor takes 2 GiB) and keeps it cached after it is freed."""
    return (1 << max(size - 1, 0).bit_length() if pin else size) / 2**30


def staged(path, dev: str, keys: set | None = None):
    """`tensors` on CUDA (plan 6.3): each tensor is read from the file into one host buffer, pinned on CUDA, and
    copied to the device with non_blocking; the next read waits only for that copy. The buffer is allocated once per
    file at the largest tensor read (never a whole file, never grown: a grown pinned buffer would leave the old block
    cached), checked first against the host-RAM budget with the allocator's rounding (RunError with the contract's
    text). The pinned memory goes back at the next free()."""
    pin = torch.device(dev).type == "cuda"
    with open(path, "rb", buffering=0) as f:
        (n,) = struct.unpack("<Q", f.read(8))
        header = json.loads(f.read(n))
        header.pop("__metadata__", None)
        wanted = [(k, i) for k, i in header.items() if keys is None or k in keys]
        largest = max((i["data_offsets"][1] - i["data_offsets"][0] for _, i in wanted), default=0)
        buf, copied = None, None
        if wanted:
            limit = host_ram()["budget_gb"]
            if (need := (rss_gb() or 0) + staging_gb(largest, pin)) > limit:
                raise RunError(f"needs {need:.1f} GB of host RAM at its peak; host {config.HOST} has "
                               f"{limit:.1f} GB")  # fmt: skip
            buf = torch.empty(max(largest, 1), dtype=torch.uint8, pin_memory=pin)
        for key, info in wanted:
            start, end = info["data_offsets"]
            size = end - start
            if copied is not None:
                copied.synchronize()  # the previous copy has left the buffer
            view = memoryview(buf.numpy())[:size]
            f.seek(8 + n + start)
            got = 0
            while got < size:
                if not (k := f.readinto(view[got:])):
                    raise ValueError(f"{Path(path).name}: {key} is cut short")
                got += k
            out = torch.empty(size, dtype=torch.uint8, device=dev)
            out.copy_(buf[:size], non_blocking=pin)
            if pin:
                copied = torch.cuda.Event()
                copied.record()
            yield key, out.view(ST_DTYPES[info["dtype"]]).reshape(info["shape"])


def load(
    module: nn.Module,
    files: list,
    *,
    dev: str,
    dtype: torch.dtype | None,
    quantize=lambda name: False,
    rotated=lambda name: None,
    rename=lambda key: key,
    hook=None,
    bake=None,
    check=lambda: None,
    progress=lambda done, total: None,
) -> nn.Module:
    """Fill `module` (parameters on meta) from `files`. `quantize(module name)` picks the Linears stored int8
    (`rotated(module name)`: their ConvRot group, or None); a Linear whose stored weight is float8 becomes an
    Fp8Linear (from the headers: API 1.33, a community FP8 build; its weight and scale are never cast);
    `rename(key)` maps a checkpoint key to the module's name for it, or None to skip it (it is not read); `hook(name,
    tensor)` may replace a tensor on the device before the cast; `dtype` None keeps stored dtypes. `bake` (module name →
    [(A, B, s)], LoRA bake) merges W + sum s * (B @ A) in fp32 into those Linears' weights after the cast and before
    quantization. `check` runs before each tensor (a cancel point); `progress(done, total)` after each."""
    bake, baked = bake or {}, set()
    headers = {p: packs.header(p) for p in files}
    fp8 = {name.rpartition(".")[0] for h in headers.values() for k, t in h.items()
           if t["dtype"].startswith("F8") and len(t["shape"]) == 2
           and (name := rename(k)) is not None and name.endswith(".weight")}  # fmt: skip
    for parent_name, parent in list(module.named_modules()):
        for child_name, child in list(parent.named_children()):
            name = f"{parent_name}.{child_name}" if parent_name else child_name
            if type(child) is nn.Linear and name in fp8:
                if name in bake:
                    raise RunError(
                        f"LoRA bake is not available on fp8 weights ({name}): use adapter mode"
                    )
                setattr(parent, child_name, Fp8Linear(child))
            elif type(child) is nn.Linear and quantize(name):
                setattr(parent, child_name, Int8Linear(child, rotated(name)))
    modules = dict(module.named_modules())
    wanted = {p: {k for k in h if rename(k) is not None} for p, h in headers.items()}
    total = sum(map(len, wanted.values()))
    done, read = 0, 0
    for path in files:
        for key, tensor in tensors(path, dev, wanted[path]):
            check()
            read += tensor.numel() * tensor.element_size()
            name = rename(key)
            if name is not None:
                owner_name, _, attr = name.rpartition(".")
                owner = modules.get(owner_name)
                if owner is None:
                    raise KeyError(f"{path.name}: {key} has no place in {type(module).__name__}")
                if hook is not None:
                    tensor = hook(name, tensor)
                # an Fp8Linear's weight and scale stay as stored; a stored int8 build's scale is [out] bf16
                if isinstance(owner, Fp8Linear) and attr in ("weight", "scale"):
                    attr = "qweight" if attr == "weight" else "scale"
                    tensor = tensor if attr == "qweight" else tensor.float().reshape(-1, 1)
                elif isinstance(owner, Int8Linear) and attr == "scale":
                    tensor = tensor.reshape(-1).to(torch.bfloat16)
                elif dtype is not None and tensor.is_floating_point() and tensor.dtype != dtype:
                    tensor = tensor.to(dtype)
                if attr == "weight" and owner_name in bake:
                    merged = tensor.float()
                    for a, b, s in bake[owner_name]:
                        merged += s * (b.float() @ a.float())
                    tensor = merged.to(dtype or tensor.dtype)
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
    free()  # the last window's cache too: the component then holds about its declared size
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
    hook = getattr(model.pack.module, "tensor_hook", None)
    rotated = getattr(model.pack.module, "rotated", None)  # API 1.33: ConvRot groups by module
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
        dtype=dtype_of(spec.get("dtype", "bf16")),
        quantize=lambda name: packs.quantized(spec, precision, name),
        rotated=rotated(component, model) if rotated else (lambda name: None),
        rename=(lambda key: rename(component, key)) if rename else (lambda key: key),
        hook=hook(component, model) if hook else None,
        bake=bake,
        check=ctx.check,
        progress=progress,
    )
