"""Stage functions of the echo pack (runtime fake): see model.yaml."""

import textwrap
import time

from PIL import Image, ImageDraw, ImageFont

from core.graph import RunError

SIZE = 512


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
