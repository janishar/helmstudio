"""Record the qwen3_tts pack's dialogue golden (API 1.32) from the reference port itself: AI-EXP's src/qwen3_tts
(~/GenAI/AI-EXP, the commit vendored in packs/qwen3_tts/vendor, VENDORED.md) loaded by its own Synthesizer.load
(compile off: torch.compile breaks end-of-speech on torch 2.15 MPS), each line spoken by Synthesizer.speak with the
pack's speaker, language, style and temperature at the one seed (its _seed: before every line), converted to 16-bit
PCM as the pack does and joined by core.media.stitch (the contract's pauses and cues). Writes <name>.wav and
<name>.json (the case, its cues, sha256, timing). The voice-over goldens beside it were recorded the same way with
Synthesizer.stream (preset-ryan-calm-s7, design-s7).

    uv run --extra torch python tests/golden/record_qwen3_tts.py [--port ~/GenAI/AI-EXP/src]
"""

import argparse
import dataclasses
import hashlib
import io
import json
import sys
import time
import wave
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[1]))

from core import media, pack

OUT = HERE / "qwen3-tts"
RYAN = {"item_id": "qwen3-tts/ryan", "version": 1, "source": "preset", "family": "qwen3-tts", "speaker": "ryan",
        "language": "en"}  # fmt: skip
AIDEN = RYAN | {"item_id": "qwen3-tts/aiden", "speaker": "aiden"}
CASES = {
    "dialogue-ryan-aiden-s1": {"seed": 1, "language": "auto", "temperature": 0.3, "pause": 0.35, "lines": [
        {"voice": RYAN, "text": "Are you coming?", "style": "", "pause_s": 0.35},
        {"voice": AIDEN, "text": "I know. The road was closed.", "style": "quietly", "pause_s": 0.35}]},
}  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--port", type=Path, default=Path.home() / "GenAI/AI-EXP/src")
    a = ap.parse_args()
    sys.path.insert(0, str(a.port))
    import torch
    from qwen3_tts.synthesizer import Synthesizer

    from packs.qwen3_tts import (
        torch as tts,
    )  # the pack's own speaker / language mapping and PCM conversion

    weights = pack.models_root() / "Qwen3-TTS-12Hz-1.7B-CustomVoice"
    t0 = time.monotonic()
    synth = Synthesizer.load(weights, torch.device("mps"), compile=False)
    load_s = time.monotonic() - t0
    for name, case in CASES.items():
        synth.sampling = dataclasses.replace(synth.sampling, seed=case["seed"], temperature=case["temperature"],
                                             code_temperature=case["temperature"])  # fmt: skip
        t1, spoken = time.monotonic(), []
        for x in case["lines"]:
            speaker, instruct = tts.voice_args(name, x["voice"], x["style"], synth._speaker_ids)
            language = tts.language_of(case["language"], x["voice"])
            spoken.append(tts.pcm16(synth.speak(x["text"], speaker, language, instruct)))
        speak_s = time.monotonic() - t1
        pcm, cues = media.stitch(case["lines"], spoken, tts.SAMPLE_RATE)
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(tts.SAMPLE_RATE)
            w.writeframes(pcm)
        wav = buf.getvalue()
        (OUT / f"{name}.wav").write_bytes(wav)
        meta = {"name": name, "variant": "custom_voice", **case, "cues": cues, "wav": f"{name}.wav",
                "wav_sha256": hashlib.sha256(wav).hexdigest(), "duration_s": round(len(pcm) / 2 / tts.SAMPLE_RATE, 3),
                "load_s": round(load_s, 2), "speak_s": round(speak_s, 2),
                "versions": {"torch": torch.__version__, "python": sys.version.split()[0],
                             "port": "AI-EXP 16b31cc3 src/qwen3_tts"}}  # fmt: skip
        (OUT / f"{name}.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False) + "\n")
        print(f"{name}: {meta['duration_s']} s, sha256 {meta['wav_sha256'][:16]}…")


if __name__ == "__main__":
    main()
