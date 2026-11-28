"""Record the stable_audio_3 pack's goldens from the reference: Stability's stable-audio-3 (4007b6f) as the sample run
loads it (~/GenAI/model-samples/stable-audio-3-small-sfx/run_samples.py: load_diffusion_cond on the local
model_config.json, fp32, the t5gemma folder pointed at the weights), StableAudioModel.generate with each case's
settings. Run it in the samples' venv (torch 2.15, transformers 5.24; not the runner's), on MPS, one model process at a
time:

    ~/GenAI/model-samples/.venvs/stable-audio-3/bin/python tests/golden/record_stable_audio_3.py \
        --samples ~/GenAI/model-samples --weights ~/GenAI/models [--case NAME ...]

The source of the edits is the first 2 s of the sample run's door.wav (source.wav here). Each case writes <name>.wav
(the whole take, 2 s: PCM 16-bit as the pack writes it) and <name>.json (the case, length, RMS, peak, timing, the
process's peak footprint). tests/test_stable_audio_3_golden.py runs the pack's workflows with the same settings and
compares the samples by SNR."""

import argparse
import ctypes
import json
import os
import sys
import threading
import time
import wave
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
OUT = HERE / "stable_audio_3"
RATE = 44100
CASES = {
    "sound-rain-s42": {"workflow": "sa3-sound", "prompt": "heavy rain on a window, distant thunder", "seconds": 2.0,
                       "seed": 42},
    "vary-s7": {"workflow": "sa3-vary", "prompt": "heavy metal door slamming shut", "change": 0.7, "seed": 7},
    "repaint-s7": {"workflow": "sa3-repaint", "prompt": "glass shattering on a stone floor", "span": [0.5, 1.5],
                   "seed": 7},
    "extend-s7": {"workflow": "sa3-extend", "prompt": "old wooden door creaking open", "source_s": 1.0,
                  "seconds": 1.0, "where": "end", "seed": 7},
}  # fmt: skip


class Rusage(ctypes.Structure):  # rusage_info_v2, as the sample run reads it
    _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [(n, ctypes.c_uint64) for n in (
        "user", "system", "pkg_idle", "intr", "pageins", "wired", "resident", "phys_footprint", "start", "exit",
        "child_user", "child_system", "child_idle", "child_intr", "child_pageins", "child_elapsed", "diskio_read",
        "diskio_written")]  # fmt: skip


PEAK = [0]


def footprint() -> int:
    r = Rusage()
    ctypes.CDLL("/usr/lib/libSystem.dylib").proc_pid_rusage(os.getpid(), 2, ctypes.byref(r))
    return r.phys_footprint


def sampler() -> None:
    while True:
        PEAK[0] = max(PEAK[0], footprint())
        time.sleep(0.02)


def pcm16(a: np.ndarray) -> bytes:
    """(2, n) float in [-1, 1] as interleaved PCM 16-bit, the pack's take()."""
    return (a.T * 32767).round().astype("<i2").tobytes()


def write_wav(path: Path, pcm: bytes) -> None:
    with wave.open(str(path), "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(pcm)


def read_wav(path: Path) -> np.ndarray:
    with wave.open(str(path)) as w:
        return np.frombuffer(w.readframes(w.getnframes()), "<i2").reshape(-1, 2).T.astype(np.float32) / 32768


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--samples", type=Path, default=Path.home() / "GenAI/model-samples")
    ap.add_argument("--weights", type=Path, default=Path.home() / "GenAI/models")
    ap.add_argument("--case", nargs="*", default=list(CASES))
    args = ap.parse_args()
    os.environ.setdefault("HF_HUB_OFFLINE", "1")
    sys.path.insert(0, str(args.samples / "src/stable-audio-3"))
    import torch
    from stable_audio_3.loading_utils import load_diffusion_cond
    from stable_audio_3.model import StableAudioModel

    w = args.weights / "Stable-Audio-3-Small-SFX"
    source = OUT / "source.wav"
    if not source.is_file():
        door = read_wav(args.samples / "stable-audio-3-small-sfx/door.wav")
        write_wav(source, pcm16(door[:, : 2 * RATE]))
    src = torch.from_numpy(read_wav(source))
    threading.Thread(target=sampler, daemon=True).start()
    t = time.perf_counter()
    cfg = json.loads((w / "model_config.json").read_text())
    for c in cfg["model"]["conditioning"]["configs"]:
        if c["type"] == "t5gemma":
            c["config"]["repo_id"] = str(w)
    model = load_diffusion_cond(cfg, str(w / "model.safetensors"), device="mps", model_half=False)
    model.use_lora, model.lora_names = False, []
    sa = StableAudioModel(model, cfg, "mps", False)
    load_s = round(time.perf_counter() - t, 1)
    for name in args.case:
        c = CASES[name]
        kw = {"prompt": c["prompt"], "steps": 8, "cfg_scale": 1.0, "seed": c["seed"]}
        if c["workflow"] == "sa3-sound":
            kw["duration"] = c["seconds"]
        elif c["workflow"] == "sa3-vary":
            kw |= {
                "duration": src.shape[-1] / RATE,
                "init_audio": (RATE, src),
                "init_noise_level": c["change"],
            }
        elif c["workflow"] == "sa3-repaint":
            kw |= {"duration": src.shape[-1] / RATE, "inpaint_audio": (RATE, src),
                   "inpaint_mask_start_seconds": c["span"][0], "inpaint_mask_end_seconds": c["span"][1]}  # fmt: skip
        else:  # extend at the end: the source first, then silence the inpaint fills
            n, add = int(c["source_s"] * RATE), int(c["seconds"] * RATE)
            placed = torch.zeros(2, n + add)
            placed[:, :n] = src[:, :n]
            kw |= {"duration": (n + add) / RATE, "inpaint_audio": (RATE, placed),
                   "inpaint_mask_start_seconds": n / RATE, "inpaint_mask_end_seconds": (n + add) / RATE}  # fmt: skip
        torch.mps.synchronize()
        t = time.perf_counter()
        audio = sa.generate(**kw)
        torch.mps.synchronize()
        gen_s = round(time.perf_counter() - t, 2)
        a = audio[0].cpu().numpy()
        pcm = pcm16(a)
        write_wav(OUT / f"{name}.wav", pcm)
        s = np.frombuffer(pcm, "<i2").astype(np.float64) / 32768
        meta = c | {"length_s": a.shape[-1] / RATE, "channels": 2, "sample_rate": RATE,
                    "rms": round(float(np.sqrt(np.mean(s**2))), 5), "peak": round(float(np.abs(s).max()), 5),
                    "reference": "stable-audio-3 4007b6f StableAudioModel.generate, mps, fp32 (T5Gemma bf16)",
                    "torch": torch.__version__, "load_s": load_s, "gen_s": gen_s,
                    "peak_footprint_gb": round(max(PEAK[0], footprint()) / 2**30, 2)}  # fmt: skip
        (OUT / f"{name}.json").write_text(json.dumps(meta, indent=2) + "\n")
        print(name, {k: meta[k] for k in ("length_s", "rms", "peak", "gen_s", "peak_footprint_gb")})


if __name__ == "__main__":
    main()
