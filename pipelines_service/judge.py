"""The `pipelines.judge.test` responder (API 1.28; contracts/README.md "Judge test"; core request/reply, queue group
`pipelines`, bare JSON): the Judge panel's Test on takes. Studio makes the run; this fills the judge template as a
batch would (the judge's examples and its memory node's notes read now, inside the owner's namespace) and derives the
verdict from the run's answer with the batch's own checks. A failed check is `{error, code: answer}`: a test never
re-asks."""

import logging

import agents
import memory
from messaging import schemas
from messaging.responder import request, respond

log = logging.getLogger(__name__)

SUBJECT = "pipelines.judge.test"


async def prompt(store, req: dict) -> dict:
    crit, n, examples, notes = req["criteria"], req["n"], [], ""
    if mem := req.get("memory"):
        found = await memory.items(store, req["owner_id"], req["pipeline_id"])
        examples = [i for i in found if i["kind"] == "judge_example" and i["key"] == req["node"]]
        examples = examples[: mem["limit"]]
        key = mem.get("key")
        read = [i for i in found if i["kind"] == "note" and key in (None, i["key"])][: mem["limit"]]
        notes = "\n".join(i["text"] for i in read)
    c = len(crit["must"]) + len(crit["nice"])
    return {
        "system": agents.SYSTEM,
        "prompt": agents.judge_prompt(req["brief"], n, crit, examples, notes),
        "schema": agents.compact(agents.judge_schema(n, c)),
        "max_tokens": agents.judge_tokens(n, c),
        "template": agents.JUDGE,
    }


def verdict(req: dict) -> dict:
    crit, cands = req["criteria"], req["take_ids"]
    schema = agents.judge_schema(len(cands), len(crit["must"]) + len(crit["nice"]))
    value, err = agents.parse(req["text"], schema)
    v = value and agents.judge_verdict(value, crit, req["keep"], cands)
    if err or "error" in v:
        return {"error": (err or v["error"])[:2000], "code": "answer"}
    takes = [
        {"take_id": t, "checks": a["checks"], "reason": a["reason"], "passes": v["passes"][i], "rank": v["ranks"][i]}
        for i, (t, a) in enumerate(zip(cands, value["takes"]))
    ]
    return {
        "verdict": v["verdict"],
        "first_failed": v["first_failed"],
        "picked": v["picked"],
        "takes": takes,
    }


async def answer(store, body: bytes) -> dict:
    req, bad = request(body, schemas.judge_test_errors, SUBJECT)
    if bad:
        return bad
    try:
        reply = await prompt(store, req) if req["op"] == "prompt" else verdict(req)
    except Exception:
        log.exception("%s %s failed", SUBJECT, req["op"])
        return {"error": "could not test the judge", "code": "internal"}
    if errs := schemas.judge_test_errors("reply", reply):
        log.error("%s reply is off-contract: %s", SUBJECT, "; ".join(errs[:3]))
        return {"error": "the judge test reply is off-contract", "code": "internal"}
    return reply


async def serve(nc, store):
    """Subscribe the responder; returns the subscription."""
    return await respond(nc, SUBJECT, lambda body: answer(store, body))
