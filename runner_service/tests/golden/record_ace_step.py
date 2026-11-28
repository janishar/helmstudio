"""Records the ace_step goldens (tests/golden/ace_step/) on this torch env: the source (the first 6 s of the
ACE-Step sample run's song, 48 kHz stereo MP3) and, per case of tests/test_ace_step_golden.py, its settings and
metrics JSON (the whole WAV's SHA-256, facts, level, sections, the plan's texts, seconds and peak GB per node, cold
and warm) and its first 2 s as a WAV. Every case runs twice in one process (the second warm, the node cache cleared)
and must repeat bit for bit: the planner and the DiT are seeded. Take /tmp/helmstudio-gpu.lock first.

    uv run --extra torch python tests/golden/record_ace_step.py ~/GenAI/model-samples ~/GenAI/models [case ...]
"""

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent

if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("samples", type=Path, help="the model-samples folder (ace-step-1.5/song.mp3, results.md)")
    ap.add_argument("models", type=Path, help="the weights root (ACE-Step-1.5, ACE-Step-1.5-XL-Turbo)")
    ap.add_argument("cases", nargs="*")
    args = ap.parse_args()
    os.environ["HELMSTUDIO_MODELS"] = str(args.models.expanduser())  # before core reads config
    sys.path.insert(0, str(HERE.parent))
    import test_ace_step_golden as g

    g.GOLDEN.mkdir(parents=True, exist_ok=True)
    src = HERE / g.SOURCE
    if not src.is_file():
        subprocess.run(["ffmpeg", "-v", "error", "-y", "-i", str(args.samples.expanduser() / "ace-step-1.5/song.mp3"),
                        "-t", str(g.SOURCE_S), "-ar", "48000", "-ac", "2", "-b:a", "128k", str(src)], check=True)  # fmt: skip
    import torch

    b, variant = g.AceBench(), None
    names = sorted(args.cases or list(g.CASES), key=lambda n: g.CASES[n]["variant"] != "turbo-2b")
    for name in names:
        if variant not in (None, g.CASES[name]["variant"]):
            b.residency.unload()  # one DiT's run at a time, so each variant's first case loads cold
        variant = g.CASES[name]["variant"]
        runs = []
        for _ in range(2):
            runs.append(b.song(name))
            b.cache = b.graph.Cache()
        cold, warm = runs
        assert cold["sha256"] == warm["sha256"], f"{name}: the seed did not repeat"
        meta = {"name": name, **g.CASES[name], "style": g.CASES[name].get("style", g.STYLE), "lyrics": g.LYRICS,
                **{k: cold[k] for k in ("sha256", "facts", "rms", "peak", "sections", "texts")},
                "cold": {k: cold[k] for k in ("s", "peak_gb", "total_s", "before_gb", "resident_gb")},
                "warm": {k: warm[k] for k in ("s", "peak_gb", "total_s", "resident_gb")},
                "wav": f"{name}.wav", "kept_s": g.KEEP_S,
                "versions": {"torch": torch.__version__, "python": sys.version.split()[0]},
                "device": b.lt.device()}  # fmt: skip
        (g.GOLDEN / f"{name}.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False) + "\n")
        (g.GOLDEN / f"{name}.wav").write_bytes(g.head(cold["wav"]))
        print(
            name,
            {k: meta[k] for k in ("rms", "peak", "facts")},
            "cold",
            meta["cold"],
            "warm",
            meta["warm"],
        )
    b.residency.unload()
