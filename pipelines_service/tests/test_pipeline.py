"""Saved pipelines on the engine (scratch Postgres, fake studio and JetStream): the demo still (4 seeds) → review
(pick 1, reject back to still at most twice, then out) → edit (the pick) → out."""

import copy
import uuid

from conftest import OWNER, FakeJS, FakeStudio, command, engine, fixture, idle

from engine import RESTARTED


def submit_cmd(b: str, n: int = 2, edges=None) -> dict:
    cmd = command("submit", b)
    d = copy.deepcopy(fixture("cmd.pipeline.submit.valid.2")["data"])
    rows = [
        {"row": i, "inputs": {"inputs.prompt": f"fox {i}", "inputs.edit": "make it night"},
         "seeds": {"still": 1000 * i, "edit": 5000 + i}}
        for i in range(1, n + 1)
    ]  # fmt: skip
    cmd["data"] |= {"snapshot": d["snapshot"], "inputs": d["inputs"], "rows": rows}
    if edges:
        cmd["data"]["snapshot"]["pipeline"]["edges"] = edges
    return cmd


async def finish_all(e, studio, row: int, status: str = "succeeded") -> None:
    for k in studio.open_keys(row):
        await e.on_run_finished(studio.finish(row, status, key=k))
    await idle(e)


async def pick(e, b, row=1, visit=1, attempt=1, take_ids=None) -> bool:
    d = {"batch_id": b, "row": row, "attempt": attempt, "gate": "review", "visit": visit}
    d |= {"take_ids": take_ids} if take_ids else {"reject": True}
    ok = await e.pick(d)
    await idle(e)
    return ok


def steps(js, row=1, attempt=1) -> dict[int, dict]:
    """The newest evt.pipeline.step per step number (as studio's projector keeps them)."""
    out = {}
    for m in sorted(js.events("step"), key=lambda m: m["data"]["seq"]):
        d = m["data"]
        if d["row"] == row and d["attempt"] == attempt:
            out[d["step"]] = d
    return out


async def test_two_steps_with_a_gate_park_pick_and_end(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(submit_cmd(b, 2))
    await idle(e)
    assert studio.asked == [f"{b}:1:1:still:0:{i}" for i in range(4)]  # row 1 first
    assert studio.runs[f"{b}:1:1:still:0:3"]["req"]["inputs"]["seed.seed"] == 1003
    await finish_all(e, studio, 1)
    r1 = js.rows()[1]
    assert r1["status"] == "waiting" and r1["node"] == "review"
    assert r1["parked"]["visit"] == 1 and len(r1["parked"]["candidates"]) == 4
    assert [c["node"] for c in r1["cells"]] == ["still", "review"]
    assert r1["cells"][1]["attempts"][0]["status"] == "waiting"
    assert f"{b}:2:1:still:0:0" in studio.asked  # row 1 parking released row 2
    assert steps(js)[4] | {"seq": 0} == steps(js)[4] | {
        "seq": 0, "node": "review", "part": "gate", "status": "current", "checkpoint_id": None,
    }  # fmt: skip

    chosen = r1["parked"]["candidates"][1]
    assert await pick(e, b, take_ids=[chosen])
    edit = studio.runs[f"{b}:1:1:edit:0:0"]["req"]["inputs"]
    assert edit["encode.refs"] == [{"image": {"take_id": chosen}}] and edit["seed.seed"] == 5001
    assert js.rows()[1]["status"] == "running" and js.rows()[1]["node"] == "edit"
    await finish_all(e, studio, 1)
    r1 = js.rows()[1]
    assert r1["status"] == "succeeded" and r1["node"] is None
    assert r1["take_ids"] == studio.runs[f"{b}:1:1:edit:0:0"]["take_ids"]
    s = steps(js)
    assert list(s) == list(range(1, 9))
    assert [(x["node"], x["part"]) for x in s.values()] == [
        ("inputs", "inputs"), ("still", "submit"), ("still", "wait"), ("review", "gate"),
        ("review", "route"), ("edit", "submit"), ("edit", "wait"), ("out", "output"),
    ]  # fmt: skip
    assert s[3]["status"] == "resumed" and s[3]["changed"] == ["takes.still = 4"]
    assert s[4]["status"] == "resumed" and s[5]["edge"] == {"to": "edit", "via": "pass"}
    assert all(x["checkpoint_id"] for x in s.values())
    # a duplicate pick, or one delivered later, changes nothing
    assert not await pick(e, b, take_ids=[chosen])
    assert sum(k.startswith(f"{b}:1:") for k in studio.runs) == 5


async def test_reject_loops_back_for_new_seeds_until_the_cap_then(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(submit_cmd(b, 1))
    await idle(e)
    for visit in (1, 2, 3):
        await finish_all(e, studio, 1)
        assert js.rows()[1]["parked"]["visit"] == visit
        assert await pick(e, b, visit=visit)  # reject all
        if visit < 3:  # back to still with the next seeds
            k = f"{b}:1:1:still:{visit}:0"
            assert studio.runs[k]["req"]["inputs"]["seed.seed"] == 1000 + visit * 4
    r1 = js.rows()[1]  # the cap of 2 is used: `then` (out), which has nothing from edit
    assert r1["status"] == "succeeded" and r1["take_ids"] == []
    assert not any(":edit:" in k for k in studio.runs)
    s = steps(js)
    routes = [x for x in s.values() if x["part"] == "route"]
    assert [x["edge"]["via"] for x in routes] == ["fail", "fail", "then"]
    assert routes[-1]["loops"] == {"review": 2}
    assert [a["status"] for a in r1["cells"][1]["attempts"]] == ["rejected"] * 3
    assert js.batch_status() == "done"


async def test_a_reject_at_a_plain_edge_fails_the_row(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    edges = [["inputs", "still"], ["still", "review"], ["review", "edit"], ["edit", "out"]]
    await e.submit(submit_cmd(b, 1, edges))
    await idle(e)
    await finish_all(e, studio, 1)
    await pick(e, b)
    assert js.rows()[1]["status"] == "failed" and js.rows()[1]["error"] == "rejected at review"


async def test_picks_while_paused_and_after_a_restart_with_rows_parked(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e1 = engine(pool, studio, js)
    await e1.submit(submit_cmd(b, 2))
    await idle(e1)
    await finish_all(e1, studio, 1)
    await finish_all(e1, studio, 2)
    assert js.rows()[1]["status"] == js.rows()[2]["status"] == "waiting"
    await e1.close()  # the crash, both rows parked

    e2 = engine(pool, studio, js)
    await e2.start()
    await idle(e2)
    batch = max(js.events("batch"), key=lambda m: m["data"]["seq"])["data"]
    assert batch["status"] == "paused" and batch["reason"] == RESTARTED
    assert js.rows()[1]["status"] == "waiting"  # still parked, republished
    c1 = js.rows()[1]["parked"]["candidates"][0]
    assert await pick(e2, b, take_ids=[c1])  # accepted while paused: the next run is asked (held)
    assert f"{b}:1:1:edit:0:0" in studio.runs
    assert not await pick(e2, b, take_ids=[c1])  # delivered twice
    assert not await pick(e2, b, row=2, visit=2)  # another visit: dropped
    assert js.rows()[2]["status"] == "waiting"
    await e2.resume(b)
    await idle(e2)
    await finish_all(e2, studio, 1)
    await pick(e2, b, row=2, take_ids=[js.rows()[2]["parked"]["candidates"][0]])
    await finish_all(e2, studio, 2)
    assert [js.rows()[r]["status"] for r in (1, 2)] == ["succeeded"] * 2
    assert sum(":edit:" in k for k in studio.runs) == 2 and js.batch_status() == "done"


async def test_stop_cancels_a_parked_row(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(submit_cmd(b, 1))
    await idle(e)
    await finish_all(e, studio, 1)
    await e.stop(b)
    assert js.rows()[1]["status"] == "cancelled" and js.batch_status() == "stopped"
    assert not await pick(e, b)


async def test_retry_from_a_step_reuses_the_steps_before_it_and_the_state_can_be_read(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(submit_cmd(b, 2))  # row 2 keeps the batch (and its threads) alive
    await idle(e)
    await finish_all(e, studio, 1)
    await pick(e, b, take_ids=[js.rows()[1]["parked"]["candidates"][0]])
    await finish_all(e, studio, 1, "failed")
    assert js.rows()[1]["status"] == "failed"
    s = steps(js)
    assert (s[6]["node"], s[6]["part"]) == ("edit", "submit")

    req = {"owner_id": OWNER, "batch_id": b, "row": 1, "checkpoint_id": s[5]["checkpoint_id"]}
    got = await e.state_get(req)
    assert got["state"]["picked"]["review"] and got["state"]["loops"] == {}
    assert (await e.state_get(req | {"owner_id": str(uuid.uuid4())}))["code"] == "not_found"
    assert (await e.state_get(req | {"checkpoint_id": "nope"}))["code"] == "not_found"

    still_runs = sum(":still:" in k for k in studio.asked)
    await e.retry(b, 1, 2, from_step=6, checkpoint_id=s[5]["checkpoint_id"])
    await idle(e)
    assert sum(":still:" in k for k in studio.asked) == still_runs  # nothing before step 6 again
    assert studio.asked[-1] == f"{b}:1:2:edit:0:0"
    await finish_all(e, studio, 1)
    r1 = js.rows()[1]
    assert r1["status"] == "succeeded" and r1["attempt"] == 2
    s2 = steps(js, attempt=2)
    assert min(s2) == 6 and (s2[6]["node"], s2[6]["part"]) == ("edit", "submit")

    # from the gate (the checkpoint after still's wait): it parks again, the same candidates
    await e.retry(b, 1, 3, from_step=4, checkpoint_id=s[3]["checkpoint_id"])
    await idle(e)
    r1 = js.rows()[1]
    assert r1["status"] == "waiting" and r1["attempt"] == 3
    assert r1["parked"]["candidates"] == steps(js)[3]["take_ids"]


async def test_a_pipeline_the_engine_cannot_run_fails_the_batch(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    edges = [["inputs", "still"], ["still", "review"], ["review", "still"], ["edit", "out"]]
    await e.submit(submit_cmd(b, 1, edges))
    await idle(e)
    batch = max(js.events("batch"), key=lambda m: m["data"]["seq"])["data"]
    assert batch["status"] == "failed" and batch["reason"].startswith("pipeline cannot run: ")
    assert studio.asked == []


async def test_the_state_request_answers_on_contract(pool):
    import json

    import states

    e = engine(pool, FakeStudio(), FakeJS())
    assert (await states.answer(e, b"{}"))["code"] == "not_found"
    req = {"owner_id": OWNER, "batch_id": str(uuid.uuid4()), "row": 1, "checkpoint_id": "x"}
    assert (await states.answer(e, json.dumps(req).encode()))["code"] == "not_found"
