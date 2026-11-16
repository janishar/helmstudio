"""The core loader interface for runtime fake (what core/loaders_<runtime>.py provides): a component "loads" as
the SHA-256 of its files, so the fake runner has resident entries to unload, pin and refuse deletes for, with no GPU
and no framework. fake.py registers it as core.loaders_fake while core has none of its own."""

import gc
import hashlib

from core.graph import RunError


def device() -> str:
    return "cpu"


def free() -> None:
    gc.collect()


def measure_gb() -> None:
    return None  # nothing measured: the entries' declared gb count


def load_component(model, component: str, ctx) -> dict:
    files = model.files(component)
    if not files:
        raise RunError(f"{component}: no weights for {model.owner(component)} in the models folder")
    return {
        "component": component,
        "sha256": [hashlib.sha256(p.read_bytes()).hexdigest() for p in files],
    }


def activate(model, component: str, module, ctx) -> float:
    return 0.0  # no adapters: echo's LoRA stack only names its rows
