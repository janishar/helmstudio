"""Image inputs (contracts/README.md, Image inputs): a resolved Image value in cmd.run.execute is
{take_id, key, content_type, width, height, bytes, etag}. Before a node runs, the core reads each one from the
object store (only under the run owner's prefix, at most MAX_BYTES, exactly `bytes` long, only while its ETag is
still `etag`), decodes it with Pillow (at most MAX_PIXELS), applies a JPEG's EXIF orientation (as studio does when it
records the upload's size) and hands the node a PIL image. The bytes are dropped after decoding.

The node's cache key hashes its params, so it covers (owner, key, etag): a changed image re-runs the node, an
unchanged one is a cache hit and is not read again.

A `Media` field of a rows item (an ask node's `media`, API 1.14) takes an image or a video take in the same form; it
is read with the same checks and handed over as {"take_id", "image": PIL image} or, for content_type video/mp4,
{"take_id", "mp4": bytes} (the ask stage takes its keyframes).

A `media` param (API 1.19, README Media inputs: a video or audio take, defs media) is read with the same checks (at
most MEDIA_MAX_BYTES) and handed over as its resolved value plus `data` (the file's bytes, undecoded: the pack
decodes it). Its cache key covers (owner, key, etag, in, out), all in the value."""

import asyncio
import io
import math

from PIL import Image, ImageOps

from core import graph  # graph imports this module: RunError is looked up at call time

MAX_BYTES = (
    64 * 2**20
)  # ponytail: one cap for every input image; per-pack caps if a pack ever needs them
# a media take is held in memory while its node runs; ponytail: a temp file if takes ever get larger
MEDIA_MAX_BYTES = 1 << 30
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


TAKES = ("Image", "Media")  # rows item ref kinds that hold a resolved take


def walk(specs: list[dict], params: dict, fn, kinds=TAKES) -> dict:
    """`params` with fn(value) applied to every take value: an `image` param that is set, and each field of a `rows`
    item whose ref kind is in `kinds`. `specs` are the node's param schemas."""
    out = dict(params)
    for spec in specs:
        name, value = spec["name"], params.get(spec["name"])
        if spec["type"] == "image" and value is not None and "Image" in kinds:
            out[name] = fn(value)
        elif spec["type"] == "rows" and value:
            fields = [f for f, t in spec["item"].items() if t in kinds]
            out[name] = [row | {f: fn(row[f]) for f in fields if row.get(f)} for row in value]
    return out


def video(value: dict) -> bool:
    return value.get("content_type") == "video/mp4"


def values(specs: list[dict], params: dict) -> list[dict]:
    """The take values (Image and Media) of `params`, in order."""
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


async def read(value: dict, owner: str, fetch, media: bool = False) -> Image.Image | bytes:
    """Fetch one resolved Image value and decode it (a video take or a `media` value: its checked bytes,
    undecoded). `fetch(key, limit, etag)` → bytes (at most limit + 1), or None when the object is missing or its
    ETag is no longer `etag` (re-PUT after studio checked it)."""
    take_id, key, size = value["take_id"], value["key"], value["bytes"]
    what, cap = ("take", MEDIA_MAX_BYTES) if media else ("image", MAX_BYTES)
    if not key.startswith(
        f"u/{owner}/"
    ):  # also a dlq'd command (messaging.schemas.foreign_keys): never read
        raise graph.RunError(f"input {what} {take_id} is not the run owner's")
    if size > cap:
        raise graph.RunError(f"input {what} {take_id} is too large ({size} bytes, at most {cap})")
    data = await fetch(key, size, value["etag"])
    if data is None or len(data) != size:
        raise graph.RunError(f"input {what} {take_id} is gone")
    if media or video(value):
        return data
    return await asyncio.to_thread(decode, data, take_id)


def media(specs: list[dict], params: dict) -> list[str]:
    """The names of the `media` params of `params` that hold a take, and of the rows params with a `media` item
    field (API 1.22, README Media inputs: rows of media) that hold items."""
    return [s["name"] for s in specs if (s["type"] == "media" and isinstance(params.get(s["name"]), dict))
            or (s["type"] == "rows" and params.get(s["name"]) and media_fields(s))
            or (s["type"] == "voice" and isinstance((params.get(s["name"]) or {}).get("clip"), dict))
            or (s["type"] == "rows" and params.get(s["name"]) and graph.voice_fields(s))]  # fmt: skip


def media_fields(spec: dict) -> list[str]:
    """A rows param's item fields that are `media` params (API 1.22)."""
    return [f for f, t in spec["item"].items() if isinstance(t, dict) and t["type"] == "media"]


async def _clip(v, owner: str, fetch, seen: dict):
    """A resolved voice with its clip's bytes as `data` (read once per key in `seen`); any other voice as it is."""
    if not isinstance(v, dict) or not isinstance(v.get("clip"), dict):
        return v
    key = v["clip"]["key"]
    if key not in seen:
        seen[key] = await read(v["clip"], owner, fetch, True)
    return v | {"clip": v["clip"] | {"data": seen[key]}}


async def load(specs: list[dict], params: dict, owner: str, fetch) -> dict:
    """`params` with every Image value replaced by its PIL image, every Media value by {take_id, image | mp4}
    (each key read once), every `media` value (a param or a rows item's field) and every voice clip (a param or,
    API 1.32, a dialogue line's voice) given its `data`."""
    params = dict(params)
    for s in specs:
        if s["name"] not in media(specs, params):
            continue
        if s["type"] == "media":
            params[s["name"]] = params[s["name"]] | {
                "data": await read(params[s["name"]], owner, fetch, True)
            }
            continue
        if s["type"] == "voice":  # API 1.31: a voice's clip is read like a media input
            params[s["name"]] = await _clip(params[s["name"]], owner, fetch, {})
            continue
        rows, clips = [], {}  # API 1.32: a line's voice clip, each voice's read once
        for row in params[s["name"]]:
            rows.append(
                row
                | {
                    f: row[f] | {"data": await read(row[f], owner, fetch, True)}
                    for f in media_fields(s)
                    if row.get(f) is not None  # API 1.37: an optional media field left empty
                }
                | {f: await _clip(row[f], owner, fetch, clips) for f in graph.voice_fields(s)}
            )
        params[s["name"]] = rows
    pics = {}
    for v in values(specs, params):
        if v["key"] not in pics:
            pics[v["key"]] = await read(v, owner, fetch)
    out = walk(specs, params, lambda v: pics[v["key"]], ("Image",))
    take = lambda v: {"take_id": v["take_id"], "mp4" if video(v) else "image": pics[v["key"]]}
    return walk(specs, out, take, ("Media",))
