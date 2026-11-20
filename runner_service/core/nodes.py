"""The core nodes (schemas in core/nodes.yaml): `prompt` (Text), `seed` (Seed: an int, already resolved by
graph.resolve), `canvas` (Canvas: {"width", "height", "mode"}), `image` (Image: its param, already fetched and
decoded by core.images), `output` (stores an Image, a Video or an Audio as a run output) and `answer` (a text workflow's
sink: its text becomes the run's answer, sent on the terminal status)."""

import io
import wave

from PIL import Image

from core.graph import RunError


def prompt(ctx, params: dict, inputs: dict) -> dict:
    return {"text": params["text"]}


def seed(ctx, params: dict, inputs: dict) -> dict:
    return {"seed": params["seed"]}


def canvas(ctx, params: dict, inputs: dict) -> dict:
    return {
        "canvas": {"width": params["width"], "height": params["height"], "mode": params["mode"]}
    }


def image(ctx, params: dict, inputs: dict) -> dict:
    if params["image"] is None:
        raise RunError(f"{ctx.node}.image: an image is required")
    return {"image": params["image"]}


def _png(image: Image.Image) -> bytes:
    buf = io.BytesIO()
    image.save(buf, "PNG")
    return buf.getvalue()


def _wav(ctx, audio: dict) -> tuple[bytes, dict]:
    """An Audio value ({wav: PCM 16-bit mono samples, or a RIFF WAVE file of them, sample_rate, duration_s}) as a
    RIFF WAVE file and its facts {duration_s = samples / sample_rate, sample_rate} (contracts/README.md, Voices)."""
    pcm, rate = audio["wav"], audio["sample_rate"]
    if pcm[:4] == b"RIFF":
        with wave.open(io.BytesIO(pcm)) as w:
            if (w.getnchannels(), w.getsampwidth()) != (1, 2):
                raise RunError(f"{ctx.node}: audio must be PCM 16-bit mono")
            rate, pcm = w.getframerate(), w.readframes(w.getnframes())
    if isinstance(rate, bool) or not isinstance(rate, int) or not 8000 <= rate <= 96000:
        raise RunError(f"{ctx.node}: audio at {rate!r} Hz is outside 8-96 kHz, up to 600 s")
    duration = len(pcm) // 2 / rate
    if not 0 < duration <= 600:
        raise RunError(
            f"{ctx.node}: audio of {duration:.2f} s at {rate} Hz is outside 8-96 kHz, up to 600 s"
        )
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm[: len(pcm) // 2 * 2])
    return buf.getvalue(), {"duration_s": duration, "sample_rate": rate}


def output(ctx, params: dict, inputs: dict) -> dict:
    """Store the input as an output of the run: an Image as a PNG; a Video value ({mp4: bytes, poster: PIL image,
    frames, fps, has_audio[, last: PIL image, stream: defs videoStream, source_map: defs sourceMap]}) as its MP4
    with the poster (frame 0) and the last frame as PNGs beside it; an Audio value ({wav, sample_rate,
    duration_s}) as a WAV (PCM 16-bit, mono, its own rate)."""
    media = inputs["media"]
    if isinstance(media, Image.Image):
        ctx.output(_png(media), ext="png", kind="image", width=media.width, height=media.height)
        return {}
    if "wav" in media:
        data, audio = _wav(ctx, media)
        ctx.output(data, ext="wav", kind="audio", audio=audio)
        return {}
    poster: Image.Image = media["poster"]
    video = {"duration_s": media["frames"] / media["fps"], "fps": media["fps"], "frames": media["frames"],
             "has_audio": media["has_audio"]} | ({"stream": media["stream"]} if media.get("stream") else {})  # fmt: skip
    last = media.get("last")
    ctx.output(media["mp4"], ext="mp4", kind="video", width=poster.width, height=poster.height, video=video,
               poster=_png(poster), last=_png(last) if last is not None else None,
               source_map=media.get("source_map"))  # fmt: skip
    return {}


def answer(ctx, params: dict, inputs: dict) -> dict:
    """The run's answer (defs answer): an ask stage's record (core.ask.Answer), or a plain text wired here as one
    try."""
    text = inputs["text"]
    ctx.answer = getattr(text, "record", None) or {
        "tries": [{"text": text[:32768], "error": None}],
        "seen": [],
        "tokens_in": 0,
        "tokens_out": 0,
        "seconds": 0,
    }
    return {}
