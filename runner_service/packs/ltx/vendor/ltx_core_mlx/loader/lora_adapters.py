"""Unfused LoRAs: apply the low-rank update at run time instead of fusing it into the weights.

:func:`~ltx_core_mlx.loader.fuse_loras.apply_loras` fuses ``strength * B @ A`` into each targeted
weight. On a quantized DiT (the q8 / q4 packs) that means dequantize, add, re-quantize, and the
re-quantization error is of the same order as the update of a small LoRA: for the LTX-2.5 IC-LoRAs
the update is 0.25-3.6 % of the weight's norm, and re-quantizing to int8 / group 64 loses most of it.

:func:`attach_loras` leaves the weights alone. Every targeted linear layer becomes an adapter that
computes ``base(x) + (x @ A^T) @ B^T`` (strength folded into ``B``; the ranks of several LoRAs on
one layer are stacked), and :meth:`AttachedLoras.detach` puts the original modules back in place.

The adapter is the original layer with two more parameters: it shares the layer's arrays and
attributes and its class subclasses the layer's class, so the parameter names do not change
(``attn1.to_q.weight`` / ``.scales`` / ``.biases`` stay where they were; only ``.lora_a`` and
``.lora_b`` are added). Code that walks the state dict therefore keeps working on an adapted model:
an in-place fusion still finds and re-quantizes ``to_q.weight``, ``load_weights`` still matches every
key, and ``LTXModel.set_compute_dtype`` casts the factors along with the other float parameters of
the attention / feed-forward module that holds them.

``LTX2_LORA_MODE=unfused`` makes the pipelines use this instead of the in-place fusion (see
:func:`lora_mode_from_env`); unset or ``fused`` keeps the fusion.
"""

from __future__ import annotations

import logging
import os
import weakref
from collections.abc import Callable, Sequence
from typing import Any, NamedTuple

import mlx.core as mx
import mlx.nn as nn

from ltx_core_mlx.loader.primitives import LoraStateDictWithStrength

logger = logging.getLogger(__name__)

LORA_MODE_ENV = "LTX2_LORA_MODE"
_LORA_MODES = {"": "fused", "fused": "fused", "unfused": "unfused"}
_LORA_KEYS = ("lora_a", "lora_b")
_A_SUFFIX = ".lora_A.weight"
_B_SUFFIX = ".lora_B.weight"


def lora_mode_from_env() -> str:
    """Parse ``LTX2_LORA_MODE``.

    Returns:
        ``"fused"`` (unset or ``fused``: LoRAs are fused into the weights, the default) or
        ``"unfused"`` (LoRAs are attached as run-time adapters, see :func:`attach_loras`).

    Raises:
        ValueError: On any other value.
    """
    value = os.environ.get(LORA_MODE_ENV, "").strip().lower()
    if value not in _LORA_MODES:
        raise ValueError(f"{LORA_MODE_ENV}={value!r}: expected fused or unfused")
    return _LORA_MODES[value]


class LoRAAdapter(nn.Module):
    """Mixin for a linear layer that adds a low-rank update to its output.

    Not instantiated directly: :func:`attach_loras` turns a layer into an instance of
    ``LoRA<LayerClass>`` (this mixin plus the layer's class) that shares the layer's parameters.

    Parameters added to the layer:
        lora_a: ``(rank, in_features)``.
        lora_b: ``(out_features, rank)``, with the LoRA strength folded in.
    """

    def __call__(self, x: mx.array) -> mx.array:
        base_call: Any = super().__call__  # the wrapped layer class's forward (Linear / QuantizedLinear)
        y = base_call(x)
        z = (x @ self["lora_a"].T) @ self["lora_b"].T
        return y + z.astype(y.dtype)


_ADAPTER_CLASSES: dict[type, type] = {}


def _adapter_class(layer_cls: type) -> type:
    if issubclass(layer_cls, LoRAAdapter):
        return layer_cls
    cls = _ADAPTER_CLASSES.get(layer_cls)
    if cls is None:
        cls = type(f"LoRA{layer_cls.__name__}", (LoRAAdapter, layer_cls), {})
        _ADAPTER_CLASSES[layer_cls] = cls
    return cls


def _in_out_features(layer: nn.Module) -> tuple[int, int]:
    weight = layer["weight"]
    if isinstance(layer, nn.QuantizedLinear):
        return weight.shape[1] * 32 // layer.bits, weight.shape[0]
    return weight.shape[1], weight.shape[0]


def _child(parent: Any, key: str) -> Any:
    if isinstance(parent, list):
        return parent[int(key)] if key.isdigit() and int(key) < len(parent) else None
    if isinstance(parent, dict):
        return parent.get(key)
    return None


def _resolve(model: nn.Module, path: str) -> tuple[Any, str, Any] | None:
    """``(parent, key, module)`` for a dotted module path, or ``None`` if it does not exist."""
    parent: Any = None
    node: Any = model
    key = ""
    for key in path.split("."):
        parent, node = node, _child(node, key)
        if node is None:
            return None
    return parent, key, node


def _set_child(parent: Any, key: str, module: nn.Module) -> None:
    if isinstance(parent, list):
        parent[int(key)] = module
    else:
        parent[key] = module


class _Replaced(NamedTuple):
    parent: Callable[[], Any]  # weak reference (strong only for a list parent)
    key: str
    previous: nn.Module  # the replaced layer, emptied of its base parameters while detached from the model
    adapter: Callable[[], nn.Module | None]  # weak reference


def _ref(obj: Any) -> Callable[[], Any]:
    try:
        return weakref.ref(obj)
    except TypeError:  # a plain list parent cannot be weakly referenced
        return lambda: obj


class AttachedLoras:
    """The layers :func:`attach_loras` replaced, so :meth:`detach` can put them back.

    The handle holds no model memory: it refers weakly to the model's modules, and the replaced layers
    it keeps are emptied of their base parameters (the adapter has them; :meth:`detach` hands them back).
    A model freed while LoRAs are attached is therefore really freed, and detaching afterwards is a no-op.
    """

    def __init__(self) -> None:
        self._replaced: list[_Replaced] = []
        self.skipped: list[str] = []

    def __len__(self) -> int:
        return len(self._replaced)

    def detach(self) -> int:
        """Put back the layers that were there before the attach.

        Each restored layer receives the adapter's current base parameters: the very arrays it had,
        unless they were replaced since the attach (e.g. by an in-place fusion, which is kept).

        Returns:
            How many layers were restored (0 when already detached or when the model was freed).

        Raises:
            RuntimeError: A layer was adapted again after this attach and not detached first.
        """
        restored = 0
        for entry in reversed(self._replaced):
            parent, adapter = entry.parent(), entry.adapter()
            if parent is None or adapter is None:  # the model was freed
                continue
            if _child(parent, entry.key) is not adapter:
                raise RuntimeError(
                    f"LoRA adapter at '{entry.key}' was replaced after it was attached; detach in reverse order"
                )
            for name, value in adapter.items():
                if name not in _LORA_KEYS:
                    entry.previous[name] = value
            _set_child(parent, entry.key, entry.previous)
            restored += 1
        self._replaced = []
        return restored


def attach_loras(
    model: nn.Module,
    lora_sd_and_strengths: Sequence[LoraStateDictWithStrength],
    dtype: mx.Dtype | None = None,
) -> AttachedLoras:
    """Attach LoRAs to ``model`` as run-time adapters instead of fusing them.

    Keys follow :func:`~ltx_core_mlx.loader.fuse_loras.apply_loras`: ``<path>.lora_A.weight`` and
    ``<path>.lora_B.weight`` target the layer at ``<path>`` (after the caller's key renaming). As in
    ``apply_loras``, a LoRA key whose layer does not exist in ``model`` is ignored (listed in
    ``skipped``). A layer that already carries an adapter gets one more: the new ranks are stacked
    after the existing ones, and :meth:`AttachedLoras.detach` restores the previous adapter.

    The factors are stored in ``dtype``; by default the model's ``compute_dtype`` when it has one,
    else the dtype of the layer's existing factors, else the LoRA file's dtype. ``B`` is multiplied
    by the strength in float32 before that cast. Without a compute dtype the DiT's activations are
    float32 and the matmuls promote the (bf16) factors to them.

    Args:
        model: The module to adapt (an ``LTXModel``, or any module tree of Linear / QuantizedLinear layers).
        lora_sd_and_strengths: ``(state_dict, strength)`` pairs, as for ``apply_loras``.
        dtype: Storage dtype of the factors (see above).

    Returns:
        A handle whose :meth:`~AttachedLoras.detach` removes the adapters again.

    Raises:
        TypeError: A LoRA targets a layer that is not a Linear / QuantizedLinear.
        ValueError: A LoRA's factors do not match the shape of the layer they target.
    """
    factors: dict[str, list[tuple[mx.array, mx.array]]] = {}
    for lora in lora_sd_and_strengths:
        sd = lora.state_dict.sd
        for key_a in sd:
            if not key_a.endswith(_A_SUFFIX):
                continue
            prefix = key_a[: -len(_A_SUFFIX)]
            key_b = f"{prefix}{_B_SUFFIX}"
            if key_b not in sd:
                continue
            factors.setdefault(prefix, []).append((sd[key_a], sd[key_b].astype(mx.float32) * lora.strength))

    model_dtype = getattr(model, "compute_dtype", None)
    handle = AttachedLoras()
    new_factors: list[mx.array] = []
    for prefix, pairs in factors.items():
        found = _resolve(model, prefix)
        if found is None or not isinstance(found[2], nn.Module):  # apply_loras only matches ``<path>.weight``
            handle.skipped.append(prefix)
            continue
        parent, key, layer = found
        if not isinstance(layer, (nn.Linear, nn.QuantizedLinear)):
            raise TypeError(f"LoRA targets '{prefix}', a {type(layer).__name__}; only Linear layers can be adapted")
        in_features, out_features = _in_out_features(layer)
        a_list = [a.astype(mx.float32) for a, _ in pairs]
        b_list = [b.astype(mx.float32) for _, b in pairs]
        for a, b in zip(a_list, b_list, strict=True):
            if a.shape[1] != in_features or b.shape[0] != out_features or a.shape[0] != b.shape[1]:
                raise ValueError(
                    f"LoRA factors for '{prefix}' have shapes A{tuple(a.shape)} / B{tuple(b.shape)}; "
                    f"the layer is ({out_features}, {in_features})"
                )
        if isinstance(layer, LoRAAdapter):
            target = dtype or model_dtype or layer["lora_a"].dtype
            a_list.insert(0, layer["lora_a"].astype(mx.float32))
            b_list.insert(0, layer["lora_b"].astype(mx.float32))
        else:
            target = dtype or model_dtype or pairs[0][0].dtype

        adapter_cls = _adapter_class(type(layer))
        adapter = adapter_cls.__new__(adapter_cls)
        adapter.__dict__.update(layer.__dict__)
        dict.update(adapter, layer)  # same parameter arrays as the layer
        adapter["lora_a"] = mx.concatenate(a_list, axis=0).astype(target)
        adapter["lora_b"] = mx.concatenate(b_list, axis=1).astype(target)
        new_factors += [adapter["lora_a"], adapter["lora_b"]]
        _set_child(parent, key, adapter)
        for name in [n for n in layer if n not in _LORA_KEYS]:
            del layer[name]  # the adapter holds them now; detach() gives them back
        handle._replaced.append(_Replaced(_ref(parent), key, layer, _ref(adapter)))

    mx.eval(new_factors)
    if handle.skipped:
        logger.info("LoRA keys with no matching layer (ignored, as when fusing): %d", len(handle.skipped))
    return handle
