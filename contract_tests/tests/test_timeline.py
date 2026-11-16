"""The timeline (API 1.17, M5b; contracts/README.md "Timeline" and "Timeline rules"): sequences (create with defaults
from the first clip, list, rename, defaults for new shots, delete), "Add to timeline" in order, every edit as a whole-
document PUT (reorder, trim with frame snapping, split, dissolve, gain and mute, an image hold, an extra audio track)
checked against check.py's t_apply, the normalize and edit fixtures of contracts/fixtures/timelines through the API
(their takes made live: echo-video 256x128 and 704x448 at 24 fps, a 256x128 upload), a stale revision 409, undo and
redo and the 100-revision cap, an offline clip, New shot into a gap (placeholder, fill, lineage, undo/redo never
bring back a finished placeholder), start from the previous clip's last frame (and end on the next one's first),
Replace between two alternatives, Re-render of two clips (a batch), the export plan, an export to a playable MP4
take, cancel mid-export, isolation. Export tests skip when no live env advertises `media` (ffmpeg)."""

import json
import os
import struct

import pytest
from helpers import (
    JOB_TIMEOUT,
    NO_SUCH_ID,
    STUDIO,
    all_takes,
    new_session,
    ok,
    png_bytes,
    start_run,
    take_of_run,
    upload,
    wait_for,
    wait_run,
)
from test_batches import wait_batch
from test_elements import element
from test_user_workflows import CONTRACTS, check

TL = f"{STUDIO}/timelines"
FIXTURES = CONTRACTS / "fixtures" / "timelines"
EXPORT_TIMEOUT = float(os.environ.get("EXPORT_TIMEOUT", "240"))
VIDEO = "echo-video"
I2V = "echo-image-to-video"
FAST = {"video.steps": 2, "video.delay_ms": 50}


def video_inputs(
    w=256, h=128, frames=49, fps=24, text="a red fox on the timeline", **extra
) -> dict:
    return FAST | {"prompt.text": text, "canvas.width": w, "canvas.height": h,
                   "duration.frames": frames, "duration.fps": fps} | extra  # fmt: skip


# ---------------------------------------------------------------- helpers


def facts(take: dict) -> dict:
    """A take as check.py's timeline functions take it (contracts/fixtures/timelines `takes`)."""
    out = {k: take[k] for k in ("kind", "width", "height", "bytes")}
    if take["kind"] == "video":
        out |= {k: take["video"][k] for k in ("duration_s", "fps", "frames", "has_audio")}
    return out


def doc(tl: dict) -> dict:
    """The normalized document of a Timeline answer."""
    return {k: tl[k] for k in ("target", "tracks", "duration_s", "frames")}


def clips(tl: dict, track: int = 0) -> list[dict]:
    return tl["tracks"][track]["clips"]


def clip(tl: dict, cid: str) -> dict:
    return next(c for t in tl["tracks"] for c in t["clips"] if c["id"] == cid)


def create(client, status: int = 201, **body) -> dict:
    return ok(client.post(TL, json=body), status)


def get(client, tl: dict | str) -> dict:
    return ok(client.get(f"{TL}/{tl if isinstance(tl, str) else tl['id']}"))


def put(client, tl: dict, written: dict, status: int = 200) -> dict:
    """PUT the written document (target + tracks) on tl's revision."""
    body = {"revision": tl["revision"], "target": written["target"], "tracks": written["tracks"]}
    return ok(client.put(f"{TL}/{tl['id']}", json=body), status)


def problems(answer: dict) -> list[list]:
    assert "problems" in answer, answer
    return [[p["code"], p["track"], p["clip"]] for p in answer["problems"]]


def edit(client, tl: dict, op: dict, takes: dict) -> dict:
    """One edit as the web makes it (check.t_edit on the stored document), sent by PUT; the answer must be what
    check.t_apply says."""
    want = check.t_apply(doc(tl), op, takes)
    assert "document" in want, f"{op}: {want}"
    got = put(client, tl, check.t_edit(doc(tl), op, takes))
    assert got["revision"] == tl["revision"] + 1
    assert doc(got) == want["document"], op
    return got


def wait_clip(client, tl: dict, cid: str, what: str, timeout: float = JOB_TIMEOUT) -> dict:
    """Poll until clip cid has a take and no shot (filled); a failed shot fails the test."""

    def filled():
        cur = get(client, tl)
        c = clip(cur, cid)
        assert (c.get("shot") or {}).get("status") != "failed", c
        return cur if c["take_id"] and "shot" not in c else None

    return wait_for(filled, timeout=timeout, what=what)


def frame_take(client, take_id: str, at: str) -> dict:
    """POST /takes/{id}/frame: the frame take (made at once, or by a media job)."""
    r = client.post(f"{STUDIO}/takes/{take_id}/frame", json={"at": at})
    if r.status_code == 200:
        return r.json()["take"]
    job = ok(r, 202)["job"]
    done = wait_job(client, job["id"])
    assert done["status"] == "succeeded", done
    return ok(client.get(f"{STUDIO}/takes/{done['take_id']}"))


def wait_job(client, job_id: str, timeout: float = EXPORT_TIMEOUT) -> dict:
    return wait_for(
        lambda: (
            (j := ok(client.get(f"{STUDIO}/media-jobs/{job_id}")))["status"]
            in ("succeeded", "failed", "cancelled")
            and j
        ),
        timeout=timeout,
        what=f"media job {job_id}",
    )


def plan(client, tl: dict) -> dict:
    return ok(client.post(f"{TL}/{tl['id']}/export/plan", json={}))


def reasons(p: dict, key: str = "reasons") -> set[tuple]:
    return {(r["code"], r["clip"]) for r in p[key]}


# ---------------------------------------------------------------- the takes


@pytest.fixture(scope="module", autouse=True)
def workflows(api) -> dict:
    ids = {w["id"]: w for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if VIDEO not in ids or I2V not in ids:
        pytest.skip("the fake runner has no echo-video / echo-image-to-video")
    return {w: ok(api.get(f"{STUDIO}/workflows/{w}")) for w in (VIDEO, I2V)}


@pytest.fixture(scope="module")
def sid(api) -> str:
    return new_session(api, "Contract timeline")["id"]


@pytest.fixture(scope="module")
def live(api, anon, sid) -> dict:
    """User A's takes: v24a, v24b (256x128, 49 frames at 24 fps: the fixtures' aaaa…1 and …2), v704 (704x448, 121
    frames: …3), v25 (256x128 at 25 fps), img (a 256x128 upload: bbbb…1)."""
    specs = {
        "v24a": video_inputs(text="shot a"),
        "v24b": video_inputs(text="shot b"),
        "v704": video_inputs(704, 448, 121, text="wide shot"),
        "v25": video_inputs(frames=49, fps=25, text="shot at 25"),
    }
    runs = {k: start_run(api, sid, v, VIDEO) for k, v in specs.items()}
    out = {}
    for k, r in runs.items():
        assert wait_run(api, r["id"])["status"] == "succeeded"
        out[k] = take_of_run(api, r["id"])
    out["img"] = upload(api, anon, png_bytes(256, 128), "still.png")
    return out


@pytest.fixture(scope="module")
def takes(live) -> dict:
    """take id → facts, for check.py."""
    return {t["id"]: facts(t) for t in live.values()}


@pytest.fixture(scope="module")
def fixture_ids(live) -> dict:
    return {
        "aaaaaaaa-0000-4000-8000-000000000001": live["v24a"]["id"],
        "aaaaaaaa-0000-4000-8000-000000000002": live["v24b"]["id"],
        "aaaaaaaa-0000-4000-8000-000000000003": live["v704"]["id"],
        "bbbbbbbb-0000-4000-8000-000000000001": live["img"]["id"],
    }


def test_fixture_takes_match(live):
    """The live takes have the fixtures' facts, so the fixtures' expectations hold as they are."""
    fx = json.loads((FIXTURES / "edits.json").read_text())["takes"]
    for key, fid in (("v24a", "aaaaaaaa-0000-4000-8000-000000000001"),
                     ("v704", "aaaaaaaa-0000-4000-8000-000000000003")):  # fmt: skip
        want = {
            k: fx[fid][k]
            for k in ("kind", "duration_s", "fps", "frames", "has_audio", "width", "height")
        }
        assert {k: facts(live[key])[k] for k in want} == want


# ---------------------------------------------------------------- sequences


def test_create_defaults_from_first_clip(api, live):
    a, b = live["v24a"], live["v24b"]
    tl = create(api, take_ids=[a["id"], b["id"]])
    assert (tl["name"], tl["revision"], tl["can_undo"], tl["can_redo"]) == (
        "Untitled sequence",
        1,
        False,
        False,
    )
    assert tl["target"] == {"width": 256, "height": 128, "fps": 24, "sample_rate": 48000}
    assert tl["session_id"] and tl["defaults"] == {"cast": [], "looks": []}
    v1 = clips(tl)
    assert [(c["take_id"], c["in"], c["out"], c["at"]) for c in v1] == [
        (a["id"], 0, 49 / 24, 0),
        (b["id"], 0, 49 / 24, 49 / 24),
    ]
    assert (tl["frames"], tl["duration_s"]) == (98, 98 / 24)
    assert set(tl["takes"]) == {a["id"], b["id"]}
    assert tl["takes"][a["id"]]["workflow_id"] == VIDEO and tl["takes"][a["id"]]["url"]
    assert create(api, take_ids=[live["v704"]["id"]])["target"] == {
        "width": 704, "height": 448, "fps": 24, "sample_rate": 48000}  # fmt: skip
    assert create(api, take_ids=[live["v25"]["id"]])["target"]["fps"] == 25
    img = create(api, name="  Stills  ", take_ids=[live["img"]["id"]])
    assert (img["name"], img["target"]["fps"], clips(img)[0]["hold"]) == ("Stills", 24, 3)
    assert create(api)["target"] == {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000}
    part = create(api, take_ids=[a["id"]], target={"width": 640, "fps": 30})
    assert part["target"] == {"width": 640, "height": 128, "fps": 30, "sample_rate": 48000}
    detail = ok(api.post(TL, json={"take_ids": [a["id"], NO_SUCH_ID]}), 422)["detail"]
    assert detail == f"take_ids[1]: take {NO_SUCH_ID} not found"


def test_list_rename_defaults_delete(api, live):
    tl = create(api, name="Fox at dusk", take_ids=[live["v24a"]["id"]])
    listed = ok(api.get(TL))["timelines"]
    assert listed[0]["id"] == tl["id"]  # newest updated_at first
    s = listed[0]
    assert (s["name"], s["revision"], s["clips"], s["duration_s"]) == ("Fox at dusk", 1, 1, 49 / 24)
    assert s["thumb_url"]
    renamed = ok(api.patch(f"{TL}/{tl['id']}", json={"name": "Fox at night"}))
    assert (renamed["name"], renamed["revision"]) == ("Fox at night", 1)  # no revision
    assert api.patch(f"{TL}/{tl['id']}", json={"name": "   "}).status_code == 422
    look = ok(api.get(f"{STUDIO}/library", params={"kind": "look"}))["items"][0]["id"]
    cast = element(api, f"fox{os.urandom(3).hex()}", [live["img"]["id"]])["id"]
    got = ok(api.patch(f"{TL}/{tl['id']}", json={"defaults": {"cast": [cast], "looks": [look]}}))
    assert (got["defaults"], got["revision"], got["can_undo"]) == (
        {"cast": [cast], "looks": [look]},
        1,
        False,
    )
    assert get(api, tl)["defaults"] == {"cast": [cast], "looks": [look]}
    assert api.delete(f"{TL}/{tl['id']}").status_code == 204
    assert api.get(f"{TL}/{tl['id']}").status_code == 404
    assert tl["id"] not in {x["id"] for x in ok(api.get(TL))["timelines"]}
    assert api.delete(f"{TL}/{tl['id']}").status_code == 404
    assert api.get(f"{TL}/not-a-uuid").status_code == 404


def test_add_clips_in_order(api, live, takes):
    a, b, img = live["v24a"]["id"], live["v24b"]["id"], live["img"]["id"]
    tl = create(api, take_ids=[a])
    tl = ok(api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [img, b]}))
    assert tl["revision"] == 2 and tl["can_undo"]
    assert [(c["take_id"], c.get("hold"), c["at"]) for c in clips(tl)] == [
        (a, None, 0),
        (img, 3, 49 / 24),
        (b, None, (49 + 72) / 24),
    ]  # fmt: skip  (at = frames × den / num)
    assert len({c["id"] for c in clips(tl)}) == 3
    r = api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [a], "track": "A2"})
    assert r.status_code == 422 and r.json()["detail"] == "unknown track A2"
    tl = edit(api, tl, {"op": "add_track"}, takes)
    tl = ok(api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [a, b], "track": "A2"}))
    assert [(c["take_id"], c["at"], c["in"], c["out"]) for c in clips(tl, 1)] == [
        (a, 0, 0, 49 / 24), (b, 49 / 24, 0, 49 / 24)]  # fmt: skip
    r = api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [a, img], "track": "A2"})
    assert (r.status_code, r.json()["detail"]) == (
        422,
        "take_ids[1]: an image cannot go on an audio track",
    )
    r = api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [NO_SUCH_ID]})
    assert (r.status_code, r.json()["detail"]) == (422, f"take_ids[0]: take {NO_SUCH_ID} not found")
    assert get(api, tl)["revision"] == tl["revision"]  # refusals store nothing


# ---------------------------------------------------------------- edits


def test_each_edit_operation(api, live, takes):
    """One sequence through every edit the editor makes: reorder, trim (an off-grid delta snaps to frames), split at
    the playhead, a dissolve with handles, gain and mute, an image hold, an extra audio track and its clip."""
    a, b, img = live["v24a"]["id"], live["v24b"]["id"], live["img"]["id"]
    tl = create(api, take_ids=[a, b, img])
    ca, cb, ci = (c["id"] for c in clips(tl))
    tl = edit(api, tl, {"op": "move", "clip": cb, "index": 0}, takes)
    assert [c["id"] for c in clips(tl)] == [cb, ca, ci]
    tl = edit(api, tl, {"op": "trim", "clip": cb, "edge": "out", "delta": -0.51}, takes)
    assert clip(tl, cb)["out"] == 37 / 24  # 49 − round(12.24) frames
    assert clip(tl, ca)["at"] == 37 / 24  # V1 ripples
    tl = edit(api, tl, {"op": "trim", "clip": ca, "edge": "in", "delta": 0.26}, takes)
    assert clip(tl, ca)["in"] == 6 / 24
    tl = edit(api, tl, {"op": "split", "track": "V1", "at": 2.5, "id": "split1"}, takes)
    assert [c["id"] for c in clips(tl)] == [cb, ca, "split1", ci]
    assert clip(tl, ca)["out"] == clip(tl, "split1")["in"]
    dissolve = {"type": "dissolve", "frames": 8}
    tl = edit(api, tl, {"op": "set", "clip": ca, "transition_in": dissolve}, takes)
    assert clip(tl, ca)["transition_in"] == dissolve
    r = put(
        api,
        tl,
        check.t_edit(doc(tl), {"op": "set", "clip": cb, "transition_in": dissolve}, takes),
        422,
    )
    assert problems(r) == [["dissolve_first", "V1", cb]]
    tl = edit(api, tl, {"op": "set", "clip": cb, "gain_db": -6, "mute": True}, takes)
    assert (clip(tl, cb)["gain_db"], clip(tl, cb)["mute"]) == (-6, True)
    tl = edit(api, tl, {"op": "set", "clip": ci, "hold": 1.3}, takes)
    assert clip(tl, ci)["hold"] == 31 / 24  # snapped to a frame
    tl = edit(api, tl, {"op": "trim", "clip": ci, "edge": "out", "delta": 0.5}, takes)
    tl = edit(api, tl, {"op": "add_track"}, takes)
    tl = ok(api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [a], "track": "A2"}))
    (m,) = clips(tl, 1)
    tl = edit(api, tl, {"op": "move", "clip": m["id"], "at": 1.01}, takes)
    assert clip(tl, m["id"])["at"] == 24 / 24
    tl = edit(api, tl, {"op": "trim", "clip": m["id"], "edge": "out", "delta": -0.5}, takes)
    tl = edit(api, tl, {"op": "track", "track": "A2", "gain_db": 3, "mute": False}, takes)
    tl = edit(api, tl, {"op": "remove", "clip": "split1"}, takes)
    tl = edit(api, tl, {"op": "lift", "clip": ca}, takes)
    assert clip(tl, ca)["take_id"] is None
    r = put(api, tl, check.t_edit(doc(tl), {"op": "set", "clip": ci, "gain_db": 13}, takes), 422)
    assert "detail" in r  # outside the schema (-60..12)


def test_stale_revision_409(api, live, takes):
    tl = create(api, take_ids=[live["v24a"]["id"]])
    newer = edit(api, tl, {"op": "set", "clip": clips(tl)[0]["id"], "gain_db": -3}, takes)
    r = put(api, tl, doc(tl), 409)
    assert r["detail"] == f"timeline changed since you opened it (now revision {newer['revision']})"
    ok(api.post(f"{TL}/{tl['id']}/undo", json={"revision": 1}), 409)
    assert get(api, tl)["revision"] == newer["revision"]


# ---------------------------------------------------------------- the rules fixtures through the API


def cases(name: str) -> list:
    fx = json.loads((FIXTURES / name).read_text())
    return [pytest.param(c, id=c["description"][:60]) for c in fx["cases"]]


LIVE_FIXTURE_TAKES = {"aaaaaaaa-0000-4000-8000-000000000001", "aaaaaaaa-0000-4000-8000-000000000002",
                      "aaaaaaaa-0000-4000-8000-000000000003", "bbbbbbbb-0000-4000-8000-000000000001"}  # fmt: skip
FIXTURE_TAKES = set(json.loads((FIXTURES / "edits.json").read_text())["takes"])


def mapped(value, ids: dict):
    """The fixture value with its take ids replaced (both ways work: pass the inverse map)."""
    s = json.dumps(value)
    for k, v in ids.items():
        s = s.replace(k, v)
    return json.loads(s)


def reproducible(case: dict) -> str | None:
    """Why a fixture case cannot run through the API, or None."""
    s = json.dumps(case)
    if any(t in s for t in FIXTURE_TAKES - LIVE_FIXTURE_TAKES):
        return "names a take the fake cannot make (a silent video, a 50-frame 25 fps one)"
    if case.get("takes"):
        return "names takes of its own (lengths the fake cannot make; studio and web run it on the fixtures)"
    if case.get("previous"):
        return "needs a stored revision with a shot or a since-deleted take (test_offline_clip covers it)"
    return None


@pytest.fixture(scope="module")
def scratch(api) -> dict:
    """One sequence the fixture cases are written to in turn."""
    return {"tl": create(api, name="Contract fixtures")}


@pytest.mark.parametrize("case", cases("normalize.json"))
def test_normalize_fixture(api, scratch, fixture_ids, case):
    if why := reproducible(case):
        pytest.skip(why)
    back = {v: k for k, v in fixture_ids.items()}
    written = mapped(case["document"], fixture_ids)
    r = api.put(f"{TL}/{scratch['tl']['id']}", json={"revision": scratch["tl"]["revision"]} | {
        k: written[k] for k in ("target", "tracks")})  # fmt: skip
    if "problems" in case["expect"]:
        assert r.status_code == 422, r.text
        assert problems(r.json()) == case["expect"]["problems"]
        assert r.json()["detail"].startswith(
            f"timeline has {len(case['expect']['problems'])} problem"
        )
        return
    scratch["tl"] = ok(r)
    assert mapped(doc(scratch["tl"]), back) == case["expect"]["document"]


@pytest.mark.parametrize("case", cases("edits.json"))
def test_edit_fixture(api, scratch, fixture_ids, takes, case):
    if why := reproducible(case):
        pytest.skip(why)
    op, expect = case["op"], case["expect"]
    if "error" in expect:
        pytest.skip(f"refused by the editor itself ({expect['error']}): nothing is sent")
    if op["op"] in ("insert", "fill"):
        pytest.skip("made by studio only (New shot): test_new_shot_in_a_gap covers it")
    back = {v: k for k, v in fixture_ids.items()}
    tl = put(api, scratch["tl"], mapped(case["document"], fixture_ids))
    assert mapped(doc(tl), back) == case["document"]  # a normalized document is a fixed point
    live_op = mapped(op, fixture_ids)
    if op["op"] == "add":
        body = {"take_ids": live_op["take_ids"], "track": op.get("track", "V1")}
        r = api.post(f"{TL}/{tl['id']}/clips", json=body)
    else:
        written = check.t_edit(doc(tl), live_op, takes)
        r = api.put(f"{TL}/{tl['id']}", json={"revision": tl["revision"]} | {
            k: written[k] for k in ("target", "tracks")})  # fmt: skip
    if "problems" in expect:
        assert r.status_code == 422, r.text
        assert problems(r.json()) == expect["problems"]
        scratch["tl"] = tl
        return
    scratch["tl"] = got = ok(r)
    assert got["revision"] == tl["revision"] + 1
    if op["op"] == "add":  # studio names the new clips: compare them under the fixture's ids
        old = {c["id"] for t in tl["tracks"] for c in t["clips"]}
        new = [c["id"] for t in got["tracks"] for c in t["clips"] if c["id"] not in old]
        assert len(new) == len(op["ids"])
        back |= {f'"id": "{n}"': f'"id": "{i}"' for n, i in zip(new, op["ids"])}
    assert mapped(doc(got), back) == expect["document"]


# ---------------------------------------------------------------- revisions


def test_undo_redo_walk_revisions(api, live, takes):
    tl = create(api, take_ids=[live["v24a"]["id"], live["v24b"]["id"]])
    ca = clips(tl)[0]["id"]
    docs = {1: doc(tl)}
    for gain in (-3, -6, -9):
        tl = edit(api, tl, {"op": "set", "clip": ca, "gain_db": gain}, takes)
        docs[tl["revision"]] = doc(tl)
    assert tl["revision"] == 4
    undo = lambda t: ok(api.post(f"{TL}/{t['id']}/undo", json={"revision": t["revision"]}))
    redo = lambda t: ok(api.post(f"{TL}/{t['id']}/redo", json={"revision": t["revision"]}))
    tl = undo(tl)  # revision 5 = the document of 3
    assert (tl["revision"], doc(tl), tl["can_undo"], tl["can_redo"]) == (5, docs[3], True, True)
    tl = undo(tl)  # 6 = 2
    assert (tl["revision"], doc(tl)) == (6, docs[2])
    tl = redo(tl)  # 7 = 3
    assert (tl["revision"], doc(tl), tl["can_redo"]) == (7, docs[3], True)
    tl = redo(tl)  # 8 = 4
    assert (tl["revision"], doc(tl), tl["can_redo"]) == (8, docs[4], False)
    r = api.post(f"{TL}/{tl['id']}/redo", json={"revision": tl["revision"]})
    assert (r.status_code, r.json()["detail"]) == (409, "nothing to redo")
    tl = undo(tl)
    tl = edit(
        api, tl, {"op": "set", "clip": ca, "mute": True}, takes
    )  # an edit after an undo clears redo
    assert not tl["can_redo"]
    assert api.post(f"{TL}/{tl['id']}/redo", json={"revision": tl["revision"]}).status_code == 409
    for _ in range(3):
        tl = undo(tl)
    assert doc(tl) == docs[1] and not tl["can_undo"]
    r = api.post(f"{TL}/{tl['id']}/undo", json={"revision": tl["revision"]})
    assert (r.status_code, r.json()["detail"]) == (409, "nothing to undo")
    assert get(api, tl)["revision"] == tl["revision"]


def test_hundred_revisions_kept(api, live, takes):
    """After 101 edits (revisions 1..102) the last 100 are kept: revision 2 can still be planned, 1 cannot. Each undo
    writes a revision, so the k-th undo (restoring 102 − k) prunes one more: exactly 50 undos fit."""
    tl = create(api, take_ids=[live["v24a"]["id"]])
    ca = clips(tl)[0]["id"]
    docs = {1: doc(tl)}
    for i in range(101):
        tl = put(
            api, tl, check.t_edit(doc(tl), {"op": "set", "clip": ca, "gain_db": -(i % 60)}, takes)
        )
        docs[tl["revision"]] = doc(tl)
    assert tl["revision"] == 102
    plan_of = lambda rev: api.post(f"{TL}/{tl['id']}/export/plan", json={"revision": rev})
    assert plan_of(3).status_code == 200 and plan_of(1).status_code == 404
    undos = 0
    while tl["can_undo"]:
        tl = ok(api.post(f"{TL}/{tl['id']}/undo", json={"revision": tl["revision"]}))
        undos += 1
        assert doc(tl) == docs[102 - undos]
    assert undos == 50
    assert api.post(f"{TL}/{tl['id']}/undo", json={"revision": tl["revision"]}).status_code == 409


# ---------------------------------------------------------------- offline


def test_offline_clip(api, live, sid):
    run = start_run(api, sid, video_inputs(text="to be deleted"), VIDEO)
    assert wait_run(api, run["id"])["status"] == "succeeded"
    gone = take_of_run(api, run["id"])["id"]
    tl = create(api, take_ids=[live["v24a"]["id"], gone])
    cg = clips(tl)[1]
    assert api.delete(f"{STUDIO}/takes/{gone}").status_code == 204
    cur = get(api, tl)  # the sequence still loads
    c = clip(cur, cg["id"])
    assert c["offline"] is True and (c["take_id"], c["in"], c["out"], c["at"]) == (
        gone,
        0,
        49 / 24,
        49 / 24,
    )
    assert gone not in cur["takes"] and cur["frames"] == 98
    p = plan(api, cur)
    assert {"code": "offline", "clip": cg["id"], "message": p["blocked"][0]["message"]} in p[
        "blocked"
    ]
    r = api.post(f"{TL}/{tl['id']}/export", json={"revision": cur["revision"]})
    assert (r.status_code, r.json()["detail"]) == (409, f"clip {cg['id']} is offline")
    again = put(
        api, cur, doc(cur)
    )  # writing it back keeps it offline (the stored revision named it)
    assert clip(again, cg["id"])["offline"] is True
    r = api.post(
        f"{TL}/{tl['id']}/rerender", json={"revision": again["revision"], "clips": [cg["id"]]}
    )
    assert (r.status_code, r.json()["detail"]) == (422, f"clip {cg['id']} is offline")


# ---------------------------------------------------------------- directing


def test_workflow_keyframes(workflows):
    assert workflows[VIDEO]["keyframes"] is None  # text-to-video: no frame options
    assert workflows[I2V]["keyframes"] == {
        "param": "keyframes.images", "image": "image", "seconds": "seconds", "last": True}  # fmt: skip


@pytest.fixture(scope="module")
def shot(api, live) -> dict:
    """A New shot into a 1 s gap after v24a: {"tl" (filled), "clip", "run", "take", "seen" (placeholder states)}."""
    a, b = live["v24a"]["id"], live["v24b"]["id"]
    tl = create(api, name="Contract shots", take_ids=[a, b])
    body = {"slot": {"at": 2.0, "length": 1.0, "id": "gap1"},
            "run": {"workflow_id": VIDEO, "inputs": video_inputs(frames=25, text="new shot", **{
                "video.steps": 8, "video.delay_ms": 150})}}  # fmt: skip
    ans = ok(api.post(f"{TL}/{tl['id']}/shots", json=body), 202)
    placeholder, run = clip(ans["timeline"], "gap1"), ans["run"]
    assert [c["id"] for c in clips(ans["timeline"])][1] == "gap1"  # at the cut nearest 2.0 s
    assert (placeholder["take_id"], placeholder["hold"], placeholder["shot"]["run_id"]) == (
        None,
        1,
        run["id"],
    )
    assert (
        ans["timeline"]["revision"] == tl["revision"] + 1 and run["session_id"] == tl["session_id"]
    )
    seen = set()

    def filled():
        cur = get(api, tl)
        c = clip(cur, "gap1")
        if "shot" in c:
            seen.add(c["shot"]["status"])
        return cur if c["take_id"] else None

    done = wait_for(filled, what="the shot's take in its slot")
    return {"tl": done, "clip": clip(done, "gap1"), "run": run, "take": take_of_run(api, run["id"]),
            "seen": seen, "before": ans["timeline"]}  # fmt: skip


def test_new_shot_in_a_gap(api, shot):
    c, take, tl = shot["clip"], shot["take"], shot["tl"]
    assert shot["seen"] <= {"queued", "running"}
    assert (c["take_id"], c["in"], c["out"], c["alternatives"]) == (take["id"], 0, 1, [take["id"]])
    assert "shot" not in c and tl["revision"] == shot["before"]["revision"] + 1  # studio's fill
    assert clips(tl)[2]["at"] == (49 + 24) / 24  # the next clip after the 1 s slot
    assert take["session_id"] == tl["session_id"] and take["made_with"]["workflow_id"] == VIDEO
    assert tl["takes"][take["id"]]["workflow_id"] == VIDEO
    # undo skips the fill: back before the shot; redo brings the slot back filled, never as a placeholder
    und = ok(api.post(f"{TL}/{tl['id']}/undo", json={"revision": tl["revision"]}))
    assert "gap1" not in {x["id"] for x in clips(und)}
    red = ok(api.post(f"{TL}/{tl['id']}/redo", json={"revision": und["revision"]}))
    rc = clip(red, "gap1")
    assert (rc["take_id"], "shot" in rc) == (take["id"], False)
    r = api.post(
        f"{TL}/{tl['id']}/shots", json={"slot": {"clip": "nope"}, "run": {"workflow_id": VIDEO}}
    )
    assert (r.status_code, r.json()["detail"]) == (422, "unknown clip nope")
    r = api.post(f"{TL}/{tl['id']}/shots", json={"slot": {"at": 0, "length": 1}, "run": {
        "workflow_id": VIDEO, "inputs": {"canvas.width": 3}}})  # fmt: skip
    assert (
        r.status_code == 422 and get(api, tl)["revision"] == red["revision"]
    )  # a refused run stores nothing


def test_replace_among_alternatives(api, shot):
    """ "From this clip": redo the slot with its own settings → a second alternative, now current; Replace picks the
    first again."""
    tl, first = get(api, shot["tl"]), shot["take"]["id"]
    reuse = ok(api.post(f"{STUDIO}/takes/{first}/reuse"))
    run = {"workflow_id": reuse["workflow_id"], "inputs": reuse["inputs"]}
    ans = ok(api.post(f"{TL}/{tl['id']}/shots", json={"slot": {"clip": "gap1"}, "run": run}), 202)
    assert clip(ans["timeline"], "gap1")["shot"]["run_id"] == ans["run"]["id"]
    r = api.post(f"{TL}/{tl['id']}/shots", json={"slot": {"clip": "gap1"}, "run": run})
    assert (r.status_code, r.json()["detail"]) == (409, "clip gap1 already has a shot running")
    tl = wait_clip(api, tl, "gap1", "the redo's take")
    second = take_of_run(api, ans["run"]["id"])["id"]
    c = clip(tl, "gap1")
    assert (c["take_id"], c["alternatives"]) == (second, [first, second])
    take_facts = {t: facts(ok(api.get(f"{STUDIO}/takes/{t}"))) for t in tl["takes"]}
    tl = edit(api, tl, {"op": "replace", "clip": "gap1", "take_id": first}, take_facts)
    assert (clip(tl, "gap1")["take_id"], clip(tl, "gap1")["alternatives"]) == (
        first,
        [first, second],
    )


def test_start_from_previous_last_frame(api, live, workflows):
    """The previous clip's last frame (an image take whose parent is the video) as row 1, the next clip's first frame
    as the last row: the new clip's parents are those frames."""
    a, b = live["v24a"], live["v24b"]
    last = frame_take(api, a["id"], "last")
    assert (last["kind"], last["source"], last["parents"]) == ("image", "frame", [a["id"]])
    assert last["frame_of"] == {"take_id": a["id"], "at": "last"} and last["title"].endswith(
        "· last frame"
    )
    assert (last["width"], last["height"], last["made_with"]) == (256, 128, None)
    assert frame_take(api, a["id"], "last")["id"] == last["id"]  # idempotent per (take, at)
    first = frame_take(api, b["id"], "first")
    assert first["frame_of"] == {"take_id": b["id"], "at": "first"} and first["parents"] == [
        b["id"]
    ]
    r = api.post(f"{STUDIO}/takes/{live['img']['id']}/frame", json={"at": "last"})
    assert (r.status_code, r.json()["detail"]) == (422, f"take {live['img']['id']} is not a video")
    kf = workflows[I2V]["keyframes"]
    tl = create(api, take_ids=[a["id"], b["id"]])
    frames, fps = 25, 24
    rows = [{kf["image"]: {"take_id": last["id"]}, kf["seconds"]: 0},
            {kf["image"]: {"take_id": first["id"]}, kf["seconds"]: frames / fps}]  # fmt: skip
    run = {"workflow_id": I2V, "inputs": {"prompt.text": "between a and b", "sample.delay_ms": 50,
                                          "duration.frames": frames, "duration.fps": fps, kf["param"]: rows}}  # fmt: skip
    ans = ok(api.post(f"{TL}/{tl['id']}/shots", json={"slot": {"at": 49 / 24, "length": 1, "id": "mid"},
                                                     "run": run}), 202)  # fmt: skip
    tl = wait_clip(api, ans["timeline"], "mid", "the in-between shot")
    made = ok(api.get(f"{STUDIO}/takes/{clip(tl, 'mid')['take_id']}"))
    assert made["parents"] == [last["id"], first["id"]]
    assert made["made_with"]["inputs"][kf["param"]][0][kf["image"]] == {"take_id": last["id"]}
    assert {x["id"] for x in ok(api.get(f"{STUDIO}/takes/{a['id']}"))["children"]} >= {last["id"]}


def test_rerender_two_clips(api, live, sid):
    runs = [
        start_run(api, sid, video_inputs(frames=25, text=f"rerender {i}"), VIDEO) for i in (1, 2)
    ]
    olds = []
    for r in runs:
        assert wait_run(api, r["id"])["status"] == "succeeded"
        olds.append(take_of_run(api, r["id"])["id"])
    tl = create(api, name="Contract rerender", take_ids=olds + [live["img"]["id"]])
    c1, c2, ci = (c["id"] for c in clips(tl))
    r = api.post(f"{TL}/{tl['id']}/rerender", json={"revision": tl["revision"], "clips": [ci]})
    assert (r.status_code, r.json()["detail"]) == (
        422,
        f"clip {ci}: its take was not made by a workflow",
    )
    r = api.post(f"{TL}/{tl['id']}/rerender", json={"revision": tl["revision"] + 1, "clips": [c1]})
    assert r.status_code == 409
    ans = ok(
        api.post(f"{TL}/{tl['id']}/rerender", json={"revision": tl["revision"], "clips": [c1, c2]}),
        202,
    )
    batch = ans["batch"]
    assert (batch["timeline_id"], batch.get("pipeline_id")) == (tl["id"], None)  # absent or null
    assert batch["pipeline_title"] == "Re-render · Contract rerender"
    for cid in (c1, c2):
        s = clip(ans["timeline"], cid)["shot"]
        assert (s["batch_id"], s["step"]) == (batch["id"], f"clip_{cid}")
    r = api.post(
        f"{TL}/{tl['id']}/rerender", json={"revision": ans["timeline"]["revision"], "clips": [c1]}
    )
    assert r.status_code == 409  # its shot is running
    assert wait_batch(api, batch["id"])["status"] == "done"
    for cid, old in ((c1, olds[0]), (c2, olds[1])):
        tl = wait_clip(api, tl, cid, f"clip {cid}'s re-render")
        c = clip(tl, cid)
        assert c["take_id"] != old and c["alternatives"] == [old, c["take_id"]]
        new = ok(api.get(f"{STUDIO}/takes/{c['take_id']}"))
        assert (
            new["batch_id"] == batch["id"] and new["made_with"]["pipeline"]["node"] == f"clip_{cid}"
        )
    assert clip(tl, ci)["take_id"] == live["img"]["id"]  # not selected: unchanged


# ---------------------------------------------------------------- export


@pytest.fixture(scope="module")
def media(api, live) -> dict:
    """The plan of a sequence that can export; skips when no live env has ffmpeg (`media`)."""
    tl = create(api, take_ids=[live["v24a"]["id"], live["v24b"]["id"]])
    p = plan(api, tl)
    if p["host"] is None:
        pytest.skip(
            "no live env advertises media (ffmpeg): the Docker fake runner's image has none"
        )
    return p


def test_export_plan(api, live):
    a, b, w, f25, img = (live[k] for k in ("v24a", "v24b", "v704", "v25", "img"))
    tl = create(api, take_ids=[a["id"], b["id"]])
    p = plan(api, tl)
    assert (p["revision"], p["stream_copy"], p["reasons"], p["frames"]) == (1, True, [], 98)
    assert reasons(p, "blocked") <= {("no_host", None)}
    assert p["output"] == {"width": 256, "height": 128, "fps": 24, "sample_rate": 48000, "container": "mp4",
                           "video_codec": "h264", "audio_codec": "aac", "video_bitrate": None}  # fmt: skip
    assert p["estimated_bytes"] == a["bytes"] + b["bytes"] + check.rnd(192000 * 98 / 24 / 8)
    # re-encode, with the reason per clip
    size = create(api, take_ids=[a["id"], w["id"]], target={"width": 256, "height": 128})
    p = plan(api, size)
    cw = clips(size)[1]["id"]
    assert not p["stream_copy"] and ("size", cw) in reasons(p)
    assert p["output"]["video_bitrate"] == 1_000_000  # clamp(256 × 128 × 24 × 0.15) to 1 Mb/s
    assert p["estimated_bytes"] == check.rnd((1_000_000 + 192000) * p["duration_s"] / 8)
    rate = create(api, take_ids=[a["id"], f25["id"]])
    assert ("frame_rate", clips(rate)[1]["id"]) in reasons(plan(api, rate))
    mixed = create(api, take_ids=[a["id"], img["id"]])
    assert ("image", clips(mixed)[1]["id"]) in reasons(plan(api, mixed))
    # a dissolve: handles first (a ends 4 frames early, b starts 4 frames in), then the dissolve
    takes = {t["id"]: facts(t) for t in (a, b)}
    ca, cb = (c["id"] for c in clips(tl))
    tl = edit(api, tl, {"op": "trim", "clip": ca, "edge": "out", "delta": -4 / 24}, takes)
    tl = edit(api, tl, {"op": "trim", "clip": cb, "edge": "in", "delta": 4 / 24}, takes)
    tl = edit(
        api,
        tl,
        {"op": "set", "clip": cb, "transition_in": {"type": "dissolve", "frames": 8}},
        takes,
    )
    p = plan(api, tl)
    assert not p["stream_copy"] and {
        ("transition", cb),
        ("trimmed", ca),
        ("trimmed", cb),
    } <= reasons(p)
    assert p["revision"] == tl["revision"]


def mp4_ok(api, take: dict) -> bytes:
    """The export's MP4 through its signed link: ftyp, moov before mdat, a video and a sound track."""
    data = api.get(take["url"]).content
    assert data[4:8] == b"ftyp", data[:16]
    tops, i = [], 0
    while i + 8 <= len(data):
        size, kind = struct.unpack(">I4s", data[i : i + 8])
        size = struct.unpack(">Q", data[i + 8 : i + 16])[0] if size == 1 else size or len(data) - i
        tops.append(kind)
        i += size
    assert tops.index(b"moov") < tops.index(b"mdat"), tops  # +faststart
    assert b"avc1" in data and b"mp4a" in data
    return data


def test_export_to_a_take(api, anon, live, media):
    a, img, b = live["v24a"], live["img"], live["v24b"]
    tl = create(api, name="Contract export", take_ids=[a["id"], img["id"], b["id"]])
    ci = clips(tl)[1]["id"]
    tl = edit(
        api, tl, {"op": "set", "clip": ci, "hold": 1.0}, {t["id"]: facts(t) for t in (a, img, b)}
    )
    p = plan(api, tl)
    assert not p["stream_copy"] and p["blocked"] == [] and p["frames"] == 49 + 24 + 49
    r = api.post(f"{TL}/{tl['id']}/export", json={"revision": tl["revision"] - 1})
    assert r.status_code == 409  # stale
    job = ok(api.post(f"{TL}/{tl['id']}/export", json={"revision": tl["revision"]}), 202)
    assert (job["kind"], job["status"], job["timeline_id"], job["revision"]) == (
        "export", "queued", tl["id"], tl["revision"])  # fmt: skip
    assert (
        job["mode"] == "conform"
        and job["take_id"] is None
        and (job["host"], job["env"]) == (p["host"], p["env"])
    )
    done = wait_job(api, job["id"])
    assert done["status"] == "succeeded", done
    assert (
        done["progress"]["total"] == p["frames"] and done["failure"] is None and done["finished_at"]
    )
    take = ok(api.get(f"{STUDIO}/takes/{done['take_id']}"))
    assert (take["kind"], take["source"], take["content_type"], take["title"]) == (
        "video", "export", "video/mp4", "Contract export")  # fmt: skip
    assert (
        take["made_with"] is None
    )  # the sequence and revision are named in `export`, not made_with
    assert take["export"] == {"timeline_id": tl["id"], "timeline_name": "Contract export",
                              "revision": tl["revision"], "job_id": job["id"], "mode": "conform"}  # fmt: skip
    assert take["parents"] == [a["id"], img["id"], b["id"]]  # inputs = its clips
    v = take["video"]
    assert (take["width"], take["height"], v["fps"], v["has_audio"]) == (256, 128, 24, True)
    assert abs(v["duration_s"] - p["duration_s"]) <= 1 / 24 and abs(v["frames"] - p["frames"]) <= 1
    mp4_ok(anon, take)
    assert ok(api.get(f"{TL}/{tl['id']}/exports"))["jobs"][0]["id"] == job["id"]
    ok(api.post(f"{STUDIO}/takes/{take['id']}/reuse"), 409)  # an export is not a run
    r = api.post(f"{STUDIO}/media-jobs/{job['id']}/cancel")
    assert (r.status_code, r.json()["detail"]) == (409, "job already ended")


def test_export_stream_copy(api, anon, live, media):
    tl = create(api, name="Contract copy", take_ids=[live["v24a"]["id"], live["v24b"]["id"]])
    assert plan(api, tl)["stream_copy"]
    job = ok(api.post(f"{TL}/{tl['id']}/export", json={"revision": 1}), 202)
    assert job["mode"] == "copy"
    done = wait_job(api, job["id"])
    assert done["status"] == "succeeded", done
    assert done["mode"] == "copy" or done["reason"]  # copy, or a conform the runner explains
    take = ok(api.get(f"{STUDIO}/takes/{done['take_id']}"))
    assert take["export"]["mode"] == done["mode"] and abs(take["video"]["frames"] - 98) <= 1
    mp4_ok(anon, take)


def test_cancel_mid_export(api, media):
    """A long sequence (an hour of black at 1280x720) cancelled once it runs: cancelled, no take."""
    tl = create(api, name="Contract cancel")
    long = {"target": tl["target"], "tracks": [{"kind": "video", "clips": [
        {"id": "black", "take_id": None, "hold": 3600}]}]}  # fmt: skip
    tl = put(api, tl, long)
    job = ok(api.post(f"{TL}/{tl['id']}/export", json={"revision": tl["revision"]}), 202)
    r = api.post(f"{TL}/{tl['id']}/export", json={"revision": tl["revision"]})
    assert (r.status_code, r.json()["detail"]) == (
        409,
        "an export of this sequence is already running",
    )
    wait_for(lambda: ok(api.get(f"{STUDIO}/media-jobs/{job['id']}"))["status"] == "running",
             timeout=EXPORT_TIMEOUT, what="the export to start")  # fmt: skip
    assert ok(api.post(f"{STUDIO}/media-jobs/{job['id']}/cancel"))["id"] == job["id"]
    done = wait_job(api, job["id"], timeout=60)
    assert (done["status"], done["take_id"]) == ("cancelled", None)
    exports = [t for t in all_takes(api, source="export") if t["export"]["timeline_id"] == tl["id"]]
    assert exports == []
    again = ok(
        api.post(f"{TL}/{tl['id']}/export", json={"revision": tl["revision"]}), 202
    )  # retryable
    ok(api.post(f"{STUDIO}/media-jobs/{again['id']}/cancel"))
    assert wait_job(api, again["id"], timeout=60)["status"] == "cancelled"


# ---------------------------------------------------------------- isolation


def test_isolation(api, api_b, live, shot):
    tl, take = (
        get(api, shot["tl"]),
        live["v24a"]["id"],
    )  # as it is now: earlier tests edit the shot's sequence
    base = f"{TL}/{tl['id']}"
    rev = {"revision": tl["revision"]}
    for method, path, body in [
        ("GET", base, None),
        ("PUT", base, rev | {"target": tl["target"], "tracks": [{"kind": "video", "clips": []}]}),
        ("PATCH", base, {"name": "mine now"}),
        ("POST", f"{base}/clips", {"take_ids": [take]}),
        ("POST", f"{base}/undo", rev),
        ("POST", f"{base}/redo", rev),
        ("POST", f"{base}/shots", {"slot": {"clip": "gap1"}, "run": {"workflow_id": VIDEO}}),
        ("POST", f"{base}/rerender", rev),
        ("POST", f"{base}/export/plan", {}),
        ("POST", f"{base}/export", rev),
        ("GET", f"{base}/exports", None),
        ("DELETE", base, None),
        ("POST", f"{STUDIO}/takes/{take}/frame", {"at": "first"}),
    ]:
        r = api_b.request(method, path, json=body)
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    assert tl["id"] not in {x["id"] for x in ok(api_b.get(TL))["timelines"]}
    jobs = ok(api.get(f"{TL}/{tl['id']}/exports"))["jobs"]
    for j in jobs[:1]:
        assert api_b.get(f"{STUDIO}/media-jobs/{j['id']}").status_code == 404
        assert api_b.post(f"{STUDIO}/media-jobs/{j['id']}/cancel").status_code == 404
    assert api_b.get(f"{STUDIO}/media-jobs/{NO_SUCH_ID}").status_code == 404
    # a clip naming A's take is refused everywhere B can write one
    r = api_b.post(TL, json={"take_ids": [take]})
    assert (r.status_code, r.json()["detail"]) == (422, f"take_ids[0]: take {take} not found")
    mine = create(api_b)
    r = api_b.post(f"{TL}/{mine['id']}/clips", json={"take_ids": [take]})
    assert (r.status_code, r.json()["detail"]) == (422, f"take_ids[0]: take {take} not found")
    written = {
        "target": mine["target"],
        "tracks": [{"kind": "video", "clips": [{"id": "x", "take_id": take, "in": 0, "out": 1}]}],
    }  # in/out given: a take B cannot see has no length to default to (out_required)
    assert problems(put(api_b, mine, written, 422)) == [["take_not_found", "V1", "x"]]
    assert get(api, tl)["revision"] == tl["revision"] and get(api, tl)["name"] == tl["name"]
