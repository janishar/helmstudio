"""A row's graph, built from the batch's plan (pipeline.plan), one thread per row (thread_id "<batch_id>:<row>").

Each pipeline node is one or two graph nodes:

    inputs → <id>                                 the row's inputs (nothing to do but count the step)
    step   → <id>.submit → <id>.wait              submit asks studio.runs.create once per seed under
                                                  <batch>:<row>:<attempt>:<id>:<visit from 0>:<seed index>, records
                                                  the runs; wait only calls interrupt() until every run has ended
    gate   → <id>.gate → <id>.route               gate only calls interrupt() (the row is parked for a pick);
                                                  route applies the pick or the reject and chooses the edge
    output → <id>                                 the row ends succeeded with its `from` takes

Every graph node sets `to` (the next graph node, None = END) and `last` (what it did, for evt.pipeline.step) and
counts `n` (the attempt's graph steps). The row ends when a node sets `status`. A node that calls interrupt()
does nothing with side effects (a resumed node runs again from its first line). State is small plain JSON."""

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TypedDict

from langgraph.graph import END, START, StateGraph
from langgraph.runtime import Runtime
from langgraph.types import interrupt

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


@dataclass
class Context:
    """Per batch, from the engine's batch record (never from state)."""

    create_run: Callable[[dict], Awaitable[dict]]
    owner_id: str
    session_id: str


def entry(plan: dict, nid: str) -> str:
    """The graph node a pipeline node starts at."""
    kind = plan["nodes"][nid]["kind"]
    return {"step": f"{nid}.submit", "gate": f"{nid}.gate"}.get(kind, nid)


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
    }


def _step(state, node, part, visit, status, what, **kw) -> Row:
    """The graph step's record and count."""
    last = {"node": node, "part": part, "visit": visit, "status": status, "what": what[:300]}
    last |= {"changed": [], "edge": None, "run_ids": [], "take_ids": [], "error": None} | kw
    last["run_ids"], last["take_ids"] = last["run_ids"][:8], last["take_ids"][:8]
    return {"n": state.get("n", 0) + 1, "last": last}


def _cell(state, node, kind, attempt: dict) -> list:
    """The cells with `attempt` set on `node` (replacing the same visit's)."""
    cells = [c | {"attempts": list(c["attempts"])} for c in state.get("cells", [])]
    c = next((c for c in cells if c["node"] == node), None)
    if c is None:
        cells.append(c := {"node": node, "kind": kind, "attempts": []})
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
            | {"runs": runs, "run_id": ids[-1] if ids else state.get("run_id")}
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
        up, edge = _go(plan, nid, plan["next"].get(nid), "next")
        what = f"{len(takes)} take{'s' * (len(takes) != 1)}"
        what = f"Parked, then resumed with {what}" if pending else what
        return (
            up
            | {"takes": (state.get("takes") or {}) | {nid: takes}}
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
            nxt["rejected"] = True
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


def recursion_limit(plan: dict) -> int:
    """LangGraph's backstop: every graph node on every loop turn, plus room (the caps decide, not this)."""
    turns = 1 + sum(b["max"] for b in plan["branch"].values())
    return 2 * len(plan["nodes"]) * turns + 10
