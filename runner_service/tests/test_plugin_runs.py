"""messaging.plugin_runs: plugins that start themselves (API 1.67), with tiny python scripts as plugins, no NATS."""

import asyncio
import json
import subprocess
from pathlib import Path
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
    assert (tmp_path / "home/run/web.pid").read_text() == str(proc.pid)
    assert (tmp_path / "home/run").stat().st_mode & 0o777 == 0o700
    await sup.want([])
    assert not (tmp_path / "home/run/web.pid").exists()
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
    plugin(a, "sub", cmd=["bin/start.sh", "x"])
    (a / "sub/bin").mkdir()
    (a / "sub/bin/start.sh").write_text("")
    assert resolve([a], "sub", "mac")[0] == [str((a / "sub/bin/start.sh").resolve()), "x"]
    for cmd in (["/bin/sh", "x"], ["../x"], ["./../b/web/main.py"], ["./nope.sh"], ["bin/x"],
                [str(a / "web/main.py")]):  # fmt: skip
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


def leftover(argv, cwd=None):
    """A group a dead runner left (its own session, not our child's supervisor)."""
    return subprocess.Popen(argv, cwd=cwd, start_new_session=True, stdout=subprocess.DEVNULL)


async def test_reaps_leftover_groups_only_when_still_the_plugins(sup, tmp_path):
    d = plugin(tmp_path / "a", "web")
    run = tmp_path / "home/run"
    run.mkdir(parents=True)
    left = leftover(["python", "main.py"], d)
    other = leftover(["sleep", "60"])
    (run / "web.pid").write_text(str(left.pid))
    (run / "x.pid").write_text(str(other.pid))  # not a plugin's process: left alone
    (run / "gone.pid").write_text("999999")
    await sup.reap_all()
    assert left.wait(5) is not None and other.poll() is None
    assert list(run.iterdir()) == []
    other.kill()
    other.wait()


async def test_a_new_retry_nonce_starts_a_failed_plugin_at_once(tmp_path):
    plugin(tmp_path / "a", "crash", script="raise SystemExit(3)\n")

    async def publish(value):
        pass

    s = Supervisor(
        [tmp_path / "a"], tmp_path / "home", publish, up_s=0.3, backoff=(30, 60), host="mac"
    )
    value = {"schema_version": 1, "host": "mac", "plugins": ["crash"], "updated_at": now()}
    await s.offer(json.dumps(value | {"retry": {"crash": 1}}).encode())
    await until(lambda: s.states.get("crash", {}).get("state") == "failed")
    await s.offer(json.dumps(value | {"retry": {"crash": 1}}).encode())  # same nonce: still waits
    await asyncio.sleep(0.2)
    assert s.states["crash"]["restarts"] == 0
    await s.offer(json.dumps(value | {"retry": {"crash": 2}}).encode())
    await until(lambda: s.states["crash"]["restarts"] == 1)
    await s.close()


# the repo's plugins: Your Claude subscription (claude-code-bridge)
BRIDGE = Path(__file__).resolve().parents[2] / "plugins"


@pytest.mark.parametrize("claude", [None, '{"loggedIn": false, "authMethod": "none"}'])
async def test_no_claude_or_not_signed_in_waits_for_try_again(sup, tmp_path, monkeypatch, claude):
    """The bridge's run.sh with a fake PATH: no claude, or claude not signed in -> failed with the plain line, no
    restarts until Try again (exit 78). Only `claude auth status` is asked; no token is made."""
    bin_ = tmp_path / "bin"
    bin_.mkdir()
    if claude:
        (bin_ / "claude").write_text(
            f"#!/bin/sh\n[ \"$1 $2\" = 'auth status' ] && echo '{claude}'\n"
        )
        (bin_ / "claude").chmod(0o755)
    monkeypatch.setenv("PATH", f"{bin_}:/usr/bin:/bin")
    monkeypatch.setenv("HOME", str(tmp_path))  # run.sh adds ~/.local/bin: never the real claude
    sup.dirs = [BRIDGE]
    await sup.want(["claude-code-bridge"])
    await until(lambda: sup.states.get("claude-code-bridge", {}).get("state") == "failed")
    assert (
        sup.states["claude-code-bridge"]["detail"]
        == "Install Claude Code and sign in, then Try again"
    )
    await asyncio.sleep(0.5)  # backoff 0.1: a plain crash would have restarted by now
    assert sup.states["claude-code-bridge"]["restarts"] == 0
    assert not (tmp_path / ".helm").exists()
    await sup.retry({"claude-code-bridge": 1})
    await until(lambda: sup.states["claude-code-bridge"]["restarts"] == 1)
    await sup.close()
