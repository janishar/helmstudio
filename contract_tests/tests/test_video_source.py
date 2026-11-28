"""Media inputs and take actions (API 1.19; contracts/README.md "Media inputs" and, under Timeline, "Take actions")
on the fake runner's `echo-retake` (prompt → echo/retake: `source` media accepts video, grid 32, max_frames 321;
`span`), `echo-a2v` (prompt → echo/a2v: `source` media accepts audio, trim, max_s 13.375) and `echo-extend`
(prompt → echo/extend: ltx/extend's frames and source_map): a video take as a
source (lineage, made_with, source_map), every studio 422 text of a media or span value, audio uploads (wav and mp3,
other types, size, duration, bad headers), "Use in…" (GET …/uses and POST …/use) on a video, an audio and an image
take checked against check.py's take_uses and extend_room, runs from the pre-filled
inputs, a batch over a video column, a pipeline echo-video → echo-retake; Timeline: a clip's actions (the range
mapped through a trimmed clip, the 422s, empty for an empty slot or an offline clip), Retake replacing a clip (its
window kept, checked against check.py's t_apply fill), Extend after (the clip grows, later clips ripple, one
revision, undo) and Extend before (inserted at the clip), Insert after, the slot {at} insert an extend before uses,
placeholder states and cancel, Replace among alternatives, undo, Re-render binding a retaken clip to its source's
clip, an audio take refused on V1 (1.32), a video's sound's Make shot offer (1.32), remove_track; isolation.
Off-grid and too-long videos are admin imports with those facts (the fake makes neither). Skipped when the fake runner has no echo-retake / echo-a2v."""

import io
import uuid
import wave

import pytest
from helpers import (
    NO_SUCH_ID,
    STUDIO,
    check,
    new_session,
    ok,
    png_bytes,
    start_run,
    take_of_run,
    upload,
    wait_for,
    wait_run,
)
from test_batches import BATCHES, finish, fresh, rows_of, wait_batch
from test_pipelines import create as create_pipeline
from test_pipelines import inputs_node
from test_pipelines import submit as submit_pipeline
from test_timeline import (
    TL,
    clip,
    clips,
    create,
    doc,
    edit,
    facts,
    get,
    problems,
    put,
    video_inputs,
)
from test_user_workflows import WF

RETAKE, A2V, EXTEND, VIDEO = "echo-retake", "echo-a2v", "echo-extend", "echo-video"
ECHO = [
    "echo",
    "echo-image-to-video",
    "echo-video",
    RETAKE,
    A2V,
    EXTEND,
]  # the echo templates "Use in…" is checked on, in the pack's order (offers list a family's templates so)
FAST = {"prompt.text": "a retake", "retake.delay_ms": 50}
FAST_A2V = {"prompt.text": "a sound to see", "a2v.delay_ms": 50}
FAST_EXT = {"prompt.text": "more of it", "extend.delay_ms": 50}
D49 = 49 / 24  # an echo-video take of 49 frames at 24 fps
IMPORTS = f"{STUDIO}/admin/imports/takes"


# ---------------------------------------------------------------- helpers


def wav_bytes(seconds: float, rate: int = 48000) -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(b"\x00\x00" * round(seconds * rate))
    return buf.getvalue()


# an ID3v2 tag and one MPEG-1 layer III frame (sync 0xFFFB): what complete checks
MP3 = b"ID3\x03\x00\x00\x00\x00\x00\x00" + b"\xff\xfb\x90\x64" + b"\x00" * 413


def wav(client, anon, seconds: float = 3.0, declared: float | None = None) -> dict:
    """An uploaded WAV take; `declared` is the duration_s sent (studio records it as declared)."""
    data = wav_bytes(seconds)
    return upload(client, anon, data, "sound.wav", content_type="audio/wav",
                  duration_s=declared or seconds)  # fmt: skip


def mfacts(take: dict) -> dict:
    """A take as check.py's media_fit / take_uses take it (contracts/fixtures/video/uses.json `takes`)."""
    out = {"id": take["id"], "kind": take["kind"], "width": take["width"], "height": take["height"]}
    if take["kind"] == "video":
        out |= {k: take["video"][k] for k in ("duration_s", "fps", "frames", "has_audio")}
    if take["kind"] == "audio":
        out |= {"duration_s": take["audio"]["duration_s"], "has_audio": True}
    return out


def made(client, sid: str, workflow_id: str, inputs: dict) -> dict:
    """The take of a finished, succeeded run."""
    run = start_run(client, sid, inputs, workflow_id)
    done = wait_run(client, run["id"])
    assert done["status"] == "succeeded", done
    return take_of_run(client, run["id"])


def refused(client, sid: str, workflow_id: str, inputs: dict) -> str:
    r = client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": workflow_id, "inputs": inputs}
    )
    assert r.status_code == 422, f"{inputs}: {r.status_code} {r.text}"
    return r.json()["detail"]


def uses(client, take_id: str) -> list[dict]:
    return ok(client.get(f"{STUDIO}/takes/{take_id}/uses"))["uses"]


def actions(client, tl: dict, cid: str, **rng) -> list[dict]:
    return ok(client.get(f"{TL}/{tl['id']}/clips/{cid}/actions", params=rng))["actions"]


def snap(s: float, fps: float = 24) -> float:
    return check.rnd(s * fps) / fps


def import_video(admin, anon, owner: str, w: int, h: int, frames: int, fps: float, sound: bool):
    """An imported video take with these facts (admin import: the fake runner makes neither an off-grid size nor more
    than 121 frames). Its MP4 is an ftyp box only: never run, only checked at Create."""
    mp4 = b"\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2" + b"\x00" * 64
    poster = png_bytes(w, h)
    body = {"owner_id": owner, "key": f"contract:media:{uuid.uuid4().hex[:12]}", "kind": "video",
            "filename": "clip.mp4", "content_type": "video/mp4", "bytes": len(mp4), "width": w, "height": h,
            "created_at": "2024-03-01T10:00:00Z", "starred": False, "title": f"imported {w}x{h} {frames}f",
            "legacy": {"studio": "contract"}, "parents": [],
            "video": {"duration_s": frames / fps, "fps": fps, "frames": frames, "has_audio": sound,
                      "poster": {"content_type": "image/png", "bytes": len(poster), "width": w, "height": h}}}  # fmt: skip
    t = ok(admin.post(IMPORTS, json=body), 201)
    for link, data, ct in (
        (t["upload_url"], mp4, "video/mp4"),
        (t["poster_upload_url"], poster, "image/png"),
    ):
        r = anon.put(link, content=data, headers={"Content-Type": ct})
        assert r.status_code == 204, f"PUT import: {r.status_code} {r.text}"
    return ok(admin.post(f"{IMPORTS}/{t['take_id']}/complete"))


def all_facts(client, tl: dict, *more: str) -> dict:
    """take id → facts (with source_map) of every take the sequence names, plus `more`, for check.py."""
    ids = set(tl["takes"]) | set(more)
    for c in (c for t in tl["tracks"] for c in t["clips"]):
        ids |= set(c.get("alternatives", []))
    takes = {i: ok(client.get(f"{STUDIO}/takes/{i}")) for i in ids}
    for t in list(takes.values()):  # a map's source is placed by its duration
        if (src := t.get("source_map", {}).get("take_id")) and src not in takes:
            takes[src] = ok(client.get(f"{STUDIO}/takes/{src}"))
    return {i: facts(t) for i, t in takes.items()}


def shot(client, tl: dict, slot: dict, wf: str, inputs: dict) -> dict:
    return ok(client.post(f"{TL}/{tl['id']}/shots",
                          json={"slot": slot, "run": {"workflow_id": wf, "inputs": inputs}}), 202)  # fmt: skip


def filled(client, tl: dict, cid: str, seen: set | None = None) -> dict:
    """Poll until clip cid has a take and no shot, collecting the shot statuses seen."""

    def done():
        cur = get(client, tl)
        c = clip(cur, cid)
        if "shot" in c:
            assert c["shot"].get("status") != "failed", c
            if seen is not None:
                seen.add(c["shot"]["status"])
            return None
        return cur if c["take_id"] else None

    return wait_for(done, what=f"clip {cid} filled")


def offer(offers: list[dict], workflow_id: str) -> dict:
    (o,) = [x for x in offers if x["workflow_id"] == workflow_id]
    return o


# ---------------------------------------------------------------- fixtures


@pytest.fixture(scope="module", autouse=True)
def echo_wfs(api) -> list[dict]:
    """The live echo templates of ECHO, in the pack's order, as check.py's take_uses takes them."""
    listed = [w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]]
    if RETAKE not in listed or A2V not in listed or EXTEND not in listed:
        pytest.skip(
            "the fake runner has no echo-retake / echo-a2v / echo-extend workflow (before API 1.19)"
        )
    docs = [ok(api.get(f"{STUDIO}/workflows/{w}")) for w in ECHO if w in listed]
    return [{k: v for k, v in d.items() if v is not None} for d in docs]


@pytest.fixture(scope="module")
def pal(echo_wfs) -> dict:
    return {k: s for w in echo_wfs for k, s in w["node_schemas"].items()}


@pytest.fixture(scope="module")
def sid(api) -> str:
    return new_session(api, "Contract media inputs")["id"]


@pytest.fixture(scope="module")
def src(api, sid) -> dict:
    """A's echo-video take: 256 × 128, 49 frames at 24 fps, with (silent) sound."""
    return made(api, sid, VIDEO, video_inputs(text="a source to retake"))


@pytest.fixture(scope="module")
def src2(api, sid) -> dict:
    return made(api, sid, VIDEO, video_inputs(text="a second source"))


@pytest.fixture(scope="module")
def sound(api, anon) -> dict:
    """A's 3 s WAV."""
    return wav(api, anon)


@pytest.fixture(scope="module")
def misfits(admin, api, anon, user_a, sound) -> dict:
    """Takes that do not fit: an image, a silent / off-grid / too long video, a 20 s audio, a pending upload, a
    deleted take."""
    owner = user_a["user"]["id"]
    gone = wav(api, anon, 1.0)["id"]
    assert api.delete(f"{STUDIO}/takes/{gone}").status_code == 204
    return {
        "image": upload(api, anon, png_bytes(64, 64))["id"],
        "silent": import_video(admin, anon, owner, 256, 128, 49, 24, False)["id"],
        "offgrid": import_video(admin, anon, owner, 450, 256, 49, 24, True)["id"],
        "long": import_video(admin, anon, owner, 448, 256, 390, 30, True)["id"],
        "audio20": wav(api, anon, 1.0, declared=20.0)["id"],
        "pending": upload(api, anon, png_bytes(8, 8), complete=False)["take_id"],
        "gone": gone,
        "audio": sound["id"],
    }


@pytest.fixture(scope="module")
def retaken(api, sid, src) -> dict:
    """A retake of src over 0.51-1.26 s (studio snaps the span to frames: 0.5-1.25)."""
    span = {"start": 0.51, "end": 1.26}
    take = made(
        api, sid, RETAKE, FAST | {"retake.source": {"take_id": src["id"]}, "retake.span": span}
    )
    return {"take": take, "span": span}


# ---------------------------------------------------------------- schemas


def test_schemas(echo_wfs):
    by_id = {w["id"]: w for w in echo_wfs}
    retake = {p["name"]: p for p in by_id[RETAKE]["node_schemas"]["echo/retake"]["params"]}
    assert retake["source"] | {"label": None, "help": None} == {
        "name": "source", "type": "media", "label": None, "default": None, "accepts": ["video"], "grid": 32,
        "max_frames": 321, "help": None}  # fmt: skip
    assert (retake["span"]["type"], retake["span"]["default"]) == ("span", None)
    a2v = {p["name"]: p for p in by_id[A2V]["node_schemas"]["echo/a2v"]["params"]}["source"]
    assert (a2v["accepts"], a2v["trim"], a2v["max_s"]) == (["audio"], True, 13.375)
    assert (by_id[RETAKE]["action"], by_id[A2V]["action"]) == ("Retake a section", "Audio to video")
    assert by_id[RETAKE]["media"] == by_id[A2V]["media"] == "video"


# ---------------------------------------------------------------- a video take as a source


def test_retake_run_lineage_and_source_map(api, src, retaken):
    t, s = retaken["take"], src
    assert (t["kind"], t["parents"]) == ("video", [s["id"]])
    assert t["made_with"]["inputs"]["retake.source"] == {"take_id": s["id"]}
    assert t["made_with"]["inputs"]["retake.span"] == {
        "start": 0.5,
        "end": 1.25,
    }  # as checked: snapped
    assert t["source_map"] == {
        "take_id": s["id"],
        "offset_s": 0,
        "new_in_s": 0.5,
        "new_out_s": 1.25,
    }
    assert (t["width"], t["height"], t["video"]["fps"], t["video"]["frames"]) == (256, 128, 24, 49)
    children = {c["id"] for c in ok(api.get(f"{STUDIO}/takes/{s['id']}"))["children"]}
    assert t["id"] in children
    reuse = ok(api.post(f"{STUDIO}/takes/{t['id']}/reuse"))
    assert (reuse["workflow_id"], reuse["inputs"]["retake.source"]) == (
        RETAKE,
        {"take_id": s["id"]},
    )
    assert reuse["inputs"]["retake.span"] == {"start": 0.5, "end": 1.25}  # as recorded


def test_a2v_from_an_audio_part(api, sid, sound):
    part = {"take_id": sound["id"], "in": 0.5, "out": 2.5}
    t = made(api, sid, A2V, FAST_A2V | {"a2v.source": part})
    assert (t["kind"], t["parents"], t["made_with"]["inputs"]["a2v.source"]) == (
        "video",
        [sound["id"]],
        part,
    )
    assert (t["width"], t["height"], t["video"]["fps"], t["video"]["has_audio"]) == (
        256,
        128,
        24,
        True,
    )
    assert abs(t["video"]["duration_s"] - 2.0) <= 1 / 24
    assert t["source_map"] == {
        "take_id": sound["id"],
        "offset_s": -0.5,
        "new_in_s": 0,
        "new_out_s": 2.0,
    }


def test_a2v_from_a_videos_sound_fills_the_trim(api, sid, src):
    """No in / out on a trim param: studio fills in 0 and out = min(the duration, max_s)."""
    t = made(api, sid, A2V, FAST_A2V | {"a2v.source": {"take_id": src["id"]}})
    assert t["parents"] == [src["id"]]
    assert t["source_map"] == {"take_id": src["id"], "offset_s": 0, "new_in_s": 0, "new_out_s": D49}
    # a video's sound snaps to the video's frames (README Media inputs), not to milliseconds
    t = made(
        api, sid, A2V, FAST_A2V | {"a2v.source": {"take_id": src["id"], "in": 0.51, "out": 1.26}}
    )
    assert t["made_with"]["inputs"]["a2v.source"] == {
        "take_id": src["id"],
        "in": 0.5,
        "out": 1.25,
    }  # as checked: on the video's frames
    assert t["source_map"] == {
        "take_id": src["id"],
        "offset_s": -0.5,
        "new_in_s": 0,
        "new_out_s": 0.75,
    }


def test_media_values_are_refused_with_their_texts(api, sid, src, misfits):
    m, s = misfits, src["id"]
    whole = {"start": 0, "end": 1}
    cases = [
        (RETAKE, {"retake.span": whole}, "retake.source: a take is required"),
        (RETAKE, {"retake.source": {"take_id": m["image"]}, "retake.span": whole},
         f"retake.source: take {m['image']} is not a video"),
        (RETAKE, {"retake.source": {"take_id": m["audio"]}, "retake.span": whole},
         f"retake.source: take {m['audio']} is not a video"),
        (RETAKE, {"retake.source": {"take_id": m["offgrid"]}, "retake.span": whole},
         f"retake.source: take {m['offgrid']} is 450 × 256; this input needs sides on a 32 px grid"),
        (RETAKE, {"retake.source": {"take_id": m["long"]}, "retake.span": whole},
         f"retake.source: take {m['long']} has 390 frames; this input takes at most 321"),
        (RETAKE, {"retake.source": {"take_id": s, "in": 0, "out": 1}, "retake.span": whole},
         "retake.source: this input takes the whole take (no in or out)"),
        (RETAKE, {"retake.source": {"take_id": s}}, "retake.span: a range is required"),
        (RETAKE, {"retake.source": {"take_id": s}, "retake.span": {"start": 1, "end": 0.5}},
         "retake.span: start must be before end"),
        (A2V, {}, "a2v.source: a take is required"),
        (A2V, {"a2v.source": {"take_id": m["image"]}}, f"a2v.source: take {m['image']} has no sound"),
        (A2V, {"a2v.source": {"take_id": m["silent"]}}, f"a2v.source: take {m['silent']} has no sound"),
        (A2V, {"a2v.source": {"take_id": m["audio"], "in": 2, "out": 1}}, "a2v.source: in must be before out"),
        (A2V, {"a2v.source": {"take_id": m["audio20"], "in": 0, "out": 14}},
         "a2v.source: the part lasts 14.0 s; this input takes at most 13.4 s"),
    ]  # fmt: skip
    for key in ("pending", "gone"):
        cases += [(RETAKE, {"retake.source": {"take_id": m[key]}, "retake.span": whole},
                   f"retake.source: take {m[key]} not found"),
                  (A2V, {"a2v.source": {"take_id": m[key]}}, f"a2v.source: take {m[key]} not found")]  # fmt: skip
    cases.append(
        (A2V, {"a2v.source": {"take_id": NO_SUCH_ID}}, f"a2v.source: take {NO_SUCH_ID} not found")
    )
    for wf, inputs, text in cases:
        base = FAST if wf == RETAKE else FAST_A2V
        detail = refused(api, sid, wf, base | inputs)
        assert text in detail, f"{wf} {inputs}: {detail}"
    # the numbers are %.1f (README Media inputs)
    end = refused(api, sid, RETAKE, FAST | {"retake.source": {"take_id": s},
                                            "retake.span": {"start": 1, "end": 3}})  # fmt: skip
    assert end == "retake.span: end 3.0 is past the clip's end 2.0", end
    out = refused(
        api, sid, A2V, FAST_A2V | {"a2v.source": {"take_id": m["audio"], "in": 0, "out": 4}}
    )
    assert out == "a2v.source: out 4.0 is past the take's end 3.0", out


def test_offers_skip_what_does_not_fit(api, misfits, echo_wfs, pal):
    """check.py's media_fit through the offers: each misfit gets exactly the echo offers take_uses gives it."""
    for key in ("silent", "offgrid", "long", "audio20"):
        t = mfacts(ok(api.get(f"{STUDIO}/takes/{misfits[key]}")))
        got = [u for u in uses(api, t["id"]) if u["workflow_id"] in ECHO]
        assert got == check.take_uses(t, echo_wfs, pal), key
    for key in ("pending", "gone"):
        assert api.get(f"{STUDIO}/takes/{misfits[key]}/uses").status_code == 404
    assert api.get(f"{STUDIO}/takes/{NO_SUCH_ID}/uses").status_code == 404


def test_only_one_video_source_takes_a_span(api, sid, src):
    """A user workflow with two retake nodes has no single video source: its span is refused."""
    w = ok(api.post(WF, json={"based_on": RETAKE, "title": "Contract two retakes"}), 201)
    d = {k: w[k] for k in ("title", "family", "env", "nodes") if k in w}
    d["nodes"]["retake_2"] = {
        "kind": "echo/retake",
        "in": {"text": "prompt.text"},
        "params": {"delay_ms": 0},
    }
    d["nodes"]["output_2"] = {"kind": "output", "in": {"media": "retake_2.video"}}
    w = ok(api.put(f"{WF}/{w['id']}", json=d | {"version": w["version"]}))
    ref = {"take_id": src["id"]}
    detail = refused(api, sid, w["id"], FAST | {"retake.source": ref, "retake.span": {"start": 0, "end": 1},
                                                "retake_2.source": ref, "retake_2.span": {"start": 0, "end": 1}})  # fmt: skip
    assert "this workflow has no single video source" in detail, detail
    assert api.delete(f"{WF}/{w['id']}").status_code == 204


# ---------------------------------------------------------------- audio uploads


def test_audio_uploads(api, anon):
    t = wav(api, anon, 2.0)
    assert (t["kind"], t["content_type"], t["width"], t["height"]) == (
        "audio",
        "audio/wav",
        None,
        None,
    )
    assert t["audio"] == {"duration_s": 2.0} and t["thumb_url"] and not t.get("video")
    m = upload(api, anon, MP3, "tune.mp3", content_type="audio/mpeg", duration_s=0.5)
    assert (m["kind"], m["content_type"], m["audio"]) == (
        "audio",
        "audio/mpeg",
        {"duration_s": 0.5},
    )
    for ct, ext in (("audio/wav", "wav"), ("audio/mpeg", "mp3")):
        up = upload(api, anon, b"x" * 10, f"a.{ext}", complete=False, content_type=ct, duration_s=1)
        assert up["upload_url"].split("?")[0].endswith(f"/uploads/{up['take_id']}.{ext}")
    assert t["id"] in {
        x["id"] for x in ok(api.get(f"{STUDIO}/takes", params={"kind": "audio"}))["takes"]
    }


# an m4a's head: ftyp (major M4A, compatible M4A isom mp42) then an empty mdat; studio reads the brand, the runner the file
M4A = b"\x00\x00\x00\x1cftypM4A \x00\x00\x02\x00M4A isommp42" + b"\x00\x00\x00\x08mdat"


def test_m4a_uploads(api, anon):
    """API 1.38: audio/mp4 (an m4a, what the web makes of a wav): the .m4a key, files_service's type on it, the ftyp
    brand checked on complete."""
    t = upload(api, anon, M4A, "voice.m4a", content_type="audio/mp4", duration_s=1.5)
    assert (t["kind"], t["content_type"], t["audio"], t["width"]) == (
        "audio",
        "audio/mp4",
        {"duration_s": 1.5},
        None,
    )
    up = upload(api, anon, M4A, "a.m4a", complete=False, content_type="audio/mp4", duration_s=1)
    assert up["upload_url"].split("?")[0].endswith(f"/uploads/{up['take_id']}.m4a")
    # the key's one type (415), the signed length (411)
    for ct, data, code in (
        ("audio/wav", M4A, 415),
        ("audio/mp4", M4A[:-1], 411),
        ("audio/mp4", M4A, 204),
    ):
        r = anon.put(up["upload_url"], content=data, headers={"Content-Type": ct})
        assert r.status_code == code, f"{ct}: {r.status_code} {r.text}"
    ok(api.post(f"{STUDIO}/uploads/{up['take_id']}/complete"))
    video = b"\x00\x00\x00\x1cftypisom\x00\x00\x02\x00isomavc1mp41" + b"\x00\x00\x00\x08mdat"
    bad = upload(api, anon, video, "b.m4a", complete=False, content_type="audio/mp4", duration_s=1)
    assert (
        anon.put(
            bad["upload_url"], content=video, headers={"Content-Type": "audio/mp4"}
        ).status_code
        == 204
    )
    r = api.post(f"{STUDIO}/uploads/{bad['take_id']}/complete")
    assert r.status_code == 422 and r.json()["detail"] == "not a valid m4a file", r.text
    assert api.post(f"{STUDIO}/uploads/{bad['take_id']}/complete").status_code == 404  # discarded


def test_upload_limits(api, no_token):
    """API 1.38: GET /uploads/limits, from the registry as check.py upload_limits computes it over the schemas."""
    groups = ok(api.get(f"{STUDIO}/nodes"))["groups"]
    nodes = [n for g in groups for n in g["nodes"]]
    assert ok(api.get(f"{STUDIO}/uploads/limits")) == check.upload_limits(nodes)
    assert no_token.get(f"{STUDIO}/uploads/limits").status_code == 401


def test_audio_upload_refusals(api, anon):
    good = {"filename": "a.wav", "content_type": "audio/wav", "bytes": 10, "duration_s": 1}
    for change, text in (
        (
            {"content_type": "audio/ogg"},
            "only png, jpeg and webp images or wav, mp3 and m4a audio can be uploaded",
        ),
        (
            {"content_type": "image/gif"},
            "only png, jpeg and webp images or wav, mp3 and m4a audio can be uploaded",
        ),
        ({"duration_s": 600.5}, "audio is longer than 600 s"),
        ({"bytes": 52428801}, None),  # over 50 MB
        ({"duration_s": None}, None),  # required for audio
        ({"content_type": "image/png"}, None),  # refused for an image
    ):
        body = {k: v for k, v in (good | change).items() if v is not None}
        r = api.post(f"{STUDIO}/uploads", json=body)
        assert r.status_code == 422, f"{change}: {r.status_code} {r.text}"
        if text:
            assert r.json()["detail"] == text, r.text
    for ct, ext, text in (("audio/wav", "wav", "not a valid wav file"),
                          ("audio/mpeg", "mp3", "not a valid mp3 file")):  # fmt: skip
        data = b"just some text, not audio at all\n"
        up = upload(api, anon, data, f"bad.{ext}", complete=False, content_type=ct, duration_s=1)
        r = anon.put(up["upload_url"], content=data, headers={"Content-Type": ct})
        assert r.status_code == 204, r.text
        r = api.post(f"{STUDIO}/uploads/{up['take_id']}/complete")
        assert (r.status_code, r.json()["detail"]) == (422, text), r.text
        assert api.get(f"{STUDIO}/takes/{up['take_id']}").status_code == 404  # discarded


# ---------------------------------------------------------------- "Use in…"


def test_use_in_on_a_video_take(api, sid, src, echo_wfs, pal):
    s = mfacts(src)
    got = [u for u in uses(api, s["id"]) if u["workflow_id"] in ECHO]
    assert got == check.take_uses(s, echo_wfs, pal)
    retake = offer(got, RETAKE)
    assert retake["inputs"] == {"retake.source": {"take_id": s["id"]},
                                "retake.span": {"start": 0, "end": D49}}  # fmt: skip
    assert (retake["action"], retake["param"], retake["media"]) == (
        "Retake a section",
        "retake.source",
        "video",
    )
    assert offer(got, A2V)["inputs"] == {"a2v.source": {"take_id": s["id"], "in": 0, "out": D49}}
    for wf in (RETAKE, A2V):
        use = ok(api.post(f"{STUDIO}/takes/{s['id']}/use", json={"workflow_id": wf}))
        assert use == {"workflow_id": wf, "inputs": offer(got, wf)["inputs"]}
    r = api.post(f"{STUDIO}/takes/{s['id']}/use", json={"workflow_id": "echo"})
    assert r.status_code == 422 and "workflow echo takes no video input" in r.json()["detail"], (
        r.text
    )
    # merged over the form, the offer runs
    t = made(api, sid, RETAKE, FAST | retake["inputs"])
    assert t["source_map"] == {"take_id": s["id"], "offset_s": 0, "new_in_s": 0, "new_out_s": D49}


def test_use_in_on_an_audio_take(api, sid, sound, echo_wfs, pal):
    a = mfacts(sound)
    got = [u for u in uses(api, a["id"]) if u["workflow_id"] in ECHO]
    assert got == check.take_uses(a, echo_wfs, pal)
    assert [u["workflow_id"] for u in got] == [A2V]
    assert got[0]["inputs"] == {"a2v.source": {"take_id": a["id"], "in": 0, "out": 3.0}}
    assert (
        ok(api.post(f"{STUDIO}/takes/{a['id']}/use", json={"workflow_id": A2V}))["inputs"]
        == got[0]["inputs"]
    )
    r = api.post(f"{STUDIO}/takes/{a['id']}/use", json={"workflow_id": RETAKE})
    assert (
        r.status_code == 422 and f"workflow {RETAKE} takes no audio input" in r.json()["detail"]
    ), r.text
    t = made(api, sid, A2V, FAST_A2V | got[0]["inputs"])
    assert t["source_map"]["take_id"] == a["id"] and t["parents"] == [a["id"]]


def test_use_in_on_an_image_take_is_unchanged(api, anon, echo_wfs, pal):
    img = upload(api, anon, png_bytes(64, 64))
    got = [u for u in uses(api, img["id"]) if u["workflow_id"] in ECHO]
    assert got == check.take_uses(mfacts(img), echo_wfs, pal)
    assert RETAKE not in {u["workflow_id"] for u in got} and A2V not in {
        u["workflow_id"] for u in got
    }
    assert offer(got, "echo")["inputs"] == {"echo.refs": [{"image": {"take_id": img["id"]}}]}
    use = ok(api.post(f"{STUDIO}/takes/{img['id']}/use", json={"workflow_id": "echo"}))
    assert use == {
        "workflow_id": "echo",
        "inputs": {"echo.refs": [{"image": {"take_id": img["id"]}}]},
    }
    r = api.post(f"{STUDIO}/takes/{img['id']}/use", json={"workflow_id": RETAKE})
    assert r.status_code == 422 and "takes no image input" in r.json()["detail"], r.text


def test_extend_offers_follow_extend_room(api, src, misfits):
    """A template with an extend node (echo-extend; ltx-extend on the mlx host) is offered only when one 8-frame step fits under 321 frames, with
    extend.seconds lowered when 2 s does not fit (check.py extend_room). Needs a live host offering it."""
    seen = False
    for tid in (src["id"], misfits["long"], misfits["offgrid"]):
        take = mfacts(ok(api.get(f"{STUDIO}/takes/{tid}")))
        for u in uses(api, tid):
            w = ok(api.get(f"{STUDIO}/workflows/{u['workflow_id']}"))
            ext = [n for n, x in w["nodes"].items() if x["kind"].endswith("/extend")]
            if not ext:
                continue
            seen = True
            room = check.extend_room(take, None)
            assert room is not None, f"{tid}: offered without room"
            assert {k: v for k, v in u["inputs"].items() if k.startswith(f"{ext[0]}.") and k != u["param"]} == {
                f"{ext[0]}.{k}": v for k, v in room.items()}  # fmt: skip
    assert seen, "echo-extend is offered on a 49-frame video"


# ---------------------------------------------------------------- batches and pipelines


def test_batch_over_a_video_column(api, src, src2, misfits):
    span = {"start": 0, "end": 1}
    prompts = fresh(["retake one", "retake two"])
    table = {"columns": ["prompt.text", "retake.source"],
             "rows": [[prompts[0], {"take_id": src["id"]}], [prompts[1], {"take_id": src2["id"]}]]}  # fmt: skip
    body = {"workflow_id": RETAKE, "rows": {"table": table},
            "inputs": {"retake.delay_ms": 50, "retake.span": span}}  # fmt: skip
    bad = body | {
        "rows": {
            "table": table
            | {"rows": [table["rows"][0], [prompts[1], {"take_id": misfits["image"]}]]}
        }
    }
    r = api.post(BATCHES, json=bad)
    assert r.status_code == 422, r.text
    assert f"row 2: retake.source: take {misfits['image']} is not a video" in r.json()["detail"], (
        r.text
    )
    batch = ok(api.post(BATCHES, json=body), 202)
    try:
        assert wait_batch(api, batch["id"])["status"] == "done"
        rows = rows_of(api, batch["id"])
        for r, s in zip(rows, (src, src2), strict=True):
            assert r["inputs"]["retake.source"] == {"take_id": s["id"]}
            (out,) = r["take_ids"]
            t = ok(api.get(f"{STUDIO}/takes/{out}"))
            assert (t["parents"], t["batch_id"], t["source_map"]["take_id"]) == (
                [s["id"]],
                batch["id"],
                s["id"],
            )
    finally:
        finish(api, batch["id"])


def test_pipeline_video_then_retake(api):
    """inputs → clip (echo-video) → retake (echo-retake, source = clip.takes) → out."""
    retake = {"kind": "step", "workflow": RETAKE, "inputs": {"retake.delay_ms": 50, "retake.span": {"start": 0, "end": 1}},
              "bind": {"prompt.text": "$prompt", "retake.source": "clip.takes"}}  # fmt: skip
    d = {
        "title": "Contract video then retake",
        "nodes": {
            "inputs": inputs_node("prompt"),
            "clip": {
                "kind": "step",
                "workflow": VIDEO,
                "inputs": {"video.steps": 2, "video.delay_ms": 50},
                "bind": {"prompt.text": "$prompt"},
            },
            "retake": retake,
            "out": {"kind": "output", "from": "retake.takes", "collection": "Contract retakes"},
        },
        "edges": [["inputs", "clip"], ["clip", "retake"], ["retake", "out"]],
    }
    unbound = {**d, "nodes": d["nodes"] | {"retake": retake | {"inputs": {"retake.delay_ms": 50}}}}
    probs = ok(api.post(f"{STUDIO}/pipelines/validate", json=unbound))["problems"]
    assert ("unbound_input", "retake", "retake.span") in {
        (p["code"], p["node"], p.get("port")) for p in probs}, probs  # fmt: skip
    p = create_pipeline(api, d)
    assert p["problems"] == [], p["problems"]
    bid = submit_pipeline(api, p["id"], fresh(["a fox to retake"]))["id"]
    try:
        assert wait_batch(api, bid)["counts"]["succeeded"] == 1
        ((tid,),) = [x["take_ids"] for x in rows_of(api, bid)]
        t = ok(api.get(f"{STUDIO}/takes/{tid}"))
        (src_id,) = t["parents"]
        assert ok(api.get(f"{STUDIO}/takes/{src_id}"))["made_with"]["workflow_id"] == VIDEO
        assert t["made_with"]["inputs"]["retake.source"] == {"take_id": src_id}
        assert t["source_map"] == {"take_id": src_id, "offset_s": 0, "new_in_s": 0, "new_out_s": 1}
        assert t["made_with"]["pipeline"]["node"] == "retake"
    finally:
        finish(api, bid)


# ---------------------------------------------------------------- Timeline: clip actions


def test_clip_actions_map_the_range(api, src, src2, echo_wfs, pal):
    tl = create(api, take_ids=[src["id"], src2["id"]])
    ca, cb = (c["id"] for c in clips(tl))
    takes = {t["id"]: facts(t) for t in (src, src2)}
    trim = {"op": "trim", "clip": ca, "edge": "in", "delta": 0.25}  # in 0.25, at 0
    tl = edit(api, tl, trim, takes)
    s = mfacts(src)
    want = lambda a, b: check.take_uses(s, echo_wfs, pal, (snap(a), snap(b)))
    assert actions(api, tl, ca) == want(0.25, D49)  # the clip's extent
    got = actions(api, tl, ca, start=0.25, end=0.75)  # sequence seconds → take seconds 0.5-1.0
    assert got == want(0.5, 1.0)
    assert offer(got, RETAKE)["inputs"]["retake.span"] == {"start": 0.5, "end": 1.0}
    assert offer(got, A2V)["inputs"]["a2v.source"] == {"take_id": s["id"], "in": 0.5, "out": 1.0}
    assert actions(api, tl, ca, start=0, end=100) == want(0.25, D49)  # clamped to the clip
    # the second clip at 43/24: its own take's time
    assert actions(api, tl, cb, start=43 / 24 + 0.5, end=43 / 24 + 1) == check.take_uses(
        mfacts(src2), echo_wfs, pal, (0.5, 1.0))  # fmt: skip
    for params, text in (({"start": 1, "end": 0.5}, "start must be before end"),):
        r = api.get(f"{TL}/{tl['id']}/clips/{ca}/actions", params=params)
        assert (r.status_code, r.json()["detail"]) == (422, text)
    r = api.get(f"{TL}/{tl['id']}/clips/nope/actions")
    assert (r.status_code, r.json()["detail"]) == (422, "unknown clip nope")
    tl = edit(api, tl, {"op": "add_track"}, takes)
    tl = ok(api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [src["id"]], "track": "A2"}))
    (m,) = clips(tl, 1)
    # 1.32 (Make shot): a video's sound on an audio track answers Audio to video at its time, on its frames
    got = [a for a in actions(api, tl, m["id"]) if a["workflow_id"] in ECHO]
    want = check.make_shot(
        doc(tl), m["id"], {k: v | {"id": k} for k, v in takes.items()}, echo_wfs, pal
    )
    assert got == want["actions"] and [a["workflow_id"] for a in got] == [A2V]
    assert got[0]["slot"] == {"at": m["at"], "length": m["out"] - m["in"], "exact": True}
    tl = edit(api, tl, {"op": "lift", "clip": cb}, takes)  # an empty slot
    assert actions(api, tl, cb) == []
    assert api.get(f"{TL}/{NO_SUCH_ID}/clips/{ca}/actions").status_code == 404


def test_actions_of_an_offline_clip_are_empty(api, sid):
    gone = made(api, sid, VIDEO, video_inputs(text="an offline source"))
    tl = create(api, take_ids=[gone["id"]])
    (c,) = clips(tl)
    assert actions(api, tl, c["id"]) != []
    assert api.delete(f"{STUDIO}/takes/{gone['id']}").status_code == 204
    assert actions(api, tl, c["id"]) == []


def test_extend_only_at_a_take_end(api, admin, anon, user_a):
    """An imported take (no workflow: every family's actions): an extend (echo-extend, ltx-extend) is offered after
    when the range ends at the take's end, before when it starts at 0, else not at all."""
    t = import_video(admin, anon, user_a["user"]["id"], 256, 128, 49, 24, True)
    tl = create(api, take_ids=[t["id"]])
    (c,) = clips(tl)

    def direction(**rng):
        ext = [
            a
            for a in actions(api, tl, c["id"], **rng)
            if any(k.endswith(".direction") for k in a["inputs"])
        ]
        return (
            next((v for k, v in ext[0]["inputs"].items() if k.endswith(".direction")), None)
            if ext
            else "none"
        )

    assert direction() == "after"  # the whole clip ends at the take's end
    assert direction(start=1, end=D49) == "after"
    assert direction(start=0, end=1) == "before"
    assert direction(start=0.5, end=1) == "none"


# ---------------------------------------------------------------- Timeline: running an action


@pytest.fixture(scope="module")
def retake_clip(api, src, src2) -> dict:
    """Retake a section of a trimmed clip (in 0.25), replacing it: {"before", "ans", "tl" (filled), "take", "seen"}."""
    tl = create(api, name="Contract retake", take_ids=[src["id"], src2["id"]])
    ca = clips(tl)[0]["id"]
    tl = edit(api, tl, {"op": "trim", "clip": ca, "edge": "in", "delta": 0.25},
              {t["id"]: facts(t) for t in (src, src2)})  # fmt: skip
    o = offer(actions(api, tl, ca, start=0.25, end=0.75), RETAKE)
    ans = shot(api, tl, {"clip": ca}, RETAKE, FAST | o["inputs"] | {"retake.delay_ms": 300})
    c = clip(ans["timeline"], ca)
    assert (c["take_id"], c["shot"]["run_id"]) == (
        src["id"],
        ans["run"]["id"],
    )  # the placeholder keeps the take
    r = api.post(
        f"{TL}/{tl['id']}/shots",
        json={"slot": {"clip": ca}, "run": {"workflow_id": RETAKE, "inputs": FAST | o["inputs"]}},
    )
    assert (r.status_code, r.json()["detail"]) == (409, f"clip {ca} already has a shot running")
    seen = set()
    done = filled(api, ans["timeline"], ca, seen)
    return {"before": tl, "ans": ans, "tl": done, "clip": ca, "seen": seen,
            "take": take_of_run(api, ans["run"]["id"])}  # fmt: skip


def test_retake_replaces_the_clip_keeping_its_window(api, src, retake_clip):
    r, ca = retake_clip, retake_clip["clip"]
    assert r["seen"] <= {"queued", "running"}
    t, tl = r["take"], r["tl"]
    assert t["source_map"] == {
        "take_id": src["id"],
        "offset_s": 0,
        "new_in_s": 0.5,
        "new_out_s": 1.0,
    }
    c = clip(tl, ca)
    assert (c["take_id"], c["in"], c["out"], c["alternatives"]) == (
        t["id"],
        0.25,
        D49,
        [src["id"], t["id"]],
    )
    assert clips(tl)[1]["at"] == 43 / 24  # nothing ripples
    want = check.t_apply(doc(r["ans"]["timeline"]), {"op": "fill", "clip": ca, "take_id": t["id"]},
                         all_facts(api, tl, t["id"]))  # fmt: skip
    assert doc(tl) == want["document"]
    assert tl["revision"] == r["ans"]["timeline"]["revision"] + 1


def test_replace_among_alternatives_and_undo(api, src, retake_clip):
    tl, ca, t = get(api, retake_clip["tl"]), retake_clip["clip"], retake_clip["take"]
    takes = all_facts(api, tl)
    back = edit(api, tl, {"op": "replace", "clip": ca, "take_id": src["id"]}, takes)
    assert (clip(back, ca)["take_id"], clip(back, ca)["in"]) == (src["id"], 0.25)
    und = ok(api.post(f"{TL}/{tl['id']}/undo", json={"revision": back["revision"]}))
    assert clip(und, ca)["take_id"] == t["id"]
    und = ok(
        api.post(f"{TL}/{tl['id']}/undo", json={"revision": und["revision"]})
    )  # skips the fill
    assert doc(und) == doc(retake_clip["before"])  # before the shot, never a finished placeholder


def test_insert_after_holds_the_generated_part(api, src, src2):
    tl = create(api, take_ids=[src["id"], src2["id"]])
    ca, cb = (c["id"] for c in clips(tl))
    o = offer(actions(api, tl, ca, start=0.5, end=1.5), A2V)
    ans = shot(api, tl, {"after": ca, "length": 1.0, "id": "ins"}, A2V, FAST_A2V | o["inputs"])
    before = ans["timeline"]
    assert [c["id"] for c in clips(before)] == [ca, "ins", cb]
    p = clip(before, "ins")
    assert (p["take_id"], p["hold"], p["shot"]["run_id"]) == (None, 1, ans["run"]["id"])
    done = filled(api, before, "ins")
    t = take_of_run(api, ans["run"]["id"])
    assert t["source_map"] == {
        "take_id": src["id"],
        "offset_s": -0.5,
        "new_in_s": 0,
        "new_out_s": 1.0,
    }
    c = clip(done, "ins")
    assert (c["take_id"], c["in"], c["out"]) == (t["id"], 0, 1.0)
    want = check.t_apply(
        doc(before), {"op": "fill", "clip": "ins", "take_id": t["id"]}, all_facts(api, done)
    )
    assert doc(done) == want["document"]


def test_slot_at_inserts_before_a_clip(api, src, src2):
    """An extend before inserts with slot {at: <the clip's at>}: the new slot comes before that clip (a retake of
    its start stands in for the extend on the fake)."""
    tl = create(api, take_ids=[src["id"], src2["id"]])
    ca, cb = (c["id"] for c in clips(tl))
    at = clip(tl, cb)["at"]
    o = offer(actions(api, tl, cb, start=at, end=at + 0.5), RETAKE)
    ans = shot(api, tl, {"at": at, "length": 0.5, "id": "pre"}, RETAKE, FAST | o["inputs"])
    assert [c["id"] for c in clips(ans["timeline"])] == [ca, "pre", cb]
    done = filled(api, ans["timeline"], "pre")
    c, t = clip(done, "pre"), take_of_run(api, ans["run"]["id"])
    assert (c["take_id"], c["in"], c["out"]) == (t["id"], 0, 0.5)  # only the generated part
    assert clip(done, cb)["at"] == (49 + 12) / 24


def test_extend_after_grows_the_clip_and_ripples(api, src, src2):
    """Extend after replacing a clip (the whole clip: the range ends at the take's end): the fill keeps the source's
    window and grows it by the new part, later clips ripple, in one revision; undo goes back to before the shot."""
    tl = create(api, take_ids=[src["id"], src2["id"]])
    ca, cb = (c["id"] for c in clips(tl))
    o = offer(actions(api, tl, ca), EXTEND)
    assert o["inputs"] == {"extend.source": {"take_id": src["id"]}, "extend.direction": "after"}
    ans = shot(api, tl, {"clip": ca}, EXTEND, FAST_EXT | o["inputs"])
    done = filled(api, ans["timeline"], ca)
    t = take_of_run(api, ans["run"]["id"])
    total = (49 + 48) / 24  # 49 frames + 8N, N = ceil(2 s · 24 / 8) = 6 (check.py extend_map)
    assert t["source_map"] == {
        "take_id": src["id"],
        "offset_s": 0,
        "new_in_s": D49,
        "new_out_s": total,
    }
    assert t["video"]["frames"] == 97 and t["parents"] == [src["id"]]
    c = clip(done, ca)
    assert (c["take_id"], c["in"], c["out"]) == (t["id"], 0, total)
    assert clip(done, cb)["at"] == total  # rippled by the new part
    want = check.t_apply(doc(ans["timeline"]), {"op": "fill", "clip": ca, "take_id": t["id"]},
                         all_facts(api, done, t["id"]))  # fmt: skip
    assert doc(done) == want["document"]
    assert done["revision"] == ans["timeline"]["revision"] + 1
    und = ok(api.post(f"{TL}/{tl['id']}/undo", json={"revision": done["revision"]}))
    assert doc(und) == doc(tl)


def test_extend_before_inserts_at_the_clip(api, src, src2):
    """Extend before (the range starts at the take's 0) inserts with slot {at: <the clip's at>}: the slot holds the
    new part [0, 8N/fps) of the result and the clip moves after it."""
    tl = create(api, take_ids=[src["id"], src2["id"]])
    ca, cb = (c["id"] for c in clips(tl))
    at = clip(tl, cb)["at"]
    o = offer(actions(api, tl, cb, start=at, end=at + 0.5), EXTEND)
    assert o["inputs"]["extend.direction"] == "before"
    ans = shot(api, tl, {"at": at, "length": 2.0, "id": "pre"}, EXTEND, FAST_EXT | o["inputs"])
    assert [c["id"] for c in clips(ans["timeline"])] == [ca, "pre", cb]
    done = filled(api, ans["timeline"], "pre")
    t = take_of_run(api, ans["run"]["id"])
    assert t["source_map"] == {
        "take_id": src2["id"],
        "offset_s": 2.0,
        "new_in_s": 0,
        "new_out_s": 2.0,
    }
    c = clip(done, "pre")
    assert (c["take_id"], c["in"], c["out"]) == (t["id"], 0, 2.0)
    assert clip(done, cb)["at"] == (49 + 48) / 24  # on frames: 49 + 8N
    want = check.t_apply(doc(ans["timeline"]), {"op": "fill", "clip": "pre", "take_id": t["id"]},
                         all_facts(api, done, t["id"]))  # fmt: skip
    assert doc(done) == want["document"]


def test_a_cancelled_action_drops_its_shot(api, src):
    tl = create(api, take_ids=[src["id"]])
    (c,) = clips(tl)
    o = offer(actions(api, tl, c["id"]), RETAKE)
    ans = shot(api, tl, {"clip": c["id"]}, RETAKE, FAST | o["inputs"] | {"retake.delay_ms": 5000})
    ok(api.post(f"{STUDIO}/runs/{ans['run']['id']}/cancel"), 202)
    assert wait_run(api, ans["run"]["id"])["status"] == "cancelled"
    cur = wait_for(
        lambda: (x := get(api, tl)) and "shot" not in clip(x, c["id"]) and x,
        what="the shot dropped",
    )
    assert (clip(cur, c["id"])["take_id"], clip(cur, c["id"])["alternatives"]) == (
        src["id"],
        [src["id"]],
    )


def test_rerender_binds_a_retaken_clip_to_its_source(api, src, retaken):
    """Clip 2 (a retake of src) and clip 1 (src): re-rendered together, clip 2's source is clip 1's new take."""
    tl = create(api, name="Contract rerender retake", take_ids=[src["id"], retaken["take"]["id"]])
    c1, c2 = (c["id"] for c in clips(tl))
    ans = ok(
        api.post(f"{TL}/{tl['id']}/rerender", json={"revision": tl["revision"], "clips": [c1, c2]}),
        202,
    )
    assert wait_batch(api, ans["batch"]["id"])["status"] == "done"
    for cid in (c1, c2):
        tl = filled(api, tl, cid)
    new1, new2 = (ok(api.get(f"{STUDIO}/takes/{clip(tl, c)['take_id']}")) for c in (c1, c2))
    assert new1["id"] != src["id"] and new2["id"] != retaken["take"]["id"]
    assert new2["made_with"]["inputs"]["retake.source"]["take_id"] == new1["id"]
    assert (new2["parents"], new2["source_map"]["take_id"]) == ([new1["id"]], new1["id"])
    assert new2["made_with"]["inputs"]["retake.span"] == {"start": 0.5, "end": 1.25}


# ---------------------------------------------------------------- Timeline: audio takes and tracks


def test_an_audio_take_is_not_a_v1_clip(api, src, sound):
    """1.32: an audio take goes on an audio track only (test_timeline_audio covers the audio track)."""
    tl = create(api, take_ids=[src["id"]])
    r = api.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [src["id"], sound["id"]]})
    assert (r.status_code, r.json()["detail"]) == (
        422,
        "take_ids[1]: an audio take goes on an audio track",
    )
    written = {"target": tl["target"], "tracks": [
        {"kind": "video", "clips": [{"id": "s", "take_id": sound["id"], "in": 0, "out": 1}]},
        {"kind": "audio", "clips": [{"id": "a", "take_id": sound["id"], "in": 0, "out": 1, "at": 0}]}]}  # fmt: skip
    assert ["wrong_track", "V1", "s"] in problems(put(api, tl, written, 422))
    assert get(api, tl)["revision"] == tl["revision"]


def test_remove_track(api, src, src2):
    """remove_track for A2…A9, empty or with clips (they go with it); later tracks are renamed; undo brings it
    back; an emptied track stays; V1 cannot go."""
    takes = {t["id"]: facts(t) for t in (src, src2)}
    tl = create(api, take_ids=[src["id"], src2["id"]])
    tl = edit(api, tl, {"op": "add_track"}, takes)
    tl = edit(api, tl, {"op": "add_track"}, takes)
    tl = ok(
        api.post(
            f"{TL}/{tl['id']}/clips", json={"take_ids": [src["id"], src2["id"]], "track": "A3"}
        )
    )
    a3 = [c["id"] for c in clips(tl, 2)]
    assert [t["name"] for t in tl["tracks"]] == ["V1", "A2", "A3"] and len(a3) == 2
    tl = edit(api, tl, {"op": "remove_track", "track": "A2"}, takes)  # empty
    assert [t["name"] for t in tl["tracks"]] == ["V1", "A2"] and [
        c["id"] for c in clips(tl, 1)
    ] == a3
    with_clips = tl
    tl = edit(api, tl, {"op": "remove_track", "track": "A2"}, takes)  # with its 2 clips
    assert [t["name"] for t in tl["tracks"]] == ["V1"]
    und = ok(api.post(f"{TL}/{tl['id']}/undo", json={"revision": tl["revision"]}))
    assert doc(und) == doc(with_clips)
    tl = edit(api, und, {"op": "remove", "clip": a3[0]}, takes)
    tl = edit(api, tl, {"op": "remove", "clip": a3[1]}, takes)
    assert [t["name"] for t in tl["tracks"]] == ["V1", "A2"] and clips(tl, 1) == []  # stays
    assert check.t_edit(doc(tl), {"op": "remove_track", "track": "V1"}, takes) == "video_track"
    no_v1 = {"target": tl["target"], "tracks": [{"kind": "audio", "clips": []}]}
    assert ["track_layout", None, None] in problems(put(api, tl, no_v1, 422))


# ---------------------------------------------------------------- isolation


def test_b_cannot_use_a_takes_as_sources(api, api_b, src, sound, retake_clip):
    sid_b = new_session(api_b, "Contract B media")["id"]
    for tid in (src["id"], sound["id"]):
        assert api_b.get(f"{STUDIO}/takes/{tid}/uses").status_code == 404
        for wf in (RETAKE, A2V):
            assert (
                api_b.post(f"{STUDIO}/takes/{tid}/use", json={"workflow_id": wf}).status_code == 404
            )
    detail = refused(api_b, sid_b, RETAKE, FAST | {"retake.source": {"take_id": src["id"]},
                                                   "retake.span": {"start": 0, "end": 1}})  # fmt: skip
    assert detail == f"retake.source: take {src['id']} not found"
    detail = refused(api_b, sid_b, A2V, FAST_A2V | {"a2v.source": {"take_id": sound["id"]}})
    assert detail == f"a2v.source: take {sound['id']} not found"
    table = {"columns": ["retake.source"], "rows": [[{"take_id": src["id"]}]]}
    r = api_b.post(BATCHES, json={"workflow_id": RETAKE, "rows": {"table": table},
                                  "inputs": {"retake.span": {"start": 0, "end": 1}}})  # fmt: skip
    assert r.status_code == 422 and f"row 1: retake.source: take {src['id']} not found" in r.text, (
        r.text
    )
    tl = retake_clip["tl"]
    r = api_b.get(f"{TL}/{tl['id']}/clips/{retake_clip['clip']}/actions")
    assert r.status_code == 404, r.text
