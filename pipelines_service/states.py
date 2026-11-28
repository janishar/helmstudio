"""pipelines.state.get (core request/reply, queue group `pipelines`, bare JSON): one row's state at one
checkpoint, for studio's GET …/rows/{row}/execution/{step}/state. Only the batch's owner and that row's thread;
anything else, a thread already deleted or an off-contract request is `not_found`; an error is `internal`."""

import json
import logging

from messaging import schemas
from messaging.responder import request, respond

log = logging.getLogger(__name__)

SUBJECT = "pipelines.state.get"
REPLY_MAX = 900_000  # under NATS max_payload (1 MB)
TOO_LARGE = {"error": "the row's state is too large to show", "code": "internal"}


def encode(reply: dict) -> bytes:
    body = json.dumps(reply, ensure_ascii=False).encode()
    return body if len(body) <= REPLY_MAX else json.dumps(TOO_LARGE).encode()


async def answer(engine, body: bytes) -> dict:
    req, bad = request(body, schemas.state_get_errors, SUBJECT, "not_found")
    if bad:
        return bad
    try:
        reply = await engine.state_get(req)
    except Exception:
        log.exception("%s %s row %s failed", SUBJECT, req["batch_id"], req["row"])
        return {"error": "could not read the row's state", "code": "internal"}
    if errs := schemas.state_get_errors("reply", reply):
        log.error("%s reply is off-contract: %s", SUBJECT, "; ".join(errs[:3]))
        return {"error": "the row's state is not plain JSON", "code": "internal"}
    return reply


async def serve(nc, engine):
    """Subscribe the responder; returns the subscription."""
    return await respond(nc, SUBJECT, lambda body: answer(engine, body), encode)
