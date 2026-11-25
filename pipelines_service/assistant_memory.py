"""The assistant's memory (plan 18, 22.8; contracts/README.md "Assistant"): LangGraph's Postgres store in
`pipelines`, namespace (<owner_id>, "assistant", <kind>), kinds preference, project, worked and summary (one per
chat). At most MAX_ITEMS items an owner (when full the oldest summary goes first), TEXT_MAX characters an item
(SUMMARY_MAX a summary). Every value is {kind, text, thread_id}; the store keeps the key (the item id) and times.
From API 1.69 a project item may also hold a film's shot list (`shots`: {film, shots}), one per film title (contracts/
README.md "Assistant: shot list"); its text is the list's summary, rewritten at each save_shots.

Writes are keyed so a replay does nothing twice: a `remember` by its call id, a summary by its chat. Only the
owner's namespace is ever read: another user's item id is simply not found.

Also the `pipelines.assistant.memory` responder (core request/reply, queue group `pipelines`, bare JSON) for
/v1/studio/assistant/memory: list, edit, delete, clear."""

import asyncio
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
SHOTS_MAX = 60  # shots a list
LISTS_MAX = 8  # lists shot_list answers
SHOTS_PROMPT_MAX = 3000  # characters of shot lists put in every chat
LABELS = {"preference": "Like", "project": "Project", "worked": "Worked", "summary": "Chat"}
NOT_FOUND = {"error": "memory item not found", "code": "not_found"}
# save_shots and a user's edit read an item and write it back whole: one at a time, so neither loses the other's
# ponytail: one lock a process; replicas in the `pipelines` queue group can still race (compare updated_at then)
_WRITE = asyncio.Lock()


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
            "created_at": _time(it.created_at), "updated_at": _time(it.updated_at)} | (
            {"shots": v["shots"]} if v.get("shots") else {})  # fmt: skip


def _value(i: dict, **change) -> dict:
    """An item's stored value (its shots kept), with `change` applied."""
    return {k: i[k] for k in ("kind", "text", "thread_id", "shots") if k in i} | change


async def items(store, owner_id, kind: str | None = None) -> list[dict]:
    """The owner's items as memoryItems, newest first."""
    found = await store.asearch(
        ns(owner_id, kind) if kind else (str(owner_id), "assistant"), limit=MAX_ITEMS + 50
    )
    out = [_item(it) for it in found if it.namespace[2] in KINDS]
    return sorted(out, key=lambda i: (i["created_at"], i["id"]), reverse=True)


async def find(store, owner_id, iid: str) -> dict | None:
    return next((i for i in await items(store, owner_id) if i["id"] == iid), None)


async def put(store, owner_id, kind: str, iid: str, text: str, thread_id, shots=None) -> str | None:
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
    await store.aput(
        ns(owner_id, kind), iid, value | ({"shots": shots} if shots else {}), index=False
    )
    return None


async def delete(store, owner_id, iid: str) -> bool:
    """A user's delete (Memory, or a Forget card approved): the chats read their memories again."""
    it = await find(store, owner_id, iid)
    if it:
        await store.adelete(ns(owner_id, it["kind"]), iid)
        await _mark(store, owner_id)
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


CHANGED = "changed"  # namespace (<owner_id>, "assistant", "changed"), key "at": the user's last Memory change


async def changed(store, owner_id) -> str | None:
    """When the user last changed Memory (edit, delete, Clear all) or a shot list was saved, or None: a chat's frozen memories (API 1.66)
    are read again only after it."""
    it = await store.aget(ns(owner_id, CHANGED), "at")
    return it.value["at"] if it else None


async def _mark(store, owner_id) -> None:
    await store.aput(ns(owner_id, CHANGED), "at", {"at": uuid.uuid4().hex}, index=False)


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
            await store.aput(
                ns(owner_id, i["kind"]), i["id"], _value(i, thread_id=None), index=False
            )


# --- a film's shot list (API 1.69, alpha.2 items 28 and 29)


def _film(film: str) -> str:
    return " ".join(film.split())


def shots_id(film: str) -> str:
    """One list per film title, any case."""
    return str(
        uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:assistant:shots:{_film(film).casefold()}")
    )


def _secs(x: float) -> str:
    return f"{x:g}"


def shots_text(sl: dict) -> str:
    """The list's summary in plain words, at most TEXT_MAX: what Memory › Projects and recall show."""
    shots = sl["shots"]
    took = sum("take_id" in x for x in shots)
    text = (f"Shot list · {sl['film']}: {len(shots)} shot{'s' * (len(shots) != 1)}, {took} with a take. "
            + "; ".join(f"{x['n']} {x['what']}" + (f" ({_secs(x['seconds'])} s)" if "seconds" in x else "")
                        for x in shots))  # fmt: skip
    return text if len(text) <= TEXT_MAX else text[: TEXT_MAX - 1].rstrip() + "…"


async def shot_lists(
    store, owner_id, film: str | None = None, limit: int | None = LISTS_MAX
) -> list[dict]:
    """The owner's shot lists, newest change first (with film: that film's)."""
    out = [{"id": i["id"], "film": i["shots"]["film"], "shots": i["shots"]["shots"], "updated_at": i["updated_at"]}
           for i in await items(store, owner_id, "project") if i.get("shots")]  # fmt: skip
    out.sort(key=lambda sl: sl["updated_at"], reverse=True)
    if film:
        out = [sl for sl in out if sl["id"] == shots_id(film)]
    return out[:limit]


async def save_shots(store, owner_id, a: dict, thread_id) -> tuple[dict | None, str | None]:
    """save_shots: shots by number added or updated (the fields given), `replace` the whole list, `remove` numbers.
    The whole list, or why nothing was saved; every chat reads the lists again (_mark)."""
    async with _WRITE:
        got = await _save_shots(store, owner_id, a, thread_id)
        if got[0]:
            await _mark(store, owner_id)
        return got


async def _save_shots(store, owner_id, a: dict, thread_id) -> tuple[dict | None, str | None]:
    film = _film(a["film"])
    if not film:
        return None, "the film needs a title"
    iid = shots_id(film)
    old = None if a.get("replace") else await find(store, owner_id, iid)
    have = {x["n"]: x for x in ((old or {}).get("shots") or {}).get("shots") or []}
    for x in a.get("shots") or []:
        have[x["n"]] = have.get(x["n"], {}) | x
        if "what" not in have[x["n"]]:
            return None, f"shot {x['n']} is new: say what happens in it"
    for n in a.get("remove") or []:
        have.pop(n, None)
    if not have:
        return None, "the shot list would be empty; give its shots"
    if len(have) > SHOTS_MAX:
        return None, f"at most {SHOTS_MAX} shots in a list"
    sl = {"film": film, "shots": [have[n] for n in sorted(have)]}
    if why := await put(store, owner_id, "project", iid, shots_text(sl), thread_id, shots=sl):
        return None, why
    it = await find(store, owner_id, iid)
    return {"id": iid, "film": film, "shots": sl["shots"], "updated_at": it["updated_at"]}, None


def shots_block(lists: list[dict]) -> str:
    """The lists as every chat reads them: one line a shot, newest list first, at most SHOTS_PROMPT_MAX; the rest
    by title only."""
    out, rest = [], []
    for sl in lists:
        lines = [f"{sl['film']} ({len(sl['shots'])} shots):"] + [
            f"{x['n']}. {x['what']}" + (f" · {_secs(x['seconds'])} s" if "seconds" in x else "")
            + (f' · "{x["line"]}"' if "line" in x else "") + (f" · take [[take:{x['take_id']}]]" if "take_id" in x else "")
            for x in sl["shots"]
        ]  # fmt: skip
        text = "\n".join(lines)
        # a list too long is named only; a smaller one after it may still fit
        if sum(map(len, out)) + len(text) > SHOTS_PROMPT_MAX:
            rest.append(sl["film"])
        else:
            out.append(text)
    if rest:
        out.append("Also (read them with shot_list): " + ", ".join(rest))
    return "\n\n".join(out)


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
        async with _WRITE:
            it = await find(store, owner, req["item_id"])
            if not it:
                return NOT_FOUND
            value = _value(it, text=req["text"][:TEXT_MAX])  # a shot list keeps its shots
            await store.aput(ns(owner, it["kind"]), it["id"], value, index=False)
        await _mark(store, owner)
        return {"item": await find(store, owner, it["id"])}
    if op == "delete":
        if not await delete(store, owner, req["item_id"]):
            return NOT_FOUND
        return {"deleted": 1}
    every = await items(store, owner)  # clear
    for i in every:
        await store.adelete(ns(owner, i["kind"]), i["id"])
    await _mark(store, owner)
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
