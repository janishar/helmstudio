"""Image inputs (contracts/README.md, Image inputs): a resolved Image value in cmd.run.execute is
{take_id, key, content_type, width, height, bytes, etag}. Before a node runs, the core reads each one from the
object store (only under the run owner's prefix, at most MAX_BYTES, exactly `bytes` long, only while its ETag is
still `etag`), decodes it with Pillow (at most MAX_PIXELS), applies a JPEG's EXIF orientation (as studio does when it
records the upload's size) and hands the node a PIL image. The bytes are dropped after decoding.

The node's cache key hashes its params, so it covers (owner, key, etag): a changed image re-runs the node, an
unchanged one is a cache hit and is not read again."""

import asyncio
import io
import math

from PIL import Image, ImageOps

from core import graph  # graph imports this module: RunError is looked up at call time

MAX_BYTES = (
    64 * 2**20
)  # ponytail: one cap for every input image; per-pack caps if a pack ever needs them
FORMATS = ("PNG", "JPEG", "WEBP")  # the decoders tried
# studio accepts the same files: Go reads a multi-picture JPEG (Pillow's MPO, from the JPEG decoder) as JPEG; its
# first frame is the picture
OPENED = (*FORMATS, "MPO")
# the same cap as studio's upload check; Pillow raises above it instead of only warning (the pack works at ~1 MP)
MAX_PIXELS = 50_000_000
Image.MAX_IMAGE_PIXELS = MAX_PIXELS


def auto_size(width: int, height: int) -> tuple[int, int]:
    """Canvas `auto` with references (contracts/README.md): about 1 MP at the aspect of `width` x `height`, sides
    rounded to multiples of 32 (diffusers' calculate_dimensions(1024², width / height))."""
    ratio = width / height
    w = math.sqrt(1024 * 1024 * ratio)
    return round(w / 32) * 32, round(w / ratio / 32) * 32


def walk(specs: list[dict], params: dict, fn) -> dict:
    """`params` with fn(value) applied to every Image value: an `image` param that is set, and each Image field
    of a `rows` item. `specs` are the node's param schemas."""
    out = dict(params)
    for spec in specs:
        name, value = spec["name"], params.get(spec["name"])
        if spec["type"] == "image" and value is not None:
            out[name] = fn(value)
        elif spec["type"] == "rows" and value:
            fields = [f for f, t in spec["item"].items() if t == "Image"]
            out[name] = [row | {f: fn(row[f]) for f in fields if row.get(f)} for row in value]
    return out


def values(specs: list[dict], params: dict) -> list[dict]:
    """The Image values of `params`, in order."""
    found: list[dict] = []
    walk(specs, params, found.append)
    return found


def decode(data: bytes, take_id: str) -> Image.Image:
    """The image in `data` (a JPEG upright: EXIF orientation applied), detached from `data`."""
    try:
        with Image.open(io.BytesIO(data), formats=FORMATS) as img:
            if img.format not in OPENED:
                raise ValueError(img.format)
            if (
                img.width * img.height > MAX_PIXELS
            ):  # checked on the header, before any pixel is decoded
                raise graph.RunError(
                    f"input image {take_id} is larger than {MAX_PIXELS // 10**6} MP"
                )
            img.load()  # an MPO loads its first frame
            if img.format in ("JPEG", "MPO"):
                return ImageOps.exif_transpose(
                    img
                )  # a new image either way: no reference to `data`
            return img.copy()
    except (OSError, ValueError, SyntaxError, Image.DecompressionBombError) as e:
        raise graph.RunError(f"input image {take_id} is not a readable image") from e


async def read(value: dict, owner: str, fetch) -> Image.Image:
    """Fetch one resolved Image value and decode it. `fetch(key, limit, etag)` → bytes (at most limit + 1), or None
    when the object is missing or its ETag is no longer `etag` (re-PUT after studio checked it)."""
    take_id, key, size = value["take_id"], value["key"], value["bytes"]
    if not key.startswith(
        f"u/{owner}/"
    ):  # also a dlq'd command (messaging.schemas.foreign_keys): never read
        raise graph.RunError(f"input image {take_id} is not the run owner's")
    if size > MAX_BYTES:
        raise graph.RunError(
            f"input image {take_id} is too large ({size} bytes, at most {MAX_BYTES})"
        )
    data = await fetch(key, size, value["etag"])
    if data is None or len(data) != size:
        raise graph.RunError(f"input image {take_id} is gone")
    return await asyncio.to_thread(decode, data, take_id)


async def load(specs: list[dict], params: dict, owner: str, fetch) -> dict:
    """`params` with every Image value replaced by its PIL image (each key read once)."""
    pics = {}
    for v in values(specs, params):
        if v["key"] not in pics:
            pics[v["key"]] = await read(v, owner, fetch)
    return walk(specs, params, lambda v: pics[v["key"]])
