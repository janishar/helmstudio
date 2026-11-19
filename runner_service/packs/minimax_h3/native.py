"""Stage functions of the minimax_h3 pack (runtime native): MiniMax H3 video with sound, run end to end by the pinned
h3 binary (runner_service/vendor/h3c, `make h3`) as a child process (core.child), with cwd = its folder (it opens
h3_shaders.metal by relative path), so every path it gets is absolute. Modes follow the inputs, as the h3 CLI: refs →
Ref2VA (`--ref-image`, `<Picture n>`), a first and/or last frame → FL2VA, neither → text to video. Speed exact is
`--layers 50 --reuse 1`, fast the old studio's fast preset `--layers 45 --reuse 2`; `--use-int8-row-fc2` always (M5).
Image inputs are written as PNGs into a per-run temp folder, deleted after with the MP4. The Video value is built as
the ltx pack's: {mp4, poster, last, frames, fps, has_audio, stream} (core.media probe, frames as PNG). No torch."""

import tempfile
from pathlib import Path

from PIL import Image

from core import child, media
from core.graph import RunError

H3C = Path(__file__).resolve().parents[2] / "vendor/h3c"
BINARY = H3C / "h3"
MAX_PIXELS = 768 * 1344  # 1,032,192 (contracts/check.py H3_MAX_PIXELS)
FPS = 24
SPEED = {"exact": ["--layers", "50", "--reuse", "1"], "fast": ["--layers", "45", "--reuse", "2"]}
# h3's stderr phases → the run's progress phases (unknown ones pass as they are); the video VAE decode prints none
PHASES = {
    "tokenizer": "encoding",
    "text encoder": "encoding",
    "refine text": "encoding",
    "Qwen vision": "encoding images",
    "video VAE encoder": "encoding images",
    "audio VAE encoder": "encoding images",
    "precompute AdaLN": "loading transformer",
    "load transformer core": "loading transformer",
    "denoise enqueue": "sampling",
    "denoise": "sampling",
    "audio VAE": "decoding audio",
    "video VAE load": "decoding video",
    "FFmpeg": "writing mp4",
}
# no progress line for this long fails the run: the longest measured silence is the video VAE decode (104 s at
# 864 x 480 x 124, ~560 s at 1344 x 768 x 243); a denoise step at the largest canvas x 362 frames is ~12 min (est.)
# ponytail: flat; scale with mp x frames if a real run ever comes near it
IDLE_S = 1800


def generate_error(
    node: str, refs: int, first: bool, last: bool, width: int, height: int
) -> str | None:
    """contracts/check.py h3_generate_error (fixtures/video/minimax-h3.json): the run's error text, or None."""
    if refs and (first or last):
        return f"{node}: references cannot go with a first or last frame"
    if any(not 256 <= x <= 1344 or x % 32 for x in (width, height)):
        return f"{node}: the canvas is {width} × {height}; sides must be multiples of 32 from 256 to 1344"
    if width * height > MAX_PIXELS:
        return f"{node}: the canvas is {width} × {height}; at most {MAX_PIXELS} pixels (768 × 1344)"
    return None


def canvas(ctx, params: dict, inputs: dict) -> dict:
    return {"canvas": {"width": params["width"], "height": params["height"]}}


def duration(ctx, params: dict, inputs: dict) -> dict:
    return {"duration": {"frames": params["frames"], "fps": params["fps"]}}


def check_generate(params: dict, wired: set, models: dict | None = None) -> str | None:
    """Before anything runs: references with a first or last frame (graph.run puts the node id before the text; the
    canvas is checked in generate: it may come from another node)."""
    if params.get("refs") and wired & {"first", "last"}:
        return ": references cannot go with a first or last frame"
    return None


def argv(model_dir: Path, prompt: str, out: Path, *, width: int, height: int, frames: int, steps: int,
         seed: int, speed: str, images: list[tuple[str, Path]]) -> list[str]:  # fmt: skip
    """The h3 command line, in the goldens' order (tests/golden/record_h3.py)."""
    a = [str(BINARY), "-d", str(model_dir), "-p", prompt, "-o", str(out)]
    a += [
        "--width",
        str(width),
        "--height",
        str(height),
        "--frames",
        str(frames),
        "--steps",
        str(steps),
    ]
    a += ["--seed", str(seed), *SPEED[speed], "--use-int8-row-fc2"]
    for flag, path in images:
        a += [flag, str(path)]
    return a


def generate(ctx, params: dict, inputs: dict) -> dict:
    size, d = inputs["canvas"], inputs["duration"]
    refs = [r["image"] for r in params.get("refs") or []]
    first, last = inputs.get("first"), inputs.get("last")
    w, h = size["width"], size["height"]
    if why := generate_error(ctx.node, len(refs), first is not None, last is not None, w, h):
        raise RunError(why)
    if not BINARY.is_file():
        raise RunError(f"{ctx.node}: the h3 binary is not built (make h3 in runner_service)")
    with tempfile.TemporaryDirectory(prefix="h3-") as tmp:
        tmp = Path(tmp).resolve()
        images: list[tuple[str, Path]] = []
        roles = [
            ("--first-frame", first),
            ("--last-frame", last),
            *(("--ref-image", r) for r in refs),
        ]
        for i, (flag, im) in enumerate((f, im) for f, im in roles if im is not None):
            path = tmp / f"input-{i}.png"
            im.convert("RGB").save(path)
            images.append((flag, path))
        out = tmp / "out.mp4"
        cmd = argv(inputs["model"].folder().resolve(), inputs["prompt"], out, width=w, height=h, frames=d["frames"],
                   steps=params["steps"], seed=inputs["seed"], speed=params["speed"], images=images)  # fmt: skip
        ctx.progress(1, 1, "starting h3")  # a phase without steps: 1 of 1
        child.run(ctx, cmd, cwd=H3C, phases=PHASES, idle_s=IDLE_S)
        if not out.is_file():
            raise RunError(f"{ctx.node}: h3 wrote no video")
        return {"video": video_value(out, tmp)}


def video_value(mp4: Path, tmp: Path) -> dict:
    """The Video value of h3's MP4 (already H.264 + AAC with +faststart): frame 0 as the poster, the last frame, the
    stream facts."""
    try:
        info = media.probe(mp4)
        media.frame_png(mp4, 0, tmp / "poster.png")
        media.frame_png(mp4, info["frames"] - 1, tmp / "last.png")
    except media.MediaError as e:
        raise RunError(f"reading back the mp4 failed: {e}") from None
    with Image.open(tmp / "poster.png") as im:
        poster = im.copy()
    with Image.open(tmp / "last.png") as im:
        last = im.copy()
    return {"mp4": mp4.read_bytes(), "poster": poster, "last": last, "frames": info["frames"], "fps": FPS,
            "has_audio": info["audio"] is not None, "stream": info["stream"]}  # fmt: skip
