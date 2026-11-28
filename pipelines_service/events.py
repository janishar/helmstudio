"""evt.studio.<session_id>.run_finished from EVT (durable `pipelines-runs`, filter evt.studio.>), so a run that
finished while this service was down is still delivered. Each resumes the thread waiting on that run (the engine
serialises resumes of one thread); one that finds no thread waiting is acked and dropped. A session's naming run
(API 1.43: batch_id null) goes to names.py. Off-contract or other
evt.studio.* messages are termed; an error naks with a delay (redelivered)."""

import json
import logging

from messaging import schemas

log = logging.getLogger(__name__)

TYPE = "evt.studio.run_finished"
ACK_WAIT = 60  # seconds
RETRY_DELAY = 5  # seconds


class RunEvents:
    def __init__(self, engine, names=None) -> None:
        self.engine, self.names = engine, names

    async def handle(self, msg) -> None:
        try:
            evt = json.loads(msg.data)
        except ValueError:
            evt = None
        errs = schemas.message_errors(evt) if isinstance(evt, dict) else ["not a JSON object"]
        if errs or evt["type"] != TYPE:
            log.error("ignoring %s: %s", msg.subject, "; ".join(errs[:3]) or "not run_finished")
            await msg.term()
            return
        d = evt["data"]
        try:
            if d["batch_id"] is None:
                resumed = self.names is not None and await self.names.on_run_finished(d)
            else:
                resumed = await self.engine.on_run_finished(d)
            log.info("run %s (%s) %s", d["run_id"], d["status"], "resumed" if resumed else "dropped")
            await msg.ack()
        except Exception:
            log.exception("run_finished %s failed; redelivering", d["run_id"])
            await msg.nak(delay=RETRY_DELAY)
