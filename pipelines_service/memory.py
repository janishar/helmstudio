"""Pipeline memory (plan 16.6, contracts/README.md "Memory"): LangGraph's Postgres store in `pipelines`, namespace
(<owner_id>, "pipeline", <pipeline_id>), at most MAX_ITEMS items, each a memoryItem without its id, pipeline_id and
times (the store keeps those). Kinds: judge_example (a pick with a note) and note (a memory write node, or typed in
the Memory panel). A batch reads the snapshot taken when it starts (`items`, stored on the batch); writes go to the
live namespace, keyed so a re-run overwrites.

Also the `pipelines.memory` responder (core request/reply, queue group `pipelines`, bare JSON) for
/v1/studio/memory: list, create, delete, clear, only inside the request owner's namespace."""

import json
import logging
import uuid

from messaging import schemas
from messaging.responder import request, respond

log = logging.getLogger(__name__)

SUBJECT = "pipelines.memory"
MAX_ITEMS = 500
NOT_FOUND = {"error": "memory item not found", "code": "not_found"}
FULL = {"error": f"memory is full ({MAX_ITEMS} items); delete some first", "code": "full"}
GONE = {"error": "the pipeline was deleted", "code": "not_found"}
# a reply under NATS max_payload (1 MB, deploy/nats/nats.conf); a longer list keeps its newest items (`truncated`)
REPLY_MAX = 900_000


def ns(owner_id, pipeline_id) -> tuple[str, ...]:
    return (str(owner_id), "pipeline", str(pipeline_id))


def _tomb(owner_id) -> tuple[str, ...]:
    """Where `clear` marks a deleted pipeline (key: its id), so a write still under way cannot refill it."""
    return (str(owner_id), "deleted")


def _time(t) -> str:
    return t.isoformat().replace("+00:00", "Z")


def _item(it, pipeline_id) -> dict:
    return it.value | {
        "id": it.key,
        "pipeline_id": str(pipeline_id),
        "created_at": _time(it.created_at),
        "updated_at": _time(it.updated_at),
    }


async def items(store, owner_id, pipeline_id) -> list[dict]:
    """Every item of the pipeline's memory as memoryItems, newest first."""
    found = await store.asearch(ns(owner_id, pipeline_id), limit=MAX_ITEMS)
    out = [_item(it, pipeline_id) for it in found]
    return sorted(out, key=lambda i: (i["created_at"], i["id"]), reverse=True)


async def put(store, owner_id, pipeline_id, item_id: str, value: dict) -> dict | None:
    """Write one item (an existing id is overwritten); else the error: FULL, or GONE for a deleted pipeline."""
    space = ns(owner_id, pipeline_id)
    if await store.aget(_tomb(owner_id), str(pipeline_id)):
        return GONE
    if not await store.aget(space, item_id) and len(await items(store, owner_id, pipeline_id)) >= MAX_ITEMS:
        return FULL
    await store.aput(space, item_id, value, index=False)
    return None


def judge_example(batch_id, row: int, parked: dict, pick: dict) -> tuple[str, dict]:
    """A pick with a note at a judge, as an item (one per judge visit of a row: a pick delivered twice is one)."""
    kept = pick.get("take_ids") or []
    iid = uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:pick:{batch_id}:{row}:{pick['gate']}:{pick['visit']}")
    source = {"kind": "pick", "batch_id": str(batch_id), "row": row, "node": pick["gate"]}
    return str(iid), {
        "kind": "judge_example",
        "key": pick["gate"],
        "text": pick["note"],
        "kept": kept,
        "rejected": [t for t in parked["candidates"] if t not in kept],
        "agreed": pick.get("agreed"),
        "source": source | {"visit": pick["visit"]},
    }


def snapshot_counts(snap: list[dict], taken_at) -> dict:
    """evt.pipeline.batch `memory` (defs memorySnapshot)."""
    examples = sum(i["kind"] == "judge_example" for i in snap)
    return {"examples": examples, "notes": len(snap) - examples, "taken_at": _time(taken_at)}


async def handle(store, req: dict) -> dict:
    owner, pid, op = req["owner_id"], req["pipeline_id"], req["op"]
    space = ns(owner, pid)
    if op == "list":
        found = await items(store, owner, pid)
        return {"items": [i for i in found if req.get("kind") in (None, i["kind"])]}
    if op == "create":
        iid, value = str(uuid.uuid4()), {"kind": "note", "key": req.get("key"), "text": req["text"]}
        value |= {"kept": [], "rejected": [], "agreed": None}
        value["source"] = {
            "kind": "user",
            "batch_id": None,
            "row": None,
            "node": None,
            "visit": None,
        }
        if err := await put(store, owner, pid, iid, value):
            return err
        return {"item": _item(await store.aget(space, iid), pid)}
    if op == "delete":
        if not await store.aget(space, req["item_id"]):
            return NOT_FOUND
        await store.adelete(space, req["item_id"])
        return {"deleted": 1}
    # clear: the mark first, so no write lands after it
    await store.aput(_tomb(owner), str(pid), {}, index=False)
    found = await items(store, owner, pid)
    for i in found:
        await store.adelete(space, i["id"])
    return {"deleted": len(found)}


async def answer(store, body: bytes) -> dict:
    req, bad = request(body, schemas.memory_errors, SUBJECT)
    if bad:
        return bad
    try:
        reply = await handle(store, req)
    except Exception:
        log.exception("%s %s failed", SUBJECT, req["op"])
        return {"error": "could not read or write memory", "code": "internal"}
    if errs := schemas.memory_errors("reply", reply):
        log.error("%s reply is off-contract: %s", SUBJECT, "; ".join(errs[:3]))
        return {"error": "memory holds an off-contract item", "code": "internal"}
    return reply


def encode(reply: dict) -> bytes:
    """The reply as UTF-8 JSON within REPLY_MAX: a list too long keeps its newest items and says `truncated`."""
    body = json.dumps(reply, ensure_ascii=False).encode()
    if len(body) <= REPLY_MAX or "items" not in reply:
        return body
    kept, size = [], len(b'{"items":[],"truncated":true}')
    for it in reply["items"]:
        size += len(json.dumps(it, ensure_ascii=False).encode()) + 1
        if size > REPLY_MAX:
            break
        kept.append(it)
    return json.dumps({"items": kept, "truncated": True}, ensure_ascii=False).encode()


async def serve(nc, store):
    """Subscribe the responder; returns the subscription."""
    return await respond(nc, SUBJECT, lambda body: answer(store, body), encode)
