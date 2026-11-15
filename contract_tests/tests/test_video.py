"""Video takes (API 1.11) on the fake runner's `echo-video` workflow (a still MP4 of the prompt, silent AAC stereo,
and a poster): workflow `media` and `?media=`, the frame grid, the run's phases and output, the take's `video`, the
MP4 through its signed link with Range (206, 416), the poster, the gallery's `kind=video`, a video take refused
where an image goes (reference rows, element and look images, "Use in…"), a batch of video rows, a pipeline echo →
gate → echo-video (the picked image as the video's reference), isolation. Skipped when the fake runner has no
`echo-video` (before M4)."""

import io
import struct
from urllib.parse import parse_qs, urlsplit

import pytest
from helpers import (
    QUICK,
    STUDIO,
    all_takes,
    new_session,
    ok,
    ref,
    run_events,
    start_run,
    take_of_run,
    wait_run,
)
from PIL import Image
from test_batches import BATCHES, finish, fresh, lines, rows_of, wait_batch
from test_pipelines import create, echo_step, inputs_node, parked, pick
from test_pipelines import submit as submit_pipeline

WF = "echo-video"
W, H, FRAMES, FPS = 256, 128, 17, 8
FAST = {"video.steps": 2, "video.delay_ms": 50}
VIDEO = FAST | {
    "prompt.text": "a red fox on video",
    "canvas.width": W,
    "canvas.height": H,
    "duration.frames": FRAMES,
    "duration.fps": FPS,
}
LIB = f"{STUDIO}/library"


@pytest.fixture(scope="module", autouse=True)
def echo_video(api) -> dict:
    """The live echo-video workflow; skips the module when the fake runner does not publish it."""
    ids = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if WF not in ids:
        pytest.skip("the fake runner has no echo-video workflow (before API 1.11)")
    return ok(api.get(f"{STUDIO}/workflows/{WF}"))


@pytest.fixture(scope="module")
def video_done(api, user_a) -> dict:
    """User A's finished echo-video run: {"session", "run", "events", "take", "owner_id"}."""
    session = new_session(api, "Contract video")
    run = start_run(api, session["id"], VIDEO, WF)
    events = run_events(api, session["id"], run["id"])
    take = take_of_run(api, run["id"])
    return {"session": session, "run": run, "events": events, "take": take,
            "owner_id": user_a["user"]["id"]}  # fmt: skip


def boxes(data: bytes) -> dict[bytes, tuple[int, int]]:
    """The top-level boxes of an MP4: type → (offset, size)."""
    out, i = {}, 0
    while i + 8 <= len(data):
        size, kind = struct.unpack(">I4s", data[i : i + 8])
        if size == 1:
            (size,) = struct.unpack(">Q", data[i + 8 : i + 16])
        elif size == 0:
            size = len(data) - i
        assert size >= 8, f"box {kind!r} at {i}: size {size}"
        out[kind] = (i, size)
        i += size
    assert i == len(data), f"the boxes end at {i}, the file at {len(data)}"
    return out


def png_size(client, url: str) -> tuple[int, int]:
    r = client.get(url)
    assert r.status_code == 200 and r.headers["content-type"] == "image/png", r.headers
    img = Image.open(io.BytesIO(r.content))
    assert img.format == "PNG"
    return img.size


# ---------------------------------------------------------------- workflows


def test_workflows_have_media_and_filter(api, echo_video):
    listed = {w["id"]: w for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    assert all(w["media"] in ("image", "video") for w in listed.values())
    assert (listed["echo"]["media"], listed[WF]["media"], echo_video["media"]) == (
        "image",
        "video",
        "video",
    )
    for media in ("image", "video"):
        got = ok(api.get(f"{STUDIO}/workflows", params={"media": media}))["workflows"]
        assert {w["id"] for w in got} == {i for i, w in listed.items() if w["media"] == media}
    assert api.get(f"{STUDIO}/workflows", params={"media": "audio"}).status_code == 422


def test_schemas_of_echo_video(echo_video):
    schemas = echo_video["node_schemas"]
    nodes = {n: schemas[x["kind"]] for n, x in echo_video["nodes"].items()}
    frames = {p["name"]: p for p in nodes["duration"]["params"]}["frames"]
    assert (frames["type"], frames["fps"], frames["min"], frames["step"]) == ("frames", "fps", 9, 8)
    assert [o["type"] for o in nodes["canvas"]["outputs"]] == ["Canvas"]
    assert [o["type"] for o in nodes["video"]["outputs"]] == ["Video"]
    assert [i["type"] for i in nodes["output"]["inputs"]] == ["Media"]


def test_frames_off_the_grid_is_422(api):
    sid = new_session(api)["id"]
    for frames in (18, 1, 129):
        r = api.post(
            f"{STUDIO}/sessions/{sid}/runs",
            json={"workflow_id": WF, "inputs": VIDEO | {"duration.frames": frames}},
        )
        assert r.status_code == 422, f"{frames}: {r.status_code} {r.text}"
        assert "duration.frames" in r.json()["detail"], r.text
    detail = api.post(
        f"{STUDIO}/sessions/{sid}/runs",
        json={"workflow_id": WF, "inputs": VIDEO | {"duration.frames": 18}},
    ).json()["detail"]
    assert "duration.frames: 18 is not on the frame grid 9 + 8k up to" in detail, detail
    assert ok(api.get(f"{STUDIO}/sessions/{sid}/runs"))["runs"] == []


# ---------------------------------------------------------------- the run and its take


def test_video_run_and_output(video_done):
    events, run_id, owner = video_done["events"], video_done["run"]["id"], video_done["owner_id"]
    assert [e["data"]["status"] for e in events if e["type"] == "status"] == [
        "queued",
        "running",
        "succeeded",
    ]
    progress = [e["data"] for e in events if e["type"] == "progress"]
    assert [(p["node"], p["step"], p["of"], p.get("phase")) for p in progress] == [
        ("video", 1, 2, "rendering"),
        ("video", 2, 2, "rendering"),
        ("video", 1, 1, "writing mp4"),
    ]
    (out,) = [e["data"] for e in events if e["type"] == "output"]
    assert (out["index"], out["kind"], out["key"]) == (
        0,
        "video",
        f"u/{owner}/runs/{run_id}/1/0.mp4",
    )
    assert (out["width"], out["height"]) == (W, H)
    v = out["video"]
    assert (v["frames"], v["fps"], v["has_audio"]) == (FRAMES, FPS, True)
    assert v["duration_s"] == pytest.approx(FRAMES / FPS)


def test_video_take(video_done):
    take = video_done["take"]
    assert (take["kind"], take["content_type"], take["width"], take["height"]) == (
        "video",
        "video/mp4",
        W,
        H,
    )
    v = take["video"]
    assert (v["frames"], v["fps"], v["has_audio"]) == (FRAMES, FPS, True)
    assert v["duration_s"] == pytest.approx(FRAMES / FPS) and v["poster_url"]
    assert take["bytes"] > 0 and take["made_with"]["workflow_id"] == WF
    inputs = take["made_with"]["inputs"]
    assert (inputs["duration.frames"], inputs["duration.fps"], inputs["canvas.width"]) == (
        FRAMES,
        FPS,
        W,
    )


def test_mp4_through_its_signed_link(anon, video_done):
    take = video_done["take"]
    r = anon.get(take["url"])
    assert r.status_code == 200, r.text
    assert (r.headers["content-type"], r.headers["accept-ranges"]) == ("video/mp4", "bytes")
    assert (r.headers["content-security-policy"], r.headers["x-content-type-options"]) == (
        "sandbox",
        "nosniff",
    )
    data = r.content
    assert int(r.headers["content-length"]) == len(data) == take["bytes"] and r.headers["etag"]
    top = boxes(data)
    assert data[4:8] == b"ftyp" and b"moov" in top and b"mdat" in top, list(top)
    assert top[b"moov"][0] < top[b"mdat"][0]  # +faststart: playable while it streams
    moov = data[top[b"moov"][0] : sum(top[b"moov"])]
    assert moov.count(b"trak") == 2 and b"avc1" in moov and b"mp4a" in moov

    size = len(data)
    for header, start, end in (
        ("bytes=0-99", 0, 99),
        (f"bytes={size - 10}-", size - 10, size - 1),
        ("bytes=-10", size - 10, size - 1),
        (f"bytes=100-{size + 1000}", 100, size - 1),  # clamped to the end
    ):
        part = anon.get(take["url"], headers={"Range": header})
        assert part.status_code == 206, f"{header}: {part.status_code} {part.text}"
        assert part.headers["content-range"] == f"bytes {start}-{end}/{size}", header
        assert (part.headers["accept-ranges"], part.headers["content-type"]) == (
            "bytes",
            "video/mp4",
        )
        assert part.content == data[start : end + 1], header
    past = anon.get(take["url"], headers={"Range": f"bytes={size}-"})
    assert past.status_code == 416 and past.headers["content-range"] == f"bytes */{size}"
    multi = anon.get(take["url"], headers={"Range": "bytes=0-1,4-5"})  # answered as if absent
    assert multi.status_code == 200 and multi.content == data


def test_poster(anon, video_done):
    take = video_done["take"]
    assert png_size(anon, take["video"]["poster_url"]) == (W, H)
    assert png_size(anon, take["thumb_url"]) == (W, H)  # in 1.11 the same object


def test_gallery_filters_by_kind(api, video_done):
    vid = video_done["take"]["id"]
    videos = all_takes(api, kind="video", limit=200)
    assert vid in {t["id"] for t in videos} and {t["kind"] for t in videos} == {"video"}
    assert all(t["video"]["poster_url"] for t in videos)
    assert vid not in {t["id"] for t in all_takes(api, kind="image", limit=200)}


# ---------------------------------------------------------------- where a video take goes in


def test_a_video_take_is_not_an_image(api, video_done):
    vid, sid = video_done["take"]["id"], video_done["session"]["id"]
    r = api.post(
        f"{STUDIO}/sessions/{sid}/runs",
        json={"workflow_id": "echo", "inputs": QUICK | {"echo.refs": [ref(vid)]}},
    )
    assert r.status_code == 422 and f"echo.refs[1].image: take {vid} is not an image" in r.text
    r = api.post(f"{STUDIO}/takes/{vid}/use", json={"workflow_id": "echo"})
    assert r.status_code == 422 and f"take {vid} is not an image" in r.json()["detail"], r.text
    element = {"kind": "element", "group": "prop", "name": "Clip", "handle": "clip-vid",
               "text": "", "images": [{"take_id": vid}]}  # fmt: skip
    look = {
        "kind": "look",
        "group": "film",
        "name": "Clip",
        "text": "t",
        "images": [{"take_id": vid}],
    }
    for body in (element, look):
        r = api.post(LIB, json=body)
        assert r.status_code == 422, f"{body['kind']}: {r.status_code} {r.text}"
        assert r.json()["detail"] == f"images[1]: take {vid} is not an image"


# ---------------------------------------------------------------- batches and pipelines


def test_batch_of_video_rows(api, anon):
    prompts = fresh(["a red fox", "a blue heron", "a green frog"])
    body = {"workflow_id": WF, "rows": lines(prompts), "inputs": FAST}
    batch = ok(api.post(BATCHES, json=body), 202)
    try:
        done = wait_batch(api, batch["id"])
    finally:
        finish(api, batch["id"])
    assert (done["status"], done["counts"]["succeeded"]) == ("done", 3)
    for r, prompt in zip(rows_of(api, batch["id"]), prompts, strict=True):
        assert r["status"] == "succeeded" and len(r["take_ids"]) == 1, r
        t = ok(api.get(f"{STUDIO}/takes/{r['take_ids'][0]}"))
        assert (t["kind"], t["batch_id"], t["made_with"]["prompt"]) == (
            "video",
            batch["id"],
            prompt,
        )
        assert t["video"]["frames"] == 25 and t["video"]["fps"] == 24  # the template's defaults
        assert png_size(anon, r["thumb_url"]) == (t["width"], t["height"])  # the poster


def test_pipeline_image_gate_video(api):
    """inputs → still (echo) → review (keep 1) → clip (echo-video, refs = the pick) → out."""
    doc = {
        "title": "Contract image to video",
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": echo_step({"prompt.text": "$prompt"}),
            "review": {"kind": "gate", "title": "Pick one", "takes": "still", "keep": 1},
            "clip": {
                "kind": "step",
                "workflow": WF,
                "inputs": FAST,
                "bind": {"prompt.text": "$prompt", "video.refs": "review.picked"},
            },
            "out": {"kind": "output", "from": "clip.takes", "collection": "Contract video"},
        },
        "edges": [["inputs", "still"], ["still", "review"], ["review", "clip"], ["clip", "out"]],
    }
    p = create(api, doc)
    assert p["problems"] == [], p["problems"]
    bid = submit_pipeline(api, p["id"], fresh(["a red fox", "a grey wolf"]))["id"]
    try:
        rows = parked(api, bid, [1, 2])
        cand = {r["row"]: r["parked"]["candidates"][0] for r in rows}
        for n in (1, 2):
            assert ok(api.get(f"{STUDIO}/takes/{cand[n]}"))["kind"] == "image"
            pick(api, bid, n, [cand[n]])
        done = wait_batch(api, bid)
        assert (done["status"], done["counts"]["succeeded"]) == ("done", 2)
        for r in rows_of(api, bid):
            (tid,) = r["take_ids"]
            t = ok(api.get(f"{STUDIO}/takes/{tid}"))
            assert (t["kind"], t["parents"]) == ("video", [cand[r["row"]]])
            assert t["made_with"]["pipeline"]["node"] == "clip"
            assert t["made_with"]["inputs"]["video.refs"] == [ref(cand[r["row"]])]
    finally:
        finish(api, bid)


# ---------------------------------------------------------------- isolation


def test_b_gets_404_for_a_video(api, api_b, anon, video_done):
    take = video_done["take"]
    tid = take["id"]
    for method, path, body in (
        ("GET", f"{STUDIO}/takes/{tid}", None),
        ("PATCH", f"{STUDIO}/takes/{tid}", {"starred": True}),
        ("DELETE", f"{STUDIO}/takes/{tid}", None),
        ("POST", f"{STUDIO}/takes/{tid}/reuse", None),
        ("POST", f"{STUDIO}/takes/{tid}/use", {"workflow_id": "echo"}),
    ):
        r = api_b.request(method, path, json=body)
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    assert tid not in {t["id"] for t in all_takes(api_b, kind="video", limit=200)}
    b_sid = new_session(api_b, "Contract B video")["id"]
    r = api_b.post(
        f"{STUDIO}/sessions/{b_sid}/runs",
        json={"workflow_id": WF, "inputs": FAST | {"video.refs": [ref(tid)]}},
    )
    assert r.status_code == 422 and f"take {tid} not found" in r.text, r.text  # not "not an image"
    # files: the signed link is the only way in (403, never 404); a signature fits its own key only
    for url in (take["url"], take["video"]["poster_url"]):
        link = urlsplit(url)
        q = {k: v[0] for k, v in parse_qs(link.query).items()}
        assert anon.get(link.path).status_code == 403
        assert api_b.get(link.path).status_code == 403
        tampered = q | {"sig": q["sig"][:-1] + ("0" if q["sig"][-1] != "0" else "1")}
        assert anon.get(link.path, params=tampered).status_code == 403
    mp4 = urlsplit(take["url"])
    q = {k: v[0] for k, v in parse_qs(mp4.query).items()}
    poster_path = urlsplit(take["video"]["poster_url"]).path
    assert anon.get(poster_path, params=q).status_code == 403  # the MP4's signature
    assert ok(api.get(f"{STUDIO}/takes/{tid}")) == take  # nothing changed for A


def test_a_failing_video_run_has_no_output(api):
    sid = new_session(api)["id"]
    run = start_run(api, sid, VIDEO | {"video.fail": True}, WF)
    done = wait_run(api, run["id"])
    assert (done["status"], done["outputs"]) == ("failed", []) and done["error"]
