"""evt.pipeline.<batch_id>.{batch,row}: each the whole state, with a per-batch `seq` the engine has already
committed (batches.seq). Checked against the contract before it leaves; published with Nats-Msg-Id
<batch_id>:<seq>, waiting for JetStream's ack, retried for up to RETRY_FOR seconds (a duplicate is dropped by its
id). A publish that still fails raises: the state is in the database and is published again on start and Resume."""

import asyncio
import json
import uuid
from datetime import UTC, datetime

from messaging import schemas

RETRY_FOR = 10.0  # seconds a publish is retried
RETRY_EVERY = 0.5  # seconds between tries


def now() -> str:
    """The current UTC time as RFC 3339 with a Z suffix."""
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


def build(kind: str, batch: dict, data: dict) -> dict:
    """evt.pipeline.<kind> for `batch` ({batch_id, owner_id, trace}); ValueError when off-contract."""
    trace = {k: v for k, v in batch["trace"].items() if k in ("traceparent", "tracestate", "correlation_id")}
    msg = {
        "type": f"evt.pipeline.{kind}",
        "version": 1,
        "id": str(uuid.uuid4()),
        "job_id": str(batch["batch_id"]),
        "owner_id": str(batch["owner_id"]),
        "occurred_at": now(),
        "trace": trace,
        "data": data,
    }
    if errs := schemas.message_errors(msg):
        raise ValueError(f"evt.pipeline.{kind} is off-contract: {'; '.join(errs[:3])}")
    return msg


class Publisher:
    """Publishes contract-checked pipeline events to JetStream."""

    def __init__(self, js) -> None:
        self.js = js

    async def publish(self, kind: str, batch: dict, data: dict) -> dict:
        """Send evt.pipeline.<kind> (data carries its seq) and wait for the ack. Returns the message."""
        msg = build(kind, batch, data)
        subject = f"evt.pipeline.{msg['job_id']}.{kind}"
        body = json.dumps(msg).encode()
        headers = {"Nats-Msg-Id": f"{msg['job_id']}:{data['seq']}"}
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
