"""studio.runs.create: core NATS request/reply to studio (bare JSON). Idempotent on the request's
idempotency_key, so a timeout, no responder, an off-contract reply or `code: internal` is simply asked again
with backoff under the same key; `invalid` and `not_found` are final and returned."""

import asyncio
import json
import logging

import nats.errors

from messaging import schemas

log = logging.getLogger(__name__)

SUBJECT = "studio.runs.create"
TIMEOUT = 5.0  # seconds per request (the contract's)
BACKOFF_MAX = 30.0


class Studio:
    """Asks studio for batch runs over `nc`."""

    def __init__(self, nc) -> None:
        self.nc = nc

    async def create_run(self, request: dict) -> dict:
        """The reply ({run_id, status, created, take_ids, error?} or {error, code: invalid|not_found}).
        Retries until it has one; ValueError when the request itself is off-contract."""
        if errs := schemas.runs_create_errors("request", request):
            raise ValueError(f"studio.runs.create request is off-contract: {'; '.join(errs[:3])}")
        body, delay = json.dumps(request).encode(), 0.5
        while True:
            try:
                msg = await self.nc.request(SUBJECT, body, timeout=TIMEOUT)
                reply = json.loads(msg.data)
                errs = schemas.runs_create_errors("reply", reply)
                if not errs and reply.get("code") != "internal":
                    return reply
                log.warning("%s %s: %s", SUBJECT, request["idempotency_key"], errs or reply)
            except (nats.errors.Error, OSError, ValueError) as e:  # timeout, no responder, not JSON
                log.warning("%s %s: %r", SUBJECT, request["idempotency_key"], e)
            await asyncio.sleep(delay)
            delay = min(delay * 2, BACKOFF_MAX)

    async def tool(self, request: dict) -> dict:
        return await tool(self.nc, request)


TOOLS = "studio.tools"


async def tool(nc, request: dict) -> dict:
    """studio.tools: one agent tool call as the batch's owner ({result, row?} or {error, code}). Idempotent on
    call_id, so a timeout, no responder, an off-contract reply or `internal` is asked again with backoff; the
    other codes are final. ValueError when the request itself is off-contract."""
    if errs := schemas.tools_errors("request", request):
        raise ValueError(f"{TOOLS} request is off-contract: {'; '.join(errs[:3])}")
    body, delay = json.dumps(request).encode(), 0.5
    while True:
        try:
            msg = await nc.request(TOOLS, body, timeout=TIMEOUT)
            reply = json.loads(msg.data)
            errs = schemas.tools_errors("reply", reply)
            if not errs and reply.get("code") != "internal":
                return reply
            log.warning("%s %s: %s", TOOLS, request["call_id"], errs or reply)
        except (nats.errors.Error, OSError, ValueError) as e:
            log.warning("%s %s: %r", TOOLS, request["call_id"], e)
        await asyncio.sleep(delay)
        delay = min(delay * 2, BACKOFF_MAX)
