"""Stage functions of the echo pack (runtime fake): see model.yaml."""

import array
import json
import math
import re
import tempfile
import textwrap
import time
import zlib
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from core import ask as asks
from core import media
from core.graph import RunError, auto_frames
from packs.echo import mp4

SIZE = 512


def draw(text: str, width: int = SIZE, height: int = SIZE) -> Image.Image:
    """`text` in white on a width x height gradient (36 px type at 512, scaled with the shorter side)."""
    ramp = Image.linear_gradient("L").resize((width, height))
    image = Image.merge(
        "RGB",
        (ramp, ramp.transpose(Image.Transpose.ROTATE_90).resize((width, height)), Image.new("L", (width, height), 150)),
    )
    lines = textwrap.fill(textwrap.shorten(text, 200, placeholder=" …") or "(empty)", 22)
    ImageDraw.Draw(image).multiline_text(
        (width / 2, height / 2),
        lines,
        font=ImageFont.load_default(size=max(8, round(36 * min(width, height) / SIZE))),
        fill="white",
        anchor="mm",
        align="center",
        stroke_width=2,
        stroke_fill="black",
    )
    return image


def echo(ctx, params: dict, inputs: dict) -> dict:
    """steps progress events, then the drawn text (or a RunError when fail is true)."""
    steps = params["steps"]
    for step in range(1, steps + 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        ctx.progress(step, steps)
    if params["fail"]:
        raise RunError("echo: failed on purpose (fail=true)")
    return {"image": draw(inputs["text"])}


def tiny(ctx, params: dict, inputs: dict) -> dict:
    """Loads the tiny model's transformer and vae (resident afterwards, as the after-run setting says), then echoes
    the text with the variant: `<text> [tiny]`."""
    model = inputs["model"]
    for name in ("transformer", "vae"):
        ctx.component(model, name)
    steps = params["steps"]
    for step in range(1, steps + 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        ctx.progress(step, steps)
    return {"image": draw(f"{inputs['text']} [{model.variant}]")}


def canvas(ctx, params: dict, inputs: dict) -> dict:
    """width x height floored to the 64 px grid (as LTX does; the take reports the real size)."""
    return {"canvas": {"width": params["width"] // 64 * 64, "height": params["height"] // 64 * 64}}


PREDICTED_S = 2.0  # the fake DurationHead: every prompt is 2 s long


def duration(ctx, params: dict, inputs: dict) -> dict:
    """frames, or with "auto" the fake prediction (PREDICTED_S) on the grid; core reports it from the clip's frames
    (Ctx.output, resolved.params)."""
    frames, fps = params["frames"], params["fps"]
    if frames == "auto":
        spec = ctx.impl.pack.spec["stages"]["duration"]["params"]["frames"]
        frames = auto_frames(PREDICTED_S, fps, spec)
    return {"duration": {"frames": frames, "fps": fps}}


def keyframes(ctx, params: dict, inputs: dict) -> dict:
    """The text with each image's pixel frame min(round(s·fps), F − 1), size and strength: `<text> [keyframes f0
    40x30 x1, ...]`; past the clip or a frame used twice fails the run as studio's run rules word it."""
    d, seen, used = inputs["duration"], {}, []
    for i, row in enumerate(params["images"], 1):
        frame, where = round(row.get("seconds", 0) * d["fps"]), f"{ctx.node}.images[{i}].seconds"
        if frame > d["frames"]:
            raise RunError(f"{where}: frame {frame} is past the clip's last frame {d['frames'] - 1}")
        frame = min(frame, d["frames"] - 1)
        if frame in seen:
            raise RunError(f"{where}: frame {frame} is already used by row {seen[frame]}")
        seen[frame] = i
        image = row["image"]
        used.append(f"f{frame} {image.width}x{image.height} x{row.get('strength', 1):g}")
    return {"text": f"{inputs['text']} [keyframes {', '.join(used)}]"}


def video(ctx, params: dict, inputs: dict) -> dict:
    """As echo, then a still clip of the drawn text (canvas size, duration frames at fps, silent stereo audio).
    A Video value is the encoded clip: {mp4 (bytes), poster (frame 0, a PIL image), last (the last frame), frames, fps,
    has_audio, stream (defs videoStream)}."""
    return _render(ctx, params, inputs, fail=params["fail"])


def sample(ctx, params: dict, inputs: dict) -> dict:
    """echo/video with stage-bound steps (studio refuses other values; the text names the stage)."""
    text = f"{inputs['text']} [{params['role']} {params['steps']} steps]"
    return _render(ctx, params, inputs | {"text": text}, fail=False)


def _render(ctx, params: dict, inputs: dict, fail: bool) -> dict:
    steps = params["steps"]
    for step in range(1, steps + 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        ctx.progress(step, steps, "rendering")
    if fail:
        raise RunError("echo: failed on purpose (fail=true)")
    size, d = inputs["canvas"], inputs["duration"]
    poster = draw(inputs["text"], size["width"], size["height"])
    clip = mp4.mp4(poster, d["frames"], d["fps"])
    ctx.progress(1, 1, "writing mp4")
    # a still clip: its last frame is its poster
    return {"video": {"mp4": clip, "poster": poster, "last": poster, "frames": d["frames"], "fps": d["fps"],
                      "has_audio": True, "stream": mp4.stream(*poster.size, d["fps"])}}  # fmt: skip


MEDIA_STEPS = 3  # progress steps of the media fakes


def _steps(ctx, params: dict) -> None:
    for step in range(1, MEDIA_STEPS + 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        ctx.progress(step, MEDIA_STEPS, "rendering")


def _clip(poster, frames: int, fps: int, data: bytes | None = None, **source_map) -> dict:
    """A still clip of `poster` (data: an MP4 with that sound instead of silence) reporting `source_map`."""
    return {"video": {"mp4": data or mp4.mp4(poster, frames, fps), "poster": poster, "last": poster,
                      "frames": frames, "fps": fps, "has_audio": True, "stream": mp4.stream(*poster.size, fps),
                      "source_map": source_map}}  # fmt: skip


def retake(ctx, params: dict, inputs: dict) -> dict:
    """The core read and checked the source (its bytes are there); a still clip of `<text> [retake a-b s]` at the
    source's size, frames and fps (ponytail: fps rounded to whole frames per second, as mp4.py writes them)."""
    src, span = params["source"], params["span"]
    _steps(ctx, params)
    poster = draw(
        f"{inputs['text']} [retake {span['start']:g}-{span['end']:g} s]",
        src["width"],
        src["height"],
    )
    return _clip(poster, src["frames"], round(src["fps"]), take_id=src["take_id"], offset_s=0,
                 new_in_s=span["start"], new_out_s=span["end"])  # fmt: skip


MAX_FRAMES = 321  # ltx/extend's longest clip (contracts check.py LTX_MAX_FRAMES)


def extend(ctx, params: dict, inputs: dict) -> dict:
    """ltx/extend's frames and source_map (check.py extend_map): the source floored to 8k + 1 frames plus 8N, N =
    max(1, ceil(seconds * fps / 8)); after: the new part follows the source, before: it leads and the source moves."""
    src, seconds, after = params["source"], params["seconds"], params["direction"] == "after"
    fps, n = src["fps"], (src["frames"] - 1) // 8 * 8 + 1
    k = max(1, math.ceil(seconds * fps / 8 - 1e-9))
    total, add = n + 8 * k, 8 * k / fps
    if total > MAX_FRAMES:
        raise RunError(f"{ctx.node}.seconds: the clip would be {total} frames; at most {MAX_FRAMES}")
    _steps(ctx, params)
    poster = draw(
        f"{inputs['text']} [extend {params['direction']} {seconds:g} s]",
        src["width"],
        src["height"],
    )
    m = {"offset_s": 0, "new_in_s": n / fps, "new_out_s": total / fps} if after else \
        {"offset_s": add, "new_in_s": 0, "new_out_s": add}  # fmt: skip
    return _clip(poster, total, round(fps), take_id=src["take_id"], **m)


A2V_SIZE, A2V_FPS = (256, 128), 24


def a2v(ctx, params: dict, inputs: dict) -> dict:
    """A still 256 x 128 clip at 24 fps as long as the part in..out, with that part as its sound (ffmpeg: the
    still clip's picture copied, the part encoded as AAC)."""
    src = params["source"]
    start, end = src["in"], src["out"]
    _steps(ctx, params)
    frames = max(1, round((end - start) * A2V_FPS))
    poster = draw(f"{inputs['text']} [a2v {start:g}-{end:g} s]", *A2V_SIZE)
    with tempfile.TemporaryDirectory(prefix="echo-") as tmp:
        tmp = Path(tmp)
        (tmp / "still.mp4").write_bytes(mp4.mp4(poster, frames, A2V_FPS))
        (tmp / "source").write_bytes(src["data"])
        try:
            media.ffmpeg(["-protocol_whitelist", "file", "-i", str(tmp / "still.mp4"), "-ss", f"{start}",
                          "-t", f"{end - start}", "-protocol_whitelist", "file", "-i", str(tmp / "source"),
                          "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-ar", "48000", "-ac", "2",
                          "-movflags", "+faststart", str(tmp / "out.mp4")], timeout=120)  # fmt: skip
        except media.MediaError as e:
            raise RunError(f"{ctx.node}.source: the sound could not be read ({e})") from None
        data = (tmp / "out.mp4").read_bytes()
    return _clip(poster, frames, A2V_FPS, data, take_id=src["take_id"], offset_s=-start, new_in_s=0,
                 new_out_s=end - start)  # fmt: skip


SPEECH_RATE, CHAR_SAMPLES, CHARS_PER_STEP = (
    24000,
    1600,
    15,
)  # 1/15 s per character (README, Voices: seconds)


def _tone(hz: float, phase: float) -> bytes:
    return array.array("h", (round(12000 * math.sin(phase + 2 * math.pi * hz * i / SPEECH_RATE))
                             for i in range(CHAR_SAMPLES))).tobytes()  # fmt: skip


def _voice(v: dict) -> int:
    """A voice's pitch offset: a clip voice's from the clip's etag (its bytes are read as a media input), any other's
    from its resolved value."""
    data = v["clip"]["etag"] if v.get("source") == "clip" else json.dumps(v, sort_keys=True)
    return zlib.crc32(data.encode())


def speak(ctx, params: dict, inputs: dict) -> dict:
    """PCM 16-bit mono at 24 kHz: one 1/15 s sine per character, its pitch from the character and the voice (defs
    voice, as studio resolved it), its phase from the seed; deterministic, one progress step per 15 characters."""
    text = inputs["text"]
    if not text:
        raise RunError(f"{ctx.node}: nothing to say")
    voice = _voice(params["voice"])
    phase = params["seed"] % 1000 / 1000 * 2 * math.pi
    tones: dict[str, bytes] = {}
    steps = math.ceil(len(text) / CHARS_PER_STEP)
    for step in range(1, steps + 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        for c in text[(step - 1) * CHARS_PER_STEP : step * CHARS_PER_STEP]:
            if c not in tones:
                tones[c] = _tone(110 + (ord(c) * 37 + voice) % 770, phase)
        ctx.progress(step, steps, "speaking")
    pcm = b"".join(tones[c] for c in text)
    return {"audio": {"wav": pcm, "sample_rate": SPEECH_RATE, "duration_s": len(text) / CHARS_PER_STEP}}


def dialogue(ctx, params: dict, inputs: dict) -> dict:
    """echo_dialogue (contracts check.py): per line one 1/15 s sine per character, its pitch from the character and
    the line's voice, its phase from the seed, then rnd(pause_s × 24000) samples of silence before the next line;
    one progress step per line; the cues from those sample counts."""
    lines = params["lines"]
    if not lines:
        raise RunError(f"{ctx.node}: nothing to say")
    phase, spoken = params["seed"] % 1000 / 1000 * 2 * math.pi, []
    for i, row in enumerate(lines, 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        voice, tones = _voice(row["voice"]), {}
        text = row["text"].strip()  # what is spoken, as the real packs (the cue's text too)
        for c in text:
            if c not in tones:
                tones[c] = _tone(110 + (ord(c) * 37 + voice) % 770, phase)
        spoken.append(b"".join(tones[c] for c in text))
        ctx.progress(i, len(lines), "speaking")
    pcm, cues = media.stitch(lines, spoken, SPEECH_RATE)
    return {"audio": {"wav": pcm, "sample_rate": SPEECH_RATE, "duration_s": len(pcm) / 2 / SPEECH_RATE, "cues": cues}}


MUSIC_RATE, SOUND_RATE = 48000, 44100  # ACE-Step's rate; Stable Audio 3's and MMAudio's
AUTO_S, AUTO_BPM = 10.0, 90  # the fake planner's picks (Auto)
TAG = re.compile(r"^\s*\[([A-Za-z][A-Za-z0-9 -]{0,39})\]\s*$", re.MULTILINE)


def _stereo(seconds: float, rate: int, hz: float, phase: float = 0.0) -> bytes:
    """`seconds` of a sine at `hz` on the left and 1.5 hz on the right, PCM 16-bit interleaved stereo."""
    n = round(seconds * rate)
    return array.array("h", (round(9000 * math.sin(phase + 2 * math.pi * f * i / rate))
                             for i in range(n) for f in (hz, 1.5 * hz))).tobytes()  # fmt: skip


def _pitch(text: str, seed: int) -> float:
    return 110 + (zlib.crc32(text.encode()) + seed) % 440


def _audio(pcm: bytes, rate: int, **extra) -> dict:
    return {"audio": {"wav": pcm, "sample_rate": rate, "channels": 2, "duration_s": len(pcm) / 4 / rate} | extra}


def sections(lyrics: str, seconds: float) -> list[dict]:
    """One section per `[tag]` line of the lyrics, in order, evenly spread over the song (the fake lyric timestamps;
    none without a tag)."""
    tags = [t.strip().lower() for t in TAG.findall(lyrics)][:40]
    step = seconds / len(tags) if tags else 0
    return [{"label": t, "start_s": round(i * step, 3), "end_s": round((i + 1) * step, 3)} for i, t in enumerate(tags)]


def music(ctx, params: dict, inputs: dict) -> dict:
    """echo/music: Length (Auto: AUTO_S) of two tones, the sections of the lyrics' tags (none when instrumental), and
    the plan line `<bpm> BPM · A minor · 4/4 · m:ss · en` as its report."""
    seconds, bpm = params["seconds"] or AUTO_S, params["bpm"] or AUTO_BPM
    _steps(ctx, params)
    pcm = _stereo(seconds, MUSIC_RATE, _pitch(inputs["text"], params["seed"]))
    parts = [] if params["instrumental"] else sections(params["lyrics"], seconds)
    plan = f"{bpm} BPM · A minor · 4/4 · {int(seconds) // 60}:{int(seconds) % 60:02d} · en"
    return _audio(pcm, MUSIC_RATE, **({"sections": parts} if parts else {})) | {"plan": plan}


def sound(ctx, params: dict, inputs: dict) -> dict:
    """echo/sound: Length seconds of two tones at 44.1 kHz stereo."""
    _steps(ctx, params)
    return _audio(_stereo(params["seconds"], SOUND_RATE, _pitch(inputs["text"], params["seed"])), SOUND_RATE)


def foley(ctx, params: dict, inputs: dict) -> dict:
    """echo/foley: a sound as long as the source video, and the source with that sound (core.media.mux)."""
    src = params["source"]
    _steps(ctx, params)
    pcm = _stereo(src["duration_s"], SOUND_RATE, _pitch(inputs["text"], params["seed"]))
    out = _audio(pcm, SOUND_RATE)
    poster = draw(f"{inputs['text']} [sound]", src["width"], src["height"])
    clip = _clip(poster, src["frames"], round(src["fps"]), media.mux(src["data"], media.wav(pcm, SOUND_RATE, 2)))
    clip["video"].pop("source_map")
    return out | clip


def _source(params: dict, rate: int) -> bytes:
    return media.pcm(params["source"]["data"], rate, 2)


def repaint(ctx, params: dict, inputs: dict) -> dict:
    """echo/repaint: the source at 48 kHz stereo with [span.start, span.end) replaced by a tone of the text."""
    span = params["span"]
    _steps(ctx, params)
    pcm = _source(params, MUSIC_RATE)
    a, b = (min(len(pcm), round(t * MUSIC_RATE) * 4) for t in (span["start"], span["end"]))
    tone = _stereo((b - a) / 4 / MUSIC_RATE, MUSIC_RATE, _pitch(inputs["text"], params["seed"]))
    return _audio(pcm[:a] + tone[: b - a] + pcm[b:], MUSIC_RATE)


def vary(ctx, params: dict, inputs: dict) -> dict:
    """echo/vary: the source at 44.1 kHz stereo, each sample moved toward a tone of the text by Change."""
    _steps(ctx, params)
    src = array.array("h", _source(params, SOUND_RATE))
    tone = array.array("h", _stereo(len(src) / 2 / SOUND_RATE, SOUND_RATE, _pitch(inputs["text"], params["seed"])))
    c = params["change"]
    return _audio(array.array("h", (round((1 - c) * x + c * y) for x, y in zip(src, tone, strict=False))).tobytes(),
                  SOUND_RATE)  # fmt: skip


def lengthen(ctx, params: dict, inputs: dict) -> dict:
    """echo/lengthen: the source at 48 kHz stereo with Add seconds of a tone before (start) or after (end) it."""
    _steps(ctx, params)
    pcm = _source(params, MUSIC_RATE)
    tone = _stereo(params["seconds"], MUSIC_RATE, _pitch(inputs["text"], params["seed"]))
    if len(pcm) + len(tone) > media.MAX_TAKE_S * MUSIC_RATE * 4:
        raise RunError(f"{ctx.node}.seconds: the song would last over {media.MAX_TAKE_S} s")
    return _audio(pcm + tone if params["where"] == "end" else tone + pcm, MUSIC_RATE)


def lora_stack(ctx, params: dict, inputs: dict) -> dict:
    """The text with the LoRAs it received named: `<text> [LoRA <name> x<strength>, ...]`; no rows: the text as it is
    (an empty stack is the identity). The fake runner applies nothing."""
    rows = params["rows"]
    if not rows:
        return {"text": inputs["text"]}
    used = ", ".join(f"{r['lora']['name']} x{r['strength']:g}" for r in rows)
    return {"text": f"{inputs['text']} [LoRA {used}]"}


ANSWER, RETRY = "ECHO-ANSWER:", "ECHO-RETRY:"
REASKED = "Your answer was invalid:"  # pipelines_service agents.REASK


def scripted(prompt: str, marker: str) -> str | None:
    """The first JSON value after the last `marker` (a JSON string: its text; not JSON: the rest of that line), or
    None without the marker."""
    at = prompt.rfind(marker)
    if at < 0:
        return None
    rest = prompt[at + len(marker) :].lstrip()
    try:
        value, _ = json.JSONDecoder().raw_decode(rest)
    except ValueError:
        return rest.split("\n", 1)[0]
    return value if isinstance(value, str) else json.dumps(value, separators=(",", ":"))


TURN = re.compile(r"This is turn (\d+) of")


def turn(prompt: str) -> str | None:
    """An agent turn's script: the JSON after `ECHO-TURN <n>:` for the <n> of the prompt's last `This is turn <n> of`
    line (contracts/README.md "Tool calling on the ask contract"), so a test scripts a whole tool loop turn by turn."""
    found = TURN.findall(prompt)
    return scripted(prompt, f"ECHO-TURN {found[-1]}:") if found else None


def ask(ctx, params: dict, inputs: dict) -> dict:
    """The fake ask (contracts/README.md, the fake): the scripted answer after the last ECHO-ANSWER: in the prompt,
    else `{}` with a schema and `echo` without; checked and retried as the real one (core.ask). The retry answers
    the JSON after ECHO-RETRY: when the prompt has one (a bad answer then a good one: one retry that passes).
    Reads its takes (the core's checks) but sends no thumbnails: `seen` [], tokens and seconds 0."""
    tries = []

    def generate(prompt: str, thumbs: list) -> tuple[str, int, int]:
        tries.append(prompt)
        original = params["prompt"]
        text = (
            # a retry here, or (1.32) a pipelines re-ask (a node's check failed a valid answer): the ECHO-RETRY one
            ((len(tries) > 1 or REASKED in original) and scripted(original, RETRY))
            or turn(original)
            or scripted(original, ANSWER)
        )
        if text is None:
            text = "{}" if params["schema"].strip() else "echo"
        return text, 0, 0

    return asks.ask(ctx, params, generate, see=False)  # seconds round to 0


globals()["lora-stack"] = lora_stack  # the stage is named lora-stack (kind echo/lora-stack)
