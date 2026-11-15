"""Contract self-check: every fixture against its schema, and a lint of openapi.yaml.

    python contracts/check.py

Needs jsonschema (+ rfc3339-validator for date-time), PyYAML, openapi-spec-validator. Fixtures are
fixtures/{messages,registry,http}/<name>.{valid,invalid}[.N].json: every valid one must pass, every invalid one must fail.
Registry fixtures may carry a family prefix (qwen-image.nodes.valid.json): <family>.<key>. HTTP fixtures are bodies
checked against openapi.yaml components/schemas/<name>. fixtures/graphs/ run through the reference graph rules below
(README, Graph rules) against fixtures/graphs/palette.json; fixtures/pipelines/ through the reference pipeline rules
(README, Pipeline rules) against fixtures/pipelines/workflows.json and the same palette; fixtures/mentions/ through
the reference mention rules (README, Mentions) against the same palette; fixtures/loras/ through the reference
LoRA normaliser (README, LoRA normaliser) against the canonical qwen-image lora block.
Exit code 1 on any failure."""

import json
import math
from fnmatch import fnmatchcase
import re
import sys
from pathlib import Path

import yaml
from jsonschema import Draft202012Validator, FormatChecker
from jsonschema.exceptions import SchemaError, ValidationError
from openapi_spec_validator import validate as validate_openapi
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

HERE = Path(__file__).parent
SCHEMA_FILES = sorted((HERE / "messages").glob("*.schema.json"))
# Every cmd.*/evt.* schema is an enveloped message; every req.* holds $defs/request and $defs/reply.
MESSAGE_TYPES = [
    n
    for p in SCHEMA_FILES
    if (n := p.name.removesuffix(".schema.json")).startswith(("cmd.", "evt."))
]
REQ_REPLY = [
    n for p in SCHEMA_FILES if (n := p.name.removesuffix(".schema.json")).startswith("req.")
]
# Canonical registries a runner publishes verbatim: fixtures/registry/<prefix>{nodes,workflows}.valid.json
CANONICAL = ["", "qwen-image.", "ltx."]
REGISTRY_KEYS = [
    "nodes",
    "workflows",
    "models",
    "loras",
    "looks",
    "host",
]  # registry.schema.json $defs; fixtures/registry/<key>.*
# openapi.yaml components/schemas with body fixtures in fixtures/http/<name>.*
HTTP_SCHEMAS = [
    "Run", "RunRequest", "Queue", "Take", "TakePage", "TakePatch", "TakeReuse", "TakeUse", "UploadCreate", "Upload",
    "Batch", "BatchCreate", "BatchRow", "BatchEvent",
    "Workflow", "WorkflowCreate", "WorkflowUpdate", "WorkflowPatch", "WorkflowValidation", "WorkflowError", "NodePalette",
    "Pipeline", "PipelineCreate", "PipelineUpdate", "PipelinePatch", "PipelineValidation", "PipelineError", "TakePipeline",
    "BatchPick", "BatchRowRetry", "BatchReview", "RowExecution", "RowState",
    "LibraryItem", "LibraryItemList", "LibraryItemCreate", "LibraryItemUpdate",
    "Lora", "LoraList", "LoraPatch", "LoraUploadCreate", "LoraUpload",
]
OPENAPI = "https://helmstudio.local/contracts/openapi.yaml"
OPERATION_PREFIX = {"/v1/studio/": "studio", "/files/": "files", "/auth/": "auth"}
# Names of the reference product that must not come back.
FORBIDDEN = re.compile(r"\bcatalog|\bora[._]|\bORA\b|agents_service|\bagents\.", re.IGNORECASE)
METHODS = {"get", "post", "put", "patch", "delete"}

errors: list[str] = []


def fail(msg: str) -> None:
    errors.append(msg)
    print("FAIL", msg)


def load_schemas() -> tuple[dict[str, dict], Registry]:
    schemas = {p.name.removesuffix(".schema.json"): json.loads(p.read_text()) for p in SCHEMA_FILES}
    schemas["registry"] = json.loads((HERE / "registry.schema.json").read_text())
    for name, s in schemas.items():
        try:
            Draft202012Validator.check_schema(s)
        except SchemaError as e:
            fail(f"schema {name}: {e}")
    spec = yaml.safe_load((HERE / "openapi.yaml").read_text())
    registry = Registry().with_resources(
        [(s["$id"], Resource.from_contents(s)) for s in schemas.values()]
        + [(OPENAPI, Resource(contents=spec, specification=DRAFT202012))]
    )
    return schemas, registry


class _Err:  # quacks like a jsonschema error for the report
    def __init__(self, path: str, message: str) -> None:
        self.json_path, self.message = path, message


def foreign_keys(msg: dict) -> list[_Err]:
    """Claim-check rule JSON Schema can't express: every object key in `data` is under u/<owner_id>/."""
    prefix, out = f"u/{msg.get('owner_id')}/", []

    def walk(v, path):
        if isinstance(v, dict):
            for k, x in v.items():
                walk(x, f"{path}.{k}")
        elif isinstance(v, list):
            for i, x in enumerate(v):
                walk(x, f"{path}[{i}]")
        elif isinstance(v, str) and v.startswith("u/") and not v.startswith(prefix):
            out.append(_Err(path, f"{v!r} is not under the owner's prefix {prefix}"))

    walk(msg.get("data"), "$.data")
    return out


def graph_errors(workflow: dict, kinds: dict[str, dict]) -> list[str]:
    """What the schema can't say about a workflow: known kinds, declared params and ports, wires whose output
    exists and has the input's type."""
    out, nodes = [], workflow["nodes"]
    for nid, node in nodes.items():
        schema = kinds.get(node["kind"])
        if not schema:
            out.append(f"{nid}: unknown kind {node['kind']!r}")
            continue
        params = {p["name"]: p for p in schema["params"]}
        for k, v in node.get("params", {}).items():
            if k not in params:
                out.append(f"{nid}.{k}: not a param of {node['kind']}")
            elif err := value_error(params[k], v):
                out.append(f"{nid}.{k}: {err}")
        inputs = {p["name"]: p["type"] for p in schema["inputs"]}
        for port, ref in node.get("in", {}).items():
            src_id, src_port = ref.split(".")
            src = kinds.get(nodes.get(src_id, {}).get("kind"))
            outputs = {p["name"]: p["type"] for p in src["outputs"]} if src else {}
            if port not in inputs:
                out.append(f"{nid}.in.{port}: not an input of {node['kind']}")
            elif not fits(outputs.get(src_port), inputs[port]):
                out.append(f"{nid}.in.{port}: {ref} is not an output of type {inputs[port]}")
    return out


def value_error(param: dict, v) -> str | None:
    """What studio checks when it resolves a run (type, min/max, enum); also applied to every default."""
    t = param["type"]
    if t == "seed" and v is None:
        return None
    if t == "image":  # a registry workflow holds no takes: an image param stays empty
        return None if v is None else f"{v!r}: a workflow cannot fill an image param"
    if t == "rows":
        if not isinstance(v, list):
            return f"{v!r} is not a list"
        if not param.get("min", 0) <= len(v) <= param["max"]:
            return f"{len(v)} items is outside {param.get('min', 0)}..{param['max']}"
        for i, row in enumerate(v, 1):
            if not isinstance(row, dict) or set(row) - set(param["item"]):
                return f"item {i}: fields must be among {sorted(param['item'])}"
            for f, spec in param["item"].items():
                if spec == "Image":  # a take; never in a registry workflow
                    return f"item {i}.{f}: a workflow cannot fill an image"
                if spec == "Lora":  # required in every item, {lora_id} (README, LoRAs)
                    v_ = row.get(f)
                    if not isinstance(v_, dict) or set(v_) != {"lora_id"} or not LORA_ID.match(str(v_["lora_id"])):
                        return f"item {i}.{f}: a LoRA is required ({{lora_id}})"
                    continue
                if f in row and (err := value_error(spec, row[f])):
                    return f"item {i}.{f}: {err}"
        return None
    if t == "frames":  # on the frame grid min + k·step (README, Video: Params)
        if not isinstance(v, int) or isinstance(v, bool):
            return f"{v!r} is not an integer"
        if not param["min"] <= v <= param["max"] or (v - param["min"]) % param["step"]:
            return f"{v!r} is not on the grid {param['min']} + {param['step']}k up to {param['max']}"
        return None
    if t in ("int", "seed") and (not isinstance(v, int) or isinstance(v, bool)):
        return f"{v!r} is not an integer"
    if t == "float" and (not isinstance(v, (int, float)) or isinstance(v, bool)):
        return f"{v!r} is not a number"
    if t == "bool" and not isinstance(v, bool):
        return f"{v!r} is not a bool"
    if t in ("string", "text", "enum") and not isinstance(v, str):
        return f"{v!r} is not a string"
    if t == "enum" and v not in param["enum"]:
        return f"{v!r} is not one of {param['enum']}"
    n = len(v) if t in ("string", "text") else v
    if t != "enum" and t != "bool" and not param.get("min", n) <= n <= param.get("max", n):
        return f"{v!r} is outside {param.get('min')}..{param.get('max')}"
    return None


def check_canonical_registries() -> None:
    """The canonical fixtures (a runner publishes them verbatim) must fit together."""
    reg = HERE / "fixtures" / "registry"
    for prefix in CANONICAL:
        nodes = json.loads((reg / f"{prefix}nodes.valid.json").read_text())["nodes"]
        kinds = {n["kind"]: n for n in nodes}
        for n in nodes:
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
            for e in errs:
                fail(f"fixtures/registry/{prefix}workflows.valid.json {wf['id']}: {e}")
            if not errs:
                print(f"ok   workflow {wf['id']} fits {prefix}nodes.valid.json")


# ---------------------------------------------------------------- graph rules (README, "Graph rules")
HANDLES = {"Model", "Conditioning", "Latent"}
MEDIA = {"Image", "Video"}  # what an input of type Media accepts (Audio later)


def fits(got: str | None, want: str) -> bool:
    """A wire of type `got` may feed a port of type `want` (README, Graph rules: fits); handles also need one family."""
    return got is not None and (got == want or (want == "Media" and got in MEDIA))


def workflow_media(nodes: dict, kinds: dict) -> str:
    """video when an output node's media is wired from a Video output, else image (README, Video)."""
    for n in nodes.values():
        ref = n.get("in", {}).get("media") if n["kind"] == "output" else None
        src = nodes.get(ref.split(".")[0]) if ref else None
        ks = kinds.get(src["kind"]) if src else None
        if ks and any(o["name"] == ref.split(".")[1] and o["type"] == "Video" for o in ks["outputs"]):
            return "video"
    return "image"
LORA_ID = re.compile(r"^([0-9a-f]{64}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$")
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
    if not any(n["kind"] == "output" for n in nodes.values()):
        add("no_output")

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
    for values in studio.get("presets", {}).values():
        for path, v in values.items():
            nid, param = path.split(".")
            if bad_path(path):
                add("preset_invalid", nid, param=param)
            elif nid in nodes and nodes[nid]["kind"] in palette:
                spec = {"type": "bool"} if param == "bypass" else next(
                    p for p in palette[nodes[nid]["kind"]]["params"] if p["name"] == param)
                if value_error(spec, v):
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


# ---------------------------------------------------------------- pipeline rules (README, "Pipeline rules")
P_MAX_NODES, P_MAX_BYTES, P_MAX_RUNS = 24, 65536, 64
P_RUN_ONLY = {"unreachable", "dead_end", "no_output", "unknown_workflow", "workflow_cannot_run", "unknown_preset",
              "step_input_invalid", "bind_unknown", "bind_type", "unbound_input", "step_seeds", "unknown_look", "look_fit"}
INPUT_FITS = {"Text": {"string", "text"}, "Int": {"int"}, "Float": {"float"}, "Seed": {"seed"}, "Image": {"image"}}


def lora_field(param: dict) -> str | None:
    """A rows param's Lora field (the lora-stack's `lora`), else None."""
    return next((k for k, v in param.get("item", {}).items() if v == "Lora"), None) if param["type"] == "rows" else None


def image_field(param: dict) -> str | None:
    """A rows param's one Image field (a list of takes binds to it), else None."""
    fields = [k for k, v in param.get("item", {}).items() if v == "Image"] if param["type"] == "rows" else []
    return fields[0] if len(fields) == 1 else None


def settable(wf: dict, palette: dict) -> dict[str, dict]:
    """`<node>.<param>` → param schema for every unwired param of a known kind: what a step may set or bind."""
    return {f"{nid}.{p['name']}": p for nid, n in wf["nodes"].items() if n["kind"] in palette
            for p in palette[n["kind"]]["params"] if p["name"] not in n.get("in", {})}


def pipeline_problems(doc: dict, workflows: dict[str, dict], palette: dict, looks: list[dict] | None = None,
                      loras: dict[str, str] | None = None) -> set[tuple]:
    """Every problem as (code, node, port, param); workflows: the id → WorkflowDoc the caller can run; looks, loras:
    the looks and LoRAs the caller can use now (as resolve_mentions takes them)."""
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
    succ, outdeg, capped = {nid: [] for nid in nodes}, {}, []  # succ: node → [(to, capped)]
    for frm, to in doc["edges"]:
        targets = [to] if isinstance(to, str) else [to["pass"], to["fail"], to["then"]]
        if frm not in nodes or any(t not in nodes for t in targets):
            add("unknown_node", frm)
            continue
        if not isinstance(to, str) and nodes[frm]["kind"] != "gate":
            add("branch_not_gate", frm)
            continue
        outdeg[frm] = outdeg.get(frm, 0) + 1
        if outdeg[frm] > 1 or nodes[frm]["kind"] == "output" or any(nodes[t]["kind"] == "inputs" for t in targets):
            add("edge_invalid", frm)
            continue
        if isinstance(to, str):
            succ[frm].append((to, False))
        else:
            succ[frm] += [(to["pass"], False), (to["fail"], True), (to["then"], False)]
            capped.append((frm, to["fail"], to["max"]))

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
    runs = sum(n.get("seeds", 1) * (1 + sum(m for g, f, m in capped if (nid == f or nid in reach(f)) and g in reach(nid)))
               for nid, n in nodes.items() if n["kind"] == "step")
    if runs > P_MAX_RUNS:
        add("too_many_runs")
    root = roots[0] if len(roots) == 1 else None
    if root:
        for nid in set(nodes) - reach(root) - {root}:
            add("unreachable", nid)
    outputs = of("output")
    if not outputs:
        add("no_output")
    for nid in nodes:
        if outputs and nodes[nid]["kind"] != "output" and not reach(nid) & set(outputs):
            add("dead_end", nid)
    # a before b on every path from the inputs node (unchecked without exactly one inputs node)
    dominates = lambda a, b: root is None or a == root or (a != b and b not in reach(root, avoid=a))

    def source(src: str) -> tuple[str, str | int] | None:
        """('input', type) or ('takes', how many at most), None when it names nothing of the right kind."""
        if src.startswith("$"):
            return ("input", inputs[src[1:]]) if src[1:] in inputs else None
        nid, what = src.split(".")
        n = nodes.get(nid)
        if not n or n["kind"] != ("step" if what == "takes" else "gate"):
            return None
        return "takes", n.get("seeds", 1) if what == "takes" else n.get("keep", 1)

    for nid in of("gate"):
        g = nodes[nid]
        t = nodes.get(g["takes"])
        if not t or t["kind"] != "step" or not dominates(g["takes"], nid):
            add("gate_takes", nid)
        elif g.get("keep", 1) > t.get("seeds", 1):
            add("gate_keep", nid)
    for nid in outputs:
        src = nodes[nid]["from"]
        if not source(src) or (root and nid not in reach(src.split(".")[0])):
            add("bind_source", nid, "from")
    for nid in of("step"):
        n = nodes[nid]
        binds = {k: source(v) for k, v in n.get("bind", {}).items()}
        for k, v in n.get("bind", {}).items():
            if not binds[k] or (not v.startswith("$") and not dominates(v.split(".")[0], nid)):
                add("bind_source", nid, k)
                binds[k] = None
        wf = workflows.get(n["workflow"])
        if not wf:
            add("unknown_workflow", nid)
            continue
        if graph_problems(wf, palette):
            add("workflow_cannot_run", nid)
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
            if src and src[0] == "input":
                fits = p["type"] in INPUT_FITS[src[1]] or (src[1] == "Image" and image_field(p) is not None)
            elif src:
                fits = (p["type"] == "image" and src[1] == 1) or (image_field(p) is not None and src[1] <= p["max"])
            if src and not fits:
                add("bind_type", nid, k)
        for k, p in params.items():
            if (p["type"] == "image" or (image_field(p) and p.get("min", 0) >= 1)) and k not in binds:
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
    doc_v = ref("PipelineDoc")
    for p in sorted(g.glob("*.json")):
        if p.name == "workflows.json":
            continue
        fx, name = json.loads(p.read_text()), f"fixtures/pipelines/{p.name}"
        if errs := list(doc_v.iter_errors(fx["pipeline"])):
            fail(f"{name}: pipeline is not a PipelineDoc: {errs[0].json_path}: {errs[0].message}")
            continue
        got = pipeline_problems(fx["pipeline"], workflows, palette, fx.get("looks"), fx.get("loras"))
        want = {(x["code"], x.get("node"), x.get("port"), x.get("param")) for x in fx["problems"]}
        if not p.name.startswith(("valid.", "invalid.")) or p.name.startswith("valid.") != (not want):
            fail(f"{name}: valid.* has no problems, invalid.* has some")
        if got != want:
            fail(f"{name}: got {sorted(got, key=str)}, want {sorted(want, key=str)}")
        else:
            print(f"ok   {name}")


# ---------------------------------------------------------------- mention rules (README, "Mentions")
HANDLE = re.compile(r"^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$")  # an element's handle
WORD = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_")  # ASCII only: bytes, runes, UTF-16 agree


def scan(text: str) -> list[dict]:
    """The prompt as parts: {text} (raw, `@@` kept) and {mention: handle, raw}; the raw parts join to the text."""
    parts, buf, i = [], "", 0
    while i < len(text):
        if text.startswith("@@", i):
            buf, i = buf + "@@", i + 2
            continue
        if text[i] == "@" and (i == 0 or text[i - 1] not in WORD):
            j = i + 1
            while j < len(text) and (text[j] in WORD or text[j] == "-"):
                j += 1
            tok = text[i + 1:j].rstrip("-_")
            if tok and tok[0].isascii() and tok[0].isalpha():
                if buf:
                    parts.append({"text": buf})
                parts.append({"mention": tok.lower(), "raw": "@" + tok})
                buf, i = "", i + 1 + len(tok)
                continue
        buf, i = buf + text[i], i + 1
    return parts + ([{"text": buf}] if buf else [])


def reference_rows(nodes: dict, palette: dict) -> tuple[str, dict] | None:
    """Where element images go: the first (node id, then schema order) rows param with a `{n}` mention and one
    Image field, on a known, not bypassed node; None = the family takes text only."""
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            if p["type"] == "rows" and "{n}" in p.get("mention", "") and image_field(p) and p["name"] not in n.get("in", {}):
                return nid, p
    return None


def auto_size(width: int, height: int) -> tuple[int, int]:
    """Canvas `auto` with references: about 1 MP at the aspect of width x height, sides rounded (half to even, as
    Python's round) to 32, as the runner sizes it."""
    ratio = width / height
    w = math.sqrt(1024 * 1024 * ratio)
    return round(w / 32) * 32, round(w / ratio / 32) * 32


def pin_canvases(nodes: dict, palette: dict, before: list, field: str, sizes: dict, changed: dict, typed: dict) -> None:
    """Mentions step 2, Canvas: element rows must not change the output size. Each `canvas` on `auto` is pinned to
    `fixed` at the size auto gives without them: its own width x height without user rows, else the auto size of the
    last user row's take (clamped to the params' min and max; unknown size: mode only)."""
    last = before[-1].get(field) if before else None
    size = sizes.get(last["take_id"]) if isinstance(last, dict) else None
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        if n["kind"] != "canvas" or not s or n.get("bypass"):
            continue
        ps = {p["name"]: p for p in s["params"]}
        val = {name: n.get("params", {}).get(name, p["default"]) for name, p in ps.items()}
        if val["mode"] != "auto":
            continue
        changed[f"{nid}.mode"], typed[f"{nid}.mode"] = "fixed", "auto"
        for name, v in zip(("width", "height"), auto_size(*size) if size else ()):
            v = min(max(v, ps[name].get("min", v)), ps[name].get("max", v))
            if v != val[name]:
                changed[f"{nid}.{name}"], typed[f"{nid}.{name}"] = v, val[name]


def prompt_params(nodes: dict, palette: dict) -> list[tuple[str, dict]]:
    """Mentions "Where": every unwired `text` param of a known, not bypassed node, node id order then schema order."""
    return [(nid, p) for nid in sorted(nodes) if (s := palette.get(nodes[nid]["kind"])) and not nodes[nid].get("bypass")
            for p in s["params"] if p["type"] == "text" and p["name"] not in nodes[nid].get("in", {})]


def look_stack(nodes: dict, palette: dict) -> tuple[str, dict] | None:
    """Where look LoRAs go (README, Looks): among the known, not bypassed `<family>/lora-stack` nodes with an unwired
    rows param that has a Lora field, the first by node id from which no other such node is reachable downstream."""
    cands = {}
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        if s and not n.get("bypass") and n["kind"].endswith("/lora-stack"):
            if p := next((p for p in s["params"] if lora_field(p) and p["name"] not in n.get("in", {})), None):
                cands[nid] = p
    succ = {nid: {m for m, x in nodes.items() for ref in x.get("in", {}).values() if ref.split(".")[0] == nid}
            for nid in nodes}

    def down(nid: str) -> set[str]:
        seen, todo = set(), list(succ[nid])
        while todo:
            if (x := todo.pop()) not in seen:
                seen.add(x)
                todo += succ.get(x, ())
        return seen

    return next(((nid, p) for nid, p in cands.items() if not down(nid) & set(cands)), None)


def look_needs(item: dict) -> list[str]:
    """LibraryItem.needs: text (non-empty), images (at least one), lora (at least one), in that order."""
    return [k for k, v in (("text", item["text"]), ("images", item["images"]), ("lora", item.get("loras"))) if v]


def look_fit(nodes: dict, palette: dict, item: dict, loras: dict[str, str]) -> str | None:
    """Why the item cannot be applied to this graph (README, Looks: fit), None when it can. `loras`: LoRA id → detected
    family of every LoRA the caller can use now in the workflow's env."""
    needs = look_needs(item)
    if ("text" in needs or item.get("kind") == "element") and not prompt_params(nodes, palette):
        return "this workflow has no prompt"
    if item.get("kind") == "element":
        return None  # an element's images are dropped where there are no reference rows (Mentions)
    if "images" in needs and not reference_rows(nodes, palette):
        return "this workflow takes no reference images"
    if "lora" in needs:
        if not (stack := look_stack(nodes, palette)):
            return "this workflow has no LoRA stack"
        for x in item["loras"]:
            fam = loras.get(x["lora_id"]) if x["lora_id"] else None
            if x["lora_id"] is None:
                return f"LoRA {x['name']} is on no live host"
            if fam is None:
                return f"LoRA {x['name']} is not available"
            if fam == "unknown":
                return f"LoRA {x['name']} fits no known model"
            if fam != family(nodes[stack[0]]["kind"]):
                return f"LoRA {x['name']} is for {fam}"
    return None


def resolve_mentions(nodes: dict, palette: dict, library: list[dict], sizes: dict | None = None,
                     looks: list[dict] | None = None, chosen: list[str] | None = None,
                     loras: dict[str, str] | None = None) -> dict:
    """Expand every mention and apply the chosen looks (README, Mentions and Looks) on a graph whose params hold the
    merged values (defaults, workflow, preset, inputs). `sizes`: take id → [width, height] of the user's own row
    takes; `looks`: the looks the caller can use now; `chosen`: RunRequest.looks; `loras`: as look_fit's.
    Answers {inputs: changed path → resolved value, typed: changed path → value as set, elements, looks, errors}."""
    lib, found, chosen = {e["handle"]: e for e in library}, {x["id"]: x for x in looks or []}, chosen or []
    val = lambda nid, p: nodes[nid].get("params", {}).get(p["name"], p["default"])
    prompts = [(nid, f"{nid}.{p['name']}", p, scan(val(nid, p))) for nid, p in prompt_params(nodes, palette)]
    mentioned = lambda parts: list(dict.fromkeys(x["mention"] for x in parts if "mention" in x))
    first = {}  # look id → its first place in the request, from 1
    for i, lid in enumerate(chosen, 1):
        first.setdefault(lid, i)
    errors = [f"{path}: unknown element @{h}" for _, path, _, parts in prompts for h in mentioned(parts) if h not in lib]
    errors += [f"looks[{i}]: unknown look {lid}" for lid, i in first.items() if lid not in found]
    if errors:
        return {"errors": errors}
    picked = [found[lid] for lid in first]
    errors = [f"looks[{first[x['id']]}]: look {x['name']} does not fit this workflow ({r})" for x in picked
              if (r := look_fit(nodes, palette, x | {"kind": "look"}, loras or {}))]
    if errors:
        return {"errors": errors}
    used = list(dict.fromkeys(h for *_, parts in prompts for h in mentioned(parts)))
    adds = [(f"@{h}", ("e", h), lib[h]["images"]) for h in used] + [(f"look {x['name']}", ("l", x["id"]), x["images"])
                                                                     for x in picked]
    tokens, changed, typed = {k: [] for _, k, _ in adds}, {}, {}
    if (target := reference_rows(nodes, palette)) and adds:
        tid, tp = target
        field, cap, path = image_field(tp), tp.get("max", 100), f"{tid}.{tp['name']}"
        rows = list(val(tid, tp))
        at = {}  # take id → its row number (the user's rows first)
        for i, r in enumerate(rows, 1):
            if isinstance(r.get(field), dict):
                at.setdefault(r[field]["take_id"], i)
        over, full = [], None
        for label, key, images in adds:
            new = [t for t in dict.fromkeys(images) if t not in at]
            if full is not None or len(rows) + len(new) > cap:
                full = len(rows) if full is None else full
                over += [f"{label} +{len(new)}"] if new else []
                continue
            for t in new:
                rows.append({field: {"take_id": t}})
                at[t] = len(rows)
            tokens[key] = [tp["mention"].replace("{n}", str(at[t])) for t in images]
        if over:
            return {"errors": [f"{path}: at most {cap} images; {full} used, {', '.join(over)} do not fit"]}
        if len(rows) > len(val(tid, tp)):
            changed[path], typed[path] = rows, val(tid, tp)
            pin_canvases(nodes, palette, val(tid, tp), field, sizes or {}, changed, typed)
    if any(x["loras"] for x in picked) and (stack := look_stack(nodes, palette)):
        sid, sp = stack
        field, cap, path, before = lora_field(sp), sp.get("max", 100), f"{sid}.{sp['name']}", val(sid, stack[1])
        present = {r[f]["lora_id"] for nid in nodes if (s := palette.get(nodes[nid]["kind"])) and not nodes[nid].get("bypass")
                   for p in s["params"] if (f := lora_field(p))
                   for r in val(nid, p) if isinstance(r.get(f), dict)}  # the user's rows win
        st = sp["item"].get("strength")
        clamp = lambda v: min(max(v, st.get("min", v)), st.get("max", v)) if isinstance(st, dict) else v
        rows, over, full = list(before), [], None
        for x in picked:
            new = []
            for y in x["loras"]:
                if y["lora_id"] not in present and all(y["lora_id"] != z["lora_id"] for z in new):
                    new.append(y)
            if full is not None or len(rows) + len(new) > cap:
                full = len(rows) if full is None else full
                over += [f"look {x['name']} +{len(new)}"] if new else []
                continue
            for y in new:
                rows.append({field: {"lora_id": y["lora_id"]}} | ({"strength": clamp(y["strength"])} if st else {}))
                present.add(y["lora_id"])
        if over:
            return {"errors": [f"{path}: at most {cap} LoRAs; {full} used, {', '.join(over)} do not fit"]}
        if len(rows) > len(before):
            changed[path], typed[path] = rows, before
    look_lines = list(dict.fromkeys(line for x in picked
                                    if (line := " ".join(([x["text"]] if x["text"] else []) + tokens[("l", x["id"])]))))
    for i, (nid, path, p, parts) in enumerate(prompts):
        out = "".join(x["text"].replace("@@", "@") if "text" in x
                      else " ".join([lib[x["mention"]]["name"]] + tokens[("e", x["mention"])]) for x in parts)
        notes = [f"{lib[h]['name']}: {lib[h]['text']}" for h in mentioned(parts) if lib[h]["text"]]
        out += "\n\n" + "\n".join(notes) if notes else ""
        extra = look_lines if i == 0 else []  # look text goes to the first prompt param only
        out = "\n\n".join(x for x in (out, "\n".join(extra)) if x)
        if len(out) > p.get("max", len(out)):
            what = "elements and looks" if extra else "elements"
            errors.append(f"{path}: at most {p['max']} characters once {what} are added ({len(out)})")
        elif out != val(nid, p):
            changed[path], typed[path] = out, val(nid, p)
    if errors:
        return {"errors": errors}
    elements = [{k: lib[h][k] for k in ("id", "handle", "name", "version")} for h in used]
    applied = [{k: x[k] for k in ("id", "source", "name", "version")} for x in picked]
    return {"inputs": changed, "typed": typed, "elements": elements, "looks": applied, "errors": []}


LOOK_ID = re.compile(r"^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9][a-z0-9_-]{0,63}/[a-z0-9][a-z0-9-]{0,63})$")


def check_mentions(registry: Registry, palette: dict) -> None:
    g = HERE / "fixtures" / "mentions"
    for case in json.loads((g / "scan.json").read_text())["cases"]:
        got = scan(case["text"])
        if got != case["parts"] or "".join(x.get("text") or x["raw"] for x in got) != case["text"]:
            fail(f"fixtures/mentions/scan.json {case['text']!r}: got {got}, want {case['parts']}")
    print("ok   fixtures/mentions/scan.json")
    doc_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/WorkflowDoc"}, registry=registry)

    def bad_looks(name: str, looks: list[dict]) -> bool:
        """The looks as studio reads them: own (UUID) or starter ids, 0-4 images, 0-4 LoRAs (null id: starter only)."""
        bad = [x["id"] for x in looks if not LOOK_ID.match(x["id"]) or x["source"] != ("mine" if "/" not in x["id"] else "starter")
               or len(x["images"]) > 4 or len(x["loras"]) > 4 or not look_needs(x)
               or any(y["lora_id"] is None and x["source"] == "mine" for y in x["loras"])]
        if bad:
            fail(f"{name}: looks break the look rules: {bad}")
        return bool(bad)

    fit = json.loads((g / "fit.json").read_text())
    for wid, wf in fit["workflows"].items():
        if errs := list(doc_v.iter_errors(wf)):
            fail(f"fixtures/mentions/fit.json {wid}: not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
    for case in fit["cases"]:
        item = case["item"]
        if item.get("kind") == "look" and bad_looks("fixtures/mentions/fit.json", [item]):
            continue
        got = look_fit(fit["workflows"][case["workflow"]]["nodes"], palette, item, case.get("loras", {}))
        if got != case["expect"]:
            fail(f"fixtures/mentions/fit.json {case['description']!r}: got {got!r}, want {case['expect']!r}")
    print("ok   fixtures/mentions/fit.json")
    for p in sorted(g.glob("resolve.*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/mentions/{p.name}"
        if errs := list(doc_v.iter_errors(fx["workflow"])):
            fail(f"{name}: workflow is not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
            continue
        # the library as studio reads it: images of deleted takes already left out (so 0 to 4)
        bad = [e["handle"] for e in fx["library"] if not HANDLE.match(e["handle"]) or len(e["images"]) > 4]
        if bad:
            fail(f"{name}: library items break the element rules: {bad}")
        if bad_looks(name, fx.get("looks", [])):
            continue
        got = resolve_mentions(fx["workflow"]["nodes"], palette, fx["library"], fx.get("sizes"), fx.get("looks"),
                               fx.get("chosen"), fx.get("loras"))
        want = fx["expect"]
        if got != (want if want.get("errors") else {"inputs": {}, "typed": {}, "elements": [], "looks": [],
                                                     "errors": []} | want):
            fail(f"{name}: got {json.dumps(got)}, want {json.dumps(want)}")
        else:
            print(f"ok   {name}")


# ---------------------------------------------------------------- LoRA normaliser (README, "LoRA normaliser")
LORA_SUFFIXES = {  # style → suffix → role
    "kohya": {".lora_down.weight": "A", ".lora_up.weight": "B", ".alpha": "alpha"},
    "comfyui": {".lora_A.weight": "A", ".lora_B.weight": "B", ".lora_down.weight": "A", ".lora_up.weight": "B",
                ".alpha": "alpha"},
    "peft": {".lora_A.weight": "A", ".lora_B.weight": "B", ".lora.down.weight": "A", ".lora.up.weight": "B",
             ".alpha": "alpha"},
}
KOHYA_PREFIXES, COMFY_PREFIX = ("lora_unet_", "lora_transformer_"), "diffusion_model."
LORA_DTYPES = {"F32", "F16", "BF16"}


def expand_modules(modules: list[str]) -> list[str]:
    """Each entry with one `{a..b}` range expands to a..b inclusive."""
    out = []
    for m in modules:
        r = re.search(r"\{(\d+)\.\.(\d+)\}", m)
        out += [m[:r.start()] + str(i) + m[r.end():] for i in range(int(r[1]), int(r[2]) + 1)] if r else [m]
    return out


def lora_style(keys: list[str]) -> str | None:
    if any(k.startswith(KOHYA_PREFIXES) for k in keys):
        return "kohya"
    if any(k.startswith(COMFY_PREFIX) for k in keys):
        return "comfyui"
    if any(k.endswith(tuple(LORA_SUFFIXES["peft"])) for k in keys):
        return "peft"
    return None


def lora_parse(key: str, style: str) -> tuple[str, str] | None:
    """(module part, role A | B | alpha), or None: not a key of that style."""
    if style == "kohya":
        pre = next((p for p in KOHYA_PREFIXES if key.startswith(p)), None)
        if pre is None:
            return None
        rest = key[len(pre):]
    elif style == "comfyui":
        if not key.startswith(COMFY_PREFIX):
            return None
        rest = key[len(COMFY_PREFIX):]
    else:
        rest = key.removeprefix("base_model.model.").removeprefix("transformer.")
    suf = next((x for x in LORA_SUFFIXES[style] if rest.endswith(x)), None)
    return (rest[:-len(suf)], LORA_SUFFIXES[style][suf]) if suf and len(rest) > len(suf) else None


def normalise(name: str, tensors: dict, spec: dict, fam: str) -> dict:
    """Apply a file's header ({key: {dtype, shape}}) to one family's lora block. Answers style, rank, total,
    applied, ignored, unapplied (sorted keys), error (None when it fits)."""
    keys = sorted(k for k in tensors if k != "__metadata__")
    style, mods = lora_style(keys), expand_modules(spec["modules"])
    resolve = {m.replace(".", "_"): m for m in mods} if style == "kohya" else {m: m for m in mods}
    ignored, unapplied, parts = [], [], {}
    for k in keys:
        if any(fnmatchcase(k, g) for g in spec["ignore"]):
            ignored.append(k)
        elif not (pr := lora_parse(k, style) if style else None):
            unapplied.append(k)
        else:
            part, role = pr
            r = next((r for r in spec["renames"] if part.startswith(r["from"])), None)
            part = r["to"] + part[len(r["from"]):] if r else part
            parts.setdefault(part, {}).setdefault(role, []).append(k)
    applied, rank = [], None
    for part, roles in parts.items():
        a, b, al = roles.get("A", []), roles.get("B", []), roles.get("alpha", [])
        ok = len(a) == len(b) == 1 and len(al) <= 1 and set(roles) <= {"A", "B", "alpha"}
        if ok:
            ta, tb = tensors[a[0]], tensors[b[0]]
            ok = (len(ta["shape"]) == len(tb["shape"]) == 2 and ta["shape"][0] == tb["shape"][1] >= 1
                  and all(tensors[k]["dtype"] in LORA_DTYPES for k in a + b + al)
                  and all(tensors[k]["shape"] in ([], [1]) for k in al))
        if ok:
            rank = max(rank or 0, ta["shape"][0])
        group = sorted(k for ks in roles.values() for k in ks)
        (applied if ok and part in resolve else unapplied).extend(group)
    unapplied.sort()
    total, error = len(keys), None
    if not applied:
        error = f"LoRA {name}: 0 of {total} keys apply to {fam} ({style or 'unrecognised'} keys); is it for another model?"
    elif unapplied:
        n = len(unapplied)
        error = f"LoRA {name}: {n} of {total} keys do not apply to {fam}: {', '.join(unapplied[:3])}"
        error += f" and {n - 3} more" if n > 3 else ""
    return {"key_style": style, "rank": rank, "total": total, "applied": len(applied), "ignored": len(ignored),
            "unapplied": unapplied, "error": error}


def lora_licence(meta: dict) -> str | None:
    v = (meta.get("modelspec.license") or meta.get("license") or "").strip()
    return v[:200] or None


def check_loras() -> None:
    """fixtures/loras/*.json: header-only LoRA files run through the reference normaliser against the canonical
    qwen-image lora block (an optional `ignore` replaces its ignore list for that case)."""
    models = json.loads((HERE / "fixtures" / "registry" / "qwen-image.models.valid.json").read_text())["models"]
    spec, fam = models[0]["lora"], models[0]["family"]
    mods = expand_modules(spec["modules"])
    if len({m.replace(".", "_") for m in mods}) != len(mods):
        fail("qwen-image lora block: module names are not unique with . read as _ (kohya)")
    if spec["modules"] != sorted(spec["modules"]):
        fail("qwen-image lora block: modules are not sorted")
    for p in sorted((HERE / "fixtures" / "loras").glob("*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/loras/{p.name}"
        s = spec | ({"ignore": fx["ignore"]} if "ignore" in fx else {})
        got = normalise(fx["name"], fx["tensors"], s, fam)
        got["family"] = fam if got["error"] is None else "unknown"
        got["licence"] = lora_licence(fx.get("metadata", {}))
        bad = [k for k in fx.get("values", {}) if k not in fx["tensors"]]
        if bad or got != fx["expect"]:
            fail(f"{name}: got {json.dumps(got)}, want {json.dumps(fx['expect'])}" + (f"; values for no tensor {bad}" if bad else ""))
        else:
            print(f"ok   {name}")


def detect_family(name: str, tensors: dict, families: dict[str, tuple[list[str], dict]]) -> str:
    """The family (family → (its envs, its lora block)) whose block the file fits: one a non-fake env publishes
    before one only env fake publishes, then by name; `unknown` when none fits."""
    order = sorted(families, key=lambda f: (all(e == "fake" for e in families[f][0]), f))
    return next((f for f in order if normalise(name, tensors, families[f][1], f)["error"] is None), "unknown")


def check_lora_detection() -> None:
    """fixtures/loras/detect/families.json: family detection over the published families of each case."""
    fx = json.loads((HERE / "fixtures" / "loras" / "detect" / "families.json").read_text())
    blocks = {f: (v["envs"], json.loads((HERE / "fixtures" / v["block"]).read_text())["models"][0]["lora"])
              for f, v in fx["families"].items()}
    for c in fx["cases"]:
        lora = json.loads((HERE / "fixtures" / "loras" / c["lora"]).read_text())
        got = detect_family(lora["name"], lora["tensors"], {f: blocks[f] for f in c["families"]})
        if got != c["family"]:
            fail(f"fixtures/loras/detect/families.json: {c['lora']} over {c['families']}: got {got}, want {c['family']}")
    print("ok   fixtures/loras/detect/families.json")


def check_names() -> None:
    for p in sorted(HERE.rglob("*")):
        if p.suffix in (".json", ".yaml", ".md") and not any(
            x.startswith(".venv") for x in p.parts
        ):
            for i, line in enumerate(p.read_text().splitlines(), 1):
                if FORBIDDEN.search(line):
                    fail(
                        f"{p.relative_to(HERE)}:{i}: a reference product name: {line.strip()[:80]}"
                    )


def check_fixtures(schemas: dict[str, dict], registry: Registry) -> None:
    def validator(schema: dict) -> Draft202012Validator:
        return Draft202012Validator(schema, registry=registry, format_checker=FormatChecker())

    reg = schemas["registry"]
    targets = {
        **{t: validator(schemas[t]) for t in MESSAGE_TYPES},
        **{k: validator({"$ref": f"{reg['$id']}#/$defs/{k}"}) for k in REGISTRY_KEYS},
        **{
            f"{t}.{d}": validator({"$ref": f"{schemas[t]['$id']}#/$defs/{d}"})
            for t in REQ_REPLY
            for d in ("request", "reply")
        },
        **{n: validator({"$ref": f"{OPENAPI}#/components/schemas/{n}"}) for n in HTTP_SCHEMAS},
    }
    for k in REGISTRY_KEYS:
        if k not in reg["$defs"]:
            fail(f"registry.schema.json has no $defs/{k}")

    seen: dict[tuple[str, str], int] = {}
    for sub in ("messages", "registry", "http"):
        for p in sorted((HERE / "fixtures" / sub).glob("*.json")):
            parts = p.name.removesuffix(".json").split(".")
            kind_at = next((i for i, x in enumerate(parts) if x in ("valid", "invalid")), None)
            name = ".".join(parts[:kind_at]) if kind_at else None
            if sub == "registry" and name:
                name = name.rsplit(".", 1)[-1]  # drop a family prefix
            if name not in targets:
                fail(f"{p.relative_to(HERE)}: no schema for {name!r}")
                continue
            kind = parts[kind_at]
            seen[name, kind] = seen.get((name, kind), 0) + 1
            doc = json.loads(p.read_text())
            errs = list(targets[name].iter_errors(doc))
            if sub == "messages" and not errs:
                errs = foreign_keys(doc)
            if kind == "valid" and errs:
                fail(f"{p.relative_to(HERE)} should pass: {errs[0].json_path}: {errs[0].message}")
            elif kind == "invalid" and not errs:
                fail(f"{p.relative_to(HERE)} should fail but passes")
            else:
                why = f"  ({errs[0].json_path}: {errs[0].message[:90]})" if errs else ""
                print(f"ok   {p.relative_to(HERE)}{why}")
    for name in targets:
        for kind in ("valid", "invalid"):
            if not seen.get((name, kind)):
                fail(f"no {kind} fixture for {name}")


def openapi_inline(node, components: dict):
    """node with every #/components/schemas $ref replaced by its schema, and descriptions/examples dropped."""
    if isinstance(node, list):
        return [openapi_inline(x, components) for x in node]
    if not isinstance(node, dict):
        return node
    if "$ref" in node and node["$ref"].startswith("#/components/schemas/"):
        return openapi_inline(components[node["$ref"].rsplit("/", 1)[1]], components)
    return {k: openapi_inline(v, components) for k, v in node.items() if k not in ("description", "examples")}


def check_openapi() -> None:
    spec = yaml.safe_load((HERE / "openapi.yaml").read_text())
    # the registry's copy of StudioLayout (a template's studio block) is the API's, ParamValue aside
    registry = json.loads((HERE / "registry.schema.json").read_text())["$defs"]["studioLayout"]
    paramvalue = {"$ref": "messages/defs.schema.json#/$defs/paramValue"}
    components = spec["components"]["schemas"] | {"ParamValue": paramvalue}
    if openapi_inline(registry, {}) != openapi_inline({"$ref": "#/components/schemas/StudioLayout"}, components):
        fail("registry.schema.json $defs/studioLayout differs from openapi.yaml StudioLayout")
    else:
        print("ok   registry.schema.json studioLayout = openapi.yaml StudioLayout")
    try:
        validate_openapi(spec)
        print("ok   openapi.yaml is a valid OpenAPI", spec["openapi"], "document")
    except ValidationError as e:  # OpenAPIValidationError
        fail(f"openapi.yaml: {str(e).splitlines()[0]}")
        return
    for path, item in spec["paths"].items():
        for method, op in item.items():
            if method not in METHODS:
                continue
            where = f"{method.upper()} {path}"
            auth = op.get("tags") == ["auth"]
            if path.startswith("/auth/") != auth or auth != (
                op.get("x-status") in ("built", "deferred")
            ):
                fail(
                    f"{where}: auth routes, and only they, are tagged auth with x-status: built|deferred"
                )
            prefix = next((v for k, v in OPERATION_PREFIX.items() if path.startswith(k)), None)
            if not prefix or not op.get("operationId", "").startswith(prefix):
                fail(f"{where}: operationId must start with {prefix!r}")
            responses = op.get("responses", {})
            ok = [c for c in responses if c.startswith("2")]
            if not ok:
                fail(f"{where}: no 2xx response")
            for code in ok:
                r = responses[code]
                if code != "204" and "$ref" not in r and "content" not in r:
                    fail(f"{where} {code}: response without a schema")
            if op.get("security", spec["security"]) and "401" not in responses:
                fail(f"{where}: secured but no 401")
            for code, r in responses.items():
                if code[0] == "4" and "$ref" not in r and code != "422":
                    fail(f"{where} {code}: use a components/responses error")


def main() -> int:
    schemas, registry = load_schemas()
    check_fixtures(schemas, registry)
    check_canonical_registries()
    palette = check_graphs(registry)
    check_pipelines(registry, palette)
    check_mentions(registry, palette)
    check_loras()
    check_lora_detection()
    check_openapi()
    check_names()
    print(f"\n{len(errors)} failure(s)" if errors else "\nall contract checks passed")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
