"""The minimax_h3 pack against the h3 goldens (runner_service/tests/golden/minimax-h3-*, recorded by
tests/golden/record_h3.py with the vendored binary at h3.c 4cafd17, 512 x 512 x 22 frames, 4 steps (the API 1.22
reference cases: 2 steps, a 2 s audio or silent video reference), seed 42, exact; API 1.37 t2v-render75: the same
t2v at Render 75 %, h3 rendering 384 x 384, recorded by this pack's own run, `params` in its json):
runs the pack's workflows in-process (graph.run with the native residency manager; no NATS) with a golden's
settings, so the real h3 child runs on the GPU, and compares the MP4's decoded frames and sound with the stored one.

- t2v repeats bit for bit: the decoded RGB24 frames and f32le PCM must hash to the golden's;
- the image modes (FL2VA, Ref2VA) do not always repeat at this h3.c (two runs of one argv differ by 37-40 dB:
  packs/minimax_h3/GOTCHAS.md), so they pass at PSNR >= BAR against the stored MP4, with 22 frames and a sound track.

Loads the real weights (the child peaks ~18.5 GB at this size): stop the runners first (make runners-stop), build the
binary (make h3).

    uv run python tests/test_h3_golden.py [t2v fl2va-first fl2va-first-last ref2va-image ref2va-image-audio
        ref2va-silent-video]
    uv run pytest -m gpu -s tests/test_h3_golden.py"""

import argparse
import asyncio
import hashlib
import json
import re
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

GOLDEN = HERE / "golden"
CASES = {"t2v": "minimax-h3-t2v", "fl2va-first": "minimax-h3-fl2va-first",
         "fl2va-first-last": "minimax-h3-fl2va-first-last", "ref2va-image": "minimax-h3-ref2va-image",
         "ref2va-image-audio": "minimax-h3-ref2va-image-audio",
         "ref2va-silent-video": "minimax-h3-ref2va-silent-video",
         "t2v-render75": "minimax-h3-t2v-render75"}  # fmt: skip
BAR = 30.0  # dB, image modes (below the 36.9-39.8 dB spread of repeated runs)
# a silent video reference repeats less (2 steps): its two recorded runs differ by 29.5 dB, the pack's run vs the
# golden 31.8 dB; the pack's cut gives h3 the same decoded pixels as the recorder's input (checked on the CPU)
# first + last frame spreads wider too (robust pass: the pack 25.8-27.0 dB, the raw binary 30.0, with pixel-equal inputs)
BARS = {"ref2va-silent-video": 27.0, "fl2va-first-last": 25.0}
# the old speed enum's flags as API 1.37 params (the templates open on Default: 45 layers, reuse 2)
SPEED = {
    "exact": {"layers": 50, "reuse": 1, "core_reuse": 1, "token_reduction": False, "render": "Full"}
}


def image_value(n: int, rel: str) -> dict:
    """A resolved Image value (defs image) for a golden file, read back by `fetch`."""
    from PIL import Image

    data = (GOLDEN / rel).read_bytes()
    with Image.open(GOLDEN / rel) as im:
        w, h = im.size
    return {"take_id": f"00000000-0000-4000-8000-{n:012d}", "key": f"u/golden/{rel}", "content_type": "image/png",
            "width": w, "height": h, "bytes": len(data), "etag": hashlib.sha256(data).hexdigest()[:32]}  # fmt: skip


def media_value(n: int, rel: str) -> dict:
    """A resolved media value (defs media) for a golden WAV or MP4, the whole take as its part (in 0, out its end)."""
    data = (GOLDEN / rel).read_bytes()
    r = subprocess.run(["ffprobe", "-v", "error", "-count_packets", "-show_entries",
                        "format=duration:stream=codec_type,width,height,nb_read_packets", "-of", "json",
                        str(GOLDEN / rel)], capture_output=True, text=True, check=True)  # fmt: skip
    info = json.loads(r.stdout)
    d = round(float(info["format"]["duration"]), 3)
    v = next((x for x in info["streams"] if x["codec_type"] == "video"), None)
    out = {"take_id": f"00000000-0000-4000-8000-{n:012d}", "key": f"u/golden/{rel}", "bytes": len(data),
           "etag": hashlib.sha256(data).hexdigest()[:32], "duration_s": d, "in": 0, "out": d,
           "has_audio": any(x["codec_type"] == "audio" for x in info["streams"])}  # fmt: skip
    if v is None:
        return out | {"kind": "audio", "content_type": "audio/wav"}
    return out | {"kind": "video", "content_type": "video/mp4", "width": v["width"], "height": v["height"],
                  "fps": 24, "frames": int(v["nb_read_packets"])}  # fmt: skip


async def fetch(key: str, limit: int, etag: str | None = None) -> bytes | None:
    path = GOLDEN / key.removeprefix("u/golden/")
    return path.read_bytes()[: limit + 1] if path.is_file() else None


def settings(name: str) -> dict:
    return json.loads((GOLDEN / CASES[name] / f"{name}.json").read_text())


class Bench:
    def __init__(self) -> None:
        from core import graph, registry
        from core import loaders_native as ln
        from core.residency import Residency

        self.g, self.ln = graph, ln
        values, self.impls, packs = registry.build("native")
        self.schemas = registry.node_schemas(values)
        self.workflows = {w["id"]: w for p in packs for w in p.workflows}
        self.residency = Residency(ln.total_gb() - 12, free=ln.free, measure=ln.measure_gb)
        self.cache = self.g.Cache()

    def nodes(self, name: str) -> dict:
        s = settings(name)
        files = {role: f"{CASES[name]}/{x['file']}" for role, x in s["inputs"].items()}
        wid = (
            "text-to-video"
            if name.startswith("t2v")
            else "references"
            if name.startswith("ref2va")
            else "image-to-video"
        )
        nodes = json.loads(json.dumps(self.workflows[f"minimax-h3-{wid}"]["nodes"]))
        nodes["prompt"]["params"]["text"] = s["prompt"]
        nodes["seed"]["params"]["seed"] = s["seed"]
        nodes["canvas"]["params"] |= {"width": s["width"], "height": s["height"]}
        nodes["duration"]["params"]["frames"] = s["frames"]
        # the goldens name their speed (exact, before API 1.37) or, from 1.37, the generate params they set
        nodes["generate"]["params"] |= {"steps": s["steps"]} | (
            s.get("params") or SPEED[s["speed"]]
        )
        if "first" in files:
            nodes["first"]["params"]["image"] = image_value(1, files["first"])
        if "last" in files:
            nodes["last"]["params"]["image"] = image_value(2, files["last"])
        for nid in [
            n for n, x in nodes.items() if x.pop("optional", False) and x["params"]["image"] is None
        ]:
            del nodes[nid]  # as studio drops an empty optional node with its wires (API 1.22)
            for x in nodes.values():
                x["in"] = {k: r for k, r in x.get("in", {}).items() if r.partition(".")[0] != nid}
        if "ref" in files:
            nodes["generate"]["params"]["refs"] = [{"image": image_value(3, files["ref"])}]
        if "audio" in files:
            nodes["generate"]["params"]["audios"] = [{"audio": media_value(4, files["audio"])}]
        if "video" in files:
            nodes["generate"]["params"]["videos"] = [
                {"video": media_value(5, files["video"]), "sound": "silent"}
            ]
        return nodes

    def run(self, nodes: dict) -> dict:
        """One run: {mp4, phases (seen in order), peak_gb (runner + child, 50 ms sampling), s}."""
        g = self.g
        assert not g.validate(nodes, self.schemas)
        nodes, _ = g.resolve(nodes, self.schemas)
        out: dict = {"mp4": None, "phases": [], "peak_gb": 0.0}

        async def progress(node, step, of, phase, eta, phase_id=None):  # API 1.36: the phase id too
            if not out["phases"] or out["phases"][-1] != phase:
                out["phases"].append(phase)

        async def output(index, data, **meta):
            out["mp4"], out["video"] = data, meta.get("video")

        ctx = g.Ctx(progress=progress, output=output, check=lambda: None, owner="golden",
                    residency=self.residency, device=self.ln.device())  # fmt: skip
        ctx.fetch = fetch
        stop = threading.Event()

        def sample() -> None:
            while not stop.wait(0.05):
                out["peak_gb"] = max(out["peak_gb"], self.ln.measure_gb() or 0.0)

        t, sampler = time.monotonic(), threading.Thread(target=sample)
        sampler.start()
        try:
            asyncio.run(g.run(nodes, self.impls, self.schemas, ctx, self.cache))
        finally:
            stop.set()
            sampler.join()
            self.residency.done()
        out["s"], out["peak_gb"] = round(time.monotonic() - t, 1), round(out["peak_gb"], 2)
        return out


def sha(mp4: Path, *args: str) -> str:
    r = subprocess.run(
        ["ffmpeg", "-v", "error", "-i", str(mp4), *args, "-"], capture_output=True, check=True
    )
    return hashlib.sha256(r.stdout).hexdigest()


def psnr(a: Path, b: Path) -> float:
    r = subprocess.run(["ffmpeg", "-i", str(a), "-i", str(b), "-lavfi", "[0:v][1:v]psnr", "-f", "null", "-"],
                       capture_output=True, text=True, check=True)  # fmt: skip
    m = re.search(r"average:([\d.]+|inf)", r.stderr)
    return float(m.group(1))


def case(bench: Bench, name: str) -> dict:
    s = settings(name)
    r = bench.run(bench.nodes(name))
    with tempfile.TemporaryDirectory() as tmp:
        mp4 = Path(tmp) / "run.mp4"
        mp4.write_bytes(r.pop("mp4"))
        rgb = sha(mp4, "-map", "0:v:0", "-f", "rawvideo", "-pix_fmt", "rgb24")
        pcm = sha(mp4, "-map", "0:a:0", "-f", "f32le")
        exact = rgb == s["rgb24_sha256"] and pcm == s["pcm_f32le_sha256"]
        r["psnr"] = psnr(GOLDEN / CASES[name] / s["mp4"]["file"], mp4)
    video = r.pop("video")
    shape = (video["frames"], video["fps"], video["has_audio"]) == (s["frames"], 24, True)
    r["exact"], r["ok"] = (
        exact,
        shape and (exact if name.startswith("t2v") else r["psnr"] >= BARS.get(name, BAR)),
    )
    return r


@pytest.fixture(scope="module")
def bench():
    from core import pack

    if not (pack.models_root() / "MiniMax-H3/FL2VA").is_dir():
        pytest.skip("MiniMax-H3 is not in the models folder")
    if not (HERE.parent / "vendor/h3c/h3").is_file():
        pytest.skip("the h3 binary is not built (make h3)")
    return Bench()


@pytest.mark.gpu
@pytest.mark.parametrize("name", list(CASES))
def test_matches_the_golden(bench, name):
    r = case(bench, name)
    print(name, r)
    assert r["ok"]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("cases", nargs="*", default=["t2v"], choices=list(CASES))
    args = ap.parse_args()
    b, failed = Bench(), False
    for name in args.cases:
        r = case(b, name)
        failed |= not r["ok"]
        print(name, json.dumps(r), flush=True)
    sys.exit(1 if failed else 0)
