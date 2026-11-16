"""The import endpoints `helm import` calls (API 1.16, contracts/README.md "Import"): admin only, idempotent on
(owner, key), pending takes hidden until complete, lineage by key, the read-only legacy snapshot, the same checks
as an upload. Two synthetic PNGs into user A's gallery; the CLI's own run on an old-layout fixture is cli's test."""

import uuid
from datetime import datetime

import pytest
from helpers import STUDIO, all_takes, ok, png_bytes

ADMIN = f"{STUDIO}/admin/imports/takes"


def body(owner: str, key: str, data: bytes, **over) -> dict:
    return {
        "owner_id": owner,
        "key": key,
        "kind": "image",
        "filename": f"{key.rsplit(':', 1)[-1]}.png",
        "content_type": "image/png",
        "bytes": len(data),
        "width": 64,
        "height": 48,
        "created_at": "2024-03-01T10:00:00Z",
        "starred": False,
        "title": "an old fox",
        "legacy": {"studio": "qwen-image", "old_id": key, "params": {"steps": 8, "seed": 7}},
        "parents": [],
    } | over


def put(anon, url: str, data: bytes) -> None:
    r = anon.put(url, content=data, headers={"Content-Type": "image/png"})
    assert r.status_code == 204, f"PUT import: {r.status_code} {r.text}"


@pytest.fixture(scope="module")
def run_id() -> str:
    return uuid.uuid4().hex[:12]


def test_only_admins_import(api, user_a):
    data = png_bytes(64, 48)
    r = api.post(ADMIN, json=body(user_a["user"]["id"], "contract:x", data))
    assert r.status_code == 403 and r.json()["detail"] == "admin role required", r.text
    r = api.post(f"{ADMIN}/00000000-0000-4000-8000-000000000000/complete")
    assert r.status_code == 403, r.text


def test_import_two_items_with_lineage_idempotent(admin, api, api_b, anon, user_a, run_id):
    owner, data = user_a["user"]["id"], png_bytes(64, 48)
    k1, k2 = f"contract:{run_id}:1", f"contract:{run_id}:2"
    b1 = body(owner, k1, data, starred=True)
    first = ok(admin.post(ADMIN, json=b1), 201)
    assert (first["key"], first["state"], first["poster_upload_url"]) == (k1, "pending", None), (
        first
    )
    assert first["upload_url"] and first["expires_at"], first
    r = admin.post(f"{ADMIN}/{first['take_id']}/complete")
    assert r.status_code == 409 and r.json()["detail"] == "upload not received", r.text
    again = ok(admin.post(ADMIN, json=b1))  # still pending: the same take, fresh links
    assert (again["take_id"], again["state"]) == (first["take_id"], "pending") and again[
        "upload_url"
    ], again
    assert api.get(f"{STUDIO}/takes/{first['take_id']}").status_code == 404  # hidden until complete
    put(anon, again["upload_url"], data)
    t1 = ok(admin.post(f"{ADMIN}/{first['take_id']}/complete"))
    assert (t1["id"], t1["source"], t1["kind"], t1["starred"]) == (
        first["take_id"],
        "import",
        "image",
        True,
    ), t1
    assert (t1["title"], t1["width"], t1["height"], t1["bytes"]) == (
        "an old fox",
        64,
        48,
        len(data),
    ), t1
    assert (t1["made_with"], t1["parents"], t1["session_id"], t1["run_id"]) == (
        None,
        [],
        None,
        None,
    ), t1
    assert t1["legacy"]["settings"] == b1["legacy"] and t1["legacy"]["imported_at"], t1
    assert datetime.fromisoformat(t1["created_at"]) == datetime.fromisoformat(
        "2024-03-01T10:00:00+00:00"
    )
    assert (
        ok(admin.post(f"{ADMIN}/{first['take_id']}/complete"))["id"] == t1["id"]
    )  # complete again: the same

    b2 = body(
        owner, k2, data, parents=[k1], title="the fox, edited", created_at="2024-03-02T10:00:00Z"
    )
    second = ok(admin.post(ADMIN, json=b2), 201)
    put(anon, second["upload_url"], data)
    t2 = ok(admin.post(f"{ADMIN}/{second['take_id']}/complete"))
    assert t2["parents"] == [t1["id"]] and t2["starred"] is False, t2

    # a re-run adds nothing: ready keys answer 200 without links
    for b, t in ((b1, t1), (b2, t2)):
        ready = ok(admin.post(ADMIN, json=b))
        assert (ready["take_id"], ready["state"]) == (t["id"], "ready"), ready
        assert (ready["upload_url"], ready["poster_upload_url"], ready["expires_at"]) == (
            None,
            None,
            None,
        ), ready
    mine = [t for t in all_takes(api) if t["id"] in (t1["id"], t2["id"])]
    assert sorted(t["id"] for t in mine) == sorted([t1["id"], t2["id"]]), mine
    assert ok(api.get(f"{STUDIO}/takes/{t2['id']}"))["parents"] == [t1["id"]]
    children = ok(api.get(f"{STUDIO}/takes/{t1['id']}"))["children"]
    assert [c["id"] for c in children] == [t2["id"]], children
    for t in (t1, t2):  # another user's take is 404
        assert api_b.get(f"{STUDIO}/takes/{t['id']}").status_code == 404


def test_import_a_video_with_its_poster(admin, api, anon, user_a, run_id):
    """A video goes to its signed video/mp4 link (files takes that type only on an import's .mp4 key), its poster to
    its own link; complete checks the ftyp box and the poster."""
    owner, poster = user_a["user"]["id"], png_bytes(64, 48)
    mp4 = (
        b"\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2" + b"\x00" * 64
    )  # the ftyp box is what complete reads
    b = body(owner, f"contract:{run_id}:v", mp4, kind="video", filename="clip.mp4", content_type="video/mp4",
             video={"duration_s": 1.0, "fps": 24, "frames": 24, "has_audio": False,
                    "poster": {"content_type": "image/png", "bytes": len(poster), "width": 64, "height": 48}})  # fmt: skip
    t = ok(admin.post(ADMIN, json=b), 201)
    assert t["upload_url"] and t["poster_upload_url"], t
    r = anon.put(t["upload_url"], content=mp4, headers={"Content-Type": "video/mp4"})
    assert r.status_code == 204, f"PUT video: {r.status_code} {r.text}"
    put(anon, t["poster_upload_url"], poster)
    done = ok(admin.post(f"{ADMIN}/{t['take_id']}/complete"))
    assert (done["kind"], done["source"], done["video"]["fps"]) == ("video", "import", 24), done
    assert ok(api.get(f"{STUDIO}/takes/{t['take_id']}"))["video"]["poster_url"]


def test_import_checks_the_file(admin, anon, user_a, run_id):
    owner, data = user_a["user"]["id"], png_bytes(64, 48)
    k = f"contract:{run_id}:3"
    pending = ok(admin.post(ADMIN, json=body(owner, k, data, width=10, height=10)), 201)
    put(anon, pending["upload_url"], data)
    r = admin.post(f"{ADMIN}/{pending['take_id']}/complete")
    assert r.status_code == 422 and r.json()["detail"] == "width and height differ from the file", (
        r.text
    )
    # the failed take is gone: the next run of the import creates it again
    assert admin.post(f"{ADMIN}/{pending['take_id']}/complete").status_code == 404
    assert ok(admin.post(ADMIN, json=body(owner, k, data)), 201)["take_id"] != pending["take_id"]
    r = admin.post(ADMIN, json=body(owner, f"contract:{run_id}:4", data, legacy={"x": "a" * 70000}))
    assert r.status_code == 422 and r.json()["detail"] == "legacy is larger than 64 KB", r.text
    assert admin.post(ADMIN, json=body(owner, "bad key!", data)).status_code == 422
