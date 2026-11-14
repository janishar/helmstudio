"""The fake model family `echo`: node `echo/echo` reports one progress step per `steps` (each after `delay_ms`,
with a cancel point before each), then draws its text on a gradient, or fails when `fail` is true.

Its registry values (node schemas, the `echo` workflow, the model) are the canonical contract fixtures copied
into gen/registry by `make gen`, so the runner and the contract cannot drift."""

import asyncio
import json
import textwrap
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from core.graph import Ctx, RunError

REGISTRY = Path(__file__).resolve().parents[2] / "gen" / "registry"
SIZE = 512


def registry_value(kind: str) -> dict:
    """The canonical registry value `kind` (nodes, workflows, models), as in the contract."""
    return json.loads((REGISTRY / f"{kind}.valid.json").read_text())


def draw(text: str) -> Image.Image:
    """`text` in white on a SIZE² gradient."""
    ramp = Image.linear_gradient("L").resize((SIZE, SIZE))
    image = Image.merge("RGB", (ramp, ramp.rotate(90), Image.new("L", (SIZE, SIZE), 150)))
    lines = textwrap.fill(textwrap.shorten(text, 200, placeholder=" …") or "(empty)", 22)
    ImageDraw.Draw(image).multiline_text(
        (SIZE / 2, SIZE / 2),
        lines,
        font=ImageFont.load_default(size=36),
        fill="white",
        anchor="mm",
        align="center",
        stroke_width=2,
        stroke_fill="black",
    )
    return image


async def echo(ctx: Ctx, params: dict, inputs: dict) -> dict:
    """steps progress events, then the drawn text (or a RunError when fail is true)."""
    steps = params["steps"]
    for step in range(1, steps + 1):
        ctx.check()
        await asyncio.sleep(params["delay_ms"] / 1000)
        await ctx.progress(step, steps)
    if params["fail"]:
        raise RunError("echo: failed on purpose (fail=true)")
    return {"image": await asyncio.to_thread(draw, inputs["text"])}


NODES = {"echo/echo": echo}
