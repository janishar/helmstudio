"""Magic's Music and Sound kinds (API 1.85, plan 39 phase H; contracts/README.md "Magic: music and sound"): through
Kong in fake mode, the kinds with Echo music and Echo sound (its video workflow as `video`), a music block's request
run with its lyrics, BPM and seconds into a music take, a sound made from a shot's video run on the shot's take that
keeps only the audio take (the video with the sound stays in the Gallery), and the sound out of date once the shot
has a new take. Every JSON answer is checked against openapi.yaml with CONTRACT_SCHEMAS=1. No GPU."""

from helpers import STUDIO, ok, wait_for
from test_magic import BOARDS
from test_magic_runs import KINDS, shot


def test_music_and_a_sound_from_a_shot(api):
    kinds = {k["kind"]: k for k in ok(api.get(KINDS))["kinds"]}
    assert kinds["music"]["fields"] == ["prompt", "lyrics", "length", "key", "bpm", "seed"]
    music = kinds["music"]["models"][0]
    assert music["workflow_id"] == "echo-music" and music["greyed"] == {
        "key": "Echo music can't take a key"
    }
    sound = kinds["sound"]["models"][0]
    assert sound["workflow_id"] == "echo-sound"
    assert sound["video"]["workflow_id"] == "echo-sound-video" and "from" in sound["fields"]

    doc = {
        "target": {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
        "lanes": [
            {"id": "shots", "name": "Shots", "role": "shots"},
            {"id": "music", "name": "Music", "role": "music"},
            {"id": "sound", "name": "Sound", "role": "sound"},
        ],
        "blocks": [
            # Echo keyframes (not Echo video, whose estimate test_magic_runs reads as the pack's)
            dict(
                shot("s1", 0, "a bus in the rain"),
                request={
                    "kind": "video",
                    "model": "echo-image-to-video",
                    "prompt": "a bus in the rain",
                },
            ),
            {
                "id": "m1",
                "lane": "music",
                "at": 0,
                "length": 48,
                "kind": "music",
                "name": "Cue 1",
                "request": {
                    "kind": "music",
                    "prompt": "soft rain, piano",
                    "lyrics": "[verse]\nla la",
                    "bpm": 72,
                    "advanced": {"music.delay_ms": 0},
                },
                "takes": [],
                "effects": [],
            },
            {
                "id": "x1",
                "lane": "sound",
                "at": 0,
                "length": 24,
                "kind": "sound",
                "name": "Bus",
                "request": {
                    "kind": "sound",
                    "from": "s1",
                    "prompt": "a bus pulls away",
                    "advanced": {"foley.delay_ms": 0},
                },
                "takes": [],
                "effects": [],
            },
        ],
        "links": [{"block": "x1", "to": ["s1"]}],
        "transitions": [],
        "assets": [],
        "notes": [],
    }
    b = ok(api.post(BOARDS, json={"name": "Music", "doc": doc}), 201)
    base = f"{BOARDS}/{b['id']}"
    # the sound needs its shot's take first
    plan = ok(api.post(f"{base}/plan", json={"blocks": ["x1"]}))
    assert plan["blocks"][0]["reason"] == "Shot 1 has no take yet"

    def status(board, block):
        return board["status"][block]["status"]

    started = ok(api.post(f"{base}/run", json={"revision": 1, "blocks": ["s1", "m1"]}), 202)
    runs = {s["block"]: s["run_id"] for s in started["started"]}
    m = ok(api.get(f"{STUDIO}/runs/{runs['m1']}"))
    assert m["workflow_id"] == "echo-music"
    assert {
        k: m["inputs"][k]
        for k in ("music.lyrics", "music.instrumental", "music.bpm", "music.seconds")
    } == {
        "music.lyrics": "[verse]\nla la",
        "music.instrumental": False,
        "music.bpm": 72,
        "music.seconds": 2,
    }
    board = wait_for(
        lambda: (
            (x := ok(api.get(base)))
            and status(x, "s1") == "done"
            and status(x, "m1") == "done"
            and x
        ),
        what="the shot's and the cue's takes",
    )
    cue = board["doc"]["blocks"][1]["takes"][0]
    assert board["takes"][cue]["kind"] == "audio" and board["takes"][cue]["sound"] == "music"

    started = ok(
        api.post(f"{base}/run", json={"revision": board["revision"], "blocks": ["x1"]}), 202
    )
    x = ok(api.get(f"{STUDIO}/runs/{started['started'][0]['run_id']}"))
    shot_take = board["doc"]["blocks"][0]["takes"][0]
    assert x["workflow_id"] == "echo-sound-video"
    assert x["inputs"]["foley.source"]["take_id"] == shot_take
    board = wait_for(
        lambda: (y := ok(api.get(base))) and status(y, "x1") == "done" and y,
        what="the sound's take",
    )
    takes = board["doc"]["blocks"][2]["takes"]
    assert len(takes) == 1 and board["takes"][takes[0]]["sound"] == "sound"
    history = [h for h in board["runs"] if h["block"] == "x1"]
    assert [h["takes"] for h in history] == [takes]
    # the run made the video with the sound too: in the Gallery, not on the block
    outputs = ok(api.get(f"{STUDIO}/runs/{history[0]['run_id']}"))["outputs"]
    assert len(outputs) == 2 and {o["kind"] for o in outputs} == {"audio", "video"}

    # a new take of the shot puts the sound out of date
    again = ok(api.post(f"{base}/run", json={"revision": board["revision"], "blocks": ["s1"]}), 202)
    assert again["started"]
    board = wait_for(
        lambda: (z := ok(api.get(base))) and len(z["doc"]["blocks"][0]["takes"]) == 2 and z,
        what="the shot's second take",
    )
    assert status(board, "x1") == "out_of_date"
