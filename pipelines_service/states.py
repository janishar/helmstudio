"""pipelines.state.get (core request/reply, queue group `pipelines`, bare JSON): one row's state at one
checkpoint, for studio's GET …/rows/{row}/execution/{step}/state. Only the batch's owner and that row's thread;
anything else, a thread already deleted or an off-contract request is `not_found`; an error is `internal`."""

import json
import logging

from messaging import schemas

log = logging.getLogger(__name__)

SUBJECT = "pipelines.state.get"


async def answer(engine, body: bytes) -> dict:
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or schemas.state_get_errors("request", req):
        return {"error": "not a pipelines.state.get request", "code": "not_found"}
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

    async def handle(msg) -> None:
        await msg.respond(json.dumps(await answer(engine, msg.data)).encode())

    return await nc.subscribe(SUBJECT, queue="pipelines", cb=handle)
