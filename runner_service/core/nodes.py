"""The core nodes, in every runner: `prompt` (a Text source) and `output` (stores an Image as a run output)."""

import asyncio
import io

from PIL import Image

from core.graph import Ctx


async def prompt(ctx: Ctx, params: dict, inputs: dict) -> dict:
    """The workflow's text."""
    return {"text": params["text"]}


def _png(image: Image.Image) -> bytes:
    buf = io.BytesIO()
    image.save(buf, "PNG")
    return buf.getvalue()


async def output(ctx: Ctx, params: dict, inputs: dict) -> dict:
    """Store the input image as a PNG output of the run."""
    image: Image.Image = inputs["media"]
    data = await asyncio.to_thread(_png, image)
    await ctx.output(data, ext="png", kind="image", width=image.width, height=image.height)
    return {}


NODES = {"prompt": prompt, "output": output}
