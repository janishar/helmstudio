"""Core request/reply responders (queue group `pipelines`, bare JSON): the request check and the subscription."""

import inspect
import json


def request(body: bytes, check, subject: str, code: str = "invalid") -> tuple[dict | None, dict | None]:
    """(the request, None), or (None, the `{error, code}` reply) when `body` is not JSON or `check("request", req)`
    finds it off-contract."""
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or check("request", req):
        return None, {"error": f"not a {subject} request", "code": code}
    return req, None


def _json(reply: dict) -> bytes:
    return json.dumps(reply, ensure_ascii=False).encode()


async def respond(nc, subject: str, fn, encode=_json):
    """Subscribe `fn(body)` (a reply, or an awaitable of one) on `subject`; returns the subscription."""

    async def on(msg) -> None:
        reply = fn(msg.data)
        await msg.respond(encode(await reply if inspect.isawaitable(reply) else reply))

    return await nc.subscribe(subject, queue="pipelines", cb=on)
