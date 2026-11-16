"""The row graph on LangGraph's in-memory saver (no database, no NATS), and the pipeline rules against
contracts/fixtures/pipelines."""

import copy
import json

import pytest
from conftest import CONTRACTS, OWNER, SESSION, FakeStudio, fixture
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.types import Command

import custom
import graph
import pipeline

BATCH = "5a2e9c4b-1d7f-4b3a-9c8e-6f0d2a1b3c47"
CFG = {"configurable": {"thread_id": f"{BATCH}:1"}}
ECHO = {"id": "echo", "title": "Echo", "family": "fake", "env": "fake", "nodes": {}}


def setup(studio, snapshot=None):
    plan = pipeline.plan(snapshot or {"workflow_id": "echo", "workflow": ECHO})
    g = graph.build(plan).compile(checkpointer=InMemorySaver())
    ctx = graph.Context(studio.create_run, OWNER, SESSION)
    return g, ctx


def result(d):
    return {d["run_id"]: {k: d[k] for k in ("status", "take_ids", "error")}}


async def test_parks_on_its_run_and_resumes_with_the_result():
    studio = FakeStudio()
    g, ctx = setup(studio)
    await g.ainvoke(graph.initial(BATCH, 1, 1, {"prompt.text": "a fox"}), CFG, context=ctx)
    snap = await g.aget_state(CFG)
    run_id = studio.run(1)["run_id"]
    assert snap.next == ("step.wait",) and snap.interrupts[0].value == {"runs": [run_id]}
    assert studio.asked == [f"{BATCH}:1:1:step:0:0"]
    assert studio.run(1)["req"]["inputs"] == {"prompt.text": "a fox"}  # the row's, as they are
    d = studio.finish(1)
    out = await g.ainvoke(Command(resume=result(d)), CFG, context=ctx)
    assert out["status"] == "succeeded" and out["take_ids"] == d["take_ids"]
    assert (await g.aget_state(CFG)).next == ()
    assert len(studio.asked) == 1  # resuming re-ran wait, not submit


async def test_a_terminal_reply_ends_the_row_without_parking():
    studio = FakeStudio()
    studio.create_status = "failed"
    g, ctx = setup(studio)
    out = await g.ainvoke(graph.initial(BATCH, 1, 1, {}), CFG, context=ctx)
    assert out["status"] == "failed" and out["error"] == "no live host for env fake"
    assert (await g.aget_state(CFG)).next == ()


async def test_a_final_error_reply_fails_the_row():
    studio = FakeStudio()
    studio.reply_error = {"error": "echo.steps: must be between 1 and 200", "code": "invalid"}
    g, ctx = setup(studio)
    out = await g.ainvoke(graph.initial(BATCH, 1, 1, {}), CFG, context=ctx)
    assert out["status"] == "failed" and out["run_id"] is None
    assert out["error"].startswith("echo.steps")


async def test_a_crash_inside_submit_asks_again_under_the_same_key():
    studio = FakeStudio()
    g, ctx = setup(studio)
    calls = 0

    async def crash_once(req):
        nonlocal calls
        calls += 1
        reply = await studio.create_run(req)
        if calls == 1:
            raise RuntimeError("process died before the reply was recorded")
        return reply

    ctx.create_run = crash_once
    with pytest.raises(RuntimeError):
        await g.ainvoke(graph.initial(BATCH, 1, 1, {}), CFG, context=ctx, durability="sync")
    assert (await g.aget_state(CFG)).next == ("step.submit",)
    await g.ainvoke(None, CFG, context=ctx, durability="sync")
    assert (await g.aget_state(CFG)).next == ("step.wait",)
    assert studio.asked == [f"{BATCH}:1:1:step:0:0"] * 2 and len(studio.runs) == 1


async def test_binds_seeds_and_a_gate_in_the_demo_pipeline():
    """still (4 seeds) → review (pick 1) → edit (the pick as refs, $edit as its prompt) → out."""
    studio = FakeStudio()
    sub = fixture("cmd.pipeline.submit.valid.2")["data"]
    g, ctx = setup(studio, sub["snapshot"])
    row = sub["rows"][0]
    await g.ainvoke(graph.initial(BATCH, 1, 1, row["inputs"], row["seeds"]), CFG, context=ctx)
    keys = [f"{BATCH}:1:1:still:0:{i}" for i in range(4)]
    assert studio.asked == keys
    req = studio.runs[keys[2]]["req"]
    assert req["workflow_id"] == "qwen-image"
    assert req["inputs"] == {"model.variant": "turbo", "canvas.width": 1024,
                             "prompt.text": "a red fox", "seed.seed": 1002}  # fmt: skip
    res = {}
    for k in keys:
        res |= result(studio.finish(1, key=k))
    await g.ainvoke(Command(resume=res), CFG, context=ctx)
    snap = await g.aget_state(CFG)
    parked = snap.interrupts[0].value
    assert snap.next == ("review.gate",) and parked["visit"] == 1 and len(parked["candidates"]) == 4
    pick = parked["candidates"][2]
    await g.ainvoke(Command(resume={"take_ids": [pick]}), CFG, context=ctx)
    edit = studio.runs[f"{BATCH}:1:1:edit:0:0"]["req"]["inputs"]
    assert edit["encode.refs"] == [{"image": {"take_id": pick}}]
    assert edit["prompt.text"] == "make it night" and edit["seed.seed"] == 5000
    d = studio.finish(1, key=f"{BATCH}:1:1:edit:0:0")
    out = await g.ainvoke(Command(resume=result(d)), CFG, context=ctx)
    assert out["status"] == "succeeded" and out["take_ids"] == d["take_ids"]
    assert out["collection"] == "Fox at night" and out["picked"] == {"review": [pick]}
    assert [c["node"] for c in out["cells"]] == ["still", "review", "edit", "out"]
    assert (
        out["n"] == 8
    )  # inputs, still.submit, still.wait, review.gate, review.route, edit ×2, out


def fx_problems(name, with_bounds=False):
    fx = json.loads((CONTRACTS / "fixtures/pipelines" / name).read_text())
    want = {(p["code"], p.get("node"), p.get("port"), p.get("param")) for p in fx["problems"]}
    out = fx["pipeline"], {p for p in want if p[0] in pipeline.CODES}
    return (*out, fx.get("bounds")) if with_bounds else out


@pytest.mark.parametrize(
    "name",
    sorted(
        p.name
        for p in (CONTRACTS / "fixtures/pipelines").glob("*.json")
        if p.name != "workflows.json"
    ),
)
def test_pipeline_rules_match_the_fixtures(name):
    doc, want, bounds = fx_problems(name, with_bounds=True)
    fx = json.loads((CONTRACTS / "fixtures/pipelines" / name).read_text())
    info = {}
    got = pipeline.problems(doc, info, fx.get("custom") or custom.registry(), fx.get("providers"))
    assert got == want
    if bounds:  # Submit's worst case per row: runs, LLM calls, tool calls, remote calls and tokens
        assert info["bounds"] == bounds
    if "egress" in fx:  # what leaves the machine, and what the servers reserve
        assert (info["egress"], info["reserved"]) == (fx["egress"], fx["reserved"])


def test_an_uncapped_loop_does_not_validate():
    doc, _ = fx_problems("valid.text-to-edit.json")
    doc = copy.deepcopy(doc)
    doc["edges"] = [e for e in doc["edges"] if e[0] != "review"] + [["review", "still"]]
    assert ("uncapped_cycle", "still", None, None) in pipeline.problems(doc)


def test_rerender_row_bind_sends_a_frame_ref():
    """API 1.17 Re-render: `<node>.<param>[<row>]` ← `<step>.last_frame` fills that row's Image with a frameRef and
    keeps its other fields."""
    wf = {"nodes": {"keyframes": {"params": {"images": []}}}}
    rows = [
        {"image": {"take_id": "old"}, "seconds": 0, "strength": 1},
        {"image": {"take_id": "x"}, "seconds": 1},
    ]
    plan = {"workflows": {"w": wf}, "inputs_node": "inputs"}
    spec = {
        "workflow": "w",
        "inputs": {"keyframes.images": rows},
        "bind": {"keyframes.images[1]": "clip_a.last_frame"},
    }
    got, err = graph.run_inputs(plan, {"inputs": {}, "takes": {"clip_a": ["t1", "t2"]}}, spec)
    assert err is None and got["keyframes.images"][0] == {
        "image": {"frame_of": "t1", "at": "last"},
        "seconds": 0,
        "strength": 1,
    }
    assert got["keyframes.images"][1] == rows[1] and rows[0]["image"] == {"take_id": "old"}
    assert graph.run_inputs(plan, {"inputs": {}, "takes": {}}, spec) == (
        None,
        "nothing from clip_a yet",
    )
    spec["bind"] = {"keyframes.images[3]": "clip_a.first_frame"}
    assert graph.run_inputs(plan, {"inputs": {}, "takes": {"clip_a": ["t1"]}}, spec)[1].endswith(
        "no row 3"
    )
