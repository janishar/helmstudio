"""Agents, fan-out, model sources and custom nodes (API 1.15, M4b stage B) on the fake stack: the 1.15 pipeline rules over
contracts/fixtures/pipelines (problems, bounds, models, egress, reserved from contracts/check.py against what is live),
an Agent's tool loop on `echo-ask`, fan-out with Send, the director's chat, a local server and a remote provider on
the fake OpenAI-compatible server (contract_tests/fake_openai, service `fake-openai` of deploy/compose/fake.yml),
the host rule, custom nodes.

Scripting. `echo-ask` answers the JSON after `ECHO-TURN <n>:` for the turn its prompt names (`This is turn <n> of`),
else after the last `ECHO-ANSWER:`; the fake server answers the same way (as native tool_calls when the request has
tools), so one scripted prompt drives a local, a server and a remote model alike. A script nested in a tool's args
(a row the director adds) is written with its colons as \\u003a, so it stays inert until the JSON is decoded.

The fake server publishes no host port: failures, delays and the requests it received are reached with `docker exec`
into `<COMPOSE_PROJECT>-fake-openai-1` (default project helmstudio-test), or over HTTP at FAKE_OPENAI_URL; those
checks skip when neither answers. Server models need `server-ask` live (pipelines' server executor, host SERVER_HOST,
default `fake`); remote ones the deployment's REMOTE_MODELS switch and a placeholder key file named KEY_SECRET in
pipelines' secrets directory (no real key: the fake ignores it)."""

import copy
import json
import os
import re
import subprocess
import uuid
from contextlib import contextmanager

import httpx
import pytest
from helpers import NO_SUCH_ID, STUDIO, new_session, ok, start_run, wait_for, wait_run
from test_batches import BATCH_TIMEOUT, BATCHES, finish, rows_of, wait_batch, wait_rows
from test_pipelines import (
    PIPELINE_FIXTURES,
    PIPES,
    cell,
    create,
    ended,
    execution,
    inputs_node,
    live_sources,
    live_workflows,
    submit,
)
from test_user_workflows import CONTRACTS, check, keys

ASK = "echo-ask"
PROVIDERS = f"{STUDIO}/admin/providers"
FAKE_PROJECT = os.environ.get("COMPOSE_PROJECT", "helmstudio-test")
FAKE_URL = os.environ.get("FAKE_OPENAI_URL", "").rstrip("/")
SERVER_BASE = os.environ.get("FAKE_SERVER_BASE", "http://fake-openai:8000/v1")
REMOTE_BASE = os.environ.get("FAKE_REMOTE_BASE", "https://fake-openai:8443/v1")
SERVER_HOST = os.environ.get("SERVER_HOST", "fake")
KEY_SECRET = os.environ.get("KEY_SECRET", "fake-openai")
FAST = {"echo.steps": 2, "echo.delay_ms": 50}
B_KINDS = {"agent", "custom"}


def stage_b(fx: dict) -> bool:
    """A 1.15 fixture: an agent or custom node, a fan-out, remote settings or providers."""
    nodes = fx["pipeline"]["nodes"].values()
    return bool(
        B_KINDS & {n["kind"] for n in nodes}
        or any("each" in n for n in nodes)
        or "remote" in fx["pipeline"]
        or "providers" in fx
    )


B_FIXTURES = [p for p in PIPELINE_FIXTURES if stage_b(json.loads(p.read_text()))]
MARK = re.compile(r"(ECHO-(?:TURN \d+|ANSWER|RETRY)):")


# ---------------------------------------------------------------- scripts


def compact(value) -> str:
    return json.dumps(value, separators=(",", ":"))


def quiet(value) -> str:
    """value as compact JSON whose nested scripts are inert until it is decoded."""
    return MARK.sub(r"\1\\u003a", compact(value))


def turn(say: str = "", *calls: tuple[str, dict]) -> dict:
    return {"say": say, "calls": [{"tool": t, "args": a} for t, a in calls]}


def script(text: str, *turns: dict) -> str:
    """text, then one ECHO-TURN per turn (1..n)."""
    return text + "".join(f" ECHO-TURN {i}: {quiet(t)}" for i, t in enumerate(turns, 1))


def answer(text: str, value) -> str:
    return f"{text} ECHO-ANSWER: {quiet(value)}"


def tag() -> str:
    return uuid.uuid4().hex[:8]


# ---------------------------------------------------------------- documents


def agent(**kw) -> dict:
    return {
        "kind": "agent",
        "instruction": "Do what the brief says.",
        "from": ["$prompt"],
        "model": ASK,
    } | kw


def step(bind: dict, **kw) -> dict:
    return {"kind": "step", "workflow": "echo", "inputs": FAST, "bind": bind} | kw


def routed_agent(title: str, **kw) -> dict:
    """inputs → boss (routes make → still, skip → out; max 1, then make) → still → out."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "boss": agent(routes={"make": "make the still", "skip": "nothing to make"}, **kw),
            "still": step({"prompt.text": "$prompt"}),
            "out": {"kind": "output", "from": "still.takes", "collection": title},
        },
        "edges": [
            ["inputs", "boss"],
            ["boss", {"routes": {"make": "still", "skip": "out"}, "max": 1, "then": "make"}],
            ["still", "out"],
        ],
    }


def fanned(title: str, producer: dict, n: int = 3) -> dict:
    """inputs → plan (a list of at most n) → shot (one echo per item) → out."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "plan": producer | {"list": {"max": n}},
            "shot": step({"prompt.text": "plan.item"}, each="plan.items"),
            "out": {"kind": "output", "from": "shot.takes", "collection": title},
        },
        "edges": [["inputs", "plan"], ["plan", "shot"], ["shot", "out"]],
    }


def decided(title: str, model: str, **doc) -> dict:
    """inputs → still → check (decide on `model`: keep → out, again → still once) → out."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": step({"prompt.text": "$prompt"}),
            "check": {
                "kind": "decide",
                "question": "Keep it?",
                "from": ["$prompt"],
                "routes": {"keep": "good", "again": "make it again"},
                "model": model,
            },
            "out": {"kind": "output", "from": "still.takes", "collection": title},
        },
        "edges": [
            ["inputs", "still"],
            ["still", "check"],
            ["check", {"routes": {"keep": "out", "again": "still"}, "max": 1, "then": "keep"}],
        ],
    } | doc


# ---------------------------------------------------------------- reading results


def decisions(client, bid: str, n: int) -> list[dict]:
    return ok(client.get(f"{BATCHES}/{bid}/rows/{n}/decisions"))["decisions"]


def batch(client, bid: str) -> dict:
    return ok(client.get(f"{BATCHES}/{bid}"))


def all_ended(client, bid: str) -> list[dict]:
    return wait_rows(
        client, bid, lambda rs: all(r["status"] in ("succeeded", "failed") for r in rs), "rows end"
    )


def chat(client, bid: str) -> dict:
    return ok(client.get(f"{BATCHES}/{bid}/chat"))


def wait_chat(client, bid: str, pred, what: str) -> dict:
    return wait_for(lambda: pred(p := chat(client, bid)) and p, timeout=BATCH_TIMEOUT, what=what)


def table(rows: list[list]) -> dict:
    return {"table": {"columns": ["inputs.script", "inputs.delay"], "rows": rows}}


# ---------------------------------------------------------------- the fake server


def fake(method: str, path: str, body=None):
    """One request to the fake server: over HTTP at FAKE_OPENAI_URL, else docker exec into the stack's container."""
    if FAKE_URL:
        r = httpx.request(method, FAKE_URL + path, json=body, timeout=10)
        r.raise_for_status()
        return r.json()
    cmd = ["docker", "exec", "-i", f"{FAKE_PROJECT}-fake-openai-1", "python", "server.py", "call"]
    out = subprocess.run(
        cmd + [method, path] + (["-"] if body is not None else []),
        input=json.dumps(body).encode() if body is not None else None,
        capture_output=True,
        timeout=30,
        check=False,
    )
    if out.returncode:
        raise RuntimeError(out.stderr.decode()[-300:])
    return json.loads(out.stdout)


@pytest.fixture(scope="module")
def fake_ctl() -> None:
    try:
        fake("GET", "/__requests")
    except (OSError, RuntimeError, httpx.HTTPError, subprocess.SubprocessError) as e:
        pytest.skip(
            f"the fake OpenAI server is not reachable (docker exec or FAKE_OPENAI_URL): {e}"
        )


def sent(model: str) -> list[dict]:
    return [r for r in fake("GET", "/__requests") if r["model"] == model]


def most_in_flight(reqs: list[dict]) -> int:
    edges = sorted([(r["t0"], 1) for r in reqs] + [(r["t1"], -1) for r in reqs if "t1" in r])
    most = now = 0
    for _, d in edges:
        now += d
        most = max(most, now)
    return most


# ---------------------------------------------------------------- providers


@pytest.fixture(scope="module")
def palette(api) -> dict:
    groups = ok(api.get(f"{STUDIO}/nodes"))["groups"]
    return {n["kind"]: n for g in groups for n in g["nodes"]}


@pytest.fixture(scope="module", autouse=True)
def ask_live(api) -> None:
    if api.get(f"{STUDIO}/workflows/{ASK}").status_code != 200:
        pytest.skip(f"the fake runner has no {ASK} workflow")


@pytest.fixture(scope="module")
def admin_view(admin) -> dict:
    return ok(admin.get(PROVIDERS))


@contextmanager
def provider(admin, name: str, body: dict):
    ok(admin.put(f"{PROVIDERS}/{name}", json=body), 201)
    try:
        yield name
    finally:
        admin.delete(f"{PROVIDERS}/{name}")


def server_body(model: str) -> dict:
    return {
        "kind": "server",
        "title": "Fake server",
        "base_url": SERVER_BASE,
        "models": [model],
        "host": SERVER_HOST,
        "memory_gb": 0.5,
        "timeout_s": 30,
    }


def remote_body(model: str) -> dict:
    return {
        "kind": "remote",
        "title": "Fake remote",
        "base_url": REMOTE_BASE,
        "models": [model],
        "key_secret": KEY_SECRET,
        "timeout_s": 30,
    }


@pytest.fixture(scope="module")
def server(api, admin):
    """A local server on the fake (server-ask must be live: pipelines' server executor)."""
    if api.get(f"{STUDIO}/workflows/server-ask").status_code != 200:
        pytest.skip("server-ask is not live (PIPELINES_SERVER_HOSTS)")
    t = tag()
    with provider(admin, f"srv-{t}", server_body(f"m-{t}")) as name:
        yield {"name": name, "model": f"m-{t}"}


@pytest.fixture(scope="module")
def remote(admin, admin_view):
    if not admin_view["remote_enabled"]:
        pytest.skip("remote models are off on this deployment (REMOTE_MODELS)")
    t = tag()
    with provider(admin, f"rmt-{t}", remote_body(f"r-{t}")) as name:
        yield {"name": name, "model": f"r-{t}"}


def admin_providers(admin) -> list[dict]:
    return ok(admin.get(PROVIDERS))["providers"]


def expected(client, admin, d: dict, palette: dict) -> tuple[set, dict]:
    info, ctx = {}, live_sources(client)
    ctx["providers"] = admin_providers(admin)  # with host and memory_gb, for `reserved`
    problems = check.pipeline_problems(d, live_workflows(client, d), palette, [], {}, info, **ctx)
    return problems, info


by_node = lambda xs: sorted(xs, key=lambda e: e["node"])


# ---------------------------------------------------------------- rules


@pytest.mark.parametrize("path", B_FIXTURES, ids=lambda p: p.stem)
def test_fixture_rules(api, admin, palette, path):
    """Problems as check.py finds them against the live workflows, providers, switch and custom nodes; the worst
    case, models, what leaves the machine and the reserved memory once saving is allowed."""
    fx = json.loads(path.read_text())
    want, info = expected(api, admin, fx["pipeline"], palette)
    v = ok(api.post(f"{PIPES}/validate", json=fx["pipeline"]))
    assert keys(v["problems"]) == want, v["problems"]
    if v["can_save"]:
        assert v["bounds"] == info["bounds"], v
        assert by_node(v["egress"]) == by_node(info["egress"]), v["egress"]
        assert v["reserved"] == info["reserved"], v
    assert v.get("models", {}) == info["models"], v


def broken() -> dict:
    """One echo document breaking each 1.15 rule that depends on the document alone."""
    return {
        "title": "Contract broken 1.15",
        "nodes": {
            "inputs": inputs_node("prompt"),
            "boss": agent(director=True, tools=["rows.add"]),
            "chief": agent(director=True),  # director_count (both)
            "helper": agent(tools=["rows.edit"], list={"max": 9}),  # agent_tools
            "still": step({"prompt.text": "$prompt"}, each="still.items"),  # fanout_source
            "shots": step({"prompt.text": "helper.item"}, each="helper.items"),
            "review": {"kind": "gate", "takes": "shots", "keep": 1},  # 9 takes: fanout_takes
            "judge": {
                "kind": "judge",
                "takes": "still",
                "criteria": {"must": ["A"]},
                "model": "server:nobody",  # model_media
            },
            "pre": {"kind": "custom", "node": "no/such"},  # unknown_custom
            "out": {"kind": "output", "from": "review.picked", "collection": "x"},
        },
        "edges": [
            ["inputs", "boss"],
            ["boss", "chief"],
            ["chief", "helper"],
            ["helper", "still"],
            ["still", "shots"],
            ["shots", "review"],
            ["review", "judge"],
            ["judge", "pre"],
            ["pre", "out"],
        ],
    }


BROKEN = {
    ("agent_tools", "helper"),
    ("director_count", "boss"),
    ("director_count", "chief"),
    ("fanout_source", "still"),
    ("fanout_takes", "review"),
    ("model_media", "judge"),
    ("unknown_custom", "pre"),
}


def test_rule_codes(api, admin, palette):
    """Each 1.15 code that depends on the document alone, on its node, equal to check.py."""
    d = broken()
    want, _ = expected(api, admin, d, palette)
    v = ok(api.post(f"{PIPES}/validate", json=d))
    assert keys(v["problems"]) == want
    codes = {(p["code"], p.get("node")) for p in v["problems"]}
    assert BROKEN <= codes, v["problems"]


# ---------------------------------------------------------------- agents: the tool loop


def test_agent_tools(api, api_b, palette):
    """search the gallery (the owner's takes only), read the library, read and write memory, then route along a
    declared edge; the next batch's snapshot reads what it wrote."""
    t = tag()
    sid = new_session(api, "Contract agent tools")["id"]
    mine = wait_run(api, start_run(api, sid, {"prompt.text": f"gallery {t}"} | FAST)["id"])
    (out,) = mine["outputs"]
    sid_b = new_session(api_b, "Contract agent tools B")["id"]
    wait_run(api_b, start_run(api_b, sid_b, {"prompt.text": f"gallery {t}"} | FAST)["id"])
    handle = f"fox{t}"
    el = ok(
        api.post(
            f"{STUDIO}/library",
            json={
                "kind": "element",
                "group": "character",
                "name": f"Fox {t}",
                "handle": handle,
                "text": "a red fox",
                "images": [{"take_id": out["take_id"]}],
            },
        ),
        201,
    )
    tools = ["gallery.search", "library.read", "memory.read", "memory.write"]
    p = create(api, routed_agent("Contract agent tools", tools=tools))
    note = f"warm light {t}"
    t1 = turn(
        "looking",
        ("gallery_search", {"text": t}),
        ("library_read", {"kind": "element", "text": f"Fox {t}"}),
        ("memory_read", {}),
        ("memory_write", {"key": "style", "text": note}),
    )
    t2 = turn("", ("route", {"to": "make", "reason": "found it"}))
    first = submit(api, p["id"], [script(f"brief {t}", t1, t2)])
    bid = first["id"]
    try:
        r = ended(api, bid, 1)
    finally:
        finish(api, bid)
    assert first["bounds"] == {
        "runs": 1,
        "llm_calls": 6,
        "tool_calls": 12,
        "remote_calls": 0,
        "remote_tokens": 0,
    }
    assert (first["egress"], first["reserved"], first["remote"]) == ([], [], None)
    assert r["status"] == "succeeded" and len(r["take_ids"]) == 1, r
    (d,) = decisions(api, bid, 1)
    assert (d["kind"], d["template"], d["source"], d["replayable"]) == (
        "agent",
        "agent/v1",
        {"kind": "local", "workflow": ASK},
        True,
    )
    a = d["agent"]
    assert (a["route"], a["to"], a["forced"], a["stopped"], a["tool_calls"]) == (
        "make",
        "still",
        False,
        None,
        4,
    )
    assert [x["turn"] for x in a["turns"]] == [1, 2] and len(d["run_ids"]) == 2
    calls = a["turns"][0]["calls"]
    assert [c["tool"] for c in calls] == [
        "gallery_search",
        "library_read",
        "memory_read",
        "memory_write",
    ]
    assert all(c["error"] is None for c in calls), calls
    assert [c["id"] for c in calls] == [f"{bid}:1:1:boss:0:t0:{i}" for i in range(4)]
    assert [x["take_id"] for x in calls[0]["result"]["takes"]] == [out["take_id"]]  # never B's
    assert calls[0]["result"]["takes"][0]["prompt"] == f"gallery {t}"
    assert [(i["id"], i["handle"]) for i in calls[1]["result"]["items"]] == [(el["id"], handle)]
    assert calls[2]["result"] == {"items": [], "from": "snapshot"}  # the batch's snapshot
    attempt = cell(r, "boss")["attempts"][0]
    assert attempt["route"] == {"label": "make", "to": "still"}
    assert attempt["agent"] == {
        "turns": 2,
        "tool_calls": 4,
        "tokens": 0,
        "items": 0,
        "route": "make",
    }
    steps = [s for s in execution(api, bid, 1)["steps"] if s["node"] == "boss"]
    assert [s["part"] for s in steps] == [
        "submit",
        "wait",
        "tools",
        "submit",
        "wait",
        "tools",
        "route",
    ]
    assert [c["id"] for c in steps[2]["tool_calls"]] == [c["id"] for c in calls]
    assert steps[-1]["edge"] == {
        "to": "still",
        "via": "next",
    }  # a route label is not a via (the enum)

    mem = ok(api.get(f"{STUDIO}/memory", params={"pipeline_id": p["id"]}))
    (item,) = mem["items"]
    assert (item["key"], item["text"], item["source"]["kind"], item["source"]["row"]) == (
        "style",
        note,
        "node",
        1,
    )
    assert calls[3]["result"] == {"id": item["id"]}

    nxt = submit(api, p["id"], [script(f"again {t}", turn("", ("memory_read", {})), t2)])
    try:
        ended(api, nxt["id"], 1)
    finally:
        finish(api, nxt["id"])
    (d2,) = decisions(api, nxt["id"], 1)
    read = d2["agent"]["turns"][0]["calls"][0]["result"]
    assert read["from"] == "snapshot" and [i["text"] for i in read["items"]] == [note]
    assert api_b.get(f"{BATCHES}/{bid}/rows/1/decisions").status_code == 404
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_agent_refusals_and_caps(api):
    """An undeclared route or tool fails the row (after one re-ask); the turn cap ends the loop."""
    t = tag()
    p = create(
        api,
        routed_agent(
            "Contract agent caps", tools=["memory.read"], caps={"max_turns": 2, "max_tool_calls": 4}
        ),
    )
    read = turn("", ("memory_read", {}))
    prompts = [
        script(f"fine {t}", turn("", ("route", {"to": "skip", "reason": "nothing"}))),
        script(f"nowhere {t}", turn("", ("route", {"to": "nowhere", "reason": "?"}))),
        script(f"undeclared {t}", turn("", ("rows_add", {"inputs": {"prompt": "x"}}))),
        script(f"loop {t}", read, read, read),
    ]
    b = submit(api, p["id"], prompts)
    try:
        one, two, three, four = all_ended(api, b["id"])
    finally:
        finish(api, b["id"])
    assert b["bounds"]["llm_calls"] == 2 and b["bounds"]["tool_calls"] == 4
    assert (one["status"], one["take_ids"]) == ("succeeded", []), one  # skip → out: no still
    assert [c["node"] for c in one["cells"]] == ["boss", "out"]
    for r in (two, three):
        assert r["status"] == "failed", r
        assert r["error"].startswith("boss: answer did not match the schema: "), r["error"]
        (d,) = decisions(api, b["id"], r["row"])
        assert len(d["run_ids"]) == 2 and all(a["error"] for a in d["answers"])  # re-asked once
    assert (
        four["status"] == "failed" and four["error"] == "boss: stopped at turns"
    )  # <cap> as decision.agent.stopped names it, four
    (d,) = decisions(api, b["id"], 4)
    assert (d["agent"]["stopped"], d["agent"]["tool_calls"], len(d["agent"]["turns"])) == (
        "turns",
        2,
        2,
    )
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


# ---------------------------------------------------------------- fan-out


def test_fanout_from_a_write(api):
    """A write's list of 3 shots → 3 step runs for one row (branch order); 4 items break the declared maximum."""
    t = tag()
    w = {"kind": "write", "from": ["$prompt"], "model": ASK}
    p = create(api, fanned("Contract fan-out", w))
    assert p["bounds"]["runs"] == 3  # seeds × max
    items = [f"shot {i} {t}" for i in range(3)]
    b = submit(
        api,
        p["id"],
        [
            answer(f"three {t}", {"items": items, "note": "three shots"}),
            answer(f"four {t}", {"items": items + [f"shot 3 {t}"], "note": "too many"}),
        ],
    )
    try:
        one, two = all_ended(api, b["id"])
    finally:
        finish(api, b["id"])
    assert one["status"] == "succeeded" and len(one["take_ids"]) == 3, one
    shots = cell(one, "shot")["attempts"]
    assert [(a["visit"], a["branch"], a["status"]) for a in shots] == [
        (1, i, "succeeded") for i in range(3)
    ]
    assert [a["take_ids"][0] for a in shots] == one["take_ids"]  # branch order
    for i, tid in enumerate(one["take_ids"]):
        take = ok(api.get(f"{STUDIO}/takes/{tid}"))
        assert take["made_with"]["inputs"]["prompt.text"] == items[i]
        run = ok(
            api.get(f"{STUDIO}/runs/{take['run_id']}")
        )  # batch_row is the run's (Take has none)
        assert (run["batch_id"], run["batch_row"]) == (b["id"], 1)
    assert len({a["run_ids"][0] for a in shots}) == 3
    branches = {s.get("branch") for s in execution(api, b["id"], 1)["steps"] if s["node"] == "shot"}
    assert branches == {None, 0, 1, 2}  # the fan and its wait are the step's; one submit per branch
    (d,) = decisions(api, b["id"], 1)
    assert d["kind"] == "write" and d["template"] == "write-list/v1"

    assert two["status"] == "failed" and two["error"].startswith("plan: "), two
    assert [c["node"] for c in two["cells"]] == ["plan"]  # no shot ran
    assert api.get(f"{BATCHES}/{b['id']}/chat").status_code == 404  # no director
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_fanout_from_an_agent(api):
    t = tag()
    p = create(api, fanned("Contract agent shots", agent(), n=2))
    items = [f"a {t}", f"b {t}"]
    b = submit(
        api,
        p["id"],
        [
            script(f"two {t}", turn("", ("set_items", {"items": items})), turn("Two shots.")),
            script(f"three {t}", turn("", ("set_items", {"items": items + [f"c {t}"]}))),
        ],
    )
    try:
        one, two = all_ended(api, b["id"])
    finally:
        finish(api, b["id"])
    assert one["status"] == "succeeded" and len(one["take_ids"]) == 2, one
    assert [a["branch"] for a in cell(one, "shot")["attempts"]] == [0, 1]
    assert cell(one, "plan")["attempts"][0]["agent"]["items"] == 2
    (d,) = decisions(api, b["id"], 1)
    assert (d["agent"]["items"], d["agent"]["say"]) == (items, "Two shots.")
    assert two["status"] == "failed" and two["error"].startswith("plan: "), two
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


# ---------------------------------------------------------------- the director


def director_doc(title: str) -> dict:
    """inputs (script, delay) → director (a list of 1-2 shots, then route go; rows.add / rows.edit in its chat) → shots
    (one echo per shot, its delay from the row) → hold (one more echo) → out. A slow row 1 keeps rows 2.. not started.
    A row's visit is one turn (set_items, route), so the script a row carries has no turn 2 that could shadow the
    chat's own turn 2 once the row's args show in the chat's transcript."""
    slow = {"echo.steps": 20}
    return {
        "title": title,
        "nodes": {
            "inputs": {
                "kind": "inputs",
                "inputs": [{"name": "script", "type": "Text"}, {"name": "delay", "type": "Int"}],
            },
            "director": agent(
                director=True,
                tools=["rows.add", "rows.edit"],
                list={"max": 2},
                routes={"go": "make the shots", "stop": "nothing to make"},
                wake_on_rows="none",
            )
            | {"from": ["$script"]},
            "shots": step(
                {"prompt.text": "director.item", "echo.delay_ms": "$delay"},
                inputs=slow,
                each="director.items",
            ),
            "hold": step({"prompt.text": "$script", "echo.delay_ms": "$delay"}, inputs=slow),
            "out": {"kind": "output", "from": "shots.takes", "collection": title},
        },
        "edges": [
            ["inputs", "director"],
            ["director", {"routes": {"go": "shots", "stop": "out"}, "max": 1, "then": "go"}],
            ["shots", "hold"],
            ["hold", "out"],
        ],
    }


READY = turn("Shots ready.")  # the chat's turn 2


def row_script(name: str) -> str:
    go = ("route", {"to": "go", "reason": "shots set"})
    return script(name, turn("", ("set_items", {"items": [f"{name} shot"]}), go))


def director_pipeline(api) -> dict:
    return create(api, director_doc("Contract director"))


def running_at(client, bid: str, n: int, node: str) -> None:
    wait_rows(
        client,
        bid,
        lambda rs: rs[n - 1]["status"] == "running" and rs[n - 1]["node"] == node,
        f"row {n} running {node}",
    )


def test_director_chat(api, api_b):
    t = tag()
    p = director_pipeline(api)
    assert p["bounds"]["runs"] == 3  # shots × 2 + hold
    r1, r2 = row_script(f"one {t}"), row_script(f"two {t}")
    b = ok(
        api.post(BATCHES, json={"pipeline_id": p["id"], "rows": table([[r1, 600], [r2, 10]])}), 202
    )
    bid = b["id"]
    dr = b["director"]
    assert (dr["node"], dr["wakes"], dr["max_wakes"]) == ("director", 0, 100) and dr[
        "state"
    ] != "off"
    x2, x3 = row_script(f"two edited {t}"), row_script(f"three {t}")
    m1 = script(
        f"Fix row 2 and add a shot {t}",
        turn(
            "",
            ("rows_edit", {"row": 2, "inputs": {"script": x2}}),
            ("rows_add", {"inputs": {"script": x3, "delay": 10}}),
            ("rows_add", {"inputs": {"script": "bad", "delay": "slow"}}),  # not an Int: refused
            ("rows_edit", {"row": 1, "inputs": {"delay": 10}}),  # row 1 has started
        ),
        READY,
    )
    try:
        running_at(api, bid, 1, "shots")  # rows 2.. wait for row 1
        assert api.post(f"{BATCHES}/{bid}/chat", json={"text": "   "}).status_code == 422
        mine = ok(api.post(f"{BATCHES}/{bid}/chat", json={"text": f"  {m1}  "}), 202)
        assert (mine["role"], mine["text"], mine["wake"], mine["rows"]) == ("user", m1, None, [])
        assert api_b.get(f"{BATCHES}/{bid}/chat").status_code == 404
        assert api_b.post(f"{BATCHES}/{bid}/chat", json={"text": "hi"}).status_code == 404
        page = wait_chat(
            api, bid, lambda c: any(m["role"] == "director" for m in c["messages"]), "the reply"
        )
        rows = all_ended(api, bid)
        done = wait_batch(api, bid)
    finally:
        finish(api, bid)
    msgs = page["messages"]
    assert msgs[0]["id"] == mine["id"] and [m["role"] for m in msgs].count("director") == 1
    (reply,) = [m for m in msgs if m["role"] == "director"]
    assert (reply["text"], reply["wake"], sorted(reply["rows"])) == ("Shots ready.", 1, [2, 3])
    calls = reply["tool_calls"]
    assert [c["id"] for c in calls] == [f"{bid}:0:1:director:0:t0:{i}" for i in range(4)]
    assert [c["error"] is None for c in calls] == [True, True, False, False], calls
    assert calls[1]["result"]["row"] == 3  # tools.schema.json rowsAddResult: the row number
    notices = [m["text"] for m in msgs if m["role"] == "notice"]
    assert any(n.startswith("Row 4 was not added: ") for n in notices), notices
    assert any("row 1 has started; only rows not yet started can change" in n for n in notices), (
        notices
    )
    assert [r["row"] for r in rows] == [1, 2, 3]  # the refused row was never added
    assert all(r["status"] == "succeeded" for r in rows), rows
    assert rows[0]["inputs"]["inputs.delay"] == 600  # unchanged
    assert (rows[1]["inputs"]["inputs.script"], rows[2]["inputs"]["inputs.script"]) == (x2, x3)
    for r, name in ((rows[1], f"two edited {t}"), (rows[2], f"three {t}")):
        (tid,) = r["take_ids"]
        assert ok(api.get(f"{STUDIO}/takes/{tid}"))["made_with"]["inputs"]["prompt.text"] == (
            f"{name} shot"
        )
    assert done["status"] == "done" and done["director"]["state"] == "off"
    r = api.post(f"{BATCHES}/{bid}/chat", json={"text": "still there?"})
    assert r.status_code == 409 and r.json()["detail"].startswith("the director is off"), r.text
    events = ok(api.get(f"{BATCHES}/{bid}/events", params={"since": 0}))["events"]
    said = [e["data"]["role"] for e in events if e["type"] == "chat"]
    assert said[0] == "user" and "director" in said and "notice" in said
    assert any(e["type"] == "batch" and e["data"].get("director") for e in events)


def test_director_cannot_change_a_finished_row(api):
    t = tag()
    p = director_pipeline(api)
    r1, r2, r3 = row_script(f"first {t}"), row_script(f"slow {t}"), row_script(f"after {t}")
    # the chat's turns share the batch lane with the rows' runs in no promised order, so the batch may end right as
    # the reply lands: every message is posted while row 2 runs (and a message waiting for its wake keeps the batch
    # from ending), never after the reply
    rows = table([[r1, 10], [r2, 600], [r3, 600]])
    b = ok(api.post(BATCHES, json={"pipeline_id": p["id"], "rows": rows}), 202)
    bid = b["id"]
    edit = script(
        f"Change row 1 {t}", turn("", ("rows_edit", {"row": 1, "inputs": {"delay": 20}})), READY
    )
    try:
        wait_rows(api, bid, lambda rs: rs[0]["status"] == "succeeded", "row 1 done")
        running_at(api, bid, 2, "shots")
        ok(api.post(f"{BATCHES}/{bid}/chat", json={"text": edit}), 202)
        statuses = [
            api.post(f"{BATCHES}/{bid}/chat", json={"text": f"hello {i}"}).status_code
            for i in range(7)
        ]
        assert statuses == [202] * 5 + [429] * 2, statuses  # 6 a minute, the edit included
        page = wait_chat(
            api, bid, lambda c: any(m["role"] == "director" for m in c["messages"]), "the reply"
        )
        (reply,) = [m for m in page["messages"] if m["role"] == "director" and m["wake"] == 1]
        assert reply["rows"] == [] and reply["tool_calls"][0]["error"], reply
        assert any(
            "row 1 has started" in m["text"] for m in page["messages"] if m["role"] == "notice"
        )
    finally:
        finish(api, bid)
    assert rows_of(api, bid)[0]["inputs"]["inputs.delay"] == 10


# ---------------------------------------------------------------- providers and the host rule


def test_providers_admin_only_and_host_rule(api, admin, admin_view):
    t = tag()
    body = server_body(f"m-{t}")
    assert api.get(PROVIDERS).status_code == 403
    assert api.put(f"{PROVIDERS}/srv-{t}", json=body).status_code == 403
    for base in (
        "http://169.254.169.254/latest",  # cloud metadata: link-local
        "http://[fe80::1]/v1",
        "http://8.8.8.8/v1",  # public
        "http://0.0.0.0:8000/v1",
        "http://metadata.google.internal/v1",  # resolves to nothing private (or nothing at all)
        "http://user:pass@fake-openai:8000/v1",  # userinfo
        "http://fake-openai:8000/v1?x=1",
        "ftp://fake-openai/v1",
    ):
        r = admin.put(f"{PROVIDERS}/srv-{t}", json=body | {"base_url": base})
        assert r.status_code == 422, f"{base}: {r.status_code} {r.text}"
    assert (
        admin.put(f"{PROVIDERS}/srv-{t}", json=body | {"default_model": "nope"}).status_code == 422
    )
    assert (
        admin.put(
            f"{PROVIDERS}/srv-{t}", json={k: body[k] for k in body if k != "host"}
        ).status_code
        == 422
    )
    assert f"srv-{t}" not in {p["name"] for p in admin_providers(admin)}

    rb = remote_body(f"r-{t}")
    if admin_view["remote_enabled"]:
        r = admin.put(f"{PROVIDERS}/rmt-{t}", json=rb | {"base_url": "http://fake-openai:8000/v1"})
        assert r.status_code == 422, r.text  # a remote must be https
    else:
        r = admin.put(f"{PROVIDERS}/rmt-{t}", json=rb)
        assert (r.status_code, r.json()["detail"]) == (
            409,
            "remote models are off on this deployment",
        )
        assert all(s["kind"] != "remote" for s in ok(api.get(f"{STUDIO}/model-sources"))["sources"])

    with provider(admin, f"srv-{t}", body) as name:
        listed = {p["name"]: p for p in admin_providers(admin)}[name]
        assert (listed["api"], listed["default_model"], listed["enabled"]) == (
            "openai",
            f"m-{t}",
            True,
        )
        assert (
            ok(admin.put(f"{PROVIDERS}/{name}", json=body | {"timeout_s": 40}))["timeout_s"] == 40
        )
        r = admin.put(f"{PROVIDERS}/{name}", json=rb)
        assert r.status_code == 409, r.text  # a provider's kind never changes
        (src,) = [s for s in ok(api.get(f"{STUDIO}/model-sources"))["sources"] if s["name"] == name]
        assert src == {
            "name": name,
            "kind": "server",
            "title": "Fake server",
            "models": [f"m-{t}"],
            "default_model": f"m-{t}",
        }
        test = ok(admin.post(f"{PROVIDERS}/{name}/test", params={"model": f"m-{t}"}))
        assert (test["ok"], test["failure"], test["answer"]) == (True, None, "ok"), test
        assert api.post(f"{PROVIDERS}/{name}/test").status_code == 403
    assert admin.delete(f"{PROVIDERS}/{name}").status_code == 404
    assert admin.post(f"{PROVIDERS}/{name}/test").status_code == 404


def test_local_server(api, admin, palette, server, fake_ctl):
    """A decide and an agent on the local server: queue items (server-ask runs on its host, batch lane), egress and
    reserved at Submit, native tool calls, no key sent."""
    t = tag()
    src = f"server:{server['name']}"
    d = routed_agent("Contract server", tools=["memory.read"], model=src)
    want, info = expected(api, admin, d, palette)
    assert want == set()
    v = ok(api.post(f"{PIPES}/validate", json=d))
    assert v["models"] == {"boss": f"{src}/{server['model']}"}
    assert v["egress"] == info["egress"] and v["egress"][0]["where"] == "server"
    assert v["egress"][0]["fields"] == info["egress"][0]["fields"]
    assert v["reserved"] == [{"provider": server["name"], "host": SERVER_HOST, "memory_gb": 0.5}]
    p = create(api, d)
    t2 = turn("", ("route", {"to": "make", "reason": "go"}))
    b = submit(api, p["id"], [script(f"server {t}", turn("reading", ("memory_read", {})), t2)])
    try:
        r = admin.delete(f"{PROVIDERS}/{server['name']}")
        assert (r.status_code, r.json()["detail"]) == (
            409,
            f"{server['name']} is in use by an unfinished batch",
        )
        r = ended(api, b["id"], 1)
    finally:
        finish(api, b["id"])
    assert r["status"] == "succeeded", r
    assert b["egress"] == v["egress"] and b["reserved"] == v["reserved"]
    (dd,) = decisions(api, b["id"], 1)
    assert dd["source"] == {
        "kind": "server",
        "provider": server["name"],
        "model": server["model"],
        "host": SERVER_HOST,
    }
    assert dd["replayable"] and len(dd["run_ids"]) == 2
    for rid in dd["run_ids"]:
        run = ok(api.get(f"{STUDIO}/runs/{rid}"))
        assert (run["workflow_id"], run["lane"], run["status"]) == (
            "server-ask",
            "batch",
            "succeeded",
        )
    assert dd["agent"]["turns"][0]["calls"][0]["tool"] == "memory_read"
    reqs = sent(server["model"])
    assert len(reqs) == 2 and all(q["authorization"] is None for q in reqs)
    assert {f["function"]["name"] for f in reqs[0]["body"]["tools"]} == {"memory_read", "route"}
    assert f"server {t}" in json.dumps(reqs[0]["body"]["messages"])
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_remote_needs_both_switches(api, admin, palette, remote):
    t = tag()
    d = decided("Contract remote off", f"remote:{remote['name']}/{remote['model']}")
    v = ok(api.post(f"{PIPES}/validate", json=d))
    assert ("remote_not_enabled", "check") in {(p["code"], p.get("node")) for p in v["problems"]}
    p = create(api, d)
    r = api.post(
        BATCHES,
        json={"pipeline_id": p["id"], "rows": {"lines": {"param": "inputs.prompt", "values": [t]}}},
    )
    assert r.status_code == 422 and r.json()["detail"].startswith("pipeline cannot run: "), r.text
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def remote_doc(remote: dict, title: str, **caps) -> dict:
    on = {"enabled": True, "providers": [remote["name"]], "max_calls": 50, "max_tokens": 200000}
    return decided(title, f"remote:{remote['name']}/{remote['model']}", remote=on | caps)


def keep(text: str) -> str:
    return answer(text, {"route": "keep", "reason": "fine"})


def test_remote_calls(api, admin, palette, remote, fake_ctl):
    """What leaves the machine at Submit; at most two requests in flight; a network error retried and counted apart;
    Run again with the same decisions asks the remote model again."""
    t = tag()
    d = remote_doc(remote, "Contract remote")
    want, info = expected(api, admin, d, palette)
    assert want == set()
    p = create(api, d)
    model = remote["model"]
    fake("POST", "/__script", {model: [{"delay_s": 2}] * 5})
    b = submit(api, p["id"], [keep(f"row {i} {t}") for i in range(5)])
    try:
        rows = all_ended(api, b["id"])
        done = wait_batch(api, b["id"])
    finally:
        finish(api, b["id"])
    (eg,) = b["egress"]
    assert eg == by_node(info["egress"])[0]
    assert (eg["node"], eg["where"], eg["provider"], eg["model"], eg["thumbnails"]) == (
        "check",
        "remote",
        remote["name"],
        model,
        0,
    )
    assert set(eg["fields"]) >= {"question", "routes", "$prompt"}
    assert all(r["status"] == "succeeded" for r in rows), rows
    reqs = sent(model)
    assert len(reqs) == 5 and most_in_flight(reqs) == 2
    assert all((q["authorization"] or "").startswith("Bearer ") for q in reqs)
    assert done["remote"] == {
        "calls": 5,
        "tokens": done["remote"]["tokens"],
        "network_failures": 0,
        "max_calls": 50,
        "max_tokens": 200000,
    }
    (dd,) = decisions(api, b["id"], 1)
    assert dd["source"] == {"kind": "remote", "provider": remote["name"], "model": model}
    assert (dd["replayable"], dd["run_ids"], dd["network_failures"]) == (False, [], 0)

    # a network error: retried, counted apart, never a verdict
    fake("POST", "/__script", {model: [{"status": 503}]})
    b2 = submit(api, p["id"], [keep(f"flaky {t}")])
    try:
        (r,) = all_ended(api, b2["id"])
        done2 = wait_batch(api, b2["id"])
    finally:
        finish(api, b2["id"])
    assert r["status"] == "succeeded", r
    assert (done2["remote"]["calls"], done2["remote"]["network_failures"]) == (1, 1)
    (d2,) = decisions(api, b2["id"], 1)
    assert d2["network_failures"] == 1 and d2["decide"]["route"] == "keep"

    # Run again with the same decisions: a remote decision is asked again
    before = len(sent(model))
    again = ok(api.post(f"{BATCHES}/{b2['id']}/again", json={"decisions": "same"}), 201)
    try:
        assert wait_batch(api, again["id"])["status"] == "done"
    finally:
        finish(api, again["id"])
    (d3,) = decisions(api, again["id"], 1)
    assert (d3["replayed"], d3["replayed_from"]) == (False, None)
    assert len(sent(model)) == before + 1
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_remote_caps_and_network_failure(api, remote, fake_ctl):
    """Per-batch caps fail the rows past them with the contract's message; a provider that never answers fails the
    row as a network error after the retries."""
    t = tag()
    p = create(api, remote_doc(remote, "Contract remote caps", max_calls=2))
    b = submit(api, p["id"], [keep(f"cap {i} {t}") for i in range(4)])
    try:
        rows = all_ended(api, b["id"])
        done = wait_batch(api, b["id"])
    finally:
        finish(api, b["id"])
    assert [r["status"] for r in rows].count("succeeded") == 2, rows
    failed = [r for r in rows if r["status"] == "failed"]
    assert len(failed) == 2 and all("remote cap reached: 2 calls" in r["error"] for r in failed)
    assert done["remote"]["calls"] == 2

    tokens = create(api, remote_doc(remote, "Contract remote tokens", max_tokens=1000))
    fake("POST", "/__script", {remote["model"]: [{"usage": {"prompt_tokens": 1200}}]})
    b = submit(api, tokens["id"], [keep(f"tok {i} {t}") for i in range(2)])
    try:
        one, two = all_ended(api, b["id"])
    finally:
        finish(api, b["id"])
    assert one["status"] == "succeeded" and two["status"] == "failed", (one, two)
    assert "remote cap reached" in two["error"]

    fake("POST", "/__script", {remote["model"]: [{"status": 503}] * 4})
    b = submit(api, p["id"], [keep(f"down {t}")])
    try:
        (r,) = all_ended(api, b["id"])
        done = wait_batch(api, b["id"])
    finally:
        finish(api, b["id"])
    assert r["status"] == "failed"
    assert r["error"] == f"check: {remote['name']}: the provider is busy or down (HTTP 503)", r["error"]
    assert done["remote"]["network_failures"] == 4  # the first try and 3 retries, none a verdict
    for x in (p, tokens):
        assert api.delete(f"{PIPES}/{x['id']}").status_code == 204


# ---------------------------------------------------------------- custom nodes


def test_custom_nodes(api, palette):
    nodes = ok(api.get(f"{STUDIO}/custom-nodes"))["nodes"]
    shipped = json.loads((CONTRACTS / "fixtures/registry/pipelines.custom.valid.json").read_text())
    (want,) = shipped["nodes"]
    (got,) = [n for n in nodes if n["name"] == "text/prefix"]
    assert {k: got[k] for k in ("name", "input", "output", "network")} == {
        k: want[k] for k in ("name", "input", "output", "network")
    }
    assert re.fullmatch(r"[0-9a-f]{12}", got["version"])

    t = tag()
    d = {
        "title": "Contract custom",
        "nodes": {
            "inputs": inputs_node("prompt"),
            "pre": {
                "kind": "custom",
                "node": "text/prefix",
                "inputs": {"prefix": "cinematic, "},
                "bind": {"text": "$prompt"},
            },
            "still": step({"prompt.text": "pre.text"}),
            "out": {"kind": "output", "from": "still.takes", "collection": "Contract custom"},
        },
        "edges": [["inputs", "pre"], ["pre", "still"], ["still", "out"]],
    }
    bad = copy.deepcopy(d)
    bad["nodes"]["pre"]["inputs"] = {"prefix": "x", "depth": 2}
    v = ok(api.post(f"{PIPES}/validate", json=bad))
    assert ("custom_input", "pre", "depth") in {
        (p["code"], p.get("node"), p.get("port")) for p in v["problems"]
    }
    p = create(api, d)
    b = submit(api, p["id"], [f"a fox {t}"])
    try:
        r = ended(api, b["id"], 1)
    finally:
        finish(api, b["id"])
    assert r["status"] == "succeeded", r
    (tid,) = r["take_ids"]
    assert (
        ok(api.get(f"{STUDIO}/takes/{tid}"))["made_with"]["inputs"]["prompt.text"]
        == f"cinematic, a fox {t}"
    )
    assert [s["part"] for s in execution(api, b["id"], 1)["steps"] if s["node"] == "pre"] == [
        "custom"
    ]
    assert b["egress"] == []
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_unknown_ids(api):
    assert api.get(f"{BATCHES}/{NO_SUCH_ID}/chat").status_code == 404
    assert api.post(f"{BATCHES}/{NO_SUCH_ID}/chat", json={"text": "hi"}).status_code == 404
