"""The ace_step pack against its goldens (runner_service/tests/golden/ace_step/, recorded by
tests/golden/record_ace_step.py on this torch env): runs the pack's four workflows in-process (graph.run with a real
residency manager, test_qwen_golden.Bench) with a golden's settings and compares the whole WAV's SHA-256 (the
planner and the DiT are seeded, so a seed repeats bit for bit on the same device), else its facts (length, rate,
channels) exactly and its level (RMS within 10 %) and first 2 s (SNR >= 30 dB: another torch or device). The
sources of Repaint, Cover and Extend are the first 6 s of the ACE-Step sample run's song (model-samples). Loads real
weights (about 20 GB a variant): never alongside another process that holds a model (take /tmp/helmstudio-gpu.lock).

    uv run --extra torch python tests/test_ace_step_golden.py [music-2b-s42 ...]
    uv run --extra torch pytest -m gpu -s tests/test_ace_step_golden.py
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
from conftest import snr

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))

import test_qwen_golden
from test_qwen_golden import Bench

GOLDEN = HERE / "golden" / "ace_step"
STYLE = "warm indie pop, acoustic guitar, female vocal, 100 bpm"  # the sample run's song
LYRICS = ("[verse]\nMorning light on the harbour wall\nBoats come home and the seagulls call\n"
          "[chorus]\nHold the tide, hold it still\nI will wait by the window sill")  # fmt: skip
SOURCE = "ace_step/source.mp3"  # 6 s of ~/GenAI/model-samples/ace-step-1.5/song.mp3, 48 kHz stereo
SOURCE_S = 6.0
CASES = {
    "music-2b-s42": {"workflow": "ace-step-music", "variant": "turbo-2b", "seed": 42, "seconds": 10},
    "music-xl-s42": {"workflow": "ace-step-music", "variant": "xl-turbo", "seed": 42, "seconds": 10},
    "repaint-2b-s7": {"workflow": "ace-step-repaint", "variant": "turbo-2b", "seed": 7,
                      "params": {"span": {"start": 2.0, "end": 4.0}}},
    "cover-2b-s7": {"workflow": "ace-step-cover", "variant": "turbo-2b", "seed": 7, "params": {"strength": 0.6},
                    "style": "acoustic folk, finger-picked guitar, male vocal, warm room"},
    "cover-xl-s7": {"workflow": "ace-step-cover", "variant": "xl-turbo", "seed": 7, "params": {"strength": 0.6},
                    "style": "acoustic folk, finger-picked guitar, male vocal, warm room"},
    "extend-2b-s7": {"workflow": "ace-step-extend", "variant": "turbo-2b", "seed": 7,
                     "params": {"seconds": 5, "where": "end"}},
}  # fmt: skip
KEEP_S = 2.0  # the WAV kept in git: the first 2 s
BAR_DB, RMS_TOL = 30, 0.1


def source_value() -> dict:
    data = (HERE / "golden" / SOURCE).read_bytes()
    return {"take_id": "00000000-0000-4000-8000-000000000001", "kind": "audio", "key": f"u/golden/{SOURCE}",
            "content_type": "audio/mpeg", "bytes": len(data), "etag": hashlib.sha256(data).hexdigest()[:32],
            "duration_s": SOURCE_S, "has_audio": True}  # fmt: skip


def samples(wav: bytes) -> tuple[np.ndarray, dict]:
    with wave.open(io.BytesIO(wav)) as w:
        x = np.frombuffer(w.readframes(w.getnframes()), "<i2").astype(np.float64) / 32768
        facts = {"sample_rate": w.getframerate(), "channels": w.getnchannels(),
                 "duration_s": round(w.getnframes() / w.getframerate(), 3)}  # fmt: skip
    return x.reshape(-1, facts["channels"]), facts


def head(wav: bytes, seconds: float = KEEP_S) -> bytes:
    """The WAV's first `seconds`, as a WAV."""
    with wave.open(io.BytesIO(wav)) as w:
        rate, ch = w.getframerate(), w.getnchannels()
        frames = w.readframes(round(seconds * rate))
    out = io.BytesIO()
    with wave.open(out, "wb") as w:
        w.setnchannels(ch)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(frames)
    return out.getvalue()


async def fetch(key: str, limit: int, etag: str | None = None) -> bytes | None:
    """ctx.fetch for these goldens: u/golden/ace_step/<file> is tests/golden/ace_step/<file>, else Bench's."""
    path = HERE / "golden" / key.removeprefix("u/golden/")
    if key.startswith("u/golden/ace_step/") and path.is_file():
        return path.read_bytes()[: limit + 1]
    return await qwen_fetch(key, limit, etag)


qwen_fetch, test_qwen_golden.fetch = (
    test_qwen_golden.fetch,
    fetch,
)  # Bench.run reads its module's fetch


class AceBench(Bench):
    def nodes(self, name: str) -> dict:
        case = CASES[name]
        wf = next(w for p in self.packs for w in p.workflows if w["id"] == case["workflow"])
        nodes = json.loads(json.dumps(wf["nodes"]))
        nodes["prompt"]["params"]["text"] = case.get("style", STYLE)
        nodes["seed"]["params"]["seed"] = case["seed"]
        nodes["model"]["params"]["variant"] = case["variant"]
        nodes["song"]["params"] |= {
            "lyrics": LYRICS,
            "language": "en",
            "seconds": case.get("seconds", 0),
        }
        stage = case["workflow"].removeprefix("ace-step-")
        stage = "compose" if stage == "music" else stage
        if stage != "compose":
            nodes[stage]["params"] |= {"source": source_value()}
        nodes[stage]["params"] |= case.get("params", {})
        return nodes

    def song(self, name: str) -> dict:
        """One run: the output WAV, its facts and level, sections, plan texts, per-node seconds and peak GB."""
        before = self.lt.measure_gb()
        t = time.monotonic()
        r = self.run(self.nodes(name))
        x, facts = samples(r["png"])
        return {"wav": r["png"], "facts": facts, "sha256": hashlib.sha256(r["png"]).hexdigest(),
                "rms": round(float(np.sqrt(np.mean(x**2))), 4), "peak": round(float(np.abs(x).max()), 4),
                "sections": r["meta"]["audio"].get("sections", []), "texts": r.get("texts", {}),
                "s": r["s"], "peak_gb": r["peak_gb"], "before_gb": round(before, 2),
                "total_s": round(time.monotonic() - t, 2),
                "resident_gb": round(sum(e["gb"] for e in self.residency.state()["resident"]), 2)}  # fmt: skip


def compare(bench: AceBench, name: str) -> dict:
    want = json.loads((GOLDEN / f"{name}.json").read_text())
    r = bench.song(name)
    w, _ = samples((GOLDEN / f"{name}.wav").read_bytes())
    g, _ = samples(head(r["wav"]))
    r["exact"] = r["sha256"] == want["sha256"]
    r["snr_db"] = snr(w, g)
    r["ok"] = r["exact"] or (r["facts"] == want["facts"] and abs(r["rms"] - want["rms"]) <= RMS_TOL * want["rms"]
                             and r["snr_db"] >= BAR_DB)  # fmt: skip
    return r


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "ACE-Step-1.5" / "acestep-v15-turbo").is_dir():
        pytest.skip("ACE-Step 1.5 is not in the models folder")
    b = AceBench()
    yield b
    b.residency.unload()


@pytest.mark.gpu
@pytest.mark.parametrize("name", list(CASES))
def test_matches_the_golden(bench, name):
    from core import pack

    if CASES[name]["variant"] == "xl-turbo" and not (pack.models_root() / "ACE-Step-1.5-XL-Turbo").is_dir():
        pytest.skip("ACE-Step 1.5 XL is not in the models folder")
    r = compare(bench, name)
    print(name, {k: r[k] for k in ("exact", "snr_db", "rms", "s", "peak_gb", "total_s")})
    assert r["ok"]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=list(CASES))
    args = ap.parse_args()
    b = AceBench()
    for name in args.cases:
        r = compare(b, name)
        print(name, {k: r[k] for k in ("ok", "exact", "snr_db", "rms", "s", "peak_gb", "total_s")})
    b.residency.unload()
