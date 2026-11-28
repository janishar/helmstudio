"""Image inputs: the `image` / `rows` param checks, reading resolved Image values from the store (owner prefix,
size, decode, EXIF), the cache key following (key, etag), and the echo node's refs end to end."""

import io
import json

import pytest
from conftest import SCHEMAS, FakeJS, FakeMsg, FakeQwen, FakeStore, command, consumer, qwen_command
from PIL import Image

from core import graph, images
from core.graph import Cache, Ctx, RunError
from core.residency import Residency
from messaging import schemas

OWNER = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
TAKE = "9b2f4c6e-1a3d-4e5f-8a7b-0c1d2e3f4a51"


def png(size=(40, 20), color="red", fmt="PNG", **save) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", size, color).save(buf, fmt, **save)
    return buf.getvalue()


def ref(data: bytes, n: int = 1, owner: str = OWNER, etag: str = "e1") -> dict:
    """A resolved Image value (defs image) for `data`, stored at a key of its own."""
    take = f"{TAKE[:-1]}{n}"
    return {"take_id": take, "key": f"u/{owner}/uploads/{take}.png", "content_type": "image/png",
            "width": 40, "height": 20, "bytes": len(data), "etag": etag}  # fmt: skip


def store_of(*pairs) -> FakeStore:
    s = FakeStore()
    for value, data in pairs:
        s.objects[value["key"]] = (data, value["content_type"])
    return s


# --- param checks


ROWS = {
    "name": "refs",
    "type": "rows",
    "default": [],
    "min": 0,
    "max": 2,
    "item": {"image": "Image"},
}


@pytest.mark.parametrize(
    ("value", "why"),
    [
        ([], None),
        ([{"image": ref(b"x")}], None),
        ("no", "must be a list"),
        ([{"image": ref(b"x")}] * 3, "must have 0 to 2 items"),
        ([{"image": {"take_id": TAKE}}], "item 1.image: an image is required"),
        ([{}], "item 1.image: an image is required"),
        ([{"image": ref(b"x"), "weight": 1}], "item 1: fields must be among ['image']"),
    ],
)
def test_rows_param(value, why):
    assert graph._check_param(ROWS, value) == why


def test_image_param_is_null_or_resolved():
    spec = {"name": "image", "type": "image", "default": None}
    assert graph._check_param(spec, None) is None
    assert graph._check_param(spec, ref(b"x")) is None
    assert graph._check_param(spec, {"take_id": TAKE}) == "must be an image"


def test_image_values_are_found_in_image_and_rows_params():
    specs = [ROWS, {"name": "image", "type": "image"}, {"name": "n", "type": "int"}]
    a, b = ref(b"a", 1), ref(b"b", 2)
    params = {"refs": [{"image": a}, {"image": b}], "image": a, "n": 3}
    assert images.values(specs, params) == [a, b, a]
    assert images.walk(specs, params, lambda v: v["take_id"][-1]) == {
        "refs": [{"image": "1"}, {"image": "2"}], "image": "1", "n": 3,
    }  # fmt: skip
    assert images.values(specs, {"refs": [], "image": None, "n": 3}) == []


# --- reading


async def test_read_decodes_the_stored_image():
    data = png()
    store = store_of((v := ref(data), data))
    img = await images.read(v, OWNER, store.get)
    assert img.size == (40, 20) and img.getpixel((0, 0)) == (255, 0, 0)


async def test_another_owners_key_is_never_fetched():
    store = store_of((v := ref(png(), owner="0" * 8 + OWNER[8:]), png()))
    with pytest.raises(RunError, match=f"input image {v['take_id']} is not the run owner's"):
        await images.read(v, OWNER, store.get)
    assert store.reads == []


async def test_a_foreign_image_key_dead_letters_the_command():
    cmd = command(refs=[{"image": ref(png(), owner="0" * 8 + OWNER[8:])}])
    assert any("not under the owner's prefix" in e for e in schemas.message_errors(cmd))


@pytest.mark.parametrize(
    ("stored", "why"),
    [
        (None, "is gone"),  # missing
        (png()[:-1], "is gone"),  # another size than `bytes`
        (png() + b"\0", "is gone"),
        (b"x" * len(png()), "is not a readable image"),
        (png(fmt="GIF"), "is not a readable image"),
    ],
)
async def test_read_fails_the_run_readably(stored, why):
    v = ref(png())
    store = FakeStore() if stored is None else store_of((v, stored))
    if why != "is gone":
        v["bytes"] = len(stored)
    with pytest.raises(RunError, match=f"input image {v['take_id']} {why}"):
        await images.read(v, OWNER, store.get)


async def test_read_refuses_more_than_the_cap_before_fetching(monkeypatch):
    monkeypatch.setattr(images, "MAX_BYTES", 10)
    store = store_of((v := ref(png()), png()))
    with pytest.raises(RunError, match="is too large"):
        await images.read(v, OWNER, store.get)
    assert store.reads == []


def test_exif_orientation_is_applied():
    exif = Image.Exif()
    exif[0x0112] = 6  # rotate 90° clockwise to view
    img = images.decode(png((40, 20), fmt="JPEG", exif=exif), TAKE)
    assert img.size == (20, 40) and img.mode == "RGB"


def test_exif_orientation_is_read_only_from_jpeg():  # as studio reads it at upload complete
    exif = Image.Exif()
    exif[0x0112] = 6
    assert images.decode(png((40, 20), exif=exif), TAKE).size == (40, 20)


def test_multi_picture_jpeg_decodes_its_first_frame():
    buf = io.BytesIO()
    first, second = Image.new("RGB", (40, 20), "red"), Image.new("RGB", (8, 8), "blue")
    first.save(buf, "MPO", save_all=True, append_images=[second])
    img = images.decode(buf.getvalue(), TAKE)
    assert img.size == (40, 20) and img.getpixel((0, 0))[0] > 200


def test_too_many_pixels_fail_before_decoding(monkeypatch):
    monkeypatch.setattr(images, "MAX_PIXELS", 799)
    with pytest.raises(RunError, match="is larger than"):
        images.decode(png((40, 20)), TAKE)


async def test_a_replaced_object_is_gone():  # re-PUT after studio recorded its etag
    data = png()
    store = store_of((v := ref(data), data))
    store.etags[v["key"]] = "e-new"
    with pytest.raises(RunError, match="is gone"):
        await images.read(v, OWNER, store.get)


@pytest.mark.parametrize(("w", "h"), [(40, 20), (4032, 3024), (3024, 4032), (1, 1), (1000, 1000), (16384, 3)])
def test_auto_size_is_the_pipelines_calculate_dimensions(w, h):
    pipeline = pytest.importorskip("diffusers.pipelines.qwenimage21.pipeline_qwenimage21")
    assert images.auto_size(w, h) == pipeline.calculate_dimensions(1024 * 1024, w / h)[:2]


def test_decoded_image_mode_is_kept():
    buf = io.BytesIO()
    Image.new("RGBA", (8, 8), (1, 2, 3, 4)).save(buf, "PNG")
    assert images.decode(buf.getvalue(), TAKE).mode == "RGBA"  # the pack's noise seed hashes the mode


# --- the graph: images reach the node as PIL, the cache follows (owner, key, etag)


async def run_edit(q: FakeQwen, cache: Cache, store: FakeStore, refs: list[dict]) -> list[str]:
    nodes = qwen_command(4)["data"]["workflow"]["nodes"]
    nodes["seed"]["params"]["seed"] = 1
    nodes["encode"]["params"]["refs"] = [{"image": v} for v in refs]
    assert graph.validate(nodes, q.schemas) == []

    async def progress(*a):
        pass

    async def output(index, data, **meta):
        pass

    c = Ctx(progress=progress, output=output, check=lambda: None, owner=OWNER, residency=Residency(100))
    c.fetch = store.get
    await graph.run(nodes, q.impls, q.schemas, c, cache)
    q.seen = list(q.calls)
    return q.ran()


async def test_encode_gets_pil_images_and_reruns_only_when_a_reference_changes(tmp_path, monkeypatch):
    q, cache = FakeQwen(tmp_path, monkeypatch), Cache()
    a, b = png(color="red"), png(color="blue")
    va, vb = ref(a, 1), ref(b, 2)
    store = store_of((va, a), (vb, b))
    assert await run_edit(q, cache, store, [va, vb]) == ["encode", "sample", "decode"]
    assert store.reads == [va["key"], vb["key"]]
    assert await run_edit(q, cache, store, [va, vb]) == []  # unchanged: a cache hit, nothing read
    assert store.reads == [va["key"], vb["key"]]
    b2 = png(color="green")
    vb2 = ref(b2, 2, etag="e2")  # same key, new content (the etag tells)
    store.objects[vb2["key"]] = (b2, "image/png")
    assert await run_edit(q, cache, store, [va, vb2]) == ["encode", "sample", "decode"]
    assert await run_edit(q, cache, store, [vb2, va]) == [
        "encode",
        "sample",
        "decode",
    ]  # order counts


async def test_the_stage_sees_pil_images_in_order(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    a, b = png(color="red"), png((30, 30), color="blue")
    va, vb = ref(a, 1), ref(b, 2)
    await run_edit(q, Cache(), store_of((va, a), (vb, b)), [va, vb])
    encode = next(p for name, p in q.seen if name == "encode")
    assert [r["image"].size for r in encode["refs"]] == [(40, 20), (30, 30)]


async def test_auto_canvas_with_references_is_sized_from_the_last_one(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    nodes = qwen_command(4)["data"]["workflow"]["nodes"]
    nodes["encode"]["params"]["refs"][-1]["image"] |= {"width": 2000, "height": 1000}
    for mode, side, mp in [("auto", 4096, 1.06), ("auto", 256, 1.06), ("fixed", 256, 0.066)]:
        nodes["canvas"]["params"] |= {"mode": mode, "width": side, "height": side}
        assert graph._canvas_mp(nodes, q.schemas, "sample") == pytest.approx(mp, abs=0.01)
    nodes["encode"]["params"]["refs"] = []
    nodes["canvas"]["params"]["mode"] = "auto"
    assert graph._canvas_mp(nodes, q.schemas, "decode") == pytest.approx(256 * 256 / 1e6)


async def test_peak_formulas_count_upstream_images(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    nodes = qwen_command(4)["data"]["workflow"]["nodes"]
    assert [graph._images(nodes, q.schemas, n) for n in ("prompt", "encode", "sample", "output")] == [
        0, 2, 2, 2,
    ]  # fmt: skip


async def test_image_node_outputs_its_image_or_fails_when_empty():
    from core.nodes import image

    c = Ctx(progress=None, output=None, check=lambda: None)
    c.node = "image"
    assert image(c, {"image": "pil"}, {}) == {"image": "pil"}
    with pytest.raises(RunError, match=r"image\.image: an image is required"):
        image(c, {"image": None}, {})


# --- the fake runner: echo reads its refs before the first step


async def test_echo_reads_its_refs_and_succeeds():
    data = png()
    js, v = FakeJS(), ref(data)
    c = consumer(js)
    c.store = store_of((v, data))
    await c.handle(FakeMsg(command(steps=1, refs=[{"image": v}]), js))
    assert js.events()[-1]["data"]["status"] == "succeeded"
    assert c.store.reads == [v["key"]]


async def test_echo_fails_when_a_ref_is_gone_before_any_step():
    js, v = FakeJS(), ref(png())
    c = consumer(js)
    await c.handle(FakeMsg(command(steps=3, refs=[{"image": v}]), js))
    evts = js.events()
    assert [e["type"] for e in evts] == [
        "evt.run.status",
        "evt.run.status",
    ]  # running, failed: no progress
    assert evts[-1]["data"]["error"] == f"input image {v['take_id']} is gone"


def test_echo_schema_has_the_rows_param():
    refs = {p["name"]: p for p in SCHEMAS["echo/echo"]["params"]}["refs"]
    assert refs["type"] == "rows" and refs["item"] == {"image": "Image"} and refs["max"] == 10
    assert json.dumps(refs["mention"]) == '"<image{n}>"'


# ---- API 1.48 thumbnails (contracts/README.md, Thumbnails)


def noisy(size, mode="RGB") -> Image.Image:
    """A picture a WebP cannot squeeze to nothing (random pixels compress badly as PNG too)."""
    import os

    return Image.frombytes(mode, size, os.urandom(size[0] * size[1] * len(mode)))


@pytest.mark.parametrize(
    "size, want",
    [((2048, 1152), (910, 512)), ((1152, 2048), (512, 910)), ((1024, 1024), (512, 512)),
     ((6000, 1000), (1024, 171)), ((513, 2000), (263, 1024)), ((512, 4000), (131, 1024)), ((864, 480), (864, 480)),
     ((480, 864), (480, 864)), ((361, 400), (361, 400)), ((640, 360), None), ((300, 200), None)],
)  # fmt: skip
def test_thumb_size_short_side_512_long_side_at_most_1024_never_upscaled(size, want):
    # owner, 2026-11-23: a thumbnail once the short side is over 360 (was 512)
    assert images.thumb_size(*size) == want


def test_thumb_is_a_webp_at_that_size_with_its_alpha():
    data, w, h = images.thumb(noisy((1200, 800), "RGBA"))
    img = Image.open(io.BytesIO(data))
    assert (img.format, img.size, (w, h), img.mode) == ("WEBP", (768, 512), (768, 512), "RGBA")
    data, w, h = images.thumb(noisy((1200, 800)))
    assert Image.open(io.BytesIO(data)).mode == "RGB"


def test_no_thumb_when_it_saves_nothing():
    assert images.thumb(noisy((360, 900))) is None  # short side at most 360
    assert images.thumb(noisy((1200, 800)), original=100) is None  # not smaller than the original


def test_thumb_of_a_file_rotates_a_jpeg_and_drafts_it():
    buf = io.BytesIO()
    exif = Image.Exif()
    exif[0x0112] = 6  # turned: upright it is 1000 x 2000
    noisy((2000, 1000)).save(buf, "JPEG", exif=exif)
    data, w, h = images.thumb_of(buf.getvalue())
    assert (w, h) == (512, 1024) and Image.open(io.BytesIO(data)).size == (512, 1024)
    buf = io.BytesIO()
    noisy((2048, 1024)).save(buf, "JPEG")  # drafted to 1024 x 512: still the full thumbnail size
    assert images.thumb_of(buf.getvalue())[1:] == (1024, 512)


def test_thumb_of_refuses_a_broken_or_huge_file_and_skips_a_small_one():
    with pytest.raises(ValueError):
        images.thumb_of(b"not an image")
    with pytest.raises(ValueError, match="MP"):
        images.thumb_of(png((8000, 7000)))
    assert images.thumb_of(png((400, 300))) is None


def test_a_864_x_480_still_gets_a_webp_of_its_own_size():
    buf = io.BytesIO()
    Image.new("RGB", (864, 480), (40, 90, 160)).save(buf, "PNG", compress_level=0)
    data, w, h = images.thumb_of(buf.getvalue())
    assert (w, h) == (864, 480) and Image.open(io.BytesIO(data)).format == "WEBP" and len(data) < len(buf.getvalue())


async def test_an_image_output_writes_its_thumb_before_the_png_and_reports_it():
    from conftest import IMPLS
    from test_consumer import PREFIX, kinds

    from core.pack import Node

    pic = noisy((1024, 768))

    def make(ctx, params, inputs):
        return {"image": pic}

    schema = {"kind": "t/pic", "title": "Pic", "family": "t", "params": [], "inputs": [],
              "outputs": [{"name": "image", "type": "Image"}]}  # fmt: skip
    cmd = command()
    cmd["data"]["workflow"]["nodes"] = {"pic": {"kind": "t/pic", "params": {}},
                                        "output": {"kind": "output", "params": {}, "in": {"media": "pic.image"}}}  # fmt: skip
    js = FakeJS()
    c = consumer(js, impls=IMPLS | {"t/pic": Node(make)}, node_schemas=SCHEMAS | {"t/pic": schema})
    await c.handle(FakeMsg(cmd, js))
    assert kinds(js) == ["running", "output", "succeeded"]
    assert list(c.store.objects) == [PREFIX + "0.thumb.webp", PREFIX + "0.png"]
    webp, ct = c.store.objects[PREFIX + "0.thumb.webp"]
    out = js.events()[1]["data"]
    assert ct == "image/webp" and schemas.message_errors(js.events()[1]) == []
    assert out["thumb"] == {
        "key": PREFIX + "0.thumb.webp",
        "width": 683,
        "height": 512,
        "bytes": len(webp),
    }
