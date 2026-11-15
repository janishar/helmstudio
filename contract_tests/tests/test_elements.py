"""Elements (API 1.8) on the fake echo workflow: library CRUD with versions, handle and image rules, the 200 cap, the q
search; `@` mentions in a run (resolved prompt and appended `echo.refs` stored in the take, made_with.elements and
typed, the escape `@@`, unknown handles 422), reuse's elements_status after an edit and a delete, a lines batch and a
pipeline batch with mentions, isolation. Every contracts/fixtures/mentions resolve case runs on echo: the expected
result is the contracts' reference resolver (check.py resolve_mentions) over the LIVE echo graph and palette.

Not covered: `images[i]: take <id> is not an image` / `has no size` (the fake runner makes only sized images)."""

import copy
import json
from datetime import datetime

import pytest
from conftest import headers, make_client
from helpers import NO_SUCH_ID, QUICK, STUDIO, new_session, ok, png_bytes, ref, upload, wait_run
from test_batches import BATCHES, FAST, finish, lines, rows_of, submit, wait_batch
from test_image_inputs import refused, run_take
from test_pipelines import batch_takes, create, echo_step, inputs_node
from test_pipelines import submit as submit_pipeline
from test_user_workflows import CONTRACTS, WF, check, copy_echo, doc

LIB = f"{STUDIO}/library"
PARAM = "echo.refs"
MENTIONS = sorted((CONTRACTS / "fixtures" / "mentions").glob("resolve.*.json"))


def body(handle: str, takes: list[str], name: str | None = None, text: str = "", group="character"):
    return {
        "kind": "element",
        "group": group,
        "name": handle.capitalize() if name is None else name,
        "handle": handle,
        "text": text,
        "images": [{"take_id": t} for t in takes],
    }


def element(client, handle: str, takes: list[str], **kw) -> dict:
    return ok(client.post(LIB, json=body(handle, takes, **kw)), 201)


def update(item: dict, **changes) -> dict:
    """A LibraryItemUpdate of the item as it is, with changes (images as take ids)."""
    u = {k: item[k] for k in ("group", "name", "handle", "text", "version")}
    u["images"] = [{"take_id": i["take_id"]} for i in item["images"]]
    if "takes" in changes:
        changes["images"] = [{"take_id": t} for t in changes.pop("takes")]
    return u | changes


def put(client, item: dict, **changes) -> dict:
    return ok(client.put(f"{LIB}/{item['id']}", json=update(item, **changes)))


def detail(r, status: int) -> str:
    assert r.status_code == status, (
        f"{r.request.method} {r.request.url.path}: {r.status_code} {r.text}"
    )
    return r.json()["detail"]


def made_with(client, take_id: str) -> dict:
    return ok(client.get(f"{STUDIO}/takes/{take_id}"))["made_with"]


@pytest.fixture(scope="module")
def ue(new_user):
    """User E: this module's elements (handles are unique per owner)."""
    with make_client(headers(new_user("E")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def pics(ue, anon) -> list[str]:
    """Five of E's uploads, 20x16 .. 24x16."""
    return [upload(ue, anon, png_bytes(20 + i, 16), f"pic {i}.png")["id"] for i in range(5)]


@pytest.fixture(scope="module")
def sid(ue) -> str:
    return new_session(ue, "Contract elements")["id"]


# ---------------------------------------------------------------- the library


def test_crud_with_versions(ue, pics):
    e = element(ue, "lamp", pics[:2], name="  Brass lamp ", text=" a lamp ", group="prop")
    assert (e["kind"], e["group"], e["name"], e["handle"], e["text"], e["version"]) == (
        "element",
        "prop",
        "Brass lamp",
        "lamp",
        "a lamp",
        1,
    )
    assert [i["take_id"] for i in e["images"]] == pics[:2]
    assert (e["images"][0]["width"], e["images"][0]["height"]) == (20, 16)
    assert e["thumb_url"] and e["images"][0]["url"]
    assert ok(ue.get(f"{LIB}/{e['id']}"))["handle"] == "lamp"

    e2 = put(ue, e, name="Lamp", text="", takes=[pics[2]])
    assert (e2["version"], e2["name"], e2["text"], e2["kind"]) == (2, "Lamp", "", "element")
    assert [i["take_id"] for i in e2["images"]] == [pics[2]]
    assert datetime.fromisoformat(e2["updated_at"]) >= datetime.fromisoformat(e["updated_at"])
    stale = ue.put(f"{LIB}/{e['id']}", json=update(e2, name="Stale", version=1))
    assert detail(stale, 409) == "element changed since you opened it (now version 2)"
    assert ok(ue.get(f"{LIB}/{e['id']}"))["name"] == "Lamp"

    assert ok(ue.delete(f"{LIB}/{e['id']}"), 204) is None
    assert ue.get(f"{LIB}/{e['id']}").status_code == 404
    assert ue.put(f"{LIB}/{e['id']}", json=update(e2)).status_code == 404
    assert ue.delete(f"{LIB}/{e['id']}").status_code == 404
    for bad in (NO_SUCH_ID, "not-a-uuid"):
        assert ue.get(f"{LIB}/{bad}").status_code == 404


def test_field_rules(ue, pics):
    p = pics[:1]
    for name in ("", "   ", "x" * 81):
        assert "name must be 1-80 characters" in detail(ue.post(LIB, json=body("n", p, name)), 422)
    for name in ("a\u0000b", "two\nlines"):
        assert (
            detail(ue.post(LIB, json=body("n", p, name)), 422)
            == "name must not contain control characters"
        )
    r = ue.post(LIB, json=body("nul-text", p, text="a\nb\u0000"))
    assert detail(r, 422) == "text must not contain control characters other than newlines and tabs"
    for handle in ("Fox", "1fox", "fox-", "fo x", "", "a" * 41, "-fox"):
        r = ue.post(LIB, json=body(handle, p, name="Bad"))
        assert detail(r, 422).startswith("handle must be a slug"), handle
    r = ue.post(LIB, json=body("long-text", p, text="x" * 2001))
    assert "text must be at most 2000 characters" in detail(r, 422)
    for handle in ("a", "a" * 40, "a-b_c1"):
        element(ue, handle, p)
    element(ue, "max-text", p, text="é" * 2000)  # code points, not bytes


def test_handle_is_unique_per_owner(ue, pics):
    heron = element(ue, "heron", pics[:1])
    r = ue.post(LIB, json=body("heron", pics[:1], name="Another"))
    assert detail(r, 409) == "handle @heron is already used"
    egret = element(ue, "egret", pics[:1])
    r = ue.put(f"{LIB}/{egret['id']}", json=update(egret, handle="heron"))
    assert detail(r, 409) == "handle @heron is already used"
    assert put(ue, heron, name="Grey heron")["handle"] == "heron"  # its own handle may stay


def test_image_rules(ue, api_b, anon, pics):
    gone = upload(ue, anon, png_bytes(8, 8))["id"]
    assert ue.delete(f"{STUDIO}/takes/{gone}").status_code == 204
    foreign = upload(api_b, anon, png_bytes(8, 8))["id"]
    pending = upload(ue, anon, png_bytes(8, 8), complete=False)["take_id"]
    for bad in (gone, foreign, pending, NO_SUCH_ID):
        r = ue.post(LIB, json=body("bad-image", [pics[0], bad]))
        assert detail(r, 404) == f"images[2]: take {bad} not found"
    for takes in ([], pics[:5]):
        r = ue.post(LIB, json=body("count", takes))
        assert detail(r, 422) == "images: an element has 1 to 4 images"
    r = ue.post(LIB, json=body("twice", [pics[0], pics[1], pics[0]]))
    assert detail(r, 422) == f"images[3]: take {pics[0]} is listed twice"
    e = element(ue, "four", pics[:4])
    r = ue.put(f"{LIB}/{e['id']}", json=update(e, takes=[]))
    assert detail(r, 422) == "images: an element has 1 to 4 images"


def test_a_deleted_take_drops_out(ue, anon, pics):
    doomed = upload(ue, anon, png_bytes(9, 9))["id"]
    e = element(ue, "drops", [doomed, pics[0]])
    assert ue.delete(f"{STUDIO}/takes/{doomed}").status_code == 204  # never refused
    assert [i["take_id"] for i in ok(ue.get(f"{LIB}/{e['id']}"))["images"]] == [pics[0]]

    last = upload(ue, anon, png_bytes(9, 9))["id"]
    e = element(ue, "all-gone", [last])
    assert ue.delete(f"{STUDIO}/takes/{last}").status_code == 204
    got = ok(ue.get(f"{LIB}/{e['id']}"))
    assert (got["images"], got["thumb_url"]) == ([], None)
    r = ue.put(f"{LIB}/{e['id']}", json=update(got))  # its next PUT needs 1-4 again
    assert detail(r, 422) == "images: an element has 1 to 4 images"
    assert put(ue, got, takes=[pics[1]])["version"] == 2


def test_list_filters_and_search(ue, pics):
    qu = element(ue, "qu", pics[:1], name="Qu", group="location")
    attic = element(ue, "attic", pics[:1], name="Quiet attic", group="location")
    items = ok(ue.get(LIB))["items"]
    stamps = [datetime.fromisoformat(i["updated_at"]) for i in items]
    assert stamps == sorted(stamps, reverse=True)  # newest first
    assert all(i["kind"] == "element" for i in ok(ue.get(LIB, params={"kind": "element"}))["items"])
    located = ok(ue.get(LIB, params={"group": "location"}))["items"]
    assert {i["id"] for i in located} == {qu["id"], attic["id"]}
    # the exact handle first, then by handle; a name prefix matches too; a leading @, case and spaces don't count
    for q in ("qu", " @QU "):
        got = ok(ue.get(LIB, params={"q": q}))["items"]
        assert [i["handle"] for i in got] == ["qu", "attic"], q
    assert len(ok(ue.get(LIB, params={"q": "qu", "limit": 1}))["items"]) == 1
    assert ok(ue.get(LIB, params={"q": "zzz-nothing"}))["items"] == []
    for bad in ({"limit": 0}, {"limit": 201}, {"group": "film"}, {"kind": "lora"}):
        assert ue.get(LIB, params=bad).status_code == 422, bad


def test_at_most_200_items(new_user, anon):
    with make_client(headers(new_user("Cap")["access_token"])) as c:
        take = upload(c, anon, png_bytes(8, 8))["id"]
        ids = [element(c, f"cap-{i}", [take])["id"] for i in range(200)]
        r = c.post(LIB, json=body("cap-200", [take]))
        assert detail(r, 409) == "You have 200 library items; delete some first."
        assert len(ok(c.get(LIB))["items"]) == 200
        assert c.delete(f"{LIB}/{ids[0]}").status_code == 204
        element(c, "cap-200", [take])


# ---------------------------------------------------------------- mentions in a run


def test_run_with_a_mention(ue, sid, pics):
    fox = element(ue, "fox", pics[:2], text="first text")
    fox = put(ue, fox, text="a small red fox")  # version 2
    prompt = "@fox in the snow, then @Fox again"
    take = run_take(ue, sid, {"prompt.text": prompt})
    mw = take["made_with"]
    assert mw["inputs"]["prompt.text"] == (
        "Fox <image1> <image2> in the snow, then Fox <image1> <image2> again\n\nFox: a small red fox"
    )
    assert mw["inputs"][PARAM] == [ref(p) for p in pics[:2]]  # the same element twice: once
    assert mw["typed"] == {"prompt.text": prompt, PARAM: []}
    assert mw["prompt"] == prompt
    assert mw["elements"] == [{"id": fox["id"], "handle": "fox", "name": "Fox", "version": 2}]
    assert take["parents"] == []  # lineage reads the typed view: element images are not parents
    assert ok(ue.get(f"{STUDIO}/runs/{take['run_id']}"))["inputs"]["prompt.text"] == prompt


def test_unknown_handle_is_422(ue, sid, pics):
    element(ue, "known", pics[:1])
    got = refused(ue, sid, {"prompt.text": "@known meets @wolf, @Wolf and @owl-x"})
    assert got == "prompt.text: unknown element @wolf; prompt.text: unknown element @owl-x"


def test_literal_at(ue, sid, pics):
    element(ue, "home", pics[:1])
    typed = "@@home at a@b.c, @ 3pm, @3pm"
    mw = run_take(ue, sid, {"prompt.text": typed})["made_with"]
    assert mw["inputs"]["prompt.text"] == "@home at a@b.c, @ 3pm, @3pm"
    assert mw["inputs"][PARAM] == []
    assert mw["typed"] == {"prompt.text": typed}
    assert not mw.get("elements")


def test_reuse_after_edit_and_delete(ue, sid, pics):
    owl = element(ue, "owl", pics[2:3], text="a snowy owl")
    take = run_take(ue, sid, {"prompt.text": "@owl at night"})
    before = take["made_with"]
    assert before["inputs"]["prompt.text"] == "Owl <image1> at night\n\nOwl: a snowy owl"

    def reuse() -> dict:
        r = ok(ue.post(f"{STUDIO}/takes/{take['id']}/reuse"))
        assert (r["inputs"]["prompt.text"], r["inputs"][PARAM]) == ("@owl at night", [])
        assert made_with(ue, take["id"]) == before  # the old take never changes
        return r

    status = {"id": owl["id"], "handle": "owl"}
    assert reuse()["elements_status"] == [status | {"status": "current", "current_handle": "owl"}]
    put(ue, owl, handle="owl-2", text="a barn owl")
    assert reuse()["elements_status"] == [status | {"status": "changed", "current_handle": "owl-2"}]
    assert ue.delete(f"{LIB}/{owl['id']}").status_code == 204
    again = reuse()
    assert again["elements_status"] == [status | {"status": "deleted", "current_handle": None}]
    assert refused(ue, sid, again["inputs"]) == "prompt.text: unknown element @owl"


# ---------------------------------------------------------------- batches


def test_lines_batch_resolves_each_row(ue, pics):
    heron = element(ue, "batch-heron", pics[3:4], name="Heron", text="a grey heron")
    values = ["@batch-heron at dawn", "two @batch-heron and @Batch-heron", "no mention, a@b.c"]
    batch = submit(ue, lines(values))
    put(ue, heron, text="edited after submit")  # the batch resolves against its own copy
    try:
        assert wait_batch(ue, batch["id"])["status"] == "done"
        rows = rows_of(ue, batch["id"])
    finally:
        finish(ue, batch["id"])
    assert [r["inputs"]["prompt.text"] for r in rows] == values  # rows keep the prompt as typed
    mws = [made_with(ue, r["take_ids"][0]) for r in rows]
    want = ["Heron <image1> at dawn", "two Heron <image1> and Heron <image1>"]
    used = [{"id": heron["id"], "handle": "batch-heron", "name": "Heron", "version": 1}]
    for mw, w, v in zip(mws, want, values, strict=False):
        assert mw["inputs"]["prompt.text"] == w + "\n\nHeron: a grey heron"
        assert (mw["inputs"][PARAM], mw["elements"], mw["prompt"]) == ([ref(pics[3])], used, v)
    assert (mws[2]["inputs"]["prompt.text"], mws[2].get("elements") or []) == (values[2], [])

    r = ue.post(
        BATCHES,
        json={
            "workflow_id": "echo",
            "rows": lines(["@batch-heron", "@nobody here", "fine", "@ghost-x and @nobody"]),
            "inputs": FAST,
        },
    )
    assert detail(r, 422) == (
        "row 2: prompt.text: unknown element @nobody; "
        "row 4: prompt.text: unknown element @ghost-x; row 4: prompt.text: unknown element @nobody"
    )


def test_pipeline_batch_resolves_a_bound_prompt(ue, pics):
    frog = element(ue, "pipe-frog", pics[4:5], name="Frog", text="a green frog")
    p = create(
        ue,
        {
            "title": "Contract mentions",
            "nodes": {
                "inputs": inputs_node("prompt"),
                "s1": echo_step({"prompt.text": "$prompt"}),
                "out": {"kind": "output", "from": "s1.takes", "collection": "Contract mentions"},
            },
            "edges": [["inputs", "s1"], ["s1", "out"]],
        },
    )
    batch = submit_pipeline(ue, p["id"], ["@pipe-frog on a leaf", "a plain frog"])
    try:
        assert wait_batch(ue, batch["id"])["status"] == "done"
        takes = {t["made_with"]["pipeline"]["row"]: t for t in batch_takes(ue, batch["id"])}
    finally:
        finish(ue, batch["id"])
    mw = takes[1]["made_with"]
    assert mw["inputs"]["prompt.text"] == "Frog <image1> on a leaf\n\nFrog: a green frog"
    assert mw["inputs"][PARAM] == [ref(pics[4])]
    assert mw["typed"]["prompt.text"] == "@pipe-frog on a leaf"
    assert mw["elements"] == [
        {"id": frog["id"], "handle": "pipe-frog", "name": "Frog", "version": 1}
    ]
    assert takes[1]["parents"] == []
    assert takes[2]["made_with"]["inputs"]["prompt.text"] == "a plain frog"

    r = ue.post(
        BATCHES, json={"pipeline_id": p["id"], "rows": lines(["ok", "@nobody"], "inputs.prompt")}
    )
    assert "row 2: s1: prompt.text: unknown element @nobody" in detail(r, 422)


def test_pipeline_step_workflow_own_prompt_mention(ue, pics):
    """A step's saved workflow whose own prompt mentions an element: checked at Submit and copied for its runs."""
    w = copy_echo(ue, "Contract saved mention")
    d = doc(w)
    d["nodes"]["prompt"]["params"]["text"] = "@pipe-owl in snow"
    d["nodes"]["echo"]["params"] |= {"delay_ms": 50}
    ok(ue.put(f"{WF}/{w['id']}", json=d | {"version": 1}))
    p = create(
        ue,
        {
            "title": "Contract saved mention",
            "nodes": {
                "inputs": {"kind": "inputs", "inputs": [{"name": "steps", "type": "Int"}]},
                "s1": {"kind": "step", "workflow": w["id"], "bind": {"echo.steps": "$steps"}},
                "out": {
                    "kind": "output",
                    "from": "s1.takes",
                    "collection": "Contract saved mention",
                },
            },
            "edges": [["inputs", "s1"], ["s1", "out"]],
        },
    )
    body = {"pipeline_id": p["id"], "rows": {"table": {"columns": ["inputs.steps"], "rows": [[2]]}}}
    assert (
        detail(ue.post(BATCHES, json=body), 422)
        == "row 1: s1: prompt.text: unknown element @pipe-owl"
    )
    element(ue, "pipe-owl", pics[3:4], name="Owl")
    batch = ok(ue.post(BATCHES, json=body), 202)
    try:
        assert wait_batch(ue, batch["id"])["status"] == "done"
        (take,) = batch_takes(ue, batch["id"])
    finally:
        finish(ue, batch["id"])
    assert take["made_with"]["inputs"]["prompt.text"] == "Owl <image1> in snow"


# ---------------------------------------------------------------- isolation


def test_isolation(ue, api_b, anon, pics):
    cat = element(ue, "iso-cat", pics[:1], name="Cat")
    b_take = upload(api_b, anon, png_bytes(8, 8))["id"]
    path = f"{LIB}/{cat['id']}"
    assert api_b.get(path).status_code == 404
    assert api_b.put(path, json=update(cat, takes=[b_take])).status_code == 404
    assert api_b.delete(path).status_code == 404
    assert ok(ue.get(path))["version"] == 1  # untouched

    b_sid = new_session(api_b, "Contract B elements")["id"]
    assert (
        refused(api_b, b_sid, {"prompt.text": "@iso-cat"})
        == "prompt.text: unknown element @iso-cat"
    )
    mine = element(api_b, "iso-cat", [b_take])  # handles are unique per owner only
    assert [i["id"] for i in ok(api_b.get(LIB))["items"]] == [mine["id"]]
    assert cat["id"] not in {i["id"] for i in ok(api_b.get(LIB, params={"q": "iso"}))["items"]}


# ---------------------------------------------------------------- contracts/fixtures/mentions on echo


@pytest.fixture(scope="module")
def um(new_user):
    """User M: its library is reset for each fixture."""
    with make_client(headers(new_user("M")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def mention_env(um, anon) -> dict:
    """The live echo graph and palette, M's session, and fixture take id → one of M's uploads."""
    groups = ok(um.get(f"{STUDIO}/nodes"))["groups"]
    pool: dict[str, str] = {}

    def real(tid: str) -> str:
        if tid not in pool:
            pool[tid] = upload(um, anon, png_bytes(10 + len(pool), 10))["id"]
        return pool[tid]

    return {
        "nodes": ok(um.get(f"{STUDIO}/workflows/echo"))["nodes"],
        "palette": {n["kind"]: n for g in groups for n in g["nodes"]},
        "sid": new_session(um, "Contract mention fixtures")["id"],
        "real": real,
    }


@pytest.mark.parametrize("path", MENTIONS, ids=lambda p: p.stem)
def test_mention_fixture_on_echo(um, anon, mention_env, path):
    fx, real = json.loads(path.read_text()), mention_env["real"]
    nodes = fx["workflow"]["nodes"]
    prompt = nodes["prompt"]["params"]["text"]
    rows = next((n["params"]["refs"] for n in nodes.values() if "refs" in n.get("params", {})), [])
    rows = [ref(real(r["image"]["take_id"])) for r in rows]

    for item in ok(um.get(LIB))["items"]:
        assert um.delete(f"{LIB}/{item['id']}").status_code == 204
    for e in fx["library"]:
        takes = [real(t) for t in e["images"]]
        doomed = [] if takes else [upload(um, anon, png_bytes(8, 8))["id"]]
        element(um, e["handle"], takes or doomed, name=e["name"], text=e["text"])
        for t in doomed:  # images gone: the element is left with none
            assert um.delete(f"{STUDIO}/takes/{t}").status_code == 204
    library = [
        {k: i[k] for k in ("id", "handle", "name", "version", "text")}
        | {"images": [x["take_id"] for x in i["images"]]}
        for i in ok(um.get(LIB))["items"]
    ]
    graph = copy.deepcopy(mention_env["nodes"])
    graph["prompt"].setdefault("params", {})["text"] = prompt
    graph["echo"].setdefault("params", {})["refs"] = rows
    want = check.resolve_mentions(graph, mention_env["palette"], library)

    inputs = QUICK | {"prompt.text": prompt, PARAM: rows}
    r = um.post(
        f"{STUDIO}/sessions/{mention_env['sid']}/runs",
        json={"workflow_id": "echo", "inputs": inputs},
    )
    if want["errors"]:
        assert detail(r, 422) == "; ".join(want["errors"])
        return
    run = ok(r, 202)
    assert wait_run(um, run["id"])["status"] == "succeeded"
    (out,) = ok(um.get(f"{STUDIO}/runs/{run['id']}"))["outputs"]
    mw = made_with(um, out["take_id"])
    for key, value in want["inputs"].items():
        assert mw["inputs"][key] == value, key
    assert (mw.get("typed") or {}, mw.get("elements") or []) == (want["typed"], want["elements"])
