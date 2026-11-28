"""Timeline audio (API 1.32; contracts/README.md "Dialogue and Timeline audio", Timeline: New line, Make shot,
export): an echo-voice-over take as a clip on an audio track (add at the track's end, move, trim on the frame grid
with the end kept, every write checked against check.py's t_apply), refused on V1 (POST …/clips
and a written document), the export plan's `sounds`, New line (the placeholder at the playhead, the fill checked
against t_apply, a redo with its 409, every 422 text, a cancelled line removed) and Make shot (the audio clip's
Audio to video offer against check.py's make_shot, the `place` edit splitting an empty slot, the shot's take with the
line as its parent). Skipped when the fake runner has no echo-voice-over or echo-a2v.

Not covered: a ZONOS3 44.1 kHz line in the mix (the fake makes 24 kHz only), the export itself with lines (needs a
live env with ffmpeg: test_timeline's exports), Re-render of a line (rerender_steps fixtures in studio's tests)."""

import pytest
from conftest import headers, make_client
from helpers import STUDIO, check, new_session, ok, start_run, take_of_run, wait_for, wait_run
from test_timeline import (
    TL,
    VIDEO,
    clip,
    clips,
    create,
    doc,
    edit,
    facts,
    get,
    plan,
    problems,
    put,
    video_inputs,
    wait_clip,
)

SPEAK, A2V = "echo-voice-over", "echo-a2v"
ADA = "echo/ada"
TEXT = "Back by noon, I said."  # 21 characters: 1.4 s
FAST_A2V = {"prompt.text": "a line to see", "a2v.delay_ms": 50}
D49 = 49 / 24


def speak_inputs(text: str = TEXT, delay_ms: int = 50) -> dict:
    return {"prompt.text": text, "speak.voice": {"voice_id": ADA}, "speak.delay_ms": delay_ms}


def line_run(text: str = TEXT, delay_ms: int = 50) -> dict:
    return {"workflow_id": SPEAK, "inputs": speak_inputs(text, delay_ms)}


def new_line(client, tl: dict, slot: dict, run: dict, status: int = 202) -> dict:
    return ok(client.post(f"{TL}/{tl['id']}/lines", json={"slot": slot, "run": run}), status)


def refused(client, tl: dict, slot: dict, run: dict, status: int = 422) -> str:
    r = client.post(f"{TL}/{tl['id']}/lines", json={"slot": slot, "run": run})
    assert r.status_code == status, r.text
    return r.json()["detail"]


def no_shots(d: dict) -> dict:
    """A document without the placeholders' shots (t_apply makes no shot: only studio does)."""
    return d | {"tracks": [t | {"clips": [{k: v for k, v in c.items() if k != "shot"} for c in t["clips"]]}
                           for t in d["tracks"]]}  # fmt: skip


def all_facts(client, tl: dict) -> dict:
    ids = set(tl["takes"]) | {
        a for t in tl["tracks"] for c in t["clips"] for a in c.get("alternatives", [])
    }
    return {i: facts(ok(client.get(f"{STUDIO}/takes/{i}"))) for i in ids}


@pytest.fixture(scope="module", autouse=True)
def live_wfs(api) -> dict:
    listed = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if not {SPEAK, A2V, VIDEO} <= listed:
        pytest.skip("the fake runner has no echo-voice-over / echo-a2v / echo-video workflow")
    return {w: ok(api.get(f"{STUDIO}/workflows/{w}")) for w in (SPEAK, A2V)}


@pytest.fixture(scope="module")
def ut(new_user):
    """User T: this module's sequences (a user holds at most 100)."""
    with make_client(headers(new_user("TA")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def sid(ut) -> str:
    return new_session(ut, "Contract timeline audio")["id"]


def made(client, sid: str, inputs: dict, wid: str) -> dict:
    run = start_run(client, sid, inputs, wid)
    assert wait_run(client, run["id"])["status"] == "succeeded"
    return take_of_run(client, run["id"])


@pytest.fixture(scope="module")
def video(ut, sid) -> dict:
    return made(ut, sid, video_inputs(text="a shot under the line"), VIDEO)


@pytest.fixture(scope="module")
def line(ut, sid) -> dict:
    t = made(ut, sid, speak_inputs(), SPEAK)
    assert (t["kind"], t["audio"]["sample_rate"]) == ("audio", 24000)
    return t


def with_a2(client, video: dict) -> tuple[dict, dict]:
    tl = create(client, take_ids=[video["id"]])
    takes = {video["id"]: facts(video)}
    return edit(client, tl, {"op": "add_track"}, takes), takes


# ---------------------------------------------------------------- audio takes on the audio tracks


def test_an_audio_take_on_an_audio_track(ut, video, line):
    tl, takes = with_a2(ut, video)
    takes[line["id"]] = facts(line)
    before = doc(tl)
    tl = ok(ut.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [line["id"]], "track": "A2"}))
    (m,) = clips(tl, 1)
    want = check.t_apply(
        before, {"op": "add", "take_ids": [line["id"]], "track": "A2", "ids": [m["id"]]}, takes
    )
    assert doc(tl) == want["document"]
    assert (m["take_id"], m["in"], m["out"], m["at"]) == (line["id"], 0, 1.4, 0)
    assert "number" not in m  # shot numbers are V1's
    tl = edit(ut, tl, {"op": "move", "clip": m["id"], "at": 0.26}, takes)
    assert clip(tl, m["id"])["at"] == 6 / 24  # snapped to a frame
    tl = edit(ut, tl, {"op": "trim", "clip": m["id"], "edge": "in", "delta": 0.21}, takes)
    c = clip(tl, m["id"])
    assert (c["in"], c["at"]) == (5 / 24, 11 / 24)  # the start moves with in, the end stays
    tl = edit(ut, tl, {"op": "trim", "clip": m["id"], "edge": "out", "delta": 5.0}, takes)
    assert clip(tl, m["id"])["out"] == 1.4  # never past the audio take's end (on the sample grid)
    tl = edit(ut, tl, {"op": "trim", "clip": m["id"], "edge": "out", "delta": -0.5}, takes)
    c = clip(tl, m["id"])
    assert c["out"] < 1.4 and round(c["out"] * 24000) == pytest.approx(c["out"] * 24000, abs=1e-6)
    # never on V1: neither added nor written
    r = ut.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [video["id"], line["id"]]})
    assert (r.status_code, r.json()["detail"]) == (
        422,
        "take_ids[1]: an audio take goes on an audio track",
    )
    written = doc(tl)
    v1 = written["tracks"][0]
    v1["clips"].append({"id": "v", "take_id": line["id"], "in": 0, "out": 1})
    assert ["wrong_track", "V1", "v"] in problems(put(ut, tl, written, 422))
    assert get(ut, tl)["revision"] == tl["revision"]


def test_the_export_plan_lists_sounds(ut, video, line):
    tl, takes = with_a2(ut, video)
    takes[line["id"]] = facts(line)
    tl = ok(ut.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [line["id"]], "track": "A2"}))
    v, (m,) = clips(tl)[0], clips(tl, 1)
    p = plan(ut, tl)
    assert p["sounds"] == [
        {"clip": v["id"], "track": "V1", "sample_rate": None, "channels": None},  # a video's sound
        {"clip": m["id"], "track": "A2", "sample_rate": 24000, "channels": 1},
    ]
    assert p["stream_copy"] is True  # sound never stops a copy: it is always re-encoded
    tl = edit(ut, tl, {"op": "set", "clip": m["id"], "mute": True}, takes)
    assert [s["clip"] for s in plan(ut, tl)["sounds"]] == [v["id"]]
    tl = edit(ut, tl, {"op": "set", "clip": m["id"], "mute": False}, takes)
    tl = edit(ut, tl, {"op": "track", "track": "A2", "mute": True}, takes)
    assert [s["clip"] for s in plan(ut, tl)["sounds"]] == [v["id"]]


# ---------------------------------------------------------------- New line


def test_new_line_fills_its_placeholder(ut, video):
    tl, _ = with_a2(ut, video)
    ans = new_line(
        ut, tl, {"track": "A2", "at": 0.51, "length": 1.0, "id": "line1"}, line_run("New line one.")
    )
    run, cur = ans["run"], ans["timeline"]
    ph = clip(cur, "line1")
    assert [c["id"] for c in clips(cur, 1)] == ["line1"]
    assert (ph["take_id"], ph["at"], ph["hold"], ph["shot"]["run_id"]) == (
        None,
        0.5,
        1.0,
        run["id"],
    )
    assert cur["revision"] == tl["revision"] + 1 and run["session_id"] == tl["session_id"]
    done = wait_clip(ut, cur, "line1", "the line's take in its placeholder")
    take = take_of_run(ut, run["id"])
    assert take["kind"] == "audio" and take["session_id"] == tl["session_id"]
    want = check.t_apply(no_shots(doc(cur)), {"op": "fill", "clip": "line1", "take_id": take["id"]},
                         all_facts(ut, done))  # fmt: skip
    assert doc(done) == want["document"]
    c = clip(done, "line1")
    assert (c["take_id"], c["in"], c["out"], c["at"], c["alternatives"]) == (
        take["id"], 0, take["audio"]["duration_s"], 0.5, [take["id"]])  # fmt: skip
    assert done["revision"] == cur["revision"] + 1  # studio's fill
    # Generate from this clip: the shot on the clip, its take kept until the new one arrives
    redo = new_line(ut, done, {"clip": "line1"}, line_run("New line two."))
    rc = clip(redo["timeline"], "line1")
    assert (rc["take_id"], rc["shot"]["run_id"]) == (take["id"], redo["run"]["id"])
    assert (
        refused(ut, done, {"clip": "line1"}, line_run("again"), 409)
        == "clip line1 already has a line running"
    )
    done = wait_clip(ut, redo["timeline"], "line1", "the redo's take")
    second = take_of_run(ut, redo["run"]["id"])["id"]
    assert (clip(done, "line1")["take_id"], clip(done, "line1")["alternatives"]) == (
        second,
        [take["id"], second],
    )


def test_new_line_refusals(ut, video, line):
    tl, _ = with_a2(ut, video)
    tl = ok(ut.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [line["id"]], "track": "A2"}))
    v, (m,) = clips(tl)[0], clips(tl, 1)
    slot = {"track": "A2", "at": 1.5, "length": 0.5}
    echo = {"workflow_id": "echo", "inputs": {"prompt.text": "a picture", "echo.steps": 2}}
    for s, run, want in (
        (slot, echo, "New line needs a workflow whose media is audio"),
        (slot | {"track": "A4"}, line_run(), "unknown track A4"),
        ({"clip": "nope"}, line_run(), "unknown clip nope"),
        ({"clip": v["id"]}, line_run(), f"clip {v['id']} is not on an audio track"),
        (
            slot,
            {"workflow_id": SPEAK, "inputs": {"prompt.text": TEXT}},
            "speak.voice: a voice is required",
        ),
    ):
        assert refused(ut, tl, s, run) == want, s
    r = ut.post(f"{TL}/{tl['id']}/lines", json={"slot": slot | {"track": "V1"}, "run": line_run()})
    assert r.status_code == 422 and "detail" in r.json(), r.text  # outside the schema (A2-A9)
    r = ut.post(f"{TL}/{tl['id']}/lines", json={"slot": {"track": "A2", "at": 0.5, "length": 1, "id": "ov"},
                                               "run": line_run()})  # fmt: skip
    assert r.status_code == 422 and ["overlap", "A2", "ov"] in problems(r.json()), r.text
    assert get(ut, tl)["revision"] == tl["revision"]  # nothing stored
    assert clip(get(ut, tl), m["id"])["take_id"] == line["id"]


def test_a_cancelled_line_is_removed(ut, video):
    tl, _ = with_a2(ut, video)
    ans = new_line(
        ut, tl, {"track": "A2", "at": 0, "length": 1.5, "id": "gone"}, line_run("x" * 150, 5000)
    )
    ok(ut.post(f"{STUDIO}/runs/{ans['run']['id']}/cancel"), 202)
    assert wait_run(ut, ans["run"]["id"])["status"] == "cancelled"
    cur = wait_for(
        lambda: (x := get(ut, tl)) and not clips(x, 1) and x, what="the placeholder removed"
    )
    assert clips(cur, 1) == [] and [c["id"] for c in clips(cur)] == [c["id"] for c in clips(tl)]


# ---------------------------------------------------------------- Make shot


def test_make_shot_from_an_audio_clip(ut, video, line, live_wfs):
    takes = {video["id"]: facts(video), line["id"]: facts(line)}
    tl = create(ut, take_ids=[video["id"]])
    tl = edit(
        ut, tl, {"op": "insert", "at": 60, "hold": 4, "id": "g"}, takes
    )  # an empty slot 2.04-6.04 s
    tl = edit(ut, tl, {"op": "add_track"}, takes)
    tl = ok(ut.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [line["id"]], "track": "A2"}))
    (m,) = clips(tl, 1)
    tl = edit(ut, tl, {"op": "move", "clip": m["id"], "at": 3.0}, takes)
    wfs = [{k: v for k, v in live_wfs[A2V].items() if v is not None}]
    pal = wfs[0]["node_schemas"]
    got = ok(ut.get(f"{TL}/{tl['id']}/clips/{m['id']}/actions"))["actions"]
    want = check.make_shot(
        doc(tl), m["id"], {k: v | {"id": k} for k, v in takes.items()}, wfs, pal
    )["actions"]
    assert [a for a in got if a["workflow_id"] == A2V] == want
    (o,) = want
    assert o["inputs"]["a2v.source"] == {"take_id": line["id"], "in": 0, "out": 1.4}
    assert o["slot"] == {"at": 3.0, "length": 1.4, "exact": True}
    assert all(a["slot"]["exact"] for a in got)
    assert ok(ut.get(f"{TL}/{tl['id']}/clips/g/actions"))["actions"] == []  # an empty slot: none
    body = {
        "slot": o["slot"] | {"id": "ms"},
        "run": {"workflow_id": A2V, "inputs": FAST_A2V | o["inputs"]},
    }
    ans = ok(ut.post(f"{TL}/{tl['id']}/shots", json=body), 202)
    ids = [c["id"] for c in clips(ans["timeline"])]
    assert (
        ids[:3] == [clips(tl)[0]["id"], "g", "ms"] and len(ids) == 4
    )  # the gap split around the slot
    place = {"op": "place", "at": 3.0, "hold": 1.4, "id": "ms", "gap_id": ids[3]}
    assert no_shots(doc(ans["timeline"])) == check.t_apply(doc(tl), place, takes)["document"]
    assert clip(ans["timeline"], "ms")["shot"]["run_id"] == ans["run"]["id"]
    assert clip(ans["timeline"], "ms")["at"] == 3.0
    done = wait_clip(ut, ans["timeline"], "ms", "the shot made from the line")
    shot = ok(ut.get(f"{STUDIO}/takes/{clip(done, 'ms')['take_id']}"))
    assert shot["kind"] == "video" and shot["parents"] == [line["id"]]
    assert shot["made_with"]["inputs"]["a2v.source"]["take_id"] == line["id"]
    assert clip(done, "ms")["at"] == 3.0
