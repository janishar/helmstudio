"""Stand-ins for NATS and studio, and a scratch Postgres database per test (TEST_DATABASE_URL, the maintenance
database of the helmstudio-test Postgres: .tools/test-db; without it the engine tests skip). Test modules import
the helpers with `from conftest import ...`."""

import asyncio
import copy
import json
import os
import uuid
from pathlib import Path
from urllib.parse import urlparse, urlunparse

import psycopg
import pytest

import main
from engine import Engine
from messaging import schemas
from messaging.publisher import Publisher, now

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
OWNER = "7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13"
SESSION = "0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10"
TRACE = {"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}


def fixture(name: str) -> dict:
    return json.loads((CONTRACTS / "fixtures/messages" / f"{name}.json").read_text())


def command(kind: str, batch_id: str, n: int = 2, **data) -> dict:
    """A cmd.pipeline.<kind> for `batch_id`; submit has `n` rows."""
    cmd = copy.deepcopy(fixture(f"cmd.pipeline.{kind}.valid"))
    cmd["id"], cmd["job_id"], cmd["owner_id"] = str(uuid.uuid4()), batch_id, OWNER
    cmd["data"]["batch_id"] = batch_id
    if kind == "submit":
        cmd["data"]["session_id"] = SESSION
        cmd["data"]["rows"] = [
            {"row": i, "inputs": {"echo.steps": 3, "prompt.text": f"row {i}"}}
            for i in range(1, n + 1)
        ]
    cmd["data"] |= data
    assert not schemas.message_errors(cmd), schemas.message_errors(cmd)
    return cmd


class FakeJS:
    """JetStream stand-in: records every publish."""

    def __init__(self) -> None:
        self.sent: list[tuple[str, bytes, dict | None]] = []
        self.down = False  # every publish fails (a NATS outage)

    async def publish(self, subject: str, data: bytes, headers: dict | None = None) -> None:
        if self.down:
            raise ConnectionError("nats is down")
        self.sent.append((subject, data, headers))

    def events(self, kind: str | None = None) -> list[dict]:
        out = [json.loads(d) for s, d, _ in self.sent if s.startswith("evt.pipeline.")]
        return [e for e in out if kind is None or e["type"] == f"evt.pipeline.{kind}"]

    def batch_status(self) -> str:
        return max(self.events("batch"), key=lambda e: e["data"]["seq"])["data"]["status"]

    def rows(self) -> dict[int, dict]:
        """Each row's newest state (by seq), as studio's projector keeps it."""
        out: dict[int, dict] = {}
        for e in sorted(self.events("row"), key=lambda e: e["data"]["seq"]):
            out[e["data"]["row"]] = e["data"]
        return out


class FakeStudio:
    """studio.runs.create on a dict: one run per idempotency key. `before[row]` / `after[row]` are events a
    request of that row waits on before creating the run / after creating it (a reply that never comes)."""

    def __init__(self) -> None:
        self.runs: dict[str, dict] = {}  # idempotency key → run
        self.asked: list[str] = []  # every key asked, in order
        self.before: dict[int, asyncio.Event] = {}
        self.after: dict[int, asyncio.Event] = {}
        self.create_status = (
            "queued"  # cancelled once the batch is stopped, failed with no live host
        )
        self.reply_error: dict | None = None  # {error, code}: answer every request with it

    async def create_run(self, req: dict) -> dict:
        assert not schemas.runs_create_errors("request", req)
        self.asked.append(req["idempotency_key"])
        if ev := self.before.get(req["row"]):
            await ev.wait()
        if self.reply_error:
            return self.reply_error
        k, created = req["idempotency_key"], req["idempotency_key"] not in self.runs
        if created:
            self.runs[k] = {"run_id": str(uuid.uuid4()), "status": self.create_status, "req": req}
            self.runs[k] |= (
                {"take_ids": [], "error": "no live host for env fake"}
                if (self.create_status == "failed")
                else {"take_ids": [], "error": None}
            )
        if ev := self.after.get(req["row"]):
            await ev.wait()
        run = self.runs[k]
        reply = {"run_id": run["run_id"], "status": run["status"], "created": created}
        reply |= {"take_ids": run["take_ids"], "error": run["error"]}
        assert not schemas.runs_create_errors("reply", reply)
        return reply

    def run(self, row: int, attempt: int = 1) -> dict:
        return next(
            r for k, r in self.runs.items() if k.split(":")[1:3] == [str(row), str(attempt)]
        )

    def finish(self, row: int, status: str = "succeeded", error: str | None = None, attempt=1):
        """The run of `row` ends: its evt.studio.run_finished data (checked against the contract)."""
        run = self.run(row, attempt)
        run["status"] = status
        run["take_ids"] = [str(uuid.uuid4())] if status == "succeeded" else []
        run["error"] = error or ("boom" if status == "failed" else None)
        req = run["req"]
        data = {
            "run_id": run["run_id"],
            "batch_id": req["batch_id"],
            "row": row,
            "idempotency_key": req["idempotency_key"],
            "status": status,
            "take_ids": run["take_ids"],
            "error": run["error"],
        }
        evt = {
            "type": "evt.studio.run_finished",
            "version": 1,
            "id": str(uuid.uuid4()),
            "job_id": run["run_id"],
            "owner_id": OWNER,
            "occurred_at": now(),
            "trace": TRACE,
            "data": data,
        }
        assert not schemas.message_errors(evt), schemas.message_errors(evt)
        return data


def engine(pool, studio: FakeStudio, js: FakeJS, concurrency: int = 8, window: int = 16) -> Engine:
    return Engine(pool, main.saver_for(pool), Publisher(js), studio.create_run, concurrency, window)


async def until(cond, timeout: float = 5.0) -> None:
    """Wait until cond() is true (fails the test after `timeout`)."""
    loop = asyncio.get_running_loop()
    end = loop.time() + timeout
    while not cond():
        assert loop.time() < end, "timed out"
        await asyncio.sleep(0.01)


async def idle(e: Engine) -> None:
    """Wait until the engine drives no row."""
    while e.tasks:
        await asyncio.gather(*list(e.tasks.values()), return_exceptions=True)


@pytest.fixture
async def pool():
    url = os.environ.get("TEST_DATABASE_URL")
    if not url:
        pytest.skip("TEST_DATABASE_URL not set (.tools/test-db, .test.env)")
    name = f"pipelines_test_{uuid.uuid4().hex[:12]}"
    with psycopg.connect(url, autocommit=True) as c:
        c.execute(f'CREATE DATABASE "{name}"')
    p = await main.open_pool(urlunparse(urlparse(url)._replace(path=f"/{name}")))
    try:
        await main.migrate(p, main.saver_for(p))
        yield p
    finally:
        await p.close()
        with psycopg.connect(url, autocommit=True) as c:
            c.execute(f'DROP DATABASE "{name}" WITH (FORCE)')
