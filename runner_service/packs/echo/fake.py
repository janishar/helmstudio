"""Stage functions of the echo pack (runtime fake): see model.yaml."""

import textwrap
import time

from PIL import Image, ImageDraw, ImageFont

from core.graph import RunError, auto_frames
from packs.echo import mp4

SIZE = 512


def draw(text: str, width: int = SIZE, height: int = SIZE) -> Image.Image:
    """`text` in white on a width x height gradient (36 px type at 512, scaled with the shorter side)."""
    ramp = Image.linear_gradient("L").resize((width, height))
    image = Image.merge(
        "RGB", (ramp, ramp.transpose(Image.Transpose.ROTATE_90).resize((width, height)),
                Image.new("L", (width, height), 150)),
    )  # fmt: skip
    lines = textwrap.fill(textwrap.shorten(text, 200, placeholder=" …") or "(empty)", 22)
    ImageDraw.Draw(image).multiline_text(
        (width / 2, height / 2),
        lines,
        font=ImageFont.load_default(size=max(8, round(36 * min(width, height) / SIZE))),
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


def canvas(ctx, params: dict, inputs: dict) -> dict:
    """width x height floored to the 64 px grid (as LTX does; the take reports the real size)."""
    return {"canvas": {"width": params["width"] // 64 * 64, "height": params["height"] // 64 * 64}}


PREDICTED_S = 2.0  # the fake DurationHead: every prompt is 2 s long


def duration(ctx, params: dict, inputs: dict) -> dict:
    """frames, or with "auto" the fake prediction (PREDICTED_S) on the grid; core reports it from the clip's frames
    (Ctx.output, resolved.params)."""
    frames, fps = params["frames"], params["fps"]
    if frames == "auto":
        spec = ctx.impl.pack.spec["stages"]["duration"]["params"]["frames"]
        frames = auto_frames(PREDICTED_S, fps, spec)
    return {"duration": {"frames": frames, "fps": fps}}


def keyframes(ctx, params: dict, inputs: dict) -> dict:
    """The text with each image's pixel frame min(round(s·fps), F − 1), size and strength: `<text> [keyframes f0
    40x30 x1, ...]`; past the clip or a frame used twice fails the run as studio's run rules word it."""
    d, seen, used = inputs["duration"], {}, []
    for i, row in enumerate(params["images"], 1):
        frame, where = round(row.get("seconds", 0) * d["fps"]), f"{ctx.node}.images[{i}].seconds"
        if frame > d["frames"]:
            raise RunError(
                f"{where}: frame {frame} is past the clip's last frame {d['frames'] - 1}"
            )
        frame = min(frame, d["frames"] - 1)
        if frame in seen:
            raise RunError(f"{where}: frame {frame} is already used by row {seen[frame]}")
        seen[frame] = i
        image = row["image"]
        used.append(f"f{frame} {image.width}x{image.height} x{row.get('strength', 1):g}")
    return {"text": f"{inputs['text']} [keyframes {', '.join(used)}]"}


def video(ctx, params: dict, inputs: dict) -> dict:
    """As echo, then a still clip of the drawn text (canvas size, duration frames at fps, silent stereo audio).
    A Video value is the encoded clip: {mp4 (bytes), poster (frame 0, a PIL image), frames, fps, has_audio}."""
    return _render(ctx, params, inputs, fail=params["fail"])


def sample(ctx, params: dict, inputs: dict) -> dict:
    """echo/video with stage-bound steps (studio refuses other values; the text names the stage)."""
    text = f"{inputs['text']} [{params['role']} {params['steps']} steps]"
    return _render(ctx, params, inputs | {"text": text}, fail=False)


def _render(ctx, params: dict, inputs: dict, fail: bool) -> dict:
    steps = params["steps"]
    for step in range(1, steps + 1):
        ctx.check()
        time.sleep(params["delay_ms"] / 1000)
        ctx.progress(step, steps, "rendering")
    if fail:
        raise RunError("echo: failed on purpose (fail=true)")
    size, d = inputs["canvas"], inputs["duration"]
    poster = draw(inputs["text"], size["width"], size["height"])
    clip = mp4.mp4(poster, d["frames"], d["fps"])
    ctx.progress(1, 1, "writing mp4")
    return {"video": {"mp4": clip, "poster": poster, "frames": d["frames"], "fps": d["fps"],
                      "has_audio": True}}  # fmt: skip


def lora_stack(ctx, params: dict, inputs: dict) -> dict:
    """The text with the LoRAs it received named: `<text> [LoRA <name> x<strength>, ...]`; no rows: the text as it is
    (an empty stack is the identity). The fake runner applies nothing."""
    rows = params["rows"]
    if not rows:
        return {"text": inputs["text"]}
    used = ", ".join(f"{r['lora']['name']} x{r['strength']:g}" for r in rows)
    return {"text": f"{inputs['text']} [LoRA {used}]"}


globals()["lora-stack"] = lora_stack  # the stage is named lora-stack (kind echo/lora-stack)
