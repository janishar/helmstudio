"""The residency manager (docs/plan/03-runtime.md section 6.2): which model components are loaded, within a memory
budget of physical RAM minus RUNNER_RESERVE_GB.

- An entry is one component keyed by (model id, owning variant, component, precision[, baked LoRAs]): one file
  set at one precision, with LoRAs merged at load (mode bake) its own entry. Adapters live on an entry's module and
  are counted in its size, so they go when it is evicted. Its size is computed from the target dtype (pack.size_gb), not from header bytes.
- Admission, before a stage runs: need = the stage's components not yet loaded + its peak_gb. When short, cached
  node results are dropped first, then the least recently used components, those the rest of the run does not
  need before those it does; never one the stage uses. A need that cannot fit even then fails the run at once,
  with numbers.
- After a run: unload (everything), keep_warm (the default: stay until memory is needed or RUNNER_IDLE_UNLOAD_S
  passes) or keep_pinned (only RUNNER_PIN components stay).

Every method but `state` runs on the GPU thread; `state` (read by the hosts heartbeat) takes the lock."""

import logging
import os
import threading
import time
from collections import OrderedDict
from collections.abc import Callable

from core.graph import RunError

log = logging.getLogger("residency")

POLICIES = ("unload", "keep_warm", "keep_pinned")


def ram_gb() -> float:
    """Physical memory in GB (2^30 bytes)."""
    return os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES") / 2**30


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
        self.entries: OrderedDict[tuple, list] = (
            OrderedDict()
        )  # key → [gb, value, loras]; LRU first
        self.in_use: set[tuple] = set()  # the running stage's components: not evictable
        self.on_short: list[Callable[[], None]] = []  # drop cached node results (graph.Cache.clear)
        self.lock = threading.Lock()

    @property
    def used_gb(self) -> float:
        return sum(e[0] for e in self.entries.values())

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
        if key in self.entries:
            self.entries.move_to_end(key)
            self.entries[key][2] = loras
            return self.entries[key][1]
        self.in_use.add(key)
        self._make_room(gb + transient_gb, set())
        t = time.monotonic()
        value = load()
        log.info(
            "loaded %s (%.1f GB est.) in %.1f s", "/".join(map(str, key)), gb, time.monotonic() - t
        )
        with self.lock:
            self.entries[key] = [gb, value, loras]
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
        """The run ended: nothing is in use."""
        self.in_use = set()

    def _make_room(self, need: float, later: set[tuple]) -> None:
        if self.used_gb + need <= self.budget_gb:
            return
        for drop in self.on_short:
            drop()
        lru = list(self.entries)
        victims = sorted(
            (k for k in lru if k not in self.in_use), key=lambda k: (k in later, lru.index(k))
        )
        evicted = False
        for k in victims:
            if self.used_gb + need <= self.budget_gb:
                break
            self._evict(k)
            evicted = True
        if evicted:
            self.free()
        if self.used_gb + need > self.budget_gb:
            raise RunError(
                f"does not fit in memory: needs {need:.1f} GB more, {self.used_gb:.1f} GB in use "
                f"by this run, budget {self.budget_gb:.1f} GB"
            )

    def _evict(self, key: tuple) -> None:
        with self.lock:
            entry = self.entries.pop(key)
        log.info("unloaded %s", "/".join(map(str, key)))
        entry.clear()  # drop the reference to the loaded module here, so free() can return its memory

    def release(self, keys: set[tuple]) -> None:
        """Unload these components now (a stage's model.yaml `unload_after`), pinned ones excepted."""
        victims = [k for k in self.entries if k in keys and k[2] not in self.pins]
        for k in victims:
            self.in_use.discard(k)
            self._evict(k)
        if victims:
            self.free()

    def unload(self, keep_pins: bool = False) -> None:
        """Unload every component (keep_pins: but RUNNER_PIN ones) and give the memory back."""
        victims = [k for k in self.entries if not (keep_pins and k[2] in self.pins)]
        for k in victims:
            self._evict(k)
        if victims:
            self.free()

    def after_run(self, policy: str) -> None:
        self.done()
        if policy == "unload":
            self.unload()
        elif policy == "keep_pinned":
            self.unload(keep_pins=True)

    def state(self) -> dict:
        """The hosts value's `memory` and `resident`."""
        with self.lock:
            entries = [(k, e[0], e[2] if len(e) > 2 else None) for k, e in self.entries.items()]
        used = sum(gb for _, gb, _ in entries)
        measured = self.measure()
        return {
            "memory": {
                "budget_gb": round(self.budget_gb, 1),
                "used_gb": round(measured if measured is not None else used, 1),
            },
            "resident": [
                _resident(k, gb, k in self.in_use or k[2] in self.pins, loras)
                for k, gb, loras in entries
            ],
        }


def _resident(key: tuple, gb: float, pinned: bool, loras: list | None) -> dict:
    """One hosts value `resident` entry; one with baked LoRAs (core.pack.ModelRef.resident_loras) lists them."""
    model, variant, component, precision, *_ = key
    out = {"component": component, "model": model, "variant": variant, "precision": precision,
           "gb": round(gb, 2), "pinned": pinned}  # fmt: skip
    if loras:
        out["loras"] = loras
    return out
