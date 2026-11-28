"""Record the zonos3 pack's golden WAVs from the reference zonos3-server: the same commit as vendor/zonos3 (VERSION)
built from a scratch copy of ~/GenAI/duplex/zonos3 (never built in place), so a golden says what upstream's own build
speaks for the pack's own request (packs/zonos3/native.py argv, speaker, body: the same command line, voice fields and
Delivery mapping):

    .venv/bin/python tests/golden/record_zonos3.py --binary <scratch>/servers/ggml/build/zonos3-server \
        [--out tests/golden/zonos3] [--case NAME ...]

Each case starts the server on a free port (`--port 0`, the port read from its ready line), posts one /v2/generate
per line (every line is one piece; a dialogue's lines joined by core.media.stitch; <= 4 s each case), writes <name>.wav and <name>.json (the case, sha256, length, timing), then stops
the server's process group. tests/test_zonos3_golden.py runs the pack on the vendored build and compares the samples
byte for byte."""

import argparse
import hashlib
import io
import json
import os
import signal
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
import wave
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[1]))

from core import media
from packs.zonos3 import native

WEIGHTS = Path.home() / "GenAI/models/ZONOS3-Mini/gguf"
OUT = HERE / "zonos3"
NEUTRAL = {
    "language": "en-US",
    "emotion": 0.5,
    "energy": 0.5,
    "whisper": 0,
    "speaking_rate": 1,
    "guidance": 2,
}
TENSE = NEUTRAL | {
    "emotion": 0.3,
    "energy": 0.75,
    "speaking_rate": 1.1,
    "guidance": 2.5,
}  # the workflow's preset
SAVANNAH = {"item_id": "zonos3/savannah", "version": 1, "source": "preset", "family": "zonos3",
            "speaker": "default_de4d191ca7574982", "language": "en-US"}  # fmt: skip
RUPERT = {"item_id": "zonos3/rupert", "version": 1, "source": "preset", "family": "zonos3",
          "speaker": "default_3765f0d1140c05c0", "language": "en-GB"}  # fmt: skip
# a clip voice: 3 s of a Qwen3-TTS take (the qwen3_tts pack's Uncle Fu preview, spoken by that pack; its goldens are
# shorter than a clip's 3 s minimum)
CLIP_FILE = HERE.parents[1] / "packs/qwen3_tts/voices/uncle-fu.wav"
CLIP = {"item_id": "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", "version": 1, "source": "clip",
        "clip": {"take_id": "qwen3-tts-uncle-fu", "file": "packs/qwen3_tts/voices/uncle-fu.wav", "in": 0.1,
                 "out": 3.1}}  # fmt: skip
CASES = {
    "preset-savannah-s1": {"text": "The lighthouse keeper waved from the rocks.", "seed": 1, "voice": SAVANNAH,
                           "params": NEUTRAL},
    "tense-savannah-s1": {"text": "Nobody leaves this room until I say so.", "seed": 1, "voice": SAVANNAH,
                          "params": TENSE},
    "clone-uncle-fu-s1": {"text": "The lighthouse keeper waved from the rocks.", "seed": 1, "voice": CLIP,
                          "params": NEUTRAL},
    # API 1.32: a dialogue, one server for both lines, the second's style mapped onto its Delivery (native.styled);
    # Tense (a speaking rate is always sent since step 5: without one a short line ran 3-8 s, GOTCHAS.md)
    "dialogue-savannah-rupert-s1": {"seed": 1, "params": TENSE | {"pause": 0.35}, "lines": [
        {"voice": SAVANNAH, "text": "Are you coming?", "style": "", "pause_s": 0.35},
        {"voice": RUPERT, "text": "I know. The road was closed.", "style": "quietly", "pause_s": 0.35}]},
}  # fmt: skip


def voice_value(voice: dict) -> dict:
    """The case's voice as the runner gets it: a clip's bytes read from its file (as core.images reads a take)."""
    if voice["source"] != "clip":
        return voice
    data = (HERE.parents[1] / voice["clip"]["file"]).read_bytes()
    return voice | {"clip": voice["clip"] | {"data": data}}


def start(argv: list[str], timeout: float = native.READY_S) -> tuple[subprocess.Popen, int]:
    proc = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                            start_new_session=True)  # fmt: skip
    until = time.monotonic() + timeout
    for raw in proc.stderr:  # ponytail: the deadline is checked per line; the server prints ~240 while loading
        if m := native.READY.search(raw.decode(errors="replace")):
            # keep draining stderr so a full pipe never blocks the server
            threading.Thread(target=lambda: all(proc.stderr), daemon=True).start()
            return proc, int(m.group(1))
        if time.monotonic() > until:
            break
    stop(proc)
    sys.exit(f"{Path(argv[0]).name}: no ready line (exit {proc.returncode})")


def stop(proc: subprocess.Popen) -> None:
    try:
        os.killpg(proc.pid, signal.SIGTERM)
        proc.wait(5)
    except ProcessLookupError:
        pass
    except subprocess.TimeoutExpired:
        os.killpg(proc.pid, signal.SIGKILL)
        proc.wait()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--binary", type=Path, required=True, help="the duplex-built zonos3-server")
    ap.add_argument("--weights", type=Path, default=WEIGHTS)
    ap.add_argument("--out", type=Path, default=OUT)
    ap.add_argument("--case", action="append", choices=sorted(CASES), help="default: every case")
    a = ap.parse_args()
    a.out.mkdir(parents=True, exist_ok=True)
    binary_sha = hashlib.sha256(a.binary.read_bytes()).hexdigest()
    for name in a.case or sorted(CASES):
        case = CASES[name]
        # a voice-over is one line; a dialogue (API 1.32) each of its lines, joined by core.media.stitch
        lines = case.get("lines") or [{"voice": case["voice"], "text": case["text"], "style": ""}]
        with tempfile.TemporaryDirectory(prefix="zonos3-golden-") as tmp:
            requests = [native.body(x["text"], case["seed"], native.styled(case["params"], x["style"]),
                                    native.speaker(name, voice_value(x["voice"]), a.weights, Path(tmp)))
                        for x in lines]  # fmt: skip
        t0 = time.monotonic()
        proc, port = start(native.argv(a.weights, a.binary.resolve()))
        try:
            ready_s, spoken = time.monotonic() - t0, []
            t1 = time.monotonic()
            for request in requests:
                req = urllib.request.Request(f"http://127.0.0.1:{port}/v2/generate", json.dumps(request).encode(),
                                             {"Content-Type": "application/json"})  # fmt: skip
                with urllib.request.urlopen(req, timeout=native.REQUEST_S) as r:
                    spoken.append(r.read())
                    headers = dict(r.headers)
            speak_s = time.monotonic() - t1
        finally:
            stop(proc)
        wav, cues = spoken[0], None
        if "lines" in case:
            pcm, cues = media.stitch(lines, [native.pcm(name, w) for w in spoken], native.SAMPLE_RATE)
            buf = io.BytesIO()
            with wave.open(buf, "wb") as w:
                w.setnchannels(1)
                w.setsampwidth(2)
                w.setframerate(native.SAMPLE_RATE)
                w.writeframes(pcm)
            wav = buf.getvalue()
        request = requests[0] if len(requests) == 1 else requests
        (a.out / f"{name}.wav").write_bytes(wav)
        with wave.open(str(a.out / f"{name}.wav")) as w:
            rate, frames, channels, width = (
                w.getframerate(),
                w.getnframes(),
                w.getnchannels(),
                w.getsampwidth(),
            )
        meta = {"name": name, **case, "wav": f"{name}.wav", "wav_sha256": hashlib.sha256(wav).hexdigest(),
                "sample_rate": rate, "channels": channels, "sample_bytes": width,
                "duration_s": round(frames / rate, 3), "ready_s": round(ready_s, 2), "speak_s": round(speak_s, 2),
                "headers": {k: v for k, v in headers.items() if k.lower().startswith("x-")},
                "request": [{k: v for k, v in r.items() if not k.endswith("_base64")} for r in requests]
                if "lines" in case else {k: v for k, v in request.items() if not k.endswith("_base64")},
                **({"cues": cues} if cues else {}),
                "argv": [Path(x).name if x.startswith("/") else x for x in native.argv(a.weights, a.binary)],
                "binary_sha256": binary_sha, "zonos3": "96d9e0ec1e39083ed336ac78f24e3dbc93721c0f"}  # fmt: skip
        (a.out / f"{name}.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False) + "\n")
        print(f"{name}: {meta['duration_s']} s, sha256 {meta['wav_sha256'][:16]}…")


if __name__ == "__main__":
    main()
