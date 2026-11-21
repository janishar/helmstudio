"""The minimax_h3 fp8 variant (SGLang on CUDA, packs/minimax_h3/sgl.py) without a GPU: SGLang's canvas and length
rules, the request a run's inputs map to, and generate against a fake `sglang serve` (a script that serves the
/v1/videos API on its --port): argv, progress, the MP4, refusals, a failed job and cancel."""

import json
import os
import shutil
import sys
from pathlib import Path

import pytest
from PIL import Image

from core import child, media, models, pack
from core.graph import Cancelled, RunError
from packs.minimax_h3 import native, sgl

FAKE = """#!{python}
import json, os, subprocess, sys, tempfile
from http.server import BaseHTTPRequestHandler, HTTPServer
a = sys.argv[1:]
port, mode, log = int(a[a.index("--port") + 1]), os.environ.get("SGLANG_FAKE_MODE", "ok"), os.environ["SGLANG_FAKE_LOG"]
polls = []
class H(BaseHTTPRequestHandler):
    def log_message(self, *x): pass
    def reply(self, code, body, kind="application/json"):
        self.send_response(code); self.send_header("Content-Type", kind); self.end_headers()
        self.wfile.write(body if isinstance(body, bytes) else json.dumps(body).encode())
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        json.dump({{"argv": a, "cwd": os.getcwd(), "env": sorted(os.environ), "body": body}}, open(log, "w"))
        if mode == "refuse":
            return self.reply(400, {{"detail": "conditions[0].uri: no such file"}})
        self.reply(200, {{"id": "job1", "status": "queued", "progress": 0}})
    def do_GET(self):
        if self.path.endswith("/content"):
            out = os.path.join(tempfile.mkdtemp(), "o.mp4")
            subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=s=256x256:r=24:d=1", "-f", "lavfi",
                            "-i", "sine=sample_rate=32000:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
                            "-movflags", "+faststart", out], check=True)
            return self.reply(200, open(out, "rb").read(), "video/mp4")
        polls.append(1)
        if mode == "fail":
            return self.reply(200, {{"id": "job1", "status": "failed", "error": {{"message": "CUDA out of memory"}}}})
        if mode == "hang" or len(polls) < 3:
            return self.reply(200, {{"id": "job1", "status": "in_progress", "progress": 40 * len(polls)}})
        self.reply(200, {{"id": "job1", "status": "completed", "progress": 100}})
s = HTTPServer(("127.0.0.1", port), H)
sys.stderr.write("[2026-11-21] INFO:     Uvicorn running on \\x1b[1mhttp://127.0.0.1:%d\\x1b[0m (Press CTRL+C to quit)\\n" % port)
sys.stderr.flush()
s.serve_forever()
"""


def test_the_canvas_and_length_sglang_can_make():
    assert sgl.target("t2va", 864, 480, 124) == {
        "short_edge": 480,
        "aspect_ratio": "16:9",
        "duration_seconds": 124 / 24,
    }
    assert (
        sgl.target("ref2va", 1344, 768, 345)["aspect_ratio"] == "16:9"
    )  # area-capped: still exact
    assert sgl.target("t2va", 512, 512, 107)["aspect_ratio"] == "1:1"
    assert sgl.target("t2va", 480, 864, 107)["aspect_ratio"] == "9:16"
    assert sgl.target("fl2va", 800, 480, 107)["aspect_ratio"] == "5:3"  # keyframes: any W:H
    with pytest.raises(ValueError, match="only at 21:9, 16:9"):
        sgl.target("t2va", 800, 480, 107)
    for frames in (22, 90, 362):  # SGLang: 4 to 15 s
        with pytest.raises(ValueError, match="makes 107 to 345 frames"):
            sgl.target("t2va", 864, 480, frames)


def test_a_runs_inputs_map_onto_the_canonical_request():
    p = lambda n: Path(f"/tmp/{n}")
    t2v = sgl.request("a fox", 7, 6, {"width": 864, "height": 480}, 124, None, None, [], [], [])
    assert t2v == {"prompt": "a fox", "task": "t2va", "conditions": [], "seed": 7, "num_inference_steps": 6,
                   "target": {"short_edge": 480, "aspect_ratio": "16:9", "duration_seconds": 124 / 24}}  # fmt: skip
    fl = sgl.request(
        "x", 1, 2, {"width": 800, "height": 480}, 107, p("f.png"), p("l.png"), [], [], []
    )
    assert fl["task"] == "fl2va" and fl["conditions"] == [
        {"type": "image", "role": "keyframe", "uri": "/tmp/f.png", "frame_index": 0},
        {"type": "image", "role": "keyframe", "uri": "/tmp/l.png", "frame_index": -1},
    ]
    last = sgl.request("x", 1, 2, {"width": 864, "height": 480}, 107, None, p("l.png"), [], [], [])
    assert [c["frame_index"] for c in last["conditions"]] == [-1]
    ref = sgl.request("<Picture 1> <Audio 1> <Video 1>", 1, 2, {"width": 864, "height": 480}, 107, None, None,
                      [p("a.png"), p("b.png")], [p("s.wav")], [p("v.mkv")])  # fmt: skip
    assert ref["task"] == "ref2va"
    assert [(c["type"], c["role"], c["uri"]) for c in ref["conditions"]] == [
        ("image", "reference", "/tmp/a.png"),
        ("image", "reference", "/tmp/b.png"),
        ("audio", "reference", "/tmp/s.wav"),
        ("video", "reference", "/tmp/v.mkv"),
    ]


def test_fp8_is_published_on_cuda_only_and_refuses_speed_fast(monkeypatch):
    (p,) = [p for p, _ in pack.for_env("native") if p.spec["id"] == "minimax-h3"]
    monkeypatch.setattr(models, "device", lambda p: "cuda")
    assert models.runs_here(p, "fp8") and not models.runs_here(p, "base")
    monkeypatch.setattr(models, "device", lambda p: "mps")
    assert models.runs_here(p, "base") and not models.runs_here(p, "fp8")

    class M:
        variant = "fp8"

    assert native.check_generate({"speed": "fast"}, {"prompt"}, {"model": M()}) == (
        ": speed fast is h3's (Mac); the FP8 (NVIDIA) variant runs exact only"
    )
    assert native.check_generate({"speed": "exact"}, {"prompt"}, {"model": M()}) is None
    M.variant = "base"
    assert native.check_generate({"speed": "fast"}, {"prompt"}, {"model": M()}) is None


@pytest.fixture
def fake(tmp_path, monkeypatch):
    if not (media.FFMPEG and shutil.which("ffmpeg")):
        pytest.skip("no ffmpeg")
    exe, log = tmp_path / "bin" / "sglang", tmp_path / "log.json"
    exe.parent.mkdir()
    exe.write_text(FAKE.format(python=sys.executable))
    exe.chmod(0o755)
    monkeypatch.setattr(sgl, "SGLANG", exe)
    monkeypatch.setattr(sgl, "POLL", 0.05)
    monkeypatch.setenv("SGLANG_FAKE_LOG", str(log))
    monkeypatch.setenv("NATS_URL", "nats://runner:secret@nats:4222")
    folder = tmp_path / "MiniMax-H3-FP8"

    class Model:
        variant = "fp8"

        def folder(self, component=None):
            return folder

        def files(self, component):
            return [folder / f"{component}.safetensors"]

    class Ctx:
        node, seen, cancel_after = "generate", [], None

        def check(self):
            self.pid = child.LIVE["pid"] or getattr(self, "pid", None)
            if self.cancel_after is not None and len(self.seen) >= self.cancel_after:
                raise Cancelled()

        def progress(self, step, of, phase=None):
            assert 1 <= step <= of
            self.seen.append((step, of, phase))

    inputs = {"model": Model(), "prompt": "a fox", "seed": 2**53 - 1, "canvas": {"width": 864, "height": 480},
              "duration": {"frames": 107, "fps": 24}, "first": Image.new("RGB", (64, 64), (255, 0, 0))}  # fmt: skip
    return Ctx(), inputs, {"steps": 4, "speed": "exact", "refs": []}, log, folder


def test_generate_serves_the_partition_and_returns_its_video(fake, monkeypatch):
    ctx, inputs, params, log, folder = fake
    v = native.generate(ctx, params, inputs)["video"]
    run = json.loads(log.read_text())
    a = run["argv"]
    assert a[:9] == [
        "serve",
        "--backend",
        "sglang",
        "--pipeline-class-name",
        "MiniMaxH3Pipeline",
        "--model-path",
        str(folder.resolve()),
        "--model-variant",
        "fl2va",
    ]
    assert a[a.index("--component-weights-paths.transformer") + 1] == str(
        (folder / "transformer_fl2va.safetensors").resolve()
    )
    assert a[a.index("--component-weights-paths.text_encoder") + 1].endswith(
        "text_encoder.safetensors"
    )
    assert a[a.index("--layerwise-offload-components") + 1] == "text_encoder"
    assert "text_encoder=torch_sdpa" in a and a[a.index("--host") + 1] == "127.0.0.1"
    assert "SGLANG_FAKE_LOG" in run["env"] and "NATS_URL" not in run["env"]
    b = run["body"]
    assert (b["task"], b["seed"], b["num_inference_steps"]) == ("fl2va", 2**53 - 1, 4)
    assert b["target"] == {
        "short_edge": 480,
        "aspect_ratio": "9:5",
        "duration_seconds": 107 / 24,
    }  # keyframes: exact W:H
    (key,) = b["conditions"]
    assert key["frame_index"] == 0 and key["uri"].endswith(".png") and key["uri"].startswith("/")
    assert not Path(key["uri"]).exists()  # the per-run folder went
    assert v["mp4"][4:8] == b"ftyp" and v["has_audio"] and v["poster"].size == (256, 256)
    assert ctx.seen[0] == (1, 1, "starting sglang") and (40, 100, "sampling") in ctx.seen
    assert child.LIVE["pid"] is None
    # references: the Ref2VA server
    refs = params | {"refs": [{"image": Image.new("RGB", (8, 8))}]}
    native.generate(ctx, refs, {k: x for k, x in inputs.items() if k != "first"})
    run = json.loads(log.read_text())
    assert run["argv"][8] == "ref2va" and run["body"]["task"] == "ref2va"
    assert run["argv"][run["argv"].index("--component-weights-paths.transformer") + 1].endswith(
        "transformer_ref2va.safetensors"
    )


def test_generate_refusals_failures_and_cancel(fake, monkeypatch):
    ctx, inputs, params, log, _ = fake
    with pytest.raises(
        RunError, match="^generate: the FP8 \\(NVIDIA\\) variant makes 107 to 345 frames"
    ):
        native.generate(ctx, params, inputs | {"duration": {"frames": 22, "fps": 24}})
    assert not log.exists()  # refused before the server starts
    monkeypatch.setenv("SGLANG_FAKE_MODE", "refuse")
    with pytest.raises(
        RunError, match="^generate: SGLang refused the request \\(400\\): conditions"
    ):
        native.generate(ctx, params, inputs)
    monkeypatch.setenv("SGLANG_FAKE_MODE", "fail")
    with pytest.raises(RunError, match="^generate: SGLang failed: CUDA out of memory$"):
        native.generate(ctx, params, inputs)
    monkeypatch.setenv("SGLANG_FAKE_MODE", "hang")
    ctx.cancel_after = 3  # starting + two progress updates
    with pytest.raises(Cancelled):
        native.generate(ctx, params, inputs)
    assert child.LIVE["pid"] is None
    with pytest.raises(ProcessLookupError):  # the server's process group was killed
        os.kill(ctx.pid, 0)
    monkeypatch.setattr(sgl, "SGLANG", Path("/nonexistent/sglang"))
    with pytest.raises(RunError, match="SGLang is not installed"):
        native.generate(ctx, params, inputs)
