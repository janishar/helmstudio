"""Record the mmaudio pack's goldens from MMAudio itself: the upstream clone (~/GenAI/model-samples/src/MMAudio at
974010a, the commit vendored in packs/mmaudio/vendor) run exactly as the sample script
(~/GenAI/model-samples/mmaudio/run_samples.py) runs it: get_my_mmaudio + FeaturesUtils loaded on the CPU, moved to
the device at bf16, eval_utils.generate with 25 Euler steps and an empty negative prompt, the seed in a device
generator, PyAV's load_video for the picture. Runs in the samples' venv (PyAV, librosa, omegaconf: not the runner's):

    ~/GenAI/model-samples/.venvs/mmaudio/bin/python tests/golden/record_mmaudio.py \\
        [--samples ~/GenAI/model-samples] [--models ~/GenAI/models]

Writes golden/mmaudio/<case>.wav (16-bit PCM, as the pack writes it) and <case>.json (the case, its metrics, timing
and peak footprint), and the case's source clip truck-2s.mp4 (made once with ffmpeg from the sample's truck_8s.mp4:
the first 2.5 s at 640 x 360, H.264, no sound) so the golden stays small."""

import argparse
import ctypes
import hashlib
import io
import json
import os
import subprocess
import sys
import time
import wave
from pathlib import Path

import numpy as np

OUT = Path(__file__).resolve().parent / "mmaudio"
CLIP = OUT / "truck-2s.mp4"
CASES = {
    "text-rain-s42": {"workflow": "mmaudio-sound", "text": "heavy rain on a window, distant thunder", "seconds": 2.0,
                      "seed": 42, "guidance": 4.5, "steps": 25, "avoid": ""},
    "video-truck-s42": {"workflow": "mmaudio-video", "text": "pickup truck engine driving on a road", "seconds": 2.0,
                        "seed": 42, "guidance": 4.5, "steps": 25, "avoid": "", "video": CLIP.name},
}  # fmt: skip


class RusageV2(ctypes.Structure):
    _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [(n, ctypes.c_uint64) for n in (
        "user", "system", "pkg_idle", "intr", "pageins", "wired", "resident", "phys_footprint", "start", "exit",
        "child_user", "child_system", "child_idle", "child_intr", "child_pageins", "child_elapsed", "diskio_read",
        "diskio_written")]  # fmt: skip


def footprint_gb() -> float:
    r = RusageV2()
    ctypes.CDLL("/usr/lib/libSystem.dylib").proc_pid_rusage(os.getpid(), 2, ctypes.byref(r))
    return r.phys_footprint / 2**30


def metrics(pcm: bytes, rate: int) -> dict:
    x = np.frombuffer(pcm, "<i2").astype(np.float64) / 32768
    return {"sample_rate": rate, "channels": 1, "samples": len(x), "duration_s": round(len(x) / rate, 4),
            "rms": round(float(np.sqrt(np.mean(x**2))), 5), "peak": round(float(np.abs(x).max()), 5)}  # fmt: skip


def wav(pcm: bytes, rate: int) -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm)
    return buf.getvalue()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--samples", type=Path, default=Path.home() / "GenAI/model-samples")
    ap.add_argument("--models", type=Path, default=Path.home() / "GenAI/models")
    a = ap.parse_args()
    os.environ.setdefault("HF_HUB_OFFLINE", "1")
    sys.path.insert(0, str(a.samples / "src/MMAudio"))
    import mmaudio.model.utils.features_utils as fu
    import torch
    from mmaudio.eval_utils import all_model_cfg, generate, load_video
    from mmaudio.ext.autoencoder import autoencoder as ae
    from mmaudio.model.flow_matching import FlowMatching
    from mmaudio.model.networks import get_my_mmaudio

    OUT.mkdir(exist_ok=True)
    if not CLIP.is_file():
        subprocess.run(["ffmpeg", "-v", "error", "-y", "-i", str(a.samples / "mmaudio/truck_8s.mp4"), "-t", "2.5",
                        "-vf", "scale=640:360", "-an", "-c:v", "libx264", "-crf", "28", "-preset", "medium",
                        "-pix_fmt", "yuv420p", "-movflags", "+faststart", str(CLIP)], check=True)  # fmt: skip
    m = a.models
    _clip = fu.create_model_from_pretrained
    fu.create_model_from_pretrained = lambda name, **kw: _clip(
        f"local-dir:{m / 'DFN5B-CLIP-ViT-H-14-384'}", **kw
    )
    _voc = ae.BigVGANv2.from_pretrained
    ae.BigVGANv2.from_pretrained = lambda repo, **kw: _voc(
        str(m / "bigvgan_v2_44khz_128band_512x"), **kw
    )
    dev, dtype = "mps" if torch.backends.mps.is_available() else "cpu", torch.bfloat16
    cfg = all_model_cfg["large_44k_v2"]
    seq = cfg.seq_cfg
    t0 = time.monotonic()
    net = get_my_mmaudio(cfg.model_name).to(dev, dtype).eval()
    net.load_weights(
        torch.load(
            m / "MMAudio/weights/mmaudio_large_44k_v2.pth", map_location=dev, weights_only=True
        )
    )
    feats = fu.FeaturesUtils(tod_vae_ckpt=str(m / "MMAudio/ext_weights/v1-44.pth"),
                             synchformer_ckpt=str(m / "MMAudio/ext_weights/synchformer_state_dict.pth"),
                             enable_conditions=True, mode=cfg.mode, bigvgan_vocoder_ckpt=None,
                             need_vae_encoder=False).to(dev, dtype).eval()  # fmt: skip
    load_s, load_gb = time.monotonic() - t0, footprint_gb()
    for name, case in CASES.items():
        t = time.monotonic()
        clip = sync = None
        seconds = case["seconds"]
        if "video" in case:
            vi = load_video(OUT / case["video"], seconds)
            clip, sync, seconds = (
                vi.clip_frames.unsqueeze(0),
                vi.sync_frames.unsqueeze(0),
                vi.duration_sec,
            )
        seq.duration = seconds
        net.update_seq_lengths(seq.latent_seq_len, seq.clip_seq_len, seq.sync_seq_len)
        rng = torch.Generator(device=dev)
        rng.manual_seed(case["seed"])
        with torch.inference_mode():
            audio = generate(clip, sync, [case["text"]], negative_text=[case["avoid"]], feature_utils=feats, net=net,
                             fm=FlowMatching(min_sigma=0, inference_mode="euler", num_steps=case["steps"]),
                             rng=rng, cfg_strength=case["guidance"])  # fmt: skip
        if dev == "mps":
            torch.mps.synchronize()
        x = audio.float().cpu()[0, 0].numpy()
        pcm = (np.clip(x, -1, 1) * 32767).round().astype("<i2").tobytes()  # the pack's conversion
        data = wav(pcm, seq.sampling_rate)
        (OUT / f"{name}.wav").write_bytes(data)
        out = case | {"name": name, "metrics": metrics(pcm, seq.sampling_rate),
                      "wav_sha256": hashlib.sha256(data).hexdigest(), "generate_s": round(time.monotonic() - t, 2),
                      "load_s": round(load_s, 1), "load_footprint_gb": round(load_gb, 2),
                      "footprint_gb": round(footprint_gb(), 2), "device": dev, "torch": torch.__version__}  # fmt: skip
        (OUT / f"{name}.json").write_text(json.dumps(out, indent=2) + "\n")
        print(name, out["metrics"], out["generate_s"], "s", out["footprint_gb"], "GB")


if __name__ == "__main__":
    main()
