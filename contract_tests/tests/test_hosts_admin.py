"""Hosts admin (API 1.16, contracts/README.md "Hosts admin") on the fake host: the admin block of GET /hosts, the
per-host settings applied live (after_run, idle_unload_min, reserve_gb), pins and unloads, on the echo pack's tiny
model (echo-tiny loads its transformer and vae; test_models.py downloads it from the fake hub).

The fake host has one runner env, so no hand-over request reaches it: that a pin is never a hand-over victim is
covered by studio's dispatch table tests (contracts/fixtures/dispatch) and, on a host with two envs, by
resilience.sh's handover-studio."""

import pytest
from helpers import STUDIO, ok, wait_for
from test_models import (
    ADMIN,
    ENV,
    HOST,
    MODEL,
    ensure_present,
    residents,
    settings_applied,
    tiny_run,
    unload_all,
)

PIN = {
    "env": ENV,
    "component": "transformer",
    "model": MODEL,
    "variant": "tiny",
    "precision": "bf16",
}
WARM = {"after_run": "keep_warm", "idle_unload_min": 0, "reserve_gb": 1}
REDACTED = {
    "host",
    "env",
    "state",
    "updated_at",
    "memory",
    "host_memory",
    "resident",
    "cached_gb",
    "machine",
    "device",  # API 1.18: the device and, cuda only, host RAM
    "host_ram",
}


def plain(pins: list[dict]) -> list[dict]:
    """Pins without an empty `loras` (absent or empty = the plain component)."""
    return [{k: v for k, v in p.items() if k != "loras" or v} for p in pins]


def put_settings(admin, **change) -> dict:
    s = ok(admin.put(f"{ADMIN}/hosts/{HOST}/settings", json=WARM | change))
    wait_for(lambda: settings_applied(admin), timeout=60, what="settings applied on fake")
    return s


def put_pins(admin, pins: list[dict]) -> dict:
    s = ok(admin.put(f"{ADMIN}/hosts/{HOST}/pins", json={"pins": pins}))
    wait_for(lambda: settings_applied(admin), timeout=60, what="pins applied on fake")
    return s


@pytest.fixture(scope="module")
def tiny(admin):
    """The tiny model present on fake, nothing of it resident, settings back to keep_warm without pins after."""
    ensure_present(admin, "tiny")
    unload_all(admin)
    yield
    put_pins(admin, [])
    # a failed test_reserve_mode_auto leaves no auto behind
    put_settings(admin, reserve_mode="custom")


def entry(client, component: str) -> dict | None:
    return next((r for r in residents(client) if r["component"] == component), None)


def test_hosts_redacted_for_a_user(api):
    hosts = ok(api.get(f"{STUDIO}/hosts"))["hosts"]
    assert any((h["host"], h["env"]) == (HOST, ENV) for h in hosts), hosts
    for h in hosts:
        assert set(h) <= REDACTED, h  # no `admin`: never a run, an owner, settings or the token
        for r in h.get("resident", []):
            assert r.get("pinned_why") in (None, "in_use", "pin"), r


def test_hosts_admin_block(admin):
    hosts = ok(admin.get(f"{STUDIO}/hosts"))["hosts"]
    (fake,) = [h for h in hosts if (h["host"], h["env"]) == (HOST, ENV)]
    a = fake["admin"]
    assert set(a) == {"settings", "policy", "run", "idle_since", "unload_pending", "hub_token"}, a
    assert a["hub_token"] is True and a["unload_pending"] is False, a
    assert a["policy"] is not None and a["policy"]["after_run"] in (
        "unload",
        "keep_warm",
        "keep_pinned",
    ), a
    assert fake["machine"]["packaging"] == "container" and fake["machine"]["ram_gb"] > 1, fake


def test_settings_refusals(admin, api):
    r = admin.put(f"{ADMIN}/hosts/{HOST}/settings", json=WARM | {"reserve_gb": 1024})
    assert (
        r.status_code == 422 and r.json()["detail"] == f"reserve_gb leaves no memory on {HOST}"
    ), r.text
    for bad in ({"after_run": "never"}, {"idle_unload_min": 1441}, {"reserve_gb": 0.3}):
        assert admin.put(f"{ADMIN}/hosts/{HOST}/settings", json=WARM | bad).status_code == 422, bad
    assert admin.put(f"{ADMIN}/hosts/nohost/settings", json=WARM).status_code == 404
    r = admin.put(f"{ADMIN}/hosts/{HOST}/pins", json={"pins": [PIN | {"env": "nope"}]})
    assert r.status_code == 422 and r.json()["detail"] == f"env nope is not on host {HOST}", r.text
    r = admin.put(f"{ADMIN}/hosts/{HOST}/pins", json={"pins": [PIN | {"model": "nomodel"}]})
    assert (
        r.status_code == 422 and r.json()["detail"] == f"nomodel is not published by {HOST}.{ENV}"
    ), r.text
    assert admin.put(f"{ADMIN}/hosts/nohost/pins", json={"pins": []}).status_code == 404


def fake_key(admin) -> dict:
    hosts = ok(admin.get(f"{STUDIO}/hosts"))["hosts"]
    return next(h for h in hosts if (h["host"], h["env"]) == (HOST, ENV))


def test_reserve_mode_auto(admin, tiny):
    """API 1.75 (plan 35): auto skips the reserve check and keeps reserve_gb; the fake host (Linux, no GPU limit)
    budgets RAM - RUNNER_RESERVE_GB (1); a PUT without reserve_mode keeps the mode; custom again cuts reserve_gb."""
    s = put_settings(admin, reserve_gb=1024, reserve_mode="auto")
    assert s["reserve_mode"] == "auto" and s["reserve_gb"] == 1024, s
    h = fake_key(admin)
    assert h["admin"]["policy"]["reserve_mode"] == "auto", h["admin"]
    assert h["machine"].get("gpu_limit_gb") is None, h["machine"]
    assert h["memory"]["budget_gb"] == pytest.approx(h["machine"]["ram_gb"] - 1, abs=0.2), h
    assert put_settings(admin, reserve_gb=1024)["reserve_mode"] == "auto"  # an older client's PUT
    s = put_settings(admin, reserve_gb=2, reserve_mode="custom")
    h = fake_key(admin)
    assert s["reserve_mode"] == "custom" and h["admin"]["policy"]["reserve_mode"] == "custom", h[
        "admin"
    ]
    assert h["memory"]["budget_gb"] == pytest.approx(h["machine"]["ram_gb"] - 2, abs=0.2), h
    r = admin.put(f"{ADMIN}/hosts/{HOST}/settings", json=WARM | {"reserve_mode": "sometimes"})
    assert r.status_code == 422 and r.json()["detail"] == "reserve_mode must be auto or custom", (
        r.text
    )


def test_pin_precision_fp32(admin, tiny):
    """API 1.20: fp32 is a Precision (a component kept at its stored float32); the runner takes the pin as is."""
    s = put_pins(admin, [PIN | {"precision": "fp32"}])
    assert plain(s["pins"]) == [PIN | {"precision": "fp32"}], s
    r = admin.put(f"{ADMIN}/hosts/{HOST}/pins", json={"pins": [PIN | {"precision": "fp64"}]})
    assert r.status_code == 422 and r.json()["detail"] == (
        "pins[0]: precision must be bf16, fp16, int8, int4, fp8 or fp32"
    ), r.text
    put_pins(admin, [])


def test_unload_requests(admin):
    a = ok(admin.post(f"{ADMIN}/hosts/{HOST}/unload", json={"all": True}), 202)
    assert a["host"] == HOST and {"env": ENV, "state": "asked"} in a["requests"], a
    a = ok(admin.post(f"{ADMIN}/hosts/{HOST}/unload", json={"cached_env": ENV}), 202)
    assert a["requests"] == [{"env": ENV, "state": "asked"}], a
    r = admin.post(f"{ADMIN}/hosts/{HOST}/unload", json={"cached_env": "nope"})
    assert r.status_code == 422 and r.json()["detail"] == f"env nope is not on host {HOST}", r.text
    assert (
        admin.post(
            f"{ADMIN}/hosts/{HOST}/unload", json={"all": True, "cached_env": ENV}
        ).status_code
        == 422
    )
    assert admin.post(f"{ADMIN}/hosts/{HOST}/unload", json={}).status_code == 422
    assert admin.post(f"{ADMIN}/hosts/nohost/unload", json={"all": True}).status_code == 404


def test_after_run_changes_live(admin, api, tiny):
    """unload, then keep_warm: each applies to the next run without a runner restart."""
    s = put_settings(admin, after_run="unload")
    assert (s["host"], s["after_run"], s["pins"]) == (HOST, "unload", []), s
    tiny_run(api)
    wait_for(lambda: not residents(admin), timeout=30, what="nothing kept after the run (unload)")
    s2 = put_settings(admin, after_run="keep_warm")
    assert s2["version"] > s["version"], (s, s2)
    tiny_run(api)
    kept = wait_for(
        lambda: residents(admin), timeout=30, what="the run's components kept (keep_warm)"
    )
    assert {(r["component"], r["variant"], r["precision"]) for r in kept} == {
        ("transformer", "tiny", "bf16"),
        ("vae", "tiny", "bf16"),
    }, kept
    assert all(not r["pinned"] for r in kept), kept


def test_pin_survives_the_idle_unload_not_an_admin_unload(admin, api, tiny):
    unload_all(admin)
    put_settings(admin, idle_unload_min=1)
    s = put_pins(admin, [PIN, PIN])  # duplicates collapse
    assert plain(s["pins"]) == [PIN], s
    tiny_run(api)
    pinned = wait_for(lambda: entry(admin, "transformer"), timeout=30, what="transformer resident")
    assert (pinned["pinned"], pinned.get("pinned_why")) == (True, "pin"), pinned
    assert entry(api, "transformer")["pinned_why"] == "pin"  # users see why, not who
    assert entry(admin, "vae")["pinned"] is False
    # a minute idle: everything but the pin is unloaded
    wait_for(
        lambda: entry(admin, "vae") is None, timeout=150, interval=2, what="the idle unload of vae"
    )
    assert entry(admin, "transformer")["pinned"] is True
    # an admin unload of the pinned entry unloads it; the pin stays set
    a = ok(admin.post(f"{ADMIN}/hosts/{HOST}/unload", json={"entries": [PIN]}), 202)
    assert a["requests"] == [{"env": ENV, "state": "asked"}], a
    wait_for(
        lambda: entry(admin, "transformer") is None,
        timeout=30,
        what="the pinned transformer unloaded",
    )
    hosts = ok(admin.get(f"{STUDIO}/hosts"))["hosts"]
    (fake,) = [h for h in hosts if (h["host"], h["env"]) == (HOST, ENV)]
    assert plain(fake["admin"]["settings"]["pins"]) == [PIN], fake["admin"]
