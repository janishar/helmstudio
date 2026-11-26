"""Stage functions of the minimax_h3 pack (runtime native): MiniMax H3 video with sound, run end to end by the pinned
h3 binary (runner_service/vendor/h3c, `make h3`) as a child process (core.child), with cwd = its folder (it opens
h3_shaders.metal by relative path), so every path it gets is absolute. Modes follow the inputs, as the h3 CLI: refs →
Ref2VA (`--ref-image` <Picture n>, `--ref-video` / `--ref-silent-video` <Video n>, `--ref-audio` <Audio n>, numbered
per kind in that order), a first and/or last frame → FL2VA, neither → text to video. API 1.37 (plan 20 24.3): the
speed params go to h3 as they are (`--layers`, `--reuse`, `--core-reuse` at reuse 1, `--token-reduction`, Render as
`--render-width/--render-height`, render_size); `--use-int8-row-fc2` always (M5). API 1.41 (plan 20 24.11): `lora:
turbo` hands h3 the Turbo add-on's file the run's partition asks for (model.yaml generate.lora `addons` /
`addons_with`: Turbo LoRA on FL2VA, Turbo Ref LoRA with any reference) as H3_LORA, with every weight copied
(H3_ZERO_COPY_WEIGHTS=0: merged pages of mapped shards stay resident); `none` sends H3_LORA empty, so a stray one in
the runner's environment never merges. Base fp8 gives SGLang the same file (sgl.argv --lora-path).
Image inputs are written as PNGs into a per-run temp folder, deleted after with the MP4; each video or audio
reference (API 1.22) is cut there to its in..out with ffmpeg (a video: lossless H.264, its sound as PCM when kept;
an audio, or a video's sound: a WAV; API 1.37 a replaced sound: the soundtrack's part muxed in as the video's PCM
track). The Video value is built as
the ltx pack's: {mp4, poster, last, frames, fps, has_audio, stream} (core.media probe, frames as PNG). No torch.
The base-fp8 variant (NVIDIA hosts) takes the same inputs to SGLang instead of h3: sgl.py."""

import math
import subprocess
import tempfile
from pathlib import Path

import yaml
from PIL import Image

import config
from core import child, media
from core.graph import RunError
from core.pack import value_addons

from . import sgl

H3C = Path(__file__).resolve().parents[2] / "vendor/h3c"
BINARY = H3C / "h3"
MAX_PIXELS = 768 * 1344  # 1,032,192 (contracts/check.py H3_MAX_PIXELS)
FPS = 24
RENDER = {"Full": 1.0, "75%": 0.75, "62.5%": 0.625}  # generate.render (API 1.37)
# h3's stderr phases → the run's progress (phase, phase id of model.yaml `generate.phases`; unknown ones pass as they
# are); the video VAE decode prints none, the MP4 write folds into decode_video, the transformer load into encode
PHASES = {
    "tokenizer": ("encoding", "encode"),
    "text encoder": ("encoding", "encode"),
    "refine text": ("encoding", "encode"),
    "Qwen vision": ("encoding images", "encode"),
    "video VAE encoder": ("encoding images", "encode"),
    "audio VAE encoder": ("encoding images", "encode"),
    "precompute AdaLN": ("loading transformer", "encode"),
    "load transformer core": ("loading transformer", "encode"),
    "denoise enqueue": ("sampling", "sample"),
    "denoise": ("sampling", "sample"),
    "audio VAE": ("decoding audio", "decode_audio"),
    "video VAE load": ("decoding video", "decode_video"),
    "FFmpeg": ("writing mp4", "decode_video"),
}
# no progress line for this long fails the run: the longest measured silence is the video VAE decode (104 s at
# 864 x 480 x 124, ~560 s at 1344 x 768 x 243); a denoise step at the largest canvas x 362 frames is ~12 min (est.)
# ponytail: flat; scale with mp x frames if a real run ever comes near it
IDLE_S = 1800
# generate's speed params (API 1.37) in generate_error's order, with the schema defaults (model.yaml)
DEFAULTS = (("layers", 50), ("reuse", 1), ("token_reduction", False), ("render", "Full"))
# h3.c's own Ref2VA limits (contracts/check.py H3_*): references in all, sound inputs, a video's sound needs 2 s of
# clip and of its part (frames on the 5 + 17k grid), all reference sound
MAX_REFS, MAX_SOUNDS, SOUND_FRAMES, MAX_SOUND_S = 12, 3, 56, 15.0
# generate's lora param (API 1.41): which add-on each value asks for, by partition (model.yaml `addons`, `addons_with`)
LORA = yaml.safe_load((Path(__file__).parent / "model.yaml").read_text())["stages"]["generate"][
    "params"
]["lora"]


def render_size(width: int, height: int, render: str) -> tuple[int, int] | None:
    """The size h3 renders at for Render (contracts/check.py h3_render_size): the canvas at Full; else, with g =
    gcd(w / 32, h / 32), k = g x scale rounded half up and capped at g - 1, (w k / g, h k / g): exactly the canvas's
    shape on the 32 px grid, as h3 needs. None when there is no such smaller size (g = 1, or k = 0)."""
    if render == "Full":
        return width, height
    g = math.gcd(width // 32, height // 32)
    k = min(
        math.floor(g * RENDER[render] + 0.5), g - 1
    )  # g x 0.75 and g x 0.625 are exact binary fractions
    return (width * k // g, height * k // g) if k > 0 else None


def generate_error(node: str, refs: int, first: bool, last: bool, width: int, height: int, frames: int = 124,
                   videos: list | None = None, audios: list | None = None,
                   speed: list | None = None) -> str | None:  # fmt: skip
    """contracts/check.py h3_generate_error (fixtures/video/minimax-h3.json): the run's error text, or None. refs =
    image references; videos = [seconds read, sound, soundtrack seconds or null] per video reference (sound: true
    when kept and the take has sound, false when not read, "replace" for Sound: replace); audios = seconds read per
    audio reference; speed = [layers, reuse, token reduction, render] on Base (null on Base fp8: SGLang ignores
    them)."""
    videos, audios = (
        [(v[0], v[1], v[2] if len(v) > 2 else None) for v in videos or []],
        audios or [],
    )
    sounds = [s for _, s, _ in videos if s]
    if (refs or videos or audios) and (first or last):
        return f"{node}: references cannot go with a first or last frame"
    for i, (_, s, t) in enumerate(videos, 1):  # API 1.37: a replaced sound
        if s == "replace" and t is None:
            return f"{node}.videos[{i}].soundtrack: Replace needs a soundtrack"
        if s != "replace" and t is not None:
            return f"{node}.videos[{i}].soundtrack: a soundtrack is read only with Sound: Replace"
    if audios and not (refs or videos):
        return f"{node}: audio references need an image or video reference"
    if (n := refs + len(videos) + len(audios)) > MAX_REFS:
        return f"{node}: at most {MAX_REFS} references in all ({n})"
    if (n := len(audios) + len(sounds)) > MAX_SOUNDS:
        return f"{node}: at most {MAX_SOUNDS} sound references (audio references and videos with sound; {n})"
    if sounds and frames < SOUND_FRAMES:
        return f"{node}: a video reference with sound needs a clip of at least {SOUND_FRAMES} frames ({frames})"
    for i, (x, s, _) in enumerate(videos, 1):
        if (
            s and (n := round(x * FPS)) < SOUND_FRAMES
        ):  # h3 keeps 5 + 17k frames, reads the sound that long
            return (
                f"{node}: a video reference with sound needs a part of at least {SOUND_FRAMES} frames at {FPS} fps "
                f"(video {i} reads {n})"
            )
    # a replaced sound is cut to the video part (and h3 to the clip's frames), so it reads the shortest of the three
    heard = sum(min(x, frames / FPS, *([t] if s == "replace" else [])) for x, s, t in videos if s)
    if (t := sum(audios) + heard) > MAX_SOUND_S + 1e-9:
        return f"{node}: the sound references last {t:.1f} s in all; at most {MAX_SOUND_S:.1f} s"
    if any(not 256 <= x <= 1344 or x % 32 for x in (width, height)):
        return f"{node}: the canvas is {width} × {height}; sides must be multiples of 32 from 256 to 1344"
    if width * height > MAX_PIXELS:
        return f"{node}: the canvas is {width} × {height}; at most {MAX_PIXELS} pixels (768 × 1344)"
    if speed:  # API 1.37, Base only
        layers, reuse, reduce, render = speed
        size = render_size(width, height, render)
        if size is None:
            return f"{node}: the canvas {width} × {height} has no smaller size of the same shape; use Full"
        if reduce and layers <= 40 and reuse == 3:  # h3.c README: colour ringing and ghosted limbs
            return (f"{node}: token reduction with 40 layers or fewer and reuse 3 gives colour ringing; "
                    "use reuse 2 or more layers")  # fmt: skip
        if reduce and size == (256, 256):  # h3.c README: keep token reduction off at this size
            return f"{node}: token reduction does not run when h3 renders 256 × 256; switch it off or render larger"
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


def argv(model_dir: Path, prompt: str, out: Path, *, width: int, height: int, frames: int, steps: int, seed: int,
         layers: int = 50, reuse: int = 1, core_reuse: int = 1, token_reduction: bool = False,
         render: tuple[int, int] | None = None, images: list[tuple[str, Path]]) -> list[str]:  # fmt: skip
    """The h3 command line, in the goldens' order; `images` are every (flag, path) input: frames, image, video and
    audio references, in that order. `--core-reuse` only at reuse 1 (h3 refuses it with reuse) and above 1; render =
    h3's internal size (render_size), passed when not the canvas."""
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
    a += ["--seed", str(seed), "--layers", str(layers), "--reuse", str(reuse)]
    if reuse == 1 and core_reuse > 1:
        a += ["--core-reuse", str(core_reuse)]
    if token_reduction:
        a += ["--token-reduction"]
    if render and render != (width, height):
        a += ["--render-width", str(render[0]), "--render-height", str(render[1])]
    a += ["--use-int8-row-fc2"]
    for flag, path in images:
        a += [flag, str(path)]
    return a


def lora_file(node: str, model, params: dict, inputs: dict) -> Path | None:
    """API 1.41: the add-on file `generate.lora`'s value asks for on this run (model.yaml `addons`, replaced by
    `addons_with` while a named rows param holds a row or input is wired: the partition's), None for none. Missing on
    this host: RunError (studio's run check refuses it first)."""
    wired = {k for k, v in params.items() if isinstance(v, list) and v} | {
        k for k, v in inputs.items() if v is not None
    }
    names = value_addons(LORA, params.get("lora", LORA["default"]), wired)
    if not names:
        return None
    (c,) = names  # one file per value and partition (h3 merges one LoRA)
    spec = model.pack.component(c)
    a = spec["addon"]
    # an add-on scoped to some variants (model.yaml `variants`; none today) is refused on the others
    if model.variant not in spec.get("variants", [model.variant]):
        title = model.pack.spec["variants"][model.variant]["title"]
        raise RunError(f"{node}: {a.get('short', a['title'])} does not run on {title}")
    if not (files := model.files(c)):
        raise RunError(f"{node}: {a.get('short', a['title'])} not downloaded on host {config.HOST}")
    return files[0].resolve()


def lora_env(path: Path | None) -> dict[str, str]:
    """h3's environment for the run's LoRA (h3_lora.c): the file with every weight copied and strength 1 (owner,
    plan 20 24.12 points 4 and 5), else H3_LORA empty."""
    if path is None:
        return {"H3_LORA": ""}
    return {"H3_LORA": str(path), "H3_LORA_STRENGTH": "1", "H3_ZERO_COPY_WEIGHTS": "0"}


def part(v: dict) -> float:
    """The seconds a media value reads: out - in (studio fills both for a trim field)."""
    return v.get("out", v["duration_s"]) - v.get("in", 0)


def sound(row: dict) -> bool | str:
    """What a video reference row reads of sound: true for sound keep (the item default) when the take has sound,
    "replace" for sound replace (API 1.37: its soundtrack), else false."""
    if (s := row.get("sound", "keep")) == "replace":
        return s
    return s == "keep" and bool(row["video"].get("has_audio"))


def cut(
    node: str, v: dict, dest: Path, *, picture: bool, audio: bool, soundtrack: dict | None = None
) -> Path:
    """Write the media value's in..out part to `dest`: the picture as lossless H.264 (frame exact: -ss after -i),
    the sound as 16-bit PCM, either one left out when not wanted. API 1.37: with `soundtrack` (a media value), the
    picture is cut alone, then the soundtrack's part comes in as a second input and is mapped as its PCM track,
    the shorter of the two setting the length (-shortest)."""
    if soundtrack is not None:
        bare = cut(
            node, v, dest.with_name(dest.stem + "-picture" + dest.suffix), picture=True, audio=False
        )
        track = dest.with_name(dest.stem + "-soundtrack")
        track.write_bytes(soundtrack["data"])
        a = [media.FFMPEG or "ffmpeg", "-v", "error", "-nostdin", "-y", "-protocol_whitelist", "file", "-i", str(bare),
             "-ss", f"{soundtrack.get('in', 0):.6f}", "-t", f"{part(soundtrack):.6f}", "-i", str(track),
             "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s16le", "-shortest", str(dest)]  # fmt: skip
        r = subprocess.run(a, capture_output=True, text=True, check=False, timeout=600)
        bare.unlink()
        track.unlink()
        if r.returncode or not dest.is_file():
            raise RunError(
                f"{node}: adding soundtrack take {soundtrack['take_id']} failed: {media.last_line(r.stderr)}"
            )
        return dest
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
    facts = [
        [part(r["video"]), sound(r), part(r["soundtrack"]) if r.get("soundtrack") else None]
        for r in videos
    ]
    fp8 = inputs["model"].variant == "base-fp8"
    speed = None if fp8 else [params.get(k, d_) for k, d_ in DEFAULTS]
    if why := generate_error(ctx.node, len(refs), first is not None, last is not None, w, h, d["frames"], facts,
                             [part(a) for a in audios], speed):  # fmt: skip
        raise RunError(why)
    if not fp8 and not BINARY.is_file():
        raise RunError(f"{ctx.node}: the h3 binary is not built (make h3 in runner_service)")
    lora = lora_file(ctx.node, inputs["model"], params, inputs)
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
        for i, (row, (_, keep, _)) in enumerate(zip(videos, facts, strict=True)):
            track = row.get("soundtrack") if keep == "replace" else None
            path = cut(ctx.node, row["video"], tmp / f"video-{i}.mkv", picture=True, audio=keep is True,
                       soundtrack=track)  # fmt: skip
            images.append(("--ref-video" if keep else "--ref-silent-video", path))
        out = tmp / "out.mp4"
        if fp8:
            by = lambda *flags: [p for f, p in images if f in flags]
            try:
                body = sgl.request(inputs["prompt"], inputs["seed"], params["steps"], size, d["frames"],
                                   (by("--first-frame") or [None])[0], (by("--last-frame") or [None])[0],
                                   by("--ref-image"), by("--ref-audio"), by("--ref-video", "--ref-silent-video"),
                                   quality=params.get("quality", "lossless"),
                                   flow_shift=params.get("flow_shift", sgl.FLOW_SHIFT),
                                   audio_flow_shift=params.get("audio_flow_shift", sgl.AUDIO_FLOW_SHIFT))  # fmt: skip
            except ValueError as e:
                raise RunError(f"{ctx.node}: {e}") from None
            sgl.generate(ctx, inputs["model"], body, out, tmp, lora=lora)
            return {"video": video_value(out, tmp)}
        layers, reuse, reduce, render = speed
        size = render_size(w, h, render)
        cmd = argv(inputs["model"].folder().resolve(), inputs["prompt"], out, width=w, height=h, frames=d["frames"],
                   steps=params["steps"], seed=inputs["seed"], layers=layers, reuse=reuse,
                   core_reuse=params.get("core_reuse", 1), token_reduction=reduce, render=size,
                   images=images)  # fmt: skip
        # a phase without steps: 1 of 1. At a smaller Render every phase text names the size (the Create stage tag
        # shows the latest one: "sampling · renders 576 × 320 → 864 × 480")
        tag = "" if size == (w, h) else f"renders {size[0]} × {size[1]} → {w} × {h}"
        phases = {k: (f"{t} · {tag}" if tag else t, i) for k, (t, i) in PHASES.items()}
        ctx.progress(1, 1, tag or "starting h3", "encode")
        child.run(ctx, cmd, cwd=H3C, name="h3", phases=phases, idle_s=IDLE_S, env=lora_env(lora))
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
