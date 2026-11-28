"""contracts/check.py, decisions (README, "Decisions").
Moved out of check.py as is; check.py re-exports every name."""

import hashlib
import json

from jsonschema import Draft202012Validator

from check_core import HERE, fail


# ---------------------------------------------------------------- decisions (README, "Decisions")
def judge_verdict(answer: dict, criteria: dict, keep: int, candidates: list[str]) -> dict:
    """The judge's result derived from its parsed answer (README, Agent nodes: Judge), or {error} for the re-ask."""
    must, nice, n = criteria["must"], criteria["nice"], len(candidates)
    takes, ranking = answer["takes"], answer["ranking"]
    if [t["take"] for t in takes] != list(range(1, n + 1)):
        return {"error": f"takes must be 1..{n} in order"}
    if bad := next((t["take"] for t in takes if len(t["checks"]) != len(must) + len(nice)), None):
        return {"error": f"take {bad} needs {len(must) + len(nice)} checks"}
    if sorted(ranking) != list(range(1, n + 1)):
        return {"error": f"ranking must order every take 1..{n} once"}
    m = len(must)
    order = sorted(range(n), key=lambda i: (-sum(takes[i]["checks"][:m]), -sum(takes[i]["checks"][m:]),
                                            ranking.index(i + 1)))
    passes = [all(t["checks"][:m]) for t in takes]
    good = [i for i in order if passes[i]]
    verdict = "pass" if len(good) >= keep else "fail"
    best = takes[order[0]]["checks"]
    return {"verdict": verdict, "picked": [candidates[i] for i in good[:keep]] if verdict == "pass" else [],
            "first_failed": next((must[j] for j in range(m) if not best[j]), None), "passes": passes,
            "ranks": [order.index(i) + 1 for i in range(n)]}


def inputs_hash(call: dict) -> str:
    """sha256 of the call as canonical JSON (sorted keys, no spaces, UTF-8): the replay key's last part."""
    return hashlib.sha256(json.dumps(call, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()


WIRE = {"gallery.search": "gallery_search", "memory.read": "memory_read", "memory.write": "memory_write",
        "library.read": "library_read", "rows.add": "rows_add", "rows.edit": "rows_edit"}
ARGS = {"gallery_search": "gallerySearchArgs", "memory_read": "memoryReadArgs", "memory_write": "memoryWriteArgs",
        "library_read": "libraryReadArgs", "rows_add": "rowsAddArgs", "rows_edit": "rowsEditArgs",
        "route": "routeArgs", "set_items": "setItemsArgs"}


def strip_descriptions(node):
    if isinstance(node, dict):
        return {k: strip_descriptions(v) for k, v in node.items() if k != "description"}
    return [strip_descriptions(x) for x in node] if isinstance(node, list) else node


def offered(node: dict, chat: bool = False) -> list[str]:
    """The wire names an agent turn may call (README, Agents: tools): its tools (rows.edit only in the director's
    chat), route with routes (never in the chat), set_items with a list (never in the chat)."""
    tools = [WIRE[x] for x in node.get("tools", []) if x != "rows.edit" or chat]
    return tools + ([] if chat else (["route"] if node.get("routes") else []) + (["set_items"] if node.get("list") else []))


def turn_schema(node: dict, chat: bool = False) -> dict:
    """The JSON schema of one agent turn (a local ask's `ask.schema`, as canonical JSON: sorted keys, no spaces)."""
    defs = json.loads((HERE / "messages" / "tools.schema.json").read_text())["$defs"]

    def args(w: str) -> dict:
        a = strip_descriptions(defs[ARGS[w]])
        if w == "route":
            a["properties"]["to"]["enum"] = list(node["routes"])
        if w == "set_items":
            a["properties"]["items"]["maxItems"] = node["list"]["max"]
        return a
    names = offered(node, chat)
    calls = {"type": "array", "maxItems": 4 if names else 0}
    if names:
        calls["items"] = {"anyOf": [{"type": "object", "additionalProperties": False, "required": ["tool", "args"],
                                     "properties": {"tool": {"const": w}, "args": args(w)}} for w in names]}
    return {"type": "object", "additionalProperties": False, "required": ["say", "calls"],
            "properties": {"say": {"type": "string", "maxLength": 4000}, "calls": calls}}


def normalise_native(msg: dict) -> dict:
    """A native OpenAI assistant message (a server or remote answer) as a turn, or {error}."""
    calls = []
    for c in msg.get("tool_calls") or []:
        try:
            a = json.loads(c["function"]["arguments"] or "{}")
        except ValueError:
            return {"error": f"tool call {c['function']['name']}: arguments are not JSON"}
        calls.append({"tool": c["function"]["name"], "args": a})
    return {"say": msg.get("content") or "", "calls": calls}


def agent_turn(turn: dict, node: dict, calls_left: int, items_set: bool, chat: bool = False) -> dict:
    """What one turn does (README, Agents: a turn), checked before anything runs: {calls, final, route} or {error}
    (the error is the re-ask's text)."""
    if "error" in turn:
        return turn
    if err := next(Draft202012Validator(turn_schema(node, chat)).iter_errors(turn), None):
        return {"error": f"answer did not match the schema: {err.message[:200]}"}
    calls = turn["calls"]
    routes = [i for i, c in enumerate(calls) if c["tool"] == "route"]
    if len(routes) > 1 or (routes and routes[0] != len(calls) - 1):
        return {"error": "call route once, as the last call"}
    work = [c for c in calls if c["tool"] != "route"]
    if len(work) > calls_left:
        return {"error": f"at most {calls_left} more tool calls in this visit"}
    items_set = items_set or any(c["tool"] == "set_items" for c in calls)
    if node.get("list") and not chat and not items_set and (routes or not calls):
        return {"error": "call set_items before you finish"}
    if not calls and node.get("routes") and not chat:
        return {"error": "call route to finish"}
    return {"calls": calls, "final": bool(routes) or not calls, "route": calls[-1]["args"]["to"] if routes else None}


def check_decisions() -> None:
    g = HERE / "fixtures" / "decisions"
    for c in json.loads((g / "agent-turn.json").read_text())["cases"]:
        turn = normalise_native(c["native"]) if "native" in c else c["answer"]
        got = agent_turn(turn, c["node"], c["calls_left"], c.get("items_set", False), c.get("chat", False))
        want = c["expect"]
        if not (got == want or ("error" in want and got.get("error", "").startswith(want["error"]))):
            fail(f"fixtures/decisions/agent-turn.json {c['description']}: got {got}, want {c['expect']}")
        else:
            print(f"ok   fixtures/decisions/agent-turn.json {c['description']}")
    for c in json.loads((g / "agent-schema.json").read_text())["cases"]:
        got = json.dumps(turn_schema(c["node"], c.get("chat", False)), sort_keys=True, separators=(",", ":"))
        if got != c["expect"]:
            fail(f"fixtures/decisions/agent-schema.json {c['description']}: got {got}")
        else:
            print(f"ok   fixtures/decisions/agent-schema.json {c['description']}")
    for c in json.loads((g / "verdict.json").read_text())["cases"]:
        got = judge_verdict(c["answer"], c["criteria"], c["keep"], c["candidates"])
        if got != c["expect"]:
            fail(f"fixtures/decisions/verdict.json {c['description']}: got {got}, want {c['expect']}")
        else:
            print(f"ok   fixtures/decisions/verdict.json {c['description']}")
    for c in json.loads((g / "replay.json").read_text())["cases"]:
        if (got := inputs_hash(c["call"])) != c["expect"]:
            fail(f"fixtures/decisions/replay.json {c['description']}: got {got}, want {c['expect']}")
        else:
            print(f"ok   fixtures/decisions/replay.json {c['description']}")
