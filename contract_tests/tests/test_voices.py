"""Voices (API 1.30) on the fake runner's `echo-voice-over` (contracts/README.md, Voices: the echo family): an audio
take from a starter voice (a 24 kHz PCM 16-bit mono WAV lasting characters / 15 s), the voice param's 422s, designed
voice CRUD and every validation text, starter voices read-only, the voice fit in `GET /library?workflow=`, the
spoken prompt (mentions refused, literal `@` as typed, looks never fit), `?media=audio`, isolation. Skipped when the
fake runner has no `echo-voice-over` (before API 1.30).

Not covered: cloned voices (1.31: only refused here), the message schemas (cmd.run.execute's `voice`,
evt.run.output's `audio`: not visible over HTTP, check.py validates their fixtures), a preset of another family
(the fake runner publishes echo voices only; the test runs when some host publishes one)."""

import io
import math
import wave
from urllib.parse import urlsplit

import pytest
import schemas
from conftest import headers, make_client
from helpers import NO_SUCH_ID, STUDIO, new_session, ok, png_bytes, take_of_run, upload, wait_run
from test_elements import LIB, detail, element
from test_looks import look

WF = "echo-voice-over"
ADA, BO = "echo/ada", "echo/bo"
TEXT = "Back by noon, I said."  # 21 characters: 21 / 15 s
FAST = {"speak.delay_ms": 50}
READ_ONLY = "starter voices are read-only"
SPOKEN = "prompt.text: mentions are not read aloud; use a dialogue script"


def voice(vid: str) -> dict:
    return {"voice_id": vid}


def designed(client, name: str = "Nervous teen", **fields) -> dict:
    body = {"kind": "voice", "group": "designed", "name": name}
    body["voice"] = {"design": "male, 17, tenor, nervous"} | fields
    return ok(client.post(LIB, json=body), 201)


def update(item: dict, **changes) -> dict:
    """A LibraryItemUpdate of a designed voice as it is, with changes (`voice` replaces its fields)."""
    v = {k: item["voice"][k] for k in ("design", "language", "style") if k in item["voice"]}
    u = {k: item[k] for k in ("group", "name", "text", "images", "version")}
    return u | {"voice": v} | changes


def post_run(client, sid: str, inputs: dict, looks=None):
    body = {"workflow_id": WF, "inputs": FAST | inputs}
    if looks is not None:
        body["looks"] = looks
    return client.post(f"{STUDIO}/sessions/{sid}/runs", json=body)


def speak(client, sid: str, vid: str, text: str = TEXT) -> dict:
    """A finished run's take, spoken by `vid`."""
    run = ok(post_run(client, sid, {"prompt.text": text, "speak.voice": voice(vid)}), 202)
    assert wait_run(client, run["id"])["status"] == "succeeded"
    return take_of_run(client, run["id"])


def refused(client, sid: str, inputs: dict, looks=None) -> str:
    return detail(post_run(client, sid, inputs, looks), 422)


def by_id(client, **params) -> dict:
    return {x["id"]: x for x in ok(client.get(LIB, params=params))["items"]}


@pytest.fixture(scope="module", autouse=True)
def voice_over(api) -> dict:
    """The live echo-voice-over workflow; skips the module when the fake runner does not publish it."""
    ids = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if WF not in ids:
        pytest.skip("the fake runner has no echo-voice-over workflow (before API 1.30)")
    return ok(api.get(f"{STUDIO}/workflows/{WF}"))


@pytest.fixture(scope="module")
def uv(new_user):
    """User V: this module's voices."""
    with make_client(headers(new_user("V")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def sid(uv) -> str:
    return new_session(uv, "Contract voices")["id"]


# ---------------------------------------------------------------- workflows


def test_media_audio_lists_echo_voice_over(api, voice_over):
    assert voice_over["media"] == "audio"
    listed = {w["id"]: w["media"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    got = ok(api.get(f"{STUDIO}/workflows", params={"media": "audio"}))["workflows"]
    assert {w["id"] for w in got} == {i for i, m in listed.items() if m == "audio"}
    assert WF in {w["id"] for w in got}


# ---------------------------------------------------------------- a run


def test_a_starter_voice_makes_an_audio_take(uv, anon, sid):
    take = speak(uv, sid, ADA)
    schemas.check_value(take, "#/components/schemas/Take", "the audio take")
    assert (take["kind"], take["content_type"], take["width"], take["height"]) == (
        "audio",
        "audio/wav",
        None,
        None,
    )
    a = take["audio"]
    assert a["sample_rate"] == 24000 and math.isclose(a["duration_s"], len(TEXT) / 15, rel_tol=1e-3)
    assert urlsplit(take["url"]).path.endswith(".wav") and take["thumb_url"]
    assert urlsplit(take["thumb_url"]).path == urlsplit(take["url"]).path  # the file itself
    r = anon.get(take["url"])
    assert r.status_code == 200 and len(r.content) == take["bytes"]
    with wave.open(io.BytesIO(r.content)) as w:
        assert (w.getnchannels(), w.getsampwidth(), w.getframerate()) == (1, 2, 24000)
        assert math.isclose(w.getnframes() / 24000, a["duration_s"], rel_tol=1e-3)
    mw = take["made_with"]
    assert mw["inputs"]["speak.voice"] == voice(ADA)
    assert mw["voices"] == [{"id": ADA, "name": "Ada", "version": 1, "source": "preset"}]
    assert take["parents"] == []


def test_a_designed_voice_speaks_the_text_as_typed(uv, sid):
    v = designed(uv, "Typed", language="en-US", style="whispering")
    text = "Mail mara@example.com at @ 3pm, say @@home."  # resolve.audio-as-typed
    take = speak(uv, sid, v["id"], text)
    mw = take["made_with"]
    assert mw["inputs"]["prompt.text"] == text and not mw.get("elements")
    assert mw["voices"] == [{"id": v["id"], "name": "Typed", "version": 1, "source": "designed"}]


def test_the_voice_param_refusals(uv, sid):
    base = {"prompt.text": TEXT}
    assert refused(uv, sid, base) == "speak.voice: a voice is required"
    assert refused(uv, sid, base | {"speak.voice": None}) == "speak.voice: a voice is required"
    for vid in (
        NO_SUCH_ID,
        "echo/nobody",
        "nofamily/ada",
        "echo/soft-light",
    ):  # a look is not a voice
        assert refused(uv, sid, base | {"speak.voice": voice(vid)}) == (
            f"speak.voice: voice {vid} not found"
        )
    gone = designed(uv, "Gone")
    assert ok(uv.delete(f"{LIB}/{gone['id']}"), 204) is None
    assert refused(uv, sid, base | {"speak.voice": voice(gone["id"])}) == (
        f"speak.voice: voice {gone['id']} not found"
    )


def test_an_element_is_not_a_voice(uv, anon, sid):
    el = element(uv, "vox-el", [upload(uv, anon, png_bytes(8, 8))["id"]])
    assert refused(uv, sid, {"prompt.text": TEXT, "speak.voice": voice(el["id"])}) == (
        f"speak.voice: voice {el['id']} not found"
    )


def test_a_voice_that_does_not_fit(uv, sid):
    other = [x for x in by_id(uv, kind="voice", source="starter").values() if x["family"] != "echo"]
    if not other:
        pytest.skip("no live host publishes a starter voice of another family")
    x = other[0]
    assert refused(uv, sid, {"prompt.text": TEXT, "speak.voice": voice(x["id"])}) == (
        f"speak.voice: voice {x['name']} is for {x['family']}"
    )


def test_mentions_are_not_read_aloud(uv, anon, sid):
    element(uv, "mara", [upload(uv, anon, png_bytes(9, 9))["id"]], name="Mara")
    for text in ("Say hi to @mara and @nobody.", "@nobody"):  # known or not, once per param
        assert refused(uv, sid, {"prompt.text": text, "speak.voice": voice(ADA)}) == SPOKEN


def test_looks_do_not_fit_speech(uv, sid):
    mine = look(uv, "Speech look", "warm")
    inputs = {"prompt.text": TEXT, "speak.voice": voice(ADA)}
    assert refused(uv, sid, inputs, ["echo/soft-light"]) == (
        "looks[1]: look Soft light does not fit this workflow (looks do not apply to speech)"
    )
    assert refused(uv, sid, inputs, [mine["id"]]) == (
        "looks[1]: look Speech look does not fit this workflow (looks do not apply to speech)"
    )


# ---------------------------------------------------------------- the library


def test_starter_voices_are_listed_and_read_only(uv, api_b):
    want = {
        ADA: ("Ada", {"source": "preset", "family": "echo", "speaker": "ada", "language": "en"}),
        BO: ("Bo", {"source": "preset", "family": "echo", "speaker": "bo", "language": "zh",
                    "style": "calm and slow"}),
    }  # fmt: skip
    for client in (uv, api_b):
        got = by_id(client, kind="voice", source="starter")
        for vid, (name, info) in want.items():
            x = got[vid]
            assert {k: x[k] for k in ("kind", "group", "name", "handle", "text", "images")} == {
                "kind": "voice",
                "group": "preset",
                "name": name,
                "handle": None,
                "text": "",
                "images": [],
            }
            assert (x["source"], x["family"], x["version"], x["loras"], x["needs"]) == (
                "starter",
                "echo",
                1,
                [],
                [],
            )
            assert x["voice"] == info and x["thumb_url"] is None  # no previews
            assert ok(client.get(f"{LIB}/{vid}")) == x
    ada = ok(uv.get(f"{LIB}/{ADA}"))
    body = {k: ada[k] for k in ("group", "name", "text", "images", "version")}
    assert detail(uv.put(f"{LIB}/{ADA}", json=body | {"name": "Mine"}), 409) == READ_ONLY
    assert detail(uv.delete(f"{LIB}/{ADA}"), 409) == READ_ONLY
    assert ok(uv.get(f"{LIB}/{ADA}")) == ada
    assert uv.get(f"{LIB}/echo/nobody").status_code == 404


def test_list_order_and_filters(new_user):
    with make_client(headers(new_user("VL")["access_token"])) as c:
        mine = designed(c, "Zed")
        ids = [x["id"] for x in ok(c.get(LIB, params={"kind": "voice"}))["items"]]
        assert ids[0] == mine["id"] and ids.index(ADA) < ids.index(BO)
        assert all(x["kind"] == "voice" for x in ok(c.get(LIB, params={"kind": "voice"}))["items"])
        assert set(by_id(c, kind="voice", group="designed")) == {mine["id"]}
        assert {ADA, BO} <= set(by_id(c, kind="voice", group="preset"))
        assert ADA in by_id(c, kind="voice", q="ad") and mine["id"] not in by_id(c, q="ad")


def test_designed_voice_crud_with_versions(uv):
    x = designed(uv, "  Teen  ", design="  male, 17, tenor  ", language="en", style=" shy ")
    assert {k: x[k] for k in ("kind", "group", "name", "handle", "text", "images", "version")} == {
        "kind": "voice",
        "group": "designed",
        "name": "Teen",
        "handle": None,
        "text": "",
        "images": [],
        "version": 1,
    }
    assert (x["source"], x["family"], x["loras"], x["needs"], x["thumb_url"]) == (
        "mine",
        None,
        [],
        [],
        None,
    )
    assert x["voice"] == {"source": "designed", "design": "male, 17, tenor", "language": "en",
                          "style": "shy"}  # fmt: skip
    assert ok(uv.get(f"{LIB}/{x['id']}")) == x
    path = f"{LIB}/{x['id']}"
    bad = update(x, voice={"design": " "})
    assert detail(uv.put(path, json=bad), 422) == "voice.design must be 1-2000 characters"
    x2 = ok(uv.put(path, json=update(x, voice={"design": "female, 40, alto"}, name="Alto")))
    assert (x2["version"], x2["name"], x2["voice"]) == (
        2,
        "Alto",
        {"source": "designed", "design": "female, 40, alto"},
    )
    stale = uv.put(path, json=update(x2, version=1))
    assert detail(stale, 409) == "voice changed since you opened it (now version 2)"
    assert ok(uv.delete(path), 204) is None
    assert uv.get(path).status_code == 404 and uv.delete(path).status_code == 404


def test_voice_rules(uv, anon):
    pic = upload(uv, anon, png_bytes(7, 7))["id"]
    base = {"kind": "voice", "group": "designed", "name": "Rule", "voice": {"design": "deep"}}
    d = base["voice"]
    for body, want in (
        (base | {"group": "film"}, "group film is not a voice group"),
        (base | {"group": "preset"}, "preset voices come from packs"),
        (base | {"group": "cloned"}, "cloned voices are not available yet"),
        (base | {"handle": "rule"}, "a voice has no handle"),
        (base | {"text": "hello"}, "a voice has no text; describe it in voice.design"),
        (base | {"images": [{"take_id": pic}]}, "images: a voice has no images"),
        (base | {"loras": [{"lora": {"lora_id": NO_SUCH_ID}, "strength": 1}]},
         "loras: a voice has no LoRAs"),
        ({k: v for k, v in base.items() if k != "voice"}, "voice: a designed voice needs voice.design"),
        (base | {"voice": {"design": "   "}}, "voice.design must be 1-2000 characters"),
        (base | {"voice": {"design": "x" * 2001}}, "voice.design must be 1-2000 characters"),
        (base | {"voice": {"design": "a\u0000b"}},
         "voice.design must not contain control characters other than newlines and tabs"),
        (base | {"voice": d | {"language": "english"}},
         "voice.language: english is not a BCP 47 language tag"),
        (base | {"voice": d | {"style": "x" * 501}}, "voice.style must be at most 500 characters"),
        (base | {"from_take": {"take_id": pic}}, "from_take makes looks only"),
    ):  # fmt: skip
        assert detail(uv.post(LIB, json=body), 422) == want, body
    for name in ("", "   ", "x" * 81):
        assert "name must be 1-80 characters" in detail(
            uv.post(LIB, json=base | {"name": name}), 422
        )
    voiced = {"voice": {"design": "deep"}}
    el = {"kind": "element", "group": "character", "name": "El", "handle": "el-v",
          "images": [{"take_id": pic}]}  # fmt: skip
    lk = {"kind": "look", "group": "film", "name": "Lk", "text": "t"}
    for body in (el | voiced, lk | voiced):
        assert detail(uv.post(LIB, json=body), 422) == "voice: only a voice has voice fields"
    for design in ("é" * 2000, "line one\n\tline two"):  # code points; newlines and tabs are fine
        designed(uv, "Edge", design=design)
    for lang in ("en", "en-US", "zh", "pt-BR"):
        assert designed(uv, "Lang", language=lang)["voice"]["language"] == lang
    assert "style" not in designed(uv, "No style", style="")["voice"]


def test_voice_fit_in_the_list(uv, anon):
    mine = designed(uv, "Fit")
    el = element(uv, "fit-el", [upload(uv, anon, png_bytes(6, 6))["id"]])
    lk = look(uv, "Fit look", "warm")
    got = by_id(uv, workflow=WF)
    for vid in (ADA, BO, mine["id"]):
        assert (got[vid]["fits"], got[vid]["reason"]) == (True, None), vid
    assert (got[el["id"]]["fits"], got[el["id"]]["reason"]) == (
        False,
        "mentions are not read aloud; use a dialogue script",
    )
    for lid in (lk["id"], "echo/soft-light"):
        assert (got[lid]["fits"], got[lid]["reason"]) == (False, "looks do not apply to speech")
    image = by_id(uv, kind="voice", workflow="echo")
    for vid in (ADA, mine["id"]):
        assert (image[vid]["fits"], image[vid]["reason"]) == (False, "this workflow takes no voice")


# ---------------------------------------------------------------- isolation


def test_isolation(uv, api_b):
    x = designed(uv, "Private")
    path = f"{LIB}/{x['id']}"
    assert api_b.get(path).status_code == 404
    assert api_b.put(path, json=update(x, name="Taken")).status_code == 404
    assert api_b.delete(path).status_code == 404
    assert ok(uv.get(path)) == x  # untouched
    assert x["id"] not in by_id(api_b, kind="voice")
    b_sid = new_session(api_b, "Contract B voices")["id"]
    assert refused(api_b, b_sid, {"prompt.text": TEXT, "speak.voice": voice(x["id"])}) == (
        f"speak.voice: voice {x['id']} not found"
    )
