"""The residency manager (docs/plan/03-runtime.md section 6.2): which model components are loaded, within a memory
budget of the device's memory (physical RAM; VRAM on CUDA) minus RUNNER_RESERVE_GB.

- An entry is one component keyed by (model id, owning variant, component, precision[, baked LoRAs]): one file
  set at one precision, with LoRAs merged at load (mode bake) its own entry. Adapters live on an entry's module and
  are counted in its size, so they go when it is evicted. Its size is computed from the target dtype (pack.size_gb), not from header bytes.
- Admission, before a stage runs: need = the stage's components not yet loaded + its peak_gb. When short, cached
  node results are dropped first, then the least recently used components, those the rest of the run does not
  need before those it does; never one the stage uses. A need that cannot fit even then fails the run at once,
  with numbers.
  Within each of those two groups a baked set the run does not use goes before plain components (the
  hand-over, contracts/README.md "Two envs on one host").
- A run's `cap` (cmd.run.execute `budget_gb`: what the host's other envs leave) lowers the budget for that run.
- What the device measures beyond the entries' sizes (`measure`: caches, the runtime) counts against the budget too,
  after the device cache was freed (contracts/README.md, "Measured memory above the declared sizes").
- After a run: a plain component whose baked set the run used is unloaded (the two never stay together); then
  unload (everything), keep_warm (the default: stay until memory is needed or RUNNER_IDLE_UNLOAD_S passes) or
  keep_pinned (only pinned components stay); then the device cache is freed, after every run.
- Pins (contracts/README.md "Hosts admin"): the host_settings key's `pins` (entries by identity, `pin_refs`), else
  RUNNER_PIN (component names). A pinned entry is never an idle or after-run victim (keep_pinned keeps it) and is an
  admission victim only as a last resort (after every unpinned one, when the stage would otherwise not fit: a pin
  never fails a run). The pin stays set: the component is pinned again when it is next loaded.
- `unload_named`: studio's dispatcher names entries to unload (runner.unload); it names a pinned one only as a last
  resort, and an admin unload names them freely, so every named entry held and not in use goes.

Every method but `state` runs on the GPU thread; `state` (read by the hosts heartbeat) takes the lock."""

import ctypes
import functools
import logging
import os
import subprocess
import sys
import threading
import time
from collections import OrderedDict
from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path

from core.graph import RunError

log = logging.getLogger("residency")

POLICIES = ("unload", "keep_warm", "keep_pinned")


def ram_gb() -> float:
    """Physical memory in GB (2^30 bytes)."""
    return os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES") / 2**30


# rusage_info_v4: a 16-byte uuid, then u64 fields; phys_footprint is the 8th
_RUSAGE = (ctypes.c_uint64 * 64)()


def footprint_gb() -> float | None:
    """This process's physical footprint (macOS phys_footprint, what Activity Monitor and jetsam count: Metal
    buffers and the allocators' caches included) in GB; None where the call does not exist."""
    if sys.platform != "darwin":
        return None
    if ctypes.CDLL(None).proc_pid_rusage(os.getpid(), 4, ctypes.byref(_RUSAGE)) != 0:
        return None
    return _RUSAGE[2 + 7] / 2**30


def rss_gb() -> float | None:
    """This process's resident set now (Linux /proc), else its footprint (macOS); None where neither exists."""
    try:
        pages = int(Path("/proc/self/statm").read_text().split()[1])
    except OSError:
        return footprint_gb()
    return pages * os.sysconf("SC_PAGE_SIZE") / 2**30


@functools.cache
def _cpu_name() -> str:
    try:
        if sys.platform == "darwin":
            out = subprocess.run(["sysctl", "-n", "machdep.cpu.brand_string"], capture_output=True, text=True,
                                 timeout=5, check=False).stdout  # fmt: skip
        else:
            out = next((ln.split(":", 1)[1] for ln in Path("/proc/cpuinfo").read_text().splitlines()
                        if ln.startswith("model name")), "")  # fmt: skip
    except OSError:
        out = ""
    return out.strip()[:80] or "cpu"


def host_device(kind: str) -> dict:
    """The hosts key's `device` without a device of its own memory (mps: unified RAM; cpu): the chip, RAM."""
    return {"kind": kind, "name": _cpu_name(), "memory_total_gb": round(ram_gb(), 1)}


class Residency:
    def __init__(
        self,
        budget_gb: float,
        *,
        free: Callable[[], None] = lambda: None,
        measure: Callable[[], float | None] = lambda: None,
        pins: set[str] = frozenset(),
    ) -> None:
        self.budget_gb, self.free, self.measure, self.pins = budget_gb, free, measure, pins
        self.total_gb: float | None = None  # what budget_gb is cut from (CUDA: VRAM); None = RAM
        self.pin_refs: set[tuple] | None = (
            None  # host_settings pins of this env (ident), replacing `pins`
        )
        # key → [gb, value, loras, last_used (epoch s)]; LRU first
        self.entries: OrderedDict[tuple, list] = OrderedDict()
        self.in_use: set[tuple] = set()  # the running stage's components: not evictable
        self.used: set[tuple] = set()  # every component the running run has used
        self.cap: float | None = None  # the running run's budget_gb, if studio sent one
        self.on_short: list[Callable[[], None]] = []  # drop cached node results (graph.Cache.clear)
        self.lock = threading.Lock()

    def pinned(self, key: tuple) -> bool:
        """Whether a pin names the entry `key`: a host_settings pin, else a RUNNER_PIN component name."""
        if self.pin_refs is None:
            return key[2] in self.pins
        entry = self.entries.get(key)
        return ident(_name(key, entry[2] if entry else None)) in self.pin_refs

    @property
    def used_gb(self) -> float:
        return sum(e[0] for e in self.entries.values())

    @property
    def limit(self) -> float:
        """The budget now: the runner's own, capped by the running run's budget_gb."""
        return self.budget_gb if self.cap is None else min(self.budget_gb, self.cap)

    def admit(self, needs: dict[tuple, float], peak_gb: float, later: set[tuple]) -> None:
        """Before a stage: `needs` = its components (key → GB), `later` = components the rest of the run
        uses. Makes room for the missing ones plus the peak; RunError when they cannot fit."""
        self.in_use = set(needs)
        missing = sum(gb for k, gb in needs.items() if k not in self.entries)
        self._make_room(missing + peak_gb, later)

    def get(
        self, key: tuple, gb: float, transient_gb: float, load: Callable[[], object], loras=None
    ):
        """The loaded component `key`, loading it (with room for `gb` + the load-time transient) if needed. `loras`:
        what the hosts value lists as its baked LoRAs (names may change; the key does not)."""
        self.used.add(key)
        if key in self.entries:
            self.entries.move_to_end(key)
            self.entries[key][2:4] = [loras, time.time()]
            return self.entries[key][1]
        self.in_use.add(key)
        self._make_room(gb + transient_gb, set())
        t = time.monotonic()
        value = load()
        log.info(
            "loaded %s (%.1f GB est.) in %.1f s", "/".join(map(str, key)), gb, time.monotonic() - t
        )
        with self.lock:
            self.entries[key] = [gb, value, loras, time.time()]
        return value

    def grow(self, key: tuple, gb: float) -> None:
        """Before memory is added to the resident entry `key` (adapters): make room for its growth to `gb`
        (RunError when it cannot fit), then count it at `gb`."""
        if (extra := gb - self.entries[key][0]) > 0:
            self._make_room(extra, set())
        self.resize(key, gb)

    def resize(self, key: tuple, gb: float) -> None:
        """A resident entry's size changed (adapters attached to or detached from it: they live and go with it)."""
        with self.lock:
            if key in self.entries:
                self.entries[key][0] = gb

    def done(self) -> None:
        """The run ended: nothing is in use, no cap."""
        self.in_use, self.used, self.cap = set(), set(), None

    def _over(self) -> float:
        """GB the device holds beyond the entries' declared sizes (its cache, the runtime, cached node results), as
        it measures them now; 0 without a measure."""
        measured = self.measure()
        return 0.0 if measured is None else max(0.0, measured - self.used_gb)

    def _make_room(self, need: float, later: set[tuple]) -> None:
        """Room for `need` GB more: the entries plus what the device measures beyond them stay within the limit.
        When short, cached node results and the device cache go first (no weights), then components."""
        if self.used_gb + self._over() + need <= self.limit:
            return
        for drop in self.on_short:
            drop()
        self.free()
        over = (
            self._over()
        )  # ponytail: assumed fixed while evicting (weights go back on free, the rest stays)
        lru = list(self.entries)

        def order(
            k: tuple,
        ) -> (
            tuple
        ):  # unpinned first (a pin is the last resort); not needed later; a baked set unused; LRU
            return self.pinned(k), k in later, not (len(k) > 4 and k not in self.used), lru.index(k)

        evicted = False
        for k in sorted((k for k in lru if k not in self.in_use), key=order):
            if self.used_gb + over + need <= self.limit:
                break
            self._evict(k)
            evicted = True
        if evicted:
            self.free()
        if self.used_gb + over + need > self.limit:
            raise RunError(
                f"does not fit in memory: needs {need:.1f} GB more, {self.used_gb + over:.1f} GB in use "
                f"by this run, budget {self.limit:.1f} GB"
            )

    def _evict(self, key: tuple) -> None:
        with self.lock:
            entry = self.entries.pop(key)
        log.info("unloaded %s", "/".join(map(str, key)))
        entry.clear()  # drop the reference to the loaded module here, so free() can return its memory

    def release(self, keys: set[tuple]) -> None:
        """Unload these components now (a stage's model.yaml `unload_after`), pinned ones excepted."""
        victims = [k for k in self.entries if k in keys and not self.pinned(k)]
        for k in victims:
            self.in_use.discard(k)
            self._evict(k)
        if victims:
            self.free()

    def unload(self, keep_pins: bool = False) -> list[dict]:
        """Unload every component (keep_pins: but pinned ones) and give the memory back; returns the
        unloaded entries' names (runner.unload `freed`)."""
        victims = [k for k in self.entries if not (keep_pins and self.pinned(k))]
        freed = [_name(k, self.entries[k][2]) for k in victims]
        for k in victims:
            self._evict(k)
        if victims:
            self.free()
        return freed

    def unload_named(self, names: list[dict]) -> tuple[list[dict], float]:
        """runner.unload `components`: unload exactly the named entries held and not in use, pinned ones too (the
        dispatcher names a pin only as a last resort); returns (their names, their GB)."""
        want = {ident(n) for n in names}
        victims = [
            k
            for k, e in self.entries.items()
            if ident(_name(k, e[2])) in want and k not in self.in_use
        ]
        freed = [_name(k, self.entries[k][2]) for k in victims]
        gb = sum(self.entries[k][0] for k in victims)
        for k in victims:
            self._evict(k)
        if victims:
            self.free()
        return freed, gb

    def after_run(self, policy: str) -> None:
        """The run ended: nothing stays in use or capped even when unloading raises, and the device cache (the
        run's activations, which the allocator keeps) goes back after every run, not only after an eviction."""
        try:
            # a baked set the run used makes the plain copy dead weight (38 GB together for LTX): it goes now
            plain = {k[:4] for k in self.used if len(k) > 4} - self.used
            for k in [k for k in plain if k in self.entries and not self.pinned(k)]:
                self._evict(k)
        finally:
            self.done()
        if policy == "unload":
            self.unload()
        elif policy == "keep_pinned":
            self.unload(keep_pins=True)
        self.free()

    def state(self) -> dict:
        """The hosts value's `memory` and `resident`."""
        with self.lock:
            entries = [(k, e[0], e[2], e[3]) for k, e in self.entries.items()]
        why = {k: "pin" if self.pinned(k) else "in_use" for k, *_ in entries
               if self.pinned(k) or k in self.in_use}  # fmt: skip
        used = sum(e[1] for e in entries)
        measured = self.measure()
        return {
            "memory": {
                "budget_gb": round(self.budget_gb, 1),
                "used_gb": round(measured if measured is not None else used, 1),
            },
            "resident": [_resident(k, gb, why.get(k), loras, at) for k, gb, loras, at in entries],
        }


def _stamp(at: float) -> str:
    return datetime.fromtimestamp(at, UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _resident(key: tuple, gb: float, why: str | None, loras: list | None, at: float) -> dict:
    """One hosts value `resident` entry (`why`: pinned_why, None = not pinned); one with baked LoRAs
    (core.pack.ModelRef.resident_loras) lists them."""
    model, variant, component, precision, *_ = key
    out = {"component": component, "model": model, "variant": variant, "precision": precision,
           "gb": round(gb, 2), "pinned": why is not None} | ({"pinned_why": why} if why else {})  # fmt: skip
    out["last_used"] = _stamp(at)
    if loras:
        out["loras"] = loras
    return out


def _name(key: tuple, loras: list | None) -> dict:
    """The req.runner.unload `component` naming the entry `key` (its baked LoRAs by id and strength)."""
    model, variant, component, precision, *_ = key
    out = {"component": component, "model": model, "variant": variant, "precision": precision}
    if loras:
        out["loras"] = [{"lora_id": x["lora_id"], "strength": x["strength"]} for x in loras]
    return out


def ident(name: dict) -> tuple:
    """A named entry (a req.runner.unload component, a host_settings pin) as a comparable tuple (absent and empty `loras` are both the plain component)."""
    loras = tuple((x.get("lora_id"), float(x.get("strength", 0))) for x in name.get("loras") or ())
    return (
        name.get("component"),
        name.get("model"),
        name.get("variant"),
        name.get("precision"),
        loras,
    )
