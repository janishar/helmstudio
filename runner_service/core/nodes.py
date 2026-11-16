"""The core nodes (schemas in core/nodes.yaml): `prompt` (Text), `seed` (Seed: an int, already resolved by
graph.resolve), `canvas` (Canvas: {"width", "height", "mode"}), `image` (Image: its param, already fetched and
decoded by core.images), `output` (stores an Image or a Video as a run output) and `answer` (a text workflow's
sink: its text becomes the run's answer, sent on the terminal status)."""

import io

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


def output(ctx, params: dict, inputs: dict) -> dict:
    """Store the input as an output of the run: an Image as a PNG; a Video value ({mp4: bytes, poster: PIL image,
    frames, fps, has_audio[, last: PIL image, stream: defs videoStream]}) as its MP4 with the poster (frame 0) and
    the last frame as PNGs beside it."""
    media = inputs["media"]
    if isinstance(media, Image.Image):
        ctx.output(_png(media), ext="png", kind="image", width=media.width, height=media.height)
        return {}
    poster: Image.Image = media["poster"]
    video = {"duration_s": media["frames"] / media["fps"], "fps": media["fps"], "frames": media["frames"],
             "has_audio": media["has_audio"]} | ({"stream": media["stream"]} if media.get("stream") else {})  # fmt: skip
    last = media.get("last")
    ctx.output(media["mp4"], ext="mp4", kind="video", width=poster.width, height=poster.height, video=video,
               poster=_png(poster), last=_png(last) if last is not None else None)  # fmt: skip
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
