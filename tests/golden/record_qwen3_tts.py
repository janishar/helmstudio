"""Record golden Qwen3-TTS takes from the reference implementation: AI-EXP's own port (`~/GenAI/AI-EXP/src/qwen3_tts`,
read-only), run from a scratch copy on the runner's torch env (torch 2.15, transformers 5.23, MPS bf16):

    S=<scratch>; mkdir -p $S && cp -R ~/GenAI/AI-EXP/src/qwen3_tts $S/
    cd runner_service && PYTHONDONTWRITEBYTECODE=1 HF_HUB_OFFLINE=1 \
        .venv/bin/python ../tests/golden/record_qwen3_tts.py --port $S [--cases preset-ryan-calm-s7 design-s7]

Each case does what the port's own library use does (`Synthesizer.load(weights, mps, compile=False)`: its loader, its
warm-up sentence, uncompiled because torch.compile breaks end-of-speech on torch 2.15 MPS; then `speak()` with both
temperatures 0.3 and `sampling.seed`, so `stream()` calls torch.manual_seed before the talker), one model per case.
A thin wrapper on `talker.frames` only observes the codes. Writes runner_service/tests/golden/qwen3-tts/<case>.wav
(PCM 16-bit mono 24 kHz, the pack's own float → int16 rounding), <case>.codes.npy (int64 [frames, 16]) and
<case>.json (settings, versions, time, peak). tests/test_qwen3_tts_golden.py compares the pack with them.
"""

import argparse
import dataclasses
import json
import os
import platform
import resource
import sys
import time
import wave
from pathlib import Path

import numpy as np
import torch
import transformers

REPO = Path(__file__).resolve().parents[2]
OUT = REPO / "runner_service" / "tests" / "golden" / "qwen3-tts"
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
CASES = [
    {"name": "preset-ryan-calm-s7", "variant": "custom_voice", "dir": "Qwen3-TTS-12Hz-1.7B-CustomVoice",
     "text": "Good morning, everyone.", "speaker": "ryan", "language": "en", "style": "calm",
     "seed": 7, "temperature": 0.3},
    {"name": "design-s7", "variant": "voice_design", "dir": "Qwen3-TTS-12Hz-1.7B-VoiceDesign",
     "text": "Good morning, everyone.", "design": "A deep, calm man in his fifties, speaking slowly.",
     "language": "auto", "seed": 7, "temperature": 0.3},
]  # fmt: skip
LANGUAGES = {"auto": "auto", "en": "english"}


def wav_bytes(samples: np.ndarray, rate: int = 24000) -> bytes:
    import io

    pcm = (np.clip(samples, -1, 1) * 32767).round().astype("<i2").tobytes()
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1), w.setsampwidth(2), w.setframerate(rate), w.writeframes(pcm)
    return buf.getvalue()


def record(case: dict) -> dict:
    from qwen3_tts import Synthesizer

    t = time.monotonic()
    tts = Synthesizer.load(MODELS / case["dir"], torch.device("mps"), compile=False)
    load_s = time.monotonic() - t
    tts.sampling = dataclasses.replace(tts.sampling, temperature=case["temperature"],
                                       code_temperature=case["temperature"], seed=case["seed"])  # fmt: skip
    codes, frames = [], tts.talker.frames

    def tapped(*a, **kw):
        for f in frames(*a, **kw):
            codes.append(f.cpu())
            yield f

    tts.talker.frames = tapped
    t = time.monotonic()
    if case["variant"] == "custom_voice":
        samples = tts.speak(case["text"], speaker=case["speaker"], language=LANGUAGES[case["language"]],
                            instruct=case["style"])  # fmt: skip
    else:
        samples = tts.speak(case["text"], language=LANGUAGES[case["language"]], instruct=case["design"])
    speak_s = time.monotonic() - t
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{case['name']}.wav").write_bytes(wav_bytes(samples))
    np.save(OUT / f"{case['name']}.codes.npy", torch.stack(codes).numpy())
    meta = case | {
        "frames": len(codes), "duration_s": round(len(samples) / 24000, 3),
        "load_s": round(load_s, 2), "speak_s": round(speak_s, 2),
        "peak_rss_gb": round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 2**30, 2),
        "versions": {"torch": torch.__version__, "transformers": transformers.__version__,
                     "python": platform.python_version(), "port": "AI-EXP 16b31cc3 src/qwen3_tts"},
        "device": f"mps, {platform.machine()}, macOS {platform.mac_ver()[0]}",
    }  # fmt: skip
    (OUT / f"{case['name']}.json").write_text(json.dumps(meta, indent=2) + "\n")
    del tts
    torch.mps.empty_cache()
    return meta


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", required=True, help="the folder holding the scratch copy of qwen3_tts/")
    ap.add_argument("--cases", nargs="*", default=[c["name"] for c in CASES])
    args = ap.parse_args()
    sys.path.insert(0, args.port)
    for c in CASES:
        if c["name"] in args.cases:
            print(json.dumps(record(c)))
