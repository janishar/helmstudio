"""Plugins that start themselves (API 1.65; contracts/README.md "Plugins: starts itself") on the test stack: the fake
runner (host fake, RUNNER_PLUGINS on, HELM_HOME /tmp/helm in its container) supervises contract_tests/fake_plugins'
fake-run (a tiny service on runner:8471) and fake-crash (exits at once). Turning a plugin on starts it, off stops it;
one that keeps stopping is Couldn't start with its log's last line."""

import pytest
from helpers import STUDIO, docker_exec, ok, wait_for

PLUGINS = f"{STUDIO}/admin/plugins"
UP = "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8471/health', timeout=2)"


def plugin(admin, name: str) -> dict:
    return next(p for p in ok(admin.get(PLUGINS))["plugins"] if p["name"] == name)


def answers() -> bool:
    try:
        docker_exec("runner", "python3", "-c", UP)
        return True
    except Exception:  # noqa: BLE001 - any failure: nothing listens
        return False


@pytest.fixture
def off(admin):
    names = []
    yield names.append
    for name in names:
        admin.put(f"{PLUGINS}/{name}", json={"enabled": False})


def test_on_starts_it_off_stops_it(admin, off):
    ok(admin.post(f"{PLUGINS}/rescan"))
    p = plugin(admin, "fake-run")
    assert p["starts"]["host"] == "fake"
    admin.put(f"{PLUGINS}/fake-run", json={"enabled": False})
    off("fake-run")
    assert ok(admin.put(f"{PLUGINS}/fake-run", json={"enabled": True}))["status"] in (
        "starting",
        "not_running",
        "running",
    )
    got = wait_for(
        lambda: (q := plugin(admin, "fake-run"))["status"] == "running" and q,
        timeout=60,
        what="Running",
    )
    assert got["starts"]["log"] == "/tmp/helm/logs/fake-run.log" or got["starts"]["log"].endswith(
        "logs/fake-run.log"
    )
    assert answers()
    assert ok(admin.put(f"{PLUGINS}/fake-run", json={"enabled": False}))["status"] == "off"
    wait_for(lambda: not answers(), timeout=40, what="the runner to stop it")


def test_one_that_keeps_stopping_is_couldnt_start(admin, off):
    ok(admin.post(f"{PLUGINS}/rescan"))
    off("fake-crash")
    ok(admin.put(f"{PLUGINS}/fake-crash", json={"enabled": True}))
    got = wait_for(lambda: (q := plugin(admin, "fake-crash"))["status"] == "failed" and q, timeout=60,
                   what="Couldn't start")  # fmt: skip
    assert "boom: fake-crash cannot start" in got["problem"] and "exit 3" in got["problem"], got
