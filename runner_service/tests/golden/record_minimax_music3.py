"""Record the minimax_music3 pack's golden from the model card's own code path, as
~/GenAI/model-samples/minimax-music3/run_samples.py runs it (ModularPipeline.from_pretrained + load_components in bf16,
one pipeline call, a seeded generator on the device), on this env's diffusers 0.43:

    uv run --extra torch python tests/golden/record_minimax_music3.py [--weights ~/GenAI/models] \
        [--samples ~/GenAI/model-samples/minimax-music3] [--out tests/golden/minimax-music3]

The case is the samples' song (its style and lyrics from run_samples.py), cut to 10 s. Writes <name>.json (the case,
the sha256 of the whole PCM 16-bit, quantized as the pack does, its length, RMS, peak, timing, versions) and
<name>.wav (its first 2 s, so the golden stays small). tests/test_minimax_music3_golden.py runs the pack's workflow
and compares the whole PCM by hash. Loads about 22 GB: never beside another model process."""

import argparse
import ast
import hashlib
import json
import platform
import re
import sys
import time
import wave
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[1]))

NAME = "song-10s-s42"
KEEP_S = 2.0


def song(samples: Path) -> dict:
    """The samples' SONG (style and lyrics), read from run_samples.py without running it."""
    src = (samples / "run_samples.py").read_text()
    block = re.search(r"SONG = dict\((.*?)\)\n", src, re.DOTALL).group(1)
    call = ast.parse(f"dict({block})", mode="eval").body
    return {k.arg: ast.literal_eval(k.value) for k in call.keywords}


def main() -> None:
    import numpy as np
    import torch
    from diffusers import ModularPipeline

    from core import loaders_torch as lt
    from core.media import pcm16

    ap = argparse.ArgumentParser()
    ap.add_argument("--weights", type=Path, default=Path.home() / "GenAI/models")
    ap.add_argument("--samples", type=Path, default=Path.home() / "GenAI/model-samples/minimax-music3")
    ap.add_argument("--out", type=Path, default=HERE / "minimax-music3")
    args = ap.parse_args()
    folder, dev = str(args.weights / "MiniMax-Music3"), lt.device()
    case = {"name": NAME, **song(args.samples), "seconds": 10.0, "seed": 42, "instrumental": False}

    t = time.perf_counter()
    pipe = ModularPipeline.from_pretrained(folder)
    pipe.load_components(dtype=torch.bfloat16, pretrained_model_name_or_path=folder)
    pipe.to(dev)
    load_s = time.perf_counter() - t
    t = time.perf_counter()
    audio = pipe(prompt=case["prompt"], lyrics=case["lyrics"], audio_duration=case["seconds"],
                 generator=torch.Generator(dev).manual_seed(case["seed"]), output="audios")[0]  # fmt: skip
    gen_s = time.perf_counter() - t
    rate, pcm = pipe.sampling_rate, pcm16(audio.T)
    args.out.mkdir(parents=True, exist_ok=True)
    with wave.open(str(args.out / f"{NAME}.wav"), "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm[: int(KEEP_S * rate) * 4])
    import diffusers
    import transformers

    case |= {"sha256": hashlib.sha256(pcm).hexdigest(), "sample_rate": rate, "channels": 2,
             "duration_s": round(audio.shape[-1] / rate, 4), "rms": round(float(np.sqrt((audio**2).mean())), 4),
             "peak": round(float(np.abs(audio).max()), 4), "load_s": round(load_s, 1), "generate_s": round(gen_s, 1),
             "versions": {"torch": torch.__version__, "diffusers": diffusers.__version__,
                          "transformers": transformers.__version__, "python": platform.python_version()},
             "device": f"{dev}, {platform.machine()}, {platform.system()} {platform.release()}"}  # fmt: skip
    (args.out / f"{NAME}.json").write_text(json.dumps(case, indent=2, ensure_ascii=False) + "\n")
    print(json.dumps({k: v for k, v in case.items() if k != "lyrics"}))


if __name__ == "__main__":
    main()
