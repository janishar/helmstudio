"""Take isolation on every path that takes a take (contracts/README.md "Image inputs", "Media inputs", Timeline, Elements,
Looks; the robust check pass of docs/backlog.md): user B naming one of user A's takes (an uploaded image, a run's
image, a video, an audio upload) in a run's media param and its rows of images (and the echo-ask rows of Media), a
batch row (table and lines), a run and a batch of B's own saved workflow (and a saved graph or preset holding the
take), a pipeline batch's Image input, a pipeline step's fixed inputs and a gate pick, "Use in…" (POST …/use and GET
…/uses) and the other take routes, a timeline (create, add clips, a written document, alternatives), a clip action and
a new shot, an element and a look (images, from_take); then the reverse (A naming B's takes).

The contract's answer is 422 `<path>: take <id> not found` in inputs and 404 on take routes, and B must learn
nothing: every answer is compared with the answer to a random id nobody has (the same status and body once the id is
replaced), so no kind, size, duration, sound, lineage or other id can leak through a different text. At the end A's
takes have no new children and B's gallery and sessions hold none of A's takes. Fake mode (echo families), no GPU.
Not covered here: rows of media (API 1.22, MiniMax H3 only: needs a live native host; test_minimax_h3.py) and a top-level
`image` param (no echo workflow has one)."""

import json
import uuid

import pytest
from helpers import QUICK, STUDIO, all_takes, new_session, ok, png_bytes, ref, upload
from test_batches import BATCHES, finish, lines
from test_music_sound import upload_audio
from test_pipelines import PIPES, gated, parked
from test_pipelines import create as create_pipeline
from test_pipelines import submit as submit_pipeline
from test_timeline import TL, clips, create, get, video_inputs
from test_user_workflows import WF, doc
from test_video_inputs import CLIP
from test_video_source import A2V, EXTEND, FAST, FAST_A2V, FAST_EXT, RETAKE, VIDEO, made, wav

I2V = "echo-image-to-video"
LIB = f"{STUDIO}/library"
ECHO = {"echo.steps": 2, "echo.delay_ms": 50}
SPAN = {"retake.span": {"start": 0, "end": 1}}
KINDS = ("image", "output", "video", "audio")


# ---------------------------------------------------------------- helpers


def nobody() -> str:
    return str(uuid.uuid4())


def norm(r, tid: str) -> tuple:
    return r.status_code, r.text.replace(tid, "<id>")


def outcome(r, tid: str) -> tuple:
    """A refusal as status and body with the id replaced; an acceptance as its status only (its body has new ids)."""
    return (r.status_code, None) if r.status_code < 400 else norm(r, tid)


def blind(call, tid: str, status: int | None = None) -> str:
    """call(id) with tid and with a random id must answer the same (body with the id replaced): tid tells nothing.
    Returns the answer's text for tid."""
    other = nobody()
    r, r0 = call(tid), call(other)
    assert norm(r, tid) == norm(r0, other), f"{tid}: {r.status_code} {r.text} | unknown: {r0.text}"
    if status is not None:
        assert r.status_code == status, f"{r.status_code} {r.text}"
    return r.json().get("detail", "") if r.content else ""


def run_call(client, sid: str, wf: str, inputs):
    """inputs(tid) → POST …/runs as a callable of the take id."""
    return lambda t: client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": wf, "inputs": inputs(t)}
    )


def takes_of(client, owner_takes: dict) -> dict:
    return {k: ok(client.get(f"{STUDIO}/takes/{v}")) for k, v in owner_takes.items()}


def made_takes(client, anon, sid: str, who: str) -> dict:
    """kind → take id: an uploaded PNG, an echo run's image, an echo-video clip (with sound), a 3 s WAV."""
    out = made(
        client, sid, "echo", QUICK | {"prompt.text": f"{who}'s still {uuid.uuid4().hex[:6]}"}
    )
    vid = made(client, sid, VIDEO, video_inputs(text=f"{who}'s clip {uuid.uuid4().hex[:6]}"))
    return {
        "image": upload(client, anon, png_bytes(64, 64), f"{who}.png")["id"],
        "output": out["id"],
        "video": vid["id"],
        "audio": wav(client, anon)["id"],
    }


# ---------------------------------------------------------------- fixtures


@pytest.fixture(scope="module", autouse=True)
def echo_media(api):
    listed = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if not {RETAKE, A2V, EXTEND, VIDEO, I2V} <= listed:
        pytest.skip("the fake runner has no echo media workflows (before API 1.19)")


@pytest.fixture(scope="module")
def a_sid(api) -> str:
    return new_session(api, "Contract take isolation A")["id"]


@pytest.fixture(scope="module")
def b_sid(api_b) -> str:
    return new_session(api_b, "Contract take isolation B")["id"]


@pytest.fixture(scope="module")
def a(api, anon, a_sid) -> dict:
    """A's takes, and each one as A read it before B tried anything (children included)."""
    ids = made_takes(api, anon, a_sid, "A")
    return {"ids": ids, "before": takes_of(api, ids)}


@pytest.fixture(scope="module")
def b(api_b, anon) -> dict:
    sid = new_session(api_b, "Contract take isolation B's own")["id"]
    return made_takes(api_b, anon, sid, "B")


# ---------------------------------------------------------------- runs


MEDIA = [  # workflow, param, base inputs, extra values that would need the take's facts (end, out past it)
    (RETAKE, "retake.source", FAST | SPAN, {}),
    (RETAKE, "retake.source", FAST | {"retake.span": {"start": 0, "end": 99}}, {}),
    (A2V, "a2v.source", FAST_A2V, {}),
    (A2V, "a2v.source", FAST_A2V, {"in": 0, "out": 99}),
    (A2V, "a2v.source", FAST_A2V, {"in": 0.5, "out": 1.5}),
    (EXTEND, "extend.source", FAST_EXT, {}),
]


@pytest.mark.parametrize(("wf", "param", "base", "extra"), MEDIA)
@pytest.mark.parametrize("kind", KINDS)
def test_media_param_of_a_run(api_b, b_sid, a, wf, param, base, extra, kind):
    t = a["ids"][kind]
    call = run_call(api_b, b_sid, wf, lambda x: base | {param: {"take_id": x} | extra})
    detail = blind(call, t, 422)
    if not extra and "99" not in json.dumps(base):
        assert detail == f"{param}: take {t} not found"


ROWS = [  # workflow, rows param, base inputs
    ("echo", "echo.refs", ECHO | {"prompt.text": "b's refs"}),
    (VIDEO, "video.refs", video_inputs(text="b's video refs")),
    (I2V, "keyframes.images", CLIP),
]


@pytest.mark.parametrize(("wf", "param", "base"), ROWS)
@pytest.mark.parametrize("kind", KINDS)
def test_image_rows_of_a_run(api_b, b_sid, a, b, wf, param, base, kind):
    t = a["ids"][kind]
    one = run_call(api_b, b_sid, wf, lambda x: base | {param: [ref(x)]})
    assert blind(one, t, 422) == f"{param}[1].image: take {t} not found"
    if wf != I2V:  # two keyframe rows at 0 s are refused for the frame first
        two = run_call(api_b, b_sid, wf, lambda x: base | {param: [ref(b["image"]), ref(x)]})
        assert blind(two, t, 422) == f"{param}[2].image: take {t} not found"


@pytest.mark.parametrize("kind", KINDS)
def test_media_rows_of_an_ask_run(api_b, b_sid, a, kind):
    """echo-ask's rows of Media items (image or video): whatever studio answers, A's take reads as no take."""
    t = a["ids"][kind]
    call = run_call(api_b, b_sid, "echo-ask", lambda x: {"ask.media": [{"take": {"take_id": x}}]})
    assert blind(call, t) is not None
    assert call(t).status_code in (404, 422)


# ---------------------------------------------------------------- batches


def batch_call(client, wf: str, rows, inputs: dict):
    return lambda t: client.post(
        BATCHES, json={"workflow_id": wf, "rows": rows(t), "inputs": inputs}
    )


@pytest.mark.parametrize("kind", KINDS)
def test_batch_rows(api_b, a, b, kind):
    t = a["ids"][kind]
    before = len(ok(api_b.get(BATCHES))["batches"])
    for wf, param, base, extra in (MEDIA[0], MEDIA[2], MEDIA[4], MEDIA[5]):
        table = lambda x, p=param, e=extra: {
            "table": {"columns": [p], "rows": [[{"take_id": x} | e]]}
        }
        assert (
            blind(batch_call(api_b, wf, table, base), t, 422)
            == f"row 1: {param}: take {t} not found"
        )
    for wf, param, base in ROWS:
        table = lambda x, p=param: {"table": {"columns": [p], "rows": [[[ref(x)]]]}}
        detail = blind(batch_call(api_b, wf, table, base), t, 422)
        assert detail == f"row 1: {param}[1].image: take {t} not found"
    by_lines = lambda x: lines([x], "echo.refs")
    detail = blind(batch_call(api_b, "echo", by_lines, ECHO | {"prompt.text": "x"}), t, 422)
    assert f"take {t} not found" in detail
    # row 1 is B's own and fits: only row 2 is named
    mixed = lambda x: {
        "table": {
            "columns": ["retake.source"],
            "rows": [[{"take_id": b["video"]}], [{"take_id": x}]],
        }
    }
    detail = blind(batch_call(api_b, RETAKE, mixed, FAST | SPAN), t, 422)
    assert detail == f"row 2: retake.source: take {t} not found"
    grid = lambda x: {
        "grid": {
            "axes": [
                {"param": "retake.source", "values": [{"take_id": b["video"]}, {"take_id": x}]}
            ]
        }
    }
    detail = blind(batch_call(api_b, RETAKE, grid, FAST | SPAN), t, 422)
    assert detail == f"row 2: retake.source: take {t} not found"
    assert len(ok(api_b.get(BATCHES))["batches"]) == before  # nothing was made


# ---------------------------------------------------------------- B's own saved workflows


@pytest.fixture(scope="module")
def b_wfs(api_b):
    made_ = {
        w: ok(api_b.post(WF, json={"based_on": w, "title": f"B's {w}"}), 201)
        for w in (RETAKE, "echo")
    }
    yield made_
    for w in made_.values():
        api_b.delete(f"{WF}/{w['id']}")


@pytest.mark.parametrize("kind", KINDS)
def test_saved_workflow_run_and_batch(api_b, b_sid, a, b_wfs, kind):
    t = a["ids"][kind]
    rt, echo = b_wfs[RETAKE]["id"], b_wfs["echo"]["id"]
    call = run_call(api_b, b_sid, rt, lambda x: FAST | SPAN | {"retake.source": {"take_id": x}})
    assert blind(call, t, 422) == f"retake.source: take {t} not found"
    call = run_call(
        api_b, b_sid, echo, lambda x: ECHO | {"prompt.text": "x", "echo.refs": [ref(x)]}
    )
    assert blind(call, t, 422) == f"echo.refs[1].image: take {t} not found"
    table = lambda x: {"table": {"columns": ["retake.source"], "rows": [[{"take_id": x}]]}}
    detail = blind(batch_call(api_b, rt, table, FAST | SPAN), t, 422)
    assert detail == f"row 1: retake.source: take {t} not found"


@pytest.mark.parametrize("kind", KINDS)
def test_saved_graph_or_preset_holding_a_take(api_b, a, b, b_wfs, kind):
    """A workflow holds no take: saving one with A's take in a param or a preset answers as with an id nobody has
    and as with B's own take (a saved one is deleted again)."""
    t, mine = a["ids"][kind], b[kind]
    base = doc(b_wfs[RETAKE])

    def save(d):
        r = api_b.post(WF, json=d)
        if r.status_code == 201:
            api_b.delete(f"{WF}/{r.json()['id']}")
        return r

    def in_param(x):
        d = json.loads(json.dumps(base))
        d["nodes"]["retake"].setdefault("params", {})["source"] = {"take_id": x}
        return save(d)

    def in_preset(x):
        d = json.loads(json.dumps(base))
        d["studio"] = (d.get("studio") or {}) | {
            "presets": {"mine": {"retake.source": {"take_id": x}}}
        }
        return save(d)

    for call in (in_param, in_preset):
        other = nobody()
        assert outcome(call(t), t) == outcome(call(other), other)
        assert outcome(call(t), t) == outcome(call(mine), mine)


# ---------------------------------------------------------------- pipelines


def image_pipeline(title: str) -> dict:
    return {
        "title": title,
        "nodes": {
            "inputs": {
                "kind": "inputs",
                "inputs": [{"name": "prompt", "type": "Text"}, {"name": "image", "type": "Image"}],
            },
            "s1": {
                "kind": "step",
                "workflow": "echo",
                "inputs": ECHO,
                "bind": {"prompt.text": "$prompt", "echo.refs": "$image"},
            },
            "out": {"kind": "output", "from": "s1.takes", "collection": title},
        },
        "edges": [["inputs", "s1"], ["s1", "out"]],
    }


@pytest.fixture(scope="module")
def b_pipe(api_b):
    p = create_pipeline(api_b, image_pipeline("B's image pipeline"))
    yield p
    api_b.delete(f"{PIPES}/{p['id']}")


@pytest.mark.parametrize("kind", KINDS)
def test_pipeline_image_input(api_b, a, b_pipe, kind):
    t = a["ids"][kind]
    before = len(ok(api_b.get(BATCHES))["batches"])
    table = lambda x: {
        "table": {"columns": ["inputs.prompt", "inputs.image"], "rows": [["x", {"take_id": x}]]}
    }
    call = lambda x: api_b.post(BATCHES, json={"pipeline_id": b_pipe["id"], "rows": table(x)})
    assert blind(call, t, 422) == f"row 1: inputs.image: take {t} not found"
    assert len(ok(api_b.get(BATCHES))["batches"]) == before


@pytest.mark.parametrize("kind", KINDS)
def test_pipeline_step_inputs_holding_a_take(api_b, a, b, kind):
    """A step's fixed inputs hold no take: A's take answers as B's own (at save, validate and submit)."""
    t, mine = a["ids"][kind], b[kind]

    def body(x):
        p = image_pipeline("B's step with a take")
        p["nodes"]["s1"]["inputs"] = ECHO | {"echo.refs": [ref(x)]}
        p["nodes"]["s1"]["bind"] = {"prompt.text": "$prompt"}
        p["nodes"]["s2"] = {
            "kind": "step",
            "workflow": RETAKE,
            "inputs": FAST | SPAN | {"retake.source": {"take_id": x}},
        }
        p["nodes"]["out"]["from"] = "s2.takes"
        p["edges"] = [["inputs", "s1"], ["s1", "s2"], ["s2", "out"]]
        return p

    def validate(x):
        return api_b.post(f"{PIPES}/validate", json=body(x))

    def save(x):
        r = api_b.post(PIPES, json=body(x))
        if r.status_code != 201:
            return r
        pid = r.json()["id"]
        sub = api_b.post(
            BATCHES,
            json={
                "pipeline_id": pid,
                "rows": {
                    "table": {
                        "columns": ["inputs.prompt", "inputs.image"],
                        "rows": [["x", {"take_id": mine}]],
                    }
                },
            },
        )
        if sub.status_code == 202:
            finish(api_b, sub.json()["id"])
        api_b.delete(f"{PIPES}/{pid}")
        return sub  # what B learns is the submit's answer

    for call in (validate, save):
        other = nobody()
        assert outcome(call(t), t) == outcome(call(other), other)
        assert outcome(call(t), t) == outcome(call(mine), mine)


def test_gate_pick_of_a_take(api_b, a):
    """B picks A's take at its own parked gate: refused as an id nobody has."""
    p = create_pipeline(api_b, gated("B's gate"))
    batch = submit_pipeline(api_b, p["id"], [f"b's still {uuid.uuid4().hex[:6]}"])
    try:
        parked(api_b, batch["id"], [1])
        path = f"{BATCHES}/{batch['id']}/rows/1/pick"
        for kind in KINDS:
            t = a["ids"][kind]
            call = lambda x: api_b.post(path, json={"gate": "review", "take_ids": [x]})
            assert blind(call, t, 422)
    finally:
        finish(api_b, batch["id"])
        api_b.delete(f"{PIPES}/{p['id']}")


# ---------------------------------------------------------------- take routes, Use in…


def test_take_routes_are_404(api, api_b, anon, a, b_wfs):
    pending = upload(api, anon, png_bytes(4, 4), complete=False)["take_id"]
    mine = b_wfs[RETAKE]["id"]
    calls = (
        [
            ("GET", "", None),
            ("PATCH", "", {"starred": True}),
            ("DELETE", "", None),
            ("POST", "/reuse", None),
            ("GET", "/uses", None),
            ("POST", "/pipeline", None),
            ("GET", "/decisions", None),
            ("POST", "/frame", {"at": "first"}),
            ("POST", "/frame", {"at": "last"}),
        ]
        + [
            ("POST", "/use", {"workflow_id": w})
            for w in ("echo", RETAKE, A2V, EXTEND, I2V, VIDEO, mine, "no-such")
        ]
        + [
            ("POST", "/use", {"workflow_id": "echo", "param": "echo.refs"}),
            ("POST", "/use", {"workflow_id": RETAKE, "param": "retake.source"}),
            ("POST", "/use", {"workflow_id": "echo", "param": "echo.steps"}),
        ]
    )
    for t in [*a["ids"].values(), pending]:
        for method, sub, body in calls:
            call = lambda x, m=method, s=sub, j=body: api_b.request(
                m, f"{STUDIO}/takes/{x}{s}", json=j
            )
            blind(call, t, 404)
        complete = lambda x: api_b.post(f"{STUDIO}/uploads/{x}/complete")
        blind(complete, t, 404)
    assert (
        api.post(f"{STUDIO}/uploads/{pending}/complete").status_code == 409
    )  # still A's, not received


@pytest.fixture(scope="module")
def a_wfs(api):
    """A's own copies of echo-retake, echo-a2v and echo: B's offers must never list them."""
    made_ = [
        ok(api.post(WF, json={"based_on": w, "title": f"A's {w}"}), 201)
        for w in (RETAKE, A2V, "echo")
    ]
    yield [w["id"] for w in made_]
    for w in made_:
        api.delete(f"{WF}/{w['id']}")


def test_offers_hold_only_own_workflows(api, api_b, a, b, a_wfs, b_wfs):
    """GET …/uses of B's own takes lists B's workflows, never A's; POST …/use naming A's workflow is the answer to a
    workflow nobody has."""
    for kind in KINDS:
        got = ok(api_b.get(f"{STUDIO}/takes/{b[kind]}/uses"))
        assert not [w for w in a_wfs if w in json.dumps(got)], got
        for w in a_wfs:
            call = lambda x, t=b[kind]: api_b.post(
                f"{STUDIO}/takes/{t}/use", json={"workflow_id": x}
            )
            blind(call, w)
    mine = {o["workflow_id"] for o in ok(api_b.get(f"{STUDIO}/takes/{b['video']}/uses"))["uses"]}
    assert b_wfs[RETAKE]["id"] in mine, (
        mine
    )  # B's own copy is offered: the check above is not vacuous
    theirs = {
        o["workflow_id"] for o in ok(api.get(f"{STUDIO}/takes/{a['ids']['video']}/uses"))["uses"]
    }
    assert set(a_wfs[:2]) <= theirs and not {w["id"] for w in b_wfs.values()} & theirs


# ---------------------------------------------------------------- timeline, clip actions, shots


@pytest.fixture(scope="module")
def b_tl(api_b, b) -> dict:
    tl = create(api_b, name="B's isolation sequence", take_ids=[b["video"]])
    yield tl
    api_b.delete(f"{TL}/{tl['id']}")


@pytest.mark.parametrize("kind", KINDS)
def test_timeline_clips(api_b, a, b, b_tl, kind):
    t = a["ids"][kind]
    new = lambda x: api_b.post(TL, json={"take_ids": [x]})
    assert blind(new, t, 422) == f"take_ids[0]: take {t} not found"
    two = lambda x: api_b.post(TL, json={"take_ids": [b["video"], x]})
    assert blind(two, t, 422) == f"take_ids[1]: take {t} not found"
    tl = get(api_b, b_tl)
    add = lambda x: api_b.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [x]})
    assert blind(add, t, 422) == f"take_ids[0]: take {t} not found"
    add_a2 = lambda x: api_b.post(f"{TL}/{tl['id']}/clips", json={"take_ids": [x], "track": "A2"})
    blind(add_a2, t)
    vid = {"id": "v", "take_id": b["video"], "in": 0, "out": 1}
    docs = [
        [{"kind": "video", "clips": [{"id": "x", "take_id": "ID", "in": 0, "out": 1}]}],
        [{"kind": "video", "clips": [{"id": "x", "take_id": "ID", "hold": 1}]}],
        [{"kind": "video", "clips": [vid | {"alternatives": [b["video"], "ID"]}]}],
        [
            {"kind": "video", "clips": [vid]},
            {"kind": "audio", "clips": [{"id": "a", "take_id": "ID", "in": 0, "out": 1, "at": 0}]},
        ],
    ]
    for tracks in docs:
        body = lambda x, tr=tracks: {
            "revision": tl["revision"],
            "target": tl["target"],
            "tracks": json.loads(json.dumps(tr).replace('"ID"', json.dumps(x))),
        }
        call = lambda x, bd=body: api_b.put(f"{TL}/{tl['id']}", json=bd(x))
        blind(call, t, 422)
        codes = {p["code"] for p in call(t).json()["problems"]}
        assert codes == {"take_not_found"}, call(t).text
    assert get(api_b, b_tl)["revision"] == tl["revision"]  # nothing written


@pytest.mark.parametrize("kind", ("video", "audio", "image"))
def test_clip_action_and_shot_with_a_take(api_b, a, b, b_tl, a_wfs, kind):
    """B's own clip's actions (retake, audio to video, extend) and a new shot, run with A's take swapped in: refused,
    no shot placed."""
    t = a["ids"][kind]
    tl = get(api_b, b_tl)
    (c,) = clips(tl)
    offers = ok(api_b.get(f"{TL}/{tl['id']}/clips/{c['id']}/actions"))["actions"]
    by_wf = {o["workflow_id"]: o for o in offers}
    assert not [w for w in a_wfs if w in json.dumps(offers)], (
        offers
    )  # A's own workflows are never offered
    assert {RETAKE, A2V, EXTEND} <= set(by_wf), offers
    base = {RETAKE: FAST, A2V: FAST_A2V, EXTEND: FAST_EXT}
    for wf, fast in base.items():
        o = by_wf[wf]
        assert json.dumps(o).count(b["video"]) >= 1  # B's own take, nothing else
        swapped = lambda x, o=o: json.loads(json.dumps(o["inputs"]).replace(b["video"], x))
        for slot in ({"clip": c["id"]}, {"after": c["id"], "length": 1.0}):
            call = lambda x, w=wf, f=fast, s=slot, sw=swapped: api_b.post(
                f"{TL}/{tl['id']}/shots",
                json={"slot": s, "run": {"workflow_id": w, "inputs": f | sw(x)}},
            )
            detail = blind(call, t, 422)
            assert f"take {t} not found" in detail, detail
    shot = lambda x: api_b.post(
        f"{TL}/{tl['id']}/shots",
        json={
            "slot": {"at": 0, "length": 1.0},
            "run": {"workflow_id": I2V, "inputs": CLIP | {"keyframes.images": [ref(x)]}},
        },
    )
    assert blind(shot, t, 422) == f"keyframes.images[1].image: take {t} not found"
    after = get(api_b, b_tl)
    assert after["revision"] == tl["revision"] and all("shot" not in x for x in clips(after))


# ---------------------------------------------------------------- elements and looks


@pytest.mark.parametrize("kind", KINDS)
def test_element_and_look_images(api_b, a, b, kind):
    t = a["ids"][kind]
    tag = uuid.uuid4().hex[:8]
    el = lambda x: api_b.post(
        LIB,
        json={
            "kind": "element",
            "group": "character",
            "name": "E",
            "handle": f"iso-{tag}",
            "images": [{"take_id": x}],
        },
    )
    assert blind(el, t, 404) == f"images[1]: take {t} not found"
    lk = lambda x: api_b.post(
        LIB, json={"kind": "look", "group": "film", "name": "L", "images": [{"take_id": x}]}
    )
    assert blind(lk, t, 404) == f"images[1]: take {t} not found"
    from_take = lambda x: api_b.post(
        LIB, json={"kind": "look", "group": "film", "name": "L", "from_take": {"take_id": x}}
    )
    assert blind(from_take, t, 404) == f"from_take: take {t} not found"
    mine = ok(
        api_b.post(
            LIB,
            json={
                "kind": "element",
                "group": "prop",
                "name": "Mine",
                "handle": f"own-{tag}",
                "images": [{"take_id": b["image"]}],
            },
        ),
        201,
    )
    try:
        put = lambda x: api_b.put(
            f"{LIB}/{mine['id']}",
            json={
                "group": "prop",
                "name": "Mine",
                "handle": f"own-{tag}",
                "text": "",
                "version": mine["version"],
                "images": [{"take_id": b["image"]}, {"take_id": x}],
            },
        )
        assert blind(put, t, 404) == f"images[2]: take {t} not found"
        assert ok(api_b.get(f"{LIB}/{mine['id']}"))["version"] == mine["version"]
    finally:
        api_b.delete(f"{LIB}/{mine['id']}")


# ---------------------------------------------------------------- music and sound (API 1.79)


def test_mux_with_another_users_take_is_404(api_b, a, b):
    """Save with the video: A's take as the audio (B's video in the body) or as the video (B's audio) is 404, the same
    as a take nobody has; A's takes get no child (test_nothing_changed_or_leaked)."""
    for t in a["ids"].values():
        sound = lambda x: api_b.post(f"{STUDIO}/takes/{x}/mux", json={"video_take_id": b["video"]})
        blind(sound, t, 404)
        picture = lambda x: api_b.post(
            f"{STUDIO}/takes/{b['audio']}/mux", json={"video_take_id": x}
        )
        blind(picture, t, 404)


@pytest.mark.parametrize("kind", ["music", "sound", "voice", "audio"])
def test_the_class_filter_never_shows_another_users_takes(api, api_b, anon, a, kind):
    """A's audio takes of each class (an upload with `sound`; the WAV of `a` is voice) never in B's ?kind=<class>."""
    mine = upload_audio(api, anon, sound=None if kind in ("voice", "audio") else kind)
    assert {mine["id"], a["ids"]["audio"]} & {t["id"] for t in all_takes(api, kind=kind)}
    seen = {t["id"] for t in all_takes(api_b, kind=kind)}
    assert not seen & {mine["id"], *a["ids"].values()}


# ---------------------------------------------------------------- the reverse, and what A sees afterwards


@pytest.mark.parametrize("kind", KINDS)
def test_a_cannot_use_b_takes(api, a_sid, b, kind):
    t = b[kind]
    call = run_call(api, a_sid, RETAKE, lambda x: FAST | SPAN | {"retake.source": {"take_id": x}})
    assert blind(call, t, 422) == f"retake.source: take {t} not found"
    call = run_call(api, a_sid, A2V, lambda x: FAST_A2V | {"a2v.source": {"take_id": x}})
    assert blind(call, t, 422) == f"a2v.source: take {t} not found"
    call = run_call(
        api, a_sid, "echo", lambda x: ECHO | {"prompt.text": "x", "echo.refs": [ref(x)]}
    )
    assert blind(call, t, 422) == f"echo.refs[1].image: take {t} not found"
    table = lambda x: {"table": {"columns": ["retake.source"], "rows": [[{"take_id": x}]]}}
    assert (
        blind(batch_call(api, RETAKE, table, FAST | SPAN), t, 422)
        == f"row 1: retake.source: take {t} not found"
    )
    for method, sub, body in (
        ("GET", "/uses", None),
        ("POST", "/use", {"workflow_id": RETAKE}),
        ("POST", "/use", {"workflow_id": "echo"}),
        ("POST", "/frame", {"at": "first"}),
        ("POST", "/reuse", None),
        ("GET", "", None),
    ):
        call = lambda x, m=method, s=sub, j=body: api.request(m, f"{STUDIO}/takes/{x}{s}", json=j)
        blind(call, t, 404)
    new = lambda x: api.post(TL, json={"take_ids": [x]})
    assert blind(new, t, 422) == f"take_ids[0]: take {t} not found"
    el = lambda x: api.post(
        LIB, json={"kind": "look", "group": "film", "name": "L", "images": [{"take_id": x}]}
    )
    assert blind(el, t, 404) == f"images[1]: take {t} not found"


def test_nothing_changed_or_leaked(api, api_b, a, b, b_sid):
    """After everything above (run last in this module): A's takes are as they were (no new children), B's gallery,
    sessions and runs hold none of A's ids, and B's refused session has no run."""
    now = takes_of(api, a["ids"])
    for k, before in a["before"].items():
        assert now[k]["children"] == before["children"], k
        assert {x: now[k][x] for x in ("starred", "parents", "deleted_at") if x in now[k]} == {
            x: before[x] for x in ("starred", "parents", "deleted_at") if x in before
        }
    seen = json.dumps(all_takes(api_b, limit=200))
    assert not [t for t in a["ids"].values() if t in seen]
    assert ok(api_b.get(f"{STUDIO}/sessions/{b_sid}/runs"))["runs"] == []
    seen = json.dumps(all_takes(api, limit=200))
    assert not [t for t in b.values() if t in seen]
