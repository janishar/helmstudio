"""Magic's standard request and runs (API 1.81, plan 39 phase D; contracts/README.md "Magic: the standard request",
"Magic: runs"): through Kong in fake mode, the kinds with the echo models and the greyed fields, the settings, a
board's plan (the pack's rate, the ask rule), a run of a block's standard request in the board's own session (hidden
from the sessions list) that becomes a take on its block with the status done, a request changed since making it
out of date, the run history, and user B 404 on another user's board's run and plan. Every JSON answer is checked
against openapi.yaml with CONTRACT_SCHEMAS=1. No GPU."""

import pytest
from conftest import headers, make_client
from helpers import STUDIO, ok, wait_for
from test_magic import BOARDS

KINDS = f"{STUDIO}/magic/kinds"
SETTINGS = f"{STUDIO}/magic/settings"


@pytest.fixture(scope="module")
def ur(new_user):
    """User R: no runs of echo-video before this module's, so a plan's rate is the pack's (user A's earlier runs
    in the suite, test_listen's for one, would make it "yours")."""
    with make_client(headers(new_user("R")["access_token"])) as c:
        yield c


def shot(bid: str, at: int, prompt: str) -> dict:
    return {
        "id": bid,
        "lane": "shots",
        "at": at,
        "length": 24,
        "kind": "video",
        "name": f"Shot {bid[1:]}",
        "request": {
            "kind": "video",
            "model": "echo-video",
            "prompt": prompt,
            "advanced": {"video.delay_ms": 0},
        },
        "takes": [],
        "effects": [],
    }


def run_doc() -> dict:
    return {
        "target": {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
        "lanes": [
            {"id": "shots", "name": "Shots", "role": "shots"},
            {"id": "music", "name": "Music", "role": "music"},
        ],
        "blocks": [
            shot("s1", 0, "a fox in the rain"),
            {
                "id": "m1",
                "lane": "music",
                "at": 0,
                "length": 48,
                "kind": "music",
                "name": "Cue 1",
                # API 1.85: music runs; this cue names a model the fake host has not got (skipped with why)
                "request": {"kind": "music", "model": "ace-step-music"},
                "takes": [],
                "effects": [],
            },
        ],
        "links": [],
        "transitions": [],
        "assets": [],
        "notes": [],
    }


def test_kinds_and_settings(api):
    kinds = {k["kind"]: k for k in ok(api.get(KINDS))["kinds"]}
    assert list(kinds) == ["video", "image", "voice", "music", "sound", "frame"]
    video = {m["workflow_id"]: m for m in kinds["video"]["models"]}
    assert video["echo-video"]["name"] == "Echo video"
    assert video["echo-video"]["greyed"]["first"] == "Echo video can't take a first frame"
    assert "first" in video["echo-image-to-video"]["fields"]
    assert any(p["path"] == "video.delay_ms" for p in video["echo-video"]["advanced"])
    assert [m["workflow_id"] for m in kinds["image"]["models"]] == ["echo"]
    assert [m["workflow_id"] for m in kinds["voice"]["models"]] == ["echo-voice-over"]
    assert [m["workflow_id"] for m in kinds["music"]["models"]] == ["echo-music"]
    assert "off" not in kinds["music"]
    assert ok(api.get(SETTINGS)) == {"ask_above_gpu_minutes": 10}
    assert ok(api.put(SETTINGS, json={"ask_above_gpu_minutes": 2.5})) == {
        "ask_above_gpu_minutes": 2.5
    }
    assert ok(api.get(SETTINGS)) == {"ask_above_gpu_minutes": 2.5}
    assert api.put(SETTINGS, json={"ask_above_gpu_minutes": -1}).status_code == 422
    ok(api.put(SETTINGS, json={"ask_above_gpu_minutes": 10}))


def test_a_block_runs_into_a_take(ur, api_b):
    b = ok(ur.post(BOARDS, json={"name": "Runs", "doc": run_doc()}), 201)
    base = f"{BOARDS}/{b['id']}"
    assert b["session_id"] is None and b["status"]["s1"] == {"status": "draft"}

    plan = ok(ur.post(f"{base}/plan", json={"stale": True}))
    assert plan["order"] == ["s1", "m1"] and plan["limit_minutes"] == 10
    s1, m1 = plan["blocks"]
    assert s1["source"] == "pack" and s1["seconds"] == 0.5 and s1["host"]
    assert m1["reason"] == "its model ace-step-music is on no host"
    assert plan["ask"] is False and plan["ask_text"] == "1 shot, about 1 s"

    r = ur.post(f"{base}/run", json={"revision": 1, "blocks": ["m1"]})
    assert (
        r.status_code == 422
        and r.json()["detail"] == "Cue 1: its model ace-step-music is on no host"
    )
    assert ur.post(f"{base}/run", json={"revision": 9, "blocks": ["s1"]}).status_code == 409
    for path, body in (("run", {"revision": 1, "stale": True}), ("plan", {"stale": True})):
        r = api_b.post(f"{base}/{path}", json=body)
        assert r.status_code == 404 and r.json() == {"detail": "not found"}

    started = ok(ur.post(f"{base}/run", json={"revision": 1, "lane": "shots"}), 202)
    assert [s["block"] for s in started["started"]] == ["s1"] and started["skipped"] == []
    session = started["board"]["session_id"]
    assert session and all(s["id"] != session for s in ok(ur.get(f"{STUDIO}/sessions"))["sessions"])
    run_id = started["started"][0]["run_id"]
    run = ok(ur.get(f"{STUDIO}/runs/{run_id}"))
    assert run["workflow_id"] == "echo-video" and run["session_id"] == session
    assert run["inputs"]["prompt.text"] == "a fox in the rain"
    assert (run["inputs"]["canvas.width"], run["inputs"]["canvas.height"]) == (1024, 576)

    def done():
        board = ok(ur.get(base))
        return board if board["status"]["s1"]["status"] == "done" else None

    board = wait_for(done, what="the shot's take")
    take = board["doc"]["blocks"][0]["takes"][0]
    assert board["takes"][take]["run_id"] == run_id and board["takes"][take]["url"]
    history = [x for x in board["runs"] if x["block"] == "s1"]
    assert [(x["status"], x["takes"], x["current"], x["by"]) for x in history] == [
        ("succeeded", [take], True, "user")
    ]
    # a request changed since: out of date, the take kept
    req = dict(board["doc"]["blocks"][0]["request"], prompt="a fox in heavier rain")
    edited = ok(
        ur.post(
            f"{base}/edits",
            json={
                "base_revision": board["revision"],
                "ops": [{"op": "block", "id": "s1", "set": {"request": req}}],
            },
        )
    )
    assert edited["board"]["status"]["s1"] == {"status": "out_of_date"}
    assert edited["board"]["runs"][0]["current"] is False
    agent = ok(ur.get(f"{base}?view=agent"))
    assert {x["id"]: x["status"] for x in agent["blocks"]} == {"s1": "out_of_date", "m1": "draft"}


def test_a_dialogue_runs_a_line_per_turn(api):
    """API 1.89: a dialogue's turns run one each on the speaker's voice; each turn has its status and take; a line
    changed puts only its turn out of date and the next Run runs only it; Run on and Loop are not inputs."""
    doc = run_doc()
    doc["lanes"] = [{"id": "lines", "name": "Lines", "role": "voice"}]
    doc["assets"] = [
        {
            "id": "a1",
            "kind": "character",
            "handle": "mara",
            "name": "Mara",
            "sheet": [],
            "voice_id": "echo/ada",
        },
        {
            "id": "a2",
            "kind": "character",
            "handle": "eli",
            "name": "Eli",
            "sheet": [],
            "voice_id": "echo/ada",
        },
    ]
    turns = [
        {"speaker": "a1", "text": "Is that the last bus?"},
        {"speaker": "a2", "text": "It was."},
    ]
    doc["blocks"] = [{
        "id": "d1", "lane": "lines", "at": 0, "length": 96, "kind": "voice", "name": "Dialogue 1",
        "request": {"kind": "voice", "turns": turns, "advanced": {"speak.delay_ms": 0}},
        "takes": [], "effects": [],
    }]  # fmt: skip
    b = ok(api.post(BOARDS, json={"name": "Dialogue", "doc": doc}), 201)
    base = f"{BOARDS}/{b['id']}"
    assert b["status"]["d1"]["status"] == "draft" and len(b["status"]["d1"]["turns"]) == 2

    started = ok(api.post(f"{base}/run", json={"revision": 1, "blocks": ["d1"]}), 202)
    assert len(started["started"]) == 2
    inputs = [ok(api.get(f"{STUDIO}/runs/{s['run_id']}"))["inputs"] for s in started["started"]]
    assert [i["prompt.text"] for i in inputs] == ["Is that the last bus?", "It was."]
    assert inputs[0]["speak.voice"] == {"voice_id": "echo/ada"}

    def done():
        board = ok(api.get(base))
        return board if board["status"]["d1"]["status"] == "done" else None

    board = wait_for(done, what="the dialogue's takes")
    st = board["status"]["d1"]
    assert [t["status"] for t in st["turns"]] == ["done", "done"]
    assert sorted(r["turn"] for r in board["runs"]) == [0, 1]
    assert board["takes"][st["turns"][0]["take"]]["duration_s"] > 0

    req = dict(board["doc"]["blocks"][0]["request"], host="mac", loop=True)
    req["turns"] = [turns[0], dict(turns[1], text="It was, yes.")]
    edited = ok(api.post(f"{base}/edits", json={"base_revision": board["revision"],
                                                "ops": [{"op": "block", "id": "d1", "set": {"request": req}}]}))  # fmt: skip
    st = edited["board"]["status"]["d1"]
    assert st["status"] == "out_of_date" and [t["status"] for t in st["turns"]] == [
        "done",
        "out_of_date",
    ]
    req.pop("host")
    edited = ok(api.post(f"{base}/edits", json={"base_revision": edited["board"]["revision"],
                                                "ops": [{"op": "block", "id": "d1", "set": {"request": req}}]}))  # fmt: skip
    again = ok(
        api.post(f"{base}/run", json={"revision": edited["board"]["revision"], "blocks": ["d1"]}),
        202,
    )
    assert len(again["started"]) == 1
    assert (
        ok(api.get(f"{STUDIO}/runs/{again['started'][0]['run_id']}"))["inputs"]["prompt.text"]
        == "It was, yes."
    )
