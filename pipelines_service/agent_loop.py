"""The Agent node as plain LangGraph nodes (plan 16.2, 16.9; contracts/README.md "Agent node", "Tools", "The director
and its chat"): no prebuilt agent. Per visit (a row's agent) or wake (the director's chat):

    <id>.submit → <id>.wait → <id>.tools   repeated per turn, then <id>.route (a row) or reply (the chat)

- `submit` asks one model call for the turn: an ask run (local: the turn schema in `ask.schema`; a server:
  `server-ask` with native `ask.tools`) under `<batch>:<row>:<attempt>:<id>:<visit from 0>:t<turn>` (`r` for the
  one re-ask), or a queued remote call under the same key. It stops the visit at a cap (turns, tokens).
- `wait` only calls interrupt().
- `tools` checks the answer (agents.agent_turn: the schema, the declared tools, route last, the tool calls left,
  set_items before the end) and runs its calls in order as the batch's owner (studio.tools by request/reply,
  idempotent on the call id; memory in pipelines' store), each result kept in the visit's transcript as data. An
  invalid turn runs nothing and is asked once more; a second fails the row.
- `route` takes a declared route (every route but `then` counts against `max`; `then` is forced at the cap), keeps
  the list, or follows the plain edge, and records the visit's decision (kind agent).

The visit's state is `call` (small plain JSON: turns with their calls and results cut to 8000 characters)."""

import uuid

from langgraph.runtime import Runtime
from langgraph.types import interrupt

import agents
import graph
import providers
from messaging.publisher import now
from pipeline import CAPS

MEMORY_WRITES = 4  # per visit or wake


def caps(spec: dict) -> dict:
    return CAPS | spec.get("caps", {})


def _sources(plan, state, spec) -> tuple[list | None, str | None]:
    texts = [graph._text(plan, state, s) for s in spec.get("from", [])]
    if None in texts:
        src = spec["from"][texts.index(None)]
        return None, f"nothing from {src if src.startswith('$') else src.split('.')[0]} yet"
    return list(zip(spec.get("from", []), texts)), None


def _new_call(nid: str, visit: int, sources: list) -> dict:
    own = [t for s, t in sources if s.startswith("$")]  # only the user's texts bring mentions in
    return {
        "node": nid, "visit": visit, "open": True, "turn": 0, "reask": None, "key": None, "run": None,
        "run_ids": [], "result": None, "inputs": {}, "first": None, "sources": sources,
        "allowed": list(dict.fromkeys(h for t in own for h in agents.mentions(t))), "template": agents.AGENT,
        "replay_key": None, "replayed_from": None, "answers": [], "turns": [], "tool_calls": 0,
        "tokens_in": 0, "tokens_out": 0, "seconds": 0, "seen": [], "memory_read": [], "memory_wrote": [],
        "items": None, "items_set": False, "final": None, "stopped": None, "failed": None, "rows": [],
        "notices": [], "network_failures": 0,
    }  # fmt: skip


def _agent_cell(call: dict, status: str, **kw) -> dict:
    agent = {"turns": len(call["turns"]), "tool_calls": call["tool_calls"],
             "tokens": call["tokens_in"] + call["tokens_out"], "items": len(call["items"] or []),
             "route": (call["final"] or {}).get("route")}  # fmt: skip
    return {
        "visit": call["visit"],
        "status": status,
        "run_ids": call["run_ids"],
        "agent": agent,
    } | kw


def submit_part(plan, nid, spec, chat: bool = False):
    src, wf = graph._src(spec), graph._wf(plan, spec)
    remote, local, ask, c = (
        src["kind"] == "remote",
        src["kind"] == "local",
        graph._names(wf)[0],
        caps(spec),
    )
    end = "reply" if chat else f"{nid}.route"

    async def submit(state: graph.Row, runtime: Runtime[graph.Context]) -> graph.Row:
        """One turn's model call (or the end of the visit at a cap); a row's first turn may be replayed."""
        ctx, call, up = runtime.context, state.get("call") or {}, {}
        if not (call.get("node") == nid and call.get("open")):  # a new visit (a row) or wake (the chat)
            if chat:
                visit, sources = state["wake"], []
            else:
                visit = state.get("visits", {}).get(nid, 0) + 1
                up["visits"] = state.get("visits", {}) | {nid: visit}
                sources, error = _sources(plan, state, spec)
                if error:
                    cell = {"visit": visit, "status": "failed", "error": error}
                    return (
                        up
                        | graph._end("failed", f"{nid}: {error}")
                        | {"cells": graph._cell(state, nid, "agent", cell)}
                        | graph._step(state, nid, "submit", visit, "finished", error, error=error)
                    )
            call = _new_call(nid, visit, sources)
        call, visit, turn = dict(call), call["visit"], call["turn"]
        used = call["tokens_in"] + call["tokens_out"]
        if chat:
            prompt = agents.director_prompt(spec, state.get("chat", []), ctx.counts, state.get("woken", []),
                                            call["turns"], turn + 1)  # fmt: skip
        else:
            prompt = agents.agent_prompt(spec, call["sources"], call["turns"], turn + 1)
        if call["reask"]:
            prompt = f"{prompt}\n\n{agents.REASK.format(error=call['reask'])}"[: agents.PROMPT_MAX]
        # the turn is asked only when its prompt's estimate and some output still fit the visit's tokens
        est = providers.estimate(agents.AGENT_SYSTEM + prompt)
        left = max(16, min(4096, c["max_tokens"] - used - est))
        inputs = {
            f"{ask}.system": agents.AGENT_SYSTEM,
            f"{ask}.prompt": prompt,
            f"{ask}.max_tokens": left,
        }
        if local:
            inputs[f"{ask}.schema"] = agents.canonical(agents.turn_schema(spec, chat))
            inputs |= graph._model_values(plan, wf, state.get("last_step"))
        else:
            inputs[f"{ask}.tools"] = agents.compact(agents.native_tools(spec, chat))
        inputs = graph._with_source(spec, inputs, ask)
        if not chat and turn == 0 and not call["reask"]:
            call["first"] = inputs
            h = agents.inputs_hash({"workflow": spec["workflow"], "template": agents.AGENT, "inputs": inputs})
            tail = f"{state['row']}:{nid}:{visit}:{h}"
            call["replay_key"] = f"{state['batch_id']}:{tail}"
            stored = (
                ctx.replay
                and ctx.replay_batch
                and state["attempt"] == 1
                and not remote  # remote answers are never treated as reproducible
                and await ctx.replay(f"{ctx.replay_batch}:{tail}")
            )
            if stored and stored.get("agent"):  # its end is reused; its tool calls are not run again
                a = stored["agent"]
                call |= {"replayed_from": stored["id"], "open": False, "inputs": inputs, "items": a["items"] or None,
                         "final": {"route": a["route"], "say": a["say"]}}  # fmt: skip
                return (
                    up
                    | {"call": call, "to": end}
                    | graph._step(state, nid, "submit", visit, "finished",
                                  f"Replayed decision {stored['id']}; no model asked, no tool run again",
                                  changed=[f"call.{nid} = replayed"])
                )  # fmt: skip
        stop = "turns" if turn >= c["max_turns"] else "tokens" if used + est + 16 > c["max_tokens"] else None
        if stop:
            return (
                up
                | {
                    "call": call | {"stopped": stop, "open": False, "inputs": call["inputs"] or inputs},
                    "to": end,
                }
                | graph._step(
                    state,
                    nid,
                    "submit",
                    visit,
                    "finished",
                    f"Stopped at {stop}",
                    turn=min(max(turn, 1), 16),
                )
            )
        key = f"{state['batch_id']}:{state['row']}:{state['attempt']}:{nid}:{visit - 1}:t{turn}"
        key += "r" if call["reask"] else ""
        run, result = await graph.call_model(ctx, state, spec, inputs, key)
        ids = (call["run_ids"] + ([run] if run and not remote else []))[-64:]
        call |= {
            "run": run,
            "run_ids": ids,
            "result": result,
            "inputs": inputs,
            "key": key,
            "remote": remote,
        }
        out = up | {"call": call, "to": f"{nid}.wait"}
        if not chat:
            out["cells"] = graph._cell(state, nid, "agent", _agent_cell(call, "running"))
            out["run_id"] = run if run and not remote else state.get("run_id")
        what = f"Turn {turn + 1}: asked {wf['title']}" + (
            " once more (the answer was invalid)" if call["reask"] else ""
        )
        return out | graph._step(state, nid, "submit", visit, "finished", what, turn=turn + 1,
                                 run_ids=ids[-1:], changed=[f"agent.{nid}.turn = {turn + 1}"])  # fmt: skip

    return submit


def wait_part(nid):
    def wait(state: graph.Row) -> graph.Row:
        """Park until the turn's call has ended (resumed with {run id or call key: result})."""
        call = state["call"]
        pending = graph.parked_on(call)
        got = interrupt(pending) if pending else {}
        result = call["result"] or got.get(call["run"]) or graph.MISSING
        what = {"succeeded": "answered", "failed": "the call failed", "cancelled": "cancelled"}[result["status"]]
        what = f"Parked, then {what}" if pending else what.capitalize()
        return {"call": call | {"result": result}, "to": f"{nid}.tools"} | graph._step(
            state, nid, "wait", call["visit"], "resumed" if pending else "finished", what,
            turn=call["turn"] + 1, run_ids=call["run_ids"][-1:],
        )  # fmt: skip

    return wait


def _turn_of(t: dict, local: bool) -> dict:
    """The model's answer as a turn: a local ask's JSON object, or a native answer normalised."""
    if not local:
        native = [{"function": {"name": x["name"], "arguments": x["arguments"]}} for x in t.get("tool_calls") or []]
        return agents.normalise_native({"content": t["text"], "tool_calls": native})
    value, err = agents.parse(t["text"], {"type": "object"})
    return value if err is None else {"error": err}


async def run_tool(state, ctx, spec, call: dict, c: dict, cid: str, chat: bool) -> tuple[dict | None, str | None]:
    """One call as the batch's owner: (result, None) or (None, the refusal told to the model). Mutates `call`
    (items, memory read and written, rows touched, notices)."""
    w, args, row = c["tool"], c["args"], 0 if chat else state["row"]
    if w == "route":
        return {"to": args["to"]}, None
    if w == "set_items":
        bad = [h for t in args["items"] for h in agents.mentions(t) if h not in call["allowed"]]
        if bad:
            return None, f"an item mentions @{bad[0]}, which the texts it is written from do not"
        call["items"], call["items_set"] = list(args["items"]), True
        return {"items": len(args["items"])}, None
    if w == "memory_read":  # a row reads the batch's snapshot; only the director's chat reads live
        src = await ctx.memory_live() if chat and ctx.memory_live else ctx.memory
        found = [i for i in src if args.get("kind") in (None, i["kind"]) and args.get("key") in (None, i["key"])]
        found = found[: args.get("limit", 8)]
        call["memory_read"] = (call["memory_read"] + [i["id"] for i in found])[-32:]
        items = [{k: i[k] for k in ("id", "kind", "key", "text", "created_at")} for i in found]
        return {"items": items, "from": "live" if chat else "snapshot"}, None
    if w == "memory_write":
        if len(call["memory_wrote"]) >= MEMORY_WRITES:
            return None, f"at most {MEMORY_WRITES} memory writes per {'wake' if chat else 'visit'}"
        if ctx.put_note is None:
            return None, "this batch has no pipeline memory"
        iid = str(uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:memory:{cid}"))  # a re-run overwrites it
        source = {"kind": "node", "batch_id": state["batch_id"], "row": None if chat else row,
                  "node": call["node"], "visit": call["visit"]}  # fmt: skip
        value = {"kind": "note", "key": args["key"], "text": args["text"], "kept": [], "rejected": [],
                 "agreed": None, "source": source}  # fmt: skip
        if why := await ctx.put_note(iid, value):
            return None, why
        call["memory_wrote"].append(iid)
        return {"id": iid}, None
    if w == "rows_edit" and ctx.row_lock:
        # held from the started check to the stored inputs: the row cannot start in between
        async with ctx.row_lock(args["row"]):
            return await _studio_tool(state, ctx, call, w, args, row, cid)
    return await _studio_tool(state, ctx, call, w, args, row, cid)


async def _studio_tool(state, ctx, call: dict, w: str, args: dict, row: int, cid: str):
    if w == "rows_edit" and ctx.row_started and await ctx.row_started(args["row"]):
        err = f"row {args['row']} has started; only rows not yet started can change"
        call["notices"].append(f"Row {args['row']} was not changed: {err}")
        return None, err
    if ctx.tool is None:
        return None, "this tool is not available here"
    req = {"owner_id": ctx.owner_id, "batch_id": state["batch_id"], "row": row, "node": call["node"],
           "call_id": cid, "tool": agents.DOTTED[w], "args": args}  # fmt: skip
    reply = await ctx.tool(req)
    if "error" in reply:
        if w == "rows_add":
            n = await ctx.next_row() if ctx.next_row else None
            call["notices"].append(f"{f'Row {n}' if n else 'A row'} was not added: {reply['error']}")
        elif w == "rows_edit":
            call["notices"].append(f"Row {args['row']} was not changed: {reply['error']}")
        return None, reply["error"]
    if reply.get("row"):
        if ctx.row_added:
            await ctx.row_added(reply["row"])
        call["rows"] = (call["rows"] + [reply["row"]["row"]])[-64:]
    return reply["result"], None


def tools_part(plan, nid, spec, chat: bool = False):
    src = graph._src(spec)
    local, c = src["kind"] == "local", caps(spec)
    end = "reply" if chat else f"{nid}.route"

    async def tools(state: graph.Row, runtime: Runtime[graph.Context]) -> graph.Row:
        """Check the turn and run its calls (nothing runs for an invalid turn: it is asked once more)."""
        ctx, call = runtime.context, dict(state["call"])
        res, visit, turn_no = call["result"] or graph.MISSING, call["visit"], call["turn"] + 1
        answer = res.get("answer") or {}
        tries = [dict(t) for t in answer.get("tries") or []]
        call["tokens_in"] += answer.get("tokens_in", 0)
        call["tokens_out"] += answer.get("tokens_out", 0)
        call["seconds"] += answer.get("seconds", 0)
        call["network_failures"] += res.get("network_failures", 0)
        step = lambda what, **kw: graph._step(state, nid, "tools", visit, "finished", what, turn=turn_no, **kw)
        err = None
        if call["tokens_in"] + call["tokens_out"] > c["max_tokens"]:  # reported tokens past the cap
            call |= {
                "open": False,
                "result": None,
                "stopped": "tokens",
                "answers": (call["answers"] + tries)[-64:],
            }
            return {"call": call, "to": end} | step("Stopped at tokens")
        # a failed run with tries (the runner's schema check refused them) is an invalid turn: asked once more
        answer_failed = res.get("kind") == "answer" or bool(res["status"] == "failed" and tries)
        if res["status"] == "cancelled" or (res["status"] == "failed" and not answer_failed):
            call |= {"open": False, "result": None, "answers": (call["answers"] + tries)[-64:]}
            if res.get("kind") == "cap":
                call["stopped"] = "remote_cap"
            call["failed"] = "cancelled" if res["status"] == "cancelled" else res.get("error") or "the call failed"
            return {"call": call, "to": end} | step(
                f"The call {'was cancelled' if res['status'] == 'cancelled' else 'failed'}"
            )
        if res["status"] == "failed":
            err = res.get("error") or "the answer was refused"
        elif not tries:
            err = "the call gave no answer"
        elif tries[-1].get("error"):  # a native answer refused as it came (too many tool calls)
            err = tries[-1]["error"]
        else:
            turn = _turn_of(tries[-1], local)
            calls_left = c["max_tool_calls"] - call["tool_calls"]
            r = agents.agent_turn(turn, spec, calls_left, call["items_set"], chat)
            if "error" in r:
                err = r["error"]
                work = [x for x in turn.get("calls") or [] if x.get("tool") != "route"]
                if calls_left <= 0 and work:
                    call["stopped"] = "tool_calls"
        if err:
            if tries:
                tries[-1]["error"] = err[:2000]
            call["answers"] = (call["answers"] + tries)[-64:]
            if call["stopped"] or call["reask"]:  # no tool calls left, or the second invalid answer
                call |= {"open": False, "failed": None if call["stopped"] else err, "result": None}
                return {"call": call, "to": end} | step(f"Answer invalid again: {err}", error=err)
            call |= {"reask": err, "result": None}
            return {"call": call, "to": f"{nid}.submit"} | step(f"Answer invalid, asking once more: {err}", error=err)
        call["answers"] = (call["answers"] + tries)[-64:]
        r0, w0, done = len(call["memory_read"]), len(call["memory_wrote"]), []
        for i, x in enumerate(r["calls"]):
            cid = f"{call['key']}:{i}"
            got, error = await run_tool(state, ctx, spec, call, x, cid, chat)
            done.append({"id": cid, "tool": x["tool"], "args": x["args"],
                         "result": agents.cut_result(got) if error is None else None,
                         "error": error[:2000] if error else None})  # fmt: skip
        call["tool_calls"] += sum(x["tool"] != "route" for x in r["calls"])
        call["turns"] = (call["turns"] + [{"turn": turn_no, "say": turn["say"][:4000], "calls": done,
                         "tokens_in": answer.get("tokens_in", 0), "tokens_out": answer.get("tokens_out", 0)}])[-16:]  # fmt: skip
        call |= {"turn": turn_no, "reask": None, "result": None}
        if r["final"]:
            call |= {"open": False, "final": {"route": r["route"], "say": turn["say"][:4000]}}
        names = ", ".join(agents.DOTTED.get(x["tool"], x["tool"]) for x in done) or "no tools"
        what = f"{len(done)} tool call{'s' * (len(done) != 1)}: {names}" if done else "Answered"
        mem = {"read": call["memory_read"][r0:], "wrote": call["memory_wrote"][w0:]}
        return {"call": call, "to": end if not call["open"] else f"{nid}.submit"} | step(
            what, changed=[f"agent.{nid}.turns = {turn_no}"], tool_calls=done[:4],
            **({"memory": mem} if mem["read"] or mem["wrote"] else {}),
        )  # fmt: skip

    return tools


def route_part(plan, nid, spec):
    def route(state: graph.Row) -> graph.Row:
        """The visit's end: a declared route (capped; `then` forced at the cap), the list, or the plain edge;
        a cap, a failed call or a second invalid answer fails the row. Records the visit's decision."""
        call = state["call"]
        visit, final = call["visit"], call["final"] or {}
        agent = {"turns": call["turns"], "route": None, "to": None, "forced": False,
                 "items": (call["items"] or [])[:24], "say": final.get("say", ""), "stopped": call["stopped"],
                 "tool_calls": call["tool_calls"], "tokens": call["tokens_in"] + call["tokens_out"]}  # fmt: skip
        dcall = call | {"inputs": call["inputs"] or call["first"] or {}}
        error = None
        if call["stopped"] == "remote_cap":
            error = call["failed"]
        elif call["stopped"]:
            error = f"stopped at {call['stopped']}"
        elif call["failed"]:
            error = call["failed"]
        elif spec.get("list") and not call["items"]:
            error = "no list was set"
        if error:
            dec = graph._decision(plan, state, spec, dcall, error=error, agent=agent)
            cell = _agent_cell(call, "cancelled" if error == "cancelled" else "failed", error=error,
                               decision_id=dec["id"])  # fmt: skip
            status = "cancelled" if error == "cancelled" else "failed"
            return (
                graph._end(status, None if status == "cancelled" else f"{nid}: {error}")
                | {"cells": graph._cell(state, nid, "agent", cell)}
                | graph._step(state, nid, "route", visit, "finished", f"Failed: {error}", error=error, decision=dec)
            )
        up, loops, changed = {}, dict(state.get("loops") or {}), []
        if spec.get("routes"):
            r = plan["routes"][nid]
            turns, label = loops.get(nid, 0), final.get("route") or r["then"]
            forced = label != r["then"] and turns >= r["max"]
            if forced:
                label = r["then"]
            elif label != r["then"]:
                loops[nid] = turns + 1
                changed.append(f"loops.{nid} = {turns + 1}")
            to = r["routes"][label]
            agent |= {"route": label, "to": to, "forced": forced}
            nxt, edge = graph._go(plan, nid, to, "then" if forced else "next")
            what = f"Route {label}, on to {to}" + (f" (forced: {r['max']} of {r['max']} turns used)" if forced else "")
        else:
            nxt, edge = graph._go(plan, nid, plan["next"].get(nid), "next")
            what = f"Finished after {len(call['turns'])} turn{'s' * (len(call['turns']) != 1)}"
        if call["items"]:
            up["items"] = (state.get("items") or {}) | {nid: call["items"]}
            changed.append(f"items.{nid} = {len(call['items'])}")
            what += f" with a list of {len(call['items'])}"
        dec = graph._decision(plan, state, spec, dcall, agent=agent)
        cell = _agent_cell(call | {"final": final | {"route": agent["route"]}}, "succeeded", decision_id=dec["id"])
        if agent["route"]:  # as a decide's cell (BatchCellAttempt.route)
            cell["route"] = {"label": agent["route"], "to": agent["to"]}
        return (
            up
            | nxt
            | {"loops": loops, "cells": graph._cell(state, nid, "agent", cell)}
            | graph._step(state, nid, "route", visit, "finished", what, changed=changed, edge=edge, decision=dec)
        )

    return route


# --- the director's chat: its own thread <batch>:director, one wake per ainvoke


def wake_part(nid):
    def wake(state: graph.Row) -> graph.Row:
        """A wake: the user messages (once each) and the rows that ended since the last one."""
        inbox, seen = state.get("inbox") or {}, {m["id"] for m in state.get("chat") or []}
        msgs = [m for m in inbox.get("messages", []) if m["message_id"] not in seen]
        chat = (state.get("chat") or []) + [{"id": m["message_id"], "role": "user", "text": m["text"],
                                             "at": m["at"]} for m in msgs]  # fmt: skip
        return {"wake": state.get("wake", 0) + 1, "chat": chat[-500:], "woken": inbox.get("rows", [])[-64:],
                "reply_to": [m["message_id"] for m in msgs][-32:], "inbox": {}, "call": None, "outbox": [],
                "consumed": inbox.get("ids", []),
                "to": f"{nid}.submit"}  # fmt: skip

    return wake


def reply_part(nid):
    def reply(state: graph.Row) -> graph.Row:
        """The wake's messages (evt.pipeline.chat): a notice per rejected row and for a cap, then the reply.
        Ids are uuid5 of the wake, so a republish replaces, never duplicates."""
        call, wake, at = state["call"], state["wake"], now()
        mid = lambda k: str(uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:chat:{state['batch_id']}:{wake}:{k}"))
        base = {
            "wake": wake,
            "rows": call["rows"][-64:],
            "reply_to": state.get("reply_to", []),
            "at": at,
        }
        texts = [("notice", n) for n in call["notices"]]
        if call["stopped"]:
            texts.append(("notice", f"The director stopped at its {call['stopped'].replace('_', ' ')} cap."))
        elif call["failed"]:
            texts.append(("notice", f"The director could not answer: {call['failed']}"))
        say = (call["final"] or {}).get("say") or (call["turns"][-1]["say"] if call["turns"] else "")
        if say:
            texts.append(("director", say))
        calls = [x for t in call["turns"] for x in t["calls"]][:64]
        out = [base | {"id": mid(k), "role": role, "text": text[:4000],
                       "tool_calls": calls if role == "director" else []}
               for k, (role, text) in enumerate(texts)]  # fmt: skip
        chat = (state.get("chat") or []) + [{"id": m["id"], "role": m["role"], "text": m["text"], "at": at}
                                             for m in out]  # fmt: skip
        return {
            "chat": chat[-500:],
            "outbox": out,
            "call": call | {"open": False},
            "to": None,
        } | graph._step(state, nid, "route", call["visit"], "finished", f"Replied ({len(out)} messages)")

    return reply
