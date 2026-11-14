"""Uploads: POST /uploads, PUT the bytes to the signed /files link, complete; the checks on each step."""

import io
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import parse_qs, urlsplit

from helpers import NO_SUCH_ID, STUDIO, all_takes, ok, png_bytes
from PIL import Image


def start(client, data: bytes, filename: str = "tiny photo.png", ct: str = "image/png") -> dict:
    return ok(
        client.post(
            f"{STUDIO}/uploads",
            json={"filename": filename, "content_type": ct, "bytes": len(data)},
        ),
        201,
    )


def split(upload_url: str) -> tuple[str, dict]:
    """The link's path and its query as a dict."""
    link = urlsplit(upload_url)
    return link.path, {k: v[0] for k, v in parse_qs(link.query).items()}


def put(anon, path: str, params: dict, data: bytes, ct: str = "image/png"):
    return anon.put(path, params=params, content=data, headers={"Content-Type": ct})


def test_upload_becomes_a_take(api, anon, user_a):
    data = png_bytes(37, 23)
    up = start(api, data)
    tid, owner = up["take_id"], user_a["user"]["id"]
    assert (up["content_type"], up["bytes"]) == ("image/png", len(data))
    path, q = split(up["upload_url"])
    assert path == f"/files/u/{owner}/uploads/{tid}.png"
    assert (q["bytes"], q["ct"]) == (str(len(data)), "image/png") and {"exp", "sig"} <= set(q)

    # pending: not in the gallery, and nothing to complete yet
    assert tid not in {t["id"] for t in all_takes(api, source="upload")}
    r = api.post(f"{STUDIO}/uploads/{tid}/complete")
    assert r.status_code == 409 and r.json()["detail"] == "upload not received"

    assert put(anon, path, q, data).status_code == 204
    assert put(anon, path, q, data).status_code == 409  # single use: never overwritten

    take = ok(api.post(f"{STUDIO}/uploads/{tid}/complete"))
    assert {
        k: take[k]
        for k in (
            "id",
            "kind",
            "source",
            "title",
            "content_type",
            "width",
            "height",
            "bytes",
            "starred",
            "session_id",
            "run_id",
            "made_with",
            "parents",
        )
    } == {
        "id": tid,
        "kind": "image",
        "source": "upload",
        "title": "tiny photo",
        "content_type": "image/png",
        "width": 37,
        "height": 23,
        "bytes": len(data),
        "starred": False,
        "session_id": None,
        "run_id": None,
        "made_with": None,
        "parents": [],
    }
    assert ok(api.post(f"{STUDIO}/uploads/{tid}/complete")) == take  # again: the same take
    assert (
        put(anon, path, q, b"x" * len(data)).status_code == 409
    )  # a checked upload is never replaced
    assert ok(api.get(f"{STUDIO}/takes/{tid}")) == {**take, "children": []}  # children only on GET
    for link in (take["url"], take["thumb_url"]):  # the original is its own thumbnail in 1.2
        r = anon.get(link)
        assert r.status_code == 200 and r.content == data
    newest = ok(api.get(f"{STUDIO}/takes", params={"source": "upload", "limit": 1}))["takes"]
    assert [t["id"] for t in newest] == [tid]
    assert tid not in {t["id"] for t in all_takes(api, source="create")}
    assert api.post(f"{STUDIO}/takes/{tid}/reuse").status_code == 409  # no settings to reuse


def test_create_rejects_bad_requests(api):
    good = {"filename": "a.png", "content_type": "image/png", "bytes": 10}
    for change in (
        {"bytes": 52428801},  # over 50 MB
        {"bytes": 0},
        {"content_type": "image/gif"},
        {"content_type": "text/plain"},
        {"filename": ""},
        {"filename": "x" * 256},
        {"extra": 1},
    ):
        r = api.post(f"{STUDIO}/uploads", json=good | change)
        assert r.status_code == 422, f"{change}: {r.status_code} {r.text}"
    for ct, ext in (("image/jpeg", "jpg"), ("image/webp", "webp")):
        up = ok(api.post(f"{STUDIO}/uploads", json=good | {"content_type": ct}), 201)
        assert split(up["upload_url"])[0].endswith(f"/{up['take_id']}.{ext}")


def test_a_text_file_is_not_an_image(api, anon):
    data = b"just some text, not a png at all\n"
    up = start(api, data, "notes.png")
    path, q = split(up["upload_url"])
    assert put(anon, path, q, data).status_code == 204  # files_service only stores bytes
    r = api.post(f"{STUDIO}/uploads/{up['take_id']}/complete")
    assert r.status_code == 422, r.text
    # discarded
    assert api.get(f"{STUDIO}/takes/{up['take_id']}").status_code == 404
    assert api.post(f"{STUDIO}/uploads/{up['take_id']}/complete").status_code == 404


def complete(api, anon, data: bytes, ct: str, filename: str = "photo.jpg"):
    up = start(api, data, filename, ct)
    path, q = split(up["upload_url"])
    assert put(anon, path, q, data, ct).status_code == 204
    return api.post(f"{STUDIO}/uploads/{up['take_id']}/complete")


def test_a_turned_jpeg_is_recorded_upright(api, anon):
    exif = Image.Exif()
    exif[0x0112] = 6  # stored 40 x 20, shown turned a quarter: 20 x 40
    buf = io.BytesIO()
    Image.new("RGB", (40, 20), "red").save(buf, "JPEG", exif=exif)
    take = ok(complete(api, anon, buf.getvalue(), "image/jpeg"))
    assert (take["width"], take["height"]) == (20, 40)


def test_more_than_50_megapixels_is_refused(api, anon):
    buf = io.BytesIO()
    Image.new("1", (8000, 7000)).save(buf, "PNG")  # 56 MP in a few KB
    r = complete(api, anon, buf.getvalue(), "image/png", "flat.png")
    assert r.status_code == 422 and "larger than 50 MP" in r.text, r.text


def test_put_link_checks(api, anon):
    data = png_bytes(8, 8)
    up = start(api, data)
    path, q = split(up["upload_url"])
    flipped = q["sig"][:-1] + ("0" if q["sig"][-1] != "0" else "1")
    for params in (
        {},
        {k: v for k, v in q.items() if k != "sig"},
        q | {"sig": flipped},  # tampered
        q | {"exp": str(int(q["exp"]) - 3600)},  # re-dated into the past
        q | {"exp": str(int(q["exp"]) + 3600)},
        q | {"bytes": str(len(data) + 1)},
        q | {"ct": "image/webp"},
    ):
        r = put(anon, path, params, data)
        assert r.status_code == 403, f"{params}: {r.status_code} {r.text}"
    sibling = path.rsplit("/", 1)[0] + f"/{NO_SUCH_ID}.png"
    assert put(anon, sibling, q, data).status_code == 403  # the signature covers the key
    assert put(anon, "/files/elsewhere/x.png", q, data).status_code == 403
    assert (
        put(anon, path, q, data[:-1]).status_code == 411
    )  # Content-Length is not the signed bytes
    assert put(anon, path, q, data, ct="image/jpeg").status_code == 415
    # nothing was stored by the refused tries: the link still works once
    assert api.post(f"{STUDIO}/uploads/{up['take_id']}/complete").status_code == 409
    assert put(anon, path, q, data).status_code == 204
    take = ok(api.post(f"{STUDIO}/uploads/{up['take_id']}/complete"))
    # a GET link of the same key never verifies as a PUT
    get_path, get_q = split(take["url"])
    assert get_path == path
    assert put(anon, path, get_q, data).status_code == 403
    assert put(anon, path, get_q | {"bytes": q["bytes"], "ct": q["ct"]}, data).status_code == 403


def test_unknown_upload_ids(api):
    for tid in (NO_SUCH_ID, "not-a-uuid"):
        assert api.post(f"{STUDIO}/uploads/{tid}/complete").status_code == 404


def test_two_puts_of_one_link_store_once(api, anon):
    """The store decides (a conditional create): of two PUTs at once exactly one stores, so what complete checks stays."""
    data = png_bytes(9, 9)
    path, q = split(start(api, data)["upload_url"])
    with ThreadPoolExecutor(2) as pool:
        codes = sorted(
            pool.map(lambda body: put(anon, path, q, body).status_code, [data, b"<" * len(data)])
        )
    assert codes == [204, 409]
