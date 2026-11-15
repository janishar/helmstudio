"""cmd.pipeline.{submit,resume,pause,stop,retry,pick} from CMD (durable `pipelines`, one at a time, in order).

One `handle(msg)` per delivery: off-contract → copied to dlq.<subject> and termed; already handled (inbox, by
Nats-Msg-Id) → acked; else the engine's handler, the inbox row, ack. Every handler is idempotent too, so a
redelivery after the handler but before the inbox row is harmless. Any error naks with a delay (redelivered, with
no limit: a command is never dropped, however long Postgres or JetStream is down)."""

import json
import logging

from messaging import schemas

log = logging.getLogger(__name__)

TYPES = {f"cmd.pipeline.{k}" for k in ("submit", "resume", "pause", "stop", "retry", "pick")}
ACK_WAIT = 60  # seconds
RETRY_DELAY = 5  # seconds before a failed delivery comes back


class Commands:
    def __init__(self, engine, pool, js) -> None:
        self.engine, self.pool, self.js = engine, pool, js

    async def handle(self, msg) -> None:
        try:
            cmd = json.loads(msg.data)
        except ValueError:
            cmd = None
        errs = schemas.message_errors(cmd) if isinstance(cmd, dict) else ["not a JSON object"]
        if errs or cmd["type"] not in TYPES:
            log.error(
                "rejecting %s: %s", msg.subject, "; ".join(errs[:3]) or "not a pipeline command"
            )
            try:
                await self.js.publish(f"dlq.{msg.subject}", msg.data)
            except Exception:
                log.exception("could not dead-letter a %s message", msg.subject)
            await msg.term()
            return
        msg_id = (msg.headers or {}).get("Nats-Msg-Id") or cmd["id"]
        try:
            if not await self._seen(msg_id):
                await self.dispatch(cmd)
                await self._mark(msg_id)
            await msg.ack()
        except Exception:
            log.exception("%s %s failed; redelivering", cmd["type"], msg_id)
            await msg.nak(delay=RETRY_DELAY)

    async def dispatch(self, cmd: dict) -> None:
        d, kind = cmd["data"], cmd["type"].rsplit(".", 1)[1]
        log.info("%s %s", cmd["type"], d["batch_id"])
        if kind == "submit":
            await self.engine.submit(cmd)
        elif kind == "retry":
            await self.engine.retry(
                d["batch_id"], d["row"], d["attempt"], d.get("from_step"), d.get("checkpoint_id")
            )
        elif kind == "pick":
            if not await self.engine.pick(d):
                log.info("pick for %s row %s dropped: not parked there", d["batch_id"], d["row"])
        else:
            await getattr(self.engine, kind)(d["batch_id"])

    async def _seen(self, msg_id: str) -> bool:
        async with self.pool.connection() as conn:
            cur = await conn.execute("SELECT 1 FROM inbox WHERE msg_id = %s", (msg_id,))
            return await cur.fetchone() is not None

    async def _mark(self, msg_id: str) -> None:
        async with self.pool.connection() as conn:
            await conn.execute(
                "INSERT INTO inbox (msg_id) VALUES (%s) ON CONFLICT DO NOTHING", (msg_id,)
            )
