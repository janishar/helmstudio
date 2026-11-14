"""The residency manager with fake components: admission, LRU eviction, after-run policies."""

import pytest

from core.graph import RunError
from core.residency import Residency

A, B, C = (("m", "base", name, "bf16") for name in ("a", "b", "c"))


def manager(budget=10.0, **kw) -> tuple[Residency, list]:
    freed = []
    res = Residency(budget, free=lambda: freed.append(1), **kw)
    return res, freed


def test_loads_once_and_counts_memory():
    res, _ = manager()
    loads = []
    for _ in range(2):
        res.get(A, 4, 0, lambda: loads.append(1) or "module-a")
    assert loads == [1] and res.used_gb == 4 and res.get(A, 4, 0, None) == "module-a"


def test_lru_component_is_evicted_when_short_and_memory_is_freed():
    res, freed = manager()
    res.get(A, 4, 0, object)
    res.get(B, 4, 0, object)
    res.done()
    res.get(A, 4, 0, object)  # A is now the most recently used
    res.done()
    res.admit({C: 4}, 0, set())
    assert list(res.entries) == [A] and freed == [1]


def test_components_the_rest_of_the_run_needs_go_last():
    res, _ = manager()
    res.get(A, 4, 0, object)
    res.get(B, 4, 0, object)
    res.done()
    res.admit({C: 4}, 0, later={A})  # A is LRU, but the run needs it again: B goes
    assert list(res.entries) == [A]


def test_the_stage_in_use_is_never_evicted_and_short_fails_with_numbers():
    res, freed = manager()
    res.get(A, 4, 0, object)
    with pytest.raises(RunError, match=r"needs 8.0 GB more, 4.0 GB in use .* budget 10.0 GB"):
        res.admit({A: 4, C: 4}, 4, set())
    assert list(res.entries) == [A] and freed == []


def test_peak_and_load_transient_count():
    res, _ = manager()
    res.get(A, 4, 0, object)
    res.done()
    res.admit({}, 7, set())  # a peak alone evicts
    assert not res.entries
    res.get(B, 6, 3, object)  # 6 resident + a 3 GB transient fits 10
    res.done()
    with pytest.raises(RunError):
        res.get(C, 2, 9, object)  # the transient alone overflows once B is gone
    assert not res.entries


def test_cached_results_are_dropped_before_components():
    res, _ = manager()
    dropped = []
    res.on_short.append(lambda: dropped.append(1))
    res.get(A, 4, 0, object)
    res.done()
    res.get(B, 4, 0, object)
    assert dropped == []  # fits: nothing dropped
    res.done()
    res.admit({C: 4}, 0, set())
    assert dropped == [1] and list(res.entries) == [
        B
    ]  # A evicted; C is loaded by get(), not admit()


@pytest.mark.parametrize(
    ("policy", "left"), [("keep_warm", [A, B]), ("keep_pinned", [B]), ("unload", [])]
)
def test_after_run_policies(policy, left):
    res, _ = manager(pins={"b"})
    res.get(A, 2, 0, object)
    res.get(B, 2, 0, object)
    res.after_run(policy)
    assert list(res.entries) == left and res.in_use == set()


def test_eviction_drops_the_last_reference():
    import gc
    import weakref

    class Module:
        pass

    res, _ = manager()
    ref = weakref.ref(res.get(A, 8, 0, Module))
    res.done()
    res.admit({B: 8}, 0, set())
    gc.collect()
    assert ref() is None


def test_state_reports_memory_and_pins():
    res, _ = manager(budget=52, pins={"b"}, measure=lambda: 17.24)
    res.get(A, 7.4, 0, object)
    res.get(B, 1.3, 0, object)
    res.done()
    res.admit({A: 7.4}, 0, set())
    state = res.state()
    assert state["memory"] == {"budget_gb": 52, "used_gb": 17.2}  # measured, when the runtime can
    assert [(r["component"], r["pinned"]) for r in state["resident"]] == [("a", True), ("b", True)]
