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
    assert list(res.entries) == [A] and freed == [
        1,
        1,
    ]  # the device cache first, then again after the eviction


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
    assert list(res.entries) == [A] and freed == [
        1
    ]  # short: the device cache went back, no weights


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


K = ("m", "base", "a", "bf16", (("sha", 1.0),))  # A with a LoRA baked in: its own entry


@pytest.mark.parametrize(
    ("used", "later", "left"),
    [
        # an unreferenced baked set goes before an older plain component
        (set(), set(), [A]),
        # a baked set this run used is plain LRU again: the older A goes
        ({K}, set(), [K]),
        # what the rest of the run needs goes last, baked or not
        (set(), {K}, [K]),
    ],
)
def test_eviction_order_puts_an_unreferenced_baked_set_first(used, later, left):
    res, _ = manager()
    res.get(A, 4, 0, object)
    res.get(K, 4, 0, object, loras=[{"lora_id": "x"}])
    res.done()
    res.used = set(used)
    res.admit({B: 4}, 0, later)
    assert list(res.entries) == left


@pytest.mark.parametrize(
    ("pins", "plain_used", "left"),
    [(frozenset(), False, [K]), ({"a"}, False, [A, K]), (frozenset(), True, [A, K])],
)
def test_after_a_baked_run_the_plain_copy_goes(pins, plain_used, left):
    """The plain transformer and its baked copy never stay resident together after a run that used the baked one
    (unless the plain one is pinned, or the run used it too)."""
    res, freed = manager(budget=48, pins=pins)
    res.get(A, 19, 0, object)
    res.done()  # an earlier plain run
    if plain_used:
        res.get(A, 19, 0, object)
    res.get(K, 19, 0, object)
    res.after_run("keep_warm")
    assert list(res.entries) == left and freed == [1]  # the device cache goes back after every run


def test_a_plain_run_keeps_a_baked_set_until_room_is_needed():
    res, _ = manager(budget=48)
    res.get(K, 19, 0, object)
    res.done()
    res.get(A, 19, 0, object)
    res.after_run("keep_warm")
    assert list(res.entries) == [K, A]


def test_after_run_frees_every_run_and_ends_the_run_when_unloading_raises():
    res, freed = manager(budget=48)
    res.get(A, 10, 0, object)
    res.cap = 20
    res.after_run("keep_warm")  # nothing evicted: the allocator's cache still goes back
    assert freed == [1] and list(res.entries) == [A]

    def broken():
        raise RuntimeError("clear_cache failed")

    res.free = broken
    res.get(K, 10, 0, object)
    res.in_use, res.cap = {K}, 20
    with pytest.raises(RuntimeError):
        res.after_run("keep_warm")
    assert res.in_use == set() and res.cap is None and res.limit == 48
    assert not any(r["pinned"] for r in res.state()["resident"])


def test_the_cap_lowers_the_budget_for_one_run():
    res, _ = manager(budget=48)
    res.get(A, 10, 0, object)
    res.done()
    res.cap = 15
    res.admit({B: 10}, 0, set())  # 10 + 10 > 15: A goes, though 48 would hold both
    assert list(res.entries) == []
    with pytest.raises(RunError, match=r"budget 15.0 GB"):
        res.admit({B: 10}, 6, set())
    res.done()
    assert res.cap is None and res.limit == 48


def test_unload_named_frees_exactly_those_held_and_not_in_use():
    res, freed = manager(budget=48, pins={"c"})
    for key in (A, B, C, K):
        res.get(
            key,
            2,
            0,
            object,
            loras=[{"lora_id": "x", "strength": 1.0, "name": "n", "bytes": 1}]
            if key == K
            else None,
        )
    res.in_use = {B}
    names = [{"component": n, "model": "m", "variant": "base", "precision": "bf16"} for n in "abc"]
    names.append(names[0] | {"loras": [{"lora_id": "x", "strength": 1}]})
    out, gb = res.unload_named(names)  # C is pinned: named, it goes too
    assert (
        out == [names[0], names[2], names[3]]
        and gb == 6
        and list(res.entries) == [B]
        and freed == [1]
    )
    assert res.unload_named(names) == ([], 0)


def test_state_reports_last_used_in_utc_and_lru_order():
    import time

    res, _ = manager()
    res.get(A, 1, 0, object)
    time.sleep(0.002)
    res.get(B, 1, 0, object)
    first = res.state()["resident"]
    res.get(A, 1, 0, object)  # a use moves it last and restamps it
    after = res.state()["resident"]
    assert [r["component"] for r in after] == ["b", "a"]
    assert all(r["last_used"].endswith("Z") for r in after)
    assert after[1]["last_used"] >= first[1]["last_used"] > first[0]["last_used"]


@pytest.mark.parametrize(
    "after_free, left",
    [(6.0, [A]), (12.0, [])],  # 2 GB beyond A once the cache is back, or still 8
)
def test_what_the_device_measures_beyond_the_entries_counts(after_free, left):
    measured = [14.0]  # A's 4 GB + 10 GB the device holds beyond them (its cache, the runtime)
    res, freed = manager(budget=20, measure=lambda: measured[0])

    def free():
        freed.append(1)
        measured[0] = after_free - (0 if A in res.entries else 4)

    res.free = free
    res.get(A, 4, 0, object)
    res.done()
    res.admit(
        {B: 4}, 6, set()
    )  # 4 + 10 beyond + 10 > 20: the cache goes back first, then A only if still short
    assert list(res.entries) == left and freed[0] == 1


def test_a_need_short_of_what_the_device_measures_fails_with_those_numbers():
    res, freed = manager(
        budget=20, measure=lambda: 12.0
    )  # the runtime and live tensors: nothing to evict
    with pytest.raises(RunError, match=r"needs 15.0 GB more, 12.0 GB in use .* budget 20.0 GB"):
        res.admit({}, 15, set())
    assert freed == [1]  # the device cache was given back before failing


def test_footprint_is_this_process_and_grows_with_what_it_touches():
    import sys

    from core.residency import footprint_gb

    if sys.platform != "darwin":
        assert footprint_gb() is None
        return
    before = footprint_gb()
    block = bytearray(256 * 2**20)  # touched (zeroed) pages count
    assert 0 < before < footprint_gb() and footprint_gb() - before >= 0.2
    del block
