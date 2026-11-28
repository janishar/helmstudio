"""Audio fades, crossfades and ducking (API 1.106, plan 41; contracts/README.md "Audio fades, crossfades and
ducking"): through Kong in fake mode, on the Timeline a clip's fades, an audio clip's crossfade that blends (its take
has sound past the cut) and one that dips (with the crossfade_dip warning), the ducking op, their 422s, the diff and
the agent view, every PUT checked against check.py's t_apply; an export of all of it (skipped with no live env that
has ffmpeg); in Magic a music block's fades, the crossfade kind (its 422s), the board's ducking, a cue that runs with
its tail (MagicBlockRun.tail, the run's seconds), the join's blend or dip (the agent view, the edit's warning), the
cue out of date once the crossfade outgrows its tail, a morph between sound blocks skipped, and Send carrying it all
onto the sequence. Every JSON answer is checked against openapi.yaml with CONTRACT_SCHEMAS=1. No GPU."""

import pytest
from conftest import headers, make_client
from helpers import STUDIO, new_session, ok, start_run, take_of_run, wait_for, wait_run
from test_magic import BOARDS
from test_timeline import TL, VIDEO, clips, create, edit, facts, get, plan, video_inputs, wait_job

MUSIC, SPEAK = "echo-music", "echo-voice-over"


@pytest.fixture(scope="module", autouse=True)
def live_wfs(api) -> None:
    listed = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if not {MUSIC, SPEAK, VIDEO} <= listed:
        pytest.skip("the fake runner has no echo-music / echo-voice-over / echo-video workflow")


@pytest.fixture(scope="module")
def uf(new_user):
    """User F: this module's sequences and boards."""
    with make_client(headers(new_user("AF")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def made(uf) -> dict:
    """A 4 s video, music takes of 4 s and 2 s, and a voice line (1.4 s)."""
    sid = new_session(uf, "Contract audio fades")["id"]

    def run(inputs: dict, wid: str) -> dict:
        r = start_run(uf, sid, inputs, wid)
        assert wait_run(uf, r["id"])["status"] == "succeeded"
        return take_of_run(uf, r["id"])

    music = lambda s: run(
        {"prompt.text": "soft rain", "music.seconds": s, "music.delay_ms": 0}, MUSIC
    )
    out = {"video": run(video_inputs(frames=97, text="a pier at dusk"), VIDEO), "long": music(4), "short": music(2),
           "line": run({"prompt.text": "Back by noon, I said.", "speak.voice": {"voice_id": "echo/ada"},
                        "speak.delay_ms": 50}, SPEAK)}  # fmt: skip
    assert (out["long"]["sound"], out["line"]["sound"]) == ("music", "voice")
    return out


def edits(client, tl: dict, *ops: dict, status: int = 200, dry: bool = False) -> dict:
    body = {"base_revision": tl["revision"], "ops": list(ops)} | ({"dry_run": True} if dry else {})
    return ok(client.post(f"{TL}/{tl['id']}/edits", json=body), status)


def sequence(uf, made: dict, first: str) -> tuple[dict, dict]:
    """The 4 s video on V1, then A2 with two music clips touching at 2 s: `first` (long: trimmed to 2 s, with 2 s of
    sound past its cut; short: the whole 2 s take) and the short take after it; A3 with the line at 1 s."""
    takes = {t["id"]: facts(t) for t in made.values()}
    tl = create(uf, take_ids=[made["video"]["id"]])
    trim = [{"op": "trim", "clip": "m1", "edge": "out", "delta": -2}] if first == "long" else []
    tl = edits(uf, tl, {"op": "add_track"}, {"op": "add_track"},
               {"op": "add", "track": "A2", "take_ids": [made[first]["id"]], "ids": ["m1"]}, *trim,
               {"op": "add", "track": "A2", "take_ids": [made["short"]["id"]], "ids": ["m2"]},
               {"op": "add", "track": "A3", "take_ids": [made["line"]["id"]], "ids": ["vo"]},
               {"op": "move", "clip": "vo", "at": 1})["timeline"]  # fmt: skip
    return tl, takes


def test_timeline_fades_crossfades_and_ducking(uf, made):
    tl, takes = sequence(uf, made, "long")
    m1, m2 = (c for c in clips(tl, 1))
    assert (m1["out"], m2["at"]) == (2, 2)
    # fades and a crossfade that blends (the 4 s take has 2 s past m1's cut), each PUT as check.py says
    tl = edit(uf, tl, {"op": "set", "clip": "m1", "fade_in": 0.5, "crossfade": 1}, takes)
    tl = edit(uf, tl, {"op": "set", "clip": clips(tl)[0]["id"], "fade_out": 0.51}, takes)
    assert clips(tl)[0]["fade_out"] == 0.5  # snapped to the frame grid
    tl = edit(uf, tl, {"op": "ducking", "on": True}, takes)
    assert tl["ducking"] == {"on": True, "db": -12}
    # the same through /edits: the diff, no dip warning, then the amount
    r = edits(
        uf, tl, {"op": "set", "clip": "m2", "fade_out": 12 / 24}, {"op": "ducking", "db": -18}
    )
    assert r["warnings"] == [] and r["diff"]["ducking"] == [
        {"on": True, "db": -12},
        {"on": True, "db": -18},
    ]
    assert r["diff"]["changed"] == [{"clip": "m2", "fields": {"fade_out": [None, 0.5]}}]
    tl = r["timeline"]
    assert tl["takes"][made["long"]["id"]]["sound"] == "music"
    view = ok(uf.get(f"{TL}/{tl['id']}", params={"view": "agent"}))
    by = {c["id"]: c for c in view["clips"]}
    assert (by["m1"]["fade_in"], by["m1"]["crossfade"], by["m1"]["crossfade_mode"], by["m1"]["sound"]) == (
        0.5, 1, "blend", "music")  # fmt: skip
    assert by["vo"]["sound"] == "voice" and view["ducking"] == {"on": True, "db": -18}
    # 0 or null removes; a crossfade on V1, a fade past 10 s and a ducking amount out of range are refused
    r = edits(uf, tl, {"op": "set", "clip": "m1", "fade_in": 0, "crossfade": None}, dry=True)
    assert r["diff"]["changed"] == [
        {"clip": "m1", "fields": {"fade_in": [0.5, None], "crossfade": [1, None]}}
    ]
    v = clips(tl)[0]["id"]
    r = edits(uf, tl, {"op": "set", "clip": v, "crossfade": 1}, status=422)
    assert r["code"] == "crossfade_track" and r["op_index"] == 0
    assert edits(uf, tl, {"op": "set", "clip": "m1", "fade_in": 12}, status=422)["detail"] == (
        "ops[0]: fade_in and fade_out must be seconds from 0 to 10")  # fmt: skip
    assert (
        edits(uf, tl, {"op": "ducking", "db": -30}, status=422)["detail"]
        == "ops[0]: db must be from -24 to -3"
    )
    # a crossfade goes when its clip no longer touches the next
    r = edits(uf, tl, {"op": "move", "clip": "m2", "at": 49 / 24}, dry=True)
    assert {"clip": "m1", "fields": {"crossfade": [1, None]}} in r["diff"]["changed"]


def test_a_crossfade_with_no_handle_dips(uf, made):
    tl, _ = sequence(uf, made, "short")
    r = edits(uf, tl, {"op": "set", "clip": "m1", "crossfade": 1})
    assert r["warnings"] == ["crossfade_dip: m1 has no sound past its cut: it dips"]
    view = ok(uf.get(f"{TL}/{tl['id']}", params={"view": "agent"}))
    assert next(c for c in view["clips"] if c["id"] == "m1")["crossfade_mode"] == "dip"
    assert "ducking" not in r["timeline"]  # a sequence without it: off


def test_export_with_fades_crossfades_and_ducking(uf, made):
    tl, _ = sequence(uf, made, "long")
    tl = edits(uf, tl, {"op": "set", "clip": "m1", "fade_in": 0.5, "crossfade": 1},
               {"op": "set", "clip": "m2", "crossfade": 1, "fade_out": 0.5},
               {"op": "set", "clip": clips(tl)[0]["id"], "fade_in": 0.25, "fade_out": 0.5},
               {"op": "ducking", "on": True, "db": -12})["timeline"]  # fmt: skip
    p = plan(uf, tl)
    if p["host"] is None:
        pytest.skip(
            "no live env advertises media (ffmpeg): the Docker fake runner's image has none"
        )
    job = ok(uf.post(f"{TL}/{tl['id']}/export", json={"revision": tl["revision"]}), 202)
    done = wait_job(uf, job["id"])
    assert done["status"] == "succeeded", done
    take = ok(uf.get(f"{STUDIO}/takes/{done['take_id']}"))
    assert (
        take["video"]["has_audio"] and abs(take["video"]["duration_s"] - p["duration_s"]) <= 1 / 24
    )


# ---------------------------------------------------------------- Magic


def cue(bid: str, at: int, name: str) -> dict:
    return {"id": bid, "lane": "music", "at": at, "length": 48, "kind": "music", "name": name,
            "request": {"kind": "music", "prompt": "soft rain, piano", "advanced": {"music.delay_ms": 0}},
            "takes": [], "effects": []}  # fmt: skip


def board(uf) -> tuple[dict, str]:
    d = {"target": {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
         "lanes": [{"id": "shots", "name": "Shots", "role": "shots"}, {"id": "music", "name": "Music", "role": "music"}],
         "blocks": [{"id": "s1", "lane": "shots", "at": 0, "length": 96, "kind": "video", "name": "Shot 1",
                     "request": {"kind": "video", "model": "echo-image-to-video", "prompt": "a pier at dusk"},
                     "takes": [], "effects": []}, cue("m1", 0, "Soft rain theme"), cue("m2", 48, "Storm")],
         "links": [], "transitions": [], "assets": [], "notes": []}  # fmt: skip
    b = ok(uf.post(BOARDS, json={"name": "Fades", "doc": d}), 201)
    return b, f"{BOARDS}/{b['id']}"


def bedit(uf, base: str, rev: int, *ops: dict, status: int = 200) -> dict:
    return ok(uf.post(f"{base}/edits", json={"base_revision": rev, "ops": list(ops)}), status)


def settled(uf, base: str, *blocks: str) -> dict:
    return wait_for(lambda: (b := ok(uf.get(base))) and all(b["status"][x]["status"] == "done" for x in blocks) and b,
                    what=f"{blocks}' takes")  # fmt: skip


def test_magic_fades_crossfades_and_ducking(uf):
    assert ok(uf.post(BOARDS), 201)["doc"]["ducking"] == {
        "on": True,
        "db": -12,
    }  # a new board ducks
    _, base = board(uf)
    r = bedit(
        uf,
        base,
        1,
        {"op": "block", "id": "m1", "set": {"fade_in": 1.5}},
        {"op": "transition", "between": ["m1", "m2"], "kind": "crossfade"},
        {"op": "ducking", "db": -18},
    )
    d = r["board"]["doc"]
    assert d["blocks"][1]["fade_in"] == 1.5 and d["ducking"] == {"on": True, "db": -18}
    (x,) = [t for t in d["transitions"] if t["kind"] == "crossfade"]
    assert x["length"] == 48 and r["warnings"] == []
    rev = r["revision"]
    for op, why in (({"op": "block", "id": "s1", "set": {"fade_in": 1}}, "fade_in is for voice, music and sound blocks"),
                    ({"op": "transition", "start": "music", "kind": "crossfade"}, "A crossfade joins two music or sound blocks.")):  # fmt: skip
        assert bedit(uf, base, rev, op, status=422) == {"detail": why, "op_index": 0}
    # the cue before the crossfade runs 2 s longer and records it
    started = ok(uf.post(f"{base}/run", json={"revision": rev, "blocks": ["s1", "m1", "m2"]}), 202)
    run = {s["block"]: s["run_id"] for s in started["started"]}
    assert ok(uf.get(f"{STUDIO}/runs/{run['m1']}"))["inputs"]["music.seconds"] == 4
    got = settled(uf, base, "s1", "m1", "m2")
    assert next(h for h in got["runs"] if h["block"] == "m1")["tail"] == 2
    view = ok(uf.get(base, params={"view": "agent"}))
    assert next(t for t in view["transitions"] if t["id"] == x["id"])["mode"] == "blend"
    assert view["ducking"] == {"on": True, "db": -18}
    # Send carries the fades, the crossfade and the ducking onto the sequence
    sent = ok(uf.post(f"{base}/send", json={"revision": got["revision"], "dry_run": True}))["plan"]
    assert {"transition": x["id"], "kind": "crossfade", "how": "crossfade"} in sent["joins"]
    s = ok(uf.post(f"{base}/send", json={"revision": got["revision"]}), 201)["send"]
    tl = get(uf, s["timeline_id"])
    (m,) = [c for c in tl["tracks"][1]["clips"] if c["at"] == 0]
    assert (m["fade_in"], m["crossfade"], tl["ducking"]) == (1.5, 2, {"on": True, "db": -18})
    # a longer crossfade outgrows the tail: the cue is out of date
    r = bedit(
        uf,
        base,
        got["revision"],
        {"op": "transition", "between": ["m1", "m2"], "kind": "crossfade", "length": 72},
    )
    assert r["board"]["status"]["m1"]["status"] == "out_of_date"
    assert r["warnings"] == ["Soft rain theme was made before the crossfade: it dips instead."]
    # a morph between two sound blocks never runs
    r = bedit(
        uf, base, r["revision"], {"op": "transition", "between": ["m1", "m2"], "kind": "morph"}
    )
    (t,) = [t for t in r["board"]["doc"]["transitions"] if t["kind"] == "morph"]
    p = ok(uf.post(f"{base}/plan", json={"blocks": [t["id"]]}))
    assert p["blocks"][0]["reason"] == "A morph is for shots."


def test_a_cue_made_before_the_crossfade_dips(uf):
    _, base = board(uf)
    ok(uf.post(f"{base}/run", json={"revision": 1, "blocks": ["m1"]}), 202)
    got = settled(uf, base, "m1")
    r = bedit(
        uf,
        base,
        got["revision"],
        {"op": "transition", "between": ["m1", "m2"], "kind": "crossfade"},
    )
    assert r["warnings"] == ["Soft rain theme was made before the crossfade: it dips instead."]
    assert (
        r["board"]["status"]["m1"]["status"] == "done"
    )  # made with no tail: it stays done and dips
    view = ok(uf.get(base, params={"view": "agent"}))
    assert [t["mode"] for t in view["transitions"]] == ["dip"]
