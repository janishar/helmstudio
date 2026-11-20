"""Looks (API 1.10) on the fake runner: the echo pack's starter looks (contracts/fixtures/registry/looks.valid.json)
listed for everyone and read-only, `fits` / `reason` for a workflow; own look CRUD with versions, the look rules and the
library cap shared with elements; Save as look from a take; runs with looks (text and images after the element lines
and rows, a repeated look once, unknown and not fitting looks 422), LoRA looks on the fake LoRA stack (the user's own
row wins), reuse's looks_status, a lines and a table batch with looks (the batch's copy), a pipeline step with looks,
isolation. Every contracts/fixtures/mentions resolve case whose graph is echo runs against check.py resolve_mentions.

Not covered: the echo image's drawn text (the LoRA rows are checked in made_with instead), starter thumbnails (the
echo pack ships none), the strength clamp (echo's stack takes 0..2, the look rules' range), looks saved before API 1.23
with a strength outside 0..2 (clamped when read: studio_service/tests/looks_test.go)."""

import copy
import json

import pytest
from conftest import headers, make_client
from helpers import (
    NO_SUCH_ID,
    QUICK,
    STUDIO,
    new_session,
    ok,
    png_bytes,
    ref,
    take_of_run,
    upload,
    usable_looks,
    usable_loras,
    wait_run,
)
from test_batches import BATCHES, FAST, finish, lines, rows_of, submit, wait_batch
from test_elements import LIB, PARAM, detail, element, made_with
from test_loras import find_stack, fixture_file, row, stack_workflow, tag, uploaded
from test_pipelines import batch_takes, create, echo_step, inputs_node
from test_pipelines import submit as submit_pipeline
from test_user_workflows import CONTRACTS, check

STARTERS = json.loads((CONTRACTS / "fixtures" / "registry" / "looks.valid.json").read_text())[
    "looks"
]
SOFT, GRAIN = "echo/soft-light", "echo/grain"
ROWS = "lora.rows"
READ_ONLY = "starter looks are read-only; save your own copy"
RESOLVE = sorted((CONTRACTS / "fixtures" / "mentions").glob("resolve.*.json"))


def look(client, name: str, text: str = "", group: str = "film", takes=(), loras=(), **kw) -> dict:
    body = {"kind": "look", "group": group, "name": name, "text": text}
    body |= {"images": [{"take_id": t} for t in takes], "loras": list(loras)} | kw
    return ok(client.post(LIB, json=body), 201)


def lora_ref(lid: str, strength: float) -> dict:
    return {"lora": {"lora_id": lid}, "strength": strength}


def update(item: dict, **changes) -> dict:
    """A LibraryItemUpdate of a look as it is, with changes."""
    u = {k: item[k] for k in ("group", "name", "text", "version")}
    u["images"] = [{"take_id": i["take_id"]} for i in item["images"]]
    u["loras"] = [lora_ref(x["lora"]["lora_id"], x["strength"]) for x in item["loras"]]
    return u | changes


def post_run(client, sid: str, inputs: dict, looks: list[str], wid: str = "echo"):
    body = {"workflow_id": wid, "inputs": QUICK | inputs, "looks": looks}
    return client.post(f"{STUDIO}/sessions/{sid}/runs", json=body)


def run_take(client, sid: str, inputs: dict, looks: list[str], wid: str = "echo") -> dict:
    run = ok(post_run(client, sid, inputs, looks, wid), 202)
    assert run.get("looks", []) == looks  # Run.looks: as requested (absent or empty = none)
    assert wait_run(client, run["id"])["status"] == "succeeded"
    return take_of_run(client, run["id"])


def refused(client, sid: str, inputs: dict, looks: list[str], wid: str = "echo") -> str:
    return detail(post_run(client, sid, inputs, looks, wid), 422)


def applied(item: dict) -> dict:
    return {k: item[k] for k in ("id", "source", "name", "version")}


def by_id(client, **params) -> dict:
    return {x["id"]: x for x in ok(client.get(LIB, params=params))["items"]}


@pytest.fixture(scope="module")
def uk(new_user):
    """User K: this module's looks."""
    with make_client(headers(new_user("K")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def pics(uk, anon) -> list[str]:
    return [upload(uk, anon, png_bytes(30 + i, 20), f"look {i}.png")["id"] for i in range(3)]


@pytest.fixture(scope="module")
def sid(uk) -> str:
    return new_session(uk, "Contract looks")["id"]


@pytest.fixture(scope="module")
def loras(uk, anon) -> dict:
    """K's ready LoRAs: peft and kohya (the fake stack's family), other (family unknown)."""
    return {
        "peft": uploaded(uk, anon, fixture_file("peft", 11), f"look-peft-{tag()}.safetensors"),
        "kohya": uploaded(uk, anon, fixture_file("kohya", 11), f"look-kohya-{tag()}.safetensors"),
        "other": uploaded(uk, anon, fixture_file("other-model", 11), "look-flux.safetensors"),
    }


@pytest.fixture(scope="module")
def lwf(uk) -> dict:
    """K's echo copy with the fake LoRA stack (`lora`) between prompt and echo; skipped without one."""
    if (stack := find_stack(uk)) is None:
        pytest.skip("the fake runner publishes no LoRA stack (echo/lora-stack)")
    return stack_workflow(uk, stack)


# ---------------------------------------------------------------- starter looks


def test_starter_looks_are_listed_for_everyone(uk, api_b):
    for client in (uk, api_b):
        got = by_id(client, source="starter")
        for want in STARTERS:
            x = got[want["id"]]
            loras = [
                {"lora": {"lora_id": y["lora_id"]} if y["lora_id"] else None}
                | {"name": y["name"], "strength": y["strength"]}
                for y in want["loras"]
            ]
            assert {
                k: x[k]
                for k in (
                    "kind",
                    "group",
                    "name",
                    "handle",
                    "text",
                    "images",
                    "version",
                    "source",
                    "family",
                    "from_take_id",
                    "thumb_url",
                )
            } == {
                "kind": "look",
                "group": want["group"],
                "name": want["name"],
                "handle": None,
                "text": want["text"],
                "images": [],
                "version": want["version"],
                "source": "starter",
                "family": "echo",
                "from_take_id": None,
                "thumb_url": None,
            }
            assert x["loras"] == loras and x["needs"] == ["text"] + (["lora"] if loras else [])
            assert x["created_at"] == x["updated_at"] and "fits" not in x
            assert ok(client.get(f"{LIB}/{want['id']}")) == x


def test_starter_looks_are_read_only(uk):
    soft = ok(uk.get(f"{LIB}/{SOFT}"))
    assert detail(uk.put(f"{LIB}/{SOFT}", json=update(soft, name="Mine now")), 409) == READ_ONLY
    assert detail(uk.delete(f"{LIB}/{SOFT}"), 409) == READ_ONLY
    assert ok(uk.get(f"{LIB}/{SOFT}")) == soft
    for path in (f"{LIB}/echo/no-such-look", f"{LIB}/no-family/soft-light"):
        assert uk.get(path).status_code == 404
        assert uk.put(path, json=update(soft)).status_code == 404
        assert uk.delete(path).status_code == 404


def test_fits_for_a_workflow(uk, pics, loras):
    text = look(uk, "Fit text", "fit text", group="colour")
    image = look(uk, "Fit image", takes=pics[:1], group="camera")
    with_lora = look(uk, "Fit lora", loras=[lora_ref(loras["peft"]["id"], 0.5)])
    got = by_id(uk, workflow="echo")
    assert all("fits" in x and "reason" in x for x in got.values())
    want = {
        SOFT: None,
        GRAIN: "this workflow has no LoRA stack",
        text["id"]: None,
        image["id"]: None,
        with_lora["id"]: "this workflow has no LoRA stack",
    }
    assert {i: (got[i]["fits"], got[i]["reason"]) for i in want} == {
        i: (r is None, r) for i, r in want.items()
    }
    assert detail(uk.get(LIB, params={"workflow": "no-such-workflow"}), 422) == (
        "unknown workflow no-such-workflow"
    )


def test_fits_on_a_lora_stack(uk, loras, lwf):
    other = look(uk, "Flux lora", loras=[lora_ref(loras["other"]["id"], 1)])
    peft = look(uk, "Peft lora", loras=[lora_ref(loras["peft"]["id"], 1)])
    got = by_id(uk, workflow=lwf["id"])
    assert (got[GRAIN]["fits"], got[GRAIN]["reason"]) == (
        False,
        "LoRA echo-grain is on no live host",
    )
    assert (got[other["id"]]["fits"], got[other["id"]]["reason"]) == (
        False,
        "LoRA look-flux fits no known model",
    )
    assert (got[peft["id"]]["fits"], got[peft["id"]]["reason"]) == (True, None)


# ---------------------------------------------------------------- own looks


def test_own_look_crud_with_versions(uk, pics):
    x = look(uk, "  Warm  ", " warm tones ", group="colour", takes=pics[:2])
    assert {k: x[k] for k in ("kind", "group", "name", "handle", "text", "version")} == {
        "kind": "look",
        "group": "colour",
        "name": "Warm",
        "handle": None,
        "text": "warm tones",
        "version": 1,
    }
    assert (x["source"], x["family"], x["loras"], x["from_take_id"]) == ("mine", None, [], None)
    assert x["needs"] == ["text", "images"] and x["thumb_url"]
    assert [i["take_id"] for i in x["images"]] == pics[:2]
    assert detail(uk.put(f"{LIB}/{x['id']}", json=update(x, images=[], text="")), 422) == (
        "a look needs text, an image or a LoRA"
    )
    x2 = ok(uk.put(f"{LIB}/{x['id']}", json=update(x, text="hot tones", images=[])))
    assert (x2["version"], x2["text"], x2["images"], x2["needs"], x2["thumb_url"]) == (
        2,
        "hot tones",
        [],
        ["text"],
        None,
    )
    stale = uk.put(f"{LIB}/{x['id']}", json=update(x2, version=1))
    assert detail(stale, 409) == "look changed since you opened it (now version 2)"
    assert ok(uk.delete(f"{LIB}/{x['id']}"), 204) is None
    assert uk.get(f"{LIB}/{x['id']}").status_code == 404
    assert uk.delete(f"{LIB}/{x['id']}").status_code == 404


def test_look_rules(uk, pics, loras):
    p = loras["peft"]
    base = {"kind": "look", "group": "film", "name": "Rule", "text": "t"}
    for body, status, want in (
        (base | {"group": "character"}, 422, "group character is not a look group"),
        (base | {"handle": "rule"}, 422, "a look has no handle"),
        (base | {"text": "  "}, 422, "a look needs text, an image or a LoRA"),
        (base | {"loras": [lora_ref(p["id"], 1)] * 5}, 422, "loras: a look has at most 4 LoRAs"),
        (
            base | {"loras": [lora_ref(p["id"], 1), lora_ref(p["id"], 0.5)]},
            422,
            f"loras[2]: LoRA {p['name']} is listed twice",
        ),
        (
            base | {"loras": [lora_ref(NO_SUCH_ID, 1)]},
            404,
            f"loras[1].lora: LoRA {NO_SUCH_ID} not found",
        ),
        (
            base | {"images": [{"take_id": NO_SUCH_ID}]},
            404,
            f"images[1]: take {NO_SUCH_ID} not found",
        ),
    ):
        assert detail(uk.post(LIB, json=body), status) == want, body
    assert uk.post(LIB, json=base | {"loras": [lora_ref(p["id"], 4.5)]}).status_code == 422
    elem = {
        "kind": "element",
        "group": "film",
        "name": "E",
        "handle": "rule-e",
        "images": [{"take_id": pics[0]}],
    }
    assert detail(uk.post(LIB, json=elem), 422) == "group film is not an element group"
    elem["group"] = "prop"
    r = uk.post(LIB, json=elem | {"loras": [lora_ref(p["id"], 1)]})
    assert detail(r, 422) == "loras: an element has no LoRAs"
    r = uk.post(LIB, json=elem | {"from_take": {"take_id": pics[0]}})
    assert detail(r, 422) == "from_take makes looks only"
    assert look(uk, "Lora only", loras=[lora_ref(p["id"], 1.5)])["needs"] == ["lora"]


def test_list_filters(uk, pics):
    mine = look(uk, "Quartz light", "quartz", group="lighting")
    got = ok(uk.get(LIB, params={"kind": "look"}))["items"]
    assert all(x["kind"] == "look" for x in got)
    own = [x for x in got if x["source"] == "mine"]
    assert got[: len(own)] == own  # own first, then the starters
    lighting = by_id(uk, group="lighting")
    assert mine["id"] in lighting and SOFT in lighting and GRAIN not in lighting
    assert set(by_id(uk, source="mine")) == {x["id"] for x in own} | set(
        by_id(uk, source="mine", kind="element")
    )
    assert all(x["source"] == "starter" for x in by_id(uk, source="starter").values())
    assert {SOFT, mine["id"]} <= set(by_id(uk, family="echo"))
    other = by_id(uk, family="some-other-family")
    assert mine["id"] in other and SOFT not in other  # family null = every family
    assert mine["id"] in by_id(uk, q="quartz")
    assert len(ok(uk.get(LIB, params={"limit": 1000}))["items"]) >= len(got)
    assert uk.get(LIB, params={"limit": 1001}).status_code == 422
    assert uk.get(LIB, params={"source": "everyone"}).status_code == 422


def test_the_200_cap_is_shared_with_elements(new_user, anon):
    with make_client(headers(new_user("Kcap")["access_token"])) as c:
        take = upload(c, anon, png_bytes(8, 8))["id"]
        for i in range(100):
            element(c, f"cap-{i}", [take])
        ids = [look(c, f"Cap {i}", "t")["id"] for i in range(100)]
        cap = "You have 200 library items; delete some first."
        assert (
            detail(
                c.post(LIB, json={"kind": "look", "group": "film", "name": "x", "text": "t"}), 409
            )
            == cap
        )
        r = c.post(
            LIB,
            json={
                "kind": "element",
                "group": "prop",
                "name": "x",
                "handle": "x",
                "images": [{"take_id": take}],
            },
        )
        assert detail(r, 409) == cap
        assert len(ok(c.get(LIB, params={"source": "mine"}))["items"]) == 200
        assert c.delete(f"{LIB}/{ids[0]}").status_code == 204
        look(c, "One more", "t")


# ---------------------------------------------------------------- Save as look


def test_save_as_look_from_a_take(uk, api_b, anon, sid, pics):
    element(uk, "sal-fox", pics[:1], name="Fox")
    take = run_take(uk, sid, {"prompt.text": "  @sal-fox in mist, @@kodak  "}, [])
    src = {"take_id": take["id"]}
    base = {"kind": "look", "group": "film", "name": "From take"}
    x = ok(uk.post(LIB, json=base | {"from_take": src}), 201)
    # the prompt as typed, @@ as @ (as it reached the runner), trimmed: mentions stay literal text
    assert (x["text"], x["loras"], x["images"], x["from_take_id"]) == (
        "@sal-fox in mist, @kodak",
        [],
        [],
        take["id"],
    )
    assert x["needs"] == ["text"] and x["thumb_url"]  # the take's thumb
    kept = ok(uk.post(LIB, json=base | {"text": "my words", "from_take": src}), 201)
    assert kept["text"] == "my words"
    off = src | {"prompt": False}
    assert detail(uk.post(LIB, json=base | {"from_take": off}), 422) == (
        "a look needs text, an image or a LoRA"
    )
    with_image = ok(uk.post(LIB, json=base | {"images": [src], "from_take": off}), 201)
    assert (with_image["text"], [i["take_id"] for i in with_image["images"]]) == ("", [take["id"]])

    up = upload(uk, anon, png_bytes(8, 8))["id"]
    r = uk.post(LIB, json=base | {"from_take": {"take_id": up}})
    assert detail(r, 422) == f"from_take: take {up} was not made by a run"
    b_take = upload(api_b, anon, png_bytes(8, 8))["id"]
    for bad in (NO_SUCH_ID, b_take):
        r = uk.post(LIB, json=base | {"from_take": {"take_id": bad}})
        assert detail(r, 404) == f"from_take: take {bad} not found"

    assert uk.delete(f"{STUDIO}/takes/{take['id']}").status_code == 204
    got = ok(uk.get(f"{LIB}/{x['id']}"))
    assert (got["from_take_id"], got["thumb_url"], got["text"]) == (
        None,
        None,
        "@sal-fox in mist, @kodak",
    )
    r = uk.post(LIB, json=base | {"from_take": src})
    assert detail(r, 404) == f"from_take: take {take['id']} not found"


def test_save_as_look_copies_usable_loras(uk, anon, sid, loras, lwf):
    p, k = loras["peft"], loras["kohya"]
    gone = uploaded(uk, anon, fixture_file("comfyui", 11), f"look-gone-{tag()}.safetensors")
    rows = [row(p["id"], 0.8, mode="adapter"), row(gone["id"], 0.3), row(k["id"], 1.5)]
    take = run_take(uk, sid, {"prompt.text": f"ink {tag()}", ROWS: rows}, [], lwf["id"])
    assert uk.delete(f"/v1/studio/loras/{gone['id']}").status_code == 204
    base = {"kind": "look", "group": "film", "name": "LoRAs of a take"}
    x = ok(uk.post(LIB, json=base | {"from_take": {"take_id": take["id"]}}), 201)
    assert x["loras"] == [
        {"lora": {"lora_id": p["id"]}, "name": p["name"], "strength": 0.8},
        {"lora": {"lora_id": k["id"]}, "name": k["name"], "strength": 1.5},
    ]
    assert x["needs"] == ["text", "lora"]
    y = ok(uk.post(LIB, json=base | {"from_take": {"take_id": take["id"], "loras": False}}), 201)
    assert y["loras"] == []


# ---------------------------------------------------------------- runs


def test_run_with_text_and_image_looks(uk, sid, pics):
    fox = element(uk, "run-fox", pics[:1], name="Fox", text="a small fox")
    warm = look(uk, "Warm run", "warm tones", group="colour", takes=pics[1:2])
    typed = "@run-fox by the river"
    take = run_take(uk, sid, {"prompt.text": typed}, [warm["id"], SOFT, warm["id"]])
    mw = take["made_with"]
    assert mw["inputs"]["prompt.text"] == (
        "Fox <image1> by the river\n\nFox: a small fox\n\nwarm tones <image2>\nsoft diffuse light"
    )
    assert mw["inputs"][PARAM] == [ref(pics[0]), ref(pics[1])]
    assert mw["typed"] == {"prompt.text": typed, PARAM: []}
    assert mw["prompt"] == typed
    assert mw["elements"] == [{"id": fox["id"], "handle": "run-fox", "name": "Fox", "version": 1}]
    soft = ok(uk.get(f"{LIB}/{SOFT}"))
    assert mw["looks"] == [applied(warm), applied(soft)]  # the repeat once, at its first place
    assert take["parents"] == []  # look images are not parents


def test_identical_lines_once_and_text_only(uk, sid):
    a = look(uk, "Same A", "the same line")
    b = look(uk, "Same B", "the same line")
    prompt = f"plain {tag()}"
    mw = run_take(uk, sid, {"prompt.text": prompt}, [a["id"], b["id"]])["made_with"]
    assert mw["inputs"]["prompt.text"] == prompt + "\n\nthe same line"
    assert mw["typed"] == {"prompt.text": prompt}
    assert mw["looks"] == [applied(a), applied(b)]
    mw = run_take(uk, sid, {"prompt.text": ""}, [a["id"]])[
        "made_with"
    ]  # no prompt: the look lines alone
    assert mw["inputs"]["prompt.text"] == "the same line"


def test_unknown_and_not_fitting_looks(uk, sid, pics, loras):
    elem = element(uk, "not-a-look", pics[:1])
    got = refused(uk, sid, {"prompt.text": "x"}, [NO_SUCH_ID, "echo/nope", NO_SUCH_ID, elem["id"]])
    assert got == (
        f"looks[1]: unknown look {NO_SUCH_ID}; looks[2]: unknown look echo/nope; "
        f"looks[4]: unknown look {elem['id']}"
    )
    got = refused(uk, sid, {"prompt.text": "@nobody-here"}, [NO_SUCH_ID])
    assert got == f"prompt.text: unknown element @nobody-here; looks[1]: unknown look {NO_SUCH_ID}"
    ink = look(uk, "Ink", "ink", loras=[lora_ref(loras["peft"]["id"], 0.7)])
    got = refused(uk, sid, {"prompt.text": "x"}, [SOFT, ink["id"], GRAIN])
    assert got == (
        "looks[2]: look Ink does not fit this workflow (this workflow has no LoRA stack); "
        "looks[3]: look Grain does not fit this workflow (this workflow has no LoRA stack)"
    )
    assert post_run(uk, sid, {}, [SOFT] * 9).status_code == 422  # at most 8


def test_lora_look_joins_the_stack(uk, sid, loras, lwf):
    p = loras["peft"]
    ink = look(uk, "Ink stack", "ink wash", loras=[lora_ref(p["id"], 0.7)])
    prompt = f"a heron {tag()}"
    mw = run_take(uk, sid, {"prompt.text": prompt}, [ink["id"]], lwf["id"])["made_with"]
    assert [(r["lora"], r["strength"]) for r in mw["inputs"][ROWS]] == [({"lora_id": p["id"]}, 0.7)]
    assert mw["typed"] == {"prompt.text": prompt, ROWS: []}
    assert mw["inputs"]["prompt.text"] == prompt + "\n\nink wash"
    assert [(x["node"], x["id"], x["strength"], x["mode"]) for x in mw["loras"]] == [
        ("lora", p["id"], 0.7, "adapter")
    ]
    assert mw["looks"] == [applied(ink)]

    # the user's own row for the same LoRA wins, at its strength
    mw = run_take(
        uk, sid, {"prompt.text": prompt, ROWS: [row(p["id"], 0.3)]}, [ink["id"]], lwf["id"]
    )["made_with"]
    assert [(r["lora"], r["strength"]) for r in mw["inputs"][ROWS]] == [({"lora_id": p["id"]}, 0.3)]
    assert ROWS not in mw["typed"] and [x["strength"] for x in mw["loras"]] == [0.3]

    got = refused(uk, sid, {"prompt.text": "x"}, [GRAIN], lwf["id"])
    assert (
        got
        == "looks[1]: look Grain does not fit this workflow (LoRA echo-grain is on no live host)"
    )


def test_reuse_after_edit_and_delete(uk, sid):
    dusk = look(uk, "Dusk", "at dusk", group="lighting")
    take = run_take(uk, sid, {"prompt.text": f"a fox {tag()}"}, [dusk["id"], SOFT])
    before = take["made_with"]

    def reuse() -> dict:
        r = ok(uk.post(f"{STUDIO}/takes/{take['id']}/reuse"))
        assert r["looks"] == [dusk["id"], SOFT]
        assert r["inputs"]["prompt.text"] == before["typed"]["prompt.text"]  # the typed view
        assert made_with(uk, take["id"]) == before  # the old take never changes
        return r

    def status(s: str) -> list[dict]:
        return [{"id": dusk["id"], "status": s}, {"id": SOFT, "status": "current"}]

    assert reuse()["looks_status"] == status("current")
    ok(uk.put(f"{LIB}/{dusk['id']}", json=update(dusk, text="at night")))
    assert reuse()["looks_status"] == status("changed")
    assert uk.delete(f"{LIB}/{dusk['id']}").status_code == 204
    again = reuse()
    assert again["looks_status"] == status("deleted")
    r = uk.post(
        f"{STUDIO}/sessions/{sid}/runs",
        json={"workflow_id": "echo", "inputs": again["inputs"], "looks": again["looks"]},
    )
    assert detail(r, 422) == f"looks[1]: unknown look {dusk['id']}"


# ---------------------------------------------------------------- batches and pipelines


def test_lines_batch_keeps_its_copy(uk):
    x = look(uk, "Batch look", "batch look text")
    values = [f"a fox {tag()}", f"a heron {tag()}"]
    batch = submit(uk, lines(values), looks=[x["id"]])
    x2 = ok(uk.put(f"{LIB}/{x['id']}", json=update(x, text="edited mid-batch")))
    try:
        assert wait_batch(uk, batch["id"])["status"] == "done"
        rows = rows_of(uk, batch["id"])
    finally:
        finish(uk, batch["id"])
    for r, v in zip(rows, values, strict=True):
        assert r["looks"] == [x["id"]] and r["inputs"]["prompt.text"] == v
        mw = made_with(uk, r["take_ids"][0])
        assert mw["inputs"]["prompt.text"] == v + "\n\nbatch look text"
        assert mw["looks"] == [applied(x)]  # version 1

    # a looks column: a row's own looks, null = the batch's
    table = {
        "columns": ["prompt.text", "looks"],
        "rows": [[f"t1 {tag()}", [SOFT]], [f"t2 {tag()}", None]],
    }
    batch = submit(uk, {"table": table}, looks=[x["id"]])
    try:
        assert wait_batch(uk, batch["id"])["status"] == "done"
        rows = rows_of(uk, batch["id"])
    finally:
        finish(uk, batch["id"])
    assert [r["looks"] for r in rows] == [[SOFT], [x["id"]]]
    texts = [made_with(uk, r["take_ids"][0])["inputs"]["prompt.text"] for r in rows]
    assert texts == [
        table["rows"][0][0] + "\n\nsoft diffuse light",
        table["rows"][1][0] + "\n\nedited mid-batch",
    ]
    assert made_with(uk, rows[1]["take_ids"][0])["looks"] == [applied(x2)]

    r = uk.post(
        BATCHES,
        json={
            "workflow_id": "echo",
            "rows": lines(["a", "b"]),
            "inputs": FAST,
            "looks": [NO_SUCH_ID],
        },
    )
    assert detail(r, 422).startswith(f"row 1: looks[1]: unknown look {NO_SUCH_ID}")


def test_pipeline_step_with_looks(uk):
    x = look(uk, "Step look", "step look text")
    step = echo_step({"prompt.text": "$prompt"}) | {"looks": [x["id"], SOFT]}
    p = create(
        uk,
        {
            "title": "Contract looks",
            "nodes": {
                "inputs": inputs_node("prompt"),
                "s1": step,
                "out": {"kind": "output", "from": "s1.takes", "collection": "Contract looks"},
            },
            "edges": [["inputs", "s1"], ["s1", "out"]],
        },
    )
    assert p["problems"] == [] and p["nodes"]["s1"]["looks"] == [x["id"], SOFT]
    value = f"a frog {tag()}"
    batch = submit_pipeline(uk, p["id"], [value])
    try:
        assert wait_batch(uk, batch["id"])["status"] == "done"
        (take,) = batch_takes(uk, batch["id"])
    finally:
        finish(uk, batch["id"])
    mw = take["made_with"]
    assert mw["inputs"]["prompt.text"] == value + "\n\nstep look text\nsoft diffuse light"
    assert mw["looks"] == [applied(x), applied(ok(uk.get(f"{LIB}/{SOFT}")))]

    body = {"pipeline_id": p["id"], "rows": lines(["x"], "inputs.prompt"), "looks": [SOFT]}
    assert (
        detail(uk.post(BATCHES, json=body), 422)
        == "looks: a pipeline batch takes its looks from its steps"
    )

    step["looks"] = [NO_SUCH_ID]
    bad = create(
        uk, {"title": "Contract bad look", "nodes": {**p["nodes"], "s1": step}, "edges": p["edges"]}
    )
    assert ("unknown_look", "s1", "looks") in {
        (q["code"], q.get("node"), q.get("param")) for q in bad["problems"]
    }
    r = uk.post(BATCHES, json={"pipeline_id": bad["id"], "rows": lines(["x"], "inputs.prompt")})
    assert detail(r, 422).startswith("pipeline cannot run: ")


# ---------------------------------------------------------------- isolation


def test_isolation(uk, api_b, anon, sid):
    a = look(uk, "Iso look", "secret look text")
    path = f"{LIB}/{a['id']}"
    assert api_b.get(path).status_code == 404
    assert api_b.put(path, json=update(a, name="B was here")).status_code == 404
    assert api_b.delete(path).status_code == 404
    assert ok(uk.get(path))["version"] == 1

    a_take = run_take(uk, sid, {"prompt.text": f"a's take {tag()}"}, [])
    r = api_b.post(
        LIB,
        json={"kind": "look", "group": "film", "name": "x", "from_take": {"take_id": a_take["id"]}},
    )
    assert detail(r, 404) == f"from_take: take {a_take['id']} not found"

    b_sid = new_session(api_b, "Contract B looks")["id"]
    assert (
        refused(api_b, b_sid, {"prompt.text": "x"}, [a["id"]])
        == f"looks[1]: unknown look {a['id']}"
    )
    r = api_b.post(
        BATCHES,
        json={"workflow_id": "echo", "rows": lines(["x"]), "inputs": FAST, "looks": [a["id"]]},
    )
    assert detail(r, 422) == f"row 1: looks[1]: unknown look {a['id']}"

    mine = look(api_b, "B's look", "b text")
    try:
        got = ok(api_b.get(LIB, params={"kind": "look"}))["items"]
        assert {x["id"] for x in got if x["source"] == "mine"} == {mine["id"]}
        assert {x["id"] for x in got if x["source"] == "starter"} >= {SOFT, GRAIN}
        assert all("secret" not in x["text"] for x in got)
    finally:  # B is shared with other modules
        assert api_b.delete(f"{LIB}/{mine['id']}").status_code == 204


# ---------------------------------------------------------------- contracts/fixtures/mentions on echo


@pytest.fixture(scope="module")
def ur(new_user):
    """User R: its library is reset for each fixture."""
    with make_client(headers(new_user("R")["access_token"])) as c:
        yield c


@pytest.fixture(scope="module")
def resolve_env(ur, anon) -> dict:
    groups = ok(ur.get(f"{STUDIO}/nodes"))["groups"]
    pool: dict[str, str] = {}

    def real(tid: str) -> str:
        if tid not in pool:
            pool[tid] = upload(ur, anon, png_bytes(10 + len(pool), 12))["id"]
        return pool[tid]

    return {
        "nodes": ok(ur.get(f"{STUDIO}/workflows/echo"))["nodes"],
        "palette": {n["kind"]: n for g in groups for n in g["nodes"]},
        "sid": new_session(ur, "Contract look fixtures")["id"],
        "real": real,
    }


@pytest.mark.parametrize(
    "path", [p for p in RESOLVE if "looks" in json.loads(p.read_text())], ids=lambda p: p.stem
)
def test_resolve_fixture_on_echo(ur, resolve_env, path):
    fx, real, env = json.loads(path.read_text()), resolve_env["real"], resolve_env
    nodes = fx["workflow"]["nodes"]
    if {n["kind"] for n in nodes.values()} - {n["kind"] for n in env["nodes"].values()}:
        pytest.skip("its graph is not the fake echo workflow")
    if any(x["loras"] for x in fx["looks"]) or fx.get("loras"):
        pytest.skip("its looks carry LoRAs (no fake LoRA ids to match)")
    starters = by_id(ur, source="starter")
    if missing := [
        x["id"] for x in fx["looks"] if x["source"] == "starter" and x["id"] not in starters
    ]:
        pytest.skip(f"no live host publishes {missing}")

    for item in ok(ur.get(LIB, params={"source": "mine"}))["items"]:
        assert ur.delete(f"{LIB}/{item['id']}").status_code == 204
    ids: dict[str, str] = {}  # fixture id → real id
    for e in fx["library"]:
        ids[e["id"]] = element(
            ur, e["handle"], [real(t) for t in e["images"]], name=e["name"], text=e["text"]
        )["id"]
    for x in fx["looks"]:
        if x["source"] == "mine":
            ids[x["id"]] = look(ur, x["name"], x["text"], takes=[real(t) for t in x["images"]])[
                "id"
            ]
    chosen = [ids.get(i, i) for i in fx.get("chosen", [])]
    prompt = nodes["prompt"]["params"]["text"]
    rows = [ref(real(r["image"]["take_id"])) for r in nodes["echo"]["params"].get("refs", [])]
    library = [
        {k: i[k] for k in ("id", "handle", "name", "version", "text")}
        | {"images": [t["take_id"] for t in i["images"]]}
        for i in ok(ur.get(LIB, params={"kind": "element"}))["items"]
    ]
    graph = copy.deepcopy(env["nodes"])
    graph["prompt"].setdefault("params", {})["text"] = prompt
    graph["echo"].setdefault("params", {})["refs"] = rows
    want = check.resolve_mentions(
        graph, env["palette"], library, None, usable_looks(ur), chosen, usable_loras(ur)
    )

    r = post_run(ur, env["sid"], {"prompt.text": prompt, PARAM: rows}, chosen)
    if want["errors"]:
        assert detail(r, 422) == "; ".join(want["errors"])
        return
    run = ok(r, 202)
    assert wait_run(ur, run["id"])["status"] == "succeeded"
    mw = take_of_run(ur, run["id"])["made_with"]
    for key, value in want["inputs"].items():
        assert mw["inputs"][key] == value, key
    assert (mw.get("typed") or {}, mw.get("elements") or [], mw.get("looks") or []) == (
        want["typed"],
        want["elements"],
        want["looks"],
    )
