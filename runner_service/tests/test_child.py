"""core.child (the native runtime's child process) with a fake child: progress lines (split on \\r, mapped, glued
log text ignored), cancel kills the whole process group, a non-zero exit's last `h3: ` line without paths, the idle
limit, the stale-child sweep, and an environment without the runner's secrets."""

import contextlib
import os
import shutil
import subprocess
import sys
import time

import pytest

from core import child
from core.graph import Cancelled, RunError


class Ctx:
    def __init__(self, cancel_after: int | None = None) -> None:
        self.node, self.calls, self.seen, self.cancel_after = "gen", 0, [], cancel_after

    def check(self) -> None:
        self.calls += 1
        if self.cancel_after is not None and self.calls >= self.cancel_after:
            raise Cancelled

    def progress(self, step: int, of: int, phase: str | None = None) -> None:
        self.seen.append((step, of, phase))


@pytest.fixture(autouse=True)
def fast(monkeypatch, tmp_path):
    monkeypatch.setattr(child, "POLL", 0.05)
    monkeypatch.setattr(child, "KILL_S", 1.0)
    monkeypatch.setattr(child.config, "CACHE", tmp_path / "cache")


def py(code: str) -> list[str]:
    return [sys.executable, "-c", code]


def gone(pid: int, within: float = 3.0) -> bool:
    until = time.monotonic() + within
    while time.monotonic() < until:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return True
        r = subprocess.run(
            ["ps", "-p", str(pid), "-o", "stat="], capture_output=True, text=True, check=False
        )
        if r.stdout.strip().startswith("Z"):  # a zombie waiting for its parent: dead
            return True
        time.sleep(0.05)
    return False


def test_progress_is_mapped_and_a_clean_exit_returns(tmp_path):
    code = r"""
import sys, time
w = sys.stderr.write
w("tokenizer                    1/1   \n")
w("denoise enqueue              0/4   \n"); sys.stderr.flush(); time.sleep(0.6)
w("\rdenoise                      1/4   "); sys.stderr.flush(); time.sleep(0.2)
w("\rdenoise                      4/4   2026-11-19 Failed to import MPS module\n"); sys.stderr.flush(); time.sleep(0.2)
w("h3: warning: --reuse with only 4 denoising steps\n")
w("odd 3/x line\n")
w("h3: wrote /tmp/out.mp4\n")
"""
    ctx = Ctx()
    child.run(ctx, py(code), cwd=tmp_path, phases={"denoise": "sampling"}, idle_s=10)
    assert (4, 4, "sampling") in ctx.seen and ctx.calls > 0
    assert all(p in ("tokenizer", "sampling") for _, _, p in ctx.seen)
    assert all(n >= 1 for n, _, _ in ctx.seen)  # `0/N` opens a phase; progress counts from 1
    assert child.LIVE["pid"] is None and not child.pid_file().exists()


def test_an_update_counts_before_the_next_one_comes(tmp_path):
    # h3's "\r<phase> n/total" has its \r first: the update must not wait in the buffer for the next \r
    code = r"""
import sys, time
sys.stderr.write("\rdenoise                      1/4   "); sys.stderr.flush(); time.sleep(30)
"""

    class Stop(Ctx):
        def check(self) -> None:
            if self.seen:
                raise Cancelled

    ctx = Stop()
    with pytest.raises(
        Cancelled
    ):  # late: no line is ever parsed, so the idle limit fails it instead
        child.run(ctx, py(code), cwd=tmp_path, phases={"denoise": "sampling"}, idle_s=3)
    assert ctx.seen == [(1, 4, "sampling")]


def test_cancel_kills_the_process_group(tmp_path):
    grand = tmp_path / "grand.pid"
    code = f"""
import subprocess, sys, time
p = subprocess.Popen(["sleep", "60"])
open({str(grand)!r}, "w").write(str(p.pid))
while True:
    sys.stderr.write("\\rdenoise 1/9 "); sys.stderr.flush(); time.sleep(0.05)
"""
    ctx = Ctx(cancel_after=20)
    with pytest.raises(Cancelled):
        child.run(ctx, py(code), cwd=tmp_path, phases={}, idle_s=10)
    assert grand.is_file() and gone(
        int(grand.read_text())
    )  # the child's own child (h3's ffmpeg) too
    assert child.LIVE["pid"] is None


def test_a_child_ignoring_sigterm_is_killed(tmp_path):
    code = "import signal, time\nsignal.signal(signal.SIGTERM, signal.SIG_IGN)\ntime.sleep(60)"
    t = time.monotonic()
    with pytest.raises(Cancelled):
        child.run(Ctx(cancel_after=10), py(code), cwd=tmp_path, phases={}, idle_s=10)
    assert time.monotonic() - t < 5


def test_nonzero_exit_fails_with_the_last_h3_line_without_paths(tmp_path):
    code = r"""
import sys
sys.stderr.write("h3: loading /Users/me/GenAI/models/MiniMax-H3\n")
sys.stderr.write("h3: cannot open /private/tmp/h3-x/input-0.png: No such file\n")
sys.stderr.write("some trailing noise\n")
sys.exit(1)
"""
    with pytest.raises(RunError) as e:
        child.run(Ctx(), py(code), cwd=tmp_path, phases={}, idle_s=10)
    assert str(e.value) == "gen: h3: cannot open <file>: No such file"
    with pytest.raises(RunError, match=r"^gen: exited with status 2$"):
        child.run(Ctx(), py("import sys; sys.exit(2)"), cwd=tmp_path, phases={}, idle_s=10)
    with pytest.raises(RunError, match=r"^gen: killed by signal 9$"):
        child.run(Ctx(), py("import os, signal; os.kill(os.getpid(), signal.SIGKILL)"), cwd=tmp_path,
                  phases={}, idle_s=10)  # fmt: skip


def test_a_silent_child_fails_at_the_idle_limit(tmp_path):
    pidf = tmp_path / "me.pid"
    code = f"import os, time\nopen({str(pidf)!r}, 'w').write(str(os.getpid()))\ntime.sleep(60)"
    with pytest.raises(RunError, match=r"^gen: the child made no progress for \d+ s$"):
        child.run(Ctx(), py(code), cwd=tmp_path, phases={}, idle_s=0.5)
    assert gone(int(pidf.read_text()))


def test_the_sweep_kills_only_the_same_binary(tmp_path):
    if not shutil.which("cc"):
        pytest.skip("no C compiler")
    fake = tmp_path / "h3"  # a binary of its own (a copied system binary is killed by macOS)
    src = b"#include <unistd.h>\nint main(void) { sleep(60); return 0; }\n"
    subprocess.run(["cc", "-x", "c", "-", "-o", str(fake)], input=src, check=True)
    other = tmp_path / "other"
    live = subprocess.Popen(
        [str(fake), "60"], start_new_session=True
    )  # a live runner's child (its parent: us)
    spawn = f"import subprocess as s; print(s.Popen([{str(fake)!r}], start_new_session=True, stdout=s.DEVNULL, stderr=s.DEVNULL).pid)"
    orphan = int(subprocess.run(py(spawn), capture_output=True, text=True, check=True).stdout)
    # its parent exited: parent pid 1, as when its runner was killed
    try:
        child.pid_file().parent.mkdir(parents=True)
        child.pid_file().write_text(f"{orphan} {other}")  # the pid now runs another binary: kept
        child.sweep()
        assert not gone(orphan, 0.3) and not child.pid_file().exists()
        child.pid_file().write_text(
            f"{live.pid} {fake}"
        )  # same binary, its runner alive (another runner's): kept
        child.sweep()
        assert live.poll() is None and not child.pid_file().exists()
        child.pid_file().write_text(f"{orphan} {fake}")
        child.sweep()
        assert gone(orphan) and not child.pid_file().exists()
    finally:
        live.kill()
        with contextlib.suppress(ProcessLookupError):
            os.kill(orphan, 9)
    child.sweep()  # no file: nothing


def test_the_child_gets_no_secrets(monkeypatch):
    monkeypatch.setenv("NATS_URL", "nats://runner:secret@nats:4222")
    monkeypatch.setenv("S3_SECRET_ACCESS_KEY", "x")
    monkeypatch.setenv("H3_ZERO_COPY_WEIGHTS", "0")
    env = child.child_env()
    assert "NATS_URL" not in env and "S3_SECRET_ACCESS_KEY" not in env
    assert env["H3_ZERO_COPY_WEIGHTS"] == "0" and "PATH" in env
