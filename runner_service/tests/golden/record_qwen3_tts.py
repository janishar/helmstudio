"""Record the qwen3_tts pack's dialogue golden (API 1.32) from the reference port itself: AI-EXP's src/qwen3_tts
(~/GenAI/AI-EXP, the commit vendored in packs/qwen3_tts/vendor, VENDORED.md) loaded by its own Synthesizer.load
(compile off: torch.compile breaks end-of-speech on torch 2.15 MPS), each line spoken by Synthesizer.speak with the
pack's speaker, language, style and temperature at the one seed (its _seed: before every line), converted to 16-bit
PCM as the pack does and joined by core.media.stitch (the contract's pauses and cues). Writes <name>.wav and
<name>.json (the case, its cues, sha256, timing). The voice-over goldens beside it were recorded the same way with
Synthesizer.stream (preset-ryan-calm-s7, design-s7).

On CUDA (M9) it records all three cases as <name>.cuda.* (wav, json; codes.npy for a voice-over, the frames the
talker wrote), the voice-overs from their MPS golden's settings, never touching the MPS goldens; the vendored port
is the reference there (byte-identical to AI-EXP's, VENDORED.md):

    uv run --extra torch python tests/golden/record_qwen3_tts.py [--port ~/GenAI/AI-EXP/src]
    uv run --extra torch python tests/golden/record_qwen3_tts.py --port packs/qwen3_tts/vendor   # on the VM
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

import numpy as np

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
VOICE_OVERS = ["preset-ryan-calm-s7", "design-s7"]
DIRS = {
    "custom_voice": "Qwen3-TTS-12Hz-1.7B-CustomVoice",
    "voice_design": "Qwen3-TTS-12Hz-1.7B-VoiceDesign",
}
# what a recording writes anew (an MPS golden's own values are not settings)
RECORDED = {"name", "variant", "cues", "wav", "wav_sha256", "frames", "duration_s", "load_s", "speak_s",
            "peak_rss_gb", "versions", "device"}  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--port", type=Path, default=Path.home() / "GenAI/AI-EXP/src")
    a = ap.parse_args()
    sys.path.insert(0, str(a.port))
    import torch
    from qwen3_tts.synthesizer import Synthesizer

    from core.loaders_torch import device
    from packs.qwen3_tts import (
        torch as tts,
    )  # the pack's own speaker / language mapping and PCM conversion

    dev = device()
    suffix = ".cuda" if dev == "cuda" else ""
    cases = dict(CASES)
    if suffix:  # the voice-overs too, at their MPS goldens' settings
        cases |= {n: json.loads((OUT / f"{n}.json").read_text()) for n in VOICE_OVERS}
    synths: dict = {}
    for name, case in cases.items():
        variant = case.get("variant", "custom_voice")
        t0 = time.monotonic()
        if variant not in synths:
            synths[variant] = Synthesizer.load(
                pack.models_root() / DIRS[variant], torch.device(dev), compile=False
            )
        load_s, synth = time.monotonic() - t0, synths[variant]
        synth.sampling = dataclasses.replace(synth.sampling, seed=case["seed"], temperature=case["temperature"],
                                             code_temperature=case["temperature"])  # fmt: skip
        t1, spoken, codes = time.monotonic(), [], []
        frames = synth.talker.frames

        def tapped(*a, frames=frames, codes=codes, **kw):  # bound now: B023
            for f in frames(*a, **kw):
                codes.append(f)
                yield f

        synth.talker.frames = tapped
        if "lines" in case:
            for x in case["lines"]:
                speaker, instruct = tts.voice_args(name, x["voice"], x["style"], synth._speaker_ids)
                language = tts.language_of(case["language"], x["voice"])
                spoken.append(tts.pcm16(synth.speak(x["text"], speaker, language, instruct)))
            pcm, cues = media.stitch(case["lines"], spoken, tts.SAMPLE_RATE)
        else:
            if variant == "custom_voice":
                voice = RYAN | {
                    "speaker": case["speaker"],
                    "language": case.get("voice_language", "en"),
                }
            else:
                voice = {"source": "design", "text": case["design"]}
            speaker, instruct = tts.voice_args(
                name, voice, case.get("style", ""), synth._speaker_ids
            )
            language = tts.language_of(case["language"], voice)
            pcm, cues = tts.pcm16(synth.speak(case["text"], speaker, language, instruct)), None
            np.save(OUT / f"{name}{suffix}.codes.npy", torch.stack(codes).cpu().numpy())
        del synth.talker.frames
        speak_s = time.monotonic() - t1
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(tts.SAMPLE_RATE)
            w.writeframes(pcm)
        wav = buf.getvalue()
        (OUT / f"{name}{suffix}.wav").write_bytes(wav)
        keep = {k: v for k, v in case.items() if k not in RECORDED}
        meta = {"name": name, "variant": variant, **keep, **({"cues": cues} if cues else {}),
                "wav": f"{name}{suffix}.wav", "wav_sha256": hashlib.sha256(wav).hexdigest(),
                "frames": len(codes), "duration_s": round(len(pcm) / 2 / tts.SAMPLE_RATE, 3),
                "load_s": round(load_s, 2), "speak_s": round(speak_s, 2),
                "versions": {"torch": torch.__version__, "python": sys.version.split()[0],
                             "port": "AI-EXP 16b31cc3 src/qwen3_tts"}, "device": dev}  # fmt: skip
        (OUT / f"{name}{suffix}.json").write_text(
            json.dumps(meta, indent=2, ensure_ascii=False) + "\n"
        )
        print(f"{name}{suffix}: {meta['duration_s']} s, sha256 {meta['wav_sha256'][:16]}…")


if __name__ == "__main__":
    main()
