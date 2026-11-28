"""The native runtime's loader hooks (contracts/README.md "The native runtime"), imported by the env `native`: a
stage runs a child process (core.child) that loads its own weights, so the runner loads nothing. Only what the
residency manager and the hosts key need: the device (Metal `mps`, else `cuda`), the memory the budget is cut from (unified
RAM), and the measure, which counts the live child (its phys_footprint) with the runner's own. No torch, no MLX."""

import sys
from pathlib import Path

import config
from core import child
from core.residency import footprint_gb, host_device, ram_gb

H3C = Path(__file__).resolve().parent.parent / "vendor/h3c"


def device() -> str:
    """Metal on a Mac, else CUDA (the gpu-host image's zonos3-server). ponytail: the budget stays host RAM
    (total_gb), not VRAM; read nvidia-smi if a CUDA native pack ever gets near it."""
    return "mps" if sys.platform == "darwin" else "cuda"


def total_gb() -> float:
    return ram_gb()


def host_ram() -> dict:
    """The hosts key's `host_ram` on a CUDA host (as loaders_torch): RAM - RUNNER_RAM_RESERVE_GB, and what the runner
    and its live child hold."""
    return {
        "budget_gb": round(ram_gb() - config.RAM_RESERVE_GB, 1),
        "used_gb": round(measure_gb() or 0, 1),
    }


def device_info() -> dict:
    """The hosts key's `device`, with the vendored binary's pinned commit as the runtime."""
    try:
        commit = next(ln.split()[1] for ln in (H3C / "VERSION").read_text().splitlines() if ln.startswith("commit:"))
    except OSError, StopIteration, IndexError:
        commit = "unknown"
    return host_device(device()) | {"runtime": f"h3 {commit[:7]}"}


def free() -> None:
    """Nothing to give back: the child's memory goes when it exits."""


def measure_gb() -> float | None:
    """The runner's footprint plus the live child's (0 when none runs)."""
    own = footprint_gb()
    if own is None:
        return None
    pid = child.LIVE["pid"]
    return own + ((footprint_gb(pid) or 0.0) if pid else 0.0)
