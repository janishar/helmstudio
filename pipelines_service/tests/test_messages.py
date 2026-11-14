"""Every message this service builds is checked against the contract; incoming ones are too."""

import json

import pytest
from conftest import OWNER, TRACE, command

import consumer
import events
from engine import row_data
from messaging import publisher, schemas

BATCH = {"batch_id": "5a2e9c4b-1d7f-4b3a-9c8e-6f0d2a1b3c47", "owner_id": OWNER, "trace": TRACE}
RUN = "1c9e3f6a-2b4d-4e8f-9a1c-5d7b3e2f8a60"


def rec(status, run_id=None, take_ids=None, error=None, attempt=1) -> dict:
    d = {"row": 3, "attempt": attempt, "status": status, "run_id": run_id}
    return d | {"take_ids": take_ids, "error": error}


@pytest.mark.parametrize(
    "record",
    [
        rec("queued"),
        rec("running", RUN),
        rec("succeeded", RUN, [RUN]),
        rec("failed", RUN, [], "boom", attempt=2),
        rec("failed", None, [], "echo.steps: too big"),
        rec("cancelled"),
        rec("cancelled", RUN, []),
    ],
)
def test_row_events_are_on_contract(record):
    msg = publisher.build("row", BATCH, {"seq": 4} | row_data(record))
    assert msg["type"] == "evt.pipeline.row" and msg["job_id"] == BATCH["batch_id"]


@pytest.mark.parametrize(
    "data",
    [
        {"status": "running"},
        {"status": "paused", "reason": "paused by you"},
        {"status": "done"},
        {"status": "stopped"},
        {"status": "failed", "reason": "no snapshot"},
    ],
)
def test_batch_events_are_on_contract(data):
    publisher.build("batch", BATCH, {"seq": 1} | data)


def test_off_contract_events_are_refused():
    with pytest.raises(ValueError):
        publisher.build("batch", BATCH, {"seq": 1, "status": "paused"})  # no reason
    with pytest.raises(ValueError):
        publisher.build("row", BATCH, {"seq": 1} | row_data(rec("succeeded")))  # no run id


class FakeMsg:
    def __init__(self, body, subject="cmd.pipeline.pause", headers=None) -> None:
        self.data = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.subject, self.headers, self.log = subject, headers, []

    async def ack(self):
        self.log.append("ack")

    async def term(self):
        self.log.append("term")

    async def nak(self, delay=None):
        self.log.append("nak")


class Recorder:
    def __init__(self) -> None:
        self.calls = []

    def __getattr__(self, name):
        async def call(*args):
            self.calls.append((name, args))
            return True

        return call


class FakePool:
    """Just the inbox."""

    def __init__(self) -> None:
        self.seen: set[str] = set()

    def connection(self):
        pool = self

        class Conn:
            async def __aenter__(self):
                return self

            async def __aexit__(self, *a):
                return False

            async def execute(self, sql, args):
                hit = args[0] in pool.seen
                pool.seen.add(args[0]) if sql.startswith("INSERT") else None

                class Cur:
                    async def fetchone(self):
                        return 1 if hit else None

                return Cur()

        return Conn()


class PubJS:
    def __init__(self) -> None:
        self.sent = []

    async def publish(self, subject, body, headers=None):
        self.sent.append(subject)


async def test_commands_dispatch_once_and_reject_off_contract():
    eng, js = Recorder(), PubJS()
    c = consumer.Commands(eng, FakePool(), js)
    cmd = command("pause", BATCH["batch_id"])
    for _ in range(2):  # a redelivery is acked without running again
        m = FakeMsg(cmd, headers={"Nats-Msg-Id": f"{BATCH['batch_id']}:2"})
        await c.handle(m)
        assert m.log == ["ack"]
    assert eng.calls == [("pause", (BATCH["batch_id"],))]
    retry = command("retry", BATCH["batch_id"])
    await c.handle(FakeMsg(retry, "cmd.pipeline.retry"))
    assert eng.calls[-1] == ("retry", (BATCH["batch_id"], 4, 2))
    bad = FakeMsg({"type": "cmd.pipeline.pause", "data": {}})
    await c.handle(bad)
    assert bad.log == ["term"] and js.sent == ["dlq.cmd.pipeline.pause"]


async def test_run_finished_off_contract_is_termed():
    eng = Recorder()
    m = FakeMsg({"type": "evt.studio.run_finished"}, "evt.studio.x.run_finished")
    await events.RunEvents(eng).handle(m)
    assert m.log == ["term"] and eng.calls == []


def test_the_contract_copy_is_current():
    from conftest import CONTRACTS

    for p in (CONTRACTS / "messages").glob("*.schema.json"):
        assert (schemas.GEN / "messages" / p.name).read_text() == p.read_text(), (
            f"make gen: {p.name}"
        )
