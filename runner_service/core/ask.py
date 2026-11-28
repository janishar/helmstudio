"""The shared part of every `<family>/ask` stage (contracts/README.md, "The llm pack and agent nodes"): thumbnails of
the media takes, the schema check with one more try in the same run, and the run's answer record (defs `answer`).
A runtime only supplies `generate(prompt, images) -> (text, tokens_in, tokens_out)`, greedy (temperature 0), seed
SEED, thinking off.

- An image take: RGBA over white, Lanczos to long edge min(THUMB, native), both sides floored to multiples of 32
  (area at least MIN_AREA), JPEG q90, PUT once to u/<owner_id>/llm/thumbs/<sha256 of the JPEG>.jpg. A video take:
  KEYFRAMES frames at floor(F * (2k + 1) / 6), each as an image (ffmpeg). At most MAX_THUMBS per run.
- With a `schema`: the outermost {...} of the answer is parsed and validated with jsonschema; a failure is asked once
  more with RETRY appended; a second failure fails the run `answer did not match the schema: <error>` (AnswerError,
  which carries the record: the failed status sends it).
- The stage's output is an `Answer`: the text, carrying its record, so a cached answer still reports it."""

import hashlib
import io
import json
import math
import subprocess
import tempfile
import time
from pathlib import Path

from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError, best_match
from PIL import Image

from core.graph import RunError
from core.media import FFMPEG, FFPROBE

THUMB, MIN_AREA, KEYFRAMES, MAX_THUMBS = 1024, 65536, 3, 24
SEED = 0  # pinned; greedy decoding makes it moot
RETRY = "Your answer was invalid: {}. Answer again with JSON only."
SCHEMA_HINT = "Answer only with JSON matching this schema: {}"
MAX_TEXT, MAX_ERROR = 32768, 2000
_put: set[str] = set()  # thumbnail keys this process already stored (content-addressed: a re-PUT is harmless)


class Answer(str):
    """An ask stage's Text output: the answer, with the run's answer record (defs `answer`) on `.record`."""

    record: dict


class AnswerError(RunError):
    """The answers did not match the schema: the run fails and its status still carries `answer`."""

    def __init__(self, message: str, answer: dict) -> None:
        super().__init__(message)
        self.answer = answer


def thumbnail(img: Image.Image) -> Image.Image:
    """The picture the model sees: over white, long edge min(THUMB, native), sides floored to 32, area >= MIN_AREA."""
    rgba = img.convert("RGBA")
    flat = Image.new("RGB", rgba.size, (255, 255, 255))
    flat.paste(rgba, mask=rgba.getchannel("A"))
    s = min(1.0, THUMB / max(flat.size))
    w, h = (max(32, int(x * s) // 32 * 32) for x in flat.size)
    if w * h < MIN_AREA:  # a tiny take: up to the processor's minimum at its own aspect, still on the grid
        f = math.sqrt(MIN_AREA / (flat.width * flat.height))
        w, h = (min(THUMB, max(32, math.ceil(x * f / 32) * 32)) for x in flat.size)
    return flat.resize((w, h), Image.Resampling.LANCZOS)


def jpeg(img: Image.Image) -> bytes:
    buf = io.BytesIO()
    img.save(buf, "JPEG", quality=90)
    return buf.getvalue()


def keyframes(mp4: bytes, take_id: str) -> list[tuple[int, Image.Image]]:
    """(frame index, RGB frame) of a clip at floor(F * (2k + 1) / 6), k < KEYFRAMES, in frame order (ffprobe counts the
    frames without decoding them; ffmpeg decodes only those)."""
    if not (FFMPEG and FFPROBE):
        raise RunError("video takes need ffmpeg and ffprobe on PATH (brew install ffmpeg)")
    with tempfile.TemporaryDirectory() as d:
        path = Path(d) / "take.mp4"
        path.write_bytes(mp4)
        probe = subprocess.run([FFPROBE, "-v", "error", "-select_streams", "v:0", "-count_packets", "-show_entries",
                                "stream=width,height,nb_read_packets", "-of", "json", str(path)],
                               capture_output=True, timeout=120, check=False)  # fmt: skip
        try:
            s = json.loads(probe.stdout)["streams"][0]
            w, h, n = int(s["width"]), int(s["height"]), int(s["nb_read_packets"])
        except (ValueError, KeyError, IndexError) as e:
            raise RunError(f"input video {take_id} is not a readable video") from e
        frames = sorted({n * (2 * k + 1) // (2 * KEYFRAMES) for k in range(KEYFRAMES)})
        select = "+".join(f"eq(n\\,{f})" for f in frames)
        dec = subprocess.run([FFMPEG, "-v", "error", "-i", str(path), "-vf", f"select='{select}'",
                              "-fps_mode", "passthrough", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1"],
                             capture_output=True, timeout=300, check=False)  # fmt: skip
    size = w * h * 3
    if dec.returncode or len(dec.stdout) != size * len(frames):
        raise RunError(f"input video {take_id} is not a readable video")
    return [(f, Image.frombytes("RGB", (w, h), dec.stdout[i * size : (i + 1) * size])) for i, f in enumerate(frames)]


def looked_at(ctx, media: list[dict]) -> tuple[list[Image.Image], list[dict]]:
    """(the thumbnails in order, the answer's `seen` list), each thumbnail stored once under the run owner. `media`
    rows hold {"take": {"take_id", "image" | "mp4"}} (core.images.load)."""
    shown: list[tuple[str, int | None, Image.Image]] = []
    for row in media:
        take = row["take"]
        if "mp4" in take:
            shown += [(take["take_id"], f, img) for f, img in keyframes(take["mp4"], take["take_id"])]
        else:
            shown.append((take["take_id"], None, take["image"]))
    if len(shown) > MAX_THUMBS:
        raise RunError(f"{ctx.node}.media: {len(shown)} thumbnails, at most {MAX_THUMBS}")
    thumbs, seen = [], []
    for take_id, frame, img in shown:
        ctx.check()
        thumb = thumbnail(img)
        data = jpeg(thumb)
        sha = hashlib.sha256(data).hexdigest()
        key = f"u/{ctx.owner}/llm/thumbs/{sha}.jpg"
        if key not in _put:
            ctx.put(key, data, "image/jpeg")
            _put.add(key)
        thumbs.append(Image.open(io.BytesIO(data)).convert("RGB"))  # exactly the stored pixels
        seen.append({"take_id": take_id, "frame": frame, "key": key, "sha256": sha,
                     "width": thumb.width, "height": thumb.height})  # fmt: skip
    return thumbs, seen


def problem(text: str, validator: Draft202012Validator | None) -> str | None:
    """Why `text` does not answer the schema (its outermost {...} parsed, then validated), or None."""
    if validator is None:
        return None
    i, j = text.find("{"), text.rfind("}")
    try:
        value = json.loads(text[i : j + 1] if 0 <= i < j else text)
    except ValueError as e:
        return str(e)
    if err := best_match(validator.iter_errors(value)):
        return err.message
    return None


def validator(ctx, schema: str) -> Draft202012Validator | None:
    """The answer's validator; None for a plain-text answer (schema ""). A bad schema fails the run before any call."""
    if not schema.strip():
        return None
    try:
        value = json.loads(schema)
        Draft202012Validator.check_schema(value)
    except (ValueError, SchemaError) as e:
        raise RunError(f"{ctx.node}.schema: not a JSON schema") from e
    return Draft202012Validator(value)


def ask(ctx, params: dict, generate, see: bool = True) -> dict:
    """Run one ask: thumbnails, then at most two tries of `generate(prompt, thumbnails)`; the stage's outputs
    {"answer": Answer}. `see=False` (the fake) reads no pixels: `seen` stays empty."""
    check = validator(ctx, params["schema"])
    thumbs, seen = looked_at(ctx, params["media"]) if see else ([], [])
    prompt = params["prompt"]
    if check is not None:
        prompt += "\n\n" + SCHEMA_HINT.format(params["schema"])
    ctx.progress(1, 1, "answering")
    tries, tokens_in, tokens_out, seconds, why = [], 0, 0, 0.0, None
    for n in range(2):
        ctx.check()
        started = time.monotonic()
        text, t_in, t_out = generate(prompt if n == 0 else f"{prompt}\n\n{RETRY.format(why)}", thumbs)
        seconds += time.monotonic() - started
        tokens_in, tokens_out = tokens_in + t_in, tokens_out + t_out
        why = problem(text, check)
        error = f"answer did not match the schema: {why}"[:MAX_ERROR] if why else None
        tries.append({"text": text[:MAX_TEXT], "error": error})
        if why is None:
            break
    record = {"tries": tries, "seen": seen, "tokens_in": tokens_in, "tokens_out": tokens_out,
              "seconds": round(seconds, 2)}  # fmt: skip
    if tries[-1]["error"]:
        raise AnswerError(tries[-1]["error"], record)
    answer = Answer(tries[-1]["text"])
    answer.record = record
    return {"answer": answer}
