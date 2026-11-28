"""Saved pipelines (API 1.7) on the fake runner: documents with versions and caps, the pipeline rules over
contracts/fixtures/pipelines (validate and save), a two-step echo → echo batch (lineage, made_with.pipeline, cells),
gates (parked rows, the review grid, picks in any order, idempotent and refused picks), a capped reject loop (stacked
attempts, the execution view, the state after a step), Retry row from here, Pause / Resume / Stop with parked rows,
delete and edit while a batch runs, Save as pipeline, isolation.

Expected problems for a fixture come from the contracts' reference implementation (contracts/check.py
pipeline_problems) against the LIVE workflows and palette; its save-blocking ones must equal the fixture's own (they
depend on the document alone). Echo has no seed param, so every step here makes one run per visit."""

import copy
import json
import time

import pytest
from conftest import headers, make_client
from helpers import (
    CONTRACTS,
    NO_SUCH_ID,
    STUDIO,
    TERMINAL,
    all_takes,
    check,
    new_session,
    ok,
    png_bytes,
    ref,
    start_run,
    upload,
    usable_looks,
    usable_loras,
    wait_run,
)
from test_batches import BATCHES, FINAL, finish, lines, rows_of, wait_batch, wait_rows
from test_user_workflows import doc, keys, sort_key

PIPES = f"{STUDIO}/pipelines"
PIPELINE_FIXTURES = sorted((CONTRACTS / "fixtures" / "pipelines").glob("*valid.*.json"))
FAST = {"echo.steps": 2, "echo.delay_ms": 50}
PROMPTS = ["a red fox", "a blue heron", "a green frog"]


# ---------------------------------------------------------------- documents


def inputs_node(*names: str) -> dict:
    return {"kind": "inputs", "inputs": [{"name": n, "type": "Text"} for n in names]}


def echo_step(bind: dict, **inputs) -> dict:
    return {"kind": "step", "workflow": "echo", "inputs": FAST | inputs, "bind": bind}


def chain(title: str = "Contract chain") -> dict:
    """inputs → s1 (echo of the prompt) → s2 (echo with s1's takes as refs) → out."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "s1": echo_step({"prompt.text": "$prompt"}),
            "s2": echo_step({"prompt.text": "$prompt", "echo.refs": "s1.takes"}),
            "out": {"kind": "output", "from": "s2.takes", "collection": "Contract chain"},
        },
        "edges": [["inputs", "s1"], ["s1", "s2"], ["s2", "out"]],
    }


def gated(title: str = "Contract gate", cap: int = 2, edit_inputs: dict | None = None) -> dict:
    """inputs → still → review (keep 1; reject = still again, `cap` times, then out) → edit (refs = the pick) → out.
    Reaching out through `then` ends the row with no takes (edit never ran)."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": echo_step({"prompt.text": "$prompt"}),
            "review": {"kind": "gate", "title": "Pick one", "takes": "still", "keep": 1},
            "edit": echo_step(
                {"prompt.text": "$prompt", "echo.refs": "review.picked"}, **(edit_inputs or {})
            ),
            "out": {"kind": "output", "from": "edit.takes", "collection": "Contract gate"},
        },
        "edges": [
            ["inputs", "still"],
            ["still", "review"],
            ["review", {"pass": "edit", "fail": "still", "max": cap, "then": "out"}],
            ["edit", "out"],
        ],
    }


def pdoc(p: dict) -> dict:
    """The PipelineDoc part of a Pipeline (a deep copy)."""
    return copy.deepcopy({k: p[k] for k in ("title", "nodes", "edges")})


def create(client, body: dict) -> dict:
    return ok(client.post(PIPES, json=body), 201)


def submit(client, pid: str, values: list[str], **extra) -> dict:
    body = {"pipeline_id": pid, "rows": lines(values, "inputs.prompt")} | extra
    batch = ok(client.post(BATCHES, json=body), 202)
    assert (batch["status"], batch["control"]) == ("queued", "run"), batch
    return batch


def row(client, bid: str, n: int) -> dict:
    return rows_of(client, bid)[n - 1]


def parked(client, bid: str, rows: list[int], visit: int = 1) -> list[dict]:
    """Wait until every row of `rows` is waiting at its gate on that visit."""

    def check_(rs):
        return all(
            rs[n - 1]["status"] == "waiting" and rs[n - 1]["parked"]["visit"] == visit for n in rows
        )

    return wait_rows(client, bid, check_, f"rows {rows} parked (visit {visit})")


def ended(client, bid: str, n: int, attempt: int = 1) -> dict:
    rs = wait_rows(
        client,
        bid,
        lambda rs: rs[n - 1]["attempt"] == attempt and rs[n - 1]["status"] in TERMINAL,
        f"row {n} attempt {attempt} to end",
    )
    return rs[n - 1]


def pick(client, bid: str, n: int, take_ids: list[str], status: int = 202, gate: str = "review"):
    return ok(
        client.post(f"{BATCHES}/{bid}/rows/{n}/pick", json={"gate": gate, "take_ids": take_ids}),
        status,
    )


def reject(client, bid: str, n: int, gate: str = "review") -> dict:
    return ok(
        client.post(f"{BATCHES}/{bid}/rows/{n}/pick", json={"gate": gate, "reject": True}), 202
    )


def execution(client, bid: str, n: int, **params) -> dict:
    return ok(client.get(f"{BATCHES}/{bid}/rows/{n}/execution", params=params))


def cell(r: dict, node: str) -> dict:
    (c,) = [c for c in r["cells"] if c["node"] == node]
    return c


def batch_takes(client, bid: str) -> list[dict]:
    return [t for t in all_takes(client, source="pipeline") if t["batch_id"] == bid]


# ---------------------------------------------------------------- documents: CRUD, caps, rules


def test_crud_with_versions(api):
    p = create(api, chain())
    pid = p["id"]
    assert (p["version"], p["based_on"], p["problems"], p["last_batch"]) == (1, None, [], None)
    assert p["workflow_titles"] == {"echo": "Echo"} and p["created_at"] and p["updated_at"]
    assert pdoc(p) == chain()
    assert ok(api.get(f"{PIPES}/{pid}")) == p

    edited = chain("Contract chain, edited")
    edited["nodes"]["s1"]["inputs"]["echo.steps"] = 3
    edited["nodes"]["s1"]["position"] = {"x": 280, "y": 40}
    v2 = ok(api.put(f"{PIPES}/{pid}", json=edited | {"version": 1}))
    assert (v2["version"], v2["title"], pdoc(v2)) == (2, "Contract chain, edited", edited)
    stale = api.put(f"{PIPES}/{pid}", json=chain() | {"version": 1})
    assert stale.status_code == 409, stale.text
    assert stale.json()["detail"] == "pipeline changed since you opened it (now version 2)"
    assert ok(api.get(f"{PIPES}/{pid}"))["version"] == 2  # nothing changed

    renamed = ok(api.patch(f"{PIPES}/{pid}", json={"title": "  Renamed  "}))
    assert (renamed["title"], renamed["version"]) == ("Renamed", 3)
    assert api.patch(f"{PIPES}/{pid}", json={"title": "   "}).status_code == 422

    dup = create(api, {"based_on": pid})
    assert (dup["title"], dup["based_on"], dup["version"]) == ("Renamed copy", pid, 1)
    assert pdoc(dup)["nodes"] == pdoc(renamed)["nodes"]
    named = create(api, {"based_on": pid, "title": "Named copy"})
    assert named["title"] == "Named copy"
    assert api.post(PIPES, json={"based_on": NO_SUCH_ID}).status_code == 404

    listed = ok(api.get(PIPES))["pipelines"]
    ids = [x["id"] for x in listed]
    assert {pid, dup["id"], named["id"]} <= set(ids)
    assert all("problems" not in x for x in listed)
    stamps = [x["updated_at"] for x in listed]
    assert stamps == sorted(stamps, reverse=True)  # newest first
    for x in (pid, dup["id"], named["id"]):
        assert api.delete(f"{PIPES}/{x}").status_code == 204
    assert api.get(f"{PIPES}/{pid}").status_code == 404
    assert api.delete(f"{PIPES}/{pid}").status_code == 404
    assert api.get(f"{PIPES}/not-a-uuid").status_code == 404


def test_at_most_100_pipelines(new_user):
    with make_client(headers(new_user("P")["access_token"])) as c:
        made = [create(c, chain(f"Cap {i}"))["id"] for i in range(100)]
        r = c.post(PIPES, json=chain("One too many"))
        assert (
            r.status_code == 409
            and r.json()["detail"] == "You have 100 pipelines; delete some first."
        )
        assert c.post(PIPES, json={"based_on": made[0]}).status_code == 409
        assert len(ok(c.get(PIPES))["pipelines"]) == 100
        for pid in made:
            assert c.delete(f"{PIPES}/{pid}").status_code == 204


def live_workflows(client, pipeline: dict) -> dict:
    """id → WorkflowDoc of every workflow the pipeline's steps name that the caller can run now, plus the ask (text)
    workflows its judge, write and decide nodes may run (API 1.14): the named models, `llm-ask` and `<family>-ask`
    of each step's family (text workflows are never listed, so `auto`'s candidates are fetched by those names)."""
    out = {}

    def fetch(wid: str) -> None:
        if wid not in out and (r := client.get(f"{STUDIO}/workflows/{wid}")).status_code == 200:
            out[wid] = doc(r.json())

    nodes = list(pipeline["nodes"].values())
    for n in nodes:
        if n["kind"] == "step":
            fetch(n["workflow"])
    for n in nodes:
        m = n.get("model", "auto")
        if n["kind"] in ("judge", "write", "decide", "agent") and m != "auto" and ":" not in m:
            fetch(m)  # server:/remote: sources are providers, not workflows (API 1.15)
    for fam in sorted({w["family"] for w in out.values() if w.get("family")}):
        fetch(
            f"{fam}-ask"
        )  # ponytail: the templates' naming (echo-ask, qwen-image-ask); a list route would be exact
    fetch("llm-ask")
    return out


def live_sources(client) -> dict:
    """check.pipeline_problems' 1.15 context as the caller sees it now: the enabled providers (GET /model-sources,
    redacted: no host or memory_gb, so `reserved` needs the admin's list), the deployment's remote switch and the
    registered custom nodes; from 1.74 what Auto resolves to."""
    ms = ok(client.get(f"{STUDIO}/model-sources"))
    return {
        "providers": [s | {"enabled": True} for s in ms["sources"]],
        "remote_on": ms["remote_enabled"],
        "custom": ok(client.get(f"{STUDIO}/custom-nodes"))["nodes"],
        # 1.74: the pipelines default model as Auto resolves now (null: the local LLM)
        "auto": f"{a['kind']}:{a['provider']}/{a['model']}" if (a := ms.get("auto")) else None,
    }


@pytest.mark.parametrize("path", PIPELINE_FIXTURES, ids=lambda p: p.stem)
def test_fixture_validate_and_save(api, palette, path):
    fx = json.loads(path.read_text())
    want = check.pipeline_problems(
        fx["pipeline"],
        live_workflows(api, fx["pipeline"]),
        palette,
        usable_looks(api),
        usable_loras(api),
        **live_sources(api),
    )
    save_blocking = {k for k in want if k[0] not in check.P_RUN_ONLY}
    assert save_blocking == {k for k in keys(fx["problems"]) if k[0] not in check.P_RUN_ONLY}

    v = ok(api.post(f"{PIPES}/validate", json=fx["pipeline"]))
    assert keys(v["problems"]) == want and len(v["problems"]) == len(want)
    assert v["problems"] == sorted(v["problems"], key=sort_key)
    for p in v["problems"]:
        assert p["blocks"] == ("run" if p["code"] in check.P_RUN_ONLY else "save"), p
    assert (v["can_run"], v["can_save"]) == (not want, not save_blocking)

    r = api.post(PIPES, json=fx["pipeline"])
    if save_blocking:
        body = ok(r, 422)
        assert keys(body["problems"]) == want and body["detail"].startswith("pipeline has "), body
    else:
        p = ok(r, 201)
        assert keys(p["problems"]) == want
        assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_a_pipeline_that_cannot_run_is_refused_at_submit(api):
    d = chain("Contract unrunnable")
    d["nodes"]["s1"]["workflow"] = "no-such-workflow"
    p = create(api, d)  # unknown_workflow blocks running only
    assert {x["code"] for x in p["problems"]} == {"unknown_workflow"}
    r = api.post(BATCHES, json={"pipeline_id": p["id"], "rows": lines(["x"], "inputs.prompt")})
    assert r.status_code == 422 and r.json()["detail"].startswith("pipeline cannot run: "), r.text
    r = api.post(BATCHES, json={"pipeline_id": NO_SUCH_ID, "rows": lines(["x"], "inputs.prompt")})
    assert r.status_code == 422 and r.json()["detail"] == f"unknown pipeline {NO_SUCH_ID}", r.text
    assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


# ---------------------------------------------------------------- a two-step chain


@pytest.fixture(scope="module")
def chain_batch(api) -> dict:
    """A's finished batch of the chain over 3 lines, the pipeline edited (and refused delete) while it ran:
    {"pipeline" (version 1), "batch" (as submitted), "done", "rows", "takes", "delete", "edited"}."""
    p = create(api, chain())
    batch = submit(api, p["id"], PROMPTS)
    try:
        delete = api.delete(f"{PIPES}/{p['id']}")
        broken = chain("Contract chain, edited after submit")
        broken["nodes"]["s2"]["inputs"]["echo.fail"] = (
            True  # would fail every row if the batch used it
        )
        edited = ok(api.put(f"{PIPES}/{p['id']}", json=broken | {"version": 1}))
        done = wait_batch(api, batch["id"])
    finally:
        finish(api, batch["id"])
    return {
        "pipeline": p,
        "batch": batch,
        "done": done,
        "rows": rows_of(api, batch["id"]),
        "takes": {t["id"]: t for t in batch_takes(api, batch["id"])},
        "delete": delete,
        "edited": edited,
    }


def test_chain_batch(api, chain_batch):
    p, batch, done = chain_batch["pipeline"], chain_batch["batch"], chain_batch["done"]
    assert (batch["pipeline_id"], batch["pipeline_title"], batch["pipeline_version"]) == (
        p["id"],
        p["title"],
        1,
    )
    assert (batch["collection"], batch["workflow_id"]) == ("Contract chain", "echo")
    assert batch["title"] == "Contract chain × 3" and batch["counts"]["total"] == 3
    assert (done["status"], done["reason"]) == ("done", None)
    assert done["counts"] | {"waiting": done["counts"].get("waiting", 0)} == {
        "total": 3,
        "queued": 0,
        "running": 0,
        "waiting": 0,
        "succeeded": 3,
        "failed": 0,
        "cancelled": 0,
    }
    for r, prompt in zip(chain_batch["rows"], PROMPTS, strict=True):
        assert (r["status"], r["attempt"], r["error"], r["node"], r["parked"]) == (
            "succeeded",
            1,
            None,
            None,
            None,
        )
        assert r["inputs"] == {"inputs.prompt": prompt}
        assert [(c["node"], c["kind"]) for c in r["cells"]] == [
            ("s1", "step"),
            ("s2", "step"),
            ("out", "output"),
        ]
        for c in r["cells"]:
            (a,) = c["attempts"]
            assert (a["visit"], a["status"], a["error"]) == (1, "succeeded", None), c
        s1, s2, out = (c["attempts"][0] for c in r["cells"])
        assert (
            len(s1["run_ids"])
            == len(s1["take_ids"])
            == len(s2["run_ids"])
            == len(s2["take_ids"])
            == 1
        )
        assert s1["seconds"] is not None and s1["thumb_url"]
        assert out["take_ids"] == s2["take_ids"] == r["take_ids"]
        assert r["run_id"] == s2["run_ids"][0]  # its latest run


def test_chain_takes_and_lineage(api, chain_batch):
    p, bid, takes = chain_batch["pipeline"], chain_batch["batch"]["id"], chain_batch["takes"]
    assert len(takes) == 6  # two steps × three rows
    for r in chain_batch["rows"]:
        first = cell(r, "s1")["attempts"][0]["take_ids"][0]
        second = cell(r, "s2")["attempts"][0]["take_ids"][0]
        t1, t2 = ok(api.get(f"{STUDIO}/takes/{first}")), ok(api.get(f"{STUDIO}/takes/{second}"))
        assert (t1["source"], t1["batch_id"], t2["source"], t2["batch_id"]) == (
            "pipeline",
            bid,
            "pipeline",
            bid,
        )
        assert t1["parents"] == [] and t2["parents"] == [first]
        assert second in [c["id"] for c in t1["children"]]
        for t, node in ((t1, "s1"), (t2, "s2")):
            mw = t["made_with"]
            assert (
                mw["workflow_id"] == "echo"
                and mw["inputs"]["prompt.text"] == r["inputs"]["inputs.prompt"]
            )
            assert mw["pipeline"] == {
                "id": p["id"],
                "title": "Contract chain",
                "version": 1,
                "node": node,
                "attempt": 1,
                "row": r["row"],
            }
        assert t2["made_with"]["inputs"]["echo.refs"] == [ref(first)]
        run = ok(api.get(f"{STUDIO}/runs/{r['run_id']}"))
        assert (run["lane"], run["batch_id"], run["batch_row"]) == ("batch", bid, r["row"])


def test_edit_and_delete_while_a_batch_runs(api, chain_batch):
    d = chain_batch["delete"]
    assert (
        d.status_code == 409
        and d.json()["detail"] == "a batch of this pipeline is unfinished; stop it or wait"
    )
    assert chain_batch["edited"]["version"] == 2
    # the batch ran its snapshot (version 1): no row failed although version 2 fails every echo of s2
    assert all(r["status"] == "succeeded" for r in chain_batch["rows"])
    pid = chain_batch["pipeline"]["id"]
    assert ok(api.get(f"{PIPES}/{pid}"))["last_batch"]["id"] == chain_batch["batch"]["id"]
    ex = execution(api, chain_batch["batch"]["id"], 1)
    assert ex["pipeline"]["title"] == "Contract chain"  # the snapshot, not the edit
    assert ex["pipeline"]["nodes"]["s2"]["inputs"].get("echo.fail") is not True
    assert api.delete(f"{PIPES}/{pid}").status_code == 204  # the batch has ended
    assert ok(api.get(f"{BATCHES}/{chain_batch['batch']['id']}"))["pipeline_id"] == pid  # kept


def test_execution_of_a_chain_row(api, chain_batch):
    ex = execution(api, chain_batch["batch"]["id"], 2)
    assert (ex["row"], ex["attempt"], ex["attempts"], ex["status"], ex["current"]) == (
        2,
        1,
        1,
        "succeeded",
        None,
    )
    assert [(s["node"], s["part"]) for s in ex["steps"]] == [
        ("inputs", "inputs"),
        ("s1", "submit"),
        ("s1", "wait"),
        ("s2", "submit"),
        ("s2", "wait"),
        ("out", "output"),
    ]
    assert [s["step"] for s in ex["steps"]] == list(range(1, 7))
    assert ex["retried_from"] is None and ex["loops"] == {}
    assert not any(s["has_state"] for s in ex["steps"])  # the batch is done: its threads are gone
    r = api.get(f"{BATCHES}/{chain_batch['batch']['id']}/rows/2/execution/3/state")
    assert (
        r.status_code == 404
        and r.json()["detail"] == "this row's state is gone; the batch has finished"
    )
    assert (
        api.get(
            f"{BATCHES}/{chain_batch['batch']['id']}/rows/2/execution", params={"attempt": 2}
        ).status_code
        == 404
    )


# ---------------------------------------------------------------- gates


def test_gate_parks_rows_and_picks_in_any_order(api):
    p = create(api, gated())
    batch = submit(api, p["id"], PROMPTS)
    bid = batch["id"]
    try:
        rows = parked(api, bid, [1, 2, 3])
        b = wait_batch(api, bid, {"waiting"})
        assert b["counts"]["waiting"] == 3 and b["counts"]["running"] == 0
        for r in rows:
            assert (r["node"], r["run_id"] is not None) == ("review", True)
            assert {k: r["parked"][k] for k in ("gate", "visit", "keep")} == {
                "gate": "review",
                "visit": 1,
                "keep": 1,
            }
            assert r["parked"]["candidates"] == cell(r, "still")["attempts"][0]["take_ids"]
            gate = cell(r, "review")["attempts"][0]
            assert (gate["status"], gate["candidates"]) == ("waiting", r["parked"]["candidates"])

        review = ok(api.get(f"{BATCHES}/{bid}/review"))
        assert review["batch_id"] == bid and [i["row"] for i in review["items"]] == [1, 2, 3]
        for item, r in zip(review["items"], rows, strict=True):
            assert (item["gate"], item["gate_title"], item["visit"], item["keep"]) == (
                "review",
                "Pick one",
                1,
                1,
            )
            assert (item["on_reject"], item["rejects_left"], item["attempt"]) == ("retry", 2, 1)
            assert [c["take_id"] for c in item["candidates"]] == r["parked"]["candidates"]
            assert all(c["url"] and c["thumb_url"] and c["width"] for c in item["candidates"])

        cand = {r["row"]: r["parked"]["candidates"][0] for r in rows}
        # refused picks change nothing
        assert (
            api.post(
                f"{BATCHES}/{bid}/rows/1/pick", json={"gate": "review", "take_ids": [NO_SUCH_ID]}
            ).status_code
            == 422
        )
        assert (
            api.post(
                f"{BATCHES}/{bid}/rows/1/pick", json={"gate": "review", "take_ids": [cand[2]]}
            ).status_code
            == 422
        )
        assert (
            api.post(
                f"{BATCHES}/{bid}/rows/1/pick", json={"gate": "edit", "take_ids": [cand[1]]}
            ).status_code
            == 409
        )
        assert (
            api.post(
                f"{BATCHES}/{bid}/rows/99/pick", json={"gate": "review", "take_ids": [cand[1]]}
            ).status_code
            == 404
        )
        assert row(api, bid, 1)["status"] == "waiting"

        for n in (3, 2, 1):  # the last row first
            moved = pick(api, bid, n, [cand[n]])
            assert (moved["row"], moved["status"], moved["parked"]) == (n, "running", None)
            if n == 3:  # the same pick again: 202, nothing sent
                again = pick(api, bid, 3, [cand[3]])
                assert again["row"] == 3 and again["status"] != "waiting"
        done = wait_batch(api, bid)
        assert done["status"] == "done" and done["counts"]["succeeded"] == 3
        for r in rows_of(api, bid):
            assert r["status"] == "succeeded" and len(r["take_ids"]) == 1, r
            assert [c["node"] for c in r["cells"]] == ["still", "review", "edit", "out"]
            gate = cell(r, "review")["attempts"]
            assert [(a["status"], a["take_ids"]) for a in gate] == [("picked", [cand[r["row"]]])]
            (edit,) = cell(r, "edit")["attempts"]
            assert len(edit["run_ids"]) == 1  # the repeated pick made no second run
            t = ok(api.get(f"{STUDIO}/takes/{r['take_ids'][0]}"))
            assert t["parents"] == [cand[r["row"]]] and t["made_with"]["pipeline"]["node"] == "edit"
        assert len(batch_takes(api, bid)) == 6  # a still and an edit per row
        assert ok(api.get(f"{BATCHES}/{bid}/review"))["items"] == []
        # a finished row: the same pick again is still 202 and sends nothing; any other pick is 409
        again = pick(api, bid, 1, [cand[1]])
        assert (again["row"], again["status"]) == (1, "succeeded")
        r = api.post(f"{BATCHES}/{bid}/rows/1/pick", json={"gate": "review", "reject": True})
        assert r.status_code == 409, r.text
    finally:
        finish(api, bid)


def test_reject_loop_is_capped(api):
    p = create(api, gated("Contract loop"))
    batch = submit(api, p["id"], ["a restless fox"])
    bid = batch["id"]
    seen, state_checked = [], False
    try:
        for visit, (on_reject, left) in enumerate(
            [("retry", 2), ("retry", 1), ("then", 0)], start=1
        ):
            (r,) = parked(api, bid, [1], visit)
            (item,) = ok(api.get(f"{BATCHES}/{bid}/review"))["items"]
            assert (item["visit"], item["on_reject"], item["rejects_left"]) == (
                visit,
                on_reject,
                left,
            )
            seen.append(r["parked"]["candidates"])
            if visit == 2:  # the batch is alive: the state after a finished step can be read
                ex = execution(api, bid, 1)
                assert ex["current"] == "review" and ex["status"] == "waiting"
                assert ex["steps"][-1]["status"] == "current" and ex["steps"][-1]["part"] == "gate"
                readable = [s for s in ex["steps"] if s["has_state"]]
                assert readable, ex["steps"]
                s = readable[-1]
                st = ok(api.get(f"{BATCHES}/{bid}/rows/1/execution/{s['step']}/state"))
                assert (st["row"], st["attempt"], st["step"], st["node"]) == (
                    1,
                    1,
                    s["step"],
                    s["node"],
                )
                assert isinstance(st["state"], dict) and st["state"]
                for bad in ("999", "abc"):
                    assert (
                        api.get(f"{BATCHES}/{bid}/rows/1/execution/{bad}/state").status_code == 404
                    )
                state_checked = True
            reject(api, bid, 1)
        r = ended(api, bid, 1)
        assert (r["status"], r["take_ids"], r["error"]) == (
            "succeeded",
            [],
            None,
        )  # `then` = out, edit never ran
        assert len({t for c in seen for t in c}) == 3  # new takes every visit
        assert state_checked

        # the run table: attempts stacked per node
        assert [c["node"] for c in r["cells"]] == ["still", "review", "out"]
        still, gate, out = (cell(r, n)["attempts"] for n in ("still", "review", "out"))
        assert [a["visit"] for a in still] == [1, 2, 3] == [a["visit"] for a in gate]
        assert all(a["status"] == "succeeded" for a in still)
        assert len({a["run_ids"][0] for a in still}) == 3  # a new run every visit
        if all(a["seeds"] for a in still):  # new seeds every visit (base + visit × seeds)
            assert len({a["seeds"][0] for a in still}) == 3
        assert [(a["status"], a["candidates"]) for a in gate] == [("rejected", c) for c in seen]
        assert [(a["status"], a["take_ids"]) for a in out] == [("succeeded", [])]

        # the execution view: the steps in order with the loop counts
        ex = execution(api, bid, 1)
        assert (ex["status"], ex["current"], ex["loops"]) == ("succeeded", None, {"review": 2})
        loop = [("still", "submit"), ("still", "wait"), ("review", "gate"), ("review", "route")]
        assert [(s["node"], s["part"]) for s in ex["steps"]] == [
            ("inputs", "inputs"),
            *loop * 3,
            ("out", "output"),
        ]
        assert [s["step"] for s in ex["steps"]] == list(range(1, 15))
        assert [s["visit"] for s in ex["steps"] if s["part"] == "submit"] == [1, 2, 3]
        routes = [s for s in ex["steps"] if s["part"] == "route"]
        assert [s["edge"] for s in routes] == [
            {"to": "still", "via": "fail"},
            {"to": "still", "via": "fail"},
            {"to": "out", "via": "then"},
        ]
        assert [s["loops"].get("review", 0) for s in routes] == [1, 2, 2]
        assert all(s["status"] == "resumed" for s in ex["steps"] if s["part"] == "gate")
        assert all(s["status"] in ("finished", "resumed") for s in ex["steps"])
        taken = {(e["from"], e["to"], e["via"]): e["count"] for e in ex["edges"]}
        assert taken == {
            ("inputs", "still", "next"): 1,
            ("still", "review", "next"): 3,
            ("review", "still", "fail"): 2,
            ("review", "out", "then"): 1,
        }
        assert {k: ex["nodes"][k]["visits"] for k in ("still", "review", "out")} == {
            "still": 3,
            "review": 3,
            "out": 1,
        }
        assert "edit" not in ex["nodes"]
        assert ex["nodes"]["review"]["status"] == "rejected"
        wait_batch(api, bid)
        assert not any(s["has_state"] for s in execution(api, bid, 1)["steps"])
    finally:
        finish(api, bid)


def test_retry_row_from_a_step(api):
    p = create(api, gated("Contract retry"))
    batch = submit(api, p["id"], PROMPTS[:2])
    bid = batch["id"]
    try:
        rows = parked(api, bid, [1, 2])
        pick(api, bid, 1, rows[0]["parked"]["candidates"])
        before = ended(api, bid, 1)
        assert before["status"] == "succeeded"
        ex1 = execution(api, bid, 1)
        k = next(s["step"] for s in ex1["steps"] if (s["node"], s["part"]) == ("edit", "submit"))
        assert api.post(f"{BATCHES}/{bid}/rows/1/retry", json={"from_step": 99}).status_code == 422

        again = ok(api.post(f"{BATCHES}/{bid}/rows/1/retry", json={"from_step": k}), 202)
        assert (again["row"], again["attempt"], again["status"]) == (1, 2, "queued")
        after = ended(api, bid, 1, attempt=2)
        assert after["status"] == "succeeded" and after["take_ids"] != before["take_ids"]
        # the still and the pick were reused (same runs, same takes); only edit ran again
        ids = lambda r, node: [(a["run_ids"], a["take_ids"]) for a in cell(r, node)["attempts"]]
        assert ids(after, "still") == ids(before, "still") and ids(after, "review") == ids(
            before, "review"
        )
        assert (
            cell(after, "edit")["attempts"][-1]["run_ids"]
            != cell(before, "edit")["attempts"][-1]["run_ids"]
        )

        ex2 = execution(api, bid, 1)
        assert (ex2["attempt"], ex2["attempts"], ex2["retried_from"]) == (
            2,
            2,
            {"attempt": 1, "step": k},
        )
        same = ("step", "node", "part", "visit", "run_ids", "take_ids")
        assert [{f: s[f] for f in same} for s in ex2["steps"][: k - 1]] == [
            {f: s[f] for f in same} for s in ex1["steps"][: k - 1]
        ]
        assert ex2["steps"][k - 1]["run_ids"] != ex1["steps"][k - 1]["run_ids"]
        old = execution(api, bid, 1, attempt=1)
        assert (old["attempt"], old["attempts"]) == (1, 2)
        assert [s["run_ids"] for s in old["steps"]] == [s["run_ids"] for s in ex1["steps"]]
        assert (
            api.get(f"{BATCHES}/{bid}/rows/1/execution", params={"attempt": 3}).status_code == 404
        )

        pick(api, bid, 2, rows[1]["parked"]["candidates"])
        assert wait_batch(api, bid)["status"] == "done"
        # 2 stills and 3 edits (row 1 twice): the retry re-made only the edit
        assert len(batch_takes(api, bid)) == 5
        r = api.post(f"{BATCHES}/{bid}/rows/1/retry", json={"from_step": 2})
        assert (
            r.status_code == 409
            and r.json()["detail"] == "this batch has finished; its checkpoints are gone"
        )
    finally:
        finish(api, bid)


def test_pause_resume_stop_with_parked_rows(api):
    p = create(api, gated("Contract pause", edit_inputs={"echo.steps": 3, "echo.delay_ms": 300}))
    batch = submit(api, p["id"], PROMPTS)
    bid = batch["id"]
    try:
        rows = parked(api, bid, [1, 2, 3])
        wait_batch(api, bid, {"waiting"})
        assert ok(api.post(f"{BATCHES}/{bid}/pause"), 202)["control"] == "pause"
        wait_batch(api, bid, {"paused"})
        assert [r["status"] for r in rows_of(api, bid)] == [
            "waiting"
        ] * 3  # Pause leaves parked rows parked

        moved = pick(
            api, bid, 1, rows[0]["parked"]["candidates"]
        )  # picks are accepted while paused
        assert moved["status"] == "running"
        time.sleep(4)
        held = row(api, bid, 1)
        assert held["status"] not in TERMINAL, held  # its edit run is held
        for c in held["cells"]:  # once pipelines has asked for the edit run, it is created held
            if c["node"] == "edit":
                assert [a["status"] for a in c["attempts"]] == ["queued"], c
                for rid in c["attempts"][0]["run_ids"]:
                    assert ok(api.get(f"{STUDIO}/runs/{rid}"))["status"] == "queued"

        assert ok(api.post(f"{BATCHES}/{bid}/resume"), 202)["control"] == "run"
        assert ended(api, bid, 1)["status"] == "succeeded"
        wait_batch(api, bid, {"waiting"})
        assert [r["status"] for r in rows_of(api, bid)] == ["succeeded", "waiting", "waiting"]

        assert ok(api.post(f"{BATCHES}/{bid}/stop"), 202)["control"] == "stop"
        stopped = wait_batch(api, bid)
        assert stopped["status"] == "stopped"
        assert [r["status"] for r in rows_of(api, bid)] == ["succeeded", "cancelled", "cancelled"]
        r = api.post(
            f"{BATCHES}/{bid}/rows/2/pick",
            json={"gate": "review", "take_ids": rows[1]["parked"]["candidates"]},
        )
        assert r.status_code == 409, r.text
    finally:
        finish(api, bid)


def test_rejects_never_pause_and_a_reopened_batch_counts(new_user):
    """A gate with no fail branch: a reject fails the row (`rejected at review`), never toward the 3-in-a-row pause.
    Retrying a failed row of a done batch reopens it, so it is refused at 5 unfinished batches; after that plain retry
    the older attempt's state is gone (has_state false) while the batch runs."""
    with make_client(headers(new_user("Reopen")["access_token"])) as c:
        doc = gated("Contract no branch")
        doc["edges"][2] = ["review", "edit"]
        p = create(c, doc)
        bid = submit(c, p["id"], [*PROMPTS, "a grey owl"])["id"]
        others = []
        try:
            rows = parked(c, bid, [1, 2, 3, 4])
            for n in (1, 2, 3):
                reject(c, bid, n)
            for n in (1, 2, 3):
                assert ended(c, bid, n)["error"] == "rejected at review"
            assert wait_batch(c, bid, {"waiting"})["status"] == "waiting"  # not paused
            pick(c, bid, 4, rows[3]["parked"]["candidates"])
            assert wait_batch(c, bid)["status"] == "done"
            assert execution(c, bid, 1)["steps"]

            for i in range(5):
                others.append(submit(c, p["id"], [f"open {i}"])["id"])
            r = c.post(f"{BATCHES}/{bid}/rows/1/retry")
            assert r.status_code == 409, r.text
            assert r.json()["detail"] == "You have 5 unfinished batches; wait for one to finish."
            finish(c, others.pop())
            again = ok(c.post(f"{BATCHES}/{bid}/rows/1/retry"), 202)
            assert again["attempt"] == 2
            assert (
                ok(c.get(f"{BATCHES}/{bid}"))["status"] not in FINAL
            )  # counted unfinished at once
            assert (
                c.post(
                    BATCHES, json={"pipeline_id": p["id"], "rows": lines(["x"], "inputs.prompt")}
                ).status_code
                == 409
            )
            parked(c, bid, [1])
            old = execution(c, bid, 1, attempt=1)
            assert old["steps"] and not any(s["has_state"] for s in old["steps"])
            assert any(s["has_state"] for s in execution(c, bid, 1)["steps"])
        finally:
            for b in [bid, *others]:
                finish(c, b)


# ---------------------------------------------------------------- Save as pipeline


def run_take(client, sid: str, inputs: dict) -> dict:
    run = start_run(client, sid, {"echo.steps": 2, "echo.delay_ms": 50} | inputs)
    done = wait_run(client, run["id"])
    assert done["status"] == "succeeded", done
    (out,) = done["outputs"]
    return ok(client.get(f"{STUDIO}/takes/{out['take_id']}"))


def test_save_as_pipeline(api, anon):
    sid = new_session(api, "Contract save as pipeline")["id"]
    first = run_take(api, sid, {"prompt.text": "a fox at night"})
    edited = run_take(api, sid, {"prompt.text": "make it snow", "echo.refs": [ref(first["id"])]})
    p = ok(api.post(f"{STUDIO}/takes/{edited['id']}/pipeline"), 201)
    assert (p["version"], p["based_on"], p["problems"]) == (1, None, [])
    assert p["title"] == f"{edited['title']} pipeline"[:80]
    assert set(p["nodes"]) == {"inputs", "step_1", "step_2", "output"}
    assert [(i["name"], i["type"]) for i in p["nodes"]["inputs"]["inputs"]] == [("prompt", "Text")]
    s1, s2 = p["nodes"]["step_1"], p["nodes"]["step_2"]
    assert (s1["kind"], s1["workflow"], s1.get("seeds", 1)) == ("step", "echo", 1)
    assert s1["bind"] == {"prompt.text": "$prompt"} and s2["bind"] == {"echo.refs": "step_1.takes"}
    for s, take, bound in ((s1, first, "prompt.text"), (s2, edited, "echo.refs")):
        recorded = {
            k: v for k, v in take["made_with"]["inputs"].items() if k not in (bound, "echo.refs")
        }
        assert {k: v for k, v in s["inputs"].items() if k != "echo.refs"} == recorded
    assert s2["inputs"]["prompt.text"] == "make it snow"
    assert p["nodes"]["output"] == {
        "kind": "output",
        "from": "step_2.takes",
        "collection": p["title"],
    }
    assert p["edges"] == [["inputs", "step_1"], ["step_1", "step_2"], ["step_2", "output"]]
    assert ok(api.post(f"{PIPES}/validate", json=pdoc(p)))["can_run"] is True

    named = ok(
        api.post(f"{STUDIO}/takes/{edited['id']}/pipeline", json={"title": "Snowy fox"}), 201
    )
    assert named["title"] == "Snowy fox" and named["nodes"]["output"]["collection"] == "Snowy fox"

    # an upload in the lineage becomes an Image input
    up = upload(api, anon, png_bytes(32, 24), "snow ref.png")
    from_upload = run_take(
        api, sid, {"prompt.text": "with my picture", "echo.refs": [ref(up["id"])]}
    )
    q = ok(api.post(f"{STUDIO}/takes/{from_upload['id']}/pipeline"), 201)
    assert set(q["nodes"]) == {"inputs", "step_1", "output"}
    assert [(i["name"], i["type"]) for i in q["nodes"]["inputs"]["inputs"]] == [
        ("prompt", "Text"),
        ("image", "Image"),
    ]
    assert q["nodes"]["step_1"]["bind"] == {"prompt.text": "$prompt", "echo.refs": "$image"}

    r = api.post(f"{STUDIO}/takes/{up['id']}/pipeline")
    assert r.status_code == 422 and r.json()["detail"] == "an upload has no lineage to save", r.text
    assert api.post(f"{STUDIO}/takes/{NO_SUCH_ID}/pipeline").status_code == 404
    for x in (p, named, q):
        assert api.delete(f"{PIPES}/{x['id']}").status_code == 204


# ---------------------------------------------------------------- isolation


def test_isolation(api, api_b, chain_batch):
    a = create(api, chain("Contract A's pipeline"))
    pid, bid = a["id"], chain_batch["batch"]["id"]
    calls = [
        ("GET", f"{PIPES}/{pid}", None),
        ("PUT", f"{PIPES}/{pid}", chain() | {"version": 1}),
        ("PATCH", f"{PIPES}/{pid}", {"title": "mine now"}),
        ("DELETE", f"{PIPES}/{pid}", None),
        ("POST", PIPES, {"based_on": pid}),
        ("POST", PIPES, chain() | {"based_on": pid}),
        ("GET", f"{BATCHES}/{bid}/review", None),
        ("POST", f"{BATCHES}/{bid}/rows/1/pick", {"gate": "review", "reject": True}),
        ("GET", f"{BATCHES}/{bid}/rows/1/execution", None),
        ("GET", f"{BATCHES}/{bid}/rows/1/execution/1/state", None),
        ("POST", f"{BATCHES}/{bid}/rows/1/retry", None),
        ("POST", f"{BATCHES}/{bid}/rows/1/retry", {"from_step": 2}),
        ("POST", f"{STUDIO}/takes/{chain_batch['rows'][0]['take_ids'][0]}/pipeline", None),
    ]
    for method, path, body in calls:
        r = api_b.request(method, path, json=body)
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    assert pid not in {x["id"] for x in ok(api_b.get(PIPES))["pipelines"]}
    r = api_b.post(BATCHES, json={"pipeline_id": pid, "rows": lines(["x"], "inputs.prompt")})
    assert r.status_code == 422 and r.json()["detail"] == f"unknown pipeline {pid}", r.text
    assert ok(api.get(f"{PIPES}/{pid}"))["version"] == 1  # unchanged
    assert api.delete(f"{PIPES}/{pid}").status_code == 204


def test_workflow_batch_has_no_execution(api):
    """A batch of one workflow: no review items, the execution view is 404."""
    body = {"workflow_id": "echo", "rows": lines(["x"]), "inputs": FAST}
    batch = ok(api.post(BATCHES, json=body), 202)
    try:
        done = wait_batch(api, batch["id"])
        assert done["status"] in FINAL and done.get("pipeline_id") is None
        assert ok(api.get(f"{BATCHES}/{batch['id']}/review"))["items"] == []
        assert api.get(f"{BATCHES}/{batch['id']}/rows/1/execution").status_code == 404
        assert "cells" not in rows_of(api, batch["id"])[0]
    finally:
        finish(api, batch["id"])
