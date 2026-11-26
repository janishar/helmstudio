"""The core nodes (schemas in core/nodes.yaml): `prompt` (Text), `seed` (Seed: an int, already resolved by
graph.resolve), `canvas` (Canvas: {"width", "height", "mode"}), `image` (Image: its param, already fetched and
decoded by core.images), `output` (stores an Image, a Video or an Audio as a run output) and `answer` (a text workflow's
sink: its text becomes the run's answer, sent on the terminal status)."""

import io
import wave

from PIL import Image

from core.graph import RunError
from core.images import thumb


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
    """An Audio value ({wav: PCM 16-bit samples, interleaved when stereo, or a RIFF WAVE file of them, sample_rate,
    duration_s[, channels: 1 | 2 (API 1.79, default 1)]}) as a RIFF WAVE file and its facts {duration_s = frames /
    sample_rate, sample_rate[, channels: 2 for stereo]} (contracts/README.md, Voices; Music and sound)."""
    pcm, rate, channels = audio["wav"], audio["sample_rate"], audio.get("channels", 1)
    if pcm[:4] == b"RIFF":
        with wave.open(io.BytesIO(pcm)) as w:
            if w.getsampwidth() != 2 or w.getnchannels() not in (1, 2):
                raise RunError(f"{ctx.node}: audio must be PCM 16-bit mono or stereo")
            rate, channels, pcm = w.getframerate(), w.getnchannels(), w.readframes(w.getnframes())
    if channels not in (1, 2):
        raise RunError(f"{ctx.node}: audio must be PCM 16-bit mono or stereo")
    if isinstance(rate, bool) or not isinstance(rate, int) or not 8000 <= rate <= 96000:
        raise RunError(f"{ctx.node}: audio at {rate!r} Hz is outside 8-96 kHz, up to 600 s")
    frame = 2 * channels
    duration = len(pcm) // frame / rate
    if not 0 < duration <= 600:
        raise RunError(
            f"{ctx.node}: audio of {duration:.2f} s at {rate} Hz is outside 8-96 kHz, up to 600 s"
        )
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(channels)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm[: len(pcm) // frame * frame])
    return buf.getvalue(), {"duration_s": duration, "sample_rate": rate} | (
        {"channels": 2} if channels == 2 else {}
    )


def output(ctx, params: dict, inputs: dict) -> dict:
    """Store the input as an output of the run: an Image as a PNG; a Video value ({mp4: bytes, poster: PIL image,
    frames, fps, has_audio[, last: PIL image, stream: defs videoStream, source_map: defs sourceMap]}) as its MP4
    with the poster (frame 0) and the last frame as PNGs beside it (an image or a poster also with its thumbnail, API
    1.48); an Audio value ({wav, sample_rate,
    duration_s[, channels, cues, sections]}) as a WAV (PCM 16-bit, mono or stereo, its own rate), a dialogue's cues and
    (API 1.79) a song's sections reported with it."""
    media = inputs["media"]
    if isinstance(media, Image.Image):  # API 1.48: with its thumbnail (none when it saves nothing)
        png = _png(media)
        ctx.output(
            png,
            ext="png",
            kind="image",
            width=media.width,
            height=media.height,
            thumb=thumb(media, len(png)),
        )
        return {}
    if "wav" in media:
        data, audio = _wav(ctx, media)
        if media.get("cues"):  # API 1.32: a dialogue's lines (README, Dialogue: cues)
            audio["cues"] = media["cues"]
        if media.get("sections"):  # API 1.79: a song's parts (README, Music and sound)
            audio["sections"] = media["sections"]
        ctx.output(data, ext="wav", kind="audio", audio=audio)
        return {}
    poster: Image.Image = media["poster"]
    video = {"duration_s": media["frames"] / media["fps"], "fps": media["fps"], "frames": media["frames"],
             "has_audio": media["has_audio"]} | ({"stream": media["stream"]} if media.get("stream") else {})  # fmt: skip
    last, png = media.get("last"), _png(poster)
    ctx.output(media["mp4"], ext="mp4", kind="video", width=poster.width, height=poster.height, video=video,
               poster=png, last=_png(last) if last is not None else None, thumb=thumb(poster, len(png)),
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
