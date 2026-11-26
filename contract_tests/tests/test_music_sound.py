"""Music and sound (API 1.79, contracts/README.md "Music and sound") on the fake runner's echo fakes: Workflow.sound and
`?media=music|sound|voice`, a song's take (class music, stereo 48 kHz, sections from the lyrics' tags, the plan as
resolved.texts), a sound take, one run giving a sound and the video with it, uploads with a class (422 on an image or
a bad class), `?kind=` by class, offers by class (Repaint and Extend on music, Vary on sound, neither on voice; a span
over the whole audio take), a Repaint run with a span on audio, mentions expanded in a music prompt (the voice rule is
class voice only), and isolation. Skipped when the fake runner has no echo-music (before API 1.79).

Not covered: the mux endpoint and job (phase M, test_mux.py), the real packs (their CPU tests and goldens), the
backfill of audio takes made before 1.79 (migration 00056; an audio upload without a class is voice here)."""

import io
import wave

import pytest
import schemas
from conftest import headers, make_client
from helpers import STUDIO, all_takes, new_session, ok, wait_run

MUSIC, SOUND, FOLEY = "echo-music", "echo-sound", "echo-sound-video"
REPAINT, VARY, EXTEND = "echo-repaint", "echo-vary", "echo-music-extend"
LYRICS = "[verse]\nla la la\n[chorus]\noh oh"


def wav_bytes(seconds: float, rate: int = 48000) -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1), w.setsampwidth(2), w.setframerate(rate)
        w.writeframes(b"\x01\x00" * round(seconds * rate))
    return buf.getvalue()


def upload_audio(client, anon, seconds: float = 2.0, sound: str | None = None) -> dict:
    data = wav_bytes(seconds)
    body = {
        "filename": "clip.wav",
        "content_type": "audio/wav",
        "bytes": len(data),
        "duration_s": seconds,
    }
    up = ok(client.post(f"{STUDIO}/uploads", json=body | ({"sound": sound} if sound else {})), 201)
    assert (
        anon.put(up["upload_url"], content=data, headers={"Content-Type": "audio/wav"}).status_code
        == 204
    )
    return ok(client.post(f"{STUDIO}/uploads/{up['take_id']}/complete"))


def run(client, sid: str, wf: str, inputs: dict, status: int = 202) -> dict:
    return ok(
        client.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": wf, "inputs": inputs}),
        status,
    )


def finished(client, sid: str, wf: str, inputs: dict) -> tuple[dict, list[dict]]:
    """The finished run and its takes, in output order."""
    done = wait_run(client, run(client, sid, wf, inputs)["id"])
    assert done["status"] == "succeeded", done
    return done, [ok(client.get(f"{STUDIO}/takes/{o['take_id']}")) for o in done["outputs"]]


@pytest.fixture(scope="module", autouse=True)
def echo_music(api):
    ids = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if MUSIC not in ids:
        pytest.skip("the fake runner has no echo-music workflow (before API 1.79)")


@pytest.fixture(scope="module")
def um(new_user):
    """User M: this module's takes."""
    with make_client(headers(new_user("M")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def sid(um) -> str:
    return new_session(um, "Contract music and sound")["id"]


@pytest.fixture(scope="module")
def song(um, sid) -> dict:
    run_, (take,) = finished(um, sid, MUSIC, {"prompt.text": "warm lo-fi", "music.lyrics": LYRICS,
                                              "music.seconds": 4, "music.delay_ms": 0})  # fmt: skip
    return take | {"run": run_}


# ---------------------------------------------------------------- workflows


def test_workflows_carry_their_class_and_filter_by_it(api):
    every = {w["id"]: w for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    assert {i: every[i].get("sound") for i in (MUSIC, REPAINT, EXTEND, SOUND, FOLEY, VARY, "echo-voice-over")} == {
        MUSIC: "music", REPAINT: "music", EXTEND: "music", SOUND: "sound", FOLEY: "sound", VARY: "sound",
        "echo-voice-over": "voice"}  # fmt: skip
    assert every[FOLEY]["media"] == "audio"  # an Audio and a Video output: the class wins
    assert "sound" not in every["echo"] and "sound" not in every["echo-video"]
    for cls in ("music", "sound", "voice"):
        got = {
            w["id"] for w in ok(api.get(f"{STUDIO}/workflows", params={"media": cls}))["workflows"]
        }
        assert got == {
            i for i, w in every.items() if w["media"] == "audio" and w.get("sound", "voice") == cls
        }
    assert ok(api.get(f"{STUDIO}/workflows", params={"media": "song"}), 422)["detail"] == (
        "media must be image, video, audio, music, sound or voice"
    )
    studio = every[SOUND]["studio"]
    assert studio["count"] == {"default": 4, "max": 8}
    assert every[MUSIC]["studio"]["groups"] == [
        {"label": "Length · BPM", "items": ["music.seconds", "music.bpm"]}
    ]
    params = {p["name"]: p for p in every[MUSIC]["node_schemas"]["echo/music"]["params"]}
    assert params["bpm"]["auto"] is True and params["lyrics"]["tags"] == [
        "verse",
        "chorus",
        "bridge",
        "intro",
        "outro",
    ]


# ---------------------------------------------------------------- takes


def test_a_song_is_stereo_music_with_sections_and_its_plan(um, song):
    schemas.check_value(song, "#/components/schemas/Take", "the song")
    assert song["kind"] == "audio" and song["sound"] == "music"
    assert song["audio"] == {"duration_s": 4, "sample_rate": 48000, "channels": 2,
                             "sections": [{"label": "verse", "start_s": 0, "end_s": 2},
                                          {"label": "chorus", "start_s": 2, "end_s": 4}]}  # fmt: skip
    assert song["run"]["resolved"]["texts"] == {"music.plan": "90 BPM · A minor · 4/4 · 0:04 · en"}


def test_bpm_and_length_take_zero_as_auto_below_their_min(um, sid):
    _, (take,) = finished(
        um, sid, MUSIC, {"prompt.text": "auto", "music.instrumental": True, "music.delay_ms": 0}
    )
    assert (
        take["audio"]["duration_s"] == 10 and "sections" not in take["audio"]
    )  # Auto: 10 s; instrumental
    detail = ok(
        um.post(
            f"{STUDIO}/sessions/{sid}/runs",
            json={"workflow_id": MUSIC, "inputs": {"music.bpm": 10}},
        ),
        422,
    )
    assert detail["detail"].startswith("music.bpm:")


def test_one_run_gives_a_sound_and_the_video_with_it(um, sid, anon):
    _, (clip,) = finished(
        um, sid, "echo-video", {"prompt.text": "a door", "video.delay_ms": 0, "video.steps": 1}
    )
    _, takes = finished(um, sid, FOLEY, {"prompt.text": "creak", "foley.source": {"take_id": clip["id"]},
                                         "foley.delay_ms": 0})  # fmt: skip
    assert sorted(t["kind"] for t in takes) == ["audio", "video"]
    sound = next(t for t in takes if t["kind"] == "audio")
    video = next(t for t in takes if t["kind"] == "video")
    assert sound["sound"] == "sound" and sound["audio"]["channels"] == 2 and "sound" not in video
    assert (
        video["video"]["has_audio"] and sound["run_id"] == video["run_id"]
    )  # "Sound only": the sibling


def test_uploads_take_a_class_and_kind_filters_by_it(um, anon, song):
    music = upload_audio(um, anon, sound="music")
    voice = upload_audio(um, anon)  # no class: voice
    assert (music["sound"], voice["sound"]) == ("music", "voice")
    for body, why in (
        ({"filename": "a.png", "content_type": "image/png", "bytes": 10, "sound": "music"}, "only audio has a sound class"),
        ({"filename": "a.wav", "content_type": "audio/wav", "bytes": 10, "duration_s": 1, "sound": "song"},
         "sound must be music, sound or voice"),
    ):  # fmt: skip
        assert ok(um.post(f"{STUDIO}/uploads", json=body), 422)["detail"] == why
    by = lambda kind: {t["id"] for t in all_takes(um, kind=kind)}
    assert {music["id"], song["id"]} <= by("music") and voice["id"] not in by("music")
    assert voice["id"] in by("voice") and song["id"] not in by("voice")
    assert {music["id"], voice["id"], song["id"]} <= by("audio")
    assert ok(um.get(f"{STUDIO}/takes", params={"kind": "song"}), 422)["detail"] == (
        "kind must be image, video, audio, music, sound or voice"
    )


# ---------------------------------------------------------------- offers by class


def offered(client, take_id: str) -> dict:
    return {u["workflow_id"]: u for u in ok(client.get(f"{STUDIO}/takes/{take_id}/uses"))["uses"]}


def test_offers_go_by_class(um, anon, song, sid):
    got = offered(um, song["id"])
    assert REPAINT in got and EXTEND in got and VARY not in got
    assert got[REPAINT]["inputs"] == {
        "repaint.source": {"take_id": song["id"]},
        "repaint.span": {"start": 0, "end": 4},
    }
    assert got[REPAINT]["action"] == "Repaint" and got[REPAINT]["media"] == "audio"
    voice = offered(um, upload_audio(um, anon)["id"])
    assert not {REPAINT, EXTEND, VARY} & set(voice)
    _, (fx,) = finished(
        um, sid, SOUND, {"prompt.text": "rain", "sound.seconds": 1, "sound.delay_ms": 0}
    )
    assert fx["sound"] == "sound" and fx["audio"]["duration_s"] == 1
    sound = offered(um, fx["id"])
    assert VARY in sound and not {REPAINT, EXTEND} & set(sound)


def test_a_repaint_takes_a_span_on_the_song(um, sid, song):
    inputs = {"prompt.text": "new chorus", "repaint.source": {"take_id": song["id"]},
              "repaint.span": {"start": 2.00049, "end": 3.5}, "repaint.delay_ms": 0}  # fmt: skip
    done, (take,) = finished(um, sid, REPAINT, inputs)
    assert done["inputs"]["repaint.span"] == {
        "start": 2.00049,
        "end": 3.5,
    }  # as requested; snapped when sent
    assert (
        take["sound"] == "music"
        and take["audio"]["duration_s"] == 4
        and take["parents"] == [song["id"]]
    )
    late = inputs | {"repaint.span": {"start": 3, "end": 9}}
    assert "past the clip's end" in ok(um.post(f"{STUDIO}/sessions/{sid}/runs",
                                               json={"workflow_id": REPAINT, "inputs": late}), 422)["detail"]  # fmt: skip


def test_a_music_prompt_reads_its_mentions(um, sid):
    """The voice rule is class voice only: an unknown @handle in a song's prompt is refused as an image prompt's is."""
    body = {
        "workflow_id": MUSIC,
        "inputs": {"prompt.text": "a song for @nobody", "music.delay_ms": 0},
    }
    assert (
        ok(um.post(f"{STUDIO}/sessions/{sid}/runs", json=body), 422)["detail"]
        == "prompt.text: unknown element @nobody"
    )


# ---------------------------------------------------------------- isolation


def test_another_users_class_filter_and_offers_never_show_these(api_b, song):
    assert song["id"] not in {t["id"] for t in all_takes(api_b, kind="music")}
    assert api_b.get(f"{STUDIO}/takes/{song['id']}/uses").status_code == 404
