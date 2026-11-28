"""core.child (the native runtime's child process) with a fake child: progress lines (split on \\r, mapped, glued
log text ignored), cancel kills the whole process group, a non-zero exit's last `h3: ` line without paths, the idle
limit, the stale-child sweep, an environment without the runner's secrets, and serve(): the ready line's port, its
timeout, an exit before it, a server dying under the body, and the group killed at the end."""

import contextlib
import os
import re
import shutil
import subprocess
import sys
import time
import types

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
        r = subprocess.run(["ps", "-p", str(pid), "-o", "stat="], capture_output=True, text=True, check=False)
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
    child.run(ctx, py(code), cwd=tmp_path, name="h3", phases={"denoise": "sampling"}, idle_s=10)
    assert (4, 4, "sampling") in ctx.seen and ctx.calls > 0
    assert all(p in ("tokenizer", "sampling") for _, _, p in ctx.seen)
    assert all(n >= 1 for n, _, _ in ctx.seen)  # `0/N` opens a phase; progress counts from 1
    assert child.LIVE["pid"] is None and not child.pid_file().exists()


def test_a_phase_maps_to_its_text_and_phase_id(tmp_path):
    """API 1.36: a `phases` value (text, phase id) reaches ctx.progress as both; a plain one as the text alone."""
    seen = []
    ctx = types.SimpleNamespace(node="gen", check=lambda: None, progress=lambda *a: seen.append(a))
    code = r"""
import sys, time
sys.stderr.write("tokenizer 1/1\n"); sys.stderr.flush(); time.sleep(0.3)
sys.stderr.write("audio VAE 2/3\n"); sys.stderr.flush(); time.sleep(0.3)
"""
    child.run(ctx, py(code), cwd=tmp_path, name="h3", idle_s=10,
              phases={"tokenizer": "encoding", "audio VAE": ("decoding audio", "decode_audio")})  # fmt: skip
    assert seen == [(1, 1, "encoding"), (2, 3, "decoding audio", "decode_audio")]


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
    with pytest.raises(Cancelled):  # late: no line is ever parsed, so the idle limit fails it instead
        child.run(ctx, py(code), cwd=tmp_path, name="h3", phases={"denoise": "sampling"}, idle_s=3)
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
        child.run(ctx, py(code), cwd=tmp_path, name="h3", phases={}, idle_s=10)
    assert grand.is_file() and gone(int(grand.read_text()))  # the child's own child (h3's ffmpeg) too
    assert child.LIVE["pid"] is None


def test_a_child_ignoring_sigterm_is_killed(tmp_path):
    code = "import signal, time\nsignal.signal(signal.SIGTERM, signal.SIG_IGN)\ntime.sleep(60)"
    t = time.monotonic()
    with pytest.raises(Cancelled):
        child.run(Ctx(cancel_after=10), py(code), cwd=tmp_path, name="h3", phases={}, idle_s=10)
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
        child.run(Ctx(), py(code), cwd=tmp_path, name="h3", phases={}, idle_s=10)
    assert str(e.value) == "gen: h3: cannot open <file>: No such file"
    with pytest.raises(RunError, match=r"^gen: exited with status 2$"):
        child.run(Ctx(), py("import sys; sys.exit(2)"), cwd=tmp_path, name="h3", phases={}, idle_s=10)
    with pytest.raises(RunError, match=r"^gen: killed by signal 9$"):
        child.run(Ctx(), py("import os, signal; os.kill(os.getpid(), signal.SIGKILL)"), cwd=tmp_path, name="h3",
                  phases={}, idle_s=10)  # fmt: skip


def test_a_silent_child_fails_at_the_idle_limit(tmp_path):
    pidf = tmp_path / "me.pid"
    code = f"import os, time\nopen({str(pidf)!r}, 'w').write(str(os.getpid()))\ntime.sleep(60)"
    with pytest.raises(RunError, match=r"^gen: the child made no progress for \d+ s$"):
        child.run(Ctx(), py(code), cwd=tmp_path, name="h3", phases={}, idle_s=0.5)
    assert gone(int(pidf.read_text()))


def test_the_sweep_kills_only_the_same_binary(tmp_path):
    if not shutil.which("cc"):
        pytest.skip("no C compiler")
    fake = tmp_path / "h3"  # a binary of its own (a copied system binary is killed by macOS)
    src = b"#include <unistd.h>\nint main(void) { sleep(60); return 0; }\n"
    subprocess.run(["cc", "-x", "c", "-", "-o", str(fake)], input=src, check=True)
    other = tmp_path / "other"
    live = subprocess.Popen([str(fake), "60"], start_new_session=True)  # a live runner's child (its parent: us)
    spawn = f"import subprocess as s; print(s.Popen([{str(fake)!r}], start_new_session=True, stdout=s.DEVNULL, stderr=s.DEVNULL).pid)"
    orphan = int(subprocess.run(py(spawn), capture_output=True, text=True, check=True).stdout)
    # its parent exited: parent pid 1, as when its runner was killed
    try:
        child.pid_file().parent.mkdir(parents=True)
        child.pid_file().write_text(f"{orphan} {other}")  # the pid now runs another binary: kept
        child.sweep()
        assert not gone(orphan, 0.3) and not child.pid_file().exists()
        child.pid_file().write_text(f"{live.pid} {fake}")  # same binary, its runner alive (another runner's): kept
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
    env = child.child_env("h3")
    assert "NATS_URL" not in env and "S3_SECRET_ACCESS_KEY" not in env
    assert env["H3_ZERO_COPY_WEIGHTS"] == "0" and "PATH" in env
    monkeypatch.setenv("ZONOS3_FFMPEG", "/opt/ffmpeg")
    env = child.child_env("zonos3")
    assert env["ZONOS3_FFMPEG"] == "/opt/ffmpeg" and "H3_ZERO_COPY_WEIGHTS" not in env


READY = re.compile(r"zonos3-server: listening on http://127\.0\.0\.1:(\d+)")


def server(tmp_path, ready: bool = True, then: str = "time.sleep(60)") -> list[str]:
    """A fake zonos3-server: Metal noise, the ready line (or not), then `then`; its pid in me.pid."""
    line = 'w("zonos3-server: listening on http://127.0.0.1:4321  (studio: not built)\\n")' if ready else "pass"
    return py(f"""
import os, sys, time
open({str(tmp_path / "me.pid")!r}, "w").write(str(os.getpid()))
w = sys.stderr.write
w("ggml_metal_init: found device\\n"); w("zonos3: backend = MTL0\\n"); sys.stderr.flush(); time.sleep(0.3)
{line}; sys.stderr.flush()
{then}
""")


def serve(tmp_path, ctx=None, ready_s: float = 10, **kw):
    return child.serve(ctx or Ctx(), server(tmp_path, **kw), cwd=tmp_path, name="zonos3", ready=READY, ready_s=ready_s)


def last_pid(tmp_path) -> int:
    return int((tmp_path / "me.pid").read_text())


def test_serve_yields_the_port_and_kills_the_server_at_the_end(tmp_path):
    with serve(tmp_path) as port:
        pid = last_pid(tmp_path)
        assert port == 4321 and child.LIVE["pid"] == pid and not gone(pid, 0.2)
    assert gone(pid) and child.LIVE["pid"] is None and not child.pid_file().exists()
    with pytest.raises(Cancelled), serve(tmp_path):  # cancel inside the body: the server goes too
        raise Cancelled
    assert gone(last_pid(tmp_path))


def test_serve_without_a_ready_line_times_out_or_is_cancelled(tmp_path):
    with pytest.raises(RunError, match=r"^gen: the server was not ready within 1 s$"), serve(tmp_path, ready_s=1,
                                                                                             ready=False):  # fmt: skip
        pytest.fail("no body without a ready line")
    assert gone(last_pid(tmp_path))
    with pytest.raises(Cancelled), serve(tmp_path, Ctx(cancel_after=3), ready=False):
        pass
    assert gone(last_pid(tmp_path)) and child.LIVE["pid"] is None


def test_serve_reports_the_servers_last_line_when_it_dies(tmp_path):
    die = 'w("zonos3: cannot open /Users/me/models/zac-keep2.gguf\\n"); sys.stderr.flush(); sys.exit(1)'
    why = r"^gen: zonos3: cannot open <file>$"
    with (
        pytest.raises(RunError, match=why),
        serve(tmp_path, ready=False, then=die),
    ):  # before the ready line
        pass
    with (
        pytest.raises(RunError, match=why),
        serve(tmp_path, then=die),
    ):  # under the body: a refused request
        time.sleep(1)
        raise ConnectionRefusedError


def test_a_server_fails_with_its_error_line_not_its_load_report(tmp_path):
    die = (
        'w("failed to load model /Users/me/x.gguf\\n"); '
        'w("error: backbone-f16.gguf not found (tried: /Users/me/x.gguf)\\n"); '
        'w("zonos3: backend = MTL0 | CPU; loaded 812 tensors, 1.20 GiB\\n"); '
        'w("zonos3: MTL0 weights: 811 tensors, 1200.0 MiB\\n"); sys.stderr.flush(); sys.exit(1)'
    )
    with pytest.raises(RunError) as e, serve(tmp_path, ready=False, then=die):
        pass
    assert str(e.value) == "gen: error: backbone-f16.gguf not found (tried: <file>"


def test_h3_keeps_only_its_own_lines(tmp_path):
    code = 'import sys; sys.stderr.write("error: boom\\nfailed x\\n"); sys.exit(3)'
    with pytest.raises(RunError, match=r"^gen: exited with status 3$"):
        child.run(Ctx(), py(code), cwd=tmp_path, name="h3", phases={}, idle_s=10)
