"""The engine on a scratch Postgres (its tables and AsyncPostgresSaver), with fake studio and JetStream."""

import asyncio
import gc
import uuid

import pytest
from conftest import FakeJS, FakeStudio, command, engine, idle, until

from engine import RESTARTED, thread_id
from messaging import publisher


def key(b: str, row: int, attempt: int = 1) -> str:
    return f"{b}:{row}:{attempt}:step:0:0"


async def threads_left(e, b: str, n: int) -> list[int]:
    out = []
    for row in range(1, n + 1):
        snap = await e.graph.aget_state({"configurable": {"thread_id": thread_id(b, row)}})
        if snap.values:
            out.append(row)
    return out


async def test_row_1_first_then_every_row_and_done(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 3))
    await idle(e)
    assert studio.asked == [key(b, 1)]
    assert js.rows()[1]["status"] == "running" and js.batch_status() == "running"
    assert await e.on_run_finished(studio.finish(1))
    await idle(e)
    assert sorted(studio.asked) == [key(b, r) for r in (1, 2, 3)]
    for row in (2, 3):
        assert await e.on_run_finished(studio.finish(row))
    rows = js.rows()
    assert [rows[r]["status"] for r in (1, 2, 3)] == ["succeeded"] * 3
    assert rows[2]["take_ids"] == studio.run(2)["take_ids"]
    assert js.batch_status() == "done"
    assert await threads_left(e, b, 3) == []  # deleted when the batch ended
    seqs = [m["data"]["seq"] for m in js.events()]
    assert len(seqs) == len(set(seqs))
    assert [h["Nats-Msg-Id"] for _, _, h in js.sent] == [f"{b}:{s}" for s in seqs]


async def test_a_failed_row_does_not_stop_the_others(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 3))
    await idle(e)
    await e.on_run_finished(studio.finish(1, "failed", "out of memory"))
    await idle(e)
    await e.on_run_finished(studio.finish(2))
    await e.on_run_finished(studio.finish(3))
    rows = js.rows()
    assert rows[1]["status"] == "failed" and rows[1]["error"] == "out of memory"
    assert rows[2]["status"] == rows[3]["status"] == "succeeded"
    assert js.batch_status() == "done"


async def test_three_failures_in_a_row_with_one_error_pause(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 5))
    await idle(e)
    for row in (1, 2, 3):
        await e.on_run_finished(studio.finish(row, "failed", "no VAE"))
        await idle(e)
    batch = max(js.events("batch"), key=lambda m: m["data"]["seq"])["data"]
    assert batch == {
        "seq": batch["seq"],
        "status": "paused",
        "reason": "3 rows in a row failed: no VAE",
    }


async def test_pause_holds_unreleased_rows_and_resume_releases_them(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 3))
    await idle(e)
    await e.pause(b)
    assert js.batch_status() == "paused"
    assert await e.on_run_finished(studio.finish(1))  # a parked thread still resumes while paused
    await idle(e)
    assert studio.asked == [key(b, 1)] and js.rows()[1]["status"] == "succeeded"
    await e.resume(b)
    await idle(e)
    assert js.batch_status() == "running"
    assert sorted(studio.asked) == [key(b, r) for r in (1, 2, 3)]


async def test_stop_cancels_rows_never_started_and_ends_with_the_runs(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 3))
    await idle(e)
    await e.stop(b)
    rows = js.rows()
    assert rows[2]["status"] == rows[3]["status"] == "cancelled" and rows[1]["status"] == "running"
    assert js.batch_status() == "running"  # until every row is final
    await e.on_run_finished(studio.finish(1, "cancelled"))  # studio cancelled the run
    await idle(e)
    assert js.rows()[1]["status"] == "cancelled" and js.batch_status() == "stopped"
    assert studio.asked == [key(b, 1)]


async def test_run_finished_twice_or_before_the_thread_parks(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    early: list[asyncio.Task] = []
    create = studio.create_run

    async def create_then_finish_at_once(req):
        reply = await create(req)
        if req["row"] == 1:  # the run ends and its event is handled before this reply is recorded
            early.append(asyncio.create_task(e.on_run_finished(studio.finish(1))))
            await asyncio.sleep(0.05)
        return reply

    e.create_run = create_then_finish_at_once
    await e.submit(command("submit", b, 2))
    await until(lambda: early)
    assert await early[0]  # it waited for the park, then resumed the thread
    assert js.rows()[1]["status"] == "succeeded"
    before = js.rows()[1]
    assert not await e.on_run_finished(studio.finish(1))  # a second delivery is dropped
    assert js.rows()[1] | {"seq": 0} == before | {"seq": 0}  # (its end is only published again)
    await idle(e)
    assert studio.asked == [key(b, 1), key(b, 2)]


async def test_a_terminal_reply_ends_the_row_and_the_later_event_is_dropped(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.create_status = "failed"  # no live host
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 1))
    await idle(e)
    assert js.rows()[1]["status"] == "failed" and js.batch_status() == "done"
    assert not await e.on_run_finished(studio.finish(1, "failed", "no live host for env fake"))


async def test_retry_runs_the_row_again_under_a_new_key(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 2))
    await idle(e)
    await e.on_run_finished(studio.finish(1))
    await idle(e)
    await e.on_run_finished(studio.finish(2, "failed"))
    assert js.batch_status() == "done"
    await e.retry(b, 2, 2)
    await e.retry(b, 2, 2)  # a redelivery changes nothing
    await idle(e)
    assert js.batch_status() == "running"
    assert studio.asked[-1] == key(b, 2, 2) and len(studio.runs) == 3
    await e.on_run_finished(studio.finish(2, attempt=2))
    rows = js.rows()
    assert rows[2]["status"] == "succeeded" and rows[2]["attempt"] == 2
    assert js.batch_status() == "done"


async def test_a_crash_mid_batch_continues_without_a_second_run(pool):
    """Rows in every state when the engine dies: 1 finished, 2 parked on its run, 3 asked studio (run created,
    reply lost), 4 in submit (studio never got it), 5 never started (no thread)."""
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    studio.after[3] = asyncio.Event()  # never set: the reply is lost
    studio.before[4] = asyncio.Event()
    e1 = engine(pool, studio, js, concurrency=2)  # rows 3 and 4 hold both slots: row 5 never starts
    await e1.submit(command("submit", b, 5))
    await idle(e1)
    await e1.on_run_finished(studio.finish(1))
    await until(lambda: key(b, 3) in studio.runs and key(b, 4) in studio.asked)
    await until(lambda: js.rows().get(2, {}).get("status") == "running")
    assert key(b, 5) not in studio.asked
    await e1.close()  # the crash
    assert await threads_left(e1, b, 5) == [1, 2, 3, 4]

    studio.after.clear()
    studio.before.clear()
    e2 = engine(pool, studio, js)
    await e2.start()
    batch = max(js.events("batch"), key=lambda m: m["data"]["seq"])["data"]
    assert batch["status"] == "paused" and batch["reason"] == RESTARTED
    await idle(e2)
    assert len(studio.asked) == 4  # nothing new until Resume
    assert await e2.on_run_finished(studio.finish(2))  # the parked thread resumes while paused
    await e2.resume(b)
    await idle(e2)
    for row in (3, 4, 5):
        assert await e2.on_run_finished(studio.finish(row))
    assert js.batch_status() == "done"
    assert [js.rows()[r]["status"] for r in range(1, 6)] == ["succeeded"] * 5
    # one run per row, each under its row's key; row 3 asked again and got the run it created before the crash
    assert sorted(studio.runs) == sorted(key(b, r) for r in range(1, 6))
    assert sorted(set(studio.asked)) == sorted(key(b, r) for r in range(1, 6))
    assert studio.asked.count(key(b, 1)) == studio.asked.count(key(b, 2)) == 1
    assert studio.asked.count(key(b, 3)) == studio.asked.count(key(b, 4)) == 2
    assert await threads_left(e2, b, 5) == []


async def test_a_redelivered_submit_continues_the_batch_it_has(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    cmd = command("submit", b, 2)
    await e.submit(cmd)
    await idle(e)
    await e.submit(cmd)
    await idle(e)
    assert studio.asked == [key(b, 1)] and len(studio.runs) == 1


async def test_an_end_whose_publish_failed_is_followed_up_on_redelivery(pool, monkeypatch):
    monkeypatch.setattr(publisher, "RETRY_FOR", 0.05)
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 1))
    await idle(e)
    done = studio.finish(1)
    js.down = True  # NATS down past the publisher's retries: the row is final, nothing published
    with pytest.raises(ConnectionError):
        await e.on_run_finished(done)
    js.down = False
    assert not await e.on_run_finished(
        done
    )  # the redelivery: no thread waits, the end is followed up
    assert js.rows()[1]["status"] == "succeeded" and js.batch_status() == "done"
    assert await threads_left(e, b, 1) == []


async def test_a_restart_ends_a_batch_whose_last_end_was_never_followed_up(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e1 = engine(pool, studio, js)
    await e1.submit(command("submit", b, 1))
    await idle(e1)
    run = studio.finish(1)
    # the crash: the row's end committed, then nothing (no publish, no batch end)
    await e1._end_row(b, 1, 1, {"status": "succeeded", "run_id": run["run_id"], "take_ids": []})
    e2 = engine(pool, studio, js)
    await e2.start()
    assert js.rows()[1]["status"] == "succeeded" and js.batch_status() == "done"


async def test_a_drive_that_fails_is_tried_again(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    create, fails = studio.create_run, [1]

    async def down_once(req):
        if fails:
            fails.pop()
            raise OSError("postgres restarted")
        return await create(req)

    e.create_run = down_once
    await e.submit(command("submit", b, 1))
    await until(lambda: js.rows().get(1, {}).get("status") == "running", timeout=10)
    assert studio.asked == [key(b, 1)]


@pytest.mark.parametrize("then", ["resume", "stop"])
async def test_pause_then_resume_or_stop_while_rows_wait_for_a_slot(pool, then):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js, concurrency=1)
    await e.submit(command("submit", b, 4))
    await idle(e)
    studio.before[2] = asyncio.Event()  # row 2 holds the only slot; rows 3 and 4 wait for it
    await e.on_run_finished(studio.finish(1))
    await until(lambda: key(b, 2) in studio.asked)
    await e.pause(b)
    studio.before[2].set()
    await getattr(e, then)(b)
    await idle(e)
    if then == "resume":
        assert sorted(studio.asked) == [key(b, r) for r in (1, 2, 3, 4)]
    else:
        await e.on_run_finished(studio.finish(2, "cancelled"))
        assert [js.rows()[r]["status"] for r in (3, 4)] == ["cancelled"] * 2
        assert js.batch_status() == "stopped"


async def test_a_redelivered_retry_drives_the_row(pool, monkeypatch):
    monkeypatch.setattr(publisher, "RETRY_FOR", 0.05)
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js)
    await e.submit(command("submit", b, 1))
    await idle(e)
    await e.on_run_finished(studio.finish(1, "failed"))
    assert js.batch_status() == "done"
    js.down = True
    with pytest.raises(ConnectionError):
        await e.retry(b, 1, 2)  # the row is queued at attempt 2, nothing published or driven
    js.down = False
    await e.retry(b, 1, 2)
    await idle(e)
    assert studio.asked[-1] == key(b, 1, 2) and js.batch_status() == "running"
    assert js.rows()[1]["status"] == "running" and js.rows()[1]["attempt"] == 2


async def test_at_most_window_rows_wait_on_studio_and_locks_are_not_kept(pool):
    studio, js, b = FakeStudio(), FakeJS(), str(uuid.uuid4())
    e = engine(pool, studio, js, window=2)
    await e.submit(command("submit", b, 5))
    await idle(e)
    await e.on_run_finished(studio.finish(1))
    await idle(e)
    assert sorted(studio.asked) == [key(b, r) for r in (1, 2, 3)]
    for row in (2, 3, 4, 5):
        await e.on_run_finished(studio.finish(row))
        await idle(e)
    assert sorted(studio.asked) == [key(b, r) for r in (1, 2, 3, 4, 5)]
    assert js.batch_status() == "done"
    gc.collect()
    assert not e.row_locks and not e.batch_locks
