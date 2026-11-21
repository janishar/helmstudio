"""Stage functions of the minimax_h3 pack (runtime native): MiniMax H3 video with sound, run end to end by the pinned
h3 binary (runner_service/vendor/h3c, `make h3`) as a child process (core.child), with cwd = its folder (it opens
h3_shaders.metal by relative path), so every path it gets is absolute. Modes follow the inputs, as the h3 CLI: refs →
Ref2VA (`--ref-image` <Picture n>, `--ref-video` / `--ref-silent-video` <Video n>, `--ref-audio` <Audio n>, numbered
per kind in that order), a first and/or last frame → FL2VA, neither → text to video. Speed exact is
`--layers 50 --reuse 1`, fast the old studio's fast preset `--layers 45 --reuse 2`; `--use-int8-row-fc2` always (M5).
Image inputs are written as PNGs into a per-run temp folder, deleted after with the MP4; each video or audio
reference (API 1.22) is cut there to its in..out with ffmpeg (a video: lossless H.264, its sound as PCM when kept;
an audio, or a video's sound: a WAV). The Video value is built as
the ltx pack's: {mp4, poster, last, frames, fps, has_audio, stream} (core.media probe, frames as PNG). No torch."""

import subprocess
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
# h3.c's own Ref2VA limits (contracts/check.py H3_*): references in all, sound inputs, a video's sound needs 2 s of
# clip and of its part (frames on the 5 + 17k grid), all reference sound
MAX_REFS, MAX_SOUNDS, SOUND_FRAMES, MAX_SOUND_S = 12, 3, 56, 15.0


def generate_error(node: str, refs: int, first: bool, last: bool, width: int, height: int, frames: int = 124,
                   videos: list | None = None, audios: list | None = None) -> str | None:  # fmt: skip
    """contracts/check.py h3_generate_error (fixtures/video/minimax-h3.json): the run's error text, or None. refs =
    image references; videos = [seconds read, sound kept and the take has sound] per video reference; audios =
    seconds read per audio reference."""
    videos, audios = videos or [], audios or []
    sounds = [s for _, s in videos if s]
    if (refs or videos or audios) and (first or last):
        return f"{node}: references cannot go with a first or last frame"
    if audios and not (refs or videos):
        return f"{node}: audio references need an image or video reference"
    if (n := refs + len(videos) + len(audios)) > MAX_REFS:
        return f"{node}: at most {MAX_REFS} references in all ({n})"
    if (n := len(audios) + len(sounds)) > MAX_SOUNDS:
        return f"{node}: at most {MAX_SOUNDS} sound references (audio references and videos with sound; {n})"
    if sounds and frames < SOUND_FRAMES:
        return f"{node}: a video reference with sound needs a clip of at least {SOUND_FRAMES} frames ({frames})"
    for i, (x, s) in enumerate(videos, 1):
        if (
            s and (n := round(x * FPS)) < SOUND_FRAMES
        ):  # h3 keeps 5 + 17k frames, reads the sound that long
            return (
                f"{node}: a video reference with sound needs a part of at least {SOUND_FRAMES} frames at {FPS} fps "
                f"(video {i} reads {n})"
            )
    if (t := sum(audios) + sum(min(x, frames / FPS) for x, s in videos if s)) > MAX_SOUND_S + 1e-9:
        return f"{node}: the sound references last {t:.1f} s in all; at most {MAX_SOUND_S:.1f} s"
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
    if any(params.get(k) for k in ("refs", "videos", "audios")) and wired & {"first", "last"}:
        return ": references cannot go with a first or last frame"
    return None


def argv(model_dir: Path, prompt: str, out: Path, *, width: int, height: int, frames: int, steps: int,
         seed: int, speed: str, images: list[tuple[str, Path]]) -> list[str]:  # fmt: skip
    """The h3 command line, in the goldens' order (tests/golden/record_h3.py); `images` are every (flag, path) input:
    frames, image, video and audio references, in that order."""
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


def part(v: dict) -> float:
    """The seconds a media value reads: out - in (studio fills both for a trim field)."""
    return v.get("out", v["duration_s"]) - v.get("in", 0)


def sound(row: dict) -> bool:
    """A video reference row reads its sound: sound keep (the item default) and the take has sound."""
    return row.get("sound", "keep") == "keep" and bool(row["video"].get("has_audio"))


def cut(node: str, v: dict, dest: Path, *, picture: bool, audio: bool) -> Path:
    """Write the media value's in..out part to `dest`: the picture as lossless H.264 (frame exact: -ss after -i),
    the sound as 16-bit PCM, either one left out when not wanted."""
    src = dest.with_name(dest.stem + "-take")
    src.write_bytes(v["data"])
    a = [media.FFMPEG or "ffmpeg", "-v", "error", "-nostdin", "-y", "-protocol_whitelist", "file", "-i", str(src),
         "-ss", f"{v.get('in', 0):.6f}", "-t", f"{part(v):.6f}"]  # fmt: skip
    a += (
        ["-map", "0:v:0", "-c:v", "libx264", "-preset", "ultrafast", "-qp", "0"]
        if picture
        else ["-vn"]
    )
    a += ["-map", "0:a:0", "-c:a", "pcm_s16le"] if audio else ["-an"]
    r = subprocess.run([*a, str(dest)], capture_output=True, text=True, check=False, timeout=600)
    src.unlink()
    if r.returncode or not dest.is_file():
        raise RunError(
            f"{node}: cutting reference take {v['take_id']} failed: {media.last_line(r.stderr)}"
        )
    return dest


def generate(ctx, params: dict, inputs: dict) -> dict:
    size, d = inputs["canvas"], inputs["duration"]
    refs = [r["image"] for r in params.get("refs") or []]
    videos, audios = params.get("videos") or [], [r["audio"] for r in params.get("audios") or []]
    first, last = inputs.get("first"), inputs.get("last")
    w, h = size["width"], size["height"]
    facts = [[part(r["video"]), sound(r)] for r in videos]
    if why := generate_error(ctx.node, len(refs), first is not None, last is not None, w, h, d["frames"], facts,
                             [part(a) for a in audios]):  # fmt: skip
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
        # audios before videos: h3 numbers <Audio n> across every sound input in argv order, a kept video soundtrack
        # included, so this keeps audio row n as <Audio n> (h3_multimodal.c)
        for i, a in enumerate(audios):
            images.append(
                ("--ref-audio", cut(ctx.node, a, tmp / f"audio-{i}.wav", picture=False, audio=True))
            )
        for i, (row, (_, keep)) in enumerate(zip(videos, facts, strict=True)):
            path = cut(ctx.node, row["video"], tmp / f"video-{i}.mkv", picture=True, audio=keep)
            images.append(("--ref-video" if keep else "--ref-silent-video", path))
        out = tmp / "out.mp4"
        cmd = argv(inputs["model"].folder().resolve(), inputs["prompt"], out, width=w, height=h, frames=d["frames"],
                   steps=params["steps"], seed=inputs["seed"], speed=params["speed"], images=images)  # fmt: skip
        ctx.progress(1, 1, "starting h3")  # a phase without steps: 1 of 1
        child.run(ctx, cmd, cwd=H3C, name="h3", phases=PHASES, idle_s=IDLE_S)
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
