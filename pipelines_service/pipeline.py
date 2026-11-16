"""A batch's pipeline: the rules the engine depends on (`problems`), and the plan it builds its graph from (`plan`).

`problems` is contracts/README.md "Pipeline rules" without the ones that need the workflows or the palette (those
studio checks at Submit): structure, cycles and caps, the run and LLM-call bounds (Submit's worst case per
row), gates, judges, writes, decides, sources, reach. Same codes, same
(code, node, port, param) tuples as check.py's `pipeline_problems`, tested against contracts/fixtures/pipelines.

`plan` turns cmd.pipeline.submit's snapshot into what graph.build needs. A batch of one workflow (no
`snapshot.pipeline`) is the plan `step → out`: one step `step` that sends the row's inputs as they are (its key
stays `…:step:0:0`) and an output of its takes; it publishes no step events and no cells."""

import json

from jsonschema import Draft202012Validator

MAX_NODES, MAX_BYTES, MAX_RUNS, MAX_CALLS = 24, 65536, 64, 32
LLM_KINDS = ("judge", "write", "decide")  # nodes that ask a model: one call (an ask run) per visit
MODEL_KINDS = LLM_KINDS + ("agent",)
CAPS = {
    "max_turns": 6,
    "max_tool_calls": 12,
    "max_tokens": 16000,
}  # an agent's defaults (per visit or wake)
ADDED_ROWS = 100  # the most rows agents may add to a batch (studio's MaxAddedRows)
PROMPT_TOKENS = 8192  # a prompt's token bound for the remote worst case: 32768 characters / 4
OUT_TOKENS = {"write": 1400, "write-list": 4096, "decide": 800}
CODES = {
    "too_many_nodes", "too_large", "inputs_node", "duplicate_input", "unknown_node", "branch_not_gate",
    "decide_routes", "edge_invalid", "uncapped_cycle", "too_many_runs", "too_many_calls", "gate_takes",
    "gate_keep", "judge_criteria", "write_target", "bind_source", "unreachable", "dead_end", "no_output",
    "fanout_source", "fanout_takes", "agent_tools", "director_count", "model_media", "unknown_custom",
    "custom_input",
}  # fmt: skip


def _field_kind(schema: dict) -> str | None:
    """A custom field a source may fill: text (a string), takes (a list of take ids, maxItems <= 8), else None."""
    if schema.get("type") == "string":
        return "text"
    it = schema.get("items", {})
    if (schema.get("type") == "array" and it.get("type") == "string" and it.get("format") == "uuid"
            and schema.get("maxItems", 99) <= 8):  # fmt: skip
        return "takes"
    return None


def problems(doc: dict, info: dict | None = None, custom: list[dict] | None = None,
             providers: list[dict] | None = None) -> set[tuple]:  # fmt: skip
    """Every problem in CODES as (code, node, port, param) (unknown_custom, custom_input only with `custom`, the
    registered custom nodes); `info`, when given, receives the worst case per row (`bounds`) and what leaves the
    machine (`egress`, `reserved`; a server model's default from `providers`) that Submit shows."""
    out, nodes = set(), doc["nodes"]

    def add(code, node=None, port=None, param=None):
        out.add((code, node, port, param))

    if len(nodes) > MAX_NODES:
        add("too_many_nodes")
    if len(json.dumps(doc, separators=(",", ":"), ensure_ascii=False).encode()) > MAX_BYTES:
        add("too_large")
    if out:
        return out
    of = lambda kind: [n for n, v in nodes.items() if v["kind"] == kind]
    roots, inputs = of("inputs"), {}
    if len(roots) != 1:
        add("inputs_node")
    for nid in roots:
        names = [i["name"] for i in nodes[nid]["inputs"]]
        for name in {x for x in names if names.count(x) > 1}:
            add("duplicate_input", nid, param=name)
        inputs |= {i["name"]: i["type"] for i in nodes[nid]["inputs"]}
    routers = lambda n: (
        nodes[n]["kind"] == "decide" or (nodes[n]["kind"] == "agent" and "routes" in nodes[n])
    )
    succ, outdeg, capped, routed = (
        {n: [] for n in nodes},
        {},
        [],
        set(),
    )  # succ: node -> [(to, capped)]
    for frm, to in doc["edges"]:
        kind = "plain" if isinstance(to, str) else "routes" if "routes" in to else "branch"
        targets = (
            [to] if kind == "plain"
            else list(to["routes"].values()) if kind == "routes"
            else [to["pass"], to["fail"], to["then"]]
        )  # fmt: skip
        if frm not in nodes or any(t not in nodes for t in targets):
            add("unknown_node", frm)
            continue
        fk = nodes[frm]["kind"]
        if (kind == "branch" and fk not in ("gate", "judge")) or (
            kind == "routes" and fk not in ("decide", "agent")
        ):
            add("branch_not_gate", frm)
            continue
        if (routers(frm) and (kind != "routes" or set(to["routes"]) != set(nodes[frm]["routes"])
                              or to["then"] not in to["routes"])) or (
                fk == "agent" and kind == "routes" and not routers(frm)):  # fmt: skip
            add("decide_routes", frm)
            continue
        outdeg[frm] = outdeg.get(frm, 0) + 1
        if outdeg[frm] > 1 or fk == "output" or any(nodes[t]["kind"] == "inputs" for t in targets):
            add("edge_invalid", frm)
            continue
        if kind == "plain":
            succ[frm].append((to, False))
        elif kind == "branch":
            succ[frm] += [(to["pass"], False), (to["fail"], True), (to["then"], False)]
            capped.append((frm, to["fail"], to["max"]))
        else:  # every route but then counts against the decide's (or agent's) max
            routed.add(frm)
            for label, t in to["routes"].items():
                succ[frm].append((t, label != to["then"]))
                if label != to["then"]:
                    capped.append((frm, t, to["max"]))
    for nid in nodes:
        if routers(nid) and nid not in routed:
            add("decide_routes", nid)

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

    def visits(nid) -> int:
        """How often a node can run in a row: 1 + the caps of every capped edge g->f on a cycle through it."""
        return 1 + sum(m for g, f, m in capped if (nid == f or nid in reach(f)) and g in reach(nid))

    root = roots[0] if len(roots) == 1 else None

    def dominates(a, b) -> bool:
        return root is None or a == root or (a != b and b not in reach(root, avoid=a))

    lists = {
        n: v["list"]["max"]
        for n, v in nodes.items()
        if v["kind"] in ("write", "agent") and "list" in v
    }
    fan = {}  # fanned-out step -> its producer
    for nid in of("step"):
        if "each" in nodes[nid]:
            prod = nodes[nid]["each"].split(".")[0]
            if prod not in lists or not dominates(prod, nid):
                add("fanout_source", nid, "each")
            else:
                fan[nid] = prod
    branches = lambda s: lists[fan[s]] if s in fan else 1
    caps = lambda n: CAPS | n.get("caps", {})
    agents = of("agent")
    runs = sum(n.get("seeds", 1) * branches(nid) * visits(nid) for nid, n in nodes.items()
               if n["kind"] == "step")  # fmt: skip
    calls = sum(visits(nid) for nid, n in nodes.items() if n["kind"] in LLM_KINDS)
    calls += sum(visits(a) * caps(nodes[a])["max_turns"] for a in agents)
    tool_calls = sum(visits(a) * caps(nodes[a])["max_tool_calls"] for a in agents)
    if runs > MAX_RUNS:
        add("too_many_runs")
    if calls > MAX_CALLS:
        add("too_many_calls")
    if root:
        for nid in set(nodes) - reach(root) - {root}:
            add("unreachable", nid)
    outputs = set(of("output"))
    if not outputs:
        add("no_output")
    for nid, n in nodes.items():
        if outputs and n["kind"] != "output" and not reach(nid) & outputs:
            add("dead_end", nid)
    registered = {c["name"]: c for c in custom or []}

    def source(src: str):
        """('input', type), ('takes', at most n), ('text', None | 'custom'), ('item' | 'items', producer) or
        ('any', None) for a field of an unchecked custom node; None when it names nothing of that kind."""
        if src.startswith("$"):
            return ("input", inputs[src[1:]]) if src[1:] in inputs else None
        nid, what = src.split(".")
        n = nodes.get(nid, {})
        k = n.get("kind")
        if what == "takes" and k == "step":
            return "takes", n.get("seeds", 1) * branches(nid)
        # API 1.17: a Re-render snapshot's row bind
        if what in ("first_frame", "last_frame") and k == "step":
            return "frame", nid
        if what == "picked" and k in ("gate", "judge"):
            return "takes", n.get("keep", 1)
        if (what == "reasons" and k == "judge") or (
            what == "notes" and k == "memory" and n["op"] == "read"
        ):
            return "text", None
        if what in ("item", "items") and nid in lists:
            return what, nid
        if k == "custom":
            if n["node"] not in registered:
                return "any", None
            f = registered[n["node"]]["output"]["properties"].get(what)
            fk = _field_kind(f) if f else None
            return (
                ("text", "custom")
                if fk == "text"
                else ("takes", f["maxItems"])
                if fk == "takes"
                else None
            )
        return None

    def before(src: str, nid: str) -> bool:
        return src.startswith("$") or dominates(src.split(".")[0], nid)

    for nid in of("gate") + of("judge"):
        g = nodes[nid]
        t = nodes.get(g["takes"])
        if not t or t["kind"] != "step" or not dominates(g["takes"], nid):
            add("gate_takes", nid)
        elif g["takes"] in fan and t.get("seeds", 1) * branches(g["takes"]) > 8:
            add("fanout_takes", nid)
        elif g.get("keep", 1) > t.get("seeds", 1) * branches(g["takes"]):
            add("gate_keep", nid)
    for nid in of("judge"):
        c = nodes[nid]["criteria"]
        lines = c.get("must", []) + c.get("nice", [])
        if not 1 <= len(lines) <= 12 or len(set(lines)) < len(lines):
            add("judge_criteria", nid)
        m = nodes[nid].get("memory")
        if m is not None and (
            nodes.get(m, {}).get("kind") != "memory"
            or nodes[m]["op"] != "read"
            or not dominates(m, nid)
        ):
            add("bind_source", nid, "memory")
        if nodes[nid].get("model", "auto").startswith(("server:", "remote:")):
            add("model_media", nid)
    for nid in of("write"):
        n = nodes[nid]
        if ("list" in n) == ("to" in n) or ("to" in n and inputs.get(n["to"][1:]) != "Text"):
            add("write_target", nid)
    texts = (
        of("write") + of("decide") + agents + [m for m in of("memory") if nodes[m]["op"] == "write"]
    )
    for nid in texts:
        for src in nodes[nid].get("from", []):
            s = source(src)
            if (not s or s[0] in ("takes", "item", "items") or (s[0] == "input" and s[1] != "Text")
                    or not before(src, nid)):  # fmt: skip
                add("bind_source", nid, "from")
    directors = [a for a in agents if nodes[a].get("director")]
    for a in agents:
        if "rows.edit" in nodes[a].get("tools", []) and not nodes[a].get("director"):
            add("agent_tools", a)
        if len(directors) > 1 and a in directors:
            add("director_count", a)
    for nid in of("custom") if custom is not None else []:
        n = nodes[nid]
        reg = registered.get(n["node"])
        if not reg:
            add("unknown_custom", nid)
            continue
        fields, required = reg["input"]["properties"], set(reg["input"].get("required", []))
        for f in required - set(n.get("inputs", {})) - set(n.get("bind", {})):
            add("custom_input", nid, f)
        for f, v in n.get("inputs", {}).items():
            if f not in fields or next(Draft202012Validator(fields[f]).iter_errors(v), None):
                add("custom_input", nid, f)
        for f, src in n.get("bind", {}).items():
            s = source(src)
            if not s or s[0] in ("item", "items") or not before(src, nid):
                add("bind_source", nid, f)
                continue
            fk = _field_kind(fields[f]) if f in fields else None
            if s[0] != "any" and (fk is None or (fk == "text") != (s[0] == "text" or s == ("input", "Text"))
                                  or (fk == "takes" and (s[0] != "takes" or s[1] > fields[f]["maxItems"]))):  # fmt: skip
                add("custom_input", nid, f)
    if info is not None:
        egress, reserved = _egress(nodes, visits, caps, registered, providers)
        remote = [e["per_row"] for e in egress if e["where"] == "remote"]
        info["bounds"] = {"runs": runs, "llm_calls": calls, "tool_calls": tool_calls,
                          "remote_calls": sum(r["calls"] for r in remote),
                          "remote_tokens": sum(r["tokens"] for r in remote)}  # fmt: skip
        for a in sorted(
            d for d in agents if nodes[d].get("director")
        ):  # per batch, any model source
            c, w = caps(nodes[a]), nodes[a].get("max_wakes", 100)
            info["bounds"]["chat"] = {"calls": w * c["max_turns"] * 2, "tokens": w * c["max_tokens"],
                                      "tool_calls": w * c["max_tool_calls"]}  # fmt: skip
        if any("rows.add" in nodes[a].get("tools", []) for a in agents):
            info["bounds"]["added_rows"] = ADDED_ROWS
        info["egress"], info["reserved"] = egress, reserved
    for nid in outputs:
        src = nodes[nid]["from"]
        if not source(src) or (root and nid not in reach(src.split(".")[0])):
            add("bind_source", nid, "from")
    for nid in of("step"):
        for k, v in nodes[nid].get("bind", {}).items():
            s = source(v)
            if (not s or (s[0] == "text" and s[1] != "custom") or s[0] == "items"
                    or ("[" in k) != (s[0] == "frame")  # `<node>.<param>[<row>]` ← `<step>.first|last_frame` only
                    or (s[0] == "item" and fan.get(nid) != s[1])
                    or (s[0] != "item" and not before(v, nid))):  # fmt: skip
                add("bind_source", nid, k)
    return out


def _egress(nodes, visits, caps, registered, providers) -> tuple[list, list]:
    """What leaves the machine per node (PipelineEgress) and the servers' reserved memory, as check.py."""
    provs = {p["name"]: p for p in providers or [] if p.get("enabled", True)}
    egress, reserved = [], {}
    for nid in sorted(nodes):
        n = nodes[nid]
        if n["kind"] == "custom" and (
            registered.get(n["node"], {}).get("network") or n.get("network")
        ):
            fields = sorted(set(n.get("inputs", {})) | set(n.get("bind", {})))
            egress.append({"node": nid, "where": "network", "provider": None, "model": None,
                           "fields": [f"input:{f}" for f in fields], "thumbnails": 0,
                           "per_row": {"calls": 0, "tokens": 0}, "chat": None})  # fmt: skip
        m = n.get("model", "auto") if n["kind"] in MODEL_KINDS else "auto"
        if (
            not m.startswith(("server:", "remote:")) or n["kind"] == "judge"
        ):  # a judge on one: model_media
            continue
        where, _, rest = m.partition(":")
        name, _, model = rest.partition("/")
        p = provs.get(name)
        if not model and p:
            model = p["default_model"]
        if p and p["kind"] != where:
            continue
        if where == "server" and p:
            reserved[name] = {"provider": name, "host": p["host"], "memory_gb": p["memory_gb"]}
        v = visits(nid)
        if n["kind"] == "agent":
            c = caps(n)
            fields = ["instruction"] + n.get("from", []) + (["routes"] if "routes" in n else [])
            fields += [f"tool:{x}" for x in n.get("tools", [])] + (
                ["chat"] if n.get("director") else []
            )
            per_row = {"calls": v * c["max_turns"] * 2, "tokens": v * c["max_tokens"]}
            mw = n.get("max_wakes", 100)
            chat = n.get("director") and {
                "calls": mw * c["max_turns"] * 2,
                "tokens": mw * c["max_tokens"],
            }
        else:
            fields = (["instruction"] if n["kind"] == "write" else ["question", "routes"]) + n.get(
                "from", []
            )
            out_tokens = OUT_TOKENS["write-list" if "list" in n else n["kind"]]
            per_row, chat = {"calls": v * 2, "tokens": v * 2 * (PROMPT_TOKENS + out_tokens)}, None
        egress.append({"node": nid, "where": where, "provider": name, "model": model or None,
                       "fields": fields, "thumbnails": 0, "per_row": per_row, "chat": chat or None})  # fmt: skip
    return egress, [reserved[k] for k in sorted(reserved)]


def plan(snapshot: dict) -> dict:
    """{pipeline, pipeline_id, nodes, start, next (node → node), branch (gate or judge → {pass, fail, max, then}),
    routes (decide → {routes: {label: node}, max, then: label}), inputs_node, workflows, gates (every node with a
    loop count: gates, judges, decides)}."""
    p = snapshot.get("pipeline")
    if not p:
        step = {"kind": "step", "workflow": snapshot["workflow_id"], "seeds": 1, "inputs": {}}
        step |= {"bind": {}, "seed_param": None, "row_inputs": True}
        return {
            "pipeline": False,
            "pipeline_id": None,
            "nodes": {"step": step, "out": {"kind": "output", "from": "step.takes"}},
            "start": "step",
            "next": {"step": "out"},
            "branch": {},
            "routes": {},
            "inputs_node": None,
            "workflows": {snapshot["workflow_id"]: snapshot["workflow"]},
            "gates": [],
            "fan": {},
            "director": None,
            "providers": {},
            "remote": {"enabled": False, "providers": [], "max_calls": 1, "max_tokens": 1000},
        }
    nodes, nxt, branch, routes = p["nodes"], {}, {}, {}
    for frm, to in p["edges"]:
        if isinstance(to, str):
            nxt[frm] = to
        elif "routes" in to:
            routes[frm] = to
        else:
            branch[frm] = to
    root = next(n for n, v in nodes.items() if v["kind"] == "inputs")
    fan = {
        n: v["each"].split(".")[0]
        for n, v in nodes.items()
        if v["kind"] == "step" and v.get("each")
    }
    director = next(
        (n for n, v in nodes.items() if v["kind"] == "agent" and v.get("director")), None
    )
    return {
        "fan": fan,
        "director": director,
        "providers": p.get("providers") or {},
        "remote": p.get("remote")
        or {"enabled": False, "providers": [], "max_calls": 1, "max_tokens": 1000},
        "pipeline": True,
        "pipeline_id": p.get("id"),
        "nodes": nodes,
        "start": root,
        "next": nxt,
        "branch": branch,
        "routes": routes,
        "inputs_node": root,
        "workflows": p["workflows"],
        "gates": sorted(
            n
            for n, v in nodes.items()
            if v["kind"] in ("gate", "judge", "decide")
            or (v["kind"] == "agent" and v.get("routes"))
        ),
    }
