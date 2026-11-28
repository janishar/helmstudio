"""The engine on gated pipelines (cmd.pipeline.submit.valid.2: inputs → still → review → edit → out): picks resume
through the window, a lost park or step publish is published again, rejects never pause, a retry draws new seeds."""

import copy
import uuid

from conftest import OWNER, SESSION, FakeJS, FakeStudio, engine, fixture, idle, until

from messaging import publisher, schemas


def gated(b: str, n: int, branch: bool = True) -> dict:
    """A gated pipeline batch of n rows, one still per visit; without a branch a reject fails the row."""
    cmd = copy.deepcopy(fixture("cmd.pipeline.submit.valid.2"))
    cmd["id"], cmd["job_id"], cmd["owner_id"] = str(uuid.uuid4()), b, OWNER
    d = cmd["data"]
    d["batch_id"], d["session_id"] = b, SESSION
    p = d["snapshot"]["pipeline"]
    p["nodes"]["still"]["seeds"] = 1
    if not branch:
        p["edges"][2] = ["review", "edit"]
    d["rows"] = [
        {"row": i, "inputs": {"inputs.prompt": f"p{i}", "inputs.edit": "night"},
         "seeds": {"still": 1000 * i, "edit": 5000 + i}}
        for i in range(1, n + 1)
    ]  # fmt: skip
    assert not schemas.message_errors(cmd), schemas.message_errors(cmd)
    return cmd


def open_of(studio: FakeStudio, node: str) -> list[str]:
    return [k for k, r in studio.runs.items() if k.split(":")[3] == node and r["status"] == "queued"]


async def finish_open(e, studio: FakeStudio, node: str) -> None:
    for k in open_of(studio, node):
        await e.on_run_finished(studio.finish(int(k.split(":")[1]), key=k))
    await idle(e)


async def park_all(e, studio, js, b: str, n: int) -> None:
    await e.submit(gated(b, n))
    await idle(e)
    while open_of(studio, "still"):
        await finish_open(e, studio, "still")
    assert all(js.rows()[r]["status"] == "waiting" for r in range(1, n + 1))


def answer(js: FakeJS, b: str, row: int, reject: bool = False) -> dict:
    p = js.rows()[row]["parked"]
    d = {"batch_id": b, "row": row, "attempt": 1, "gate": "review", "visit": p["visit"]}
    return d | ({"reject": True} if reject else {"take_ids": p["candidates"][:1]})


async def test_picks_resume_through_the_window(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js, window=2)
    await park_all(e, studio, js, b, 5)
    for row in range(1, 6):  # a burst of picks
        assert await e.pick(answer(js, b, row))
    await idle(e)
    assert len(open_of(studio, "edit")) == 2  # not 5
    done = 0
    while open_of(studio, "edit"):
        assert len(open_of(studio, "edit")) <= 2
        done += len(open_of(studio, "edit"))
        await finish_open(e, studio, "edit")
    assert done == 5 and js.batch_status() == "done"
    assert all(r["status"] == "succeeded" for r in js.rows().values())


async def test_a_park_or_step_whose_publish_failed_is_published_again(pool, monkeypatch):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(gated(b, 2))
    await idle(e)
    monkeypatch.setattr(publisher, "RETRY_FOR", 0.0)  # a publish that fails raises at once
    real, failed = js.publish, []

    async def flaky(subject, data, headers=None):  # the first step event and the first parked row are lost
        parked = subject.endswith(".row") and b'"waiting"' in data
        if (subject.endswith(".step") or parked) and subject[-4:] not in failed:
            failed.append(subject[-4:])
            raise ConnectionError("nats is down")
        await real(subject, data, headers)

    monkeypatch.setattr(js, "publish", flaky)
    (k,) = open_of(studio, "still")
    await e.on_run_finished(studio.finish(1, key=k))
    await until(lambda: js.rows().get(1, {}).get("status") == "waiting", timeout=10)
    await until(lambda: open_of(studio, "still"))  # row 2 went once row 1 parked
    await idle(e)
    assert sorted(failed) == [".row", "step"]
    steps = sorted({s["data"]["step"] for s in js.events("step") if s["data"]["row"] == 1
                    and s["data"]["status"] != "current"})  # fmt: skip
    assert steps == list(range(1, len(steps) + 1)) and len(steps) >= 3


async def test_rejects_never_pause_the_batch(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(gated(b, 4, branch=False))
    await idle(e)
    while open_of(studio, "still"):
        await finish_open(e, studio, "still")
    for row in (1, 2, 3):
        assert await e.pick(answer(js, b, row, reject=True))
        await idle(e)
    rows = js.rows()
    assert [rows[r]["error"] for r in (1, 2, 3)] == ["rejected at review"] * 3
    assert js.batch_status() == "running"
    assert await e.pick(answer(js, b, 4))
    await idle(e)
    await finish_open(e, studio, "edit")
    assert js.batch_status() == "done"


async def test_a_retry_draws_new_seeds(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(gated(b, 1, branch=False))
    await idle(e)
    await finish_open(e, studio, "still")
    await e.pick(answer(js, b, 1, reject=True))
    await idle(e)
    assert js.batch_status() == "done"
    await e.retry(b, 1, 2)
    await idle(e)
    seeds = {k.split(":")[2]: r["req"]["inputs"]["seed.seed"] for k, r in studio.runs.items()}
    assert seeds == {"1": 1000, "2": 1064}  # base + (attempt - 1) × 64
