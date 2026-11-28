"""The service's NATS events. `envelope` builds one and checks it against the contract; `send` publishes it with a
Nats-Msg-Id, waiting for JetStream's ack, retried for up to RETRY_FOR seconds (a duplicate is dropped by its id).

evt.pipeline.<batch_id>.{batch,row}: each the whole state, with a per-batch `seq` the engine has already committed
(batches.seq), published with Nats-Msg-Id <batch_id>:<seq>. A publish that still fails raises: the state is in the
database and is published again on start and Resume."""

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


def envelope(kind: str, job_id: str, owner_id: str, trace: dict, data: dict, msg_id: str | None = None) -> dict:
    """The `kind` event (id random, or fixed by `msg_id`); ValueError when off-contract."""
    msg = {
        "type": kind,
        "version": 1,
        "id": str(uuid.uuid4() if msg_id is None else uuid.uuid5(uuid.NAMESPACE_URL, msg_id)),
        "job_id": str(job_id),
        "owner_id": str(owner_id),
        "occurred_at": now(),
        "trace": {k: v for k, v in trace.items() if k in ("traceparent", "tracestate", "correlation_id")},
        "data": data,
    }
    if errs := schemas.message_errors(msg):
        raise ValueError(f"{kind} is off-contract: {'; '.join(errs[:3])}")
    return msg


async def send(js, subject: str, msg: dict, msg_id: str) -> None:
    """Publish `msg` on `subject` with Nats-Msg-Id `msg_id` and wait for the ack, retrying for up to RETRY_FOR s."""
    body, headers = json.dumps(msg, ensure_ascii=False).encode(), {"Nats-Msg-Id": msg_id}
    loop = asyncio.get_running_loop()
    deadline = loop.time() + RETRY_FOR
    while True:
        try:
            await js.publish(subject, body, headers=headers)
            return
        except Exception:
            if loop.time() + RETRY_EVERY > deadline:
                raise
            await asyncio.sleep(RETRY_EVERY)


class Publisher:
    """Publishes contract-checked pipeline events to JetStream."""

    def __init__(self, js) -> None:
        self.js = js

    async def publish(self, kind: str, batch: dict, data: dict) -> dict:
        """Send evt.pipeline.<kind> (data carries its seq) and wait for the ack. Returns the message."""
        msg = envelope(f"evt.pipeline.{kind}", batch["batch_id"], batch["owner_id"], batch["trace"], data)
        await send(self.js, f"evt.pipeline.{msg['job_id']}.{kind}", msg, f"{msg['job_id']}:{data['seq']}")
        return msg
