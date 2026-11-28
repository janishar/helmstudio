"""Loading model components for MLX (docs/plan/03-runtime.md section 6.3), imported only by the mlx env
(runner_service/envs/mlx).

A component is built by the pack (`build(component, model)`: an mlx.nn.Module whose arrays are lazy and never
evaluated), then filled from its safetensors files: `mx.load` (lazy: only the header is read; each tensor is read
into a Metal buffer when it is evaluated) → rename (the pack's `rename(component, key)`, None skips the key) →
convert (the pack's optional `convert(component, name, value)`, e.g. conv weights to channels-last) → cast (only
with an explicit component `dtype`; `stored` keeps each tensor's own dtype) → quantize (at int8 / int4, the 2-D
`.weight`s whose module matches the component's `quantize` globs: `mx.quantize`, group 64, one tensor at a time,
after the kept tensors are evaluated: quantizing can evict lazy buffers) → `nn.quantize` on exactly those Linears →
`load_weights(strict=True)`: a missing or extra key fails the load. Nothing is written anywhere. A pack whose library
loads its own checkpoint format (llm: mlx-vlm) defines `load_component(component, model, ctx)` instead.

LoRA (contracts/README.md "LoRAs", MLX bake and adapter). The rows of mode bake are normalised against
the family's lora block (core/lora.py: no key is silently dropped) and merged after quantization, module by module
and evaluated as it goes, in the old studio's order (ltx_core_mlx.loader.fuse_loras): delta = sum (B · s) @ A in
float32 (s = strength · alpha / rank); a quantized Linear is dequantized, + delta, requantized with its scales and
biases kept at their dtype (mx.quantize gives float32 from the float32 sum, which made quantized_matmul promote the
layer to float32), a float one is (W.f32 + delta) cast back to W's dtype. A baked component is its own residency
entry (core.pack.ModelRef.key). Adapters (`activate`, the twin of loaders_torch's LoraLinear, as upstream's unfused
LoRAs: the vendored ltx_core_mlx lora_adapters.py, not imported) wrap a resident component's Linears in place:
y = base(x) + s * (x A^T) B^T, s a held float, so a strength change never reloads the base.

Memory: `measure_gb` is the process's physical footprint (core.residency.footprint_gb); `free` is gc +
`mx.clear_cache()`; the Metal buffer cache is capped at CACHE_GB (never raising a stricter cap already set)."""

import gc
import logging

import mlx.core as mx
from mlx import nn

import config
from core import child
from core import pack as packs
from core.graph import RunError
from core.residency import footprint_gb, host_device, ram_gb

log = logging.getLogger("loader")
GROUP = 64  # quantization group size
BITS = {"int8": 8, "int4": 4}
DTYPES = {"bf16": mx.bfloat16, "fp16": mx.float16, "fp32": mx.float32, "stored": None}


# GB of freed Metal buffers MLX may keep for reuse. At the default (the memory limit) one LTX VAE decode grew the cache
# to 27 GB beside a 9 GB active peak (footprint 49 GB, decode 6.8 s); at 0.5-8 GB it takes 1.9 s and the samplers'
# speed is unchanged (GOTCHAS.md, Measured). The stages' peak_gb are measured footprints at this cap.
CACHE_GB = 2


def _cap_cache() -> None:
    cap = int(min(CACHE_GB, max(ram_gb() - config.RESERVE_GB, 1)) * 2**30)
    previous = mx.set_cache_limit(cap)  # the only way to read the current limit
    if previous < cap:
        mx.set_cache_limit(previous)


_cap_cache()


def device() -> str:
    return "gpu" if mx.metal.is_available() else "cpu"


def total_gb() -> float:
    """The memory the budget is cut from: unified RAM."""
    return ram_gb()


def device_info() -> dict:
    """The hosts key's `device`: MLX's Metal is `mps`."""
    return host_device("mps" if device() == "gpu" else "cpu") | {"runtime": f"mlx {mx.__version__}"[:80]}


def free() -> None:
    """Give memory released by dropped modules back to the system (after the residency manager evicts)."""
    gc.collect()
    mx.clear_cache()


def measure_gb() -> float:
    """What the process really holds: its physical footprint (MLX active + cache + everything else), or MLX active
    + cache where the footprint cannot be read."""
    fp = footprint_gb()
    own = fp if fp is not None else (mx.get_active_memory() + mx.get_cache_memory()) / 2**30
    pid = child.LIVE["pid"]  # a resident server child (omni's llama-server) counts with the runner, as native
    return own + ((footprint_gb(pid) or 0.0) if pid else 0.0)


def linears(module: nn.Module) -> list[str]:
    """The names of the Linear layers of `module` a LoRA may patch."""
    return [n for n, m in module.named_modules() if isinstance(m, (nn.Linear, nn.QuantizedLinear))]


class LoraLinear:
    """Mixed into an adapted Linear / QuantizedLinear (`activate` swaps the layer's class to `Lora<class>` in place, so
    its arrays, names and attributes stay as they were): y = base(x) + sum s * (x A^T) B^T. The factors are the
    layer's parameters `loras` {sha256: {a [r, in], b [out, r]}}, so a compute-dtype cast of the module casts them
    too (LTXModel.set_compute_dtype); each s is a float in `_lora_s` (hidden from the parameters), so a strength
    change is an assignment. The class goes back when the last adapter goes."""

    def __call__(self, x: mx.array) -> mx.array:
        y = super().__call__(x)
        for sha, f in self["loras"].items():
            z = (x @ f["a"].T) @ f["b"].T  # upstream's order (lora_adapters.LoRAAdapter)
            y = y + (z * self._lora_s[sha]).astype(y.dtype)
        return y


_ADAPTED: dict[type, type] = {}


def _adapted(cls: type) -> type:
    if cls not in _ADAPTED:
        _ADAPTED[cls] = type(f"Lora{cls.__name__}", (LoraLinear, cls), {})
    return _ADAPTED[cls]


def _in_out(m: nn.Module) -> tuple[int, int]:
    w = m["weight"]
    packed = isinstance(m, nn.QuantizedLinear)  # bits per element packed into uint32
    return (w.shape[1] * 32 // m.bits if packed else w.shape[1]), w.shape[0]


def activate(model, component: str, module: nn.Module, ctx) -> float:
    """Make `module` carry exactly `model`'s adapter LoRAs for `component`: detach the extra, attach the missing
    (read from the LoRA's file, fetched for an own one) in the module's compute dtype (else the file's), set every
    s to strength * alpha / rank. Returns the adapters' GB. The base arrays are never written."""
    want = model.adapters(component)
    state = module.__dict__.setdefault("_lora_adapters", {})  # sha256 → {module name: factor}
    if not want and not state:
        return 0.0  # nothing to walk (every run of a model without adapters)
    lins = dict(module.named_modules())
    for sha in [s for s in state if s not in want]:
        for name in state.pop(sha):
            m = lins[name]
            del m["loras"][sha], m._lora_s[sha]
            if not m["loras"]:  # the plain Linear again, exactly as it was
                del m["loras"], m["_lora_s"]
                m.__class__ = type(m).__bases__[1]  # Lora<cls>'s bases: (LoraLinear, cls)
    shapes = {n: _in_out(lins[n]) for n in linears(module)}
    target = getattr(module, "compute_dtype", None)
    new = []
    for sha, (use, _) in want.items():
        if sha in state:
            continue
        ctx.check()
        # a bad file fails before any attach
        tensors = lora_tensors(model, use, ctx.lora_path(use), shapes)
        state[sha] = {}
        for name, (a, b, factor) in tensors.items():
            m = lins[name]
            if not isinstance(m, LoraLinear):
                m.__class__ = _adapted(type(m))
                m["loras"], m["_lora_s"] = {}, {}
            dtype = target or a.dtype
            m["loras"][sha] = {"a": a.astype(dtype), "b": b.astype(dtype)}
            new += m["loras"][sha].values()
            state[sha][name] = factor
    mx.eval(new)
    total = 0
    for sha, (_, strength) in want.items():
        for name, factor in state[sha].items():
            m = lins[name]
            m._lora_s[sha] = strength * factor
            total += sum(v.nbytes for v in m["loras"][sha].values())
    return total / 2**30


def extra_gb(module: nn.Module) -> float:
    """What the quantized Linears' scales and biases hold beyond size_gb's count of them at bf16 (a Linear quantized
    from a float32 weight carries float32 ones; `_bake` keeps the loaded dtype)."""
    extra = sum(m.scales.nbytes + m.biases.nbytes - 2 * (m.scales.size + m.biases.size)
                for _, m in module.named_modules() if isinstance(m, nn.QuantizedLinear))  # fmt: skip
    return extra / 2**30


def lora_tensors(model, use, path, shapes: dict) -> dict:
    """module → (A, B, alpha / rank) of one LoRA file (lazy arrays) normalised against the family's lora block.
    RunError with the contract's text when a key does not apply (each unapplied key is logged)."""
    from core import lora

    spec = model.pack.spec["lora"]
    block = {"modules": list(shapes), "renames": spec.get("renames", []), "ignore": spec.get("ignore", [])}
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
    arrays = mx.load(str(path))
    out = {}
    for module, g in res["groups"].items():
        a, b = arrays[g["A"]], arrays[g["B"]]
        factor = float(arrays[g["alpha"]].astype(mx.float32).reshape(-1)[0]) / a.shape[0] if g["alpha"] else 1.0
        out[module] = (a, b, factor)
    return out


def _bake(weights: dict, layer: str, terms: list, bits: int | None, quantized: bool) -> None:
    """fuse_loras._prepare_deltas + _fuse_delta_with_{quantized,float} for one module, evaluated."""
    if f"{layer}.weight" not in weights:
        raise KeyError(f"LoRA module {layer} has no weight")
    deltas = [mx.matmul(b.astype(mx.float32) * s, a.astype(mx.float32)) for a, b, s in terms]
    delta = deltas[0] if len(deltas) == 1 else mx.sum(mx.stack(deltas, axis=0), axis=0)
    w = weights[f"{layer}.weight"]
    if quantized:
        scales, biases = weights[f"{layer}.scales"], weights[f"{layer}.biases"]
        f32 = mx.dequantize(w, scales=scales, biases=biases, group_size=GROUP, bits=bits).astype(mx.float32)
        q, s, b = mx.quantize(f32 + delta.astype(mx.float32), group_size=GROUP, bits=bits)
        # fuse_loras._fuse_delta_with_quantized (f3c5bad): the loaded scales / biases dtype kept
        s, b = s.astype(scales.dtype), b.astype(biases.dtype)
        weights |= {f"{layer}.weight": q, f"{layer}.scales": s, f"{layer}.biases": b}
        mx.eval(q, s, b)
    else:
        weights[f"{layer}.weight"] = (w.astype(mx.float32) + delta.astype(mx.float32)).astype(w.dtype)
        mx.eval(weights[f"{layer}.weight"])


def load(
    module: nn.Module,
    files: list,
    *,
    dtype=None,
    bits: int | None = None,
    quantize=lambda name: False,
    rename=lambda key: key,
    convert=lambda name, value: value,
    bake=None,
    check=lambda: None,
    baking=lambda: None,
) -> nn.Module:
    """Fill `module` from `files` (see the module doc). `quantize(module name)` picks the Linears stored at `bits`;
    `bake` (module name → [(A, B, s)]) merges LoRA deltas after quantization (`baking()` first); `check` runs before
    each quantized tensor and each baked module (a cancel point)."""
    weights: dict[str, mx.array] = {}
    for path in files:
        for key, value in mx.load(str(path)).items():
            name = rename(key)
            if name is None:
                continue
            if name in weights:
                raise KeyError(f"{path.name}: {key} gives {name} a second time")
            value = convert(name, value)
            if dtype is not None and mx.issubdtype(value.dtype, mx.floating) and value.dtype != dtype:
                value = value.astype(dtype)
            weights[name] = value
    layers = set()
    if bits:
        layers = {
            n.removesuffix(".weight")
            for n, v in weights.items()
            if n.endswith(".weight") and v.ndim == 2 and v.shape[-1] % GROUP == 0
            and quantize(n.removesuffix(".weight"))
        }  # fmt: skip
        mx.eval(*(v for n, v in weights.items() if n.removesuffix(".weight") not in layers))
        for layer in sorted(layers):
            check()
            w = weights.pop(f"{layer}.weight")
            q, scales, biases = mx.quantize(w, group_size=GROUP, bits=bits)
            mx.eval(q, scales, biases)
            weights |= {f"{layer}.weight": q, f"{layer}.scales": scales, f"{layer}.biases": biases}
            del w
    if bake:
        baking()
        for layer in sorted(bake):  # module by module: the old all-at-once build peaked +5.3 GiB (ltx note 11.7)
            check()
            _bake(weights, layer, bake[layer], bits, layer in layers)
    if bits:
        nn.quantize(module, group_size=GROUP, bits=bits,
                    class_predicate=lambda p, m: p in layers and isinstance(m, nn.Linear))  # fmt: skip
    module.load_weights(list(weights.items()), strict=True)
    mx.eval(module.parameters())
    gc.collect()
    return module


def prefused(model, baked) -> bool:
    """Whether the baked rows are only the pack's pre-fused LoRA files (LTX ltx/distill: the phase `fusing LoRA`,
    contracts README Media inputs), by file name."""
    files = {p.rsplit("/", 1)[-1].removesuffix(".safetensors") for p in
             (x["file"] for x in (model.pack.spec.get("lora") or {}).get("prefused", []))}  # fmt: skip
    return bool(baked) and all(u.name in files for u in baked)


def load_component(model, component: str, ctx):
    """Load one component of a Model handle (core.pack.ModelRef) for ctx's run, reporting the phase
    `loading <component>` (underscores as spaces) under the node that needs it."""
    spec, precision = model.pack.component(component), model.precision_of(component)
    files = [p for p in model.files(component) if p.suffix in (".safetensors", ".gguf")]  # GGUF: omni's server
    if not files:
        raise RunError(f"{component}: no weights for {model.owner(component)} in the models folder")
    ctx.progress(1, 1, f"loading {component.replace('_', ' ')}")
    if own := getattr(model.pack.module, "load_component", None):  # a pack whose library loads its own format
        return own(component, model, ctx)
    module = model.pack.module.build(component, model)
    rename = getattr(model.pack.module, "rename", None)
    convert = getattr(model.pack.module, "convert", None)
    bake: dict[str, list] = {}
    if baked := model.baked(component):  # the files first (an own LoRA is fetched): a bad one fails before loading
        lin = set(linears(module))  # name → (in_features, out_features)
        shapes = {n: (m.weight.shape[1], m.weight.shape[0]) for n, m in module.named_modules() if n in lin}
        for use in baked:
            for name, (a, b, factor) in lora_tensors(model, use, ctx.lora_path(use), shapes).items():
                bake.setdefault(name, []).append((a, b, use.strength * factor))
    return load(
        module,
        files,
        dtype=DTYPES[spec.get("dtype", "bf16")],
        bits=BITS.get(precision),
        quantize=lambda name: packs.quantized(spec, precision, name),
        rename=(lambda key: rename(component, key)) if rename else (lambda key: key),
        convert=(lambda name, v: convert(component, name, v)) if convert else (lambda name, v: v),
        bake=bake,
        check=ctx.check,
        baking=lambda: ctx.progress(1, 1, "fusing LoRA" if prefused(model, baked) else "baking LoRAs"),
    )
