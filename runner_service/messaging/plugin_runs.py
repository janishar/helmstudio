"""Plugins that start themselves (contracts/README.md, Plugins: starts itself; API 1.67), only with RUNNER_PLUGINS on.

Every POLL seconds this reads KV host_settings key <host>.plugins (the names studio wants running; missing = none) and
keeps each one's `run.cmd` running from its own folder in RUNNER_PLUGINS_DIRS (first folder wins a name; a real folder,
never a symlink or a .zip): its own process group, a clean env (never the runner's NATS or S3 secrets), output appended
to $HELM_HOME/logs/<name>.log. One that exits is started again after a backoff (1 s doubling to 60 s, reset after 60 s
up); one that leaves the list is stopped (SIGTERM to the group, SIGKILL after 5 s). Each state change is published to
KV registry key <host>.<env>.plugins. The first read after the runner starts starts every plugin on the list.

Each child's pid (= its group) is in $HELM_HOME/run/<name>.pid while it runs: a runner that died without stopping its
plugins (a stall's os._exit, a crash, SIGKILL) leaves them running, so at start and before starting a plugin the runner
stops (SIGTERM, then SIGKILL) a leftover group whose process is still that plugin's (its command holds run.cmd, or its
cwd is the plugin's folder). A `retry` {name: nonce} in the key restarts a waiting plugin at once when its nonce changes.
Run one supervising runner per host (dev's make runners or the Mac app, not both): two would stop each other's plugins."""

import asyncio
import contextlib
import json
import logging
import os
import signal
import subprocess
import sys
from pathlib import Path

from nats.js.errors import NotFoundError

from config import ENV, HOST
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger("plugins")

BUCKET = "host_settings"
WANT_KEY = f"{HOST}.plugins"
KEY = f"{HOST}.{ENV}.plugins"
POLL = 5  # seconds between reads of the key
RETRY = 60  # seconds between tries to open the bucket
LOG_CAP = 5 * 1024 * 1024  # a log over this when a plugin starts is emptied first


class Refused(Exception):
    """Why a plugin can't start (the published `detail`)."""


def resolve(dirs: list[Path], name: str, host: str = HOST) -> tuple[list[str], Path]:
    """(argv, folder) of plugin `name`, or Refused with the reason."""
    zipped = False
    for d in dirs:
        folder = d / name
        zipped = zipped or (d / f"{name}.zip").exists()
        mf = folder / "plugin.json"
        if not folder.is_symlink() and folder.is_dir() and mf.is_file() and not mf.is_symlink():
            break
    else:
        raise Refused(
            "a .zip plugin can't start: unpack it into a folder"
            if zipped
            else "not found in its plugin folders"
        )
    try:
        run = json.loads(mf.read_text()).get("run")
    except OSError, ValueError, AttributeError:
        raise Refused("its plugin.json can't be read") from None
    cmd = run.get("cmd") if isinstance(run, dict) else None
    if not (isinstance(cmd, list) and cmd and all(isinstance(a, str) and a for a in cmd)):
        raise Refused("no run in its plugin.json")
    if (want := run.get("host", "mac")) != host:
        raise Refused(f"its run is for host {want}, not {host}")
    prog, root = cmd[0], folder.resolve()
    if "/" in prog:
        exe = (folder / prog).resolve()
        if prog.startswith("/") or not exe.is_relative_to(root) or not exe.is_file():
            raise Refused(f"run.cmd {prog[:100]} is not a file inside its folder")
        prog = str(exe)
    return [prog, *cmd[1:]], root


def child_env(home: Path) -> dict:
    """A plugin's whole env: never the runner's own (NATS_*, S3_*, …)."""
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin:/usr/sbin:/sbin"),
           "HOME": os.environ.get("HOME", str(Path.home())), "HELM_HOME": str(home)}  # fmt: skip
    if lang := os.environ.get("LANG"):
        env["LANG"] = lang
    return env


def last_line(path: Path) -> str:
    """The log's last non-empty line, ≤ 200 chars."""
    try:
        with path.open("rb") as f:
            f.seek(max(0, f.seek(0, 2) - 4096))
            lines = f.read().decode(errors="replace").splitlines()
    except OSError:
        return ""
    return next((x.strip()[:200] for x in reversed(lines) if x.strip()), "")


def shown(path: Path) -> str:
    """The path as the host sees it, with ~ for the home."""
    home = str(Path.home())
    p = str(path)
    return "~" + p[len(home) :] if p.startswith(home + "/") else p


def signal_group(pid: int, sig: int) -> None:
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(pid, sig)


def command(pid: int) -> str:
    """The process's command line ('' when it is gone)."""
    try:
        return subprocess.run(["ps", "-o", "command=", "-p", str(pid)], capture_output=True, check=False,
                              text=True, timeout=5).stdout.strip()  # fmt: skip
    except OSError, subprocess.SubprocessError:
        return ""


def cwd_of(pid: int) -> str:
    """The process's working folder ('' when unknown)."""
    if sys.platform == "linux":
        with contextlib.suppress(OSError):
            return os.readlink(f"/proc/{pid}/cwd")
        return ""
    try:
        out = subprocess.run(["lsof", "-a", "-d", "cwd", "-p", str(pid), "-Fn"], capture_output=True, check=False,
                             text=True, timeout=5).stdout  # fmt: skip
    except OSError, subprocess.SubprocessError:
        return ""
    return next((x[1:] for x in out.splitlines() if x.startswith("n")), "")


def owned(pid: int, argv: list[str], folder: Path) -> bool:
    """Is pid (still) this plugin's process? Its command holds run.cmd, or (after an exec) its cwd is the folder."""
    cmd = command(pid)
    return bool(cmd) and (" ".join(argv) in cmd or cwd_of(pid) == str(folder))


def alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


class Supervisor:
    """Keeps the wanted plugins running and publishes their states through `publish(value)` (awaitable)."""

    def __init__(self, dirs, home, publish, *, up_s=3.0, backoff=(1.0, 60.0), reset_s=60.0,
                 kill_s=5.0, host=HOST) -> None:  # fmt: skip
        self.dirs, self.home, self.publish = [Path(d) for d in dirs], Path(home), publish
        self.up_s, self.backoff, self.reset_s, self.kill_s, self.host = (
            up_s,
            backoff,
            reset_s,
            kill_s,
            host,
        )
        self.logs, self.run = self.home / "logs", self.home / "run"
        self.kicks: dict[str, asyncio.Event] = {}  # set by a retry: start again now
        self.nonces: dict[str, int] = {}
        self.states: dict[str, dict] = {}
        self.tasks: dict[str, asyncio.Task] = {}
        self.procs: dict[str, asyncio.subprocess.Process] = {}
        self.lock = asyncio.Lock()

    async def set(self, name: str, state: str, detail: str | None = None) -> None:
        """Record a state change and publish every plugin's state (checked against the schema first)."""
        old = self.states.get(name, {})
        self.states[name] = {"state": state, "restarts": old.get("restarts", 0), "since": now(),
                             "log": shown(self.logs / f"{name}.log")} | ({"detail": detail[:300]} if detail else {})  # fmt: skip
        async with self.lock:
            value = {"schema_version": 1, "plugins": dict(self.states),
                     "published_by": f"runner {self.host}.{ENV}", "published_at": now()}  # fmt: skip
            if errs := schemas.registry_errors("plugins", value):
                log.error("plugins value is off-contract, not published: %s", "; ".join(errs[:3]))
                return
            try:
                await self.publish(value)
            except Exception:
                log.warning("could not publish %s", KEY, exc_info=True)

    def forget(self, name: str, pid: int) -> None:
        """Drop the pid file once its child is reaped (not one a later start wrote)."""
        f = self.run / f"{name}.pid"
        with contextlib.suppress(OSError):
            if f.read_text().strip() == str(pid):
                f.unlink()

    async def reap(
        self, name: str, argv: list[str] | None = None, folder: Path | None = None
    ) -> None:
        """Stop the group a dead (or other) runner left for `name`, when its process is still that plugin's."""
        f = self.run / f"{name}.pid"
        try:
            pid = int(f.read_text().strip())
        except OSError, ValueError:
            pid = 0
        if pid and pid not in {p.pid for p in self.procs.values() if p.returncode is None}:
            if argv is None:
                with contextlib.suppress(Refused):
                    argv, folder = resolve(self.dirs, name, self.host)
            if argv and await asyncio.to_thread(owned, pid, argv, folder):
                log.warning("plugin %s was left running by another runner or one that died (group %d): "
                            "stopping it; run one supervising runner per host", name, pid)  # fmt: skip
                signal_group(pid, signal.SIGTERM)
                for _ in range(int(self.kill_s / 0.1)):
                    if not alive(pid):
                        break
                    await asyncio.sleep(0.1)
                signal_group(pid, signal.SIGKILL)
        elif pid:
            return  # our own live child
        with contextlib.suppress(OSError):
            f.unlink()

    async def reap_all(self) -> None:
        """At start: the groups every leftover pid file names."""
        for f in sorted(self.run.glob("*.pid")):
            await self.reap(f.stem)

    async def retry(self, nonces: dict) -> None:
        """A changed nonce: reset that plugin's backoff and start it now if it waits."""
        for name, n in nonces.items():
            if self.nonces.get(name) != n and name in self.kicks:
                self.kicks[name].set()
            self.nonces[name] = n

    async def want(self, names: list[str]) -> None:
        """Start what is new on the list, stop what left it."""
        for name in [n for n in self.tasks if n not in names]:
            await self.stop(name)
        for name in names:
            if name not in self.tasks:
                self.states.pop(name, None)  # a fresh start: restarts from 0
                self.tasks[name] = asyncio.create_task(self.keep(name))

    async def keep(self, name: str) -> None:
        """Run `name` until cancelled, again after the backoff each time it exits or can't start."""
        loop, delay, stops = asyncio.get_running_loop(), self.backoff[0], 0
        kick = self.kicks[name] = asyncio.Event()
        self.logs.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.run.mkdir(parents=True, exist_ok=True, mode=0o700)
        path = self.logs / f"{name}.log"
        while True:
            try:
                argv, cwd = resolve(self.dirs, name, self.host)
                await self.reap(name, argv, cwd)
                with contextlib.suppress(FileNotFoundError):
                    if path.stat().st_size > LOG_CAP:
                        path.write_bytes(b"")
                with path.open("ab") as out:
                    proc = self.procs[name] = await asyncio.create_subprocess_exec(
                        *argv, cwd=cwd, env=child_env(self.home), stdin=asyncio.subprocess.DEVNULL,
                        stdout=out, stderr=asyncio.subprocess.STDOUT, start_new_session=True)  # fmt: skip
                (self.run / f"{name}.pid").write_text(str(proc.pid))
            except Refused as e:
                await self.set(name, "failed", str(e))
            except OSError as e:
                await self.set(name, "failed", f"it can't start: {e}")
            else:
                started = loop.time()
                await self.set(name, "starting")
                try:
                    await asyncio.wait_for(asyncio.shield(proc.wait()), self.up_s)
                except TimeoutError:
                    await self.set(name, "running")
                    await proc.wait()
                signal_group(
                    proc.pid, signal.SIGKILL
                )  # what it left behind (a server holding its port)
                self.forget(name, proc.pid)
                stops += 1
                if loop.time() - started >= self.reset_s:
                    delay = self.backoff[0]
                log.warning("plugin %s exited %s", name, proc.returncode)
                await self.set(
                    name,
                    "failed",
                    f"it stopped {stops} times: exit {proc.returncode}: {last_line(path)}",
                )
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(kick.wait(), delay)
            delay = self.backoff[0] if kick.is_set() else min(delay * 2, self.backoff[1])
            kick.clear()
            self.states[name]["restarts"] += 1

    async def stop(self, name: str) -> None:
        """SIGTERM to its group, SIGKILL after kill_s; state stopped."""
        task = self.tasks.pop(name)
        self.kicks.pop(name, None)
        task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await task
        proc = self.procs.pop(name, None)
        if proc and proc.returncode is None:
            signal_group(proc.pid, signal.SIGTERM)
            try:
                await asyncio.wait_for(proc.wait(), self.kill_s)
            except TimeoutError:
                signal_group(proc.pid, signal.SIGKILL)
                await proc.wait()
        if proc:
            self.forget(name, proc.pid)
        await self.set(name, "stopped")

    async def close(self) -> None:
        """Stop every plugin (the runner is stopping)."""
        await asyncio.gather(*(self.stop(n) for n in list(self.tasks)))

    async def offer(self, raw: bytes | None) -> None:
        """A read of the key (None: missing = none); off-contract keeps the current set."""
        if raw is None:
            return await self.want([])
        try:
            value = json.loads(raw)
        except ValueError:
            value = None
        # ponytail: retry read leniently (checked here) until gen's schema has it; then validate the whole value
        retry = value.pop("retry", {}) if isinstance(value, dict) else {}
        if not (isinstance(retry, dict) and all(type(n) is int for n in retry.values())):
            retry = {}
        if errs := schemas.registry_errors("plugin_runs", value):
            log.error(
                "host_settings %s is off-contract, ignored: %s", WANT_KEY, "; ".join(errs[:3])
            )
            return
        await self.want(value["plugins"])
        await self.retry(retry)

    async def watch(self, js, stop: asyncio.Event) -> None:
        """Read the key every POLL seconds until `stop` (a direct get, as messaging.settings)."""
        kv = None
        await self.reap_all()
        while not stop.is_set():
            wait = POLL
            try:
                kv = kv or await js.key_value(BUCKET)
                try:
                    raw = (await kv.get(WANT_KEY)).value or None
                except NotFoundError:
                    raw = None
            except Exception:  # noqa: BLE001 (no bucket yet, NATS away)
                log.warning("bucket %s not available; retrying in %d s", BUCKET, RETRY)
                kv, wait = None, RETRY
            else:
                try:
                    await self.offer(raw)
                except Exception:
                    log.exception("plugins could not be applied")
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), wait)
