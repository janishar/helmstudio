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

LoRA (contracts/README.md "LoRAs", MLX bake): bake only, no adapters. The rows of mode bake are normalised against
the family's lora block (core/lora.py: no key is silently dropped) and merged after quantization, module by module
and evaluated as it goes, in the old studio's order (ltx_core_mlx.loader.fuse_loras): delta = sum (B · s) @ A in
float32 (s = strength · alpha / rank); a quantized Linear is dequantized, + delta, requantized (so it carries float32
scales and biases), a float one is (W.f32 + delta) cast back to W's dtype. A baked component is its own residency
entry (core.pack.ModelRef.key).

Memory: `measure_gb` is the process's physical footprint (core.residency.footprint_gb); `free` is gc +
`mx.clear_cache()`; the Metal buffer cache is capped at CACHE_GB (never raising a stricter cap already set)."""

import gc
import logging

import mlx.core as mx
from mlx import nn

import config
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
    return host_device("mps" if device() == "gpu" else "cpu") | {
        "runtime": f"mlx {mx.__version__}"[:80]
    }


def free() -> None:
    """Give memory released by dropped modules back to the system (after the residency manager evicts)."""
    gc.collect()
    mx.clear_cache()


def measure_gb() -> float:
    """What the process really holds: its physical footprint (MLX active + cache + everything else), or MLX active
    + cache where the footprint cannot be read."""
    fp = footprint_gb()
    return fp if fp is not None else (mx.get_active_memory() + mx.get_cache_memory()) / 2**30


def linears(module: nn.Module) -> list[str]:
    """The names of the Linear layers of `module` a LoRA may patch."""
    return [n for n, m in module.named_modules() if isinstance(m, (nn.Linear, nn.QuantizedLinear))]


def activate(model, component: str, module: nn.Module, ctx) -> float:
    """MLX has no LoRA adapters (bake only: model.yaml `lora.modes` [bake], so studio refuses adapter rows)."""
    if model.adapters(component):
        raise RunError(f"{ctx.node}: LoRA adapters are not available on MLX; use mode bake")
    return 0.0


def extra_gb(module: nn.Module) -> float:
    """What the quantized Linears' scales and biases hold beyond size_gb's count of them at bf16: a baked one is
    re-quantized from float32 (`_bake`), so its scales and biases are float32."""
    extra = sum(m.scales.nbytes + m.biases.nbytes - 2 * (m.scales.size + m.biases.size)
                for _, m in module.named_modules() if isinstance(m, nn.QuantizedLinear))  # fmt: skip
    return extra / 2**30


def lora_tensors(model, use, path, shapes: dict) -> dict:
    """module → (A, B, alpha / rank) of one LoRA file (lazy arrays) normalised against the family's lora block.
    RunError with the contract's text when a key does not apply (each unapplied key is logged)."""
    from core import lora

    spec = model.pack.spec["lora"]
    block = {"modules": list(shapes), "renames": spec.get("renames", []), "ignore": spec.get("ignore", [])}  # fmt: skip
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
        factor = float(arrays[g["alpha"]].astype(mx.float32).reshape(-1)[0]) / a.shape[0] if g["alpha"] else 1.0  # fmt: skip
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
        f32 = mx.dequantize(w, scales=scales, biases=biases, group_size=GROUP, bits=bits).astype(
            mx.float32
        )
        q, scales, biases = mx.quantize(f32 + delta.astype(mx.float32), group_size=GROUP, bits=bits)
        weights |= {f"{layer}.weight": q, f"{layer}.scales": scales, f"{layer}.biases": biases}
        mx.eval(q, scales, biases)
    else:
        weights[f"{layer}.weight"] = (w.astype(mx.float32) + delta.astype(mx.float32)).astype(
            w.dtype
        )
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
            if (
                dtype is not None
                and mx.issubdtype(value.dtype, mx.floating)
                and value.dtype != dtype
            ):
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
        for layer in sorted(
            bake
        ):  # module by module: the old all-at-once build peaked +5.3 GiB (ltx note 11.7)
            check()
            _bake(weights, layer, bake[layer], bits, layer in layers)
    if bits:
        nn.quantize(module, group_size=GROUP, bits=bits,
                    class_predicate=lambda p, m: p in layers and isinstance(m, nn.Linear))  # fmt: skip
    module.load_weights(list(weights.items()), strict=True)
    mx.eval(module.parameters())
    gc.collect()
    return module


def load_component(model, component: str, ctx):
    """Load one component of a Model handle (core.pack.ModelRef) for ctx's run, reporting the phase
    `loading <component>` (underscores as spaces) under the node that needs it."""
    spec, precision = model.pack.component(component), model.precision_of(component)
    files = [p for p in model.files(component) if p.suffix == ".safetensors"]
    if not files:
        raise RunError(f"{component}: no weights for {model.owner(component)} in the models folder")
    ctx.progress(1, 1, f"loading {component.replace('_', ' ')}")
    if own := getattr(
        model.pack.module, "load_component", None
    ):  # a pack whose library loads its own format
        return own(component, model, ctx)
    module = model.pack.module.build(component, model)
    rename = getattr(model.pack.module, "rename", None)
    convert = getattr(model.pack.module, "convert", None)
    bake: dict[str, list] = {}
    if baked := model.baked(
        component
    ):  # the files first (an own LoRA is fetched): a bad one fails before loading
        lin = set(linears(module))  # name → (in_features, out_features)
        shapes = {
            n: (m.weight.shape[1], m.weight.shape[0]) for n, m in module.named_modules() if n in lin
        }
        for use in baked:
            for name, (a, b, factor) in lora_tensors(
                model, use, ctx.lora_path(use), shapes
            ).items():
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
        baking=lambda: ctx.progress(1, 1, "baking LoRAs"),
    )
