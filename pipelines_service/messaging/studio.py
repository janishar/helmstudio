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

    async def see(self, request: dict) -> dict | None:
        return await see(self.nc, request)

    async def plugin_pass(self, request: dict) -> dict | None:
        return await plugin_pass(self.nc, request)


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
            errs = _reply_errors(request, reply)
            if not errs and reply.get("code") != "internal":
                return reply
            log.warning("%s %s: %s", TOOLS, request["call_id"], errs or reply)
        except (nats.errors.Error, OSError, ValueError) as e:
            log.warning("%s %s: %r", TOOLS, request["call_id"], e)
        await asyncio.sleep(delay)
        delay = min(delay * 2, BACKOFF_MAX)


def _reply_errors(request: dict, reply) -> list[str]:
    """An assistant read's result is checked for its envelope only (`tool` and an object): its shape is
    openapi.yaml's, which pipelines does not carry; the result only ever reaches the model, fenced, as data.
    Everything else is checked whole."""
    if "thread_id" in request and isinstance(reply, dict) and "error" not in reply:
        ok = set(reply) == {"tool", "result"} and reply["tool"] == request["tool"]
        return [] if ok and isinstance(reply["result"], dict) else ["not an assistant tool reply"]
    return schemas.tools_errors("reply", reply)


SEE = "studio.see"


async def see(nc, request: dict) -> dict | None:
    """studio.see (API 1.65): one take's pictures for the assistant's model ({take_id, kind, title, images} or {error,
    code}). Read-only, so asked at most twice; None when no good reply came (the model goes on without them)."""
    if errs := schemas.see_errors("request", request):
        raise ValueError(f"{SEE} request is off-contract: {'; '.join(errs[:3])}")
    for _ in range(2):
        try:
            msg = await nc.request(SEE, json.dumps(request).encode(), timeout=TIMEOUT)
            reply = json.loads(msg.data)
            if not (errs := schemas.see_errors("reply", reply)) and reply.get("code") != "internal":
                return reply
            log.warning("%s %s: %s", SEE, request["take_id"], errs or reply)
        except (nats.errors.Error, OSError, ValueError) as e:
            log.warning("%s %s: %r", SEE, request["take_id"], e)
    return None


PASS = "studio.plugins.pass"


async def plugin_pass(nc, request: dict) -> dict | None:
    """studio.plugins.pass (API 1.70): one plugin tool call's pass ({pass, api} or {error, code}), signed by studio.
    Asked at most twice; None when no good reply came (the call fails with a plain line)."""
    if errs := schemas.plugins_pass_errors("request", request):
        raise ValueError(f"{PASS} request is off-contract: {'; '.join(errs[:3])}")
    for _ in range(2):
        try:
            msg = await nc.request(PASS, json.dumps(request).encode(), timeout=TIMEOUT)
            reply = json.loads(msg.data)
            if (
                not (errs := schemas.plugins_pass_errors("reply", reply))
                and reply.get("code") != "internal"
            ):
                return reply
            log.warning("%s %s: %s", PASS, request["call_id"], errs or reply)
        except (nats.errors.Error, OSError, ValueError) as e:
            log.warning("%s %s: %r", PASS, request["call_id"], e)
    return None
