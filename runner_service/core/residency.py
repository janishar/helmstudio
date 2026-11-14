"""The residency manager (docs/plan/03-runtime.md section 6.2): which model components are loaded, within a memory
budget of physical RAM minus RUNNER_RESERVE_GB.

- An entry is one component keyed by (model id, owning variant, component, precision): one file set at one
  precision. Its size is computed from the target dtype (pack.size_gb), not from header bytes.
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
        self.entries: OrderedDict[tuple, list] = OrderedDict()  # key → [gb, value]; LRU first
        self.in_use: set[tuple] = set()  # the running stage's components: not evictable
        self.on_short: list[Callable[[], None]] = []  # drop cached node results (graph.Cache.clear)
        self.lock = threading.Lock()

    @property
    def used_gb(self) -> float:
        return sum(gb for gb, _ in self.entries.values())

    def admit(self, needs: dict[tuple, float], peak_gb: float, later: set[tuple]) -> None:
        """Before a stage: `needs` = its components (key → GB), `later` = components the rest of the run
        uses. Makes room for the missing ones plus the peak; RunError when they cannot fit."""
        self.in_use = set(needs)
        missing = sum(gb for k, gb in needs.items() if k not in self.entries)
        self._make_room(missing + peak_gb, later)

    def get(self, key: tuple, gb: float, transient_gb: float, load: Callable[[], object]):
        """The loaded component `key`, loading it (with room for `gb` + the load-time transient) if needed."""
        if key in self.entries:
            self.entries.move_to_end(key)
            return self.entries[key][1]
        self.in_use.add(key)
        self._make_room(gb + transient_gb, set())
        t = time.monotonic()
        value = load()
        log.info(
            "loaded %s (%.1f GB est.) in %.1f s", "/".join(map(str, key)), gb, time.monotonic() - t
        )
        with self.lock:
            self.entries[key] = [gb, value]
        return value

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
            entries = [(k, gb) for k, (gb, *_) in self.entries.items()]
        used = sum(gb for _, gb in entries)
        measured = self.measure()
        return {
            "memory": {
                "budget_gb": round(self.budget_gb, 1),
                "used_gb": round(measured if measured is not None else used, 1),
            },
            "resident": [
                {
                    "component": component,
                    "model": model,
                    "variant": variant,
                    "precision": precision,
                    "gb": round(gb, 2),
                    "pinned": (model, variant, component, precision) in self.in_use
                    or component in self.pins,
                }
                for (model, variant, component, precision), gb in entries
            ],
        }
