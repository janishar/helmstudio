"""The stable_audio_3 pack against the reference's goldens (runner_service/tests/golden/stable_audio_3/, recorded by
tests/golden/record_stable_audio_3.py from Stability's own StableAudioModel.generate, the sample run's code path):
runs the pack's four workflows in-process (graph.run with a real residency manager, test_qwen_golden.Bench) with a
golden's settings and compares the samples by SNR (bar 30 dB: two implementations of one model on one device, the
same seeded noise), the length, channels and rate exactly, RMS and peak within 5 %. Loads real weights (about 3.5 GB):
never alongside another process that holds a model.

Measured 2026-11-27 (M5 Pro 64 GB, MPS, torch 2.15, diffusers 0.43, transformers 5.23): SNR against the reference
81.6 dB (sound), 75.4 (vary), 76.0 (repaint), 76.5 (extend); 1.6-1.8 s cold with the load (components 3.86 GB
resident, process peak 4.43 GB), 0.3-0.4 s warm per 2 s take (peak 4.27-4.29 GB); a 30 s sound 1.1 s warm, peak
5.29 GB. The reference itself: 4.4 GB peak, 0.43-0.56 s warm.

    uv run --extra torch python tests/test_stable_audio_3_golden.py [sound-rain-s42 ...]
    uv run --extra torch pytest -m gpu -s tests/test_stable_audio_3_golden.py
"""

import argparse
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

import test_qwen_golden
from test_qwen_golden import Bench

GOLDEN = HERE / "golden" / "stable_audio_3"
CASES = ["sound-rain-s42", "vary-s7", "repaint-s7", "extend-s7"]
BAR = 30


def samples(wav: bytes) -> np.ndarray:
    with wave.open(io.BytesIO(wav)) as w:
        assert (w.getnchannels(), w.getframerate()) == (2, 44100)
        return np.frombuffer(w.readframes(w.getnframes()), "<i2").astype(np.float64) / 32768


def snr(want: np.ndarray, got: np.ndarray) -> float:
    if len(want) != len(got):
        return float("-inf")
    noise = np.sum((want - got) ** 2)
    return float("inf") if noise == 0 else 10 * np.log10(np.sum(want**2) / noise)


def wav_bytes(pcm: bytes) -> bytes:
    out = io.BytesIO()
    with wave.open(out, "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(44100)
        w.writeframes(pcm)
    return out.getvalue()


def source(seconds: float | None = None) -> dict:
    """golden/stable_audio_3/source.wav (or its first `seconds`) as a resolved media value of the owner `golden`."""
    path = GOLDEN / "source.wav"
    if seconds is not None:  # a shorter source: its own file next to the golden (written once)
        path = GOLDEN / f"source-{seconds:g}s.wav"
        if not path.is_file():
            with wave.open(str(GOLDEN / "source.wav")) as w:
                path.write_bytes(wav_bytes(w.readframes(int(seconds * 44100))))
    data = path.read_bytes()
    with wave.open(str(path)) as w:
        duration = w.getnframes() / w.getframerate()
    return {"take_id": "00000000-0000-4000-8000-000000000003", "key": f"u/golden/stable_audio_3/{path.name}",
            "bytes": len(data), "etag": hashlib.sha256(data).hexdigest()[:32], "kind": "audio",
            "content_type": "audio/wav", "duration_s": duration, "has_audio": True}  # fmt: skip


async def fetch(key: str, limit: int, etag: str | None = None) -> bytes | None:
    """ctx.fetch for these goldens: u/golden/stable_audio_3/<file> is golden/stable_audio_3/<file> here."""
    path = HERE / "golden" / key.removeprefix("u/golden/")
    return path.read_bytes()[: limit + 1] if path.is_file() else None


class SA3Bench(Bench):
    def __init__(self) -> None:
        super().__init__()
        test_qwen_golden.fetch = fetch  # Bench.run's ctx.fetch reads the repo's tests/golden

    def nodes(self, case: dict) -> dict:
        wf = next(w for p in self.packs for w in p.workflows if w["id"] == case["workflow"])
        nodes = json.loads(json.dumps(wf["nodes"]))
        nodes["prompt"]["params"]["text"] = case["prompt"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        stage = case["workflow"].removeprefix("sa3-")
        p = nodes[stage]["params"]
        if stage == "sound":
            p["seconds"] = case["seconds"]
        elif stage == "vary":
            p |= {"source": source(), "change": case["change"]}
        elif stage == "repaint":
            p |= {"source": source(), "span": {"start": case["span"][0], "end": case["span"][1]}}
        else:
            p |= {
                "source": source(case["source_s"]),
                "seconds": case["seconds"],
                "where": case["where"],
            }
        return nodes

    def make(self, case: dict) -> dict:
        r = self.run(self.nodes(case))
        r["wav"] = r.pop("png")
        r["resident_gb"] = round(sum(e["gb"] for e in self.residency.state()["resident"]), 2)
        return r


def case(bench: SA3Bench, name: str) -> dict:
    s = json.loads((GOLDEN / f"{name}.json").read_text())
    t = time.monotonic()
    r = bench.make(s)
    r["total_s"] = round(time.monotonic() - t, 2)
    want, got = (
        samples((GOLDEN / f"{name}.wav").read_bytes()),
        samples(r["wav"]),
    )  # the output node's WAV
    r["snr_db"] = round(snr(want, got), 1)
    rms, peak = float(np.sqrt(np.mean(got**2))), float(np.abs(got).max()) if len(got) else 0.0
    r["levels"] = {"rms": round(rms, 5), "peak": round(peak, 5)}
    close = abs(rms - s["rms"]) <= 0.05 * s["rms"] and abs(peak - s["peak"]) <= 0.05 * s["peak"]
    r["ok"] = len(got) == len(want) and r["snr_db"] >= BAR and close
    return r


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "Stable-Audio-3-Small-SFX").is_dir():
        pytest.skip("Stable Audio 3 Small-SFX is not in the models folder")
    b = SA3Bench()
    yield b
    b.residency.unload()


@pytest.mark.gpu
@pytest.mark.parametrize("name", CASES)
def test_matches_the_golden(bench, name):
    r = case(bench, name)
    print(name, {k: r[k] for k in ("snr_db", "levels", "s", "peak_gb", "resident_gb")})
    assert r["ok"]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=CASES)
    args = ap.parse_args()
    b = SA3Bench()
    for name in args.cases:
        r = case(b, name)
        print(
            name,
            {k: r[k] for k in ("ok", "snr_db", "levels", "s", "peak_gb", "resident_gb", "total_s")},
        )
        b.cache = b.graph.Cache()
        r2 = case(b, name)  # warm: the components resident
        print(" warm", {k: r2[k] for k in ("ok", "snr_db", "s", "peak_gb", "total_s")})
    b.residency.unload()
