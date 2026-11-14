"""A run end to end on fakes: the events it publishes (order, seq, Nats-Msg-Id, contract), the PNG, the ack."""

import io
import json

from conftest import FakeJS, FakeMsg, command, consumer
from PIL import Image

from consumer import SHUTDOWN
from messaging import schemas

RUN = "3c9a7e21-5d4b-4f6a-8b1c-9e2d7f0a4b35"
SESSION = "0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10"
PREFIX = "u/7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13/runs/3c9a7e21-5d4b-4f6a-8b1c-9e2d7f0a4b35/"


async def run(cmd, js: FakeJS | None = None, setup=None):
    js = js or FakeJS()
    c = consumer(js)
    if setup:
        setup(c)
    msg = FakeMsg(cmd, js)
    await c.handle(msg)
    return js, c, msg


def kinds(js: FakeJS) -> list[str]:
    out = []
    for e in js.events():
        k = e["type"].removeprefix("evt.run.")
        out.append(e["data"]["status"] if k == "status" else k)
    return out


def check_events(js: FakeJS) -> None:
    """Every event is on contract, on its subject, numbered 1..n with a matching Nats-Msg-Id."""
    evts = [(s, json.loads(d), h) for s, d, h in js.sent if s.startswith("evt.")]
    for n, (subject, msg, headers) in enumerate(evts, 1):
        assert schemas.message_errors(msg) == []
        assert msg["job_id"] == RUN and msg["data"]["seq"] == n
        assert subject == f"evt.run.{SESSION}.{msg['type'].removeprefix('evt.run.')}"
        assert headers == {"Nats-Msg-Id": f"{RUN}:{n}"}


async def test_echo_run_succeeds_with_progress_and_a_png():
    js, c, msg = await run(command(steps=3))
    assert kinds(js) == ["running", "progress", "progress", "progress", "output", "succeeded"]
    check_events(js)
    evts = js.events()
    assert evts[0]["data"]["host"] == "fake"
    assert [(e["data"]["node"], e["data"]["step"], e["data"]["of"]) for e in evts[1:4]] == [
        ("echo", 1, 3),
        ("echo", 2, 3),
        ("echo", 3, 3),
    ]
    out = evts[4]["data"]
    data, content_type = c.store.objects[PREFIX + "0.png"]
    assert content_type == "image/png"
    image = Image.open(io.BytesIO(data))
    assert image.format == "PNG"
    assert out == {
        "seq": 5,
        "index": 0,
        "key": PREFIX + "0.png",
        "kind": "image",
        "width": image.width,
        "height": image.height,
        "bytes": len(data),
    }
    assert msg.log == [("ack", len(js.sent))]  # acked after the terminal status
    assert c.host.run_id is None and json.loads(c.host.kv.data["fake.fake"])["state"] == "idle"


async def test_fail_reports_failed_after_the_last_step_without_output():
    js, c, msg = await run(command(steps=2, fail=True))
    assert kinds(js) == ["running", "progress", "progress", "failed"]
    check_events(js)
    assert js.events()[-1]["data"]["error"] == "echo: failed on purpose (fail=true)"
    assert c.store.objects == {}
    assert msg.log == [("ack", len(js.sent))]


async def test_cancel_key_written_mid_run_stops_at_the_next_step():
    holder = {}

    def on_publish(subject, body):
        if subject.endswith(".progress") and body["data"]["step"] == 2:
            holder["c"].cancels_kv.put_nowait(RUN)

    js = FakeJS(on_publish)
    js, c, msg = await run(command(steps=10, delay_ms=5), js, setup=lambda c: holder.update(c=c))
    got = kinds(js)  # the watch is asynchronous: the step in flight may still finish
    assert got[0] == "running" and got[-1] == "cancelled"
    assert got[1:-1] in (["progress"] * 2, ["progress"] * 3)
    check_events(js)
    assert c.store.objects == {}
    assert msg.log == [("ack", len(js.sent))]


async def test_cancel_key_written_before_start_runs_nothing():
    js, _, msg = await run(command(), setup=lambda c: c.cancels_kv.data.update({RUN: b"t"}))
    assert kinds(js) == ["cancelled"]
    check_events(js)
    assert msg.log == [("ack", 1)]


async def test_runner_error_outside_the_graph_still_ends_the_run():
    async def broken_watch(key, ignore_deletes=False):
        raise TimeoutError

    def setup(c):
        c.cancels_kv.watch = broken_watch

    js, _, msg = await run(command(), setup=setup)
    assert kinds(js) == ["failed"]
    assert js.events()[-1]["data"]["error"] == "runner error (TimeoutError)"
    check_events(js)
    assert msg.log == [("ack", 1)]


async def test_shutdown_stops_the_run_and_reports_it_failed():
    holder = {}

    def on_publish(subject, body):
        if subject.endswith(".progress"):
            holder["c"].stopping = True

    js = FakeJS(on_publish)
    js, _, msg = await run(command(steps=5), js, setup=lambda c: holder.update(c=c))
    assert kinds(js) == ["running", "progress", "failed"]
    assert js.events()[-1]["data"]["error"] == SHUTDOWN
    assert msg.log == [("ack", len(js.sent))]


async def test_off_contract_command_is_dead_lettered_and_termed():
    bad = command()
    del bad["data"]["session_id"]
    js, _, msg = await run(bad)
    # no session: nothing to report on
    assert [s for s, _, _ in js.sent] == ["dlq.cmd.run.fake.fake"]
    assert js.sent[0][1] == msg.data
    assert msg.log == [("term", 1)]


async def test_garbage_is_dead_lettered_and_termed():
    js, _, msg = await run(b"not json")
    assert [s for s, _, _ in js.sent] == ["dlq.cmd.run.fake.fake"]
    assert msg.log == [("term", 1)]


async def test_graph_that_does_not_fit_the_node_schemas_is_rejected_and_reported():
    bad = command()
    bad["data"]["workflow"]["nodes"]["echo"]["kind"] = "echo/missing"
    js, _, msg = await run(bad)
    assert [s for s, _, _ in js.sent] == ["dlq.cmd.run.fake.fake", f"evt.run.{SESSION}.status"]
    check_events(js)
    status = js.events()[0]["data"]
    assert status["status"] == "failed" and "unknown node kind" in status["error"]
    assert msg.log == [("term", 2)]
