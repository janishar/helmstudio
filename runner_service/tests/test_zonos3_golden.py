"""The zonos3 pack against the reference build's goldens (tests/golden/zonos3/, recorded by
tests/golden/record_zonos3.py from a zonos3-server built from the duplex checkout at the vendored commit): runs the
pack's speak (native.speak with the vendored build, `make zonos3`) or, for a case with lines, its dialogue with each
golden's case and compares the samples byte for byte (and a dialogue's cues exactly). Prints the run's wall time and the server child's peak footprint (sampled every 50 ms). Loads real
weights (~0.6 GB, 1.6 GB child): stop nothing, it is small, but never run it on the dev stack's runner.

    .venv/bin/python tests/test_zonos3_golden.py [preset-savannah-s1 tense-savannah-s1 clone-uncle-fu-s1]
    .venv/bin/python -m pytest -m gpu -s tests/test_zonos3_golden.py
"""

import io
import json
import sys
import tempfile
import threading
import time
import wave
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))

GOLDEN = HERE / "golden" / "zonos3"
CASES = sorted(p.stem for p in GOLDEN.glob("*.json"))


class Ctx:
    node = "speak"

    def __init__(self) -> None:
        self.peak, self.steps = 0.0, []

    def check(self) -> None:
        pass

    def progress(self, *a) -> None:
        self.steps.append(a)


def frames(wav: bytes) -> bytes:
    with wave.open(io.BytesIO(wav)) as w:
        return w.readframes(w.getnframes())


def run(name: str) -> dict:
    from golden.record_zonos3 import voice_value

    import config
    from core import child, pack, residency
    from packs.zonos3 import native

    case = json.loads((GOLDEN / f"{name}.json").read_text())
    if not native.BINARY.is_file():
        pytest.skip("zonos3-server is not built (make zonos3)")
    p = next(x for x, _ in pack.for_env("native") if x.name == "zonos3")
    model = pack.ModelRef(p, "mini", "int8")
    if not (model.folder() / "gguf/backbone-ptq.gguf").is_file():
        pytest.skip("ZONOS3-Mini is not in the models folder")
    ctx, done = Ctx(), threading.Event()

    def sample() -> None:  # the child's phys_footprint while it lives
        while not done.wait(0.05):
            if pid := child.LIVE["pid"]:
                ctx.peak = max(ctx.peak, residency.footprint_gb(pid) or 0.0)

    keep = config.CACHE
    with tempfile.TemporaryDirectory() as cache:
        config.CACHE = Path(cache)  # the pid file: never the dev runner's
        threading.Thread(target=sample, daemon=True).start()
        t0 = time.monotonic()
        try:
            if "lines" in case:  # API 1.32: a dialogue, one server for its lines
                lines = [x | {"voice": voice_value(x["voice"])} for x in case["lines"]]
                out = native.dialogue(
                    ctx, case["params"] | {"lines": lines}, {"model": model, "seed": case["seed"]}
                )
            else:
                params = case["params"] | {"voice": voice_value(case["voice"])}
                out = native.speak(
                    ctx, params, {"model": model, "text": case["text"], "seed": case["seed"]}
                )
        finally:
            config.CACHE = keep
            done.set()
        wall = time.monotonic() - t0
    want = frames((GOLDEN / case["wav"]).read_bytes())
    audio = out["audio"]
    return {"name": name, "same": audio["wav"] == want and audio.get("cues") == case.get("cues"), "duration_s": round(audio["duration_s"], 3),
            "wall_s": round(wall, 2), "peak_gb": round(ctx.peak, 2), "sample_rate": audio["sample_rate"]}  # fmt: skip


@pytest.mark.gpu
@pytest.mark.parametrize("name", CASES)
def test_the_pack_speaks_the_reference_builds_bytes(name):
    got = run(name)
    print(got)
    assert got["same"] and got["sample_rate"] == 44100, got


if __name__ == "__main__":
    for n in sys.argv[1:] or CASES:
        print(run(n))
