"""A batch's pipeline: the rules the engine depends on (`problems`), and the plan it builds its graph from (`plan`).

`problems` is contracts/README.md "Pipeline rules" without the ones that need the workflows or the palette (those
studio checks at Submit): structure, cycles and caps, the run bound, gates, sources, reach. Same codes, same
(code, node, port, param) tuples as check.py's `pipeline_problems`, tested against contracts/fixtures/pipelines.

`plan` turns cmd.pipeline.submit's snapshot into what graph.build needs. A batch of one workflow (no
`snapshot.pipeline`) is the plan `step → out`: one step `step` that sends the row's inputs as they are (its key
stays `…:step:0:0`) and an output of its takes; it publishes no step events and no cells."""

import json

MAX_NODES, MAX_BYTES, MAX_RUNS = 24, 65536, 64
CODES = {
    "too_many_nodes", "too_large", "inputs_node", "duplicate_input", "unknown_node", "branch_not_gate",
    "edge_invalid", "uncapped_cycle", "too_many_runs", "gate_takes", "gate_keep", "bind_source",
    "unreachable", "dead_end", "no_output",
}  # fmt: skip


def problems(doc: dict) -> set[tuple]:
    """Every problem in CODES as (code, node, port, param)."""
    out, nodes = set(), doc["nodes"]

    def add(code, node=None, port=None, param=None):
        out.add((code, node, port, param))

    if len(nodes) > MAX_NODES:
        add("too_many_nodes")
    if len(json.dumps(doc, separators=(",", ":"), ensure_ascii=False).encode()) > MAX_BYTES:
        add("too_large")
    if out:
        return out
    roots, inputs = [n for n, v in nodes.items() if v["kind"] == "inputs"], set()
    if len(roots) != 1:
        add("inputs_node")
    for nid in roots:
        names = [i["name"] for i in nodes[nid]["inputs"]]
        for name in {x for x in names if names.count(x) > 1}:
            add("duplicate_input", nid, param=name)
        inputs |= set(names)
    succ, outdeg, capped = {n: [] for n in nodes}, {}, []  # succ: node → [(to, capped)]
    for frm, to in doc["edges"]:
        targets = [to] if isinstance(to, str) else [to["pass"], to["fail"], to["then"]]
        if frm not in nodes or any(t not in nodes for t in targets):
            add("unknown_node", frm)
            continue
        if not isinstance(to, str) and nodes[frm]["kind"] != "gate":
            add("branch_not_gate", frm)
            continue
        outdeg[frm] = outdeg.get(frm, 0) + 1
        if (
            outdeg[frm] > 1
            or nodes[frm]["kind"] == "output"
            or any(nodes[t]["kind"] == "inputs" for t in targets)
        ):
            add("edge_invalid", frm)
            continue
        if isinstance(to, str):
            succ[frm].append((to, False))
        else:
            succ[frm] += [(to["pass"], False), (to["fail"], True), (to["then"], False)]
            capped.append((frm, to["fail"], to["max"]))

    def reach(src, avoid=None, uncapped_only=False) -> set[str]:
        seen, todo = set(), [src]
        while todo:
            for t, cap in succ[todo.pop()]:
                if t != avoid and t not in seen and not (uncapped_only and cap):
                    seen.add(t)
                    todo.append(t)
        return seen

    for nid in nodes:  # ponytail: O(n·e) per node, fine at 24 nodes
        if nid in reach(nid, uncapped_only=True):
            add("uncapped_cycle", nid)
    if sum(n.get("seeds", 1) * visits(nid, capped, reach) for nid, n in steps(nodes)) > MAX_RUNS:
        add("too_many_runs")
    root = roots[0] if len(roots) == 1 else None
    if root:
        for nid in set(nodes) - reach(root) - {root}:
            add("unreachable", nid)
    outputs = {n for n, v in nodes.items() if v["kind"] == "output"}
    if not outputs:
        add("no_output")
    for nid, n in nodes.items():
        if outputs and n["kind"] != "output" and not reach(nid) & outputs:
            add("dead_end", nid)

    def dominates(a, b) -> bool:
        return root is None or a == root or (a != b and b not in reach(root, avoid=a))

    def names_something(src: str) -> bool:
        if src.startswith("$"):
            return src[1:] in inputs
        nid, what = src.split(".")
        return nid in nodes and nodes[nid]["kind"] == ("step" if what == "takes" else "gate")

    for nid, g in nodes.items():
        if g["kind"] != "gate":
            continue
        t = nodes.get(g["takes"])
        if not t or t["kind"] != "step" or not dominates(g["takes"], nid):
            add("gate_takes", nid)
        elif g.get("keep", 1) > t.get("seeds", 1):
            add("gate_keep", nid)
    for nid in outputs:
        src = nodes[nid]["from"]
        if not names_something(src) or (root and nid not in reach(src.split(".")[0])):
            add("bind_source", nid, "from")
    for nid, n in steps(nodes):
        for k, v in n.get("bind", {}).items():
            if not names_something(v) or (
                not v.startswith("$") and not dominates(v.split(".")[0], nid)
            ):
                add("bind_source", nid, k)
    return out


def steps(nodes: dict):
    return [(nid, n) for nid, n in nodes.items() if n["kind"] == "step"]


def visits(nid: str, capped: list, reach) -> int:
    """How often a step can run in a row: 1 + the caps of every capped edge g→f on a cycle through it."""
    return 1 + sum(m for g, f, m in capped if (nid == f or nid in reach(f)) and g in reach(nid))


def plan(snapshot: dict) -> dict:
    """{pipeline, nodes, start, next (node → node), branch (gate → {pass, fail, max, then}), inputs_node,
    workflows, gates}."""
    p = snapshot.get("pipeline")
    if not p:
        step = {"kind": "step", "workflow": snapshot["workflow_id"], "seeds": 1, "inputs": {}}
        step |= {"bind": {}, "seed_param": None, "row_inputs": True}
        return {
            "pipeline": False,
            "nodes": {"step": step, "out": {"kind": "output", "from": "step.takes"}},
            "start": "step",
            "next": {"step": "out"},
            "branch": {},
            "inputs_node": None,
            "workflows": {snapshot["workflow_id"]: snapshot["workflow"]},
            "gates": [],
        }
    nodes, nxt, branch = p["nodes"], {}, {}
    for frm, to in p["edges"]:
        if isinstance(to, str):
            nxt[frm] = to
        else:
            branch[frm] = to
    root = next(n for n, v in nodes.items() if v["kind"] == "inputs")
    return {
        "pipeline": True,
        "nodes": nodes,
        "start": root,
        "next": nxt,
        "branch": branch,
        "inputs_node": root,
        "workflows": p["workflows"],
        "gates": sorted(n for n, v in nodes.items() if v["kind"] == "gate"),
    }
