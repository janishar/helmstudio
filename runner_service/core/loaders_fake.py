"""The core loader interface for runtime fake (what core/loaders_<runtime>.py provides): a component "loads" as
the SHA-256 of its files, so the fake runner has resident entries to unload, pin and refuse deletes for, with no GPU
and no framework. The echo pack's tiny model loads through it."""

import gc
import hashlib

from core.graph import RunError
from core.residency import host_device, ram_gb


def device() -> str:
    return "cpu"


def total_gb() -> float:
    return ram_gb()  # device cpu: the budget is RAM − reserve


def device_info() -> dict:
    return host_device("cpu")


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
