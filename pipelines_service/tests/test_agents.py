"""Agent nodes on the engine (scratch Postgres, fake studio and JetStream): the M4b demo shape on fake workflows,
inputs → mem (read) → still (4 seeds) → judge (keep 1; fail → rewrite → still, max 2, then review) → clip → out;
plus the judge's derived verdict and the replay hash against contracts/fixtures/decisions."""

import copy
import json
import uuid

import pytest
from conftest import CONTRACTS, OWNER, FakeJS, FakeStudio, command, engine, idle

import agents
import memory
from messaging import schemas

PIPELINE = "4e5f6a7b-8c9d-4e0f-9a1b-2c3d4e5f6a00"
MUST, NICE = ["No extra or missing limbs", "Matches the brief"], ["Subject is sharp"]
REG = json.loads((CONTRACTS / "fixtures/registry/workflows.valid.json").read_text())["workflows"]
WF = {w["id"]: {k: v for k, v in w.items() if k != "media"} for w in REG}


def doc(mode="suggest", edges=None, **nodes) -> dict:
    n = {
        "inputs": {"kind": "inputs", "inputs": [{"name": "prompt", "type": "Text"},
                                               {"name": "motion", "type": "Text"}]},
        "mem": {"kind": "memory", "op": "read", "limit": 8},
        "still": {"kind": "step", "workflow": "echo", "seeds": 4, "inputs": {},
                  "bind": {"prompt.text": "$prompt"}, "seed_param": None},
        "judge": {"kind": "judge", "takes": "still", "criteria": {"must": MUST, "nice": NICE},
                  "keep": 1, "mode": mode, "model": "auto", "workflow": "echo-ask", "memory": "mem"},
        "rewrite": {"kind": "write", "from": ["$prompt", "judge.reasons"], "to": "$prompt",
                    "instruction": "Fix what the reviewer found; keep everything else.",
                    "model": "auto", "workflow": "echo-ask"},
        "review": {"kind": "gate", "takes": "still", "keep": 1},
        "clip": {"kind": "step", "workflow": "echo-image-to-video", "seeds": 1, "inputs": {},
                 "bind": {"prompt.text": "$motion", "keyframes.images": "judge.picked"},
                 "seed_param": None},
        "out": {"kind": "output", "from": "clip.takes", "collection": "Best still to clip"},
    } | nodes  # fmt: skip
    edges = edges or [
        ["inputs", "mem"], ["mem", "still"], ["still", "judge"],
        ["judge", {"pass": "clip", "fail": "rewrite", "max": 2, "then": "review"}],
        ["rewrite", "still"], ["review", "clip"], ["clip", "out"],
    ]  # fmt: skip
    used = {v["workflow"] for v in n.values() if "workflow" in v}
    return {"id": PIPELINE, "title": "Best still to clip", "version": 1, "nodes": n,
            "edges": edges, "workflows": {w: WF[w] for w in used}}  # fmt: skip


def submit(b: str, p: dict, n: int = 1, replay_from=None) -> dict:
    cmd = command("submit", b, n)
    rows = [{"row": i, "inputs": {"inputs.prompt": f"a fox {i} with @rex", "inputs.motion": "pan"}}
            for i in range(1, n + 1)]  # fmt: skip
    cmd["data"] |= {"snapshot": {"workflow_id": "echo", "workflow": WF["echo"], "pipeline": p}}
    cmd["data"] |= {"inputs": {}, "rows": rows}
    if replay_from:
        cmd["data"]["replay_from"] = replay_from
    assert not schemas.message_errors(cmd), schemas.message_errors(cmd)
    return cmd


def judged(checks: list[list[bool]], ranking=None) -> str:
    takes = [{"take": i, "checks": c, "reason": f"reason {i}"} for i, c in enumerate(checks, 1)]
    return json.dumps({"takes": takes, "ranking": ranking or list(range(1, len(checks) + 1))})


PASS3 = judged([[False, True, True], [True, True, False], [True, True, True], [True, False, True]],
               [1, 3, 2, 4])  # fmt: skip
FAIL = judged([[False, True, True]] * 4)


class Script:
    """The fake ask runs' answers per node, in order (the last one repeats)."""

    def __init__(self, **by_node):
        self.q = {k: list(v) for k, v in by_node.items()}
        self.prompts: list[str] = []

    def __call__(self, req) -> str:
        self.prompts.append(req["inputs"]["ask.prompt"])
        q = self.q[req["idempotency_key"].split(":")[3]]
        return q.pop(0) if len(q) > 1 else q[0]


def node_of(k: str) -> str:
    return k.split(":")[3]


async def finish(e, studio, node: str, status="succeeded", twice=False) -> list[str]:
    """Finish every open run of `node` (each delivered twice when asked: the second is dropped)."""
    keys = [k for k in studio.open_keys(1) + studio.open_keys(2) if node_of(k) == node]
    for k in keys:
        d = studio.finish(int(k.split(":")[1]), status, key=k)
        assert await e.on_run_finished(d)
        if twice:
            assert not await e.on_run_finished(d)
    await idle(e)
    return keys


def steps(js, row=1, attempt=1) -> list[dict]:
    out = {}
    for m in sorted(js.events("step"), key=lambda m: m["data"]["seq"]):
        d = m["data"]
        if d["row"] == row and d["attempt"] == attempt:
            out[d["step"]] = d
    return [out[k] for k in sorted(out)]


def cell(js, node, row=1) -> dict:
    return next(c for c in js.rows()[row]["cells"] if c["node"] == node)


async def run_to_judge(e, studio, js, b, p, n=1, **kw):
    await e.submit(submit(b, p, n, **kw))
    await idle(e)
    await finish(e, studio, "still")


# --- unit: the contract's reference cases


def test_judge_verdict_and_replay_hash_match_the_fixtures():
    g = CONTRACTS / "fixtures/decisions"
    for c in json.loads((g / "verdict.json").read_text())["cases"]:
        got = agents.judge_verdict(c["answer"], c["criteria"], c["keep"], c["candidates"])
        assert got == c["expect"], c["description"]
    for c in json.loads((g / "replay.json").read_text())["cases"]:
        assert agents.inputs_hash(c["call"]) == c["expect"]


def test_answers_are_parsed_from_the_outermost_object():
    value, err = agents.parse('```json\n{"text": "a fox"}\n```', agents.WRITE_SCHEMA)
    assert value == {"text": "a fox"} and err is None
    assert agents.parse("no json", agents.WRITE_SCHEMA)[1].startswith("answer did not match")
    _, err = agents.parse('{"route": "maybe", "reason": ""}', agents.decide_schema(["a", "b"]))
    assert err.startswith("answer did not match the schema: route:")
    assert agents.mentions("@rex meets @@not and mail@x.com @Tom-") == ["rex", "tom"]


# --- the engine


async def test_auto_judge_keeps_the_best_of_4_and_records_the_decision(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[PASS3])
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc("auto"))
    takes = studio.runs[f"{b}:1:1:still:0:3"]["take_ids"]
    req = studio.runs[f"{b}:1:1:judge:0:0"]["req"]
    assert req["workflow_id"] == "echo-ask" and len(req["inputs"]["ask.media"]) == 4
    assert json.loads(req["inputs"]["ask.schema"])["required"] == ["takes", "ranking"]
    assert "The brief:\n<<<\na fox 1 with @rex\n>>>" in req["inputs"]["ask.prompt"]
    assert js.rows()[1]["status"] == "running" and js.rows()[1]["node"] == "judge"
    await finish(
        e, studio, "judge", twice=True
    )  # a result delivered twice is dropped the second time
    still = [studio.runs[f"{b}:1:1:still:0:{i}"]["take_ids"][0] for i in range(4)]
    clip = studio.runs[f"{b}:1:1:clip:0:0"]["req"]["inputs"]
    assert clip["keyframes.images"] == [{"image": {"take_id": still[2]}}] and takes == [still[3]]
    s = {(x["node"], x["part"]): x for x in steps(js)}
    dec = s[("judge", "verdict")]["decision"]
    assert dec["judge"]["verdict"] == "pass" and dec["judge"]["picked"] == [still[2]]
    assert [t["rank"] for t in dec["judge"]["takes"]] == [3, 2, 1, 4] and not dec["replayed"]
    assert dec["id"] == f"{b}:1:1:judge:1" and dec["replay_key"].startswith(f"{b}:1:judge:1:")
    assert s[("judge", "route")]["edge"] == {"to": "clip", "via": "pass"}
    c = cell(js, "judge")["attempts"][0]
    assert c["status"] == "picked" and c["judged"]["picked"] == 1 and c["judged"]["of"] == 4
    assert c["judged"]["first_failed"] is None  # a pass names no failed criterion
    assert c["judged"]["takes"][0]["first_failed"] == MUST[0] and c["decision_id"] == dec["id"]
    await finish(e, studio, "clip")
    assert js.rows()[1]["status"] == "succeeded" and js.batch_status() == "done"
    stored = await e._replay(dec["replay_key"])
    assert stored["id"] == dec["id"]


async def test_suggest_parks_with_the_pick_preselected_and_records_agreement(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[PASS3])
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc("suggest"), n=2)
    await finish(e, studio, "judge")  # row 1 parks: row 2 is released
    await finish(e, studio, "still")
    await finish(e, studio, "judge")
    rows = js.rows()
    assert rows[1]["status"] == rows[2]["status"] == "waiting"
    p1, p2 = rows[1]["parked"], rows[2]["parked"]
    assert p1["kind"] == "judge" and p1["suggested"] == [p1["candidates"][2]]
    assert p1["on_reject"] == "retry" and p1["decision_id"] == f"{b}:1:1:judge:1"
    assert cell(js, "judge")["attempts"][0]["status"] == "waiting"
    base = {"batch_id": b, "attempt": 1, "gate": "judge", "visit": 1}
    assert await e.pick(base | {"row": 1, "take_ids": p1["suggested"], "agreed": True})
    note = "The softer one keeps the face."
    other = [p2["candidates"][1]]
    assert await e.pick(base | {"row": 2, "take_ids": other, "agreed": False, "note": note})
    await idle(e)
    assert not await e.pick(base | {"row": 2, "take_ids": other, "agreed": False})  # twice
    await idle(e)
    req2 = next(r["req"] for k, r in studio.runs.items() if k.startswith(f"{b}:2:1:clip"))
    assert req2["inputs"]["keyframes.images"] == [{"image": {"take_id": other[0]}}]
    j1 = cell(js, "judge", 1)["attempts"][0]["judged"]
    j2 = cell(js, "judge", 2)["attempts"][0]["judged"]
    assert (j1["agreed"], j2["agreed"]) == (True, False) and j2["suggested"] == p2["suggested"]
    items = await memory.items(e.store, OWNER, PIPELINE)
    assert [(i["kind"], i["key"], i["text"], i["agreed"]) for i in items] == [
        ("judge_example", "judge", note, False)
    ]
    assert items[0]["kept"] == other and len(items[0]["rejected"]) == 3
    assert items[0]["source"] == {
        "kind": "pick",
        "batch_id": b,
        "row": 2,
        "node": "judge",
        "visit": 1,
    }


async def test_a_failing_judge_rewrites_and_retries_until_the_cap_then_review(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    rewrite = ['{"text": "a fox with @rex, four legs", "note": "fixed the legs"}']
    studio.answer_for = Script(judge=["not json", FAIL], rewrite=rewrite)
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc("auto"))
    await finish(e, studio, "judge")  # an invalid answer: asked once more under branch 1
    assert f"{b}:1:1:judge:0:1" in studio.runs
    reask = studio.runs[f"{b}:1:1:judge:0:1"]["req"]["inputs"]["ask.prompt"]
    assert "Your answer was invalid: answer did not match" in reask
    await finish(e, studio, "judge")
    await finish(e, studio, "rewrite")
    still = studio.runs[f"{b}:1:1:still:1:0"]["req"]["inputs"]
    assert still["prompt.text"] == "a fox with @rex, four legs"  # the write set $prompt
    for visit in (1, 2):
        await finish(e, studio, "still")
        await finish(e, studio, "judge")
        if visit == 1:
            await finish(e, studio, "rewrite")
    r = js.rows()[1]
    assert r["status"] == "waiting" and r["node"] == "review" and "kind" not in r["parked"]
    c = cell(js, "judge")
    assert [a["status"] for a in c["attempts"]] == ["rejected"] * 3
    assert c["loop"] == {"turns": 2, "max": 2, "then": "review", "gave_up": True}
    s = steps(js)
    w = next(x for x in s if x["part"] == "apply")["decision"]["write"]
    assert w == {"to": "$prompt", "before": "a fox 1 with @rex",
                 "after": "a fox with @rex, four legs", "note": "fixed the legs"}  # fmt: skip
    routes = [x for x in s if (x["node"], x["part"]) == ("judge", "route")]
    assert [x["edge"]["via"] for x in routes] == ["fail", "fail", "then"]
    assert routes[-1]["loops"]["judge"] == 2
    first = next(x for x in s if "decision" in x and x["decision"]["visit"] == 1)
    assert [a["error"] is None for a in first["decision"]["answers"]] == [False, True]
    assert len(first["decision"]["run_ids"]) == 2


async def test_a_write_naming_an_unknown_mention_fails_the_row_after_one_retry(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[FAIL], rewrite=['{"text": "a fox with @bob"}'])
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc("auto"))
    await finish(e, studio, "judge")
    await finish(e, studio, "rewrite")
    await finish(e, studio, "rewrite")
    r = js.rows()[1]
    assert r["status"] == "failed" and r["error"].startswith("rewrite: the text mentions @bob")


async def test_decide_routes_along_its_edges_and_an_undeclared_route_fails(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    retry, bad = '{"route": "retry", "reason": "miss"}', '{"route": "maybe", "reason": "?"}'
    studio.answer_for = lambda req: (
        PASS3 if node_of(req["idempotency_key"]) == "judge"
        else retry if req["row"] == 1 else bad
    )  # fmt: skip
    check = {"kind": "decide", "question": "Animate it, or make the stills again?",
             "from": ["$prompt", "judge.reasons"], "routes": {"retry": "miss", "accept": "ok"},
             "model": "auto", "workflow": "echo-ask"}  # fmt: skip
    edges = [["inputs", "mem"], ["mem", "still"], ["still", "judge"], ["judge", "check"],
             ["check", {"routes": {"retry": "still", "accept": "clip"}, "max": 1, "then": "accept"}],
             ["clip", "out"]]  # fmt: skip
    p = doc("auto", edges, check=check)
    del p["nodes"]["rewrite"], p["nodes"]["review"]
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, p, n=2)
    await finish(e, studio, "judge")
    await finish(e, studio, "check")  # retry: back to still
    await finish(e, studio, "still")
    await finish(e, studio, "judge")
    await finish(e, studio, "check")  # retry again: the cap forces accept
    await finish(e, studio, "clip")  # row 1 ends: row 2 goes
    await finish(e, studio, "still")
    await finish(e, studio, "judge")
    await finish(e, studio, "check")
    await finish(e, studio, "check")  # an undeclared route twice
    r1 = [x for x in steps(js) if (x["node"], x["part"]) == ("check", "route")]
    assert [x["decision"]["decide"]["route"] for x in r1] == ["retry", "accept"]
    assert r1[-1]["decision"]["decide"]["forced"] and r1[-1]["edge"] == {
        "to": "clip",
        "via": "then",
    }
    assert cell(js, "check", 1)["loop"] == {"turns": 1, "max": 1, "then": "clip", "gave_up": True}
    assert cell(js, "check", 1)["attempts"][0]["route"] == {"label": "retry", "to": "still"}
    assert f"{b}:1:1:clip:0:0" in studio.runs
    r2 = js.rows()[2]
    assert r2["status"] == "failed" and r2["error"].startswith("check: answer did not match")
    assert f"{b}:2:1:check:0:1" in studio.runs and not any(":2:1:clip" in k for k in studio.runs)


async def test_memory_reads_the_batch_snapshot_and_writes_for_the_next_batch(pool):
    studio, js = FakeStudio(), FakeJS()
    studio.answer_for = Script(judge=[PASS3])
    note = {"kind": "memory", "op": "write", "key": "briefs", "from": ["judge.reasons"]}
    edges = [["inputs", "mem"], ["mem", "still"], ["still", "judge"],
             ["judge", {"pass": "note", "fail": "rewrite", "max": 2, "then": "review"}],
             ["rewrite", "still"], ["review", "clip"], ["note", "clip"], ["clip", "out"]]  # fmt: skip
    p = doc("auto", edges, note=note)
    e = engine(pool, studio, js)
    seed = {"owner_id": OWNER, "op": "create", "pipeline_id": PIPELINE, "text": "foxes face left"}
    assert (await memory.answer(e.store, json.dumps(seed).encode()))["item"]["kind"] == "note"
    b1 = str(uuid.uuid4())
    await run_to_judge(e, studio, js, b1, p, n=2)
    for _ in range(2):
        await finish(e, studio, "judge")
        await finish(e, studio, "clip")
        await finish(e, studio, "still")
    batch = max(js.events("batch"), key=lambda m: m["data"]["seq"])["data"]
    assert batch["memory"]["notes"] == 1 and batch["memory"]["examples"] == 0
    reads = [x for x in steps(js, 2) if x["part"] == "memory" and x["node"] == "mem"]
    assert len(reads[0]["memory"]["read"]) == 1  # row 2 read the snapshot, not row 1's note
    verdict = next(
        x for x in steps(js) if x["part"] == "verdict"
    )  # the judge's reads are on its step too
    assert verdict["memory"] == {"read": verdict["decision"]["memory_read"], "wrote": []}
    assert len(verdict["memory"]["read"]) == 1
    assert "foxes face left" in studio.answer_for.prompts[0]
    wrote = next(x for x in steps(js) if x["node"] == "note")["memory"]["wrote"]
    items = await memory.items(e.store, OWNER, PIPELINE)
    assert len(items) == 3 and wrote[0] in {i["id"] for i in items}
    assert {i["source"]["kind"] for i in items} == {"user", "node"}

    b2, js2 = str(uuid.uuid4()), FakeJS()
    e2 = engine(pool, studio, js2)
    await run_to_judge(e2, studio, js2, b2, p)
    reads = [x for x in steps(js2) if x["part"] == "memory" and x["node"] == "mem"]
    assert len(reads[0]["memory"]["read"]) == 3  # batch 1's notes are seen from the next batch
    assert "reason 3" in studio.runs[f"{b2}:1:1:judge:0:0"]["req"]["inputs"]["ask.prompt"]
    # the Memory panel's requests
    lst = await memory.answer(e.store, json.dumps(seed | {"op": "list", "kind": "note"}).encode())
    assert (
        len(lst["items"]) == 3 and lst["items"][0]["created_at"] >= lst["items"][-1]["created_at"]
    )
    gone = {"owner_id": OWNER, "op": "delete", "pipeline_id": PIPELINE, "item_id": wrote[0]}
    assert await memory.answer(e.store, json.dumps(gone).encode()) == {"deleted": 1}
    assert (await memory.answer(e.store, json.dumps(gone).encode()))["code"] == "not_found"
    other = gone | {"owner_id": str(uuid.uuid4()), "item_id": items[0]["id"]}
    assert (await memory.answer(e.store, json.dumps(other).encode()))["code"] == "not_found"
    clear = {"owner_id": OWNER, "op": "clear", "pipeline_id": PIPELINE}
    assert await memory.answer(e.store, json.dumps(clear).encode()) == {"deleted": 2}
    # the pipeline was deleted: a write still under way (a running batch, a pick's note) lands nowhere
    assert await memory.put(e.store, OWNER, PIPELINE, str(uuid.uuid4()), items[0]) == memory.GONE
    assert await memory.items(e.store, OWNER, PIPELINE) == []


async def test_decisions_are_replayed_on_resume_retry_and_run_again(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[PASS3])
    p = doc("auto")
    e1 = engine(pool, studio, js)
    await run_to_judge(e1, studio, js, b, p, n=2)
    await e1.close()  # the crash, row 1's judge run asked and not finished
    e2 = engine(pool, studio, js)
    await e2.start()
    await idle(e2)
    await finish(e2, studio, "judge")  # its result, delivered after the restart (paused)
    await e2.resume(b)
    await idle(e2)
    assert sum(node_of(k) == "judge" for k in studio.asked if k.startswith(f"{b}:1:")) == 1
    await finish(e2, studio, "clip")
    s = steps(js)
    dec = next(x for x in s if x["part"] == "verdict")["decision"]

    assert len(dec["seen"]) == 4 and not dec["replayed"]

    # Retry from the judge's submit: the stills are reused, the model is asked again
    sub = next(x for x in s if (x["node"], x["part"]) == ("judge", "submit"))
    before = next(x for x in s if x["step"] == sub["step"] - 1)
    await e2.retry(b, 1, 2, from_step=sub["step"], checkpoint_id=before["checkpoint_id"])
    await idle(e2)
    assert f"{b}:1:2:judge:0:0" in studio.runs
    await finish(e2, studio, "judge")
    d2 = next(x for x in steps(js, attempt=2) if x["part"] == "verdict")["decision"]
    assert not d2["replayed"] and len(d2["run_ids"]) == 1
    await finish(e2, studio, "clip")

    # memory changes after the source batch (a note the judge reads): Run again still replays, on its snapshot
    note = {"owner_id": OWNER, "op": "create", "pipeline_id": PIPELINE, "text": "new note"}
    assert "item" in await memory.answer(e2.store, json.dumps(note).encode())
    # Run again with the same decisions: same seeds, new takes, the source batch's decisions
    b2, js2 = str(uuid.uuid4()), FakeJS()
    e3 = engine(pool, studio, js2)
    await run_to_judge(e3, studio, js2, b2, copy.deepcopy(p), replay_from=b)
    assert not any(k.startswith(f"{b2}:1:1:judge") for k in studio.asked)
    d3 = next(x for x in steps(js2) if x["part"] == "verdict")["decision"]
    assert d3["replayed_from"] == dec["id"] and d3["replay_key"].startswith(f"{b2}:1:judge:1:")
    picked = studio.runs[f"{b2}:1:1:still:0:2"]["take_ids"]
    assert d3["judge"]["picked"] == picked  # this batch's take at the same position
    assert (
        d3["seen"] == []
    )  # the model saw nothing in this batch (the source decision holds what it saw)
    # a Retry inside the Run-again batch asks the model
    s3 = steps(js2)
    sub = next(x for x in s3 if (x["node"], x["part"]) == ("judge", "submit"))
    before = next(x for x in s3 if x["step"] == sub["step"] - 1)
    await e3.retry(b2, 1, 2, from_step=sub["step"], checkpoint_id=before["checkpoint_id"])
    await idle(e3)
    assert f"{b2}:1:2:judge:0:0" in studio.runs
    # and decide again (no replay_from): the model is asked
    b3, js3 = str(uuid.uuid4()), FakeJS()
    e4 = engine(pool, studio, js3)
    await run_to_judge(e4, studio, js3, b3, copy.deepcopy(p))
    assert f"{b3}:1:1:judge:0:0" in studio.runs


@pytest.mark.parametrize("mode", ["suggest", "auto"])
async def test_a_failed_judge_run_fails_the_row_with_its_decision(pool, mode):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc(mode))
    await finish(e, studio, "judge", "failed")
    r = js.rows()[1]
    assert r["status"] == "failed" and r["error"] == "judge: boom"
    dec = next(x for x in steps(js) if x["part"] == "verdict")["decision"]
    assert dec["error"] == "boom" and "judge" not in dec
    assert await e._replay(dec["replay_key"]) is None  # a failed call is never replayed


def test_prompts_keep_their_instructions_and_room_for_the_re_ask():
    crit = {"must": MUST, "nice": NICE}
    examples = [{"kept": ["x"], "text": "é" * 2000}] * 16
    p = agents.judge_prompt("a fox", 4, crit, examples, "n" * 40000)
    assert len(p) + len(agents.REASK.format(error="e" * 2000)) + 2 <= agents.PROMPT_MAX
    assert (
        p.startswith(agents.DATA) and p.endswith("passes all of them.") and "[must] " + MUST[0] in p
    )
    w = agents.write_prompt("Fix it.", "a fox @rex", [("judge.reasons", "r" * 40000)], ["rex"])
    assert w.endswith("add no others): @rex") and "Reviewer's reasons:\n<<<\nrrr" in w
    assert len(w) <= agents.PROMPT_MAX - agents.RESERVE
    d = agents.decide_prompt("Go on?", [("$prompt", "p" * 40000)], {"yes": "ok", "no": "again"})
    assert d.endswith('"no": again') and len(d) <= agents.PROMPT_MAX - agents.RESERVE
    assert agents.judge_tokens(4, 3) == agents.MAX_TOKENS and agents.judge_tokens(8, 12) > 1100


def test_a_memory_list_reply_stays_under_the_nats_payload():
    items = [{"id": str(i), "text": "é" * 2000} for i in range(500)]
    body = memory.encode({"items": items})
    got = json.loads(body)
    assert len(body) <= memory.REPLY_MAX and got["truncated"] and got["items"][0]["id"] == "0"
    assert b"\\u00e9" not in body  # UTF-8, not \u escapes
    assert json.loads(memory.encode({"items": items[:2]})) == {"items": items[:2]}


def test_tracing_is_forced_off(monkeypatch):
    import importlib

    import langsmith.utils as ls

    import main

    monkeypatch.setenv("LANGCHAIN_TRACING_V2", "true")
    monkeypatch.setenv("LANGSMITH_TRACING", "true")
    importlib.reload(main)
    ls.get_env_var.cache_clear()
    assert ls.tracing_is_enabled() is False


async def run_rows(e, studio, rows) -> None:
    """Finish every open run of these rows until each has ended."""
    while keys := [k for r in rows for k in studio.open_keys(r)]:
        for k in keys:
            await e.on_run_finished(studio.finish(int(k.split(":")[1]), key=k))
        await idle(e)


@pytest.mark.parametrize("then", [True, False])
async def test_machine_failures_after_a_judge_gave_up_pause_the_batch(pool, then):
    """No reviewer answered: a judge's `then` edge (or an Auto 'no take passed') still counts for the streak."""
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[FAIL])
    br = {"pass": "clip", "fail": "still", "max": 1, "then": "clip"} if then else "clip"
    p = doc("auto", [["inputs", "mem"], ["mem", "still"], ["still", "judge"], ["judge", br],
                     ["clip", "out"]])  # fmt: skip
    del p["nodes"]["rewrite"], p["nodes"]["review"]
    e = engine(pool, studio, js)
    await e.submit(submit(b, p, 4))
    await idle(e)
    await run_rows(e, studio, [1, 2, 3])
    rows = js.rows()
    err = "nothing from judge yet" if then else "no take passed judge"
    assert [rows[r]["error"] for r in (1, 2, 3)] == [err] * 3 and rows[4]["status"] != "failed"
    assert js.batch_status() == "paused"


async def test_a_write_reads_memory_and_allows_only_the_users_mentions(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    bad = json.dumps({"takes": [{"take": i, "checks": [False, True, True], "reason": "add @bob"}
                                for i in range(1, 5)], "ranking": [1, 2, 3, 4]})  # fmt: skip
    studio.answer_for = Script(judge=[bad], rewrite=['{"text": "a fox with @rex and @bob"}'])
    rewrite = doc()["nodes"]["rewrite"] | {"from": ["$prompt", "judge.reasons", "mem.notes"]}
    e = engine(pool, studio, js)
    seed = {"owner_id": OWNER, "op": "create", "pipeline_id": PIPELINE, "text": "foxes face left"}
    note = (await memory.answer(e.store, json.dumps(seed).encode()))["item"]["id"]
    await run_to_judge(e, studio, js, b, doc("auto", rewrite=rewrite))
    await finish(e, studio, "judge")
    await finish(e, studio, "rewrite")
    await finish(e, studio, "rewrite")
    r = js.rows()[1]
    assert r["status"] == "failed" and r["error"].startswith("rewrite: the text mentions @bob")
    apply = [x for x in steps(js) if x["part"] == "apply"][-1]
    assert apply["decision"]["memory_read"] == [note]
    assert apply["memory"] == {"read": [note], "wrote": []}
    prompt = studio.runs[f"{b}:1:1:rewrite:0:0"]["req"]["inputs"]["ask.prompt"]
    assert prompt.endswith("add no others): @rex") and "foxes face left" in prompt


async def test_a_judge_short_of_keep_names_the_counts_and_a_failed_criterion(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    one = judged(
        [[False, True, True], [True, True, True], [False, True, True], [True, False, True]]
    )
    studio.answer_for = Script(judge=[one])
    judge = doc()["nodes"]["judge"] | {"keep": 2, "mode": "auto"}
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc("auto", judge=judge))
    await finish(e, studio, "judge")
    v = next(x for x in steps(js) if x["part"] == "verdict")
    assert v["what"] == f"1 of 4 passed, 2 needed; first failed: {MUST[0]}"
    assert (
        v["decision"]["judge"]["first_failed"] is None
    )  # the best take passes (fixtures/decisions)
    assert cell(js, "judge")["attempts"][0]["judged"]["first_failed"] == MUST[0]


@pytest.mark.parametrize("nice", [True, False])
async def test_a_kept_take_is_all_checks_pass_only_with_its_nice_ones(pool, nice):
    """'all checks pass' names every check; a kept take that missed a nice one passed every must only."""
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[judged([[True, True, nice]] + [[False, True, True]] * 3)])
    judge = doc()["nodes"]["judge"] | {"mode": "auto"}
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, doc("auto", judge=judge))
    await finish(e, studio, "judge")
    v = next(x for x in steps(js) if x["part"] == "verdict")
    assert v["what"] == f"Kept take 1 of 4: {'all checks pass' if nice else 'every must passes'}"


async def test_ask_me_at_the_cap_parks_in_auto_and_a_reject_fails_the_row(pool):
    """API 1.28 "Then: Ask me": a judge branch without `then` parks at the cap (Auto too); a reject ends the row."""
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[FAIL], rewrite=['{"text": "a fox with @rex", "note": "n"}'])
    edges = [["inputs", "mem"], ["mem", "still"], ["still", "judge"],
             ["judge", {"pass": "clip", "fail": "rewrite", "max": 1}],
             ["rewrite", "still"], ["clip", "out"]]  # fmt: skip
    p = doc("auto", edges)
    del p["nodes"]["review"]
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, p)
    await finish(e, studio, "judge")  # a retry is left: rewrite, no parking
    await finish(e, studio, "rewrite")
    await finish(e, studio, "still")
    await finish(e, studio, "judge")  # the cap: Ask me
    r = js.rows()[1]
    assert r["status"] == "waiting" and r["parked"]["kind"] == "judge"
    assert r["parked"]["suggested"] == [] and r["parked"]["on_reject"] == "fail"
    assert cell(js, "judge")["loop"] == {"turns": 1, "max": 1, "then": None, "gave_up": False}
    base = {"batch_id": b, "attempt": 1, "gate": "judge", "visit": 2, "row": 1}
    assert await e.pick(base | {"take_ids": [], "agreed": True})
    await idle(e)
    r = js.rows()[1]
    assert r["status"] == "failed" and r["error"] == "rejected at judge"


async def test_a_pick_at_ask_me_goes_on(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.answer_for = Script(judge=[FAIL])
    edges = [["inputs", "mem"], ["mem", "still"], ["still", "judge"],
             ["judge", {"pass": "clip", "fail": "still", "max": 1}], ["clip", "out"]]  # fmt: skip
    p = doc("auto", edges)
    del p["nodes"]["review"], p["nodes"]["rewrite"]
    e = engine(pool, studio, js)
    await run_to_judge(e, studio, js, b, p)
    await finish(e, studio, "judge")
    await finish(e, studio, "still")
    await finish(e, studio, "judge")
    parked = js.rows()[1]["parked"]
    pick = parked["candidates"][:1]
    base = {"batch_id": b, "attempt": 1, "gate": "judge", "visit": 2, "row": 1}
    assert await e.pick(base | {"take_ids": pick, "agreed": False})
    await idle(e)
    req = next(r["req"] for k, r in studio.runs.items() if k.startswith(f"{b}:1:1:clip"))
    assert req["inputs"]["keyframes.images"] == [{"image": {"take_id": pick[0]}}]


async def test_judge_test_fills_the_template_with_memory_and_derives_the_verdict(pool):
    """pipelines.judge.test (API 1.28): the prompt as a batch's judge would ask, and the verdict from the answer."""
    import judge_test

    e = engine(pool, FakeStudio(), FakeJS())
    for text, key in (("faces left", "briefs"), ("ignore me", "other")):
        seed = {
            "owner_id": OWNER,
            "op": "create",
            "pipeline_id": PIPELINE,
            "text": text,
            "key": key,
        }
        assert "item" in await memory.answer(e.store, json.dumps(seed).encode())
    crit = {"must": MUST, "nice": NICE}
    req = {"op": "prompt", "owner_id": OWNER, "pipeline_id": PIPELINE, "node": "judge", "criteria": crit,
           "n": 4, "brief": "a fox", "memory": {"limit": 8, "key": "briefs"}}  # fmt: skip
    got = await judge_test.answer(e.store, json.dumps(req).encode())
    assert got["prompt"] == agents.judge_prompt("a fox", 4, crit, [], "faces left")
    assert got["template"] == agents.JUDGE and got["max_tokens"] == agents.judge_tokens(4, 3)
    assert json.loads(got["schema"]) == agents.judge_schema(4, 3)
    other = req | {"owner_id": str(uuid.uuid4())}  # another owner's namespace is empty
    assert (
        "faces left" not in (await judge_test.answer(e.store, json.dumps(other).encode()))["prompt"]
    )
    ids = [str(uuid.uuid4()) for _ in range(4)]
    v = {"op": "verdict", "criteria": crit, "keep": 1, "take_ids": ids, "text": PASS3}
    got = await judge_test.answer(e.store, json.dumps(v).encode())
    assert got["verdict"] == "pass" and got["picked"] == [ids[2]]
    assert [t["passes"] for t in got["takes"]] == [False, True, True, False]
    assert got["takes"][0]["reason"] == "reason 1"
    bad = await judge_test.answer(e.store, json.dumps(v | {"text": "not json"}).encode())
    assert bad["code"] == "answer" and bad["error"].startswith("answer did not match")
    nope = await judge_test.answer(e.store, json.dumps(v | {"keep": 0}).encode())
    assert nope["code"] == "invalid"
