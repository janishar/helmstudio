"""Loading model components for MLX (docs/plan/03-runtime.md section 6.3), imported only by the mlx env
(runner_service/envs/mlx).

A component is built by the pack (`build(component, model)`: an mlx.nn.Module whose arrays are lazy and never
evaluated), then filled from its safetensors files: `mx.load` (lazy: only the header is read; each tensor is read
into a Metal buffer when it is evaluated) → rename (the pack's `rename(component, key)`, None skips the key) →
convert (the pack's optional `convert(component, name, value)`, e.g. conv weights to channels-last) → cast (only
with an explicit component `dtype`; `stored` keeps each tensor's own dtype) → quantize (at int8 / int4, the 2-D
`.weight`s whose module matches the component's `quantize` globs: `mx.quantize`, group 64, one tensor at a time,
after the kept tensors are evaluated: quantizing can evict lazy buffers) → `nn.quantize` on exactly those Linears →
`load_weights(strict=True)`: a missing or extra key fails the load. Nothing is written anywhere.

Memory: `measure_gb` is MLX active + cache memory; `free` is gc + `mx.clear_cache()`; the Metal cache is capped at
the residency budget (RAM - RUNNER_RESERVE_GB), never raising a stricter cap already set."""

import gc
import logging

import mlx.core as mx
from mlx import nn

import config
from core import pack as packs
from core.graph import RunError
from core.residency import ram_gb

log = logging.getLogger("loader")
GROUP = 64  # quantization group size
BITS = {"int8": 8, "int4": 4}
DTYPES = {"bf16": mx.bfloat16, "fp16": mx.float16, "fp32": mx.float32, "stored": None}


def _cap_cache() -> None:
    cap = int(max(ram_gb() - config.RESERVE_GB, 1) * 2**30)
    previous = mx.set_cache_limit(cap)  # the only way to read the current limit
    if previous < cap:
        mx.set_cache_limit(previous)


_cap_cache()


def device() -> str:
    return "gpu" if mx.metal.is_available() else "cpu"


def free() -> None:
    """Give memory released by dropped modules back to the system (after the residency manager evicts)."""
    gc.collect()
    mx.clear_cache()


def measure_gb() -> float:
    """What MLX really holds: active buffers plus its buffer cache."""
    return (mx.get_active_memory() + mx.get_cache_memory()) / 2**30


def load(
    module: nn.Module,
    files: list,
    *,
    dtype=None,
    bits: int | None = None,
    quantize=lambda name: False,
    rename=lambda key: key,
    convert=lambda name, value: value,
    check=lambda: None,
) -> nn.Module:
    """Fill `module` from `files` (see the module doc). `quantize(module name)` picks the Linears stored at `bits`;
    `check` runs before each quantized tensor (a cancel point)."""
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
    module = model.pack.module.build(component, model)
    rename = getattr(model.pack.module, "rename", None)
    convert = getattr(model.pack.module, "convert", None)
    return load(
        module,
        files,
        dtype=DTYPES[spec.get("dtype", "bf16")],
        bits=BITS.get(precision),
        quantize=lambda name: packs.quantized(spec, precision, name),
        rename=(lambda key: rename(component, key)) if rename else (lambda key: key),
        convert=(lambda name, v: convert(component, name, v)) if convert else (lambda name, v: v),
        check=ctx.check,
    )
