"""evt.run.<session_id>.{status,progress,output} for one run. `seq` is per run, from 1, over all three kinds; it
lives in memory because a run command is delivered once (studio, not redelivery, retries a run). Every message
is checked against the contract before it leaves and is published with Nats-Msg-Id <run_id>:<attempt>:<seq>
(seq restarts at 1 for each attempt; data.attempt is the command's attempt); the publish
waits for JetStream's ack, and is retried for up to RETRY_FOR seconds (JetStream drops a duplicate by its id), so a
broker hiccup does not fail the run."""

import asyncio
import json
import uuid
from datetime import UTC, datetime

from config import HOST
from messaging import schemas

RETRY_FOR = 10.0  # seconds a publish is retried
RETRY_EVERY = 0.5  # seconds between tries


def now() -> str:
    """The current UTC time as RFC 3339 with a Z suffix."""
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


class RunEvents:
    """Builds and publishes the events of the run named by a cmd.run.execute command."""

    def __init__(self, js, cmd: dict) -> None:
        self.js, self.cmd, self.seq = js, cmd, 0

    def build(self, kind: str, data: dict) -> dict:
        """The next evt.run.<kind> message, seq included. ValueError when it is off-contract (the
        seq is not used up)."""
        cmd, seq = self.cmd, self.seq + 1
        trace = {
            k: v
            for k, v in cmd["trace"].items()
            if k in ("traceparent", "tracestate", "correlation_id")
        }
        msg = {
            "type": f"evt.run.{kind}",
            "version": 1,
            "id": str(uuid.uuid4()),
            "job_id": cmd["job_id"],
            "owner_id": cmd["owner_id"],
            "occurred_at": now(),
            "trace": trace,
            "data": {"seq": seq, "attempt": cmd["data"].get("attempt", 1)} | data,
        }
        if errs := schemas.message_errors(msg):
            raise ValueError(f"evt.run.{kind} is off-contract: {'; '.join(errs[:3])}")
        self.seq = seq
        return msg

    async def publish(self, kind: str, data: dict) -> dict:
        """Send evt.run.<kind> and wait for the stream's ack. Returns the message sent."""
        msg = self.build(kind, data)
        subject = f"evt.run.{self.cmd['data']['session_id']}.{kind}"
        body, headers = (
            json.dumps(msg).encode(),
            {"Nats-Msg-Id": f"{msg['job_id']}:{msg['data']['attempt']}:{msg['data']['seq']}"},
        )
        loop = asyncio.get_running_loop()
        deadline = loop.time() + RETRY_FOR
        while True:
            try:
                await self.js.publish(subject, body, headers=headers)
                return msg
            except Exception:
                if loop.time() + RETRY_EVERY > deadline:
                    raise
                await asyncio.sleep(RETRY_EVERY)

    async def status(
        self, status: str, error: str | None = None, resolved: dict | None = None
    ) -> dict:
        """running (with the host), succeeded (with the resolved values), failed (with a plain error) or
        cancelled."""
        data: dict = {"status": status}
        if status == "running":
            data["host"] = HOST
        if error:
            data["error"] = error[:2000]
        if resolved:
            data["resolved"] = resolved
        return await self.publish("status", data)

    async def progress(
        self, node: str, step: int, of: int, phase: str | None = None, eta_s: float | None = None
    ) -> dict:
        """One step of a stepping node done; phase says what it is doing (loading transformer, sampling)."""
        data = {"node": node, "step": step, "of": of}
        if phase:
            data["phase"] = phase[:80]
        if eta_s is not None:
            data["eta_s"] = eta_s
        return await self.publish("progress", data)

    async def output(self, **data) -> dict:
        """One output stored: index, key, kind, width, height, bytes, seed."""
        return await self.publish("output", {k: v for k, v in data.items() if v is not None})
