"""User workflows (API 1.5) on the fake runner: the node palette, copying the echo template, editing with versions,
the graph rules over contracts/fixtures/graphs (validate and save), runs and batches of a user workflow with their
snapshots, reuse after an edit or a delete, delete conflicts, templates read-only, isolation.

Expected problems for a fixture come from the contracts' reference implementation (contracts/check.py
graph_problems) run against the LIVE palette (GET /nodes); when every kind of a fixture is live they must also equal
the fixture's own `problems` (written against fixtures/graphs/palette.json)."""

import json

import pytest
from helpers import (  # check: the contracts' reference graph rules
    CONTRACTS,
    NO_SUCH_ID,
    STUDIO,
    check,
    new_session,
    ok,
    start_run,
    wait_run,
)
from test_batches import BATCHES, SLOWER, finish, lines, rows_of, submit, wait_batch

WF = f"{STUDIO}/workflows"
GRAPHS = sorted((CONTRACTS / "fixtures" / "graphs").glob("*valid.*.json"))  # valid.* and invalid.*


def doc(w: dict) -> dict:
    """The WorkflowDoc part of a Workflow (a deep copy: callers edit it)."""
    return json.loads(
        json.dumps({k: w[k] for k in ("title", "family", "env", "nodes", "studio") if k in w})
    )


def keys(problems: list[dict]) -> set[tuple]:
    return {(p["code"], p.get("node"), p.get("port"), p.get("param")) for p in problems}


def sort_key(p: dict) -> tuple:
    return ("node" in p, p.get("node", ""), p["code"], p.get("port", ""), p.get("param", ""))


def strip_positions(nodes: dict) -> dict:
    return {nid: {k: v for k, v in n.items() if k != "position"} for nid, n in nodes.items()}


def copy_echo(client, title: str | None = None) -> dict:
    body = {"based_on": "echo"} | ({"title": title} if title else {})
    return ok(client.post(WF, json=body), 201)


def edited(w: dict) -> dict:
    """The copy edited: quicker defaults, a second echo → output chain, positions and a studio block."""
    d = doc(w)
    nodes = d["nodes"]
    nodes["echo"]["params"] |= {"steps": 2, "delay_ms": 50}
    nodes["echo_2"] = {
        "kind": "echo/echo",
        "in": {"text": "prompt.text"},
        "params": {"steps": 3, "delay_ms": 0},
    }
    nodes["output_2"] = {"kind": "output", "in": {"media": "echo_2.image"}}
    for i, nid in enumerate(sorted(nodes)):
        nodes[nid]["position"] = {"x": 280 * i, "y": 40}
    d["studio"] = {
        "composer": ["prompt.text"],
        "quick": ["echo.steps"],
        "inspector": [{"title": "Echo", "items": ["echo.delay_ms", "echo_2.steps"]}],
        "labels": {"echo.steps": "Steps"},
        "presets": {"Fast": {"echo.steps": 2, "echo.delay_ms": 0}},
    }
    return d


def put(client, wid: str, body: dict, version: int, status: int = 200):
    return ok(client.put(f"{WF}/{wid}", json=body | {"version": version}), status)


@pytest.fixture(scope="module")
def mine(api) -> dict:
    """User A's copy of echo, edited (version 2)."""
    w = copy_echo(api, "Contract echo, edited")
    return put(api, w["id"], edited(w), 1)


def test_nodes_palette(api, palette):
    groups = ok(api.get(f"{STUDIO}/nodes"))["groups"]
    assert (groups[0]["family"], groups[0]["title"]) == (None, "Core")
    assert {"prompt", "output"} <= {n["kind"] for n in groups[0]["nodes"]}
    (echo_group,) = [g for g in groups if g["family"] == "echo"]
    echo = next(n for n in echo_group["nodes"] if n["kind"] == "echo/echo")
    assert "fake" in echo["envs"] and echo["family"] == "echo"
    assert [p["name"] for p in echo["params"]][:3] == ["steps", "delay_ms", "fail"]
    assert echo["inputs"][0] == {"name": "text", "type": "Text"}
    assert echo["outputs"] == [{"name": "image", "type": "Image"}]
    template = ok(api.get(f"{WF}/echo"))["node_schemas"]
    assert {k: v for k, v in echo.items() if k != "envs"} == template["echo/echo"]
    fake = ok(api.get(f"{STUDIO}/nodes", params={"env": "fake"}))["groups"]
    assert all("fake" in n["envs"] for g in fake for n in g["nodes"])
    assert {n["kind"] for g in fake for n in g["nodes"]} >= {"prompt", "output", "echo/echo"}
    assert ok(api.get(f"{STUDIO}/nodes", params={"env": "nosuchenv"}))["groups"] == []
    assert api.get(f"{STUDIO}/nodes", params={"env": "Not An Env!"}).status_code == 422


def test_copy_template(api):
    template = ok(api.get(f"{WF}/echo"))
    w = copy_echo(api)
    assert (w["source"], w["based_on"], w["version"]) == ("mine", "echo", 1)
    assert w["title"] == f"{template['title']} copy"[:80] and w["id"] != "echo"
    assert (w["family"], w["env"]) == (template["family"], template["env"])
    assert strip_positions(w["nodes"]) == strip_positions(template["nodes"])
    assert w["node_schemas"] == template["node_schemas"] and w["problems"] == []
    assert w["created_at"] and w["updated_at"]
    assert ok(api.get(f"{WF}/{w['id']}")) | {"problems": []} == w | {"problems": []}
    # a copy of one's own copy
    again = ok(api.post(WF, json={"based_on": w["id"], "title": "Copy of a copy"}), 201)
    assert (again["based_on"], again["title"], again["version"]) == (w["id"], "Copy of a copy", 1)
    assert api.post(WF, json={"based_on": "no-such-workflow"}).status_code == 404
    assert api.post(WF, json={"based_on": NO_SUCH_ID}).status_code == 404
    for wid in (w["id"], again["id"]):
        assert api.delete(f"{WF}/{wid}").status_code == 204


def test_list_templates_then_mine(api, mine):
    every = ok(api.get(WF))["workflows"]
    sources = [w["source"] for w in every]
    assert sources == sorted(sources, key=lambda s: s != "template")  # templates first
    templates = [w for w in every if w["source"] == "template"]
    own = [w for w in every if w["source"] == "mine"]
    assert [w["title"] for w in templates] == sorted(w["title"] for w in templates)
    assert [w["updated_at"] for w in own] == sorted((w["updated_at"] for w in own), reverse=True)
    assert all("problems" not in w for w in every)
    assert mine["id"] in {w["id"] for w in own} and "echo" in {w["id"] for w in templates}
    only = lambda s: ok(api.get(WF, params={"source": s}))["workflows"]
    assert [w["id"] for w in only("template")] == [w["id"] for w in templates]
    assert [w["id"] for w in only("mine")] == [w["id"] for w in own]
    assert api.get(WF, params={"source": "theirs"}).status_code == 422


def test_edit_with_versions(api, mine):
    assert (mine["version"], mine["problems"]) == (2, [])
    assert mine["nodes"]["echo"]["params"]["steps"] == 2
    assert mine["nodes"]["output_2"]["in"] == {"media": "echo_2.image"}
    assert mine["nodes"]["echo_2"]["position"] == edited(mine)["nodes"]["echo_2"]["position"]
    assert mine["studio"] == edited(mine)["studio"]
    assert ok(api.get(f"{WF}/{mine['id']}")) == mine
    stale = api.put(f"{WF}/{mine['id']}", json=doc(mine) | {"version": 1})
    assert stale.status_code == 409 and "now version 2" in stale.json()["detail"]
    assert ok(api.get(f"{WF}/{mine['id']}"))["version"] == 2  # nothing changed


def test_rename_bumps_version(api):
    w = copy_echo(api)
    renamed = ok(api.patch(f"{WF}/{w['id']}", json={"title": "  Renamed  "}))
    assert (renamed["title"], renamed["version"]) == ("Renamed", 2)
    assert api.put(f"{WF}/{w['id']}", json=doc(w) | {"version": 1}).status_code == 409
    assert api.patch(f"{WF}/{w['id']}", json={"title": "   "}).status_code == 422
    assert api.delete(f"{WF}/{w['id']}").status_code == 204


def test_templates_are_read_only(api):
    echo = ok(api.get(f"{WF}/echo"))
    assert api.put(f"{WF}/echo", json=doc(echo) | {"version": 1}).status_code == 409
    assert api.patch(f"{WF}/echo", json={"title": "Mine"}).status_code == 409
    assert api.delete(f"{WF}/echo").status_code == 409
    assert api.get(f"{WF}/{NO_SUCH_ID}").status_code == 404


def expected(fx: dict, palette: dict) -> set[tuple]:
    want = check.graph_problems(fx["workflow"], palette)
    if {n["kind"] for n in fx["workflow"]["nodes"].values()} <= set(palette):
        assert want == keys(fx["problems"]), (
            "the live palette differs from fixtures/graphs/palette.json"
        )
    return want


@pytest.mark.parametrize("path", GRAPHS, ids=lambda p: p.stem)
def test_validate_fixture(api, palette, path):
    fx = json.loads(path.read_text())
    want = expected(fx, palette)
    v = ok(api.post(f"{WF}/validate", json=fx["workflow"]))
    assert keys(v["problems"]) == want and len(v["problems"]) == len(want)
    assert v["problems"] == sorted(v["problems"], key=sort_key)
    for p in v["problems"]:
        assert p["blocks"] == ("run" if p["code"] in check.RUN_ONLY else "save"), p
    assert v["can_run"] == (not want)
    assert v["can_save"] == all(code in check.RUN_ONLY for code, *_ in want)


@pytest.mark.parametrize("path", GRAPHS, ids=lambda p: p.stem)
def test_save_fixture(api, palette, path):
    fx = json.loads(path.read_text())
    want = expected(fx, palette)
    r = api.post(WF, json=fx["workflow"])
    if all(code in check.RUN_ONLY for code, *_ in want):
        w = ok(r, 201)
        assert keys(w["problems"]) == want and w["source"] == "mine"
        assert ok(api.get(f"{WF}/{w['id']}"))["problems"] == w["problems"]
        assert api.delete(f"{WF}/{w['id']}").status_code == 204
    else:
        body = ok(r, 422)
        assert keys(body["problems"]) == want and body["detail"].startswith("workflow has ")


def test_a_huge_graph_is_refused_fast(api):
    """A ~0.8 MB chain of 18k nodes gets the size problems only, without the per-node rules (bounded work)."""
    nodes = {"a0": {"kind": "x"}} | {
        f"a{i}": {"kind": "x", "in": {"i": f"a{i - 1}.o"}} for i in range(1, 18000)
    }
    body = {"title": "Huge", "family": "echo", "env": "fake", "nodes": nodes}
    r = api.post(f"{WF}/validate", json=body, timeout=10)
    assert keys(ok(r)["problems"]) == {
        ("too_large", None, None, None),
        ("too_many_nodes", None, None, None),
    }
    assert r.elapsed.total_seconds() < 5
    assert ok(api.post(WF, json=body, timeout=10), 422)["detail"].startswith(
        "workflow has 2 problems"
    )


def test_unknown_kind_blocks_running(api):
    fx = json.loads((CONTRACTS / "fixtures/graphs/invalid.unknown-kind.json").read_text())
    w = ok(api.post(WF, json=fx["workflow"]), 201)  # unknown_kind blocks running only
    assert keys(w["problems"]) == {("unknown_kind", "up", None, None)}
    sid = new_session(api, "Contract unknown kind")["id"]
    r = api.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": w["id"], "inputs": {}})
    detail = ok(r, 422)["detail"]
    assert detail.startswith("workflow cannot run: ") and "up" in detail, detail
    r = api.post(BATCHES, json={"workflow_id": w["id"], "rows": lines(["x"])})
    assert ok(r, 422)["detail"].startswith("workflow cannot run: ")
    assert api.delete(f"{WF}/{w['id']}").status_code == 204


def test_run_snapshot_edit_and_delete(api, mine):
    wid = mine["id"]
    sid = new_session(api, "Contract user workflow")["id"]
    run = start_run(api, sid, {"prompt.text": "a user fox"}, wid)
    assert run["workflow_id"] == wid and run["workflow_version"] == 2
    done = wait_run(api, run["id"])
    assert done["status"] == "succeeded", done
    assert len(done["outputs"]) == 2  # one take per output node
    takes = [ok(api.get(f"{STUDIO}/takes/{o['take_id']}")) for o in done["outputs"]]
    for t in takes:
        mw = t["made_with"]
        assert (mw["workflow_id"], mw["workflow_title"], mw["workflow_version"]) == (
            wid,
            mine["title"],
            2,
        )
        assert mw["inputs"]["prompt.text"] == "a user fox" and mw["prompt"] == "a user fox"
        assert (mw["inputs"]["echo.steps"], mw["inputs"]["echo_2.steps"]) == (2, 3)
    assert ok(api.post(f"{STUDIO}/takes/{takes[0]['id']}/reuse")) == {
        "workflow_id": wid,
        "inputs": takes[0]["made_with"]["inputs"],
        "workflow_status": "current",
    }

    # edit after the run: drop the second chain, change a default
    d = doc(mine)
    del d["nodes"]["echo_2"], d["nodes"]["output_2"]
    d["nodes"]["echo"]["params"]["steps"] = 4
    d["studio"]["inspector"][0]["items"] = ["echo.delay_ms"]
    now = put(api, wid, d, 2)
    assert now["version"] == 3 and set(now["nodes"]) == {"prompt", "echo", "output"}
    again = ok(api.get(f"{STUDIO}/runs/{run['id']}"))
    assert (again["status"], again["workflow_version"], again["inputs"]) == (
        "succeeded",
        2,
        done["inputs"],
    )
    assert [o["take_id"] for o in again["outputs"]] == [t["id"] for t in takes]
    for t in takes:
        assert ok(api.get(f"{STUDIO}/takes/{t['id']}"))["made_with"] == t["made_with"]
    reuse = ok(api.post(f"{STUDIO}/takes/{takes[0]['id']}/reuse"))
    recorded = takes[0]["made_with"]["inputs"]
    assert reuse["workflow_status"] == "changed"
    assert reuse["inputs"] == {k: v for k, v in recorded.items() if not k.startswith("echo_2.")}

    # delete: 409 while a run of it is queued or running, 204 once it ended
    slow = start_run(
        api, sid, {"prompt.text": "slow", "echo.steps": 200, "echo.delay_ms": 300}, wid
    )
    r = api.delete(f"{WF}/{wid}")
    assert r.status_code == 409 and "run of this workflow" in r.json()["detail"], r.text
    ok(api.post(f"{STUDIO}/runs/{slow['id']}/cancel"), 202)
    assert wait_run(api, slow["id"])["status"] == "cancelled"
    assert api.delete(f"{WF}/{wid}").status_code == 204
    assert api.get(f"{WF}/{wid}").status_code == 404
    assert api.delete(f"{WF}/{wid}").status_code == 404
    assert wid not in {w["id"] for w in ok(api.get(WF))["workflows"]}

    # the takes survive with made_with intact; reuse gives the inputs as recorded, a run of it is 422
    for t in takes:
        assert ok(api.get(f"{STUDIO}/takes/{t['id']}"))["made_with"] == t["made_with"]
    reuse = ok(api.post(f"{STUDIO}/takes/{takes[0]['id']}/reuse"))
    assert reuse == {"workflow_id": wid, "inputs": recorded, "workflow_status": "deleted"}
    body = {"workflow_id": wid, "inputs": reuse["inputs"]}
    assert api.post(f"{STUDIO}/sessions/{sid}/runs", json=body).status_code == 422


def test_batch_over_user_workflow(api):
    w = copy_echo(api, "Contract batch workflow")
    batch = submit(api, lines(["a heron", "a crane"]), SLOWER, workflow_id=w["id"])
    try:
        r = api.delete(f"{WF}/{w['id']}")
        assert r.status_code == 409 and "batch of this workflow" in r.json()["detail"], r.text
        # an edit after Submit does not change the batch: it runs the version at Submit (this one would fail)
        failing = doc(w)
        failing["nodes"]["echo"]["params"]["fail"] = True
        put(api, w["id"], failing, 1)
        done = wait_batch(api, batch["id"])
    finally:
        finish(api, batch["id"])
    assert done["status"] == "done" and batch["workflow_id"] == w["id"]
    for row in rows_of(api, batch["id"]):
        assert row["status"] == "succeeded" and len(row["take_ids"]) == 1, row
        run = ok(api.get(f"{STUDIO}/runs/{row['run_id']}"))
        assert (run["workflow_id"], run["workflow_version"]) == (w["id"], 1)
        mw = ok(api.get(f"{STUDIO}/takes/{row['take_ids'][0]}"))["made_with"]
        assert (mw["workflow_id"], mw["workflow_title"], mw["workflow_version"]) == (
            w["id"],
            "Contract batch workflow",
            1,
        )
    assert api.delete(f"{WF}/{w['id']}").status_code == 204


def test_isolation(api, api_b):
    a = copy_echo(api, "Contract A's own")
    wid = a["id"]
    calls = [
        ("GET", f"{WF}/{wid}", None),
        ("PUT", f"{WF}/{wid}", doc(a) | {"version": 1}),
        ("PATCH", f"{WF}/{wid}", {"title": "mine now"}),
        ("DELETE", f"{WF}/{wid}", None),
        ("POST", WF, {"based_on": wid}),  # copy
        ("POST", WF, doc(a) | {"based_on": wid}),  # save as, from A's
    ]
    for method, path, body in calls:
        r = api_b.request(method, path, json=body)
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    # running another user's workflow is an unknown workflow (422), as for runs and batches of any unknown id
    sid = new_session(api_b, "Contract B")["id"]
    r = api_b.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": wid, "inputs": {}})
    assert r.status_code == 422 and f"unknown workflow {wid}" in r.json()["detail"], r.text
    r = api_b.post(BATCHES, json={"workflow_id": wid, "rows": lines(["x"])})
    assert r.status_code == 422 and f"unknown workflow {wid}" in r.json()["detail"], r.text

    b = copy_echo(api_b, "Contract B's own")
    listed = ok(api_b.get(WF))["workflows"]
    assert [w["id"] for w in listed if w["source"] == "mine"] == [b["id"]]
    assert [w["id"] for w in ok(api_b.get(WF, params={"source": "mine"}))["workflows"]] == [b["id"]]
    assert b["id"] not in {w["id"] for w in ok(api.get(WF))["workflows"]}
    assert ok(api.get(f"{WF}/{wid}")) | {"problems": []} == a | {"problems": []}  # unchanged
    assert api.get(f"{WF}/{b['id']}").status_code == 404
    for client, w in ((api, a), (api_b, b)):
        assert client.delete(f"{WF}/{w['id']}").status_code == 204
