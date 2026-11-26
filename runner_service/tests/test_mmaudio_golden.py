"""The mmaudio pack against MMAudio's own output (tests/golden/mmaudio/, recorded by tests/golden/record_mmaudio.py
from the upstream clone in the samples' venv): runs the pack's two workflows in-process (graph.run with a real
residency manager, test_qwen_golden.Bench) at a golden's settings. Text: the WAV equal, else at the SNR bar (another
torch or device). Video: the frames come from ffmpeg here and from PyAV there, so the sound is held to its length,
rate and channels exactly and its RMS and peak within a tolerance; the muxed video must be H.264 + AAC at the cut
length. Loads real weights (about 5.5 GB): never alongside another process that holds a model.

    uv run --extra torch python tests/test_mmaudio_golden.py [text-rain-s42 video-truck-s42]
    uv run --extra torch pytest -m gpu -s tests/test_mmaudio_golden.py
"""

import argparse
import dataclasses
import hashlib
import io
import json
import sys
import tempfile
import time
import wave
from pathlib import Path

import numpy as np
import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))

import test_qwen_golden
from test_qwen_golden import Bench

GOLDEN = HERE / "golden" / "mmaudio"
CASES = ["text-rain-s42", "video-truck-s42"]
BAR = 30  # dB, the text case on another torch or device
RMS_TOL, PEAK_TOL = 0.25, 0.15  # the video case: relative RMS, absolute peak (full scale 1)


async def fetch(key: str, limit: int, etag: str | None = None) -> bytes | None:
    path = GOLDEN / key.removeprefix("u/golden/")
    return path.read_bytes()[: limit + 1] if path.is_file() else None


def samples(wav: bytes) -> tuple[np.ndarray, int, int]:
    with wave.open(io.BytesIO(wav)) as w:
        x = np.frombuffer(w.readframes(w.getnframes()), "<i2").astype(np.float64) / 32768
        return x, w.getframerate(), w.getnchannels()


def snr(want: np.ndarray, got: np.ndarray) -> float:
    if len(want) != len(got):
        return float("-inf")
    noise = np.sum((want - got) ** 2)
    return float("inf") if noise == 0 else float(10 * np.log10(np.sum(want**2) / noise))


class SoundBench(Bench):
    """Bench with every output node's value kept (the video workflow has two)."""

    def __init__(self) -> None:
        super().__init__()
        test_qwen_golden.fetch = (
            fetch  # Bench.run's ctx.fetch: u/golden/<file> is golden/mmaudio/<file>
        )
        self.got: dict = {}
        out = self.impls["output"]

        def keep(ctx, params, inputs):
            if ctx is not None:
                self.got[ctx.node] = inputs["media"]
            return out.fn(ctx, params, inputs)

        self.impls["output"] = dataclasses.replace(out, fn=keep)

    def nodes(self, case: dict) -> dict:
        from core import media

        wf = next(w for p in self.packs for w in p.workflows if w["id"] == case["workflow"])
        nodes = json.loads(json.dumps(wf["nodes"]))
        stage = "foley" if "video" in case else "sound"
        nodes["prompt"]["params"]["text"] = case["text"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        nodes[stage]["params"] |= {k: case[k] for k in ("seconds", "avoid", "guidance", "steps")}
        if "video" in case:
            path = GOLDEN / case["video"]
            info, data = media.probe(path), path.read_bytes()
            num, den = (int(x) for x in info["stream"]["frame_rate"].split("/"))
            nodes[stage]["params"]["source"] = {
                "take_id": "00000000-0000-4000-8000-000000000003", "kind": "video", "key": f"u/golden/{path.name}",
                "content_type": "video/mp4", "bytes": len(data), "etag": hashlib.md5(data).hexdigest(),
                "width": info["stream"]["width"], "height": info["stream"]["height"], "fps": num / den,
                "frames": info["frames"], "duration_s": info["frames"] * den / num, "has_audio": False}  # fmt: skip
        return nodes

    def sound(self, case: dict) -> dict:
        self.got = {}
        r = self.run(self.nodes(case))
        r["resident_gb"] = round(sum(e["gb"] for e in self.residency.state()["resident"]), 2)
        return r


def case(bench: SoundBench, name: str) -> dict:
    from core import media
    from core.nodes import _wav

    want = json.loads((GOLDEN / f"{name}.json").read_text())
    t = time.monotonic()
    r = bench.sound(want)
    r["total_s"] = round(time.monotonic() - t, 2)
    wav, _ = _wav(None, bench.got["output"])
    x, rate, ch = samples(wav)
    golden_wav = (GOLDEN / f"{name}.wav").read_bytes()
    y = samples(golden_wav)[0]
    m = want["metrics"]
    r["wav_exact"] = wav == golden_wav
    r["snr_db"] = round(snr(y, x), 1)
    r["rms"], r["peak"] = round(float(np.sqrt(np.mean(x**2))), 5), round(float(np.abs(x).max()), 5)
    shape_ok = (len(x), rate, ch) == (m["samples"], m["sample_rate"], m["channels"])
    if "video" not in want:
        r["ok"] = shape_ok and (r["wav_exact"] or r["snr_db"] >= BAR)
        return r
    near = abs(r["rms"] - m["rms"]) <= RMS_TOL * m["rms"] and abs(r["peak"] - m["peak"]) <= PEAK_TOL
    video = bench.got["output_video"]
    with tempfile.NamedTemporaryFile(suffix=".mp4") as f:
        f.write(video["mp4"])
        f.flush()
        info = media.probe(Path(f.name))
    seconds = float(info["video"].get("duration", 0))
    r["video"] = {"codec": info["stream"]["codec"], "audio": (info["audio"] or {}).get("codec_name"),
                  "seconds": round(seconds, 3), "frames": video["frames"], "bytes": len(video["mp4"])}  # fmt: skip
    r["ok"] = (shape_ok and near and r["video"]["codec"] == "h264" and r["video"]["audio"] == "aac"
               and abs(seconds - want["seconds"]) < 0.1)  # fmt: skip
    return r


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "MMAudio" / "weights").is_dir():
        pytest.skip("MMAudio is not in the models folder")
    b = SoundBench()
    yield b
    b.residency.unload()


SHOW = ("ok", "wav_exact", "snr_db", "rms", "peak", "s", "peak_gb", "resident_gb", "total_s")


@pytest.mark.gpu
@pytest.mark.parametrize("name", CASES)
def test_matches_the_golden(bench, name):
    r = case(bench, name)
    print(name, {k: r.get(k) for k in (*SHOW, "video")})
    assert r["ok"]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=CASES)
    args = ap.parse_args()
    b = SoundBench()
    for name in args.cases:
        r = case(b, name)
        print(name, {k: r.get(k) for k in (*SHOW, "video")})
        b.cache = b.graph.Cache()
        r2 = case(b, name)  # warm: the components resident
        print(" warm", {k: r2.get(k) for k in ("ok", "wav_exact", "s", "peak_gb", "total_s")})
    b.residency.unload()
