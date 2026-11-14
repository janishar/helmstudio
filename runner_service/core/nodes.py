"""The core nodes (schemas in core/nodes.yaml): `prompt` (Text), `seed` (Seed: an int, already resolved by
graph.resolve), `canvas` (Canvas: {"width", "height"}) and `output` (stores an Image as a run output)."""

import io

from PIL import Image


def prompt(ctx, params: dict, inputs: dict) -> dict:
    return {"text": params["text"]}


def seed(ctx, params: dict, inputs: dict) -> dict:
    return {"seed": params["seed"]}


def canvas(ctx, params: dict, inputs: dict) -> dict:
    return {"canvas": {"width": params["width"], "height": params["height"]}}


def output(ctx, params: dict, inputs: dict) -> dict:
    """Store the input image as a PNG output of the run."""
    image: Image.Image = inputs["media"]
    buf = io.BytesIO()
    image.save(buf, "PNG")
    ctx.output(buf.getvalue(), ext="png", kind="image", width=image.width, height=image.height)
    return {}
