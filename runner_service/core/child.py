"""A stage's child process (the native runtime, contracts/README.md "The native runtime"): a binary that loads its
own weights (h3, zonos3) runs in its own session while the GPU thread waits on it. `name` is the binary's own
prefix: its stderr lines `<name>: ` are its diagnostics, its environment knobs `<NAME>_*` pass through.

- `run()` waits for the child to exit (h3: one take per process); `serve()` starts a server child, waits for its ready
  line and hands the stage its port for the body of a `with` (zonos3-server: many lines in one load);

- its stderr is read by a thread, split on \\r and \\n; a line `<phase> <n>/<total>` (anything after it is ignored:
  macOS 27 can glue a system log line on) is progress, mapped through the pack's `phases` (h3 phase → runner phase,
  or (runner phase, phase id): API 1.36, the node's model.yaml `phases`; unknown ones pass as they are) and handed
  to ctx.progress on the GPU thread, at most every POLL s;
- the GPU thread polls ctx.check() every POLL s; on Cancelled or RunError (cancel, shutdown) the child's process
  group gets SIGTERM, SIGKILL after KILL_S, and the exception goes on;
- it has its own idle limit: polling feeds the stall watchdog, so a hung child is failed here when no progress line
  came for `idle_s`;
- a non-zero exit fails the run with the child's last `<name>: ` line (paths replaced, as core.media.last_line); a
  server (serve(), zonos3) also its last `error: …` or `failed …` line, never a load report;
- the live child's pid is in LIVE (loaders_native.measure_gb adds its footprint) and in a pid file under
  RUNNER_CACHE; `sweep()` at start kills a child a killed runner left behind (macOS has no parent-death signal),
  only when that pid still runs the same binary and has lost its runner (parent pid 1: never a live runner's child,
  say another runner of this env sharing RUNNER_CACHE)."""

import collections
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
from core.graph import Cancelled, RunError
from core.media import last_line

log = logging.getLogger("child")

POLL = 0.5  # seconds between cancel points and progress forwards
KILL_S = 3.0  # SIGTERM → SIGKILL
PROGRESS = re.compile(r"^(.*?)\s+(\d+)/(\d+)(?:\s|$)")
INFO = re.compile(
    r"\b(weights|loaded)\b.*\btensors\b"
)  # a loose child's load report, never a reason
LIVE: dict = {"pid": None}  # the running child, read by loaders_native.measure_gb
# the child's environment: what a native binary needs, never the runner's secrets (NATS_URL, S3 keys)
KEEP = ("PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "USER")


def pid_file() -> Path:
    return config.CACHE / f"child-{config.ENV}.pid"


def child_env(name: str) -> dict[str, str]:
    """The runner's environment cut to KEEP plus the binary's own knobs (<NAME>_*: H3_*, ZONOS3_*)."""
    knobs = f"{name.upper()}_"
    return {k: v for k, v in os.environ.items() if k in KEEP or k.startswith(knobs)}


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


class _Child:
    """A started child: its process (own session), a thread reading its stderr into progress, diagnostics and the
    ready line, LIVE and the pid file set; stop() kills the group (if still running) and clears them."""

    def __init__(self, argv: list[str], cwd: Path, name: str, phases: dict[str, str | tuple] | None = None,
                 ready: re.Pattern | None = None, loose: bool = False, env: dict | None = None) -> None:  # fmt: skip
        self.proc = subprocess.Popen(argv, cwd=cwd, env=child_env(name) | (env or {}), stdin=subprocess.DEVNULL,
                                     stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, start_new_session=True)  # fmt: skip
        LIVE["pid"] = self.proc.pid
        with contextlib.suppress(OSError):
            pid_file().parent.mkdir(parents=True, exist_ok=True)
            pid_file().write_text(f"{self.proc.pid} {argv[0]}")
        self.name, self.diag_prefix, self.phases, self.ready_re = (
            name,
            f"{name}: ",
            phases or {},
            ready,
        )
        self.seen, self.progress, self.diag, self.ready = time.monotonic(), None, [], None
        self.tail = collections.deque(
            maxlen=20
        )  # every stderr line's last: logged when a server fails
        self.loose = loose
        self.lock = threading.Lock()
        self.reader = threading.Thread(target=self.read, name="child-stderr", daemon=True)
        self.reader.start()

    def take(self, line: str) -> None:
        m = PROGRESS.match(line)
        with self.lock:
            if line:
                self.tail.append(line)
            if m and not line.startswith(self.name):
                phase = m.group(1).strip()
                # `<phase> 0/N` only opens a phase: evt.run.progress counts from 1
                if int(m.group(2)) >= 1:
                    to = self.phases.get(phase, phase)  # a phase text, or (text, phase id)
                    self.progress = (
                        int(m.group(2)),
                        int(m.group(3)),
                        *((to,) if isinstance(to, str) else to),
                    )
                self.seen = time.monotonic()
            elif self.diagnostic(line):
                self.diag = [*self.diag[-9:], line]
            if self.ready is None and self.ready_re and (r := self.ready_re.search(line)):
                self.ready = r

    def diagnostic(self, line: str) -> bool:
        """A line that can say why the child failed: `<name>: ` (not a warning); a loose child (a server: zonos3)
        also `error: …` and `failed …`, but not its load reports (`<name>: backend = …`, `… N tensors …`)."""
        if self.loose and (line.startswith(f"{self.name}: backend = ") or INFO.search(line)):
            return False
        if line.startswith(self.diag_prefix):
            return not line.startswith(f"{self.name}: warning")
        return self.loose and line.lower().startswith(("error:", "failed"))

    def read(self) -> None:
        buf = b""
        while chunk := self.proc.stderr.read1(4096):
            parts = re.split(rb"[\r\n]", buf + chunk)
            buf = parts.pop()
            for raw in parts:
                self.take(raw.decode(errors="replace").strip())
            # h3 writes "\r<phase> %4d/%-4d" (\r first, total padded): a padded update left in buf is whole, so it
            # counts now, not when the next one comes; buf stays (that \r re-reads it, the same progress)
            tail = buf.decode(errors="replace")
            if tail[-1:].isspace() and not tail.lstrip().startswith(self.name):
                self.take(tail.strip())

    def gone(self, timeout: float | None) -> bool:
        try:
            self.proc.wait(timeout)
        except subprocess.TimeoutExpired:
            return False
        return True

    def stop(self) -> None:
        if self.proc.poll() is None:
            _kill(self.proc.pid, self.gone)
        self.reader.join(5)
        self.proc.stderr.close()
        LIVE["pid"] = None
        with contextlib.suppress(OSError):
            pid_file().unlink(missing_ok=True)

    def why(self) -> str:
        """The failure of an exited child: killed by a signal, its last diagnostic line, or its status."""
        self.reader.join(5)  # its last lines first (the pipe ends with it)
        code = self.proc.returncode
        if code < 0:
            return f"killed by signal {-code}"  # jetsam or another process: no line of its own
        with self.lock:
            diag = self.diag
        if not diag and self.loose:  # a server (SGLang) prints tracebacks: its last line says why
            diag = list(self.tail)
        return last_line("\n".join(diag)) if diag else f"exited with status {code}"


def run(ctx, argv: list[str], *, cwd: Path, name: str, phases: dict[str, str | tuple], idle_s: float,
        env: dict[str, str] | None = None) -> None:  # fmt: skip
    """Run `argv` (argv[0] an absolute binary path) in `cwd` until it exits 0; RunError otherwise (see the module
    doc). Every path in `argv` must be absolute (cwd is the binary's folder). `env` is merged over child_env (the
    stage's own knobs for this run: MiniMax H3's H3_LORA)."""
    c = _Child(argv, cwd, name, phases, env=env)
    sent = None
    try:
        while not c.gone(POLL):
            ctx.check()
            with c.lock:
                progress, quiet = c.progress, time.monotonic() - c.seen
            if progress is not None and progress != sent:
                ctx.progress(*progress)
                sent = progress
            if quiet > idle_s:
                raise RunError(f"{ctx.node}: the child made no progress for {quiet:.0f} s")
    finally:  # Cancelled, RunError or anything else on the way out: the whole group goes
        c.stop()
    if not c.proc.returncode:
        if c.progress not in (None, sent):  # the last line before a quick exit
            ctx.progress(*c.progress)
        return
    raise RunError(f"{ctx.node}: {c.why()}")


@contextlib.contextmanager
def serve(
    ctx,
    argv: list[str],
    *,
    cwd: Path,
    name: str,
    ready: re.Pattern,
    ready_s: float,
    env: dict[str, str] | None = None,
):
    """Start the server `argv` (as run(): absolute paths) and wait for its ready line, `ready` with the port as its
    group 1 (polling ctx.check() meanwhile), then yield the port for the body; the process group is killed when the
    body ends, raises or is cancelled. No ready line within `ready_s`, or an exit before it: RunError. The body's
    error when the server died under it: RunError with the server's reason instead."""
    c = _Child(argv, cwd, name, ready=ready, loose=True, env=env)
    try:
        until = time.monotonic() + ready_s
        while True:
            with c.lock:
                m = c.ready
            if m:
                break
            if c.gone(POLL):
                raise RunError(f"{ctx.node}: {c.why()}")
            ctx.check()
            if time.monotonic() > until:
                raise RunError(f"{ctx.node}: the server was not ready within {ready_s:.0f} s")
        try:
            yield int(m.group(1))
        except Exception as e:
            # the server died under the body (a refused connection, a timeout the stage turned into a RunError):
            # its own reason, not the body's; a cancel stays a cancel
            if c.proc.poll() is not None and not isinstance(e, Cancelled):
                raise RunError(f"{ctx.node}: {c.why()}") from e
            raise
    except RunError:
        c.reader.join(5)
        log.error("%s failed; its last stderr lines:\n%s", name, "\n".join(c.tail))
        raise
    finally:
        c.stop()
