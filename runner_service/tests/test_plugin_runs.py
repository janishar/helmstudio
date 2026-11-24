"""messaging.plugin_runs: plugins that start themselves (API 1.65), with tiny python scripts as plugins, no NATS."""

import asyncio
import json
from types import SimpleNamespace

import pytest
from nats.js.errors import KeyNotFoundError

from messaging import plugin_runs
from messaging.plugin_runs import Refused, Supervisor, resolve
from messaging.publisher import now
from messaging.schemas import registry_errors

ENV_DUMP = (
    "import json, os, time; print(json.dumps(dict(os.environ)), flush=True); time.sleep(60)\n"
)


def plugin(root, name, script=ENV_DUMP, cmd=None, host="mac"):
    d = root / name
    d.mkdir(parents=True)
    (d / "main.py").write_text(script)
    run = {"cmd": cmd or ["python", "main.py"], "host": host}
    (d / "plugin.json").write_text(json.dumps({"name": name, "run": run}))
    return d


async def until(cond, timeout=5.0):
    for _ in range(int(timeout / 0.02)):
        if cond():
            return
        await asyncio.sleep(0.02)
    raise AssertionError("timed out")


@pytest.fixture
def sup(tmp_path, monkeypatch):
    monkeypatch.setenv("NATS_URL", "nats://runner:secret@x:4222")
    monkeypatch.setenv("S3_SECRET_ACCESS_KEY", "secret")
    published = []

    async def publish(value):
        assert registry_errors("plugins", value) == []
        published.append(value)

    s = Supervisor([tmp_path / "a", tmp_path / "b"], tmp_path / "home", publish, up_s=0.3,
                   backoff=(0.1, 0.4), kill_s=1.0, host="mac")  # fmt: skip
    s.published = published
    return s


async def test_runs_logs_clean_env_and_stops(sup, tmp_path):
    plugin(tmp_path / "a", "web")
    await sup.want(["web"])
    await until(lambda: sup.states.get("web", {}).get("state") == "running")
    log = tmp_path / "home/logs/web.log"
    await until(lambda: log.read_text().strip())
    env = json.loads(log.read_text().splitlines()[0])
    assert not [k for k in env if k.startswith(("NATS_", "S3_"))]
    assert env["HELM_HOME"] == str(tmp_path / "home")
    proc = sup.procs["web"]
    await sup.want([])
    assert proc.returncode is not None and sup.states.get("web", {}).get("state") == "stopped"
    assert [p["plugins"]["web"]["state"] for p in sup.published] == [
        "starting",
        "running",
        "stopped",
    ]


async def test_restarts_with_backoff(sup, tmp_path):
    plugin(tmp_path / "a", "crash", script="print('boom: no port')\nraise SystemExit(3)\n")
    await sup.want(["crash"])
    await until(lambda: sup.states.get("crash", {}).get("restarts", 0) >= 2)
    st = sup.states["crash"]
    assert st["state"] in ("failed", "starting")
    failed = [
        p["plugins"]["crash"] for p in sup.published if p["plugins"]["crash"]["state"] == "failed"
    ]
    assert failed[0]["detail"] == "it stopped 1 times: exit 3: boom: no port"
    await sup.close()


async def test_first_folder_wins_and_refusals(tmp_path):
    a, b = tmp_path / "a", tmp_path / "b"
    plugin(b, "web")
    plugin(a, "web", cmd=["./main.py"])
    assert resolve([a, b], "web", "mac")[1] == (a / "web").resolve()
    for cmd in (["/bin/sh", "x"], ["../x"], ["./../b/web/main.py"], ["./nope.sh"], ["bin/x"]):
        plugin(a, "bad", cmd=cmd)
        with pytest.raises(Refused, match="not a file inside its folder"):
            resolve([a], "bad", "mac")
        (a / "bad/plugin.json").unlink()
        (a / "bad/main.py").unlink()
        (a / "bad").rmdir()
    with pytest.raises(Refused, match="not found in its plugin folders"):
        resolve([a, b], "gone", "mac")
    (a / "zipped.zip").write_bytes(b"PK")
    with pytest.raises(Refused, match=r"a \.zip plugin can't start"):
        resolve([a], "zipped", "mac")
    (a / "link").symlink_to(b / "web")
    with pytest.raises(Refused, match="not found"):
        resolve([a], "link", "mac")
    plugin(a, "gpu", host="gpu1")
    with pytest.raises(Refused, match="host gpu1"):
        resolve([a], "gpu", "mac")
    (a / "norun").mkdir()
    (a / "norun/plugin.json").write_text('{"name": "norun"}')
    with pytest.raises(Refused, match="no run in its plugin.json"):
        resolve([a], "norun", "mac")


async def test_missing_is_failed_with_a_reason(sup):
    await sup.want(["gone"])
    await until(lambda: "gone" in sup.states)
    assert sup.states["gone"] | {"since": ""} == {
        "state": "failed", "detail": "not found in its plugin folders", "restarts": 0, "since": "",
        "log": str(sup.logs / "gone.log")}  # fmt: skip
    await sup.close()


class FakeKV:
    def __init__(self, value=None):
        self.value = value

    async def get(self, key):
        assert key == plugin_runs.WANT_KEY
        if self.value is None:
            raise KeyNotFoundError
        return SimpleNamespace(value=json.dumps(self.value).encode())


async def test_watch_reads_the_key_and_keeps_the_set_when_off_contract(sup, tmp_path, monkeypatch):
    monkeypatch.setattr(plugin_runs, "POLL", 0.05)
    plugin(tmp_path / "a", "web")
    kv = FakeKV({"schema_version": 1, "host": "mac", "plugins": ["web"], "updated_at": now()})

    async def key_value(bucket):
        assert bucket == "host_settings"
        return kv

    stop = asyncio.Event()
    task = asyncio.create_task(sup.watch(SimpleNamespace(key_value=key_value), stop))
    await until(lambda: "web" in sup.procs)
    kv.value = {"plugins": "web, off-contract"}
    await asyncio.sleep(0.2)
    assert "web" in sup.tasks
    kv.value = None  # deleted: none
    await until(lambda: sup.states.get("web", {}).get("state") == "stopped")
    stop.set()
    await task
