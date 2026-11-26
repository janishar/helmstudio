"""Media inputs (API 1.19, contracts/README.md "Media inputs"): the `media` and `span` param checks (the caps studio
checked, re-checked on the facts it sent), a run needing both, the media value read under the claim-check rules, and
the fake echo-retake / echo-a2v end to end with the output's source_map."""

import io
import json
import shutil
import subprocess
import wave

import pytest
from conftest import CONTRACTS, SCHEMAS, FakeJS, FakeMsg, FakeStore, command, consumer
from PIL import Image

from core import graph
from messaging import schemas
from packs.echo import mp4

OWNER = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
TAKE = "8a1f0c22-4b5d-4e6f-9a7b-1c2d3e4f5a6b"
VIDEO = {
    "name": "source",
    "type": "media",
    "default": None,
    "accepts": ["video"],
    "grid": 32,
    "max_frames": 321,
}
AUDIO = {
    "name": "source",
    "type": "media",
    "default": None,
    "accepts": ["audio"],
    "trim": True,
    "max_s": 13.375,
}


def value(data: bytes = b"x", **facts) -> dict:
    """A resolved media value (defs media): a 448 x 256 49-frame video take with sound unless `facts` say else."""
    v = {"take_id": TAKE, "kind": "video", "key": f"u/{OWNER}/runs/r/1/0.mp4", "content_type": "video/mp4",
         "bytes": len(data), "etag": "e1", "width": 448, "height": 256, "fps": 24, "frames": 49,
         "duration_s": 49 / 24, "has_audio": True}  # fmt: skip
    return v | facts


def audio_value(data: bytes, **facts) -> dict:
    v = {"take_id": TAKE, "kind": "audio", "key": f"u/{OWNER}/uploads/{TAKE}.wav", "content_type": "audio/wav",
         "bytes": len(data), "etag": "e1", "duration_s": 1.0, "has_audio": True, "in": 0, "out": 1.0}  # fmt: skip
    return v | facts


@pytest.mark.parametrize(
    ("spec", "facts", "why"),
    [
        (VIDEO, {}, None),
        (
            VIDEO,
            {"width": 450},
            f": take {TAKE} is 450 × 256; this input needs sides on a 32 px grid",
        ),
        (VIDEO, {"frames": 390}, f": take {TAKE} has 390 frames; this input takes at most 321"),
        (VIDEO, {"kind": "audio"}, f": take {TAKE} is not a video"),
        (VIDEO, {"in": 0, "out": 1}, ": this input takes the whole take (no in or out)"),
        (AUDIO, {"in": 0, "out": 1}, None),  # a video take's sound
        (AUDIO, {"has_audio": False, "in": 0, "out": 1}, f": take {TAKE} has no sound"),
        (AUDIO, {"in": 1, "out": 1}, ": in must be before out"),
        (AUDIO, {"in": 0, "out": 3}, ": out 3.0 is past the take's end 2.0"),
        (
            AUDIO,
            {"duration_s": 20, "in": 0, "out": 14},
            ": the part lasts 14.0 s; this input takes at most 13.4 s",
        ),
        (
            AUDIO | {"trim": False},
            {"duration_s": 20},
            f": take {TAKE} lasts 20.0 s; this input takes at most 13.4 s",
        ),
        (VIDEO, None, "must be a take"),  # an unresolved value (no key)
        # API 1.22 min_s: the take, then (with trim) the part
        (AUDIO | {"min_s": 2}, {"in": 0, "out": 2}, None),
        (
            AUDIO | {"min_s": 3},
            {"in": 0, "out": 1},
            f": take {TAKE} lasts 2.0 s; this input takes at least 3.0 s",
        ),
        (
            AUDIO | {"min_s": 2},
            {"in": 0.5, "out": 2},
            ": the part lasts 1.5 s; this input takes at least 2.0 s",
        ),
    ],
)
def test_media_params_recheck_the_caps(spec, facts, why):
    v = {"take_id": TAKE} if facts is None else value(**facts)
    assert graph._check_param(spec, v) == why
    assert graph._check_param(spec, None) is None  # a registry workflow holds null


@pytest.mark.parametrize(
    ("v", "why"),
    [
        ({"start": 0.5, "end": 1.0}, None),
        ({"start": 1.0, "end": 1.0}, ": start must be before end"),
        ({"start": 0}, "must be a range {start, end}"),
        ({"start": True, "end": 1}, "must be a range {start, end}"),
        (None, None),
    ],
)
def test_span_values(v, why):
    assert graph._check_param({"name": "span", "type": "span", "default": None}, v) == why


def retake_nodes(source, span) -> dict:
    return {"prompt": {"kind": "prompt", "params": {"text": "hi"}},
            "retake": {"kind": "echo/retake", "in": {"text": "prompt.text"},
                       "params": {"source": source, "span": span, "delay_ms": 0}},
            "output": {"kind": "output", "params": {}, "in": {"media": "retake.video"}}}  # fmt: skip


def test_a_run_needs_its_take_and_its_range():
    errs = graph.validate(retake_nodes(None, None), SCHEMAS)
    assert errs == ["retake.source: a take is required", "retake.span: a range is required"]
    assert (
        graph.validate(retake_nodes(None, None), SCHEMAS, run=False) == []
    )  # the registry workflow


def run_command(nodes: dict) -> dict:
    cmd = command()
    cmd["data"]["workflow"] = {"id": "echo-retake", "nodes": nodes}
    return cmd


def events_of(js: FakeJS) -> list[dict]:
    evts = js.events()
    for e in evts:
        assert schemas.message_errors(e) == [], e
    return evts


async def test_echo_retake_reads_the_source_and_reports_its_source_map():
    clip = mp4.mp4(Image.new("RGB", (64, 32), "red"), 9, 24)
    src = value(clip, width=64, height=32, frames=9, duration_s=9 / 24)
    js, store = FakeJS(), FakeStore()
    store.objects[src["key"]] = (clip, "video/mp4")
    c = consumer(js)
    c.store = store
    cmd = run_command(retake_nodes(src, {"start": 0.125, "end": 0.25}))
    assert schemas.message_errors(cmd) == []
    await c.handle(FakeMsg(cmd, js))
    evts = events_of(js)
    assert evts[-1]["data"]["status"] == "succeeded", evts[-1]
    out = next(e for e in evts if e["type"] == "evt.run.output")["data"]
    assert out["source_map"] == {
        "take_id": TAKE,
        "offset_s": 0,
        "new_in_s": 0.125,
        "new_out_s": 0.25,
    }
    assert (out["width"], out["height"], out["video"]["frames"], out["video"]["fps"]) == (
        64,
        32,
        9,
        24,
    )
    assert store.reads == [src["key"]]  # read under the claim check before it rendered


def extend_nodes(source, seconds: float, direction: str) -> dict:
    return {"prompt": {"kind": "prompt", "params": {"text": "hi"}},
            "extend": {"kind": "echo/extend", "in": {"text": "prompt.text"},
                       "params": {"source": source, "seconds": seconds, "direction": direction, "delay_ms": 0}},
            "output": {"kind": "output", "params": {}, "in": {"media": "extend.video"}}}  # fmt: skip


@pytest.mark.parametrize(
    ("frames", "seconds", "direction", "want"),
    [  # check.py extend_map: the source floored to 8k + 1, + 8N
        (12, 0.5, "after", (25, {"offset_s": 0, "new_in_s": 9 / 24, "new_out_s": 25 / 24})),
        (9, 0.1, "before", (17, {"offset_s": 8 / 24, "new_in_s": 0, "new_out_s": 8 / 24})),
        (313, 2, "after", "extend.seconds: the clip would be 361 frames; at most 321"),
    ],
)
async def test_echo_extend_grows_the_clip_as_ltx_extend(frames, seconds, direction, want):
    clip = mp4.mp4(Image.new("RGB", (64, 32), "red"), 9, 24)
    src = value(clip, width=64, height=32, frames=frames, duration_s=frames / 24)
    js, store = FakeJS(), FakeStore()
    store.objects[src["key"]] = (clip, "video/mp4")
    c = consumer(js)
    c.store = store
    await c.handle(FakeMsg(run_command(extend_nodes(src, seconds, direction)), js))
    evts = events_of(js)
    if isinstance(want, str):
        assert evts[-1]["data"]["error"] == want
        return
    assert evts[-1]["data"]["status"] == "succeeded", evts[-1]
    out = next(e for e in evts if e["type"] == "evt.run.output")["data"]
    assert out["video"]["frames"] == want[0] and out["source_map"] == {"take_id": TAKE} | want[1]


async def test_a_media_take_that_is_gone_fails_the_run_before_any_step():
    src = value(b"0123")
    js = FakeJS()
    c = consumer(js)
    await c.handle(FakeMsg(run_command(retake_nodes(src, {"start": 0, "end": 1})), js))
    evts = events_of(js)
    assert [e["type"] for e in evts] == ["evt.run.status", "evt.run.status"]
    assert evts[-1]["data"]["error"] == f"input take {TAKE} is gone"


def wav(seconds: float = 1.0, rate: int = 48000) -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "w") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(b"\x10\x00" * 2 * int(seconds * rate))
    return buf.getvalue()


@pytest.mark.skipif(
    shutil.which("ffmpeg") is None, reason="echo/a2v muxes with ffmpeg (in the fake image)"
)
async def test_echo_a2v_clips_the_part_with_its_sound_and_maps_it_from_in():
    data = wav()
    src = audio_value(data, **{"in": 0.25, "out": 0.75})
    js, store = FakeJS(), FakeStore()
    store.objects[src["key"]] = (data, "audio/wav")
    c = consumer(js)
    c.store = store
    nodes = {"prompt": {"kind": "prompt", "params": {"text": "hi"}},
             "a2v": {"kind": "echo/a2v", "in": {"text": "prompt.text"}, "params": {"source": src, "delay_ms": 0}},
             "output": {"kind": "output", "params": {}, "in": {"media": "a2v.video"}}}  # fmt: skip
    await c.handle(FakeMsg(run_command(nodes), js))
    evts = events_of(js)
    assert evts[-1]["data"]["status"] == "succeeded", evts[-1]
    out = next(e for e in evts if e["type"] == "evt.run.output")["data"]
    assert out["source_map"] == {
        "take_id": TAKE,
        "offset_s": -0.25,
        "new_in_s": 0,
        "new_out_s": 0.5,
    }
    assert (out["width"], out["height"], out["video"]["frames"]) == (256, 128, 12)
    assert codecs(store.objects[out["key"]][0]) == ["h264", "aac"]


def codecs(clip: bytes) -> list[str]:
    probe = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-of", "json",
                            "-"], input=clip, capture_output=True, check=True).stdout  # fmt: skip
    return [s["codec_name"] for s in json.loads(probe)["streams"]]


def test_the_canonical_media_commands_fit_the_message_schema():
    for n in (8, 9):
        cmd = json.loads(
            (CONTRACTS / f"fixtures/messages/cmd.run.execute.valid.{n}.json").read_text()
        )
        assert schemas.message_errors(cmd) == []


# --- API 1.22: rows of media (minimax-h3/generate's videos and audios)
H3_VIDEOS = {"name": "videos", "type": "rows", "default": [], "min": 0, "max": 3,
             "item": {"video": {"type": "media", "default": None, "accepts": ["video"], "trim": True, "min_s": 2,
                                "max_s": 15},
                      "sound": {"type": "enum", "default": "keep", "enum": ["keep", "silent"]}}}  # fmt: skip


def test_a_rows_media_field_is_required_and_rechecked():
    v = value(duration_s=3.0, **{"in": 0, "out": 3.0})
    assert graph._check_param(H3_VIDEOS, [{"video": v, "sound": "silent"}, {"video": v}]) is None
    assert graph._check_param(H3_VIDEOS, [{"sound": "keep"}]) == "item 1.video: a take is required"
    assert graph._check_param(H3_VIDEOS, [{"video": None}]) == "item 1.video: a take is required"
    short = v | {"in": 1.5}
    assert graph._check_param(H3_VIDEOS, [{"video": v}, {"video": short}]) == (
        "item 2.video : the part lasts 1.5 s; this input takes at least 2.0 s")  # fmt: skip


async def test_rows_media_values_are_read_with_the_claim_check():
    from core import images

    data = b"0123456789"
    v = value(data, duration_s=3.0, **{"in": 0, "out": 3.0})
    store = {v["key"]: data}

    async def fetch(key, limit, etag):
        return store.get(key) if etag == "e1" else None

    params = {"videos": [{"video": v, "sound": "keep"}], "steps": 2}
    assert (
        images.media([H3_VIDEOS], params) == ["videos"]
        and images.media([H3_VIDEOS], {"videos": []}) == []
    )
    got = await images.load([H3_VIDEOS], params, OWNER, fetch)
    assert got["videos"] == [{"video": v | {"data": data}, "sound": "keep"}] and got["steps"] == 2
    with pytest.raises(graph.RunError, match="is not the run owner's"):
        await images.load(
            [H3_VIDEOS], {"videos": [{"video": v | {"key": "u/other/x.mp4"}}]}, OWNER, fetch
        )
    with pytest.raises(graph.RunError, match="is gone"):
        await images.load([H3_VIDEOS], {"videos": [{"video": v | {"etag": "e2"}}]}, OWNER, fetch)


# API 1.79 (contracts/README.md "Music and sound"): the echo music and sound fakes on the consumer


def one(kind: str, out: str, source=None, **params) -> dict:
    node = {
        "kind": kind,
        "in": {"text": "prompt.text"},
        "params": {"delay_ms": 0, "seed": 7} | params,
    }
    if source is not None:
        node["params"]["source"] = source
    return {"prompt": {"kind": "prompt", "params": {"text": "hi"}}, "n": node,
            "output": {"kind": "output", "params": {}, "in": {"media": f"n.{out}"}}}  # fmt: skip


async def run_nodes(
    nodes: dict, store: FakeStore | None = None
) -> tuple[list[dict], FakeStore, list[dict]]:
    js, store = FakeJS(), store or FakeStore()
    c = consumer(js)
    c.store = store
    cmd = run_command(nodes)
    assert schemas.message_errors(cmd) == []
    await c.handle(FakeMsg(cmd, js))
    evts = events_of(js)
    assert evts[-1]["data"]["status"] == "succeeded", evts[-1]
    return [e["data"] for e in evts if e["type"] == "evt.run.output"], store, evts


def wav_facts(data: bytes) -> tuple:
    with wave.open(io.BytesIO(data)) as w:
        return w.getnchannels(), w.getframerate(), w.getnframes()


async def test_echo_music_is_stereo_with_sections_and_reports_its_plan():
    lyrics = "[verse]\nla la\n[chorus]\noh"
    outs, store, evts = await run_nodes(
        one("echo/music", "audio", lyrics=lyrics, instrumental=False, seconds=0, bpm=0)
    )
    audio = outs[0]["audio"]
    assert audio == {"duration_s": 10.0, "sample_rate": 48000, "channels": 2,
                     "sections": [{"label": "verse", "start_s": 0, "end_s": 5.0},
                                  {"label": "chorus", "start_s": 5.0, "end_s": 10.0}]}  # fmt: skip
    assert wav_facts(store.objects[outs[0]["key"]][0]) == (2, 48000, 480000)
    assert evts[-1]["data"]["resolved"]["texts"] == {"n.plan": "90 BPM · A minor · 4/4 · 0:10 · en"}


async def test_echo_sound_takes_its_length_and_an_instrumental_song_has_no_sections():
    outs, _, _ = await run_nodes(one("echo/sound", "audio", avoid="", seconds=1.5))
    assert outs[0]["audio"] == {"duration_s": 1.5, "sample_rate": 44100, "channels": 2}
    outs, _, _ = await run_nodes(
        one("echo/music", "audio", lyrics="[verse]", instrumental=True, seconds=2, bpm=120)
    )
    assert "sections" not in outs[0]["audio"] and outs[0]["audio"]["duration_s"] == 2


def test_auto_takes_zero_below_min_only_where_declared():
    music = SCHEMAS["echo/music"]
    seconds = next(p for p in music["params"] if p["name"] == "seconds")
    assert graph._check_param(seconds, 0) is None
    assert graph._check_param(seconds | {"auto": None}, 0) == "is below the minimum 1"
    assert graph._check_param(seconds, 0.5) == "is below the minimum 1"


async def test_echo_repaint_and_extend_read_an_audio_source():
    src = audio_value(b"", duration_s=1.0)
    del src["in"], src["out"]
    import array
    import math

    pcm = array.array("h", (round(8000 * math.sin(i / 10)) for i in range(48000))).tobytes()
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1), w.setsampwidth(2), w.setframerate(48000), w.writeframes(pcm)
    data = buf.getvalue()
    src |= {"bytes": len(data)}
    store = FakeStore()
    store.objects[src["key"]] = (data, "audio/wav")
    outs, _, _ = await run_nodes(
        one("echo/repaint", "audio", src, span={"start": 0.25, "end": 0.5}), store
    )
    assert outs[0]["audio"] == {"duration_s": 1.0, "sample_rate": 48000, "channels": 2}
    outs, _, _ = await run_nodes(
        one("echo/lengthen", "audio", src, seconds=2, where="start"), store
    )
    assert outs[0]["audio"] == {"duration_s": 3.0, "sample_rate": 48000, "channels": 2}


async def test_echo_foley_gives_a_sound_and_the_video_with_it():
    clip = mp4.mp4(Image.new("RGB", (64, 32), "red"), 24, 24)
    src = value(clip, width=64, height=32, frames=24, duration_s=1.0)
    store = FakeStore()
    store.objects[src["key"]] = (clip, "video/mp4")
    nodes = one("echo/foley", "audio", src)
    nodes["output_video"] = {"kind": "output", "params": {}, "in": {"media": "n.video"}}
    outs, _, _ = await run_nodes(nodes, store)
    assert sorted(o["kind"] for o in outs) == ["audio", "video"]
    audio = next(o for o in outs if o["kind"] == "audio")["audio"]
    video = next(o for o in outs if o["kind"] == "video")
    assert audio == {"duration_s": 1.0, "sample_rate": 44100, "channels": 2}
    assert video["video"]["has_audio"] and "source_map" not in video
