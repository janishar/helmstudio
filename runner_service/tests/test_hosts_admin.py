"""Hosts admin on the runner (contracts/README.md "Hosts admin"): pins by identity, pinned_why, the admin's unload
(pinned entries too, or only cached handles), host_settings applied live (idle at once, busy after the run, a deleted
key back to the env) and the hosts key's new fields."""

import json
from pathlib import Path

from conftest import CONTRACTS, FakeJS, consumer

import config
from core import models
from core.residency import Residency, host_device, ident
from messaging import schemas
from messaging.settings import Settings

A = ("ltx-2.5", "distilled", "transformer", "int8")
B = ("ltx-2.5", "distilled", "vae", "bf16")
C = ("ltx-2.5", "distilled", "upscaler", "bf16")
PIN = {
    "env": "fake",
    "component": "transformer",
    "model": "ltx-2.5",
    "variant": "distilled",
    "precision": "int8",
}
UNLOAD = "https://helmstudio.local/contracts/messages/req.runner.unload.schema.json#/$defs/"


def settings_value(**kw) -> dict:
    v = json.loads((CONTRACTS / "fixtures/registry/settings.valid.json").read_text())
    return v | {"pins": [PIN]} | kw


def held(res: Residency, *keys) -> None:
    for i, k in enumerate(keys):
        res.entries[k] = [4.0, object(), None, float(i)]


def test_a_pinned_entry_goes_after_every_unpinned_one_and_reports_why():
    res = Residency(10)
    res.pin_refs = {ident(PIN)}
    held(res, A, B)  # A, the pinned one, is least recently used
    res.admit({C: 4.0}, 0, set())
    res.get(C, 4.0, 0, object)
    assert list(res.entries) == [A, C]
    why = {r["component"]: r.get("pinned_why") for r in res.state()["resident"]}
    assert why == {"transformer": "pin", "upscaler": "in_use"}
    res.done()
    res.after_run("keep_pinned")
    assert list(res.entries) == [A]


def test_a_named_pin_is_unloaded_and_stays_set():
    """The dispatcher names a pin only as a last resort (contracts/README.md, the hand-over): it goes, the pin stays
    and the component is pinned again when it is next loaded."""
    res = Residency(48)
    res.pin_refs = {ident(PIN)}
    held(res, A)
    name = {k: PIN[k] for k in ("component", "model", "variant", "precision")}
    assert res.unload_named([name]) == ([name], 4.0) and not res.entries
    res.get(A, 4.0, 0, object)
    assert res.pinned(A) and res.state()["resident"][0]["pinned_why"] == "pin"


def test_a_pin_is_an_admission_victim_only_as_a_last_resort():
    res = Residency(10)
    res.pin_refs = {ident(PIN)}
    held(res, A, B)  # A, the pinned one, is least recently used
    res.admit({C: 4.0}, 0, set())  # 4 + 4 + 4 > 10: one goes, the unpinned B
    assert list(res.entries) == [A]
    res.done()
    res.admit(
        {C: 4.0}, 4.0, set()
    )  # 4 + 4 + 4 > 10 with only the pin left: the pin goes, the run fits
    assert list(res.entries) == []


async def test_admin_unload_frees_pinned_entries_and_cached_only_keeps_them():
    import main

    res = Residency(48)
    res.pin_refs = {ident(PIN)}
    held(res, A, B)
    c = consumer(FakeJS(), residency=res)
    c.host.extra = res.state
    c.cache["k"] = {"x": 1}
    reply = await main.unload_reply(
        c, c.host, {"for_env": "fake", "by": "admin", "cached_only": True}
    )
    assert (
        schemas.errors(UNLOAD + "reply", reply) == []
        and len(reply["resident"]) == 2
        and not c.cache
    )
    name = {k: PIN[k] for k in ("component", "model", "variant", "precision")}
    reply = await main.unload_reply(
        c, c.host, {"for_env": "fake", "by": "admin", "components": [name]}
    )
    assert reply["freed"] == [name] and [r["component"] for r in reply["resident"]] == ["vae"]


async def test_a_runner_without_weights_answers_an_unload():
    import main

    c = consumer(FakeJS())
    c.cache["k"] = {"x": 1}
    reply = await main.unload_reply(c, c.host, {"for_env": "fake", "by": "admin"})
    assert reply == {"unloaded_gb": 0, "freed": [], "resident": []} and not c.cache


def test_the_fake_runner_gets_a_residency_manager_and_a_cpu_device():
    """The echo pack's loader (runtime fake) offers what residency_for and the hosts key call (M6)."""
    import types

    import main
    import packs.echo.fake  # noqa: F401  registers core.loaders_fake

    res, dev, loader = main.residency_for(
        [types.SimpleNamespace(runtime="fake", spec={"components": {"x": {}}})]
    )
    assert dev == "cpu" and res.total_gb == loader.total_gb() > 0
    assert loader.device_info()["kind"] == "cpu"


async def test_settings_apply_at_once_when_idle_and_after_the_run_when_busy(monkeypatch):
    monkeypatch.setattr("messaging.settings.ram_gb", lambda: 64.0)
    res = Residency(52)
    held(res, A, B)
    c = consumer(FakeJS(), residency=res)
    s = Settings(c, c.host)
    c.host.extra = lambda: res.state() | {"policy": s.policy()}
    assert s.policy() == {"after_run": config.AFTER_RUN, "idle_unload_min": round(config.IDLE_UNLOAD_S / 60),
                          "reserve_gb": config.RESERVE_GB, "settings_version": None}  # fmt: skip
    await s.offer(settings_value(after_run="keep_pinned", reserve_gb=16, idle_unload_min=0))
    assert list(res.entries) == [A]  # keep_pinned: what is not pinned goes now
    assert res.budget_gb == 48 and c.after_run == "keep_pinned" and c.idle_unload_s == 0
    key = json.loads(c.host.kv.data["fake.fake"])
    assert key["policy"] == {"after_run": "keep_pinned", "idle_unload_min": 0, "reserve_gb": 16,
                             "settings_version": 3}  # fmt: skip
    assert key["resident"][0]["pinned_why"] == "pin"
    c.ctx = object()  # busy: nothing changes mid-run
    await s.offer(settings_value(version=4, after_run="unload", pins=[]))
    assert s.version == 3 and list(res.entries) == [A]
    c.ctx = None
    await s.run_ended()
    assert s.version == 4 and not res.entries and res.pin_refs == set()
    await s.offer(None)  # the key was deleted: the env's values again
    assert s.version is None and res.pin_refs is None and res.budget_gb == 64 - config.RESERVE_GB


async def test_an_older_version_never_replaces_a_newer_one_and_unload_clears_the_cache(monkeypatch):
    monkeypatch.setattr("messaging.settings.ram_gb", lambda: 64.0)
    c = consumer(FakeJS(), residency=Residency(52))
    s = Settings(c, c.host)
    await s.offer(settings_value(version=6, after_run="keep_warm"))
    await s.offer(settings_value(version=5, after_run="keep_pinned"))  # its KV put landed late
    assert s.version == 6 and s.after_run == "keep_warm"
    c.ctx = object()
    await s.offer(settings_value(version=8, after_run="keep_warm"))
    await s.offer(settings_value(version=7, after_run="keep_pinned"))  # older than the pending one
    c.ctx = None
    await s.run_ended()
    assert s.version == 8 and s.after_run == "keep_warm"
    c.cache["x"] = object()
    await s.offer(settings_value(version=9, after_run="unload"))  # idle: cached node results go too
    assert s.version == 9 and not c.cache


async def test_off_contract_settings_are_ignored():
    c = consumer(FakeJS())
    s = Settings(c, c.host)
    await s.offer(settings_value(after_run="sometimes"))
    assert s.version is None


def test_the_hosts_key_reports_disk_machine_token_and_policy(tmp_path):
    from messaging.hosts import Host

    c = consumer(FakeJS())
    s = Settings(c, c.host)
    extra = {"models_disk": models.disk(tmp_path / "not-yet"), "machine": models.machine("cpu", 7.9),
             "hub_token": False, "policy": s.policy(), "device": host_device("cpu")}  # fmt: skip
    value = Host(None, extra=lambda: extra).value()
    assert schemas.registry_errors("host", value) == []
    assert extra["machine"]["ram_gb"] == 7.9 and extra["models_disk"]["free_bytes"] > 0
    home = models.disk(Path.home() / "GenAI/models")["root"]
    assert home == "~/GenAI/models"


async def test_settings_follow_the_key_by_reading_it(monkeypatch):
    """No consumer on the bucket (a remote host may not create one): a direct get every POLL seconds offers each new
    revision once, and a deleted key brings back the env's values."""
    import asyncio
    import types

    from nats.js.errors import KeyNotFoundError

    import messaging.settings as ms

    monkeypatch.setattr(ms, "POLL", 0.01)
    store: dict = {}

    class KV:
        async def get(self, key):
            if key not in store:
                raise KeyNotFoundError
            return types.SimpleNamespace(revision=store[key][0], value=store[key][1])

    class JS:
        async def key_value(self, bucket):
            assert bucket == "host_settings"
            return KV()

    c = consumer(FakeJS())
    s = Settings(c, c.host)
    offered = []

    async def offer(value):
        offered.append(value and value["version"])

    s.offer = offer
    stop = asyncio.Event()
    task = asyncio.create_task(s.watch(JS(), stop))
    await asyncio.sleep(0.05)
    store[config.HOST] = (7, json.dumps(settings_value(version=3)).encode())
    await asyncio.sleep(0.05)
    del store[config.HOST]
    await asyncio.sleep(0.05)
    stop.set()
    await task
    assert offered == [3, None]


def test_a_read_only_models_root_is_not_writable(tmp_path):
    ro = tmp_path / "models"
    ro.mkdir()
    ro.chmod(0o555)
    try:
        assert models.disk(ro)["writable"] is False and models.disk(tmp_path)["writable"] is True
    finally:
        ro.chmod(0o755)


def test_tls_paths_stay_paths_and_make_the_nats_context(tmp_path, monkeypatch):
    import subprocess

    import pytest

    import main

    assert {
        "NATS_CA_FILE",
        "NATS_KEY_FILE",
        "S3_CA_FILE",
    } <= config.PATHS  # never read into the environment
    assert main.tls_context() is None or config.NATS_CA_FILE
    cert, key = tmp_path / "ca.pem", tmp_path / "ca.key"
    r = subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=t",
                        "-keyout", str(key), "-out", str(cert)], capture_output=True, check=False)  # fmt: skip
    if r.returncode:
        pytest.skip("no openssl")
    monkeypatch.setattr(config, "NATS_CA_FILE", str(cert))
    monkeypatch.setattr(config, "NATS_CERT_FILE", str(cert))
    monkeypatch.setattr(config, "NATS_KEY_FILE", str(key))
    ctx = main.tls_context()
    assert ctx.verify_mode.name == "CERT_REQUIRED" and ctx.check_hostname
    assert [c["subject"] for c in ctx.get_ca_certs()] == [((("commonName", "t"),),)]


async def test_a_reserve_above_vram_leaves_no_budget_never_a_negative_one(monkeypatch):
    """On CUDA the reserve is cut from VRAM (total_gb), not RAM: 32 of a 24 GB card leaves 0, and the hosts key stays
    on contract (budget_gb ≥ 0) so the host never goes offline for it."""
    monkeypatch.setattr("messaging.settings.ram_gb", lambda: 64.0)
    res = Residency(22)
    res.total_gb = 24.0
    c = consumer(FakeJS(), residency=res)
    s = Settings(c, c.host)
    c.host.extra = lambda: res.state()
    await s.offer(settings_value(after_run="keep_warm", reserve_gb=32, idle_unload_min=0))
    assert res.budget_gb == 0
    assert json.loads(c.host.kv.data["fake.fake"])["memory"]["budget_gb"] == 0


def test_a_ca_file_refuses_a_plain_link(monkeypatch):
    """contracts/README.md, A remote GPU host: with a CA file set, a nats:// or http:// URL is refused, never used."""
    for k, v in {"NATS_CA_FILE": "/ca", "NATS_URL": "tls://h:4222", "S3_CA_FILE": "/ca",
                 "S3_ENDPOINT": "https://h:8333"}.items():  # fmt: skip
        monkeypatch.setattr(config, k, v)
    assert config.plain_with_ca() is None
    monkeypatch.setattr(config, "NATS_URL", "nats://runner:pw@h:4222")
    assert "NATS_URL" in config.plain_with_ca()
    monkeypatch.setattr(config, "NATS_URL", "tls://h:4222")
    monkeypatch.setattr(config, "S3_ENDPOINT", "http://h:8333")
    assert "S3_ENDPOINT" in config.plain_with_ca()
    monkeypatch.setattr(config, "S3_CA_FILE", "")
    assert config.plain_with_ca() is None  # no CA: the operator's plain in-stack link (compose)
