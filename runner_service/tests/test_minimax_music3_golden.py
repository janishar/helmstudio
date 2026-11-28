"""The minimax_music3 pack against the model card's own code path (runner_service/tests/golden/minimax-music3/,
recorded by tests/golden/record_minimax_music3.py: one diffusers pipeline call, bf16, a seeded device generator):
runs the pack's workflow in-process (graph.run with a real residency manager, test_qwen_golden.Bench) with the
golden's settings and compares the whole PCM by hash (bit for bit: the same seed repeats exactly) and its first 2 s
byte for byte, else RMS and peak within 2 %. Loads about 22 GB: never beside another process that holds a model.

    uv run --extra torch python tests/test_minimax_music3_golden.py        # the case, then a warm repeat
    uv run --extra torch pytest -m gpu -s tests/test_minimax_music3_golden.py
"""

import hashlib
import io
import json
import sys
import time
import wave
from pathlib import Path

import numpy as np
import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))

from test_qwen_golden import Bench

GOLDEN = HERE / "golden" / "minimax-music3"
NAME = "song-10s-s42"


class MusicBench(Bench):
    def nodes(self, case: dict) -> dict:
        wf = next(w for p in self.packs for w in p.workflows if w["id"] == "minimax-music3")
        nodes = json.loads(json.dumps(wf["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        nodes["lm"]["params"] |= {k: case[k] for k in ("lyrics", "instrumental", "seconds")}
        return nodes


def frames(wav: bytes) -> tuple[bytes, int]:
    with wave.open(io.BytesIO(wav)) as w:
        assert (w.getnchannels(), w.getsampwidth()) == (2, 2)
        return w.readframes(w.getnframes()), w.getframerate()


def case(bench: MusicBench) -> dict:
    s = json.loads((GOLDEN / f"{NAME}.json").read_text())
    t = time.monotonic()
    r = bench.run(bench.nodes(s))
    r["total_s"] = round(time.monotonic() - t, 1)
    pcm, rate = frames(r.pop("png"))
    head, _ = frames((GOLDEN / f"{NAME}.wav").read_bytes())
    x = np.frombuffer(pcm, "<i2").astype(np.float64) / 32767
    r["exact"] = hashlib.sha256(pcm).hexdigest() == s["sha256"]
    r["head_exact"] = pcm[: len(head)] == head
    r["duration_s"] = round(len(pcm) / 4 / rate, 4)
    r["rms"], r["peak"] = round(float(np.sqrt((x**2).mean())), 4), round(float(np.abs(x).max()), 4)
    close = abs(r["rms"] - s["rms"]) <= 0.02 * s["rms"] and abs(r["peak"] - s["peak"]) <= 0.02 * s["peak"]
    r["ok"] = rate == s["sample_rate"] and r["duration_s"] == s["duration_s"] and (r["exact"] or close)
    return r


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "MiniMax-Music3").is_dir():
        pytest.skip("MiniMax Music 3 is not in the models folder")
    b = MusicBench()
    yield b
    b.residency.unload()


KEYS = ("ok", "exact", "head_exact", "duration_s", "rms", "peak", "s", "peak_gb", "total_s")


@pytest.mark.gpu
def test_matches_the_golden(bench):
    r = case(bench)
    print(NAME, {k: r[k] for k in KEYS})
    assert r["ok"] and r["exact"]


if __name__ == "__main__":
    b = MusicBench()
    r = case(b)
    print(NAME, {k: r[k] for k in KEYS})
    b.cache = b.graph.Cache()
    r2 = case(b)  # warm: the components resident
    print(" warm", {k: r2[k] for k in KEYS})
    b.residency.unload()
