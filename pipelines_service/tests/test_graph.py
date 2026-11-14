"""The row graph on LangGraph's in-memory saver: no database, no NATS."""

import pytest
from conftest import OWNER, SESSION, FakeStudio
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.types import Command

import graph

BATCH = "5a2e9c4b-1d7f-4b3a-9c8e-6f0d2a1b3c47"
CFG = {"configurable": {"thread_id": f"{BATCH}:1"}}


def setup(studio):
    g = graph.build().compile(checkpointer=InMemorySaver())
    ctx = graph.Context(studio.create_run, OWNER, SESSION, "echo")
    return g, ctx, graph.initial(BATCH, 1, 1, {"prompt.text": "a fox"})


async def test_parks_on_its_run_and_resumes_with_the_result():
    studio = FakeStudio()
    g, ctx, init = setup(studio)
    await g.ainvoke(init, CFG, context=ctx, durability="sync")
    snap = await g.aget_state(CFG)
    run_id = studio.run(1)["run_id"]
    assert snap.next == ("wait",) and snap.values["run_id"] == run_id
    assert studio.asked == [f"{BATCH}:1:1:step:0:0"]
    d = studio.finish(1)
    out = await g.ainvoke(
        Command(resume={k: d[k] for k in ("status", "take_ids", "error")}),
        CFG,
        context=ctx,
        durability="sync",
    )
    assert out["status"] == "succeeded" and out["take_ids"] == d["take_ids"]
    assert (await g.aget_state(CFG)).next == ()
    assert len(studio.asked) == 1  # resuming re-ran wait, not submit


async def test_a_terminal_reply_ends_the_row_without_parking():
    studio = FakeStudio()
    studio.create_status = "failed"
    g, ctx, init = setup(studio)
    out = await g.ainvoke(init, CFG, context=ctx, durability="sync")
    assert out["status"] == "failed" and out["error"] == "no live host for env fake"
    assert (await g.aget_state(CFG)).next == ()


async def test_a_final_error_reply_fails_the_row():
    studio = FakeStudio()
    studio.reply_error = {"error": "echo.steps: must be between 1 and 200", "code": "invalid"}
    g, ctx, init = setup(studio)
    out = await g.ainvoke(init, CFG, context=ctx, durability="sync")
    assert out["status"] == "failed" and out["run_id"] is None
    assert out["error"].startswith("echo.steps")


async def test_a_crash_inside_submit_asks_again_under_the_same_key():
    studio = FakeStudio()
    g, ctx, init = setup(studio)
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
        await g.ainvoke(init, CFG, context=ctx, durability="sync")
    assert (await g.aget_state(CFG)).next == ("submit",)
    await g.ainvoke(None, CFG, context=ctx, durability="sync")
    assert (await g.aget_state(CFG)).next == ("wait",)
    assert studio.asked == [f"{BATCH}:1:1:step:0:0"] * 2 and len(studio.runs) == 1
