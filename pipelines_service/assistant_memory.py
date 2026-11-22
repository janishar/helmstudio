"""The assistant's memory (plan 18, 22.8; contracts/README.md "Assistant"): LangGraph's Postgres store in
`pipelines`, namespace (<owner_id>, "assistant", <kind>), kinds preference, project, worked and summary (one per
chat). At most MAX_ITEMS items an owner (when full the oldest summary goes first), TEXT_MAX characters an item
(SUMMARY_MAX a summary). Every value is {kind, text, thread_id}; the store keeps the key (the item id) and times.

Writes are keyed so a replay does nothing twice: a `remember` by its call id, a summary by its chat. Only the
owner's namespace is ever read: another user's item id is simply not found.

Also the `pipelines.assistant.memory` responder (core request/reply, queue group `pipelines`, bare JSON) for
/v1/studio/assistant/memory: list, edit, delete, clear."""

import json
import logging
import uuid

from messaging import schemas

log = logging.getLogger(__name__)

SUBJECT = "pipelines.assistant.memory"
KINDS = ("preference", "project", "worked", "summary")
MAX_ITEMS = 200
TEXT_MAX = 500
SUMMARY_MAX = 600
PREFERENCES_MAX = 2000  # characters of preferences put in every turn
RECALL_MAX = 8
LABELS = {"preference": "Like", "project": "Project", "worked": "Worked", "summary": "Chat"}
NOT_FOUND = {"error": "memory item not found", "code": "not_found"}


def ns(owner_id, kind: str) -> tuple[str, ...]:
    return (str(owner_id), "assistant", kind)


def item_id(call_id: str) -> str:
    """A `remember` call's item: the same call id, the same item."""
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:assistant:{call_id}"))


def summary_id(thread_id) -> str:
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:assistant:summary:{thread_id}"))


def _time(t) -> str:
    return t.isoformat().replace("+00:00", "Z")


def _item(it) -> dict:
    v = it.value
    return {"id": it.key, "kind": v["kind"], "text": v["text"], "thread_id": v.get("thread_id"),
            "created_at": _time(it.created_at), "updated_at": _time(it.updated_at)}  # fmt: skip


async def items(store, owner_id, kind: str | None = None) -> list[dict]:
    """The owner's items as memoryItems, newest first."""
    found = await store.asearch(
        ns(owner_id, kind) if kind else (str(owner_id), "assistant"), limit=MAX_ITEMS + 50
    )
    out = [_item(it) for it in found if it.namespace[2] in KINDS]
    return sorted(out, key=lambda i: (i["created_at"], i["id"]), reverse=True)


async def find(store, owner_id, iid: str) -> dict | None:
    return next((i for i in await items(store, owner_id) if i["id"] == iid), None)


async def put(store, owner_id, kind: str, iid: str, text: str, thread_id) -> str | None:
    """Write one item (an existing id is overwritten); when full the oldest summary goes first. Else the
    reason it was refused."""
    cap = SUMMARY_MAX if kind == "summary" else TEXT_MAX
    text = text.strip()[:cap]
    if not text:
        return "the text is empty"
    have = await items(store, owner_id)
    if not any(i["id"] == iid for i in have) and len(have) >= MAX_ITEMS:
        summaries = [i for i in have if i["kind"] == "summary"]
        if not summaries:
            return f"memory is full ({MAX_ITEMS} items); delete some in Memory first"
        await store.adelete(ns(owner_id, "summary"), summaries[-1]["id"])
    value = {"kind": kind, "text": text, "thread_id": str(thread_id) if thread_id else None}
    await store.aput(ns(owner_id, kind), iid, value, index=False)
    return None


async def delete(store, owner_id, iid: str) -> bool:
    it = await find(store, owner_id, iid)
    if it:
        await store.adelete(ns(owner_id, it["kind"]), iid)
    return bool(it)


async def preferences(store, owner_id) -> list[str]:
    """The preferences put in every turn, newest first, within PREFERENCES_MAX characters."""
    out, size = [], 0
    for i in await items(store, owner_id, "preference"):
        if size + len(i["text"]) > PREFERENCES_MAX:
            break
        out.append(i["text"])
        size += len(i["text"])
    return out


async def recall(store, owner_id, query: str, kind: str | None = None) -> list[dict]:
    """A plain text match (every word of the query, any case) over the owner's items, at most RECALL_MAX."""
    words = query.lower().split()
    hits = [
        i for i in await items(store, owner_id, kind) if all(w in i["text"].lower() for w in words)
    ]
    return [{k: i[k] for k in ("id", "kind", "text", "thread_id", "updated_at")} for i in hits][
        :RECALL_MAX
    ]


async def drop_thread(store, owner_id, thread_id) -> None:
    """A chat was deleted: its summary goes; the other items it saved stay, their thread_id null."""
    await store.adelete(ns(owner_id, "summary"), summary_id(thread_id))
    for i in await items(store, owner_id):
        if i["thread_id"] == str(thread_id):
            await store.aput(ns(owner_id, i["kind"]), i["id"],
                             {"kind": i["kind"], "text": i["text"], "thread_id": None}, index=False)  # fmt: skip


# --- pipelines.assistant.memory


async def handle(store, req: dict) -> dict:
    owner, op = req["owner_id"], req["op"]
    if op == "list":
        every = await items(store, owner)
        counts = {k: sum(i["kind"] == k for i in every) for k in KINDS}
        return {
            "items": [i for i in every if req.get("kind") in (None, i["kind"])],
            "counts": counts,
        }
    if op == "edit":
        it = await find(store, owner, req["item_id"])
        if not it:
            return NOT_FOUND
        value = {"kind": it["kind"], "text": req["text"][:TEXT_MAX], "thread_id": it["thread_id"]}
        await store.aput(ns(owner, it["kind"]), it["id"], value, index=False)
        return {"item": await find(store, owner, it["id"])}
    if op == "delete":
        return {"deleted": 1} if await delete(store, owner, req["item_id"]) else NOT_FOUND
    every = await items(store, owner)  # clear
    for i in every:
        await store.adelete(ns(owner, i["kind"]), i["id"])
    return {"deleted": len(every)}


async def answer(store, body: bytes) -> dict:
    ref = f"{schemas.BASE}req.pipelines.assistant.memory.schema.json#/$defs/"
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or schemas.errors(ref + "request", req):
        return {"error": f"not a {SUBJECT} request", "code": "invalid"}
    try:
        reply = await handle(store, req)
    except Exception:
        log.exception("%s %s failed", SUBJECT, req["op"])
        return {"error": "could not read or write memory", "code": "internal"}
    if errs := schemas.errors(ref + "reply", reply):
        log.error("%s reply is off-contract: %s", SUBJECT, "; ".join(errs[:3]))
        return {"error": "memory holds an off-contract item", "code": "internal"}
    return reply


async def serve(nc, store):
    """Subscribe the responder; returns the subscription."""

    async def on(msg) -> None:
        await msg.respond(json.dumps(await answer(store, msg.data), ensure_ascii=False).encode())

    return await nc.subscribe(SUBJECT, queue="pipelines", cb=on)
