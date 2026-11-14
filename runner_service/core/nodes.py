"""The core nodes (schemas in core/nodes.yaml): `prompt` (Text), `seed` (Seed: an int, already resolved by
graph.resolve), `canvas` (Canvas: {"width", "height", "mode"}), `image` (Image: its param, already fetched and
decoded by core.images) and `output` (stores an Image as a run output)."""

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


def output(ctx, params: dict, inputs: dict) -> dict:
    """Store the input image as a PNG output of the run."""
    image: Image.Image = inputs["media"]
    buf = io.BytesIO()
    image.save(buf, "PNG")
    ctx.output(buf.getvalue(), ext="png", kind="image", width=image.width, height=image.height)
    return {}
