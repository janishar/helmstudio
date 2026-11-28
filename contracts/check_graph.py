"""contracts/check.py, graph rules (README, "Graph rules"), and the canonical registries fitting together.
Moved out of check.py as is; check.py re-exports every name."""

import json

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry

from check_core import (
    CANONICAL,
    HANDLES,
    HERE,
    OPENAPI,
    fail,
    fits,
    graph_errors,
    graph_values,
    group_errors,
    lora_field,
    only_errors,
    schema_errors,
    standard_errors,
    upload_limits,
    value_error,
    values_by_errors,
)


def check_canonical_registries() -> None:
    """The canonical fixtures (a runner publishes them verbatim) must fit together."""
    reg = HERE / "fixtures" / "registry"
    for prefix in CANONICAL:
        nodes = json.loads((reg / f"{prefix}nodes.valid.json").read_text())["nodes"]
        kinds = {n["kind"]: n for n in nodes}
        models = reg / f"{prefix}models.valid.json"
        extra = {m["family"]: {a["component"] for a in m.get("addons", [])}
                 for m in (json.loads(models.read_text())["models"] if models.exists() else [])}  # fmt: skip
        for n in nodes:
            for e in schema_errors(n):
                fail(f"fixtures/registry/{prefix}nodes.valid.json {n['kind']}.{e}")
            for p in n["params"]:  # API 1.41: each an add-on of the family's models entry
                got = {c for cs in [*p.get("addons", {}).values(),
                                    *(c for by in p.get("addons_with", {}).values() for c in by.values())] for c in cs}
                if bad := got - extra.get(n.get("family"), set()):
                    fail(f"fixtures/registry/{prefix}nodes.valid.json {n['kind']}.{p['name']}: addons names no add-on "
                         f"of {prefix}models.valid.json {sorted(bad)}")
            for p in n["params"]:
                if err := value_error(p, p["default"]):
                    fail(
                        f"fixtures/registry/{prefix}nodes.valid.json {n['kind']}.{p['name']} default: {err}"
                    )
            for p in n["params"]:
                if p["type"] == "int" and "step" in p and (p["default"] - p.get("min", 0)) % p["step"]:
                    fail(f"fixtures/registry/{prefix}nodes.valid.json {n['kind']}.{p['name']} default: off its step grid")
                fps = next((q for q in n["params"] if q["name"] == p.get("fps")), None)
                if p["type"] == "frames" and (not fps or fps["type"] not in ("int", "float")):
                    fail(f"fixtures/registry/{prefix}nodes.valid.json {n['kind']}.{p['name']}: fps names no int/float param")
        for wf in json.loads((reg / f"{prefix}workflows.valid.json").read_text())["workflows"]:
            errs = graph_errors(wf, kinds)
            if wf.get("media", "image") != workflow_media(wf["nodes"], kinds):
                errs.append(f"media {wf.get('media', 'image')!r} is not what its graph makes")
            # a template's studio block obeys the rules of a user workflow's (README, User workflows)
            errs += [f"studio: {p}" for p in graph_problems(wf, {k: v | {"envs": [wf["env"]]} for k, v in kinds.items()})
                     if p[0].startswith(("studio_", "preset_"))]
            errs += group_errors(wf.get("studio") or {})
            errs += standard_errors(wf, kinds)
            for e in errs:
                fail(f"fixtures/registry/{prefix}workflows.valid.json {wf['id']}: {e}")
            if not errs:
                print(f"ok   workflow {wf['id']} fits {prefix}nodes.valid.json")
    every = [n for prefix in CANONICAL for n in json.loads((reg / f"{prefix}nodes.valid.json").read_text())["nodes"]]
    for nodes, want in ((every, 8192), ([n for n in every if not n["kind"].startswith("minimax-h3-image/")], 4096), ([], 16384)):
        if (got := upload_limits(nodes)) != {"image_max_side": want}:
            fail(f"upload_limits: {got}, want image_max_side {want}")
    print("ok   upload_limits over the canonical registries")


def workflow_media(nodes: dict, kinds: dict) -> str:
    """text when an answer node's text is wired (README, The llm pack); else video when any output node's media is
    wired from a Video output, else image when any from an Image, else (API 1.30) audio when any from an Audio; else
    image (README, Video; Voices). Not node order: the first type of that list that any output has."""
    if any(n["kind"] == "answer" and "text" in n.get("in", {}) for n in nodes.values()):
        return "text"
    got = {t for t, _ in output_sources(nodes, kinds)}
    if workflow_sound(nodes, kinds):  # API 1.79: a classed sound wins over a second Video output (MMAudio's video)
        got.discard("Video")
    return next((t.lower() for t in ("Video", "Image", "Audio") if t in got), "image")


def output_sources(nodes: dict, kinds: dict) -> list[tuple[str, dict]]:
    """(type, source node schema) of every output node's media wire, node id order."""
    out = []
    for nid in sorted(nodes):
        n = nodes[nid]
        ref = n.get("in", {}).get("media") if n["kind"] == "output" else None
        src = nodes.get(ref.split(".")[0]) if ref else None
        ks = kinds.get(src["kind"]) if src else None
        out += [(o["type"], ks) for o in ks["outputs"] if o["name"] == ref.split(".")[1]] if ks else []
    return out


def workflow_sound(nodes: dict, kinds: dict) -> str | None:
    """API 1.79 (README, Music and sound): the `sound` of the first node (output node id order) whose Audio output an
    output node takes; None when none has one."""
    return next((ks["sound"] for t, ks in output_sources(nodes, kinds) if t == "Audio" and ks.get("sound")), None)


def spoken(nodes: dict, kinds: dict) -> bool:
    """The voice rules (README, Voices) hold for an audio workflow of class voice, or (API 1.79) of no class."""
    return workflow_media(nodes, kinds) == "audio" and workflow_sound(nodes, kinds) in (None, "voice")
WIREABLE = {"int": "Int", "float": "Float", "seed": "Seed", "string": "Text", "text": "Text", "image": "Image"}
RUN_ONLY = {"unknown_kind", "env_mismatch", "required_input", "no_output", "too_many_outputs", "too_many_seeds",
            "lora_unknown", "lora_family"}  # every other code blocks saving
MAX_NODES, MAX_BYTES, MAX_OUTPUTS = 64, 262144, 4


def family(kind: str) -> str | None:
    return kind.split("/")[0] if "/" in kind else None


def passthrough(schema: dict) -> list[str] | None:
    """The input each output passes through when bypassed (first input of its type), or None: not bypassable."""
    through = []
    for o in schema["outputs"]:
        i = next((i["name"] for i in schema["inputs"] if i["type"] == o["type"]), None)
        if i is None:
            return None
        through.append(i)
    return list(dict.fromkeys(through)) or None


def port_type(schema: dict, name: str) -> str | None:
    """The type a wire into `name` must carry: an input's type, or a wireable value param's port type."""
    t = next((i["type"] for i in schema["inputs"] if i["name"] == name), None)
    p = next((p for p in schema["params"] if p["name"] == name), None)
    return t or (WIREABLE.get(p["type"]) if p else None)


LOOK_GROUPS = ("film", "camera", "colour", "lighting")
TILES = {"elements"} | {f"look:{g}" for g in LOOK_GROUPS}  # studio.quick library tiles (README, Looks)


def graph_problems(doc: dict, palette: dict[str, dict], loras: dict[str, str] | None = None) -> set[tuple]:
    """Every problem as (code, node, port, param); palette: kind → schema with `envs` (the live kinds); loras: the
    caller's usable LoRAs (own ready ones, shared ones a live host of the env lists) as id → detected family."""
    loras = loras or {}
    out, nodes = set(), doc["nodes"]
    add = lambda code, node=None, port=None, param=None: out.add((code, node, port, param))
    if len(nodes) > MAX_NODES:
        add("too_many_nodes")
    if len(json.dumps(doc, separators=(",", ":"), ensure_ascii=False).encode()) > MAX_BYTES:
        add("too_large")
    if out:
        return out  # nothing else is checked: the work stays bounded
    if sum(n["kind"] == "output" for n in nodes.values()) > MAX_OUTPUTS:
        add("too_many_outputs")
    seeds = [p for n in nodes.values() if n["kind"] in palette
             for p in palette[n["kind"]]["params"] if p["type"] == "seed" and p["name"] not in n.get("in", {})]
    if len(seeds) > 1:
        add("too_many_seeds")
    vals = graph_values(nodes, palette)
    for nid, n in nodes.items():
        kind, wires = n["kind"], n.get("in", {})
        if family(kind) and family(kind) != doc["family"]:
            add("wrong_family", nid)
        s = palette.get(kind)
        if not s:
            add("unknown_kind", nid)
            continue
        if doc["env"] not in s["envs"]:
            add("env_mismatch", nid)
        params = {p["name"]: p for p in s["params"]}
        for k, v in n.get("params", {}).items():
            if k not in params:
                add("unknown_param", nid, param=k)
            elif value_error(params[k], v):
                add("param_invalid", nid, param=k)
            elif lora_field(params[k]):  # README, LoRA rules
                ids = [r[lora_field(params[k])]["lora_id"] for r in v]
                if len(set(ids)) < len(ids):
                    add("lora_duplicate", nid, param=k)
                if not n.get("bypass"):
                    if any(i not in loras for i in ids):
                        add("lora_unknown", nid, param=k)
                    if any(i in loras and loras[i] != family(kind) for i in ids):
                        add("lora_family", nid, param=k)
        for k, _ in values_by_errors(s, n.get("params", {}), vals):
            if k not in n.get("params", {}) or not value_error(params[k], n["params"][k]):
                add("param_invalid", nid, param=k)
        for k, _ in only_errors(s, n):
            add("param_invalid", nid, param=k)
        for port, ref in wires.items():
            want = port_type(s, port)
            if not want:
                add("unknown_input", nid, port)
                continue
            src_id, out_name = ref.split(".")
            src = nodes.get(src_id)
            ss = palette.get(src["kind"]) if src else None
            if src and not ss:
                continue  # an unknown kind's outputs are not checked
            got = next((o["type"] for o in ss["outputs"] if o["name"] == out_name), None) if ss else None
            if not got:
                add("unknown_source", nid, port)
            elif not fits(got, want) or (want in HANDLES and family(src["kind"]) != family(kind)):
                add("type_mismatch", nid, port)
        if n.get("bypass"):
            through = passthrough(s)
            if through is None:
                add("not_bypassable", nid)
            for i in through or []:
                if i not in wires:
                    add("required_input", nid, i)
        else:
            for i in s["inputs"]:
                if not i.get("optional") and i["name"] not in wires:
                    add("required_input", nid, i["name"])
    for nid, n in nodes.items():  # an optional node (API 1.22): an image source whose wires all go to optional inputs
        required = lambda m, port: any(i["name"] == port and not i.get("optional")
                                       for i in palette.get(nodes[m]["kind"], {}).get("inputs", []))
        if n.get("optional") and (n["kind"] != "image" or any(
                required(m, port) for m, x in nodes.items() for port, ref in x.get("in", {}).items()
                if ref.split(".")[0] == nid)):
            add("not_optional", nid)
    succ = {nid: set() for nid in nodes}
    for nid, n in nodes.items():
        for ref in n.get("in", {}).values():
            if ref.split(".")[0] in nodes:
                succ[ref.split(".")[0]].add(nid)
    for nid in nodes:  # on a cycle = reachable from itself; ponytail: O(n·e), fine at 64 nodes
        seen, todo = set(), list(succ[nid])
        while todo:
            x = todo.pop()
            if x not in seen:
                seen.add(x)
                todo += succ[x]
        if nid in seen:
            add("cycle", nid)
    if not any(n["kind"] in ("output", "answer") for n in nodes.values()):
        add("no_output")  # an ask workflow ends in an answer node (README, The llm pack)

    def bad_path(path: str) -> bool:
        nid, param = path.split(".")
        n = nodes.get(nid)
        s = palette.get(n["kind"]) if n else None
        if n and not s:
            return False  # an unknown kind's params are not checked
        if not n:
            return True
        if param == "bypass":
            return passthrough(s) is None
        return param not in {p["name"] for p in s["params"]} or param in n.get("in", {})

    studio = doc.get("studio", {})
    placed = studio.get("composer", []) + studio.get("quick", [])
    placed += [p for sec in studio.get("inspector", []) for p in sec["items"]]
    for path in placed + list(studio.get("labels", {})):
        if path not in TILES and bad_path(path):
            add("studio_unknown_path", *path.split(".")[:1], param=path.split(".")[1])
    for path in {p for p in placed if placed.count(p) > 1}:  # a tile twice: no node, param = the tile
        nid, param = (None, path) if path in TILES else path.split(".")
        add("studio_duplicate", nid, param=param)
    sets = list(studio.get("presets", {}).values())  # a choice's option sets are checked as presets (API 1.26)
    sets += [o["set"] for sec in studio.get("inspector", []) for c in sec.get("choices", []) for o in c["options"]]
    for values in sets:
        for path, v in values.items():
            nid, param = path.split(".")
            if bad_path(path):
                add("preset_invalid", nid, param=param)
            elif nid in nodes and nodes[nid]["kind"] in palette:
                s = palette[nodes[nid]["kind"]]
                spec = {"type": "bool"} if param == "bypass" else next(p for p in s["params"] if p["name"] == param)
                merged = nodes[nid].get("params", {}) | {k.split(".")[1]: x for k, x in values.items()
                                                         if k.split(".")[0] == nid and k.split(".")[1] != "bypass"}
                if (value_error(spec, v) or param in dict(values_by_errors(s, merged, graph_values(nodes, palette, values)))
                        or param in dict(only_errors(s, nodes[nid] | {"params": merged}))):
                    add("preset_invalid", nid, param=param)
    return out


def autowire(doc: dict, palette: dict, selected: str | None, new_id: str, kind: str) -> dict[str, str]:
    """The `in` of a node just added (README, Auto-wiring)."""
    nodes, wired = doc["nodes"], {}

    def candidates(ids: list[str], t: str) -> list[str]:
        return [f"{m}.{o['name']}" for m in ids if m != new_id and nodes[m]["kind"] in palette
                for o in palette[nodes[m]["kind"]]["outputs"]
                if fits(o["type"], t) and (t not in HANDLES or family(nodes[m]["kind"]) == family(kind))]

    succ = {}
    for nid, n in nodes.items():
        for ref in n.get("in", {}).values():
            succ.setdefault(ref.split(".")[0], set()).add(nid)
    reached, todo = set(), list(succ.get(new_id, ()))
    while todo:  # what the new node already feeds (a wire-drop from an input): never a source, or it makes a cycle
        if (x := todo.pop()) not in reached:
            reached.add(x)
            todo += succ.get(x, ())
    nodes = {m: n for m, n in nodes.items() if m not in reached}
    for i in palette[kind]["inputs"]:
        if i.get("optional"):
            continue
        for scope in ([[selected]] if selected in nodes else []) + [list(nodes)]:
            if len(c := candidates(scope, i["type"])) == 1:
                wired[i["name"]] = c[0]
                break
    return wired


def wire_drop(doc: dict, palette: dict, node: str, port: str, side: str) -> list[str]:
    """The kinds offered when a wire from (node, port) is dropped on empty canvas (README, Wire-drop search)."""
    src_kind = doc["nodes"][node]["kind"]
    s = palette[src_kind]
    t = next(o["type"] for o in s["outputs"] if o["name"] == port) if side == "output" else port_type(s, port)
    hits = []
    for k, ks in palette.items():
        if doc["env"] not in ks["envs"] or family(k) not in (doc["family"], None):
            continue
        if t in HANDLES and family(k) != family(src_kind):
            continue
        if side == "input":
            hit = any(fits(o["type"], t) for o in ks["outputs"])
        else:
            hit = any(fits(t, x) for x in [i["type"] for i in ks["inputs"]] + [WIREABLE.get(p["type"]) for p in ks["params"]])
        if hit:
            hits.append((family(k) != doc["family"], ks["title"], k))
    return [k for *_, k in sorted(hits)]


def drop_pick(doc: dict, palette: dict, node: str, port: str, side: str, new_id: str, kind: str) -> dict[str, str]:
    """The `in` of kind `kind` picked from a wire-drop search, after the dragged wire and its auto-wiring (README)."""
    s, ks = palette[doc["nodes"][node]["kind"]], palette[kind]
    t = next(o["type"] for o in s["outputs"] if o["name"] == port) if side == "output" else port_type(s, port)
    nodes, wired = json.loads(json.dumps(doc["nodes"])), {}
    if side == "output":
        key = next((i["name"] for i in ks["inputs"] if fits(t, i["type"])), None) or next(
            (p["name"] for p in ks["params"] if WIREABLE.get(p["type"]) == t), None)
        if key:
            wired[key] = f"{node}.{port}"
    elif out := next((o["name"] for o in ks["outputs"] if fits(o["type"], t)), None):
        nodes[node].setdefault("in", {})[port] = f"{new_id}.{out}"
    nodes[new_id] = {"kind": kind, "in": dict(wired)}
    auto = autowire(doc | {"nodes": nodes}, palette, node if side == "output" else None, new_id, kind)
    return wired | {k: v for k, v in auto.items() if k not in wired}


def check_graphs(registry: Registry) -> dict:
    g = HERE / "fixtures" / "graphs"
    ref = lambda n: Draft202012Validator(
        {"$ref": f"{OPENAPI}#/components/schemas/{n}"}, registry=registry, format_checker=FormatChecker())
    pal_doc = json.loads((g / "palette.json").read_text())
    for e in ref("NodePalette").iter_errors(pal_doc):
        fail(f"fixtures/graphs/palette.json: {e.json_path}: {e.message}")
    node_v = Draft202012Validator({"$ref": "https://helmstudio.local/contracts/registry.schema.json#/$defs/nodeSchema"},
                                  registry=registry)
    palette = {}
    for grp in pal_doc["groups"]:
        for n in grp["nodes"]:
            schema = {k: v for k, v in n.items() if k != "envs"}
            for e in node_v.iter_errors(schema):
                fail(f"fixtures/graphs/palette.json {n['kind']}: {e.message}")
            names = [p["name"] for p in n["params"] + n["inputs"]]
            if len(names) != len(set(names)):
                fail(f"fixtures/graphs/palette.json {n['kind']}: params and inputs share a name")
            palette[n["kind"]] = n
    doc_v = ref("WorkflowDoc")
    for p in sorted(g.glob("*.json")):
        if p.name == "palette.json":
            continue
        fx, name = json.loads(p.read_text()), f"fixtures/graphs/{p.name}"
        if errs := list(doc_v.iter_errors(fx["workflow"])):
            fail(f"{name}: workflow is not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
            continue
        if p.name.startswith(("valid.", "invalid.")):
            got = graph_problems(fx["workflow"], palette, fx.get("loras"))
            want = {(x["code"], x.get("node"), x.get("port"), x.get("param")) for x in fx["problems"]}
            if p.name.startswith("valid.") != (not want):
                fail(f"{name}: a valid graph has no problems, an invalid one has some")
        elif p.name.startswith("autowire."):
            got = autowire(fx["workflow"], palette, fx["selected"], fx["add"]["id"], fx["add"]["kind"])
            want = fx["expect_in"]
        elif p.name.startswith("wiredrop."):
            f = fx["from"]
            got, want = wire_drop(fx["workflow"], palette, f["node"], f["port"], f["side"]), fx["expect"]
            if "pick" in fx and got == want:
                got = drop_pick(fx["workflow"], palette, f["node"], f["port"], f["side"], fx["pick"]["id"],
                                fx["pick"]["kind"])
                want = fx["expect_in"]
        else:
            fail(f"{name}: unknown fixture kind")
            continue
        if got != want:
            fail(f"{name}: got {sorted(got) if isinstance(got, set) else got}, want {want}")
        else:
            print(f"ok   {name}")
    return palette
