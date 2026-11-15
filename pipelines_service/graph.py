"""A row's graph, built from the batch's plan (pipeline.plan), one thread per row (thread_id "<batch_id>:<row>").

Each pipeline node is one or two graph nodes:

    inputs → <id>                                 the row's inputs (nothing to do but count the step)
    step   → <id>.submit → <id>.wait              submit asks studio.runs.create once per seed under
                                                  <batch>:<row>:<attempt>:<id>:<visit from 0>:<seed index>, records
                                                  the runs; wait only calls interrupt() until every run has ended
    gate   → <id>.gate → <id>.route               gate only calls interrupt() (the row is parked for a pick);
                                                  route applies the pick or the reject and chooses the edge
    output → <id>                                 the row ends succeeded with its `from` takes
    judge  → <id>.submit → .wait → .verdict → [.gate] → .route
    write  → <id>.submit → .wait → .apply
    decide → <id>.submit → .wait → .route         an LLM call (agents.py): submit looks the replay key up (a stored
                                                  decision: no run, on to the check part) or asks one ask run
                                                  under <batch>:<row>:<attempt>:<id>:<visit from 0>:<0 | 1 for the
                                                  re-ask>; wait only calls interrupt(); the check part parses the
                                                  answer (a failed check asks once more), derives the decision and
                                                  routes: a judge parks like a gate in Suggest (its pick suggested)
    memory → <id>                                 reads the batch's memory snapshot, or writes a note

Every graph node sets `to` (the next graph node, None = END) and `last` (what it did, for evt.pipeline.step) and
counts `n` (the attempt's graph steps). The row ends when a node sets `status`. A node that calls interrupt()
does nothing with side effects (a resumed node runs again from its first line). State is small plain JSON."""

import uuid
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import TypedDict

from langgraph.graph import END, START, StateGraph
from langgraph.runtime import Runtime
from langgraph.types import interrupt

import agents
from pipeline import MAX_RUNS

TERMINAL = ("succeeded", "failed", "cancelled")
# ponytail: a rows param's Image field is `image` in every pack today; the snapshot does not name it
IMAGE_FIELD = "image"


class Row(TypedDict, total=False):
    batch_id: str
    row: int
    attempt: int
    inputs: dict  # the row's inputs (a pipeline: `<inputs node>.<name>` → value)
    seeds: dict  # step → the row's base seed
    visits: dict  # node → visits so far
    loops: dict  # gate → fail-edge turns so far
    takes: dict  # step → its takes from its latest visit
    picked: dict  # gate → its latest pick
    runs: list  # the current step visit's runs: [{run_id, seed, result?}]
    pick: dict  # a gate's answer: {take_ids} | {reject: true}
    cells: list  # evt.pipeline.row cells
    n: int  # graph steps so far in this attempt
    last: dict  # the last graph step (evt.pipeline.step data without seq, row, attempt, loops, checkpoint)
    to: str | None
    run_id: str | None  # the latest run
    status: str  # the row's end: succeeded | failed | cancelled
    rejected: bool  # a reviewer's reject decided the row (not a failure streak)
    take_ids: list[str]
    error: str | None
    collection: str | None
    call: dict  # the current LLM call: node, visit, branch, inputs, replay key, runs, answers, result, derived
    reasons: dict  # judge → its reasons (one line per candidate)
    notes: dict  # memory read node → its notes (one per line)
    memread: dict  # memory read node → the item ids it read
    pos: dict  # step → each take's position <step>:<visit>:<seed index>:<seed> (the replay key's media)
    last_step: str  # the step visited last (a write's or decide's family model values)


@dataclass
class Context:
    """Per batch, from the engine's batch record (never from state)."""

    create_run: Callable[[dict], Awaitable[dict]]
    owner_id: str
    session_id: str
    replay: Callable[[str], Awaitable[dict | None]] | None = None  # replay key → stored decision
    replay_batch: str | None = (
        None  # whose decisions are reused: replay_from, else the batch itself
    )
    memory: list = field(
        default_factory=list
    )  # the batch's memory snapshot (memoryItems, newest first)
    put_note: Callable[[str, dict], Awaitable[str | None]] | None = (
        None  # item id, value → None when written, else why not (full, the pipeline deleted)
    )


def entry(plan: dict, nid: str) -> str:
    """The graph node a pipeline node starts at."""
    kind = plan["nodes"][nid]["kind"]
    if kind in ("step", "judge", "write", "decide"):
        return f"{nid}.submit"
    return f"{nid}.gate" if kind == "gate" else nid


def key(state: Row, nid: str, visit: int, i: int) -> str:
    return f"{state['batch_id']}:{state['row']}:{state['attempt']}:{nid}:{visit}:{i}"


def initial(batch_id: str, row: int, attempt: int, inputs: dict, seeds: dict | None = None) -> Row:
    return {
        "batch_id": batch_id,
        "row": row,
        "attempt": attempt,
        "inputs": inputs,
        "seeds": seeds or {},
        "visits": {},
        "loops": {},
        "takes": {},
        "picked": {},
        "cells": [],
        "n": 0,
        "reasons": {},
        "notes": {},
        "memread": {},
        "pos": {},
    }


def _step(state, node, part, visit, status, what, **kw) -> Row:
    """The graph step's record and count."""
    last = {"node": node, "part": part, "visit": visit, "status": status, "what": what[:300]}
    last |= {"changed": [], "edge": None, "run_ids": [], "take_ids": [], "error": None} | kw
    last["run_ids"], last["take_ids"] = last["run_ids"][:8], last["take_ids"][:8]
    if (dec := last.get("decision")) and dec.get("memory_read") and "memory" not in kw:
        last["memory"] = {"read": dec["memory_read"], "wrote": []}  # a judge's examples and notes
    return {"n": state.get("n", 0) + 1, "last": last}


def _cell(state, node, kind, attempt: dict, loop: dict | None = None) -> list:
    """The cells with `attempt` set on `node` (replacing the same visit's), and its loop when given."""
    cells = [c | {"attempts": list(c["attempts"])} for c in state.get("cells", [])]
    c = next((c for c in cells if c["node"] == node), None)
    if c is None:
        cells.append(c := {"node": node, "kind": kind, "attempts": []})
    if loop:
        c["loop"] = loop
    att = {"run_ids": [], "take_ids": [], "seeds": [], "error": None} | attempt
    att["run_ids"], att["take_ids"] = att["run_ids"][:8], att["take_ids"][:8]
    if c["attempts"] and c["attempts"][-1]["visit"] == att["visit"]:
        c["attempts"][-1] = att
    else:
        c["attempts"].append(att)
    return cells


def _end(status: str, error: str | None = None, take_ids=()) -> Row:
    return {"status": status, "error": error, "take_ids": list(take_ids), "to": None}


def _go(plan, frm: str, to: str | None, via: str) -> tuple[Row, dict | None]:
    """The update and the edge for leaving `frm` to `to`; a missing edge fails the row."""
    if to is None:
        return _end("failed", f"nowhere to go from {frm}"), None
    return {"to": entry(plan, to)}, {"to": to, "via": via}


def _source(state, src: str) -> list[str] | None:
    nid, what = src.split(".")
    return (state.get("takes" if what == "takes" else "picked") or {}).get(nid)


def _is_rows(wf: dict, key: str) -> bool:
    wn, p = key.split(".")
    return isinstance(wf["nodes"].get(wn, {}).get("params", {}).get(p), list)


def run_inputs(plan, state, spec) -> tuple[dict | None, str | None]:
    """A run's inputs: (the row's, for a batch of one workflow), the step's fixed inputs, then its binds;
    or the error that fails the row."""
    wf = plan["workflows"][spec["workflow"]]
    out = dict(state["inputs"]) if spec.get("row_inputs") else {}
    out |= spec["inputs"]
    for k, src in spec["bind"].items():
        if src.startswith("$"):
            name = f"{plan['inputs_node']}.{src[1:]}"
            if name not in state["inputs"]:
                return None, f"nothing from {src} yet"
            v = state["inputs"][name]
            if _is_rows(wf, k) and isinstance(v, dict):
                v = [{IMAGE_FIELD: v}]
        else:
            ids = _source(state, src)
            if not ids:
                return None, f"nothing from {src.split('.')[0]} yet"
            rows = _is_rows(wf, k)
            v = [{IMAGE_FIELD: {"take_id": t}} for t in ids] if rows else {"take_id": ids[0]}
        out[k] = v
    return out, None


def _base_seed(state, nid, spec, inputs) -> int | None:
    base = state.get("seeds", {}).get(nid)
    if base is None and spec["seed_param"]:
        v = inputs.get(spec["seed_param"])
        base = v if isinstance(v, int) and not isinstance(v, bool) and v >= 0 else None
    return base


def _parked(plan, state, gid) -> dict:
    """What a gate offers on this visit (the interrupt's value and the row-parked event's `parked`)."""
    g = plan["nodes"][gid]
    visit = state.get("visits", {}).get(gid, 0) + 1
    cands = (state.get("takes") or {}).get(g["takes"], [])[:8]
    return {"gate": gid, "visit": visit, "keep": g.get("keep", 1), "candidates": cands}


def build(plan: dict) -> StateGraph:
    nodes, names = plan["nodes"], []
    g = StateGraph(Row, context_schema=Context)

    def add(name, fn):
        g.add_node(name, fn)
        names.append(name)

    for nid, spec in nodes.items():
        kind = spec["kind"]
        if kind == "inputs":
            add(nid, _inputs_node(plan, nid))
        elif kind == "step":
            add(f"{nid}.submit", _submit_node(plan, nid, spec))
            add(f"{nid}.wait", _wait_node(plan, nid))
        elif kind == "gate":
            add(f"{nid}.gate", _gate_node(plan, nid))
            add(f"{nid}.route", _route_node(plan, nid))
        elif kind in ("judge", "write", "decide"):
            check = CHECK[kind]
            add(f"{nid}.submit", _ask_submit_node(plan, nid, spec, check))
            add(f"{nid}.wait", _ask_wait_node(nid, check))
            add(f"{nid}.{check}", {"judge": _verdict_node, "write": _apply_node,
                                   "decide": _decide_node}[kind](plan, nid, spec))  # fmt: skip
            if kind == "judge":
                add(f"{nid}.gate", _judge_gate_node(nid))
                add(f"{nid}.route", _judge_route_node(plan, nid, spec))
        elif kind == "memory":
            add(nid, _memory_node(plan, nid, spec))
        else:
            add(nid, _output_node(nid, spec))
    g.add_edge(START, entry(plan, plan["start"]))
    for name in names:
        g.add_conditional_edges(name, lambda s: s.get("to") or END, [*names, END])
    return g


def _inputs_node(plan, nid):
    def inputs(state: Row) -> Row:
        visit = state.get("visits", {}).get(nid, 0) + 1
        up, edge = _go(plan, nid, plan["next"].get(nid), "next")
        n = len(state["inputs"])
        return (
            up
            | {"visits": state.get("visits", {}) | {nid: visit}}
            | _step(state, nid, "inputs", visit, "finished", f"Row inputs: {n} value{'s' * (n != 1)}",
                    changed=[f"inputs = {n}"], edge=edge)
        )  # fmt: skip

    return inputs


def _submit_node(plan, nid, spec):
    async def submit(state: Row, runtime: Runtime[Context]) -> Row:
        """Ask studio for this visit's runs (one per seed); a terminal reply is that run's result already."""
        ctx = runtime.context
        visit = state.get("visits", {}).get(nid, 0)  # from 0, as in the key
        up: Row = {"visits": state.get("visits", {}) | {nid: visit + 1}}
        inputs, error = run_inputs(plan, state, spec)
        if error:
            cell = {"visit": visit + 1, "status": "failed", "error": error}
            return (
                up
                | _end("failed", error)
                | {"cells": _cell(state, nid, "step", cell)}
                | _step(state, nid, "submit", visit + 1, "finished", error, error=error)
            )
        sp, n, runs = spec["seed_param"], spec["seeds"], []
        base = _base_seed(state, nid, spec, inputs)
        for i in range(n):
            seed, inp = None, dict(inputs)
            if sp and base is not None:  # each row attempt: its own MAX_RUNS seeds
                seed = inp[sp] = (base + (state["attempt"] - 1) * MAX_RUNS + visit * n + i) % 2**53
            reply = await ctx.create_run(
                {
                    "owner_id": ctx.owner_id,
                    "session_id": ctx.session_id,
                    "workflow_id": spec["workflow"],
                    "inputs": inp,
                    "lane": "batch",
                    "batch_id": state["batch_id"],
                    "row": state["row"],
                    "idempotency_key": key(state, nid, visit, i),
                }
            )
            if "code" in reply:  # invalid | not_found: final; later seeds are not asked
                fail = {"status": "failed", "take_ids": [], "error": reply["error"]}
                runs.append({"run_id": None, "seed": seed, "result": fail})
                break
            run = {"run_id": reply["run_id"], "seed": seed}
            if reply["status"] in TERMINAL:
                run["result"] = {k: reply.get(k) for k in ("status", "take_ids", "error")}
            runs.append(run)
        ids = [r["run_id"] for r in runs if r["run_id"]]
        seeds = [r["seed"] for r in runs if r["seed"] is not None]
        title = plan["workflows"][spec["workflow"]]["title"]
        what = f"{n} run{'s' * (n > 1)} queued for {title}"
        cell = {"visit": visit + 1, "status": "running", "run_ids": ids, "seeds": seeds}
        return (
            up
            | {"runs": runs, "run_id": ids[-1] if ids else state.get("run_id"), "last_step": nid}
            | {"to": f"{nid}.wait", "cells": _cell(state, nid, "step", cell)}
            | _step(state, nid, "submit", visit + 1, "finished", what,
                    changed=[f"runs.{nid} = {len(runs)}"], run_ids=ids)
        )  # fmt: skip

    return submit


def _wait_node(plan, nid):
    def wait(state: Row) -> Row:
        """Park until every run of the visit has ended (resumed with {run_id: result}); then the step's end."""
        runs = state["runs"]
        pending = [r["run_id"] for r in runs if "result" not in r]
        got = interrupt({"runs": pending}) if pending else {}
        missing = {"status": "failed", "take_ids": [], "error": "no result for the run"}
        results = [r.get("result") or got.get(r["run_id"]) or missing for r in runs]
        visit = state["visits"][nid]
        ids = [r["run_id"] for r in runs if r["run_id"]]
        seeds = [r["seed"] for r in runs if r["seed"] is not None]
        status = (
            "failed"
            if any(r["status"] == "failed" for r in results)
            else "cancelled"
            if any(r["status"] == "cancelled" for r in results)
            else "succeeded"
        )
        how = "resumed" if pending else "finished"
        cell = {"visit": visit, "status": status, "run_ids": ids, "seeds": seeds}
        if status != "succeeded":
            error = next((r.get("error") for r in results if r["status"] == "failed"), None)
            error = (error or "the run failed") if status == "failed" else None
            what = f"Run failed: {error}" if error else "Cancelled"
            return (
                _end(status, error)
                | {"cells": _cell(state, nid, "step", cell | {"error": error})}
                | _step(state, nid, "wait", visit, how, what, run_ids=ids, error=error)
            )
        takes = [t for r in results for t in (r.get("take_ids") or [])]
        pos = [f"{nid}:{visit}:{i}:{'-' if r['seed'] is None else r['seed']}"
               for i, (r, res) in enumerate(zip(runs, results)) for _ in res.get("take_ids") or []]  # fmt: skip
        up, edge = _go(plan, nid, plan["next"].get(nid), "next")
        what = f"{len(takes)} take{'s' * (len(takes) != 1)}"
        what = f"Parked, then resumed with {what}" if pending else what
        return (
            up
            | {"takes": (state.get("takes") or {}) | {nid: takes}}
            | {"pos": (state.get("pos") or {}) | {nid: pos}}
            | {"cells": _cell(state, nid, "step", cell | {"take_ids": takes})}
            | _step(state, nid, "wait", visit, how, what, changed=[f"takes.{nid} = {len(takes)}"],
                    edge=edge, run_ids=ids, take_ids=takes)
        )  # fmt: skip

    return wait


def _gate_node(plan, gid):
    def gate(state: Row) -> Row:
        """Park for a pick (the interrupt's value is what the row-parked event offers)."""
        info = _parked(plan, state, gid)
        if not info["candidates"]:
            error = f"no takes from {plan['nodes'][gid]['takes']} to review"
            return _end("failed", error) | _step(
                state, gid, "gate", info["visit"], "finished", error, error=error
            )
        pick = interrupt(info)
        k = len(pick.get("take_ids") or [])
        what = f"Parked, then resumed with a pick of {k}" if k else "Parked, then all rejected"
        return {"pick": pick, "to": f"{gid}.route"} | _step(
            state, gid, "gate", info["visit"], "resumed", what
        )

    return gate


def _route_node(plan, gid):
    def route(state: Row) -> Row:
        """Apply the pick: pass on; a reject goes along the fail edge until the cap, then `then`."""
        info, pick = _parked(plan, state, gid), state["pick"]
        visit, cands, n = info["visit"], info["candidates"], len(info["candidates"])
        br, loops = plan["branch"].get(gid), dict(state.get("loops") or {})
        up: Row = {"visits": state.get("visits", {}) | {gid: visit}}
        cell = {"visit": visit, "candidates": cands}
        if pick.get("take_ids"):
            ids = pick["take_ids"]
            nxt, edge = _go(plan, gid, br["pass"] if br else plan["next"].get(gid), "pass")
            return (
                up
                | nxt
                | {"picked": (state.get("picked") or {}) | {gid: ids}}
                | {"cells": _cell(state, gid, "gate", cell | {"status": "picked", "take_ids": ids})}
                | _step(state, gid, "route", visit, "finished",
                        f"Picked {len(ids)} of {n}; on to {edge['to'] if edge else 'nothing'}",
                        changed=[f"picked.{gid} = {len(ids)}"], edge=edge, take_ids=ids)
            )  # fmt: skip
        up["cells"] = _cell(state, gid, "gate", cell | {"status": "rejected"})
        if not br:
            error = f"rejected at {gid}"
            return (
                up
                | _end("failed", error)
                | {"rejected": True}
                | _step(state, gid, "route", visit, "finished", f"Rejected all {n}; the row fails",
                        error=error)
            )  # fmt: skip
        turns = loops.get(gid, 0)
        if turns < br["max"]:
            loops[gid] = turns + 1
            nxt, edge = _go(plan, gid, br["fail"], "fail")
            what = f"Rejected all {n}; back to {br['fail']} ({turns + 1} of {br['max']})"
            changed = [f"loops.{gid} = {turns + 1}"]
        else:
            nxt, edge = _go(plan, gid, br["then"], "then")
            what = f"Rejected all {n}; {br['max']} of {br['max']} tries used, on to {br['then']}"
            changed = []
        return (
            up
            | nxt
            | {"loops": loops}
            | _step(state, gid, "route", visit, "finished", what, changed=changed, edge=edge)
        )

    return route


def _output_node(nid, spec):
    def output(state: Row) -> Row:
        """The row ends with the `from` takes (none when that node never produced any)."""
        ids = _source(state, spec["from"]) or []
        visit = state.get("visits", {}).get(nid, 0) + 1
        coll = spec.get("collection")
        what = f"Row ended with {len(ids)} take{'s' * (len(ids) != 1)}" + (
            f" in {coll}" if coll else ""
        )
        return (
            _end("succeeded", None, ids)
            | {"visits": state.get("visits", {}) | {nid: visit}, "collection": coll}
            | {"cells": _cell(state, nid, "output", {"visit": visit, "status": "succeeded",
                                                       "take_ids": ids})}
            | _step(state, nid, "output", visit, "finished", what,
                    changed=[f"output = {len(ids)}"], take_ids=ids)
        )  # fmt: skip

    return output


# --- agent nodes: judge, write, decide (one LLM call per visit: an ask run) and memory

CHECK = {"judge": "verdict", "write": "apply", "decide": "route"}
MISSING = {"status": "failed", "take_ids": [], "error": "no result for the run"}


def _names(wf: dict) -> tuple[str, str | None]:
    """A workflow's ask node and its model node (None without one), found by kind."""
    kinds = {k: n["kind"] for k, n in wf["nodes"].items()}
    ask = next((k for k, v in kinds.items() if v.endswith("/ask")), "ask")
    return ask, next((k for k, v in kinds.items() if v.endswith("/model")), None)


def _model_values(plan, wf: dict, step: str | None) -> dict:
    """A family's ask runs at that step's merged model values (variant, precision): it reuses its entry."""
    _, model = _names(wf)
    s = plan["nodes"].get(step) if step else None
    swf = plan["workflows"].get(s["workflow"], {}) if s and s["kind"] == "step" else {}
    if not model or not swf or swf.get("family") != wf.get("family"):
        return {}
    _, smodel = _names(swf)
    keys = [(p, f"{smodel}.{p}") for p in ("variant", "precision")]
    return {f"{model}.{p}": s["inputs"][k] for p, k in keys if k in s["inputs"]}


def _model_ref(wf: dict, inputs: dict) -> dict | None:
    _, model = _names(wf)
    if not model:
        return None
    params = wf["nodes"][model].get("params", {})
    get = lambda p: inputs.get(f"{model}.{p}", params.get(p))
    return {"id": None, "variant": get("variant"), "precision": get("precision")}


def _text(plan, state, src: str) -> str | None:
    """A text source's value now: a Text input (as a write may have set it), a judge's reasons, a read's notes."""
    if src.startswith("$"):
        v = state["inputs"].get(f"{plan['inputs_node']}.{src[1:]}")
        return v if isinstance(v, str) else None
    nid, what = src.split(".")
    return (state.get(what) or {}).get(nid)


def _brief(plan, state, step: str) -> str:
    """The step's prompt text as its runs had it (the judge's brief)."""
    inputs, _ = run_inputs(plan, state, plan["nodes"][step])
    return next(
        (v for k, v in (inputs or {}).items() if k.endswith(".text") and isinstance(v, str)), ""
    )


def _decision_id(state, nid: str, visit: int) -> str:
    return f"{state['batch_id']}:{state['row']}:{state['attempt']}:{nid}:{visit}"


def _ask_call(plan, state, ctx, nid, spec) -> tuple[dict | None, str | None]:
    """This visit's call: {inputs, hashed (the replay hash's inputs), template, memory_read, candidates | allowed};
    or the error that fails the row. Every read of memory is the batch's snapshot (ctx.memory, state.notes)."""
    wf = plan["workflows"][spec["workflow"]]
    ask, _ = _names(wf)
    kind, extra, media, read = spec["kind"], {}, None, []
    if kind == "judge":
        step, crit = spec["takes"], spec["criteria"]
        cands = (state.get("takes") or {}).get(step, [])[:8]
        if not cands:
            return None, f"no takes from {step} to judge"
        examples, notes = [], ""
        if m := spec.get("memory"):
            limit = plan["nodes"][m].get("limit", 8)
            examples = [i for i in ctx.memory if i["kind"] == "judge_example" and i["key"] == nid]
            examples = examples[:limit]
            notes = (state.get("notes") or {}).get(m, "")
            read = [e["id"] for e in examples] + (state.get("memread") or {}).get(m, [])
        prompt = agents.judge_prompt(_brief(plan, state, step), len(cands), crit, examples, notes)
        schema = agents.judge_schema(len(cands), len(crit["must"]) + len(crit["nice"]))
        pos = (state.get("pos") or {}).get(step, [])
        media = (
            [{"take": {"take_id": t}} for t in cands],
            pos[: len(cands)] if len(pos) >= len(cands) else cands,
        )
        template, extra = agents.JUDGE, {"candidates": cands}
        tokens = agents.judge_tokens(len(cands), len(crit["must"]) + len(crit["nice"]))
    else:
        texts = [_text(plan, state, s) for s in spec["from"]]
        if None in texts:
            src = spec["from"][texts.index(None)]
            return None, f"nothing from {src if src.startswith('$') else src.split('.')[0]} yet"
        step, context = state.get("last_step"), list(zip(spec["from"][1:], texts[1:]))
        memread = state.get("memread") or {}
        read = [i for s in spec["from"] if s.endswith(".notes") for i in memread.get(s[:-6], [])]
        tokens = agents.MAX_TOKENS
        if kind == "write":
            # only the user's own texts (the row's inputs) may bring mentions in, never model-written reasons/notes
            own = [t for s, t in zip(spec["from"], texts) if s.startswith("$")]
            allowed = list(dict.fromkeys(h for t in own for h in agents.mentions(t)))
            prompt = agents.write_prompt(spec["instruction"], texts[0], context, allowed)
            schema, template, extra = agents.WRITE_SCHEMA, agents.WRITE, {"allowed": allowed}
            tokens = agents.WRITE_TOKENS
        else:
            prompt = agents.decide_prompt(
                spec["question"], list(zip(spec["from"], texts)), spec["routes"]
            )
            schema, template = agents.decide_schema(list(spec["routes"])), agents.DECIDE
    inputs = {f"{ask}.system": agents.SYSTEM, f"{ask}.prompt": prompt}
    inputs |= {f"{ask}.schema": agents.compact(schema), f"{ask}.max_tokens": tokens}
    inputs |= _model_values(plan, wf, step)
    hashed = dict(inputs)
    if (
        media
    ):  # the takes by id to the run; by position <step>:<visit>:<seed index>:<seed> in the hash
        inputs[f"{ask}.media"], hashed[f"{ask}.media"] = media
    call = {"inputs": inputs, "hashed": hashed, "template": template, "memory_read": read[:32]}
    return call | extra, None


def _ask_submit_node(plan, nid, spec, check):
    kind = spec["kind"]
    title = plan["workflows"][spec["workflow"]]["title"]

    async def submit(state: Row, runtime: Runtime[Context]) -> Row:
        """A stored decision for the replay key: no run, on to the check part. Else ask one ask run (the
        visit's, or under branch 1 the one re-ask after the checks failed); a terminal reply is its result."""
        ctx, call = runtime.context, state.get("call") or {}
        if call.get("node") == nid and call.get("reask"):
            pk = f"{_names(plan['workflows'][spec['workflow']])[0]}.prompt"
            again = agents.REASK.format(error=call["reask"])
            inputs = call["inputs"] | {pk: f"{call['inputs'][pk]}\n\n{again}"[: agents.PROMPT_MAX]}
            call = call | {"branch": 1, "reask": None, "result": None, "inputs": inputs}
            visit, up = call["visit"], {}
        else:
            visit = state.get("visits", {}).get(nid, 0) + 1
            up = {"visits": state.get("visits", {}) | {nid: visit}}
            built, error = _ask_call(plan, state, ctx, nid, spec)
            if error:
                cell = {"visit": visit, "status": "failed", "error": error}
                return (
                    up
                    | _end("failed", f"{nid}: {error}")
                    | {"cells": _cell(state, nid, kind, cell)}
                    | _step(state, nid, "submit", visit, "finished", error, error=error)
                )
            call_in = {"workflow": spec["workflow"], "template": built["template"]}
            tail = f"{state['row']}:{nid}:{visit}:{agents.inputs_hash(call_in | {'inputs': built.pop('hashed')})}"
            call = built | {"node": nid, "visit": visit, "branch": 0, "run": None, "run_ids": []}
            call |= {
                "replay_key": f"{state['batch_id']}:{tail}",
                "replayed_from": None,
                "answers": [],
            }
            call |= {"seen": [], "tokens_in": 0, "tokens_out": 0, "seconds": 0, "result": None}
            call |= {"reask": None, "derived": None, "park": None}
            # only "Run again: same decisions", a row's first attempt: Resume continues from the checkpoint (which
            # holds the answer) and a Retry asks again
            stored = (
                ctx.replay
                and ctx.replay_batch
                and state["attempt"] == 1
                and await ctx.replay(f"{ctx.replay_batch}:{tail}")
            )
            if (
                stored
            ):  # replayed: the stored answer is checked again against this visit's candidates
                good = [a for a in stored["answers"] if a["error"] is None][-1:]
                # the model saw nothing in this batch: the source decision (replayed_from) holds what it saw
                answer = {"tries": good, "seen": [], "tokens_in": 0, "tokens_out": 0}
                result = {"status": "succeeded", "take_ids": [], "error": None}
                call |= {
                    "replayed_from": stored["id"],
                    "result": result | {"answer": answer | {"seconds": 0}},
                }
                cell = {
                    "visit": visit,
                    "status": "running",
                    "decision_id": _decision_id(state, nid, visit),
                }
                return (
                    up
                    | {"call": call, "to": f"{nid}.{check}", "cells": _cell(state, nid, kind, cell)}
                    | _step(state, nid, "submit", visit, "finished",
                            f"Replayed decision {stored['id']}; no model asked",
                            changed=[f"call.{nid} = replayed"])
                )  # fmt: skip
        branch = call["branch"]
        reply = await ctx.create_run(
            {
                "owner_id": ctx.owner_id,
                "session_id": ctx.session_id,
                "workflow_id": spec["workflow"],
                "inputs": call["inputs"],
                "lane": "batch",
                "batch_id": state["batch_id"],
                "row": state["row"],
                "idempotency_key": f"{state['batch_id']}:{state['row']}:{state['attempt']}:{nid}:{visit - 1}:{branch}",
            }
        )
        if "code" in reply:  # invalid | not_found: final
            run, result = None, {"status": "failed", "take_ids": [], "error": reply["error"]}
        else:
            run, result = reply["run_id"], None
            if reply["status"] in TERMINAL:
                result = {k: reply.get(k) for k in ("status", "take_ids", "error", "answer")}
        ids = call["run_ids"] + ([run] if run else [])
        call = call | {"run": run, "run_ids": ids, "result": result}
        cell = {"visit": visit, "status": "running", "run_ids": ids}
        cell |= {"decision_id": _decision_id(state, nid, visit)} | (
            {"candidates": call["candidates"]} if kind == "judge" else {}
        )
        what = f"Asked {title} once more (the answer was invalid)" if branch else f"Asked {title}"
        return (
            up
            | {"call": call, "run_id": run or state.get("run_id"), "to": f"{nid}.wait"}
            | {"cells": _cell(state, nid, kind, cell)}
            | _step(state, nid, "submit", visit, "finished", what, changed=[f"call.{nid} = {branch}"],
                    run_ids=ids)
        )  # fmt: skip

    return submit


def _ask_wait_node(nid, check):
    def wait(state: Row) -> Row:
        """Park until the ask run has ended (resumed with {run_id: result}); then on to the check part."""
        call = state["call"]
        pending = [] if call["result"] else [call["run"]]
        got = interrupt({"runs": pending}) if pending else {}
        result = call["result"] or got.get(call["run"]) or MISSING
        what = {"succeeded": "answered", "failed": "the run failed", "cancelled": "cancelled"}
        what = what[result["status"]]
        what = f"Parked, then {what}" if pending else what.capitalize()
        return {"call": call | {"result": result}, "to": f"{nid}.{check}"} | _step(
            state, nid, "wait", call["visit"], "resumed" if pending else "finished", what,
            run_ids=call["run_ids"],
        )  # fmt: skip

    return wait


def _checked(call: dict, check) -> tuple:
    """(value, check error, run error, call with the run's tries, seen and counts added)."""
    res = call["result"]
    answer = res.get("answer") or {}
    tries = [dict(t) for t in answer.get("tries") or []]
    call = call | {
        "seen": (answer.get("seen") or call["seen"])[:24],
        "tokens_in": call["tokens_in"] + answer.get("tokens_in", 0),
        "tokens_out": call["tokens_out"] + answer.get("tokens_out", 0),
        "seconds": call["seconds"] + answer.get("seconds", 0),
    }
    value = err = failed = None
    if res["status"] != "succeeded":
        failed = res.get("error") or res["status"]
    elif not tries:
        err = "the run gave no answer"
    else:
        value, err = check(tries[-1]["text"])
        if err:
            tries[-1]["error"] = err[:2000]
    return value, err, failed, call | {"answers": (call["answers"] + tries)[-4:]}


def _decision(plan, state, spec, call, error=None, **result) -> dict:
    """The decision record (defs decision): what was asked, what was seen and answered, what it decided."""
    wf = plan["workflows"][spec["workflow"]]
    ask, inp = _names(wf)[0], call["inputs"]
    return {
        "id": _decision_id(state, call["node"], call["visit"]),
        "replay_key": call["replay_key"],
        "node": call["node"],
        "kind": spec["kind"],
        "visit": call["visit"],
        "attempt": state["attempt"],
        "replayed": bool(call["replayed_from"]),
        "replayed_from": call["replayed_from"],
        "run_ids": call["run_ids"][:2],
        "workflow": spec["workflow"],
        "model": _model_ref(wf, inp),
        "template": call["template"],
        "system": inp[f"{ask}.system"],
        "prompt": inp[f"{ask}.prompt"],
        "schema": inp[f"{ask}.schema"],
        "seen": call["seen"],
        "answers": call["answers"],
        "tokens_in": call["tokens_in"],
        "tokens_out": call["tokens_out"],
        "seconds": call["seconds"],
        "memory_read": call["memory_read"],
        "error": error[:2000] if error else None,
    } | result


def _not_passed(plan, state, nid, spec, call, err, failed, part) -> Row | None:
    """The re-ask (an answer that failed the checks, once per visit) or the row's end (the run failed, or the
    second answer failed too); None when the answer passed."""
    if err is None and failed is None:
        return None
    visit, kind, ids = call["visit"], spec["kind"], call["run_ids"]
    if failed is None and call["branch"] == 0:
        return {"call": call | {"reask": err}, "to": f"{nid}.submit"} | _step(
            state, nid, part, visit, "finished", f"Answer invalid, asking once more: {err}",
            error=err, run_ids=ids,
        )  # fmt: skip
    if call["result"]["status"] == "cancelled":
        cell = {"visit": visit, "status": "cancelled", "run_ids": ids}
        return (
            _end("cancelled")
            | {"call": call, "cells": _cell(state, nid, kind, cell)}
            | _step(state, nid, part, visit, "finished", "Cancelled", run_ids=ids)
        )
    error = failed or err
    dec = _decision(plan, state, spec, call, error=error)
    cell = {
        "visit": visit,
        "status": "failed",
        "run_ids": ids,
        "error": error,
        "decision_id": dec["id"],
    }
    return (
        _end("failed", f"{nid}: {error}")
        | {"call": call, "cells": _cell(state, nid, kind, cell)}
        | _step(state, nid, part, visit, "finished", f"Failed: {error}", error=error, run_ids=ids,
                decision=dec)
    )  # fmt: skip


def _judge_loop(br, turns: int, gave_up: bool) -> dict | None:
    return br and {"turns": turns, "max": br["max"], "then": br["then"], "gave_up": gave_up}


def _verdict_node(plan, nid, spec):
    crit, keep, mode = spec["criteria"], spec.get("keep", 1), spec.get("mode", "suggest")
    m = len(crit["must"])

    def verdict(state: Row) -> Row:
        """Check the answer, derive the verdict (agents.judge_verdict) and record the decision; then park for
        a pick (Suggest: a pass, or a fail with no retry left) or route (Auto, or a fail with a retry left)."""
        call = state["call"]
        visit, cands = call["visit"], call["candidates"]
        schema = agents.judge_schema(len(cands), m + len(crit["nice"]))

        def check(text):
            value, err = agents.parse(text, schema)
            v = value and agents.judge_verdict(value, crit, keep, cands)
            return (
                (None, err or v["error"]) if err or "error" in v else (value | {"derived": v}, None)
            )

        value, err, failed, call = _checked(call, check)
        if (out := _not_passed(plan, state, nid, spec, call, err, failed, "verdict")) is not None:
            return out
        v, answers = value["derived"], value["takes"]
        takes = [
            {"take_id": t, "checks": a["checks"], "reason": a["reason"], "passes": v["passes"][i],
             "rank": v["ranks"][i]}
            for i, (t, a) in enumerate(zip(cands, answers))
        ]  # fmt: skip
        result = {
            "criteria": crit,
            "keep": keep,
            "mode": mode,
            "takes": takes,
            "picked": v["picked"],
        }
        dec = _decision(plan, state, spec, call, judge=result | {"verdict": v["verdict"],
                                                                "first_failed": v["first_failed"]})  # fmt: skip
        br, turns = plan["branch"].get(nid), (state.get("loops") or {}).get(nid, 0)
        retry = bool(br) and turns < br["max"]
        park = mode == "suggest" and (v["verdict"] == "pass" or not retry)
        firsts = [
            next((crit["must"][j] for j in range(m) if not a["checks"][j]), None) for a in answers
        ]
        judged = {
            "verdict": v["verdict"],
            "picked": len(v["picked"]),
            "of": len(cands),
            "keep": keep,
        }
        # the best take may pass every must while too few others do (keep > 1): name a failing take's instead
        first = v["first_failed"]
        if v["verdict"] == "fail" and first is None:
            first = next((f for f in firsts if f), None)
        judged |= {"first_failed": first, "suggested": v["picked"], "agreed": None}
        judged["takes"] = [
            {
                "take_id": t,
                "passes": v["passes"][i],
                "first_failed": firsts[i],
                "reason": a["reason"],
            }
            for i, (t, a) in enumerate(zip(cands, answers))
        ]
        status = "waiting" if park else "picked" if v["verdict"] == "pass" else "rejected"
        cell = {"visit": visit, "status": status, "run_ids": call["run_ids"], "candidates": cands}
        cell |= {"take_ids": v["picked"] if status == "picked" else [], "decision_id": dec["id"]}
        cell["judged"] = judged
        on_reject = "retry" if retry else "then" if br else "fail"
        info = {"gate": nid, "kind": "judge", "visit": visit, "keep": keep, "candidates": cands}
        info |= {"suggested": v["picked"], "decision_id": dec["id"], "on_reject": on_reject}
        reasons = "\n".join(f"take {i}: {a['reason']}" for i, a in enumerate(answers, 1))
        kept = ", ".join(f"take {cands.index(t) + 1}" for t in v["picked"])
        what = (
            f"Kept {kept} of {len(cands)}: all checks pass" if v["verdict"] == "pass"
            else f"{sum(v['passes'])} of {len(cands)} passed, {keep} needed"
            + (f"; first failed: {first}" if first else "")
        ) + (" (suggested for review)" if park else "")  # fmt: skip
        return {
            "call": call | {"derived": v, "park": info if park else None},
            "reasons": (state.get("reasons") or {}) | {nid: reasons},
            "pick": None,
            "to": f"{nid}.gate" if park else f"{nid}.route",
            "cells": _cell(state, nid, "judge", cell, _judge_loop(br, turns, False)),
        } | _step(state, nid, "verdict", visit, "finished", what,
                  changed=[f"decisions.{nid} = {visit}", f"reasons.{nid} = {len(cands)}"],
                  run_ids=call["run_ids"], take_ids=v["picked"], decision=dec)  # fmt: skip

    return verdict


def _judge_gate_node(nid):
    def gate(state: Row) -> Row:
        """Park for a pick with the judge's suggestion (the interrupt's value is the row-parked event's)."""
        info = state["call"]["park"]
        pick = interrupt(info)
        k = len(pick.get("take_ids") or [])
        what = f"Parked, then resumed with a pick of {k}" if k else "Parked, then all rejected"
        return {"pick": pick, "to": f"{nid}.route"} | _step(
            state, nid, "gate", info["visit"], "resumed", what
        )

    return gate


def _judge_route_node(plan, nid, spec):
    def route(state: Row) -> Row:
        """Apply the pick (Suggest) or the verdict (Auto, or a fail with a retry left): kept takes go on; none
        kept goes along the fail edge until the cap, then `then`; with a plain edge the row fails."""
        call, pick = state["call"], state.get("pick")
        v, visit, n = call["derived"], call["visit"], len(call["candidates"])
        br, loops = plan["branch"].get(nid), dict(state.get("loops") or {})
        turns = loops.get(nid, 0)
        att = dict(next(c for c in state["cells"] if c["node"] == nid)["attempts"][-1])
        judged = dict(att["judged"])
        ids = (pick.get("take_ids") or []) if pick else v["picked"]
        if pick:
            judged["agreed"] = pick.get("agreed")
        judged["picked"] = len(ids)
        att |= {"status": "picked" if ids else "rejected", "take_ids": ids, "judged": judged}
        up: Row = {"pick": None}
        changed, gave_up = [], False
        if ids:
            nxt, edge = _go(plan, nid, br["pass"] if br else plan["next"].get(nid), "pass")
            up["picked"] = (state.get("picked") or {}) | {nid: ids}
            what = f"Kept {len(ids)} of {n}; on to {edge['to'] if edge else 'nothing'}"
            changed = [f"picked.{nid} = {len(ids)}"]
        elif br and turns < br["max"]:
            loops[nid] = turns + 1
            nxt, edge = _go(plan, nid, br["fail"], "fail")
            what = f"No take kept; back to {br['fail']} ({turns + 1} of {br['max']})"
            changed = [f"loops.{nid} = {turns + 1}"]
        elif br:
            nxt, edge = _go(plan, nid, br["then"], "then")
            gave_up = True
            what = f"No take kept; {br['max']} of {br['max']} tries used, on to {br['then']}"
        else:
            error = f"rejected at {nid}" if pick else f"no take passed {nid}"
            return (
                _end("failed", error)
                | {"rejected": bool(pick), "pick": None, "cells": _cell(state, nid, "judge", att)}
                | _step(state, nid, "route", visit, "finished", error, error=error)
            )
        loop = _judge_loop(br, loops.get(nid, 0), gave_up)
        return (
            up
            | nxt
            | {"loops": loops, "cells": _cell(state, nid, "judge", att, loop)}
            | _step(state, nid, "route", visit, "finished", what, changed=changed, edge=edge,
                    take_ids=ids)
        )  # fmt: skip

    return route


def _apply_node(plan, nid, spec):
    name = f"{plan['inputs_node']}.{spec['to'][1:]}"

    def apply(state: Row) -> Row:
        """Check the rewrite (schema; mentions only among the `from` texts') and set the input for the rest of
        the row attempt (later `$<input>` binds read it)."""
        call = state["call"]
        allowed = set(call["allowed"])

        def check(text):
            value, err = agents.parse(text, agents.WRITE_SCHEMA)
            extra = [h for h in agents.mentions(value["text"]) if h not in allowed] if value else []
            if extra:
                err = f"the text mentions @{extra[0]}, which the text it rewrites does not"
            return (None, err) if err else (value, None)

        value, err, failed, call = _checked(call, check)
        if (out := _not_passed(plan, state, nid, spec, call, err, failed, "apply")) is not None:
            return out
        before, after, note = state["inputs"].get(name, ""), value["text"], value.get("note", "")
        result = {"to": spec["to"], "before": before, "after": after, "note": note}
        dec = _decision(plan, state, spec, call, write=result)
        nxt, edge = _go(plan, nid, plan["next"].get(nid), "next")
        cell = {"visit": call["visit"], "status": "succeeded", "run_ids": call["run_ids"]}
        cell["decision_id"] = dec["id"]
        what = f"Rewrote {spec['to']}" + (f": {note}" if note else "")
        return (
            nxt
            | {"call": call, "inputs": state["inputs"] | {name: after}}
            | {"cells": _cell(state, nid, "write", cell)}
            | _step(state, nid, "apply", call["visit"], "finished", what,
                    changed=[f"inputs.{spec['to'][1:]} = {len(after)} chars"], edge=edge,
                    run_ids=call["run_ids"], decision=dec)
        )  # fmt: skip

    return apply


def _decide_node(plan, nid, spec):
    labels = list(spec["routes"])

    def route(state: Row) -> Row:
        """Check the answer (a declared label: the schema's enum) and take that route; every route but `then`
        counts against `max`, and once used up `then` is taken whatever the answer (forced)."""
        call = state["call"]
        r = plan["routes"][nid]
        value, err, failed, call = _checked(
            call, lambda t: agents.parse(t, agents.decide_schema(labels))
        )
        if (out := _not_passed(plan, state, nid, spec, call, err, failed, "route")) is not None:
            return out
        loops = dict(state.get("loops") or {})
        turns, label = loops.get(nid, 0), value["route"]
        forced = label != r["then"] and turns >= r["max"]
        if forced:
            label = r["then"]
        elif label != r["then"]:
            loops[nid] = turns + 1
        to = r["routes"][label]
        result = {"route": label, "to": to, "reason": value["reason"], "forced": forced}
        dec = _decision(plan, state, spec, call, decide=result)
        up, edge = _go(plan, nid, to, "then" if forced else "next")
        cell = {"visit": call["visit"], "status": "succeeded", "run_ids": call["run_ids"]}
        cell |= {"decision_id": dec["id"], "route": {"label": label, "to": to}}
        loop = {"turns": loops.get(nid, 0), "max": r["max"], "then": r["routes"][r["then"]]}
        what = f"Route {label}, on to {to}" + (
            f" (forced: {r['max']} of {r['max']} turns used)" if forced else ""
        )
        changed = [f"loops.{nid} = {loops[nid]}"] if loops.get(nid, 0) != turns else []
        return (
            up
            | {"call": call, "loops": loops}
            | {"cells": _cell(state, nid, "decide", cell, loop | {"gave_up": forced})}
            | _step(state, nid, "route", call["visit"], "finished", what, changed=changed,
                    edge=edge, run_ids=call["run_ids"], decision=dec)
        )  # fmt: skip

    return route


def memory_item_id(batch_id: str, row: int, node: str, visit: int) -> str:
    """A memory write's item id: one per (batch, row, node, visit), so a re-run overwrites it."""
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"helmstudio:memory:{batch_id}:{row}:{node}:{visit}"))


def _memory_node(plan, nid, spec):
    async def memory(state: Row, runtime: Runtime[Context]) -> Row:
        """read: the notes of the batch's snapshot (key filter, newest `limit`) into state; write: the `from`
        texts as one note to the pipeline's live memory (seen from the next batch)."""
        ctx = runtime.context
        visit = state.get("visits", {}).get(nid, 0) + 1
        up = {"visits": state.get("visits", {}) | {nid: visit}}
        cell = {"cells": _cell(state, nid, "memory", {"visit": visit, "status": "succeeded"})}
        nxt, edge = _go(plan, nid, plan["next"].get(nid), "next")
        if spec["op"] == "read":
            key = spec.get("key")
            items = [i for i in ctx.memory if i["kind"] == "note" and key in (None, i["key"])]
            items = items[: spec.get("limit", 8)]
            ids = [i["id"] for i in items]
            return (
                up
                | nxt
                | cell
                | {"notes": (state.get("notes") or {}) | {nid: "\n".join(i["text"] for i in items)}}
                | {"memread": (state.get("memread") or {}) | {nid: ids}}
                | _step(state, nid, "memory", visit, "finished",
                        f"Read {len(ids)} note{'s' * (len(ids) != 1)} from the batch's memory",
                        changed=[f"notes.{nid} = {len(ids)}"], edge=edge,
                        memory={"read": ids, "wrote": []})
            )  # fmt: skip
        texts = [_text(plan, state, s) for s in spec["from"]]
        if None in texts:
            src = spec["from"][texts.index(None)]
            error = f"nothing from {src if src.startswith('$') else src.split('.')[0]} yet"
            return (
                up
                | _end("failed", error)
                | _step(state, nid, "memory", visit, "finished", error, error=error)
            )
        iid = memory_item_id(state["batch_id"], state["row"], nid, visit)
        source = {"kind": "node", "batch_id": state["batch_id"], "row": state["row"]}
        value = {"kind": "note", "key": spec.get("key"), "text": "\n".join(texts)[:2000]}
        value |= {
            "kept": [],
            "rejected": [],
            "agreed": None,
            "source": source | {"node": nid, "visit": visit},
        }
        why = await ctx.put_note(iid, value) if ctx.put_note else "no pipeline memory"
        wrote = why is None
        what = "Noted for the next batches" if wrote else f"Nothing noted: {why}"
        return (
            up
            | nxt
            | cell
            | _step(state, nid, "memory", visit, "finished", what, edge=edge,
                    memory={"read": [], "wrote": [iid] if wrote else []})
        )  # fmt: skip

    return memory


def recursion_limit(plan: dict) -> int:
    """LangGraph's backstop: every graph node on every loop turn (a judge's 5 parts and a re-ask's 2 more:
    at most 8 per visit), plus room (the caps in state decide, not this)."""
    turns = 1 + sum(b["max"] for b in [*plan["branch"].values(), *plan.get("routes", {}).values()])
    return 8 * len(plan["nodes"]) * turns + 10
