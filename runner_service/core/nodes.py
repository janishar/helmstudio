"""The core nodes (schemas in core/nodes.yaml): `prompt` (Text), `seed` (Seed: an int, already resolved by
graph.resolve), `canvas` (Canvas: {"width", "height", "mode"}), `image` (Image: its param, already fetched and
decoded by core.images) and `output` (stores an Image or a Video as a run output)."""

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
    frames, fps, has_audio}) as its MP4 with the poster (frame 0) as a PNG beside it."""
    media = inputs["media"]
    if isinstance(media, Image.Image):
        ctx.output(_png(media), ext="png", kind="image", width=media.width, height=media.height)
        return {}
    poster: Image.Image = media["poster"]
    video = {"duration_s": media["frames"] / media["fps"], "fps": media["fps"], "frames": media["frames"],
             "has_audio": media["has_audio"]}  # fmt: skip
    ctx.output(media["mp4"], ext="mp4", kind="video", width=poster.width, height=poster.height, video=video,
               poster=_png(poster))  # fmt: skip
    return {}
