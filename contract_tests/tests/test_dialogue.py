"""Dialogue (API 1.32; contracts/README.md "Dialogue and Timeline audio") on the fake runner's `echo-dialogue`: a
character element's `voice_id` (every 422 and 404 text, a deleted voice reading null, another user's voice), a run
from a script of two voiced elements (the expanded lines, made_with's typed script, speakers and voices, the WAV and
its cues against check.py's echo_dialogue), every script 422 text (a narrowed `only` included), isolation, binds and
offers into a rows param of one media field (when a live workflow has one), `library.read` voices and an element's
voice through an Agent's tools, and a Write node with `form: script` (write-script/v1: the cast, a re-asked answer)
whose script a dialogue step speaks. Skipped when the fake runner has no echo-dialogue (before API 1.32).

Not covered: the Qwen3-TTS and ZONOS3 dialogue stages (a GPU; fixtures/voices/scripts.json in the services' tests),
a batch row's script (the batch's element copy), the cmd.run.execute lines (not visible over HTTP)."""

import io
import math
import uuid
import wave

import pytest
import schemas
from conftest import headers, make_client
from helpers import NO_SUCH_ID, STUDIO, check, detail, new_session, ok, png_bytes, upload, wait_run
from test_batches import BATCHES, finish
from test_director import answer, quiet, routed_agent, turn
from test_director import decisions as row_decisions
from test_director import script as agent_script
from test_elements import LIB, update
from test_pipelines import PIPES, create, ended, inputs_node
from test_pipelines import submit as submit_pipeline
from test_user_workflows import doc

WF, ASK, SPEAK = "echo-dialogue", "echo-ask", "echo-voice-over"
ADA, BO = "echo/ada", "echo/bo"
FAST = {"dialogue.delay_ms": 50}
SCRIPT = (
    "@mara (quiet): You said you'd be back by noon.\n@Bo: I know. The road was closed.\n\n"
    "@mara: Then you should have called."
)
ONLY = "this workflow cannot use a designed voice"


def voiced(client, handle: str, pic: str, voice_id, group: str = "character", status: int = 201):
    body = {"kind": "element", "group": group, "name": handle.capitalize(), "handle": handle,
            "text": "", "images": [{"take_id": pic}], "voice_id": voice_id}  # fmt: skip
    r = client.post(LIB, json=body)
    return ok(r, status) if status < 300 else detail(r, status)


def designed(client, name: str) -> dict:
    body = {"kind": "voice", "group": "designed", "name": name, "voice": {"design": "low and slow"}}
    return ok(client.post(LIB, json=body), 201)


def post_run(client, sid: str, inputs: dict, wid: str = WF):
    return client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": wid, "inputs": FAST | inputs}
    )


def refused(client, sid: str, script: str, wid: str = WF, **inputs) -> str:
    return detail(post_run(client, sid, {"dialogue.script": script} | inputs, wid), 422)


def line(voice_id: str, text: str, style: str = "", pause_s: float = 0) -> dict:
    return {"voice": {"voice_id": voice_id}, "text": text, "style": style, "pause_s": pause_s}


def spoken(client, sid: str, inputs: dict, wid: str = WF) -> dict:
    run = ok(post_run(client, sid, inputs, wid), 202)
    done = wait_run(client, run["id"])
    assert done["status"] == "succeeded", done
    (out,) = done["outputs"]
    return ok(client.get(f"{STUDIO}/takes/{out['take_id']}"))


def rows_media(client) -> list[dict]:
    """The live workflows of media video with one rows param of exactly one media field accepting audio (MiniMax H3
    References: `generate.audios`), each {doc, path, field}."""
    out = []
    for w in ok(client.get(f"{STUDIO}/workflows", params={"media": "video"}))["workflows"]:
        d = ok(client.get(f"{STUDIO}/workflows/{w['id']}"))
        for nid, n in sorted(d["nodes"].items()):
            for p in d["node_schemas"].get(n["kind"], {}).get("params", []):
                f = check.media_field(p)
                if (
                    f
                    and "audio" in p["item"][f].get("accepts", [])
                    and p["name"] not in n.get("in", {})
                ):
                    out.append({"doc": d, "path": f"{nid}.{p['name']}", "field": f})
    return out


@pytest.fixture(scope="module", autouse=True)
def dialogue(api) -> dict:
    r = api.get(f"{STUDIO}/workflows/{WF}")
    if r.status_code != 200:
        pytest.skip("the fake runner has no echo-dialogue workflow (before API 1.32)")
    return r.json()


@pytest.fixture(scope="module")
def ud(new_user):
    """User D: this module's cast (library.read lists at most 20, so a user of its own)."""
    with make_client(headers(new_user("Dlg")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def sid(ud) -> str:
    return new_session(ud, "Contract dialogue")["id"]


@pytest.fixture(scope="module")
def pic(ud, anon) -> str:
    return upload(ud, anon, png_bytes(16, 16))["id"]


@pytest.fixture(scope="module")
def cast(ud, pic) -> dict:
    """mara (Ada), bo (Bo), kai (a designed voice), ned (a character without one), harbour (a location)."""
    kai_voice = designed(ud, "Kai")
    return {
        "mara": voiced(ud, "mara", pic, ADA),
        "bo": voiced(ud, "bo", pic, BO),
        "kai": voiced(ud, "kai", pic, kai_voice["id"]),
        "ned": voiced(ud, "ned", pic, None),
        "harbour": voiced(ud, "harbour", pic, None, group="location"),
        "kai_voice": kai_voice,
    }


# ---------------------------------------------------------------- elements carry a voice


def test_an_element_carries_a_voice(ud, cast):
    m = cast["mara"]
    schemas.check_value(m, "#/components/schemas/LibraryItem", "a voiced element")
    assert m["voice_id"] == ADA and ok(ud.get(f"{LIB}/{m['id']}")) == m
    assert cast["kai"]["voice_id"] == cast["kai_voice"]["id"]
    assert (cast["ned"]["voice_id"], cast["harbour"]["voice_id"]) == (None, None)  # always sent
    listed = {x["id"]: x for x in ok(ud.get(LIB, params={"kind": "element"}))["items"]}
    assert listed[m["id"]]["voice_id"] == ADA and listed[cast["harbour"]["id"]]["voice_id"] is None
    got = ok(ud.put(f"{LIB}/{m['id']}", json=update(m, voice_id=BO)))
    assert (got["voice_id"], got["version"]) == (BO, m["version"] + 1)
    back = ok(ud.put(f"{LIB}/{m['id']}", json=update(got, voice_id=ADA)))
    assert back["voice_id"] == ADA
    cast["mara"] = back


def test_voice_id_rules(ud, api_b, pic, cast):
    only = "voice_id: only a character has a voice"
    for group in ("location", "prop"):
        assert voiced(ud, f"v{group}", pic, ADA, group=group, status=422) == only
    look = {"kind": "look", "group": "film", "name": "L", "text": "warm", "voice_id": ADA}
    assert detail(ud.post(LIB, json=look), 422) == only
    v = {
        "kind": "voice",
        "group": "designed",
        "name": "V",
        "voice": {"design": "deep"},
        "voice_id": ADA,
    }
    assert detail(ud.post(LIB, json=v), 422) == only
    h = cast["harbour"]
    assert detail(ud.put(f"{LIB}/{h['id']}", json=update(h, voice_id=ADA)), 422) == only
    b_voice = designed(api_b, "B's own")
    for vid in (NO_SUCH_ID, "echo/nobody", "echo/soft-light", cast["ned"]["id"], b_voice["id"]):
        assert voiced(ud, f"x{uuid.uuid4().hex[:6]}", pic, vid, status=404) == (
            f"voice_id: voice {vid} not found"
        )  # unknown, unpublished, a look, an element, another user's: one text
    n = cast["ned"]
    assert detail(ud.put(f"{LIB}/{n['id']}", json=update(n, voice_id=b_voice["id"])), 404) == (
        f"voice_id: voice {b_voice['id']} not found"
    )
    assert ok(ud.get(f"{LIB}/{n['id']}"))["voice_id"] is None  # untouched
    # B's library is shared by the session: leave it as found (test_elements counts B's own items)
    assert api_b.delete(f"{LIB}/{b_voice['id']}").status_code == 204


def test_a_deleted_voice_reads_null(ud, sid, pic):
    gone = designed(ud, "Short lived")
    el = voiced(ud, "sol", pic, gone["id"])
    assert ok(ud.delete(f"{LIB}/{gone['id']}"), 204) is None
    again = ok(ud.get(f"{LIB}/{el['id']}"))
    assert (
        again["voice_id"] is None and again["version"] == el["version"]
    )  # deleting never touches it
    saved = ok(ud.put(f"{LIB}/{el['id']}", json=update(again, voice_id=gone["id"])))
    assert saved["voice_id"] is None  # a stored id gone since saves null
    assert refused(ud, sid, "@sol: hello") == "dialogue.script line 1: @sol has no voice"


# ---------------------------------------------------------------- a dialogue from a script


def test_a_script_of_two_voiced_elements(ud, anon, sid, cast):
    take = spoken(ud, sid, {"dialogue.script": SCRIPT})
    schemas.check_value(take, "#/components/schemas/Take", "a dialogue take")
    lines = [
        line(ADA, "You said you'd be back by noon.", "quiet", 0.35),
        line(BO, "I know. The road was closed.", "", 0.7),  # an empty line doubles the pause
        line(ADA, "Then you should have called."),
    ]
    want = check.echo_dialogue(lines)
    mw = take["made_with"]
    assert mw["inputs"]["dialogue.script"] == "" and mw["inputs"]["dialogue.lines"] == lines
    assert mw["typed"]["dialogue.script"] == SCRIPT and mw["typed"]["dialogue.lines"] == []
    m, b = cast["mara"], cast["bo"]
    assert mw["elements"] == [
        {k: e[k] for k in ("id", "handle", "name", "version")} for e in (m, b)
    ]
    assert mw["voices"] == [
        {"id": ADA, "name": "Ada", "version": 1, "source": "preset"},
        {"id": BO, "name": "Bo", "version": 1, "source": "preset"},
    ]
    assert take["parents"] == []  # presets send no clip
    a = take["audio"]
    assert (take["kind"], a["sample_rate"]) == ("audio", 24000)
    assert math.isclose(a["duration_s"], want["duration_s"], abs_tol=1e-6)
    assert len(a["cues"]) == 3
    for got, cue in zip(a["cues"], want["cues"]):
        assert {k: got[k] for k in ("line", "voice_id", "text")} == {
            k: cue[k] for k in ("line", "voice_id", "text")
        }
        assert got["start_s"] == pytest.approx(cue["start_s"], abs=1e-6)
        assert got["end_s"] == pytest.approx(cue["end_s"], abs=1e-6)
    r = anon.get(take["url"])
    with wave.open(io.BytesIO(r.content)) as w:
        assert (w.getnchannels(), w.getsampwidth(), w.getframerate()) == (1, 2, 24000)
        assert w.getnframes() == round(want["duration_s"] * 24000)
    reuse = ok(ud.post(f"{STUDIO}/takes/{take['id']}/reuse"))
    assert (
        reuse["inputs"]["dialogue.script"] == SCRIPT
    )  # the typed view: re-expanded on the next run


def test_pauses_and_lines_given_directly(ud, sid):
    take = spoken(ud, sid, {"dialogue.script": "\n\n@bo: One.\r\n\r\n@bo: Two.\n@mara: Three.\n\n",
                            "dialogue.pause": 0.5})  # fmt: skip
    lines = [line(BO, "One.", "", 1.0), line(BO, "Two.", "", 0.5), line(ADA, "Three.")]
    assert take["made_with"]["inputs"]["dialogue.lines"] == lines
    assert take["audio"]["duration_s"] == pytest.approx(check.echo_dialogue(lines)["duration_s"])
    given = [line(BO, "Hi there.", "", 0.25), line(ADA, "Hello.")]
    take = spoken(ud, sid, {"dialogue.lines": given})
    assert [c["voice_id"] for c in take["audio"]["cues"]] == [BO, ADA]
    assert not take["made_with"].get("elements")
    r = post_run(ud, sid, {"dialogue.lines": [given[0] | {"voice": None}]})
    assert detail(r, 422) == "dialogue.lines[1].voice: a voice is required"


def test_every_script_refusal(ud, sid, cast, dialogue):
    bad = "mara: hi\n@mara:\n@jonny: hey\n@harbour: waves\n@ned (calm): ok\n@mara: fine"
    want = [
        "dialogue.script line 1: write @name: text or @name (style): text",
        "dialogue.script line 2: nothing to say",
        "dialogue.script line 3: unknown element @jonny",
        "dialogue.script line 4: @harbour has no voice",
        "dialogue.script line 5: @ned has no voice",
    ]
    assert refused(ud, sid, bad).split("; ") == want
    long = f"@mara: {'a' * 601}\n@mara ({'s' * 201}): ok"
    assert refused(ud, sid, long) == (
        "dialogue.script line 1: at most 600 characters (601); "
        "dialogue.script line 2: the style is at most 200 characters (201)"
    )
    for script in (" \n\n", ""):
        assert refused(ud, sid, script) == "dialogue.script: the script is empty"
    assert refused(ud, sid, "\n".join(["@bo: a"] * 41)) == "dialogue.lines: at most 40 lines"
    assert refused(ud, sid, "@mara: hi", **{"dialogue.lines": [line(ADA, "hi", "", 0.35)]}) == (
        "dialogue.lines: give the script or the lines, not both"
    )
    pinned = doc(dialogue) | {"title": "Presets and clips only"}
    pinned["nodes"]["dialogue"]["only"] = {"lines": ["preset", "clip"]}
    w = ok(ud.post(f"{STUDIO}/workflows", json=pinned), 201)
    assert refused(ud, sid, "@kai: Inside.\n@mara: Now.", w["id"]) == (
        f"dialogue.script line 1: voice Kai does not fit this workflow ({ONLY})"
    )
    take = spoken(ud, sid, {"dialogue.script": "@mara: Inside, now."}, w["id"])  # a preset fits
    assert take["audio"]["cues"][0]["voice_id"] == ADA


def test_isolation(ud, api_b, cast):
    b_sid = new_session(api_b, "Contract B dialogue")["id"]
    assert refused(api_b, b_sid, "@mara: hi") == "dialogue.script line 1: unknown element @mara"
    for path in (f"{LIB}/{cast['mara']['id']}", f"{LIB}/{cast['kai_voice']['id']}"):
        assert api_b.get(path).status_code == 404


# ---------------------------------------------------------------- rows of one media field (1.32)


def test_binds_and_offers_into_rows_of_one_media_field(ud, sid):
    targets = rows_media(ud)
    if not targets:
        pytest.skip(
            "no live workflow has a rows param of one media field taking audio (MiniMax H3)"
        )
    say = {"prompt.text": "One more line.", "speak.voice": {"voice_id": ADA}, "speak.delay_ms": 50}
    run = ok(
        ud.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": SPEAK, "inputs": say}), 202
    )
    done = wait_run(ud, run["id"])
    assert done["status"] == "succeeded", done
    tk = ok(ud.get(f"{STUDIO}/takes/{done['outputs'][0]['take_id']}"))
    facts = {"id": tk["id"], "kind": "audio", "width": None, "height": None,
             "duration_s": tk["audio"]["duration_s"], "has_audio": True}  # fmt: skip
    offers = {o["workflow_id"]: o for o in ok(ud.get(f"{STUDIO}/takes/{tk['id']}/uses"))["uses"]}
    for t in targets:
        d = {k: v for k, v in t["doc"].items() if v is not None}
        want = check.take_uses(facts, [d], d["node_schemas"])
        if want:
            assert offers[d["id"]] == want[0]
            (item,) = want[0]["inputs"][t["path"]]
            assert item[t["field"]]["take_id"] == tk["id"]
        for seeds, bad in ((1, False), (2, True)):
            p = {"title": "Voice to refs", "nodes": {
                "inputs": inputs_node("line"),
                "voice": {"kind": "step", "workflow": SPEAK, "seeds": seeds,
                          "inputs": {"speak.voice": {"voice_id": ADA}}, "bind": {"prompt.text": "$line"}},
                "refs": {"kind": "step", "workflow": d["id"], "seeds": 1, "bind": {t["path"]: "voice.takes"}},
                "out": {"kind": "output", "from": "refs.takes", "collection": "x"}},
                "edges": [["inputs", "voice"], ["voice", "refs"], ["refs", "out"]]}  # fmt: skip
            got = ok(ud.post(f"{PIPES}/validate", json=p))["problems"]
            hit = [
                x
                for x in got
                if (x["code"], x.get("node"), x.get("port")) == ("bind_type", "refs", t["path"])
            ]
            assert bool(hit) == bad, got


# ---------------------------------------------------------------- agents: library.read and form script


@pytest.fixture(scope="module")
def ask(ud) -> None:
    if ud.get(f"{STUDIO}/workflows/{ASK}").status_code != 200:
        pytest.skip("the fake runner has no echo-ask workflow")


def test_library_read_lists_voices(ud, cast, ask):
    t = uuid.uuid4().hex[:8]
    mine = designed(ud, f"{t} narrator")  # text matches a name by prefix (as GET /library q)
    p = create(ud, routed_agent("Contract voices read", tools=["library.read"]))
    t1 = turn(
        "reading",
        ("library_read", {"kind": "voice", "text": "Ada"}),
        ("library_read", {"kind": "voice", "text": t}),
        ("library_read", {"kind": "element", "text": "Mara"}),
        ("library_read", {"kind": "element", "group": "location"}),
    )
    t2 = turn("", ("route", {"to": "skip", "reason": "read only"}))
    b = submit_pipeline(ud, p["id"], [agent_script(f"cast {t}", t1, t2)])
    try:
        r = ended(ud, b["id"], 1)
    finally:
        finish(ud, b["id"])
    assert r["status"] == "succeeded", r
    (d,) = row_decisions(ud, b["id"], 1)
    calls = d["agent"]["turns"][0]["calls"]
    assert all(c["error"] is None for c in calls), calls
    ada = {i["id"]: i for i in calls[0]["result"]["items"]}[ADA]
    assert {k: ada[k] for k in ("kind", "group", "name", "family")} == {
        "kind": "voice", "group": "preset", "name": "Ada", "family": "echo"}  # fmt: skip
    (own,) = calls[1]["result"]["items"]
    assert (own["id"], own["group"], own["family"]) == (mine["id"], "designed", None)
    (mara,) = calls[2]["result"]["items"]
    assert (mara["id"], mara["handle"], mara["voice"]) == (cast["mara"]["id"], "mara", "Ada")
    (harbour,) = calls[3]["result"]["items"]
    assert harbour["voice"] is None  # who cannot speak
    assert ud.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_write_form_script(ud, cast, ask):
    """inputs (prompt, script) → writer (form script, $prompt → $script) → scene (echo-dialogue ← $script) → out.
    Row 1 answers a good script; row 2 a line for @harbour (not in the cast: no voice), re-asked once."""
    t = uuid.uuid4().hex[:8]
    good = f"@mara (quiet): Noon, {t}.\n\n@bo: Closed."
    title = f"Contract script {t}"
    p = create(ud, {"title": title, "nodes": {
        "inputs": inputs_node("prompt", "script"),
        "writer": {"kind": "write", "from": ["$prompt"], "to": "$script", "form": "script", "model": ASK,
                   "instruction": "Write the scene as a short dialogue for the cast."},
        "scene": {"kind": "step", "workflow": WF, "inputs": FAST, "bind": {"dialogue.script": "$script"}},
        "out": {"kind": "output", "from": "scene.takes", "collection": title}},
        "edges": [["inputs", "writer"], ["writer", "scene"], ["scene", "out"]]})  # fmt: skip
    assert p["problems"] == [], p["problems"]
    once_bad = {"script": f"@mara: Noon, {t}.\n@harbour: Waves.", "note": "x"}
    rows = [
        [answer(f"scene {t}", {"script": good, "note": "two lines"}), "(written)"],
        [answer(f"again {t}", once_bad) + f" ECHO-RETRY: {quiet({'script': good, 'note': 'again'})}",
         "(written)"],
    ]  # fmt: skip
    body = {"pipeline_id": p["id"], "rows": {"table": {"columns": ["inputs.prompt", "inputs.script"],
                                                       "rows": rows}}}  # fmt: skip
    b = ok(ud.post(BATCHES, json=body), 202)
    try:
        one, two = ended(ud, b["id"], 1), ended(ud, b["id"], 2)
    finally:
        finish(ud, b["id"])
    for n, r in ((1, one), (2, two)):
        assert r["status"] == "succeeded" and len(r["take_ids"]) == 1, (r.get("error"), r)
        (d,) = row_decisions(ud, b["id"], n)
        assert (d["kind"], d["template"]) == ("write", "write-script/v1")
        assert d["write"]["to"] == "$script" and d["write"]["after"] == good
        assert d["write"]["before"] == "(written)"
        assert "@mara — Mara, voice Ada" in d["prompt"] and "@bo — Bo, voice Bo" in d["prompt"]
        assert "@ned —" not in d["prompt"] and "@harbour —" not in d["prompt"]  # no voice: not cast
        take = ok(ud.get(f"{STUDIO}/takes/{r['take_ids'][0]}"))
        assert take["made_with"]["typed"]["dialogue.script"] == good
        assert [c["voice_id"] for c in take["audio"]["cues"]] == [ADA, BO]
    assert ud.delete(f"{PIPES}/{p['id']}").status_code == 204
