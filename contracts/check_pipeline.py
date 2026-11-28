"""contracts/check.py, pipeline rules (README, "Pipeline rules").
Moved out of check.py as is; check.py re-exports every name."""

import json
import re

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry

from check_core import HERE, OPENAPI, fail, image_field, media_field, value_error
from check_graph import graph_problems, passthrough, workflow_media
from check_mentions import look_fit

# ---------------------------------------------------------------- pipeline rules (README, "Pipeline rules")
P_MAX_NODES, P_MAX_BYTES, P_MAX_RUNS, P_MAX_CALLS = 24, 65536, 64, 32
P_RUN_ONLY = {"unreachable", "dead_end", "no_output", "unknown_workflow", "workflow_cannot_run", "unknown_preset",
              "step_input_invalid", "bind_unknown", "bind_type", "unbound_input", "step_seeds", "unknown_look", "look_fit",
              "unknown_model", "remote_not_enabled", "remote_off", "unknown_custom", "custom_input"}
INPUT_FITS = {"Text": {"string", "text"}, "Int": {"int"}, "Float": {"float"}, "Seed": {"seed"}, "Image": {"image"}}
LLM_KINDS = ("judge", "write", "decide")  # nodes that ask a model once per visit (README, Agent nodes)
MODEL_KINDS = LLM_KINDS + ("agent",)  # nodes with a `model` (README, Model sources)
PROMPT_TOKENS = 8192  # a prompt's token bound for the remote worst case: 32768 characters / 4 (README, Model sources)
WRITE_TOKENS = {"write": 1400, "write-list": 4096, "decide": 800}
UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


def settable(wf: dict, palette: dict) -> dict[str, dict]:
    """`<node>.<param>` → param schema for every unwired param of a known kind: what a step may set or bind."""
    return {f"{nid}.{p['name']}": p for nid, n in wf["nodes"].items() if n["kind"] in palette
            for p in palette[n["kind"]]["params"] if p["name"] not in n.get("in", {})}


def pipeline_problems(doc: dict, workflows: dict[str, dict], palette: dict, looks: list[dict] | None = None,
                      loras: dict[str, str] | None = None, info: dict | None = None, providers: list[dict] | None = None,
                      remote_on: bool = False, custom: list[dict] | None = None, auto: str | None = None) -> set[tuple]:
    """Every problem as (code, node, port, param); workflows: the id → WorkflowDoc the caller can run; looks, loras:
    the looks and LoRAs the caller can use now (as resolve_mentions takes them); providers: the enabled model
    providers (name, kind, models, default_model, host, memory_gb); remote_on: the deployment's remote switch;
    custom: the registered custom nodes; auto (from 1.74): the pipelines default model as resolved and usable now
    (`server:<n>/<m>`, `remote:<p>/<m>`; None: the local LLM), what a write, decide or agent node's auto runs on. `info`, when given, receives the worst case per row (`bounds`), the model
    nodes' models as resolved (`models`), what leaves the machine (`egress`) and the servers' memory (`reserved`)."""
    out, nodes = set(), doc["nodes"]
    add = lambda code, node=None, port=None, param=None: out.add((code, node, port, param))
    if len(nodes) > P_MAX_NODES:
        add("too_many_nodes")
    if len(json.dumps(doc, separators=(",", ":"), ensure_ascii=False).encode()) > P_MAX_BYTES:
        add("too_large")
    if out:
        return out
    of = lambda kind: [nid for nid, n in nodes.items() if n["kind"] == kind]
    roots, inputs = of("inputs"), {}
    if len(roots) != 1:
        add("inputs_node")
    for nid in roots:
        names = [i["name"] for i in nodes[nid]["inputs"]]
        for name in {x for x in names if names.count(x) > 1}:
            add("duplicate_input", nid, param=name)
        inputs |= {i["name"]: i["type"] for i in nodes[nid]["inputs"]}
    routers = lambda nid: nodes[nid]["kind"] == "decide" or (nodes[nid]["kind"] == "agent" and "routes" in nodes[nid])
    succ, outdeg, capped, routed = {nid: [] for nid in nodes}, {}, [], set()  # succ: node → [(to, capped)]
    for frm, to in doc["edges"]:
        kind = "plain" if isinstance(to, str) else "routes" if "routes" in to else "branch"
        targets = [to] if kind == "plain" else list(to["routes"].values()) if kind == "routes" else [
            to["pass"], to["fail"]] + ([to["then"]] if "then" in to else [])
        if frm not in nodes or any(t not in nodes for t in targets):
            add("unknown_node", frm)
            continue
        fk = nodes[frm]["kind"]
        if (kind == "branch" and fk not in ("gate", "judge")) or (kind == "routes" and fk not in ("decide", "agent")):
            add("branch_not_gate", frm)
            continue
        if kind == "branch" and "then" not in to and fk != "judge":  # API 1.28: only a judge's may Ask me
            add("edge_invalid", frm)
            continue
        if (routers(frm) and (kind != "routes" or set(to["routes"]) != set(nodes[frm]["routes"])
                              or to["then"] not in to["routes"])) or (fk == "agent" and kind == "routes" and not routers(frm)):
            add("decide_routes", frm)
            continue
        outdeg[frm] = outdeg.get(frm, 0) + 1
        if outdeg[frm] > 1 or fk == "output" or any(nodes[t]["kind"] == "inputs" for t in targets):
            add("edge_invalid", frm)
            continue
        if kind == "plain":
            succ[frm].append((to, False))
        elif kind == "branch":
            succ[frm] += [(to["pass"], False), (to["fail"], True)] + ([(to["then"], False)] if "then" in to else [])
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

    def reach(src: str, avoid: str | None = None, uncapped_only: bool = False) -> set[str]:
        """Nodes reachable from src in one edge or more (never through `avoid`)."""
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
    visits = lambda nid: 1 + sum(m for g, f, m in capped if (nid == f or nid in reach(f)) and g in reach(nid))
    root = roots[0] if len(roots) == 1 else None
    # a before b on every path from the inputs node (unchecked without exactly one inputs node)
    dominates = lambda a, b: root is None or a == root or (a != b and b not in reach(root, avoid=a))
    lists = {nid: n["list"]["max"] for nid, n in nodes.items() if n["kind"] in ("write", "agent") and "list" in n}
    fan = {}  # fanned-out step → its producer
    for nid in of("step"):
        if "each" in nodes[nid]:
            prod = nodes[nid]["each"].split(".")[0]
            if prod not in lists or not dominates(prod, nid):
                add("fanout_source", nid, "each")
            else:
                fan[nid] = prod
    branches = lambda s: lists[fan[s]] if s in fan else 1
    caps = lambda n: {"max_turns": 6, "max_tool_calls": 12, "max_tokens": 16000} | n.get("caps", {})
    agents = of("agent")
    runs = sum(n.get("seeds", 1) * branches(nid) * visits(nid) for nid, n in nodes.items() if n["kind"] == "step")
    calls = sum(visits(nid) for nid, n in nodes.items() if n["kind"] in LLM_KINDS) + sum(
        visits(a) * caps(nodes[a])["max_turns"] for a in agents)
    tool_calls = sum(visits(a) * caps(nodes[a])["max_tool_calls"] for a in agents)
    if runs > P_MAX_RUNS:
        add("too_many_runs")
    if calls > P_MAX_CALLS:
        add("too_many_calls")
    if root:
        for nid in set(nodes) - reach(root) - {root}:
            add("unreachable", nid)
    outputs = of("output")
    if not outputs:
        add("no_output")
    for nid in nodes:
        if outputs and nodes[nid]["kind"] != "output" and not reach(nid) & set(outputs):
            add("dead_end", nid)
    registered = {c["name"]: c for c in (custom or [])}

    def field_kind(schema: dict) -> str | None:
        """A custom field a source may fill: text (a string), takes (a list of take ids, n = maxItems ≤ 8), else None."""
        if schema.get("type") == "string":
            return "text"
        it = schema.get("items", {})
        if schema.get("type") == "array" and it.get("type") == "string" and it.get("format") == "uuid" and schema.get("maxItems", 99) <= 8:
            return "takes"
        return None

    def source(src: str) -> tuple[str, str | int | None] | None:
        """('input', type), ('takes', how many at most), ('text', None), ('item', producer), ('items', producer),
        ('any', None) for a field of an unknown custom node; None when it names nothing of that kind."""
        if src.startswith("$"):
            return ("input", inputs[src[1:]]) if src[1:] in inputs else None
        nid, what = src.split(".")
        k, n = nodes.get(nid, {}).get("kind"), nodes.get(nid, {})
        if what == "takes" and k == "step":
            return "takes", n.get("seeds", 1) * branches(nid)
        if what == "picked" and k in ("gate", "judge"):
            return "takes", n.get("keep", 1)
        if (what == "reasons" and k == "judge") or (what == "notes" and k == "memory" and n["op"] == "read"):
            return "text", None
        if what in ("item", "items") and nid in lists:
            return what, nid
        if k == "custom":
            if n["node"] not in registered:
                return "any", None
            f = registered[n["node"]]["output"]["properties"].get(what)
            fk = field_kind(f) if f else None
            return ("text", "custom") if fk == "text" else ("takes", f["maxItems"]) if fk == "takes" else None
        return None

    def text_sources(nid: str) -> None:
        """write, decide, memory write and agent `from`: Text inputs and text sources that come before the node."""
        for src in nodes[nid].get("from", []):
            s = source(src)
            if not s or s[0] in ("takes", "item", "items") or (s[0] == "input" and s[1] != "Text") or (
                    not src.startswith("$") and not dominates(src.split(".")[0], nid)):
                add("bind_source", nid, "from")

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
        if m is not None and (nodes.get(m, {}).get("kind") != "memory" or nodes[m]["op"] != "read" or not dominates(m, nid)):
            add("bind_source", nid, "memory")
        if nodes[nid].get("model", "auto").startswith(("server:", "remote:")):
            add("model_media", nid)
    for nid in of("write"):
        n = nodes[nid]
        if ("list" in n) == ("to" in n) or ("to" in n and inputs.get(n["to"][1:]) != "Text") or (
                n.get("form") == "script" and "list" in n):  # 1.32: a script is one text
            add("write_target", nid)
    for nid in of("write") + of("decide") + of("agent") + [m for m in of("memory") if nodes[m]["op"] == "write"]:
        text_sources(nid)
    directors = [a for a in agents if nodes[a].get("director")]
    for a in agents:
        if "rows.edit" in nodes[a].get("tools", []) and not nodes[a].get("director"):
            add("agent_tools", a)
        if len(directors) > 1 and a in directors:
            add("director_count", a)

    def text_workflow(wid: str) -> bool:
        return wid in workflows and workflow_media(workflows[wid]["nodes"], palette) == "text"

    def family_ask(step: str | None) -> str | None:
        """The ask template of the step's workflow's family (first by id), else llm-ask (README, Agent nodes: model)."""
        fam = workflows.get(nodes[step]["workflow"], {}).get("family") if step in nodes and nodes[step]["kind"] == "step" else None
        own = sorted(w for w in workflows if not UUID.match(w) and workflows[w]["family"] == fam and text_workflow(w))
        return own[0] if fam and own else "llm-ask" if text_workflow("llm-ask") else None

    remote = {"enabled": False, "providers": []} | doc.get("remote", {})
    provs = {p["name"]: p for p in providers or [] if p.get("enabled", True)}

    def remote_ok(nid: str, name: str | None) -> bool:
        """The deployment's switch, then the pipeline's (README, Model sources); adds the problem when not."""
        if not remote_on:
            add("remote_off", nid)
        elif not remote["enabled"] or (name is not None and name not in remote["providers"]):
            add("remote_not_enabled", nid)
        else:
            return True
        return False

    models = {}
    for nid in [n for n in nodes if nodes[n]["kind"] in MODEL_KINDS]:
        n, m = nodes[nid], nodes[nid].get("model", "auto")
        if m.startswith(("server:", "remote:")):
            where, _, rest = m.partition(":")
            name, _, model = rest.partition("/")
            p = provs.get(name)
            if where == "remote" and not remote_ok(nid, name):
                continue
            wid = f"{where}:{name}/{model or p['default_model']}" if p and p["kind"] == where and (
                model or p["default_model"]) in p["models"] else None
        elif m != "auto":
            wid = m if text_workflow(m) else None
        elif n["kind"] == "judge":  # a judge sends thumbnails: always its family's vision model
            wid = family_ask(n["takes"])
        elif auto:  # 1.74: the admin's default for every pipeline (no pipeline remote switch)
            wid = auto
        else:  # the step nearest before it: the one every other step before it comes before
            doms = [s for s in of("step") if dominates(s, nid) and s != nid]
            wid = family_ask(next((d for d in doms if all(o == d or dominates(o, d) for o in doms)), None))
        if wid:
            models[nid] = wid
        else:
            add("unknown_model", nid)
    for nid in of("custom"):
        n = nodes[nid]
        reg = registered.get(n["node"])
        if not reg:
            add("unknown_custom", nid)
            continue
        if reg["network"]:
            remote_ok(nid, None)
        fields, required = reg["input"]["properties"], set(reg["input"].get("required", []))
        for f in required - set(n.get("inputs", {})) - set(n.get("bind", {})):
            add("custom_input", nid, f)
        for f, v in n.get("inputs", {}).items():
            if f not in fields or next(Draft202012Validator(fields[f], format_checker=FormatChecker()).iter_errors(v), None):
                add("custom_input", nid, f)
        for f, src in n.get("bind", {}).items():
            s = source(src)
            if not s or s[0] in ("item", "items") or (not src.startswith("$") and not dominates(src.split(".")[0], nid)):
                add("bind_source", nid, f)
                continue
            fk = field_kind(fields[f]) if f in fields else None
            if s[0] != "any" and (fk is None or (fk == "text") != (s[0] == "text" or s == ("input", "Text"))
                                  or (fk == "takes" and (s[0] != "takes" or s[1] > fields[f]["maxItems"]))):
                add("custom_input", nid, f)

    egress, reserved = [], {}
    for nid in sorted(nodes):
        n = nodes[nid]
        src = models.get(nid, "")
        if n["kind"] == "custom" and registered.get(n["node"], {}).get("network"):
            egress.append({"node": nid, "where": "network", "provider": None, "model": None,
                           "fields": [f"input:{f}" for f in sorted(set(n.get("inputs", {})) | set(n.get("bind", {})))],
                           "thumbnails": 0, "per_row": {"calls": 0, "tokens": 0}, "chat": None})
        if not src.startswith(("server:", "remote:")) or n["kind"] == "judge":  # a judge on one is model_media
            continue
        where, _, rest = src.partition(":")
        name, _, model = rest.partition("/")
        if where == "server":
            reserved[name] = {"provider": name, "host": provs[name]["host"], "memory_gb": provs[name]["memory_gb"]}
        v = visits(nid)
        if n["kind"] == "agent":
            c = caps(n)
            fields = ["instruction"] + n.get("from", []) + (["routes"] if "routes" in n else []) + [
                f"tool:{x}" for x in n.get("tools", [])] + (["chat"] if n.get("director") else [])
            per_row = {"calls": v * c["max_turns"] * 2, "tokens": v * c["max_tokens"]}
            mw = n.get("max_wakes", 100)
            chat = {"calls": mw * c["max_turns"] * 2, "tokens": mw * c["max_tokens"]} if n.get("director") else None
        else:
            fields = (["instruction"] if n["kind"] == "write" else ["question", "routes"]) + n.get("from", [])
            out_tokens = WRITE_TOKENS["write-list" if "list" in n else n["kind"]]
            per_row, chat = {"calls": v * 2, "tokens": v * 2 * (PROMPT_TOKENS + out_tokens)}, None
        egress.append({"node": nid, "where": where, "provider": name, "model": model, "fields": fields,
                       "thumbnails": 0, "per_row": per_row, "chat": chat})
    if info is not None:
        remote_rows = [e["per_row"] for e in egress if e["where"] == "remote"]
        bounds = {"runs": runs, "llm_calls": calls, "tool_calls": tool_calls,
                  "remote_calls": sum(r["calls"] for r in remote_rows),
                  "remote_tokens": sum(r["tokens"] for r in remote_rows)}
        for d in sorted(nid for nid, n in nodes.items() if n["kind"] == "agent" and n.get("director")):
            c, w = caps(nodes[d]), nodes[d].get("max_wakes", 100)
            bounds["chat"] = {"calls": w * c["max_turns"] * 2, "tokens": w * c["max_tokens"],
                              "tool_calls": w * c["max_tool_calls"]}
        if any(n["kind"] == "agent" and "rows.add" in n.get("tools", []) for n in nodes.values()):
            bounds["added_rows"] = 100
        info.update(bounds=bounds,
                    models=models, egress=egress, reserved=[reserved[k] for k in sorted(reserved)])
    for nid in outputs:
        src = nodes[nid]["from"]
        if not source(src) or (root and nid not in reach(src.split(".")[0])):
            add("bind_source", nid, "from")
    for nid in of("step"):
        n = nodes[nid]
        binds = {k: source(v) for k, v in n.get("bind", {}).items()}
        for k, v in n.get("bind", {}).items():
            s = binds[k]
            if (not s or (s[0] == "text" and s[1] != "custom") or s[0] == "items"
                    or (s[0] == "item" and fan.get(nid) != s[1])
                    or (not v.startswith("$") and s[0] != "item" and not dominates(v.split(".")[0], nid))):
                add("bind_source", nid, k)
                binds[k] = None
            elif s[0] in ("item", "text"):
                binds[k] = ("input", "Text")  # a branch's item, a custom text: bound like a Text input
        wf = workflows.get(n["workflow"])
        if not wf:
            add("unknown_workflow", nid)
            continue
        if graph_problems(wf, palette) or text_workflow(n["workflow"]):
            add("workflow_cannot_run", nid)  # a text workflow answers a judge, write or decide only
        if "preset" in n and n["preset"] not in wf.get("studio", {}).get("presets", {}):
            add("unknown_preset", nid)
        found = {x["id"]: x for x in looks or []}
        if any(lid not in found for lid in n.get("looks", [])):
            add("unknown_look", nid, param="looks")
        elif any(look_fit(wf["nodes"], palette, found[lid] | {"kind": "look"}, loras or {}) for lid in n.get("looks", [])):
            add("look_fit", nid, param="looks")
        params = settable(wf, palette)
        for k, v in n.get("inputs", {}).items():
            wn, p = k.split(".")
            if p == "bypass":
                kind = wf["nodes"].get(wn, {}).get("kind")
                if kind not in palette or passthrough(palette[kind]) is None or not isinstance(v, bool):
                    add("step_input_invalid", nid, param=k)
            elif k not in params or value_error(params[k], v):
                add("step_input_invalid", nid, param=k)
        for k, src in binds.items():
            if k not in params:
                add("bind_unknown", nid, k)
                continue
            p = params[k]
            if src and src[0] == "any":
                continue
            if src and src[0] == "input":
                fits = p["type"] in INPUT_FITS[src[1]] or (src[1] == "Image" and image_field(p) is not None)
            elif src:
                timed = any(isinstance(v, dict) and v.get("position") == "seconds" for v in p.get("item", {}).values())
                fits = (p["type"] in ("image", "media") and src[1] == 1) or (
                    image_field(p) is not None and src[1] <= (1 if timed else p["max"])) or (
                    media_field(p) is not None and src[1] == 1)  # 1.32: one take as item 1 (H3 audios)
            if src and not fits:
                add("bind_type", nid, k)
        for k, p in params.items():  # a param of an optional node (API 1.22) may stay empty
            if (p["type"] in ("image", "media", "span") or (image_field(p) and p.get("min", 0) >= 1)) and k not in binds \
                    and k not in n.get("inputs", {}) and not wf["nodes"][k.split(".")[0]].get("optional"):
                add("unbound_input", nid, k)
        if n.get("seeds", 1) > 1 and not any(p["type"] == "seed" for p in params.values()):
            add("step_seeds", nid)
    return out


def check_pipelines(registry: Registry, palette: dict) -> None:
    g = HERE / "fixtures" / "pipelines"
    ref = lambda n: Draft202012Validator(
        {"$ref": f"{OPENAPI}#/components/schemas/{n}"}, registry=registry, format_checker=FormatChecker())
    workflows = json.loads((g / "workflows.json").read_text())["workflows"]
    for wid, wf in workflows.items():
        for e in ref("WorkflowDoc").iter_errors(wf):
            fail(f"fixtures/pipelines/workflows.json {wid}: {e.json_path}: {e.message}")
    custom = json.loads((HERE / "fixtures" / "registry" / "pipelines.custom.valid.json").read_text())["nodes"]
    doc_v = ref("PipelineDoc")
    for p in sorted(g.glob("*.json")):
        if p.name == "workflows.json":
            continue
        fx, name = json.loads(p.read_text()), f"fixtures/pipelines/{p.name}"
        if errs := list(doc_v.iter_errors(fx["pipeline"])):
            fail(f"{name}: pipeline is not a PipelineDoc: {errs[0].json_path}: {errs[0].message}")
            continue
        info = {}
        got = pipeline_problems(fx["pipeline"], workflows, palette, fx.get("looks"), fx.get("loras"), info,
                                fx.get("providers"), fx.get("remote_deployment", False), fx.get("custom", custom),
                                fx.get("auto_model"))
        want = {(x["code"], x.get("node"), x.get("port"), x.get("param")) for x in fx["problems"]}
        if not p.name.startswith(("valid.", "invalid.")) or p.name.startswith("valid.") != (not want):
            fail(f"{name}: valid.* has no problems, invalid.* has some")
        if got != want:
            fail(f"{name}: got {sorted(got, key=str)}, want {sorted(want, key=str)}")
        elif bad := [k for k in ("bounds", "models", "egress", "reserved") if k in fx and fx[k] != info.get(k)]:
            fail(f"{name}: got {', '.join(f'{k} {info.get(k)}' for k in bad)}; want {', '.join(f'{k} {fx[k]}' for k in bad)}")
        else:
            print(f"ok   {name}")
