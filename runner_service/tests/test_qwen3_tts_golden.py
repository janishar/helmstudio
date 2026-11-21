"""The qwen3_tts pack against the reference port's goldens (runner_service/tests/golden/qwen3-tts/, recorded by
tests/golden/record_qwen3_tts.py from AI-EXP's own Synthesizer on this torch env): runs the pack's workflows
in-process (graph.run with a real residency manager, test_qwen_golden.Bench) with a golden's settings and compares the
codec codes exactly and the WAV bytes exactly, else by SNR (bar 40 dB: another torch or device). Loads real weights
(about 4.5 GB a variant): never alongside another process that holds a model.

    uv run --extra torch python tests/test_qwen3_tts_golden.py [preset-ryan-calm-s7 design-s7]
    uv run --extra torch python tests/test_qwen3_tts_golden.py --previews   # (re)speak voices/*.wav: seed 1
    uv run --extra torch pytest -m gpu -s tests/test_qwen3_tts_golden.py
"""

import argparse
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

GOLDEN = HERE / "golden" / "qwen3-tts"
CASES = ["preset-ryan-calm-s7", "design-s7"]
WORKFLOW = {"custom_voice": "qwen3-tts-voice-over", "voice_design": "qwen3-tts-design-voice"}
DESIGN_ID = "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21"
BAR = 40
PREVIEW = {"zh": "你好，很高兴认识你。", "en": "Hello there, it is nice to meet you.",
           "ja": "こんにちは、よろしくお願いします。", "ko": "안녕하세요, 만나서 반가워요."}  # fmt: skip
PREVIEW_SEED, PREVIEW_MAX_S = 1, 4.0


def samples(wav: bytes) -> np.ndarray:
    with wave.open(io.BytesIO(wav)) as w:
        return np.frombuffer(w.readframes(w.getnframes()), "<i2").astype(np.float64)


def snr(want: np.ndarray, got: np.ndarray) -> float:
    if len(want) != len(got):
        return float("-inf")
    noise = np.sum((want - got) ** 2)
    return float("inf") if noise == 0 else 10 * np.log10(np.sum(want**2) / noise)


class TTSBench(Bench):
    def __init__(self) -> None:
        super().__init__()
        from packs.qwen3_tts import torch as tts

        self.tts, self.codes = tts, []
        say = tts.say

        def tapped(*a, **kw):  # observes the codes of each piece
            out = say(*a, **kw)
            self.codes.append(out[1])
            return out

        tts.say = tapped

    def nodes(self, case: dict) -> dict:
        wf = next(
            w for p in self.packs for w in p.workflows if w["id"] == WORKFLOW[case["variant"]]
        )
        nodes = json.loads(json.dumps(wf["nodes"]))
        nodes["prompt"]["params"]["text"] = case["text"]
        nodes["seed"]["params"]["seed"] = case["seed"]
        if case["variant"] == "custom_voice":
            voice = {"item_id": f"qwen3-tts/{case['speaker'].replace('_', '-')}", "version": 1, "source": "preset",
                     "family": "qwen3-tts", "speaker": case["speaker"], "language": case.get("voice_language", "en")}  # fmt: skip
        else:
            voice = {"item_id": DESIGN_ID, "version": 1, "source": "design", "text": case["design"]}
        nodes["speak"]["params"] |= {"voice": voice, "language": case["language"],
                                     "temperature": case["temperature"], "style": case.get("style", "")}  # fmt: skip
        return nodes

    def speak(self, case: dict) -> dict:
        self.codes = []
        before = self.lt.measure_gb()
        r = self.run(self.nodes(case))
        r["wav"], r["codes"] = r.pop("png"), list(self.codes)
        r["before_gb"] = round(before, 2)
        r["resident_gb"] = round(sum(e["gb"] for e in self.residency.state()["resident"]), 2)
        return r


def case(bench: TTSBench, name: str) -> dict:
    s = json.loads((GOLDEN / f"{name}.json").read_text())
    t = time.monotonic()
    r = bench.speak(s)
    r["total_s"] = round(time.monotonic() - t, 2)
    want_codes = np.load(GOLDEN / f"{name}.codes.npy")
    want_wav = (GOLDEN / f"{name}.wav").read_bytes()
    r["codes_exact"] = len(r["codes"]) == 1 and np.array_equal(r["codes"][0].numpy(), want_codes)
    r["wav_exact"] = r["wav"] == want_wav
    r["snr_db"] = snr(samples(want_wav), samples(r["wav"]))
    r["ok"] = r["codes_exact"] and (r["wav_exact"] or r["snr_db"] >= BAR)
    return r


def previews(bench: TTSBench) -> list:
    """Speak each preset's preview line (its own language, seed 1, temperature 0.3, no style) to voices/<id>.wav."""
    import yaml

    folder = HERE.parent / "packs/qwen3_tts/voices"
    out = []
    for v in yaml.safe_load((folder / "presets.yaml").read_text()):
        c = {"variant": "custom_voice", "text": PREVIEW[v["language"]], "seed": PREVIEW_SEED, "speaker": v["speaker"],
             "voice_language": v["language"], "language": "auto", "temperature": 0.3}  # fmt: skip
        r = bench.speak(c)
        with wave.open(io.BytesIO(r["wav"])) as w:
            seconds = w.getnframes() / w.getframerate()
        assert seconds <= PREVIEW_MAX_S, (v["id"], seconds)
        (folder / f"{v['id']}.wav").write_bytes(r["wav"])
        out.append((v["id"], round(seconds, 2), r["s"], r["peak_gb"]))
    return out


@pytest.fixture(scope="module")
def bench():
    pytest.importorskip("torch")
    from core import pack

    if not (pack.models_root() / "Qwen3-TTS-12Hz-1.7B-CustomVoice").is_dir():
        pytest.skip("Qwen3-TTS is not in the models folder")
    b = TTSBench()
    yield b
    b.residency.unload()


@pytest.mark.gpu
@pytest.mark.parametrize("name", CASES)
def test_matches_the_golden(bench, name):
    r = case(bench, name)
    print(
        name,
        {k: r[k] for k in ("codes_exact", "wav_exact", "snr_db", "s", "peak_gb", "resident_gb")},
    )
    assert r["ok"]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=CASES)
    ap.add_argument("--previews", action="store_true")
    args = ap.parse_args()
    b = TTSBench()
    if args.previews:
        for row in previews(b):
            print(row)
    else:
        for name in args.cases:
            r = case(b, name)
            print(name, {k: r[k] for k in ("ok", "codes_exact", "wav_exact", "snr_db", "s", "peak_gb", "before_gb",
                                           "resident_gb", "total_s")})  # fmt: skip
            b.cache = b.graph.Cache()
            r2 = case(b, name)  # warm: the components resident, only the speech
            print(" warm", {k: r2[k] for k in ("ok", "s", "peak_gb", "total_s")})
    b.residency.unload()
