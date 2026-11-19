"""A stage's child process (the native runtime, contracts/README.md "The native runtime"): a binary that loads its
own weights (h3) runs in its own session while the GPU thread waits on it.

- its stderr is read by a thread, split on \\r and \\n; a line `<phase> <n>/<total>` (anything after it is ignored:
  macOS 27 can glue a system log line on) is progress, mapped through the pack's `phases` (h3 phase → runner phase;
  unknown ones pass as they are) and handed to ctx.progress on the GPU thread, at most every POLL s;
- the GPU thread polls ctx.check() every POLL s; on Cancelled or RunError (cancel, shutdown) the child's process
  group gets SIGTERM, SIGKILL after KILL_S, and the exception goes on;
- it has its own idle limit: polling feeds the stall watchdog, so a hung child is failed here when no progress line
  came for `idle_s`;
- a non-zero exit fails the run with the child's last `h3: ` line (paths replaced, as core.media.last_line);
- the live child's pid is in LIVE (loaders_native.measure_gb adds its footprint) and in a pid file under
  RUNNER_CACHE; `sweep()` at start kills a child a killed runner left behind (macOS has no parent-death signal),
  only when that pid still runs the same binary and has lost its runner (parent pid 1: never a live runner's child,
  say another runner of this env sharing RUNNER_CACHE)."""

import contextlib
import logging
import os
import re
import signal
import subprocess
import threading
import time
from pathlib import Path

import config
from core.graph import RunError
from core.media import last_line

log = logging.getLogger("child")

POLL = 0.5  # seconds between cancel points and progress forwards
KILL_S = 3.0  # SIGTERM → SIGKILL
PROGRESS = re.compile(r"^(.*?)\s+(\d+)/(\d+)(?:\s|$)")
LIVE: dict = {"pid": None}  # the running child, read by loaders_native.measure_gb
# the child's environment: what a native binary needs, never the runner's secrets (NATS_URL, S3 keys)
KEEP = ("PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "USER")


def pid_file() -> Path:
    return config.CACHE / f"child-{config.ENV}.pid"


def child_env() -> dict[str, str]:
    """The runner's environment cut to KEEP plus the binary's own knobs (H3_*)."""
    return {k: v for k, v in os.environ.items() if k in KEEP or k.startswith("H3_")}


def _kill(pid: int, wait) -> None:
    """SIGTERM to the process group `pid`, SIGKILL after KILL_S; `wait(timeout)` is True once it is gone. A group
    left with only zombies answers EPERM on macOS: gone too."""
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(pid, signal.SIGTERM)
        if not wait(KILL_S):
            os.killpg(pid, signal.SIGKILL)
            wait(None)


def sweep() -> None:
    """Kill the child a previous runner of this env left running (its pid file), only when that pid still runs the
    binary the file names and is an orphan (parent pid 1); then forget it."""
    f = pid_file()
    try:
        pid, binary = f.read_text().split(" ", 1)
        pid = int(pid)
    except OSError, ValueError:
        return
    ps = subprocess.run(["ps", "-p", str(pid), "-o", "ppid=,command="], capture_output=True, text=True,
                        check=False).stdout.split(None, 1)  # fmt: skip
    if len(ps) == 2 and ps[0] == "1" and ps[1].startswith(binary.strip()):
        log.warning("killing a stale child %d (%s)", pid, Path(binary).name)

        def gone(timeout: float | None) -> bool:
            until = time.monotonic() + (timeout if timeout is not None else 30)
            while time.monotonic() < until:
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    return True
                time.sleep(0.1)
            return False

        _kill(pid, gone)
    f.unlink(missing_ok=True)


def run(ctx, argv: list[str], *, cwd: Path, phases: dict[str, str], idle_s: float) -> None:
    """Run `argv` (argv[0] an absolute binary path) in `cwd` until it exits 0; RunError otherwise (see the module
    doc). Every path in `argv` must be absolute (cwd is the binary's folder)."""
    proc = subprocess.Popen(argv, cwd=cwd, env=child_env(), stdin=subprocess.DEVNULL,
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, start_new_session=True)  # fmt: skip
    LIVE["pid"] = proc.pid
    with contextlib.suppress(OSError):
        pid_file().parent.mkdir(parents=True, exist_ok=True)
        pid_file().write_text(f"{proc.pid} {argv[0]}")
    state = {"seen": time.monotonic(), "progress": None, "diag": []}
    lock = threading.Lock()

    def take(line: str) -> None:
        m = PROGRESS.match(line)
        with lock:
            if m and not line.startswith("h3"):
                phase = m.group(1).strip()
                # `<phase> 0/N` only opens a phase: evt.run.progress counts from 1
                if int(m.group(2)) >= 1:
                    state["progress"] = (int(m.group(2)), int(m.group(3)), phases.get(phase, phase))
                state["seen"] = time.monotonic()
            elif line.startswith("h3: ") and not line.startswith("h3: warning"):
                state["diag"] = [*state["diag"][-9:], line]

    def read() -> None:
        buf = b""
        while chunk := proc.stderr.read1(4096):
            parts = re.split(rb"[\r\n]", buf + chunk)
            buf = parts.pop()
            for raw in parts:
                take(raw.decode(errors="replace").strip())
            # h3 writes "\r<phase> %4d/%-4d" (\r first, total padded): a padded update left in buf is whole, so it
            # counts now, not when the next one comes; buf stays (that \r re-reads it, the same progress)
            tail = buf.decode(errors="replace")
            if tail[-1:].isspace() and not tail.lstrip().startswith("h3"):
                take(tail.strip())

    reader = threading.Thread(target=read, name="child-stderr", daemon=True)
    reader.start()

    def gone(timeout: float | None) -> bool:
        try:
            proc.wait(timeout)
        except subprocess.TimeoutExpired:
            return False
        return True

    sent = None
    try:
        while not gone(POLL):
            ctx.check()
            with lock:
                progress, quiet = state["progress"], time.monotonic() - state["seen"]
            if progress is not None and progress != sent:
                ctx.progress(*progress)
                sent = progress
            if quiet > idle_s:
                raise RunError(f"{ctx.node}: the child made no progress for {quiet:.0f} s")
    finally:  # Cancelled, RunError or anything else on the way out: the whole group goes
        if proc.poll() is None:
            _kill(proc.pid, gone)
        reader.join(5)
        proc.stderr.close()
        LIVE["pid"] = None
        with contextlib.suppress(OSError):
            pid_file().unlink(missing_ok=True)
    if not proc.returncode:
        if state["progress"] not in (None, sent):  # the last line before a quick exit
            ctx.progress(*state["progress"])
        return
    with lock:
        diag = state["diag"]
    why = last_line("\n".join(diag)) if diag else f"exited with status {proc.returncode}"
    if proc.returncode < 0:
        why = (
            f"killed by signal {-proc.returncode}"  # jetsam or another process: no line of its own
        )
    raise RunError(f"{ctx.node}: {why}")
