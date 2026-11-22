"""Contract self-check: every fixture against its schema, and a lint of openapi.yaml.

    python contracts/check.py

Needs jsonschema (+ rfc3339-validator for date-time), PyYAML, openapi-spec-validator. Fixtures are
fixtures/{messages,registry,http}/<name>.{valid,invalid}[.N].json: every valid one must pass, every invalid one must fail.
Registry fixtures may carry a family prefix (qwen-image.nodes.valid.json): <family>.<key>. HTTP fixtures are bodies
checked against openapi.yaml components/schemas/<name>. fixtures/graphs/ run through the reference graph rules below
(README, Graph rules) against fixtures/graphs/palette.json; fixtures/pipelines/ through the reference pipeline rules
(README, Pipeline rules) against fixtures/pipelines/workflows.json and the same palette; fixtures/mentions/ through
the reference mention rules (README, Mentions) against the same palette; fixtures/loras/ through the reference
LoRA normaliser (README, LoRA normaliser) against the canonical qwen-image lora block (or the fixture's `family`);
fixtures/decisions/ through the reference judge verdict and replay hash (README, Agent nodes);
fixtures/video/ through the reference auto length and keyframe position rules (README, Video); fixtures/dispatch/
through the reference host admission (`admit`), dispatch order (`simulate`) (README, The hand-over) and placement
among several hosts (`place`) (README, Several hosts);
fixtures/timelines/ through the reference timeline rules (`t_normalize`, `t_edit`, `snap_point`, `frame_at`,
`export_plan`, `shot_keyframes`, `rerender_steps`) (README, Timeline rules); fixtures/voices/ through the reference voice rules (README, Voices; from
API 1.32 Dialogue and Timeline audio: `expand_script`, `echo_dialogue`, `script_check`, `make_shot`).
Exit code 1 on any failure."""

import copy
import hashlib
import json
import math
from decimal import ROUND_HALF_UP, Decimal
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
CANONICAL = ["", "qwen-image.", "ltx.", "llm.", "server.", "minimax-h3-image.", "minimax-h3.", "qwen3-tts.", "zonos3."]
REGISTRY_KEYS = [
    "nodes",
    "workflows",
    "models",
    "loras",
    "looks",
    "voices",
    "host",
    "custom",
    "settings",
]  # registry.schema.json $defs; fixtures/registry/<key>.*
# openapi.yaml components/schemas with body fixtures in fixtures/http/<name>.*
HTTP_SCHEMAS = [
    "Run", "RunRequest", "RunPlan", "Queue", "Take", "TakePage", "TakePatch", "TakeReuse", "TakeUse", "UploadCreate", "Upload",
    "Batch", "BatchCreate", "BatchRow", "BatchEvent",
    "Workflow", "WorkflowCreate", "WorkflowUpdate", "WorkflowPatch", "WorkflowValidation", "WorkflowError", "NodePalette",
    "Pipeline", "PipelineCreate", "PipelineUpdate", "PipelinePatch", "PipelineValidation", "PipelineError", "TakePipeline",
    "BatchPick", "BatchRowRetry", "BatchReview", "RowExecution", "RowState",
    "LibraryItem", "LibraryItemList", "LibraryItemCreate", "LibraryItemUpdate",
    "Lora", "LoraList", "LoraPatch", "LoraUploadCreate", "LoraUpload",
    "HostList",
    "Decision", "DecisionList", "MemoryItem", "MemoryList", "MemoryCreate", "BatchAgain",
    "ChatMessage", "ChatPage", "ChatPost", "Provider", "ProviderPut", "ProviderList", "ProviderTest", "ModelSourceList",
    "CustomNodeList",
    "ModelList", "ModelDownload", "ModelJob", "ModelJobList", "HostSettings", "HostSettingsPut", "HostPins", "HostUnload",
    "HostUnloadAccepted", "ImportTakeCreate", "ImportTake",
    "Timeline", "TimelineList", "TimelineCreate", "TimelineUpdate", "TimelineAddClips", "TimelineShotCreate",
    "TimelineRerender", "ExportPlan", "MediaJob", "TakeFrame", "TakeUses", "ClipActions", "TimelineLineCreate",
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
        out += [f"{nid}.{k}: {err}" for k, err in values_by_errors(schema, node.get("params", {}))]
        out += [f"{nid}.{k}: {err}" for k, err in only_errors(schema, node)]
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


def is_take(spec) -> bool:
    """A rows item field that holds a take: ref kind Image or Media, or (API 1.22) a media param."""
    return spec in ("Image", "Media") or (isinstance(spec, dict) and spec["type"] == "media")


def is_voice(spec) -> bool:
    """A rows item field that holds a saved voice (API 1.32: a dialogue's `lines`)."""
    return isinstance(spec, dict) and spec["type"] == "voice"


def voice_field(param: dict) -> str | None:
    """A rows param's one voice field (API 1.32), else None."""
    fields = [k for k, v in param.get("item", {}).items() if is_voice(v)]
    return fields[0] if param["type"] == "rows" and len(fields) == 1 else None


def media_field(param: dict) -> str | None:
    """A rows param's one media field (API 1.22: video or audio references), else None."""
    fields = [k for k, v in param.get("item", {}).items() if isinstance(v, dict) and v["type"] == "media"]
    return fields[0] if param["type"] == "rows" and len(fields) == 1 else None


def value_error(param: dict, v) -> str | None:
    """What studio checks when it resolves a run (type, min/max, enum); also applied to every default."""
    t = param["type"]
    if t == "seed" and v is None:
        return None
    if t in ("image", "media"):  # a registry workflow holds no takes: an image or media param stays empty
        return None if v is None else f"{v!r}: a workflow cannot fill an {t} param"
    if t == "voice":  # API 1.30: a workflow holds no voice either; a run names one (README, Voices)
        return None if v is None else f"{v!r}: a workflow cannot fill a voice param"
    if t == "span":  # {start, end} seconds, start < end (README, Media inputs); null = none yet
        ok = v is None or (isinstance(v, dict) and set(v) == {"start", "end"} and all(
            isinstance(x, (int, float)) and not isinstance(x, bool) for x in v.values()) and 0 <= v["start"] < v["end"])
        return None if ok else f"{v!r} is not a range {{start, end}} with 0 <= start < end"
    if t == "rows":
        if not isinstance(v, list):
            return f"{v!r} is not a list"
        if v == [] and any(is_take(x) or is_voice(x) for x in param["item"].values()):
            return None  # a workflow holds no takes or voices: [] even under min; the run needs min items (README, Video)
        if not param.get("min", 0) <= len(v) <= param["max"]:
            return f"{len(v)} items is outside {param.get('min', 0)}..{param['max']}"
        for i, row in enumerate(v, 1):
            if not isinstance(row, dict) or set(row) - set(param["item"]):
                return f"item {i}: fields must be among {sorted(param['item'])}"
            for f, spec in param["item"].items():
                if is_take(spec):  # a take; never in a registry workflow
                    return f"item {i}.{f}: a workflow cannot fill an image" if spec in ("Image", "Media") \
                        else f"item {i}.{f}: a workflow cannot fill a media field"
                if is_voice(spec):  # API 1.32: nor a voice
                    return f"item {i}.{f}: a workflow cannot fill a voice field"
                if spec == "Lora":  # required in every item, {lora_id} (README, LoRAs)
                    v_ = row.get(f)
                    if not isinstance(v_, dict) or set(v_) != {"lora_id"} or not LORA_ID.match(str(v_["lora_id"])):
                        return f"item {i}.{f}: a LoRA is required ({{lora_id}})"
                    continue
                if f in row and (err := value_error(spec, row[f])):
                    return f"item {i}.{f}: {err}"
        return None
    if t == "frames":  # on the frame grid min + k·step (README, Video: Params); "auto" where declared
        if v == "auto":
            return None if "auto" in param else "auto length is not offered"
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
    if t == "int" and "values" in param and v not in param["values"]:
        return f"must be one of {', '.join(map(str, param['values']))}"
    n = len(v) if t in ("string", "text") else v
    if t != "enum" and t != "bool" and not param.get("min", n) <= n <= param.get("max", n):
        return f"{v!r} is outside {param.get('min')}..{param.get('max')}"
    return None


def only_errors(schema: dict, node: dict) -> list[tuple[str, str]]:
    """(param, message) for a workflow node's `only` (API 1.19): an enum param, a subset of its enum, holding the
    node's merged value; the form offers only those values (README, Media inputs: pinned variant). From 1.30 also a
    voice param, a subset of its accepts: the fit rule reads the narrowed set (voice_params), a value is not checked here;
    from 1.32 also a rows param with one voice field (a dialogue's lines), a subset of that field's accepts."""
    params, out = {p["name"]: p for p in schema["params"]}, []
    for k, allowed in node.get("only", {}).items():
        p = params.get(k)
        pool = (p["enum"] if p["type"] == "enum" else p["accepts"] if p["type"] == "voice"
                else p["item"][voice_field(p)]["accepts"] if voice_field(p) else None) if p else None
        if pool is None or set(allowed) - set(pool):
            out.append((k, "only must name an enum, voice or voice rows param and values of it"))
        elif p["type"] == "enum" and node.get("params", {}).get(k, p["default"]) not in allowed:
            out.append((k, f"must be one of {', '.join(allowed)}"))
    return out


def values_by_errors(schema: dict, values: dict) -> list[tuple[str, str]]:
    """(param, message) for every int param whose `values_by` refuses the node's merged value (schema defaults, then
    `values`): its allowed subset for the node's value of `values_by.param` (README, Video: steps)."""
    merged = {p["name"]: p["default"] for p in schema["params"]} | values
    out = []
    for p in schema["params"]:
        vb = p.get("values_by")
        allowed = vb["values"].get(merged.get(vb["param"]), p["values"]) if vb else None
        if allowed is not None and merged.get(p["name"]) in p["values"] and merged[p["name"]] not in allowed:
            out.append((p["name"], f"must be one of {', '.join(map(str, allowed))}"))
    return out


def schema_errors(n: dict) -> list[str]:
    """What JSON Schema can't say about one node schema's `values`, `values_by`, `auto`."""
    out, params = [], {p["name"]: p for p in n["params"]}
    for p in n["params"]:
        vs, vb, auto = p.get("values"), p.get("values_by"), p.get("auto")
        if vs and (vs != sorted(vs) or p["default"] not in vs or not all(p.get("min", x) <= x <= p.get("max", x) for x in vs)):
            out.append(f"{p['name']}: values must ascend, lie within min..max and hold the default")
        if vb:
            ep = params.get(vb["param"])
            if not ep or ep["type"] != "enum" or set(vb["values"]) - set(ep["enum"]) or any(
                    set(x) - set(vs) for x in vb["values"].values()):
                out.append(f"{p['name']}: values_by must name an enum param of the node, its values, subsets of values")
            elif p["default"] not in vb["values"].get(ep["default"], vs):
                out.append(f"{p['name']}: the default is not allowed for {vb['param']} {ep['default']}")
        if auto and not 0 < auto["min_s"] < auto["max_s"]:
            out.append(f"{p['name']}: auto needs 0 < min_s < max_s")
        if set(p.get("not_with", [])) - {i["name"] for i in n["inputs"] if i.get("optional")}:
            out.append(f"{p['name']}: not_with must name optional inputs of the node")
        if "max_total_s" in p and not media_field(p):
            out.append(f"{p['name']}: max_total_s needs an item with exactly one media field")
        for x in [p] + [v for v in p.get("item", {}).values() if isinstance(v, dict)]:
            if x.get("min_s", 0) > x.get("max_s", math.inf):
                out.append(f"{p['name']}: min_s is above max_s")
        if p.get("script") and not script_target(n):  # API 1.32 (README, Dialogue)
            out.append(f"{p['name']}: a script needs a rows param `lines` with voice, text, style and pause_s fields")
    return out


def script_target(schema: dict) -> dict | None:
    """The rows param a node's script expands into: `lines`, with one voice field `voice` and the string fields
    `text`, `style` and the float `pause_s` (API 1.32), else None."""
    p = next((q for q in schema["params"] if q["name"] == "lines" and q["type"] == "rows"), None)
    it = p["item"] if p else {}
    typ = lambda k: it[k]["type"] if isinstance(it.get(k), dict) else None
    ok = p and voice_field(p) == "voice" and set(it) == {"voice", "text", "style", "pause_s"} and \
        (typ("text"), typ("style"), typ("pause_s")) == ("string", "string", "float")
    return p if ok else None


def check_canonical_registries() -> None:
    """The canonical fixtures (a runner publishes them verbatim) must fit together."""
    reg = HERE / "fixtures" / "registry"
    for prefix in CANONICAL:
        nodes = json.loads((reg / f"{prefix}nodes.valid.json").read_text())["nodes"]
        kinds = {n["kind"]: n for n in nodes}
        for n in nodes:
            for e in schema_errors(n):
                fail(f"fixtures/registry/{prefix}nodes.valid.json {n['kind']}.{e}")
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
MEDIA = {"Image", "Video", "Audio"}  # what an input of type Media accepts (Audio from API 1.30)


def fits(got: str | None, want: str) -> bool:
    """A wire of type `got` may feed a port of type `want` (README, Graph rules: fits); handles also need one family."""
    return got is not None and (got == want or (want == "Media" and got in MEDIA))


def workflow_media(nodes: dict, kinds: dict) -> str:
    """text when an answer node's text is wired (README, The llm pack); else video when any output node's media is
    wired from a Video output, else image when any from an Image, else (API 1.30) audio when any from an Audio; else
    image (README, Video; Voices). Not node order: the first type of that list that any output has."""
    if any(n["kind"] == "answer" and "text" in n.get("in", {}) for n in nodes.values()):
        return "text"
    got = set()
    for n in nodes.values():
        ref = n.get("in", {}).get("media") if n["kind"] == "output" else None
        src = nodes.get(ref.split(".")[0]) if ref else None
        ks = kinds.get(src["kind"]) if src else None
        got |= {o["type"] for o in ks["outputs"] if o["name"] == ref.split(".")[1]} if ks else set()
    return next((t.lower() for t in ("Video", "Image", "Audio") if t in got), "image")
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
        for k, _ in values_by_errors(s, n.get("params", {})):
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
                if (value_error(spec, v) or param in dict(values_by_errors(s, merged))
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
                      loras: dict[str, str] | None = None, info: dict | None = None, providers: list[dict] | None = None,
                      remote_on: bool = False, custom: list[dict] | None = None) -> set[tuple]:
    """Every problem as (code, node, port, param); workflows: the id → WorkflowDoc the caller can run; looks, loras:
    the looks and LoRAs the caller can use now (as resolve_mentions takes them); providers: the enabled model
    providers (name, kind, models, default_model, host, memory_gb); remote_on: the deployment's remote switch;
    custom: the registered custom nodes. `info`, when given, receives the worst case per row (`bounds`), the model
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
        elif n["kind"] == "judge":
            wid = family_ask(n["takes"])
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
                                fx.get("providers"), fx.get("remote_deployment", False), fx.get("custom", custom))
        want = {(x["code"], x.get("node"), x.get("port"), x.get("param")) for x in fx["problems"]}
        if not p.name.startswith(("valid.", "invalid.")) or p.name.startswith("valid.") != (not want):
            fail(f"{name}: valid.* has no problems, invalid.* has some")
        if got != want:
            fail(f"{name}: got {sorted(got, key=str)}, want {sorted(want, key=str)}")
        elif bad := [k for k in ("bounds", "models", "egress", "reserved") if k in fx and fx[k] != info.get(k)]:
            fail(f"{name}: got {', '.join(f'{k} {info.get(k)}' for k in bad)}; want {', '.join(f'{k} {fx[k]}' for k in bad)}")
        else:
            print(f"ok   {name}")


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


def excluded(n: dict, p: dict) -> bool:
    """A rows param that must stay empty in this graph: an input its `not_with` names is wired (API 1.22)."""
    return any(i in n.get("in", {}) for i in p.get("not_with", []))


def reference_rows(nodes: dict, palette: dict) -> tuple[str, dict] | None:
    """Where element images go: the first (node id, then schema order) rows param with a `{n}` mention and one
    Image field, on a known, not bypassed node, not excluded by a wired `not_with` input; None = the family takes
    text only."""
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            if p["type"] == "rows" and "{n}" in p.get("mention", "") and image_field(p) and p["name"] not in n.get("in", {}) \
                    and not excluded(n, p):
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
    last user row's take (clamped to the params' min and max; unknown size: mode only). A `minimax-h3-image/canvas`
    (auto follows the first row) is pinned only without user rows, its width x height kept."""
    last = before[-1].get(field) if before else None
    size = sizes.get(last["take_id"]) if isinstance(last, dict) else None
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        first = n["kind"] == "minimax-h3-image/canvas"
        if n["kind"] != "canvas" and not first or not s or n.get("bypass") or first and before:
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
    if workflow_media(nodes, palette) == "audio":  # API 1.30: spoken text is never expanded (README, Voices)
        return "mentions are not read aloud; use a dialogue script" if item.get("kind") == "element" \
            else "looks do not apply to speech"
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
    if workflow_media(nodes, palette) == "audio":  # API 1.30: nothing is expanded; a mention is refused (README, Voices)
        errors = [f"{path}: mentions are not read aloud; use a dialogue script" for _, path, _, parts in prompts
                  if mentioned(parts)]
        prompts = []
    else:
        errors = [f"{path}: unknown element @{h}" for _, path, _, parts in prompts for h in mentioned(parts)
                  if h not in lib]
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
            end = lambda r: r.get("at", "start") == "end"
            r = next((r for r in spec["renames"] if (part.endswith if end(r) else part.startswith)(r["from"])), None)
            if r:
                part = part[:-len(r["from"])] + r["to"] if end(r) else r["to"] + part[len(r["from"]):]
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
    lora block of the fixture's `family` (default qwen-image; an optional `ignore` replaces its ignore list)."""
    blocks = {}
    for prefix in CANONICAL[1:]:
        if not (f := HERE / "fixtures" / "registry" / f"{prefix}models.valid.json").exists():
            continue  # server: no models of its own
        m = json.loads(f.read_text())["models"][0]
        if "lora" not in m:
            continue  # a family without LoRAs (llm)
        blocks[m["family"]] = spec = m["lora"]
        mods = expand_modules(spec["modules"])
        if len({m.replace(".", "_") for m in mods}) != len(mods):
            fail(f"{prefix}models lora block: module names are not unique with . read as _ (kohya)")
        if spec["modules"] != sorted(spec["modules"]):
            fail(f"{prefix}models lora block: modules are not sorted")
    for p in sorted((HERE / "fixtures" / "loras").glob("*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/loras/{p.name}"
        fam = fx.get("family", "qwen-image")
        s = blocks[fam] | ({"ignore": fx["ignore"]} if "ignore" in fx else {})
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


# ---------------------------------------------------------------- video rules (README, "Video")
def auto_frames(seconds: float, fps: float, min_s: float, max_s: float, param: dict) -> int:
    """Auto length: the predicted seconds as frames, clamped to min_s..max_s and the param's max, floored onto the
    grid min + k·step and snapped up one step when that fell below min_s (round = half to even, as the old studio)."""
    top = param["min"] + (param["max"] - param["min"]) // param["step"] * param["step"]
    lo, hi = round(min_s * fps), min(round(max_s * fps), top)
    f = min(max(round(seconds * fps), lo), hi)
    f = param["min"] + max(f - param["min"], 0) // param["step"] * param["step"]
    return min(f + param["step"] if f < lo else f, top)


def clip_of(nodes: dict, palette: dict) -> tuple | None:
    """(frames or "auto", fps) of the workflow's one unwired frames param on a known, not bypassed node whose fps
    param is unwired; None when there is not exactly one."""
    found = []
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            if p["type"] == "frames" and p["name"] not in n.get("in", {}) and p["fps"] not in n.get("in", {}):
                val = lambda name: n.get("params", {}).get(name, next(q["default"] for q in s["params"] if q["name"] == name))
                found.append((val(p["name"]), val(p["fps"])))
    return found[0] if len(found) == 1 else None


def position_errors(nodes: dict, palette: dict) -> list[str]:
    """Keyframe positions (README, Video: keyframes): each `position: seconds` item field of an unwired rows param
    of a known, not bypassed node is the pixel frame min(round(s·fps), F − 1); past the clip (round(s·fps) > F) or a
    frame an earlier row of that param has is a 422. With auto length (or no single clip) the past-the-clip check
    is left to the runner."""
    clip, out = clip_of(nodes, palette), []
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            fields = [k for k, v in p.get("item", {}).items() if isinstance(v, dict) and v.get("position") == "seconds"]
            if p["type"] != "rows" or p["name"] in n.get("in", {}) or not fields or not clip:
                continue
            frames, fps = clip
            for f in fields:
                seen = {}
                for i, row in enumerate(n.get("params", {}).get(p["name"], p["default"]), 1):
                    fr = round(row.get(f, p["item"][f]["default"]) * fps)
                    where = f"{nid}.{p['name']}[{i}].{f}"
                    if isinstance(frames, int) and fr > frames:
                        out.append(f"{where}: frame {fr} is past the clip's last frame {frames - 1}")
                        continue
                    fr = min(fr, frames - 1) if isinstance(frames, int) else fr
                    if fr in seen:
                        out.append(f"{where}: frame {fr} is already used by row {seen[fr]}")
                    else:
                        seen[fr] = i
    return out


# ---------------------------------------------------------------- media inputs (README, "Media inputs")
LTX_MAX_FRAMES = 321  # ltx/duration's max: the longest clip retake, extend and audio to video make


def ltx_floor(n: int) -> int:
    """A source's frames floored to LTX's 8k + 1 (k ≥ 1), as the old studio reads it."""
    return 8 * max(1, (n - 1) // 8) + 1


def retake_map(start: float, end: float, fps: float, frames: int) -> dict:
    """ltx/retake on whole frames (studio snaps the span to the take's frames): f = rnd(t·fps), S = ⌊(f_start + 4)/8⌋,
    E = max(⌊(f_end + 4)/8⌋, S + 1) clamped to the F latent frames (end exclusive), in integer arithmetic: f/8 rounded
    half up, as the old UI's Math.round (tasks.js). Pixels max(8S − 7, 0) … 8(E − 1) are regenerated. An end at or past
    the clip's end (f_end ≥ n) is the last latent frame (ours: the old UI's round(duration·fps/8) left the last 8
    frames out). The output is the whole source (floored to 8k + 1), offset 0."""
    n, fs, fe = ltx_floor(frames), rnd(start * fps), rnd(end * fps)
    s, last = (fs + 4) // 8, (n - 1) // 8 + 1
    e = last if fe >= n else min(max((fe + 4) // 8, s + 1), last)
    if s >= e:
        return {"error": "retake.span: nothing to regenerate on the 8-frame grid"}
    return {"latent": [s, e], "frames": n,
            "map": {"offset_s": 0, "new_in_s": max(8 * s - 7, 0) / fps, "new_out_s": (8 * (e - 1) + 1) / fps}}


def extend_map(seconds: float, direction: str, fps: float, frames: int) -> dict:
    """ltx/extend: N = max(1, ceil(seconds·fps/8)) latent frames (8N pixel frames) after or before the source floored
    to 8k + 1; the output is the whole extended clip, at most 321 frames."""
    n = ltx_floor(frames)
    k = max(1, math.ceil(seconds * fps / 8 - 1e-9))
    total, add = n + 8 * k, 8 * k / fps
    if total > LTX_MAX_FRAMES:
        return {"error": f"extend.seconds: the clip would be {total} frames; at most {LTX_MAX_FRAMES}"}
    m = {"offset_s": 0, "new_in_s": n / fps, "new_out_s": total / fps} if direction == "after" else \
        {"offset_s": add, "new_in_s": 0, "new_out_s": add}
    return {"latent": k, "frames": total, "map": m}


def audio_map(in_s: float, out_s: float, fps: float) -> dict:
    """ltx/encode-audio: the clip's frames = the part in..out at fps, floored to 8k + 1 (at most 321); the output's 0
    is the source's `in`."""
    f = min(LTX_MAX_FRAMES, (math.floor((out_s - in_s) * fps + 1e-6) - 1) // 8 * 8 + 1)
    if f < 9:
        return {"error": "audio.source: the part from in to out is shorter than 9 frames"}
    return {"frames": f, "map": {"offset_s": -in_s, "new_in_s": 0, "new_out_s": f / fps}}


def media_fit(p: dict, take: dict) -> str | None:
    """Why a take does not fit a `media` param (the 422 text after `<node>.<param>: `), else None. A trim part longer
    than max_s is the value's own 422 (README, Media inputs); an offer caps it instead."""
    tid, kinds = take["id"], p["accepts"]
    picture = take["kind"] == "video" and "video" in kinds
    if not picture and not ("audio" in kinds and take["kind"] in ("audio", "video") and take.get("has_audio", True)):
        return f"take {tid} has no sound" if kinds == ["audio"] else f"take {tid} is not a video"
    if picture and p.get("grid") and (take["width"] % p["grid"] or take["height"] % p["grid"]):
        return f"take {tid} is {take['width']} × {take['height']}; this input needs sides on a {p['grid']} px grid"
    if picture and p.get("max_frames") and take["frames"] > p["max_frames"]:
        return f"take {tid} has {take['frames']} frames; this input takes at most {p['max_frames']}"
    if p.get("min_s") and take["duration_s"] < p["min_s"]:  # API 1.22; a trim cannot make it longer
        return f"take {tid} lasts {take['duration_s']:.1f} s; this input takes at least {p['min_s']:.1f} s"
    if p.get("max_s") and take["duration_s"] > p["max_s"] and not p.get("trim"):
        return f"take {tid} lasts {take['duration_s']:.1f} s; this input takes at most {p['max_s']:.1f} s"
    return None


def placed_paths(wf: dict) -> set[str] | None:
    """The paths a workflow's studio block places (composer, quick, inspector); None without one (every param shows).
    Create shows exactly those, so "Use in…" fills no other (API 1.22)."""
    st = wf.get("studio")
    if st is None:
        return None
    return set(st.get("composer", []) + st.get("quick", [])) | {x for sec in st.get("inspector", []) for x in sec["items"]}


def media_value(spec: dict, ref: dict, take: dict | None) -> tuple[str | None, dict]:
    """Studio's check of one media value (a media param or, API 1.22, a rows item's media field): the 422 text after
    `<path>: ` or None, and the value with a trim field's in / out filled (in 0, out min(duration, max_s)). In / out
    are taken as already snapped (README, Media inputs: Values)."""
    if take is None:
        return f"take {ref['take_id']} not found", ref
    if err := media_fit(spec, take):
        return err, ref
    if not spec.get("trim"):
        return ("this input takes the whole take (no in or out)" if {"in", "out"} & set(ref) else None), ref
    d = take["duration_s"]
    a, b = ref.get("in", 0), ref.get("out", min(d, spec.get("max_s", d)))
    if a >= b:
        return "in must be before out", ref
    if b > d:
        return f"out {b:.1f} is past the take's end {d:.1f}", ref
    if spec.get("max_s") and b - a > spec["max_s"]:
        return f"the part lasts {b - a:.1f} s; this input takes at most {spec['max_s']:.1f} s", ref
    if spec.get("min_s") and b - a < spec["min_s"]:
        return f"the part lasts {b - a:.1f} s; this input takes at least {spec['min_s']:.1f} s", ref
    return None, ref | {"in": a, "out": b}


def media_rows(nodes: dict, palette: dict, takes: dict) -> dict:
    """Studio's run checks of API 1.22 (README, Media inputs: rows of media) on merged, type-checked values: an
    `optional` node left empty is dropped with its wires, then a rows param with a value while an input its
    `not_with` names is wired is refused, then every rows param with one media field is checked item by item
    (`media_value`, items from 1) and over `max_total_s`. Answers {error} or {error: null, dropped, values: path → the
    rows with in / out filled}."""
    drop = sorted(nid for nid, n in nodes.items() if n.get("optional") and n.get("params", {}).get("image") is None)
    nodes = {nid: n | {"in": {k: r for k, r in n.get("in", {}).items() if r.split(".")[0] not in drop}}
             for nid, n in nodes.items() if nid not in drop}
    values = {}
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            path, v = f"{nid}.{p['name']}", n.get("params", {}).get(p["name"], p["default"])
            if p["type"] != "rows" or not v or p["name"] in n.get("in", {}):
                continue
            if wired := next((i for i in p.get("not_with", []) if i in n["in"]), None):
                return {"error": f"{path}: must stay empty while {nid}.{wired} is wired"}
            if not (f := media_field(p)):
                continue
            rows, total = [], 0.0
            for i, row in enumerate(v, 1):
                take = takes.get(row[f]["take_id"])
                err, ref = media_value(p["item"][f], row[f], take)
                if err:
                    return {"error": f"{path}[{i}].{f}: {err}"}
                rows.append(row | {f: ref})
                total += ref["out"] - ref["in"] if "in" in ref else take["duration_s"]
            if "max_total_s" in p and total > p["max_total_s"] + 1e-9:
                return {"error": f"{path}: the takes last {total:.1f} s in all; at most {p['max_total_s']:.1f} s"}
            values[path] = rows
    return {"error": None, "dropped": drop, "values": values}


def extend_room(take: dict, rng: tuple | None) -> dict | None:
    """ltx/extend's pre-fill for a video take (None: not offered): the default 2 s, or the most whole 8-frame steps
    under 321 frames as seconds (a 0.1 s step, rounded down); a Timeline range gives the direction: after when it ends
    at the take's end (within one frame), else before when it starts at 0, else none."""
    fps, n = take["fps"], ltx_floor(take["frames"])
    k = (LTX_MAX_FRAMES - n) // 8
    if k < 1:
        return None
    out = {} if n + 8 * math.ceil(2 * fps / 8) <= LTX_MAX_FRAMES else {"seconds": math.floor(8 * k / fps * 10) / 10}
    if rng:
        if take["duration_s"] - rng[1] < 1 / fps:
            out["direction"] = "after"
        elif rng[0] < 0.5 / fps:
            out["direction"] = "before"
        else:
            return None
    return out


def take_uses(take: dict, workflows: list[dict], palette: dict, rng: tuple | None = None) -> list[dict]:
    """"Use in…" offers (README, Media inputs: offers): every workflow with exactly one unwired input of the take's
    sort on a known, not bypassed, not optional node (an image: an image param or Image rows; a video or audio take: a
    media param whose `accepts` names its kind, or `audio` for a video with sound) that the take fits, with the inputs
    pre-filled. From API 1.22 an input counts only when the workflow's studio block places it (none: every input) and
    a rows param excluded by a wired `not_with` input does not count.
    rng: (in, out) in the take's own seconds (a Timeline clip or selection), else the whole take."""
    out = []
    for wf in workflows:
        found, shown = [], placed_paths(wf)
        for nid in sorted(wf["nodes"]):
            n, s = wf["nodes"][nid], palette.get(wf["nodes"][nid]["kind"])
            for p in (s["params"] if s and not n.get("bypass") and not n.get("optional") else []):
                if p["name"] in n.get("in", {}) or excluded(n, p) or (shown is not None and f"{nid}.{p['name']}" not in shown):
                    continue
                if take["kind"] == "image" and (p["type"] == "image" or image_field(p)):
                    found.append((nid, p))
                elif take["kind"] != "image" and p["type"] == "media" and (
                        take["kind"] in p["accepts"] or (take["kind"] == "video" and "audio" in p["accepts"])):
                    found.append((nid, p))
                elif take["kind"] != "image" and media_field(p) and take["kind"] in p["item"][media_field(p)]["accepts"]:
                    found.append((nid, p))  # 1.32: rows of one media field, by the take's own kind only
        spec = (found[0][1]["item"][media_field(found[0][1])] if media_field(found[0][1]) else found[0][1]) if found else None
        if len(found) != 1 or (take["kind"] != "image" and media_fit(spec, take)):
            continue
        (nid, p), path = found[0], f"{found[0][0]}.{found[0][1]['name']}"
        a, b = rng or (0, take.get("duration_s"))
        if p["type"] == "image":
            inputs = {path: {"take_id": take["id"]}}
        elif p["type"] == "rows" and image_field(p):
            inputs = {path: [{image_field(p): {"take_id": take["id"]}}]}
        elif p["type"] == "rows":  # 1.32: the take as item 1 of a rows param of one media field
            longest = min(spec.get("max_s", math.inf), p.get("max_total_s", math.inf))
            part = {"in": a, "out": min(b, a + longest)} if spec.get("trim") else {}
            n = part["out"] - a if part else b - a  # the part offered (a Timeline range) holds the field's limits
            if n < spec.get("min_s", 0) - 1e-9 or n > spec.get("max_s", math.inf) + 1e-9:
                continue
            inputs = {path: [{media_field(p): {"take_id": take["id"]} | part}]}
        else:
            cap = min(b, a + p["max_s"]) if p.get("max_s") else b
            inputs = {path: {"take_id": take["id"]} | ({"in": a, "out": cap} if p.get("trim") else {})}
            if take["kind"] == "video" and "video" in p["accepts"]:
                inputs |= {f"{x}.{q['name']}": {"start": a, "end": b} for x in sorted(wf["nodes"])
                           for q in palette.get(wf["nodes"][x]["kind"], {}).get("params", []) if q["type"] == "span"}
                ext = [x for x in sorted(wf["nodes"]) if wf["nodes"][x]["kind"].endswith("/extend")]  # ltx/extend, echo/extend
                if ext and (room := extend_room(take, rng)) is None:
                    continue
                inputs |= {f"{x}.{k}": v for x in ext for k, v in room.items()} if ext else {}
        out.append({"workflow_id": wf["id"], "title": wf["title"]} | ({"action": wf["action"]} if "action" in wf else {})
                   | {"family": wf["family"], "media": wf.get("media", "image"), "param": path, "inputs": inputs})
    return out


# ---------------------------------------------------------------- minimax-h3 generate (README, the minimax-h3 family)
H3_MAX_PIXELS = 768 * 1344  # 1,032,192: the h3 binary's largest canvas
H3_MAX_REFS, H3_MAX_SOUNDS, H3_MAX_SOUND_S, H3_SOUND_FRAMES = 12, 3, 15.0, 56  # h3.c's Ref2VA limits


def h3_generate_error(node: str, refs: int, first: bool, last: bool, width: int, height: int, frames: int = 124,
                      videos: list | None = None, audios: list | None = None) -> str | None:
    """minimax-h3/generate's refusals, before the child starts (the run fails with the text). refs = image
    references; videos = [seconds read (out - in), sound] per video reference, sound true when the row keeps it and
    the take has sound; audios = seconds read per audio reference (API 1.22). In order: any reference with a first
    or last frame (Ref2VA takes no anchors); audio references without an image or video one; more than 12
    references; more than 3 sound inputs (audio references and videos with sound); a video with sound under 56
    frames, or whose part reads under 56 frames (h3 reads a video reference up to the clip's frames, keeps 5 + 17k of
    them and reads its sound only that long; it needs 2 s); more than 15 s of sound in
    all (an audio's part, a video's sound up to the clip's length); a side off 256..1344 or off the 32 px grid;
    more than 768 × 1344 pixels. Studio checks each row (2-15 s), the counts and each kind's 15 s; these cross the
    params, need the clip's frames or the take's sound, or a canvas that may come from any Canvas output."""
    videos, audios = videos or [], audios or []
    sounds = [s for _, s in videos if s]
    if (refs or videos or audios) and (first or last):
        return f"{node}: references cannot go with a first or last frame"
    if audios and not (refs or videos):
        return f"{node}: audio references need an image or video reference"
    if (n := refs + len(videos) + len(audios)) > H3_MAX_REFS:
        return f"{node}: at most {H3_MAX_REFS} references in all ({n})"
    if (n := len(audios) + len(sounds)) > H3_MAX_SOUNDS:
        return f"{node}: at most {H3_MAX_SOUNDS} sound references (audio references and videos with sound; {n})"
    if sounds and frames < H3_SOUND_FRAMES:
        return f"{node}: a video reference with sound needs a clip of at least {H3_SOUND_FRAMES} frames ({frames})"
    for i, (x, s) in enumerate(videos, 1):
        if s and (n := round(x * 24)) < H3_SOUND_FRAMES:  # h3 keeps 5 + 17k frames, reads the sound that long
            return (f"{node}: a video reference with sound needs a part of at least {H3_SOUND_FRAMES} frames at 24 fps "
                    f"(video {i} reads {n})")
    if (t := sum(audios) + sum(min(x, frames / 24) for x, s in videos if s)) > H3_MAX_SOUND_S + 1e-9:
        return f"{node}: the sound references last {t:.1f} s in all; at most {H3_MAX_SOUND_S:.1f} s"
    if any(not 256 <= x <= 1344 or x % 32 for x in (width, height)):
        return f"{node}: the canvas is {width} × {height}; sides must be multiples of 32 from 256 to 1344"
    if width * height > H3_MAX_PIXELS:
        return f"{node}: the canvas is {width} × {height}; at most {H3_MAX_PIXELS} pixels (768 × 1344)"
    return None


def check_media(registry: Registry) -> None:
    g, reg = HERE / "fixtures" / "video", HERE / "fixtures" / "registry"
    fx = json.loads((g / "sources.json").read_text())
    for c in fx["cases"]:
        fn = {"retake": retake_map, "extend": extend_map, "audio": audio_map}[c["kind"]]
        if (got := fn(*c["args"])) != c["expect"]:
            fail(f"fixtures/video/sources.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/sources.json")
    for c in json.loads((g / "minimax-h3.json").read_text())["cases"]:
        if (got := h3_generate_error(*c["args"])) != c["expect"]:
            fail(f"fixtures/video/minimax-h3.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/minimax-h3.json")
    pres = ("ltx.", "qwen-image.", "minimax-h3-image.", "minimax-h3.")
    pal = {n["kind"]: n for pre in pres for n in json.loads((reg / f"{pre}nodes.valid.json").read_text())["nodes"]}
    wfs = [w for pre in pres for w in json.loads((reg / f"{pre}workflows.valid.json").read_text())["workflows"]]
    uses_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/TakeUses"}, registry=registry,
                                  format_checker=FormatChecker())
    fx = json.loads((g / "uses.json").read_text())
    for c in fx["cases"]:
        take = fx["takes"][c["take"]] | {"id": c["take"]}
        got = {"uses": take_uses(take, wfs, pal, tuple(c["range"]) if "range" in c else None)}
        if c.get("param"):
            got = {"error": media_fit(c["param"], take)}
        elif errs := list(uses_v.iter_errors(got)):
            fail(f"fixtures/video/uses.json {c['description']!r}: not TakeUses: {errs[0].message}")
        if got != c["expect"]:
            fail(f"fixtures/video/uses.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/uses.json")
    inputs_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/RunInputs"}, registry=registry,
                                    format_checker=FormatChecker())
    fx, by_id = json.loads((g / "media-rows.json").read_text()), {w["id"]: w for w in wfs}
    takes = {k: t | {"id": k} for k, t in fx["takes"].items()}
    for c in fx["cases"]:
        if errs := list(inputs_v.iter_errors(c["inputs"])):
            fail(f"fixtures/video/media-rows.json {c['description']!r}: not RunInputs: {errs[0].message}")
        nodes = copy.deepcopy(by_id[c["workflow"]]["nodes"])
        for k, v in c["inputs"].items():
            nodes[k.split(".")[0]].setdefault("params", {})[k.split(".")[1]] = v
        if (got := media_rows(nodes, pal, takes)) != c["expect"]:
            fail(f"fixtures/video/media-rows.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/media-rows.json")


def check_video(registry: Registry, palette: dict) -> None:
    check_media(registry)
    g = HERE / "fixtures" / "video"
    fx = json.loads((g / "auto-frames.json").read_text())
    for c in fx["cases"]:
        got = auto_frames(c["seconds"], c["fps"], c["min_s"], c["max_s"], fx["param"] | {"max": c["max"]})
        if got != c["frames"]:
            fail(f"fixtures/video/auto-frames.json {c}: got {got}")
    print("ok   fixtures/video/auto-frames.json")
    doc_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/WorkflowDoc"}, registry=registry)
    for c in json.loads((g / "keyframes.json").read_text())["cases"]:
        if errs := list(doc_v.iter_errors(c["workflow"])):
            fail(f"fixtures/video/keyframes.json {c['description']!r}: not a WorkflowDoc: {errs[0].message}")
        elif (got := position_errors(c["workflow"]["nodes"], palette)) != c["errors"]:
            fail(f"fixtures/video/keyframes.json {c['description']!r}: got {got}, want {c['errors']}")
    print("ok   fixtures/video/keyframes.json")


# ---------------------------------------------------------------- voices (README, "Voices")
VOICE_SOURCE = {"preset": "preset", "cloned": "clip", "designed": "design"}  # a voice's group → what `accepts` names
VOICE_REFUSED = {"preset": "this workflow cannot use a preset voice", "clip": "this workflow cannot clone a voice",
                 "design": "this workflow cannot use a designed voice"}
SPEECH_CPS = 15  # characters per second of planned speech (the peak_gb variable `speech_seconds`)


def voice_params(nodes: dict, palette: dict) -> list[tuple[str, dict]]:
    """Every voice param of a known, not bypassed node, node id order then schema order (it takes no wire), its
    accepts narrowed to the node's `only` (1.30); from 1.32 a rows param's one voice field (a dialogue's lines) counts
    as one, named `<param>.<field>` and narrowed by `only` on the rows param."""
    def one(nid: str, p: dict) -> dict | None:
        f = voice_field(p) if p["type"] == "rows" else None
        q = p if p["type"] == "voice" else p["item"][f] | {"name": f"{p['name']}.{f}"} if f else None
        if q and p["name"] in nodes[nid].get("only", {}):
            q = q | {"accepts": [a for a in q["accepts"] if a in nodes[nid]["only"][p["name"]]]}
        return q
    return [(nid, q) for nid in sorted(nodes) if (s := palette.get(nodes[nid]["kind"]))
            and not nodes[nid].get("bypass") for p in s["params"] if (q := one(nid, p))]


def voice_sources(item: dict) -> list[str]:
    """What a saved voice offers a voice param, its own source first; from 1.31 a designed voice with a clip also
    offers `clip` (README, Voices: cloned voices)."""
    own = VOICE_SOURCE[item["group"]]
    return [own] + (["clip"] if own != "clip" and "clip" in item["voice"] else [])


def voice_source(param: dict, item: dict) -> str | None:
    """The defs voice `source` studio sends for this param: the first of voice_sources it accepts (None: it refuses)."""
    return next((x for x in voice_sources(item) if x in param["accepts"]), None)


def voice_reason(kind: str, param: dict, item: dict) -> str | None:
    """Why one voice param cannot take this saved voice (item: a LibraryItem of kind voice), None when it can. Studio's
    run 422 is `<node>.<param>: <reason>`."""
    src = VOICE_SOURCE[item["group"]]
    if voice_source(param, item) is None:
        return VOICE_REFUSED[src]
    if src == "preset" and item["voice"]["family"] != family(kind):
        return f"voice {item['name']} is for {item['voice']['family']}"
    return None


def voice_fit(nodes: dict, palette: dict, item: dict) -> str | None:
    """GET /library?workflow= for a voice: None when some voice param takes it, else the first param's reason."""
    params = voice_params(nodes, palette)
    if not params:
        return "this workflow takes no voice"
    reasons = [voice_reason(nodes[nid]["kind"], p, item) for nid, p in params]
    return None if None in reasons else reasons[0]


VOICE_CLIP = {"accepts": ["audio"], "trim": True, "min_s": 3, "max_s": 30}  # a voice's clip, checked as this media param
CONSENT = 'voice.consent: tick "I have the right to use this voice"'


def save_voice(body: dict, takes: dict, voices: dict) -> dict:
    """POST /library with kind voice, the 1.31 part (README, Voices: cloned voices), after the 1.30 checks up to
    `loras: a voice has no LoRAs` and before language, style and name: {status, detail} or {voice, from_take_id}.
    takes: the caller's live takes by id ({id, kind, duration_s, has_audio?, voices?: made_with.voices}); voices: the
    caller's voices by id (LibraryItems). In / out are taken as already snapped."""
    g, v, ft = body["group"], dict(body.get("voice") or {}), body.get("from_take")
    err = lambda d, status=422: {"status": status, "detail": d}
    if g == "cloned" and "design" in v:
        return err("voice.design: a cloned voice has no design")
    if "clip" in v and ft:
        return err("voice.clip: give the clip or from_take, not both")
    path, ref = ("voice.clip", v.pop("clip")) if "clip" in v else \
        ("from_take", {k: ft[k] for k in ("take_id", "in", "out") if k in ft}) if ft else (None, None)
    if g == "cloned" and ref is None:
        return err("voice: a cloned voice needs voice.clip")
    if ref is not None and (take := takes.get(ref["take_id"])) is None:
        return err(f"{path}: take {ref['take_id']} not found", 404)
    if g == "designed" and "design" not in v:
        if not ft:
            return err("voice: a designed voice needs voice.design")
        made = next((x for x in take.get("voices", []) if x["source"] == "designed"), None)
        if made is None:
            return err(f"from_take: take {take['id']} was not made with a designed voice")
        if (src := voices.get(made["id"])) is None:
            return err(f"from_take: voice {made['name']} was deleted")
        v |= {k: src["voice"][k] for k in ("design", "language", "style") if k in src["voice"] and k not in v}
    out = {k: v[k] for k in ("design", "language", "style") if v.get(k)}
    if ref is not None:
        e, ref = media_value(VOICE_CLIP, ref, take)
        if e:
            return err(f"{path}: {e}")
        if v.get("consent") is not True:
            return err(CONSENT)
        out |= {"clip": ref, "consent": True}
    return {"voice": {"source": g} | out, "from_take_id": ref["take_id"] if ref else None}


def sweep(takes: list[dict], voices: list[dict]) -> list[str]:
    """The deleted takes whose objects the sweep may delete now: none that one of the same owner's voices keeps as its
    clip (README, Voices: cloned voices)."""
    kept = {(x["owner_id"], x["voice"]["clip"]["take_id"]) for x in voices if "clip" in x["voice"]}
    return [t["id"] for t in takes if t["deleted"] and (t["owner_id"], t["id"]) not in kept]


def speech_seconds(nodes: dict, palette: dict, stage: str, max_seconds: float | None = None) -> float:
    """The peak_gb variable `speech_seconds` of node `stage` (README, Voices: memory): code points of the text reaching its
    first Text input (a prompt node's merged text; through any other node, the nearest upstream prompt's), else of
    its nearest upstream prompt, ÷ 15, capped at max_seconds; 0 without text. Nearest = fewest wires, then node id."""
    val = lambda nid: nodes[nid].get("params", {}).get("text", next(
        p["default"] for p in palette[nodes[nid]["kind"]]["params"] if p["name"] == "text"))
    s = palette[nodes[stage]["kind"]]
    if script_target(s):  # API 1.32, a dialogue: its lines' text and the pauses between them (README, Dialogue)
        rows = nodes[stage].get("params", {}).get("lines", [])
        sec = sum(len(r.get("text", "")) for r in rows) / SPEECH_CPS + sum(r.get("pause_s", 0) for r in rows[:-1])
        return min(sec, max_seconds) if max_seconds is not None else sec
    port = next((i["name"] for i in s["inputs"] if i["type"] == "Text" and i["name"] in nodes[stage].get("in", {})), None)
    level = [nodes[stage]["in"][port].split(".")[0]] if port else [stage]
    seen = set(level)
    while level:
        if hits := sorted(n for n in level if nodes[n]["kind"] == "prompt" and n != stage):
            chars = len(val(hits[0]))
            break
        level = sorted({ref.split(".")[0] for n in level for ref in nodes[n].get("in", {}).values()} - seen)
        seen |= set(level)
    else:
        chars = 0
    sec = chars / SPEECH_CPS
    return min(sec, max_seconds) if max_seconds is not None else sec


SCRIPT_LINE = re.compile(r"^@([A-Za-z][A-Za-z0-9_-]*)\s*(?:\(([^()]*)\))?\s*:(.*)$")
SCRIPT_FORM = "write @name: text or @name (style): text"


def script_lines(script: str) -> list[dict]:
    """A script's lines (README, Dialogue: scripts): each line of the text (split on \\n, a \\r before it dropped),
    trimmed, from 1: blank = a pause; else `@handle: text` or `@handle (style): text` with the handle lower-cased and
    style and text trimmed, or {line, error: SCRIPT_FORM}."""
    out = []
    for k, raw in enumerate(script.replace("\r\n", "\n").split("\n"), 1):
        t = raw.strip()
        m = SCRIPT_LINE.match(t) if t else None
        if not t:
            out.append({"line": k, "blank": True})
        elif not m or not HANDLE.match(m.group(1).lower()):
            out.append({"line": k, "error": SCRIPT_FORM})
        else:
            out.append({"line": k, "handle": m.group(1).lower(), "style": (m.group(2) or "").strip(),
                        "text": m.group(3).strip()})
    return out


def expand_script(nodes: dict, palette: dict, library: list[dict], voices: dict) -> dict:
    """Studio's script expansion at create time (README, Dialogue: scripts), on merged, type-checked values: every
    known, not bypassed node with a `script` param whose value is not blank writes its `lines` from it (the script is
    then sent empty), each line's voice = its element's `voice_id`, checked with voice_reason against the lines'
    voice field (narrowed by `only`). library: the caller's elements as studio reads them ({id, handle, name,
    version, voice_id}, a deleted voice already null); voices: voice id → LibraryItem (the caller's own and the live
    starter voices). Answers {errors} or {inputs, typed, elements, voices, parents} (parents: the clip takes the lines
    send, first use order)."""
    by_handle, errs = {e["handle"]: e for e in library}, []
    res = {"inputs": {}, "typed": {}, "elements": [], "voices": [], "parents": []}
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        sp = next((p for p in s["params"] if p.get("script")), None) if s and not n.get("bypass") else None
        if not sp:
            continue
        lp, vals = script_target(s), {p["name"]: p["default"] for p in s["params"]} | n.get("params", {})
        script, given = vals[sp["name"]], vals["lines"]
        if not script.strip():
            if not given:
                errs.append(f"{nid}.{sp['name']}: the script is empty")
            continue  # typed lines are resolved as voice values, not here
        if given:
            errs.append(f"{nid}.lines: give the script or the lines, not both")
            continue
        field = next(q for x, q in voice_params({nid: n}, palette) if q["name"] == "lines.voice")
        it, pause = lp["item"], vals.get("pause", lp["item"]["pause_s"]["default"])
        parsed, rows, mine = script_lines(script), [], []
        for ln in parsed:
            if ln.get("blank"):
                if rows:
                    rows[-1][1] += 1
                continue
            at = f"{nid}.{sp['name']} line {ln['line']}: "
            el = by_handle.get(ln.get("handle"))
            item = voices.get(el["voice_id"]) if el and el.get("voice_id") else None
            why = item and voice_reason(n["kind"], field, item)
            e = ln.get("error") or ("nothing to say" if not ln["text"] else None) \
                or (f"at most {it['text']['max']} characters ({len(ln['text'])})" if len(ln["text"]) > it["text"]["max"] else None) \
                or (f"the style is at most {it['style']['max']} characters ({len(ln['style'])})"
                    if len(ln["style"]) > it["style"]["max"] else None) \
                or (f"unknown element @{ln['handle']}" if not el else None) \
                or (f"@{ln['handle']} has no voice" if not item else None) \
                or (f"voice {item['name']} does not fit this workflow ({why})" if why else None)
            if e:
                errs.append(at + e)
                continue
            rows.append([{"voice": {"voice_id": item["id"]}, "text": ln["text"], "style": ln["style"]}, 0])
            mine.append((el, item))
        if errs:
            continue
        if not rows:
            errs.append(f"{nid}.{sp['name']}: the script is empty")
            continue
        if len(rows) > lp["max"]:
            errs.append(f"{nid}.lines: at most {lp['max']} lines")
            continue
        last = len(rows) - 1
        lines = [r | {"pause_s": 0 if i == last else min(it["pause_s"]["max"], rnd(pause * (1 + b) * 1000) / 1000)}
                 for i, (r, b) in enumerate(rows)]
        res["inputs"] |= {f"{nid}.{sp['name']}": "", f"{nid}.lines": lines}
        res["typed"] |= {f"{nid}.{sp['name']}": script, f"{nid}.lines": given}
        for el, item in mine:
            if el["id"] not in [x["id"] for x in res["elements"]]:
                res["elements"].append({k: el[k] for k in ("id", "handle", "name", "version")})
            if item["id"] not in [x["id"] for x in res["voices"]]:
                res["voices"].append({"id": item["id"], "name": item["name"], "version": item["version"],
                                      "source": item["group"]})
            clip = item["voice"].get("clip", {}).get("take_id") if voice_source(field, item) == "clip" else None
            if clip and clip not in res["parents"]:
                res["parents"].append(clip)
    return {"errors": errs} if errs else res


def echo_dialogue(lines: list[dict], rate: int = 24000) -> dict:
    """The fake echo/dialogue's take (README, Dialogue: echo): each line one sine tone per character (rate / 15
    samples each), then rnd(pause_s × rate) samples of silence before the next line; cues from 1 in seconds."""
    cues, at = [], 0
    for i, r in enumerate(lines, 1):
        text = r["text"].strip()  # the text spoken: a typed line's spaces are not
        end = at + len(text) * rate // SPEECH_CPS
        cues.append({"line": i, "start_s": at / rate, "end_s": end / rate, "voice_id": r["voice"]["voice_id"],
                     "text": text})
        at = end + (rnd(r["pause_s"] * rate) if i < len(lines) else 0)
    return {"duration_s": at / rate, "cues": cues}


def script_check(script: str, cast: list[str], max_lines: int = 40, text_max: int = 600,
                 style_max: int = 200) -> str | None:
    """A write-script/v1 answer's check in pipelines (README, Dialogue: agents): None, or the failed check's text
    (the write is re-asked once): the first bad line in expand_script's order (malformed, nothing to say, text over
    text_max, style over style_max, a handle not in the cast), no line, too many lines."""
    parsed = [x for x in script_lines(script) if not x.get("blank")]
    for x in parsed:
        e = x.get("error") or ("nothing to say" if not x["text"] else None) \
            or (f"at most {text_max} characters ({len(x['text'])})" if len(x["text"]) > text_max else None) \
            or (f"the style is at most {style_max} characters ({len(x['style'])})" if len(x["style"]) > style_max
                else None) \
            or (f"@{x['handle']} is not in the cast" if x["handle"] not in cast else None)
        if e:
            return f"line {x['line']}: {e}"
    if not parsed:
        return "the script is empty"
    return f"more than {max_lines} lines" if len(parsed) > max_lines else None


def check_voices(registry: Registry, palette: dict) -> None:
    g = HERE / "fixtures" / "voices"
    doc_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/WorkflowDoc"}, registry=registry)
    item_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/LibraryItem"}, registry=registry,
                                  format_checker=FormatChecker())
    for name in ("media.json", "fit.json", "seconds.json"):
        fx = json.loads((g / name).read_text())
        for wid, wf in fx["workflows"].items():
            if errs := list(doc_v.iter_errors(wf)):
                fail(f"fixtures/voices/{name} {wid}: not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
        for case in fx["cases"]:
            nodes = fx["workflows"][case["workflow"]]["nodes"]
            if name == "media.json":
                got = workflow_media(nodes, palette)
            elif name == "fit.json":
                if errs := list(item_v.iter_errors(case["item"])):
                    fail(f"fixtures/voices/fit.json {case['description']!r}: item: {errs[0].message}")
                    continue
                item = case["item"]
                got = voice_fit(nodes, palette, item) if item["kind"] == "voice" else look_fit(nodes, palette, item, {})
                if "sends" in case:  # 1.31: the defs voice source of the first param that takes it
                    sent = next((voice_source(p, item) for nid, p in voice_params(nodes, palette)
                                 if voice_reason(nodes[nid]["kind"], p, item) is None), None)
                    if sent != case["sends"]:
                        fail(f"fixtures/voices/fit.json {case['description']!r}: sends {sent!r}, want {case['sends']!r}")
            else:
                got = speech_seconds(nodes, palette, case["stage"], case.get("max_seconds"))
            if got != case["expect"]:
                fail(f"fixtures/voices/{name} {case['description']!r}: got {got!r}, want {case['expect']!r}")
        print(f"ok   fixtures/voices/{name}")
    fx = json.loads((g / "clone.json").read_text())  # 1.31: cloned voices, Save as voice, the sweep
    body_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/LibraryItemCreate"}, registry=registry)
    for case in fx["cases"]:
        if errs := list(body_v.iter_errors(case["body"])):
            fail(f"fixtures/voices/clone.json {case['description']!r}: body: {errs[0].message}")
        elif (got := save_voice(case["body"], fx["takes"], fx["voices"])) != case["expect"]:
            fail(f"fixtures/voices/clone.json {case['description']!r}: got {got!r}, want {case['expect']!r}")
    for case in fx["sweep"]:
        if (got := sweep(case["takes"], case["voices"])) != case["expect"]:
            fail(f"fixtures/voices/clone.json sweep {case['description']!r}: got {got!r}, want {case['expect']!r}")
    print("ok   fixtures/voices/clone.json")
    fx = json.loads((g / "scripts.json").read_text())  # 1.32: scripts, dialogue lines, echo cues, write-script checks
    inputs_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/RunInputs"}, registry=registry)
    for vid, item in fx["voices"].items():
        if errs := list(item_v.iter_errors(item)):
            fail(f"fixtures/voices/scripts.json voice {vid}: {errs[0].json_path}: {errs[0].message}")
    for wid, wf in fx["workflows"].items():
        if errs := list(doc_v.iter_errors(wf)):
            fail(f"fixtures/voices/scripts.json {wid}: not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
    for case in fx["cases"]:
        where = f"fixtures/voices/scripts.json {case['description']!r}"
        nodes = copy.deepcopy(fx["workflows"][case["workflow"]]["nodes"])
        for k, v in case.get("params", {}).items():
            nodes[k.split(".")[0]].setdefault("params", {})[k.split(".")[1]] = v
        got = expand_script(nodes, palette, fx["library"], fx["voices"])
        if got != case["expect"]:
            fail(f"{where}: got {json.dumps(got)}, want {json.dumps(case['expect'])}")
            continue
        if "inputs" in got and (errs := list(inputs_v.iter_errors(got["inputs"]))):
            fail(f"{where}: inputs are not RunInputs: {errs[0].message}")
        if "take" in case and (t := echo_dialogue(next(v for k, v in got["inputs"].items() if k.endswith(".lines")))) != case["take"]:
            fail(f"{where}: echo take {json.dumps(t)}, want {json.dumps(case['take'])}")
    for case in fx["checks"]:
        if (got := script_check(case["script"], case["cast"])) != case["expect"]:
            fail(f"fixtures/voices/scripts.json check {case['description']!r}: got {got!r}, want {case['expect']!r}")
    print("ok   fixtures/voices/scripts.json")
    # starter voices: a family's voice ids are not its look ids (they share the /library/<family>/<id> URL)
    reg = HERE / "fixtures" / "registry"
    looks = {x["id"] for x in json.loads((reg / "looks.valid.json").read_text())["looks"]}
    for f in sorted(reg.glob("*voices.valid.json")):
        voices = json.loads(f.read_text())["voices"]
        if clash := sorted({v["id"] for v in voices} & looks) or [v["id"] for v in voices
                                                                   if v["id"].split("/")[0] != v["family"]]:
            fail(f"fixtures/registry/{f.name}: ids clash with looks or name another family: {clash}")
        else:
            print(f"ok   fixtures/registry/{f.name} ids")


# ---------------------------------------------------------------- timeline rules (README, "Timeline rules")
# The frame-rate table: fps as JSON writes it → the exact rate num/den.
RATES = [(23.976, 24000, 1001), (24, 24, 1), (25, 25, 1), (29.97, 30000, 1001), (30, 30, 1), (48, 48, 1), (50, 50, 1),
         (59.94, 60000, 1001), (60, 60, 1)]
T_MAGNET_PX = 6  # an edge lands on a cut within this many screen pixels
T_IMAGE_HOLD_S = 3.0  # an image take added to V1 holds this long
T_MAX_AUDIO_TRACKS, T_MAX_CLIPS, T_MAX_BYTES, T_MAX_ALTERNATIVES, T_MAX_RERENDER = 8, 1000, 262144, 32, 22
# an export: at most 2 hours, an estimated 16 GiB written and 32 GiB of distinct takes read (cmd.media.job, runner)
T_MAX_EXPORT_S, T_MAX_EXPORT_BYTES, T_MAX_EXPORT_INPUTS = 7200, 16 << 30, 32 << 30


def rnd(x: float) -> int:
    """Round half away from zero, exactly: Go math.Round; JS Math.sign(x) * Math.round(Math.abs(x))."""
    return int(Decimal(x).quantize(Decimal(1), rounding=ROUND_HALF_UP))


class Grid:
    """The target's frame and sample grids: f(s) = rnd(s × num / den), s(f) = f × den / num (in that order)."""

    def __init__(self, target: dict) -> None:
        self.num, self.den = next((n, d) for fps, n, d in RATES if abs(fps - target["fps"]) < 1e-6)
        self.sr = target["sample_rate"]

    def f(self, s: float) -> int:
        return rnd(s * self.num / self.den)

    def s(self, f: int) -> float:
        return f * self.den / self.num

    def snap(self, s: float) -> float:
        return self.s(self.f(s))

    def smp(self, s: float) -> int:
        return rnd(s * self.sr)


def t_kind(c: dict, takes: dict) -> str:
    """slot (no take: an empty slot, or from 1.32 a New line's placeholder on an audio track), video, image or
    (1.32) audio; an offline clip by its fields (hold = image)."""
    tid = c.get("take_id")
    return "slot" if not tid else takes[tid]["kind"] if tid in takes else "image" if "hold" in c else "video"


def t_len(c: dict, kind: str, g: Grid, audio: bool = False) -> int:
    """A clip's length in frames on the sequence (an audio-track clip: its end's frame minus its start's)."""
    if audio:
        return g.f(c["at"] + (c["hold"] if "hold" in c else c["out"] - c["in"])) - g.f(c["at"])
    return g.f(c["out"]) - g.f(c["in"]) if kind == "video" else g.f(c["hold"])


def a_len(c: dict, g: Grid) -> int:
    """An audio-track clip's length in target samples: out − in, or (1.32) a line placeholder's hold."""
    return g.smp(c["hold"]) if "hold" in c else g.smp(c["out"]) - g.smp(c["in"])


def shot_ref(shot: dict) -> dict:
    return {k: shot[k] for k in ("run_id", "batch_id", "step") if k in shot}


def t_normalize(doc: dict, takes: dict, previous: dict | None = None) -> tuple[dict | None, list]:
    """A written document (`TimelineDoc`) → (the normalized document, []) or (None, problems): every time snapped,
    V1 laid end to end from 0, defaults filled, track names given. takes: the caller's takes the document names
    (id → facts); a take that is missing is offline when `previous` (the stored revision) named it, else
    `take_not_found`. A `shot` must be the previous revision's for that clip (only studio makes one)."""
    probs = []
    add = lambda code, track=None, clip=None: probs.append([code, track, clip])
    if len(json.dumps(doc, separators=(",", ":"), ensure_ascii=False).encode()) > T_MAX_BYTES:
        return None, [["too_large", None, None]]
    tracks = doc["tracks"]
    if (not tracks or tracks[0]["kind"] != "video" or any(t["kind"] != "audio" for t in tracks[1:])
            or len(tracks) > 1 + T_MAX_AUDIO_TRACKS):
        return None, [["track_layout", None, None]]
    if sum(len(t["clips"]) for t in tracks) > T_MAX_CLIPS:
        return None, [["too_many_clips", None, None]]
    g = Grid(doc["target"])
    known, shots = set(), {}
    for t in (previous or {}).get("tracks", []):
        for c in t["clips"]:
            known |= {c.get("take_id")} | set(c.get("alternatives", []))
            if "shot" in c:
                shots[c["id"]] = shot_ref(c["shot"])
    ids, out, ends = set(), [], []
    for ti, t in enumerate(tracks):
        name, video, clips = ("V1" if ti == 0 else f"A{ti + 1}"), ti == 0, []
        for c in t["clips"]:
            cid, tid = c["id"], c.get("take_id")
            tk, kind = takes.get(tid), t_kind(c, takes)
            if cid in ids:
                add("duplicate_clip", name, cid)
            ids.add(cid)
            if kind == "audio" and video:  # 1.32: an audio take goes on A2-A9 only (README, Timeline rules)
                add("wrong_track", name, cid)
                continue
            if any(x not in takes and x not in known for x in [tid] * bool(tid) + c.get("alternatives", [])):
                add("take_not_found", name, cid)
            if "shot" in c and shots.get(cid) != shot_ref(c["shot"]):
                add("shot_invalid", name, cid)
            n = {"id": cid, "take_id": tid}
            if kind in ("video", "audio"):
                if "hold" in c:
                    add("hold_on_video", name, cid)
                end = c.get("out", tk["duration_s"] if tk else None)
                if end is None:
                    add("out_required", name, cid)
                    continue
                if video:
                    i_, o_ = g.f(c.get("in", 0)), g.f(end)
                    if tk and not (0 <= i_ and o_ - i_ >= 1 and o_ <= g.f(tk["duration_s"])):
                        add("trim", name, cid)
                    n |= {"in": g.s(i_), "out": g.s(o_)}
                else:
                    i_, o_ = g.smp(c.get("in", 0)), g.smp(end)
                    if tk and not (0 <= i_ < o_ <= g.smp(tk["duration_s"])):
                        add("trim", name, cid)
                    if tk and kind == "video" and not tk["has_audio"]:
                        add("no_audio", name, cid)
                    if "at" not in c:
                        add("at_required", name, cid)
                        continue
                    n |= {"in": i_ / g.sr, "out": o_ / g.sr, "at": g.snap(c["at"])}
            elif not video and not (kind == "slot" and "shot" in c):  # 1.32: a New line placeholder is the exception
                add("wrong_track", name, cid)
                continue
            elif not video:
                if "in" in c or "out" in c:
                    add("image_trim", name, cid)
                if "hold" not in c or "at" not in c:
                    add("hold_required" if "hold" not in c else "at_required", name, cid)
                    continue
                if g.f(c["hold"]) < 1:
                    add("hold", name, cid)
                n |= {"at": g.snap(c["at"]), "hold": g.snap(c["hold"])}
            else:
                if "in" in c or "out" in c:
                    add("image_trim", name, cid)
                if "hold" not in c:
                    add("hold_required", name, cid)
                    continue
                if g.f(c["hold"]) < 1:
                    add("hold", name, cid)
                n["hold"] = g.snap(c["hold"])
            n |= {"gain_db": c.get("gain_db", 0), "mute": c.get("mute", False)}
            if "transition_in" in c:
                if video:
                    n["transition_in"] = c["transition_in"]
                else:
                    add("dissolve_track", name, cid)
            alts = list(dict.fromkeys(c.get("alternatives", []) + [tid] * bool(tid)))
            while len(alts) > T_MAX_ALTERNATIVES:  # the oldest that is not the current one goes
                alts.remove(next(a for a in alts if a != tid))
            n["alternatives"] = alts
            if "shot" in c:
                n["shot"] = shot_ref(c["shot"])
            if video and "number" in c:  # 1.29: studio gives it when it stores (README, Timeline rules: shot numbers)
                n["number"] = c["number"]
            if tid and not tk:
                n["offline"] = True
            clips.append((n, kind))
        if video:
            pos = 0
            for n, kind in clips:
                n["at"] = g.s(pos)
                pos += t_len(n, kind, g)
            din = [n.get("transition_in", {}).get("frames", 0) for n, _ in clips] + [0]
            for k, (n, kind) in enumerate(clips):
                d = din[k]
                if d and k == 0:
                    add("dissolve_first", name, n["id"])
                elif d and "slot" in (kind, clips[k - 1][1]):
                    add("dissolve_gap", name, n["id"])
                elif d:
                    p, pk = clips[k - 1]
                    ptk = takes.get(p["take_id"])
                    if (pk == "video" and ptk and g.f(p["out"]) + d // 2 > g.f(ptk["duration_s"])) or (
                            kind == "video" and g.f(n["in"]) - d // 2 < 0):
                        add("dissolve_handles", name, n["id"])
                half = (din[k] if k else 0) // 2 + din[k + 1] // 2
                if half and half > t_len(n, kind, g):
                    add("dissolve_length", name, n["id"])
        else:
            clips.sort(key=lambda x: x[0]["at"])
            prev_end = None
            for n, _ in clips:
                a, e = g.smp(n["at"]), g.smp(n["at"]) + a_len(n, g)
                if prev_end is not None and a < prev_end:
                    add("overlap", name, n["id"])
                prev_end = max(e, prev_end or 0)
                ends.append((name, n["id"], e))
        out.append({"name": name, "kind": t["kind"], "gain_db": t.get("gain_db", 0), "mute": t.get("mute", False),
                    "clips": [n for n, _ in clips]})
    v1 = out[0]["clips"]
    if v1:
        frames = g.f(v1[-1]["at"]) + t_len(v1[-1], t_kind(v1[-1], takes), g)
        for name, cid, e in ends:
            if e > g.smp(g.s(frames)):
                add("past_end", name, cid)
    else:
        frames = max([g.f(e / g.sr) for _, _, e in ends], default=0)
    if probs:
        return None, sorted(probs, key=lambda p: (p[0], p[1] or "", p[2] or ""))
    return {"target": doc["target"], "tracks": out, "duration_s": g.s(frames), "frames": frames}, []


def t_edit(doc: dict, op: dict, takes: dict) -> dict | str:
    """One edit on a normalized document → the new document as written (normalize it next), or an error code when
    the edit cannot apply. The web draws with it; studio runs `add`, `insert` and `fill` itself."""
    d, g = copy.deepcopy(doc), Grid(doc["target"])
    tracks = {t["name"]: t for t in d["tracks"]}
    v1 = d["tracks"][0]["clips"]

    def find(cid: str) -> tuple[dict, int]:
        return next((t, i) for t in d["tracks"] for i, c in enumerate(t["clips"]) if c["id"] == cid)

    def din(i: int) -> int:
        return v1[i].get("transition_in", {}).get("frames", 0) if 0 < i < len(v1) else 0

    def cut_at(i: int) -> None:  # the cut before v1[i] changed: its dissolve goes
        if i < len(v1):
            v1[i].pop("transition_in", None)

    o = op["op"]
    if o == "add":
        t = tracks[op.get("track", "V1")]
        for tid, cid in zip(op["take_ids"], op["ids"]):
            tk = takes[tid]
            if t["name"] != "V1":  # at the first frame at or after the track's end (in samples): never an overlap
                end = max((g.smp(c["at"]) + g.smp(c["out"]) - g.smp(c["in"]) for c in t["clips"]), default=0)
                f = g.f(end / g.sr)
                f += g.smp(g.s(f)) < end
                t["clips"].append({"id": cid, "take_id": tid, "in": 0, "out": tk["duration_s"], "at": g.s(f)})
            elif tk["kind"] == "image":
                t["clips"].append({"id": cid, "take_id": tid, "hold": T_IMAGE_HOLD_S})
            else:
                t["clips"].append({"id": cid, "take_id": tid, "in": 0, "out": tk["duration_s"]})
        return d
    cuts = [0]
    for c in v1:
        cuts.append(cuts[-1] + t_len(c, t_kind(c, takes), g))
    if o == "place":  # 1.32, Make shot: an empty slot at exactly frames(at) (README, Timeline: Make shot)
        f, n = g.f(op["at"]), g.f(op["hold"])
        new = {"id": op["id"], "take_id": None, "hold": g.s(n)}
        if f >= cuts[-1]:  # past V1's end: a gap up to it first
            v1 += [{"id": op["gap_id"], "take_id": None, "hold": g.s(f - cuts[-1])}] * (f > cuts[-1]) + [new]
            return d
        k = max(i for i in range(len(v1)) if cuts[i] <= f)
        gap = v1[k]
        if gap.get("take_id") or "shot" in gap:  # inside a take's clip or a running shot: the insert edit
            o, op = "insert", {"at": op["at"], "hold": g.s(n), "id": op["id"]}
        else:  # inside an empty slot: split it around the new slot
            left, right = f - cuts[k], cuts[k + 1] - f - n
            parts = ([gap | {"hold": g.s(left)}] if left else []) + [new] + \
                ([{"id": op["gap_id"] if left else gap["id"], "take_id": None, "hold": g.s(right)}] if right > 0 else [])
            v1[k:k + 1] = parts
            cut_at(k + len(parts))
            for t in d["tracks"][1:] if right < 0 else []:  # longer than the gap: an insert of what overflows, so
                for c in t["clips"]:  # every track moves on together after the gap (V1 rippled; the audio too)
                    if g.f(c["at"]) >= cuts[k + 1]:
                        c["at"] = g.s(g.f(c["at"]) - right)
            return d
    if o == "insert":  # an empty slot at the V1 cut nearest the playhead (a tie: the later cut)
        f = g.f(op["at"])
        i = min(range(len(cuts)), key=lambda k: (abs(cuts[k] - f), -k))
        v1.insert(i, {"id": op["id"], "take_id": None, "hold": op["hold"]})
        cut_at(i + 1)
        return d
    if o in ("add_track", "remove_track", "track"):
        if o == "add_track":
            if len(d["tracks"]) > T_MAX_AUDIO_TRACKS:
                return "too_many_tracks"
            d["tracks"].append({"kind": "audio", "gain_db": 0, "mute": False, "clips": []})
        elif o == "remove_track":
            if op["track"] == "V1":
                return "video_track"
            d["tracks"].remove(tracks[op["track"]])
        else:
            tracks[op["track"]] |= {k: op[k] for k in ("gain_db", "mute") if k in op}
        for t in d["tracks"]:
            t.pop("name", None)  # names follow the position
        return d
    if o == "split":
        return t_split(d, tracks[op["track"]], op, takes, g)
    t, i = find(op["clip"])
    c, kind, on_v1 = t["clips"][i], t_kind(t["clips"][i], takes), t is d["tracks"][0]
    if o == "move":
        if on_v1:
            v1.pop(i)
            cut_at(i)
            v1.insert(op["index"], c)
            c.pop("transition_in", None)
            cut_at(op["index"] + 1)
        else:
            t["clips"].pop(i)
            tracks[op.get("track", t["name"])]["clips"].append(c | {"at": op["at"]})
    elif o == "trim":
        df = g.f(op["delta"])
        src = g.f(takes[c["take_id"]]["duration_s"]) if kind in ("video", "audio") and c.get("take_id") in takes else None
        if on_v1:
            d_in, d_out = din(i), din(i + 1)
            least = max(1, d_in // 2 + d_out // 2)
            if kind == "video":
                i_, o_ = g.f(c["in"]), g.f(c["out"])
                if op["edge"] == "in":
                    i_ = min(max(i_ + df, d_in // 2), o_ - least)
                else:
                    o_ = max(o_ + df, i_ + least)
                    o_ = o_ if src is None else min(o_, src - d_out // 2)
                c["in"], c["out"] = g.s(i_), g.s(o_)
            else:
                c["hold"] = g.s(max(g.f(c["hold"]) + (df if op["edge"] == "out" else -df), least))
        elif kind == "slot":  # 1.32: a line placeholder's hold, its start kept
            c["hold"] = g.s(max(g.f(c["hold"]) + (df if op["edge"] == "out" else -df), 1))
        else:
            i_, o_, frame = g.smp(c["in"]), g.smp(c["out"]), g.smp(g.s(1))
            if op["edge"] == "in":
                df = max(df, -g.f(c["at"]))
                ni = min(max(i_ + g.smp(g.s(df)), 0), o_ - frame)
                c["at"], c["in"] = g.snap(c["at"] + (ni - i_) / g.sr), ni / g.sr
            else:
                no = max(o_ + g.smp(g.s(df)), i_ + frame)  # 1.32: a video's sound or an audio take ends at the take's end
                c["out"] = (no if src is None else min(no, g.smp(takes[c["take_id"]]["duration_s"]))) / g.sr
    elif o in ("remove", "lift"):
        if o == "lift" and not on_v1:
            return "lift_track"
        if o == "lift":
            t["clips"][i] = {"id": c["id"], "take_id": None, "hold": g.s(t_len(c, kind, g))}
        else:
            t["clips"].pop(i)
        if on_v1:
            cut_at(i if o == "remove" else i + 1)
    elif o == "replace":
        if op["take_id"] not in c.get("alternatives", []):
            return "not_alternative"
        if op["take_id"] not in takes:
            return "take_not_found"
        tk, ln = takes[op["take_id"]], t_len(c, kind, g, audio=not on_v1)
        old_in = g.f(c["in"]) if kind in ("video", "audio") else 0
        for k in ("in", "out", "hold"):
            c.pop(k, None)
        if tk["kind"] == "image" and on_v1:
            c["hold"] = g.s(ln)
        else:
            src = g.f(tk["duration_s"])
            i_ = min(old_in, src - 1)
            c["in"], c["out"] = g.s(i_), g.s(min(i_ + ln, src))
        c["take_id"] = op["take_id"]
    elif o == "set":
        for k in ("gain_db", "mute", "hold", "transition_in"):
            if k in op:
                if op[k] is None:
                    c.pop(k, None)
                else:
                    c[k] = op[k]
    elif o == "fill":  # studio: a shot's take fills its slot (README, Timeline: New shot; take actions)
        tk, ln, cur = takes[op["take_id"]], t_len(c, kind, g), takes.get(c.get("take_id")) or {}
        sm, i0, o0 = tk.get("source_map"), c.get("in"), c.get("out")
        for k in ("in", "out", "hold", "shot"):
            c.pop(k, None)
        if not on_v1:  # 1.32, an audio track (New line, a line's re-render): the whole take at the slot's start
            c["in"], c["out"] = 0, tk["duration_s"]
        elif tk["kind"] == "image":
            c["hold"] = g.s(ln)
        elif sm:  # the generated part; replacing its source (or a take made from it): the clip's window, grown on a
            # side only where the generated part lies outside the source (extend), never by the 8-frame snap (retake)
            a, b, off = sm["new_in_s"], sm["new_out_s"], sm["offset_s"]
            mine = 0 if c.get("take_id") == sm["take_id"] else \
                cur.get("source_map", {}).get("offset_s") if cur.get("source_map", {}).get("take_id") == sm["take_id"] else None
            if mine is not None and o0 is not None:
                end = off + takes[sm["take_id"]]["duration_s"]
                a = a if g.f(a) < g.f(off) else i0 - mine + off
                b = b if g.f(b) > g.f(end) else o0 - mine + off
            c["in"], c["out"] = g.s(max(g.f(a), 0)), g.s(min(g.f(b), g.f(tk["duration_s"])))
        else:
            c["in"], c["out"] = 0, g.s(min(g.f(tk["duration_s"]), ln))
        c["take_id"] = op["take_id"]
        c["alternatives"] = c.get("alternatives", []) + [op["take_id"]]
    else:
        return "unknown_op"
    return d


def t_split(d: dict, t: dict, op: dict, takes: dict, g: Grid) -> dict | str:
    """Split at the playhead (`at`, sequence seconds) the clip of `track` under it; the right part gets `id`, keeps
    the take and alternatives, never the dissolve or the shot."""
    f, on_v1 = g.f(op["at"]), t is d["tracks"][0]
    hit = None
    for k, c in enumerate(t["clips"]):
        a, ln = g.f(c["at"]), t_len(c, t_kind(c, takes), g, audio=not on_v1)
        if a <= f < a + ln:
            hit = (k, c, f - a, ln)
    if hit is None:
        return "split_none"
    k, c, p, ln = hit
    if p == 0:
        return "split_edge"
    if "shot" in c:
        return "split_shot"
    c2 = {x: v for x, v in copy.deepcopy(c).items() if x not in ("transition_in", "shot")} | {"id": op["id"]}
    if not on_v1:
        cut = g.smp(g.s(f)) - g.smp(c["at"]) + g.smp(c["in"])  # the left part ends on the right part's start sample
        c["out"], c2["in"], c2["at"] = cut / g.sr, cut / g.sr, g.s(f)
    elif t_kind(c, takes) == "video":
        c["out"] = c2["in"] = g.s(g.f(c["in"]) + p)
    else:
        c["hold"], c2["hold"] = g.s(p), g.s(ln - p)
    t["clips"].insert(k + 1, c2)
    return d


def t_apply(doc: dict, op: dict, takes: dict) -> dict:
    """An edit, then normalize against the edited document as the previous revision. `fill` then drops a dissolve
    the new take cannot carry and cuts an audio-track clip a shorter V1 now ends inside (removed when it starts at
    or after the end), so a fill never fails."""
    raw = t_edit(doc, op, takes)
    if isinstance(raw, str):
        return {"error": raw}
    n, probs = t_normalize(raw, takes, doc)
    if op["op"] == "fill" and probs:
        g = Grid(raw["target"])
        bad = {p[2] for p in probs if p[0] in ("dissolve_handles", "dissolve_length")}
        past = {p[2] for p in probs if p[0] == "past_end"}
        for t in raw["tracks"][1:]:  # 1.32: a filled line running into the next clip of its track is cut at its start
            order = sorted(t["clips"], key=lambda c: g.smp(c["at"]))
            for prev, c in zip(order, order[1:]):
                if [p for p in probs if p[0] == "overlap" and p[2] == c["id"]] and "out" in prev:
                    prev["out"] = (g.smp(prev["in"]) + g.smp(c["at"]) - g.smp(prev["at"])) / g.sr
        end = g.smp(g.s(sum(t_len(c, t_kind(c, takes), g) for c in raw["tracks"][0]["clips"])))
        for c in raw["tracks"][0]["clips"]:
            if c["id"] in bad:
                c.pop("transition_in", None)
        for t in raw["tracks"][1:]:
            for c in [c for c in t["clips"] if c["id"] in past]:
                if (a := g.smp(c["at"])) >= end:
                    t["clips"].remove(c)
                elif "hold" in c:  # 1.32: a line placeholder (never longer than it is: it may be cut already)
                    c["hold"] = min(g.smp(c["hold"]), end - a) / g.sr
                else:  # never past the out it has (a line cut at the next clip above stays cut)
                    c["out"] = min(g.smp(c["out"]), g.smp(c["in"]) + end - a) / g.sr
        n, probs = t_normalize(raw, takes, doc)
    return {"document": n} if n else {"problems": probs}


def t_cuts(doc: dict, takes: dict, exclude: str | None = None) -> list[float]:
    """What an edge snaps to: 0 and every clip's start and end on every track, but the dragged clip's own."""
    g, cuts = Grid(doc["target"]), {0.0}
    for ti, t in enumerate(doc["tracks"]):
        for c in t["clips"]:
            if c["id"] != exclude:
                cuts |= {c["at"], g.s(g.f(c["at"]) + t_len(c, t_kind(c, takes), g)) if ti == 0
                         else c["at"] + (c["hold"] if "hold" in c else c["out"] - c["in"])}
    return sorted(cuts)


def snap_point(t: float, cuts: list[float], px_per_s: float, g: Grid) -> float:
    """A dragged edge: the nearest cut when within T_MAGNET_PX on screen (a tie: the earlier), else its frame."""
    best = min(cuts, key=lambda c: (abs(c - t), c), default=None)
    return best if best is not None and abs(best - t) * px_per_s <= T_MAGNET_PX else g.snap(t)


def drop_index(doc: dict, clip: str, mid: float, takes: dict) -> int:
    """Moving a V1 clip: its index among the others = how many others have their midpoint before the dragged
    clip's midpoint `mid` (sequence seconds)."""
    g = Grid(doc["target"])
    m2 = 2 * g.f(mid)
    return sum(1 for c in doc["tracks"][0]["clips"] if c["id"] != clip
               and 2 * g.f(c["at"]) + t_len(c, t_kind(c, takes), g) < m2)


def frame_at(doc: dict, f: int, takes: dict) -> dict:
    """What the preview shows and plays at sequence frame f: pictures bottom first ({clip, t (source seconds; null
    for an image), opacity, offline?}) and sounds ({clip, t, gain_db = track + clip, fade}); a dissolve of d frames
    centred on its cut runs over [cut − d/2, cut + d/2): the incoming picture's opacity and sound fade are
    (f − (cut − d/2)) / d, the outgoing sound's 1 minus that. Muted clips and tracks, empty slots, images and takes
    without sound play no sound; an offline clip shows as offline and plays nothing."""
    g, pics, sounds = Grid(doc["target"]), [], []
    v1t = doc["tracks"][0]
    v1 = v1t["clips"]
    dins = [c.get("transition_in", {}).get("frames", 0) for c in v1] + [0]
    for k, c in enumerate(v1):
        kind, a = t_kind(c, takes), g.f(c["at"])
        ln = t_len(c, kind, g)
        start, end = a - dins[k] // 2, a + ln + dins[k + 1] // 2
        if kind == "slot" or not start <= f < end:
            continue
        mix_in = (f - start) / dins[k] if dins[k] and f < a + dins[k] // 2 else 1
        mix_out = (f - (a + ln - dins[k + 1] // 2)) / dins[k + 1] if dins[k + 1] and f >= a + ln - dins[k + 1] // 2 else 0
        src = g.s(g.f(c["in"]) + f - a) if kind == "video" else None
        pic = {"clip": c["id"], "t": src, "opacity": mix_in}
        if c.get("offline"):
            pics.append(pic | {"offline": True})
            continue
        pics.append(pic)
        if kind == "video" and takes[c["take_id"]]["has_audio"] and not c["mute"] and not v1t["mute"]:
            sounds.append({"clip": c["id"], "t": src, "gain_db": v1t["gain_db"] + c["gain_db"],
                           "fade": mix_in if mix_in < 1 else 1 - mix_out})
    for t in doc["tracks"][1:]:
        for c in t["clips"]:
            a = g.f(c["at"])
            if c.get("take_id") and a <= f < a + t_len(c, "video", g, audio=True) and not c.get("offline") \
                    and not c["mute"] and not t["mute"]:
                sounds.append({"clip": c["id"], "t": c["in"] + g.s(f - a), "gain_db": t["gain_db"] + c["gain_db"],
                               "fade": 1})
    return {"pictures": pics, "sounds": sounds}


def ratio(s: str) -> tuple[int, int]:
    a, _, b = s.partition("/")
    return int(a), int(b or 1)


def export_plan(doc: dict, takes: dict) -> dict:
    """The export plan of a normalized document (README, Timeline: export): stream copy only when every V1 clip is a
    whole, probed H.264 take in MP4/MOV matching the target and the first clip; sound is always re-encoded."""
    g, tg = Grid(doc["target"]), doc["target"]
    blocked = [["offline", c["id"]] for t in doc["tracks"] for c in t["clips"] if c.get("offline")]
    if not doc["tracks"][0]["clips"]:  # sound alone has no picture to export
        blocked.append(["empty", None])
    reasons, first = [], None
    why = lambda code, cid: reasons.append([code, cid])
    for c in doc["tracks"][0]["clips"]:
        cid, tk = c["id"], takes.get(c.get("take_id"))
        if not c.get("take_id"):
            why("gap", cid)
            continue
        if not tk:
            continue
        if tk["kind"] == "image":
            why("image", cid)
            continue
        if "transition_in" in c:
            why("transition", cid)
        s = tk.get("stream")
        if not s:
            why("not_probed", cid)
            continue
        fr = ratio(s["frame_rate"])
        for code, bad in (("container", s["container"] not in ("mp4", "mov")), ("codec", s["codec"] != "h264"),
                          ("pixel_format", s["pix_fmt"] != "yuv420p"),
                          ("size", (s["width"], s["height"]) != (tg["width"], tg["height"])),
                          ("aspect", s["sar"] not in (None, "1:1")),
                          ("field_order", s["field_order"] not in (None, "progressive", "unknown")),
                          ("frame_rate", fr[0] * g.den != fr[1] * g.num),
                          ("trimmed", g.f(c["in"]) != 0 or g.f(c["out"]) != g.f(tk["duration_s"]))):
            if bad:
                why(code, cid)
        if first is None:
            first = s
            continue
        for code, key in (("profile", "profile"), ("level", "level"), ("time_base", "time_base"),
                          ("colour", "color"), ("parameter_sets", "extradata_sha256")):
            if s[key] != first[key]:
                why(code, cid)
    copy_ok = not reasons and not blocked
    bitrate = min(max(rnd(tg["width"] * tg["height"] * g.num / g.den * 0.15), 1_000_000), 40_000_000)
    dur = g.s(doc["frames"])
    audio = rnd(192000 * dur / 8)
    est = (sum(takes[c["take_id"]]["bytes"] for c in doc["tracks"][0]["clips"]) + audio if copy_ok
           else rnd((bitrate + 192000) * dur / 8))
    inputs = sum(takes[x]["bytes"] for x in {c.get("take_id") for t in doc["tracks"] for c in t["clips"]} if x in takes)
    if dur > T_MAX_EXPORT_S or est > T_MAX_EXPORT_BYTES or inputs > T_MAX_EXPORT_INPUTS:
        blocked.append(["too_large", None])
    sounds = []  # 1.32: every sound the mix reads, with the facts it is resampled from (to target, stereo)
    for ti, t in enumerate(doc["tracks"]):
        for c in t["clips"]:
            tk = takes.get(c.get("take_id"))
            if not tk or c.get("offline") or c["mute"] or t["mute"] or (ti == 0 and not (
                    tk["kind"] == "video" and tk["has_audio"])):
                continue
            sounds.append({"clip": c["id"], "track": t["name"], "sample_rate": tk.get("sample_rate"),
                           "channels": 1 if tk["kind"] == "audio" else None})
    return {"stream_copy": copy_ok, "reasons": reasons, "blocked": blocked,
            "video_bitrate": None if copy_ok else bitrate, "estimated_bytes": est, "sounds": sounds}


def shot_keyframes(nodes: dict, palette: dict) -> dict | None:
    """Which frames a New shot can bind (Workflow.keyframes): the workflow's one rows param, unwired on a known, not
    bypassed node, with one Image field and a `position: seconds` field. `last` when it takes 2 rows or more."""
    found = []
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            pos = [k for k, v in p.get("item", {}).items() if isinstance(v, dict) and v.get("position") == "seconds"]
            if image_field(p) and pos and p["name"] not in n.get("in", {}):
                found.append({"param": f"{nid}.{p['name']}", "image": image_field(p), "seconds": pos[0],
                              "last": p["max"] >= 2})
    return found[0] if len(found) == 1 else None


def rerender_steps(doc: dict, takes: dict, clips: list[str] | None, keyframes: dict, media: dict | None = None) -> dict:
    """Re-render's pipeline order and binds (README, Timeline: re-render): a step `clip_<id>` per selected V1 clip;
    a keyframe row whose image is a frame take of a take in another selected clip's alternatives binds that clip's
    step (`<param>[<row>]` ← `clip_<other>.first_frame|last_frame`) and runs after it; ties in timeline order.
    keyframes: workflow id → its `shot_keyframes` (absent: none). From 1.19 a media input (media: workflow id → its
    `media` param paths) holding a take of another selected clip binds `<param>` ← `clip_<other>.takes` (in / out
    kept) and runs after it: a retake or extend re-renders on its source clip's new take. From 1.32 `clips` may name
    audio-track clips (a line made by a voice workflow): a V1 clip whose media input holds such a clip's take (Make
    shot) binds `clip_<line>.takes` and runs after it."""
    v1 = [c for ti, t in enumerate(doc["tracks"]) for c in t["clips"]  # 1.32: named audio-track clips too
          if (c["id"] in clips if clips is not None else ti == 0 and c.get("take_id"))]
    if len(v1) > T_MAX_RERENDER:
        return {"error": "too_many", "clip": None}
    for c in v1:
        tk = takes.get(c.get("take_id"))
        code = "empty" if not c.get("take_id") else "offline" if not tk else "not_made" if not tk.get("made_with") else None
        if code:
            return {"error": code, "clip": c["id"]}
    owner = {a: c["id"] for c in v1 for a in c["alternatives"]}
    deps, binds = {c["id"]: set() for c in v1}, {c["id"]: {} for c in v1}
    for c in v1:
        mw = takes[c["take_id"]]["made_with"]
        kf = keyframes.get(mw["workflow_id"])
        for r, row in enumerate(mw["inputs"].get(kf["param"], []) if kf else [], 1):
            fo = takes.get((row.get(kf["image"]) or {}).get("take_id"), {}).get("frame_of")
            src = owner.get(fo["take_id"]) if fo else None
            if src and src != c["id"]:
                binds[c["id"]][f"{kf['param']}[{r}]"] = f"clip_{src}.{fo['at']}_frame"
                deps[c["id"]].add(src)
        for path in (media or {}).get(mw["workflow_id"], []):
            src = owner.get((mw["inputs"].get(path) or {}).get("take_id"))
            if src and src != c["id"]:
                binds[c["id"]][path] = f"clip_{src}.takes"
                deps[c["id"]].add(src)
    order, left = [], [c["id"] for c in v1]
    while left:
        ready = next((x for x in left if deps[x] <= set(order)), None)
        if ready is None:
            return {"error": "rerender_cycle", "clip": left[0]}
        order.append(ready)
        left.remove(ready)
    return {"steps": [{"step": f"clip_{x}", "clip": x, "binds": binds[x]} for x in order]}


def make_shot(doc: dict, clip: str, takes: dict, workflows: list[dict], palette: dict, rng: tuple | None = None) -> dict:
    """An audio-track clip's actions (1.32, Make shot; README, Timeline: Make shot): the offers of its take (every
    family) into one `media` param accepting audio of a workflow whose media is video, the range = the clip's in..out in the take's
    own time (an audio take snapped to whole ms, a video's sound to its frames; capped at the param's max_s by the
    offer), each with `slot {at: the clip's at + (the range's start − its in), length: out − in, exact: true}`.
    rng: a selection inside the clip in the take's own seconds (else the clip's in..out). A V1 clip has none here."""
    t, c = next((t, c) for t in doc["tracks"] for c in t["clips"] if c["id"] == clip)
    tk = takes.get(c.get("take_id"))
    if t is doc["tracks"][0] or not tk or c.get("offline"):
        return {"actions": []}
    snap = (lambda x: rnd(x * 1000) / 1000) if tk["kind"] == "audio" else (lambda x: rnd(x * tk["fps"]) / tk["fps"])
    acts, by_id = [], {w["id"]: w for w in workflows}
    a, b = rng or (c["in"], c["out"])
    at = c["at"] + (a - c["in"])  # a selection starts the slot where its sound sits on the track
    for o in take_uses(tk, [w for w in workflows if w.get("media") == "video"], palette, (snap(a), snap(b))):
        v, (nid, name) = o["inputs"][o["param"]], o["param"].split(".")
        spec = next(q for q in palette[by_id[o["workflow_id"]]["nodes"][nid]["kind"]]["params"] if q["name"] == name)
        if isinstance(v, dict) and "audio" in spec.get("accepts", []):  # the clip's sound drives the shot
            acts.append(o | {"slot": {"at": at, "length": v["out"] - v["in"], "exact": True}})
    return {"actions": acts}


def check_timelines(registry: Registry, palette: dict) -> None:
    g_ = HERE / "fixtures" / "timelines"
    ref = lambda n: Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/{n}"}, registry=registry,
                                         format_checker=FormatChecker())
    doc_v, out_v = ref("TimelineDoc"), ref("TimelineNormalized")

    def shape(name: str, v, x) -> bool:
        if errs := list(v.iter_errors(x)):
            fail(f"{name}: {errs[0].json_path}: {errs[0].message}")
            return False
        return True

    for c in json.loads((g_ / "rates.json").read_text())["cases"]:
        g = Grid({"fps": c["fps"], "sample_rate": 48000})
        got = {"frames": g.f(c["seconds"]), "snapped": g.snap(c["seconds"])}
        if got != {k: c[k] for k in got}:
            fail(f"fixtures/timelines/rates.json {c}: got {got}")
    print("ok   fixtures/timelines/rates.json")
    for p in sorted(g_.glob("*.json")):
        name, fx = f"fixtures/timelines/{p.name}", json.loads(p.read_text())
        if p.name == "rates.json":
            continue
        for c in fx["cases"]:
            where, takes = f"{name} {c['description']!r}", fx.get("takes", {}) | c.get("takes", {})
            if p.name.startswith("normalize."):
                if not shape(where, doc_v, c["document"]):
                    continue
                n, probs = t_normalize(c["document"], takes, c.get("previous"))
                got = {"document": n} if n else {"problems": probs}
                if n and not shape(where + " (result)", out_v, n):
                    continue
            elif p.name.startswith("edits."):
                if not shape(where, out_v, c["document"]):
                    continue
                got = t_apply(c["document"], c["op"], takes)
            elif p.name.startswith("snap."):
                g = Grid(c["document"]["target"])
                got = {"to": snap_point(c["t"], t_cuts(c["document"], takes, c.get("exclude")), c["px_per_s"], g)} \
                    if "t" in c else {"index": drop_index(c["document"], c["clip"], c["mid"], takes)}
            elif p.name.startswith("frame-at."):
                got = frame_at(c["document"], c["frame"], takes)
            elif p.name.startswith("plan."):
                got = export_plan(c["document"], takes)
            elif p.name.startswith("shot-keyframes."):
                got = {"keyframes": shot_keyframes(c["workflow"]["nodes"], palette)}
            elif p.name.startswith("rerender."):
                got = rerender_steps(c["document"], takes, c.get("clips"), fx["keyframes"], fx.get("media"))
            elif p.name.startswith("make-shot."):  # 1.32
                reg = HERE / "fixtures" / "registry"
                wfs = [w for pre in ("", "ltx.") for w in json.loads((reg / f"{pre}workflows.valid.json").read_text())["workflows"]]
                got = make_shot(c["document"], c["clip"], {k: v | {"id": k} for k, v in takes.items()}, wfs, palette,
                                tuple(c["range"]) if "range" in c else None)
                if errs := list(ref("ClipActions").iter_errors(got)):
                    fail(f"{where}: not ClipActions: {errs[0].message}")
            else:
                fail(f"{name}: unknown timeline fixture")
                break
            if got != c["expect"]:
                fail(f"{where}: got {json.dumps(got)}, want {json.dumps(c['expect'])}")
        print(f"ok   {name}")


# ---------------------------------------------------------------- the dispatcher's hand-over (README, "The hand-over")
SWAP_SKIPS, SWAP_WAIT_S = 4, 180  # the fairness bound: studio_service dispatcher.go SwapSkips, SwapWait
WINDOW = 16  # rows of one batch in flight (the pipelines engine)


def cgb(x: float) -> int:
    """Memory arithmetic is in integer hundredths of a GB."""
    return round(x * 100)


def entry_key(e: dict) -> tuple:
    """A resident entry's (or an unload component's) identity; no loras = the plain component."""
    loras = tuple((x["lora_id"], x["strength"]) for x in e.get("loras") or [])
    return e["component"], e["model"], e["variant"], e["precision"], loras


def admit(host: str, entries: list[tuple], budget: int | None, env: str, peak: int | None, needs: set,
          referenced: set, running: str | None = None, ram_budget: int | None = None, ram_peak: int | None = None) -> dict:
    """The admission decision for releasing a run to `env` of `host`. entries: (env, key, gb, pinned, fresh, used)
    for every resident entry of the host's present keys (gb in hundredths, used sortable, None = oldest); budget: the
    host's (None: no key reports memory); peak: the run's planned peak (None: no plan = the whole budget); needs: the
    keys the run uses; referenced: (env, key) of the host's other queued runs; running: an env whose key still names
    a run (that run may use up to its budget_gb, more than its key declares: the run waits for that key's next write).
    ram_budget / ram_peak (API 1.18, cuda hosts): host_ram's smallest budget_gb and the plan's ram_peak_gb; entries held
    in host RAM (`device: cpu`) are not entries here."""
    if budget is None:
        return {"decision": "release", "unload": {}, "budget_gb": None, "error": None}
    p = budget if peak is None else peak
    counted = [x for x in entries if not (x[0] == env and x[1] in needs)]
    if peak is None:  # P = B takes what it can get: other envs' measured excess (never freed) lowers it, never holds it
        p -= sum(x[2] for x in counted if x[0] != env and x[1][0] == "(measured)")
    c = sum(x[2] for x in counted)
    own = sum(x[2] for x in counted if x[0] == env and x[1][0] == "(measured)")  # the run's own runner's excess
    if p > budget:  # a pin never fails a run: pinned entries are victims too, the last ones (below)
        error = f"needs {p / 100:.1f} GB at its peak; host {host} has {budget / 100:.1f} GB"
        return {"decision": "fail", "unload": {}, "budget_gb": None, "error": error}
    if ram_budget is not None and ram_peak is not None and ram_peak > ram_budget:
        error = f"needs {ram_peak / 100:.1f} GB of host RAM at its peak; host {host} has {ram_budget / 100:.1f} GB"
        return {"decision": "fail", "unload": {}, "budget_gb": None, "error": error}
    if running:
        return {"decision": "wait", "unload": {}, "budget_gb": None, "error": None}
    others = sum(x[2] for x in entries if x[0] != env)
    released = {"decision": "release", "unload": {}, "budget_gb": (budget - others) / 100, "error": None}
    if c + p <= budget:
        return released
    tier = lambda x: 0 if x[1][4] and (x[0], x[1]) not in referenced else 1  # an unreferenced baked set first
    victims = sorted((x for x in counted if x[4]),  # pinned ones last: taken only when nothing else makes it fit
                     key=lambda x: (x[3], tier(x), x[5] is not None, x[5] or 0, -x[2], x[0], x[1][:4]))
    unload: dict[str, list] = {}
    for x in victims:
        if c + p <= budget:
            break
        unload.setdefault(x[0], []).append(x[1])
        c -= x[2]
    if not unload and c - own + p <= budget:  # only its own excess is in the way: its runner drops its caches itself
        return released
    return {"decision": "unload" if unload else "wait", "unload": unload, "budget_gb": None, "error": None}


def as_component(key: tuple) -> dict:
    out = dict(zip(("component", "model", "variant", "precision"), key[:4]))
    if key[4]:
        out["loras"] = [{"lora_id": i, "strength": s} for i, s in key[4]]
    return out


def host_view(keys: dict) -> tuple[list, int | None, int | None, str | None]:
    """One host's present keys (fixture form: env -> {state fresh|offline|gone, budget_gb?, used_gb?, ram_budget_gb?,
    run_id?, resident}) -> (admission entries, budget, host-RAM budget, the env of an in-run key)."""
    from datetime import datetime
    entries, budgets, rams = [], [], []
    for env, k in keys.items():
        if k["state"] == "gone":
            continue
        if "budget_gb" in k:
            budgets.append(cgb(k["budget_gb"]))
        if "ram_budget_gb" in k:
            rams.append(cgb(k["ram_budget_gb"]))
        fresh = k["state"] == "fresh" and not k.get("run_id")  # a key still naming a run waits for its next write
        on_device = [e for e in k.get("resident", []) if e.get("device") != "cpu"]  # host RAM is not the budget
        for e in on_device:
            used = datetime.fromisoformat(e["last_used"].replace("Z", "+00:00")).timestamp() if "last_used" in e else None
            entries.append((env, entry_key(e), cgb(e["gb"]), e["pinned"], fresh, used))
        extra = cgb(k["used_gb"]) - sum(cgb(e["gb"]) for e in on_device) if "used_gb" in k else 0
        if extra > 0:  # measured beyond the entries: counted (the run's own env too), never a victim
            entries.append((env, ("(measured)", "", "", "", ()), extra, False, False, None))
    running = next((env for env, k in sorted(keys.items()) if k["state"] != "gone" and k.get("run_id")), None)
    return entries, min(budgets) if budgets else None, min(rams) if rams else None, running


def admit_fixture(fx: dict) -> dict:
    run = fx["run"]
    entries, budget, ram_budget, running = host_view(fx["keys"])
    peak = None if run["peak_gb"] is None else cgb(run["peak_gb"])
    ram_peak = cgb(run["ram_peak_gb"]) if run.get("ram_peak_gb") is not None else None
    referenced = {(q["env"], entry_key(n)) for q in fx.get("queued", []) for n in q["needs"]}
    got = admit(fx["host"], entries, budget, run["env"], peak, {entry_key(n) for n in run["needs"]}, referenced,
                running, ram_budget, ram_peak)
    got["unload"] = {env: [as_component(k) for k in keys] for env, keys in got["unload"].items()}
    return got


def place(fx: dict) -> dict:
    """Several hosts (README, Several hosts): the run's eligible hosts and the one it is lined up on, or the error it
    fails with. hosts: name -> {keys (as admit fixtures), workflows: {env: [ids]}, variants: {env: {"model/variant":
    [precisions]}} (present ones only), addons?: {env: ["model/component"]} (1.36: present add-ons only), loras: {env:
    [shared LoRA names]}, free, idle_since (null = never ran), reserved_gb? (enabled local servers)};
    run: {env, workflow, model?, variant?, precision?, addons? ([{node, kind, component, title, bypass?}]: 1.36), loras
    (shared, graph order), peak_gb, ram_peak_gb?, needs, lora_component?, lost_host?, host? (1.35: the run is set to
    that host only)}."""
    run, hosts = fx["run"], fx["hosts"]
    env, pin = run["env"], run.get("host")
    if pin and run.get("lost_host"):
        gone = lambda m: f"host {pin} went offline; a run set to {pin} never moves to another host"
    else:
        gone = lambda m: (f"runner on host {run['lost_host']} stopped responding; no other live host for env {env}"
                          if run.get("lost_host") else m)
    cand = sorted(h for h, x in hosts.items() if x["keys"].get(env, {}).get("state") == "fresh" and pin in (None, h))
    cand = [h for h in cand if run["workflow"] in hosts[h]["workflows"].get(env, [])]
    if not cand:
        if pin:
            live = any(k.get("state") == "fresh" for k in hosts.get(pin, {"keys": {}})["keys"].values())
            return {"host": None, "eligible": [], "error": gone(
                f"host {pin} cannot run workflow {run['workflow']}" if live else f"host {pin} is offline")}
        return {"host": None, "eligible": [], "error": gone(f"no live host for env {env}")}
    if run.get("model"):
        mv = f"{run['model']}/{run['variant']}"
        cand = [h for h in cand if run["precision"] in hosts[h]["variants"].get(env, {}).get(mv, [])]
        if not cand:
            want = f"model {run['model']} variant {run['variant']} at {run['precision']}"
            return {"host": None, "eligible": [], "error": gone(
                f"host {pin} does not have {want}" if pin else f"no live host for env {env} has {want}")}
    for a in sorted(run.get("addons", []), key=lambda a: a["node"]):  # 1.36: Optional add-ons, by node id
        if a.get("bypass"):
            continue
        cand = [h for h in cand if f"{run['model']}/{a['component']}" in hosts[h].get("addons", {}).get(env, [])]
        if not cand:
            return {"host": None, "eligible": [], "error": gone(
                f"{a['node']}: {a['title']} not downloaded on host {pin}" if pin
                else f"{a['node']}: {a['title']} not downloaded on any live host for env {env}")}
    has = lambda h, n: n in hosts[h]["loras"].get(env, [])
    with_loras = [h for h in cand if all(has(h, n) for n in run.get("loras", []))]
    if not with_loras:
        first = next(n for n in run["loras"] if not has(cand[0], n))
        if pin:
            return {"host": None, "eligible": [], "error": gone(f"host {pin} does not have LoRA {first}")}
        if not any(has(h, first) for h in cand):
            return {"host": None, "eligible": [], "error": gone(f"no live host for env {env} has LoRA {first}")}
        return {"host": None, "eligible": [], "error": gone(
            f"no live host for env {env} has all of its LoRAs ({first} is not on {cand[0]})")}
    peak = None if run["peak_gb"] is None else cgb(run["peak_gb"])
    ram_peak = cgb(run["ram_peak_gb"]) if run.get("ram_peak_gb") is not None else None
    needs = {entry_key(n) for n in run["needs"]}
    views = {h: host_view(hosts[h]["keys"]) for h in with_loras}
    # what enabled local servers reserve on a host is outside the app's control (decide subtracts it too)
    views = {h: (v[0], None if v[1] is None else v[1] - cgb(hosts[h].get("reserved_gb", 0)), *v[2:])
             for h, v in views.items()}
    decided = {h: admit(h, v[0], v[1], env, peak, needs, set(), v[3], v[2], ram_peak) for h, v in views.items()}
    eligible = [h for h in with_loras if decided[h]["decision"] != "fail"]
    if not eligible:  # too big everywhere: the largest host's reason (budget, then name)
        big = sorted(with_loras, key=lambda h: (-(views[h][1] or 0), h))[0]
        return {"host": None, "eligible": [], "error": gone(decided[big]["error"])}
    if run.get("lost_host") in eligible and len(eligible) > 1:  # attempt 2 goes elsewhere when it can
        eligible.remove(run["lost_host"])
    comp = run.get("lora_component")
    resident = lambda h: any(x[0] == env and x[1] in needs and (comp is None or x[1][0] == comp) for x in views[h][0])
    idle = lambda h: hosts[h].get("idle_since") or ""  # never ran = idle longest
    best = sorted(eligible, key=lambda h: (not hosts[h]["free"], not resident(h),
                                           decided[h]["decision"] != "release", idle(h), h))
    return {"host": best[0], "eligible": eligible, "error": None}


def simulate(fx: dict) -> dict:
    """One host, runs released one at a time in the dispatcher's order with host admission; every unload is answered
    at once. Plans: {env, peak_gb, lora_component, components (in order of last use)}; batches: two-step pipelines
    with row 1 first and a window of WINDOW rows."""
    budget, plans = cgb(fx["budget_gb"]), fx["plans"]
    keys_of = {n: [(c, entry_key(c | {"model": p["model"]})) for c in p["components"]] for n, p in plans.items()}
    kept = lambda n: [(c, k) for c, k in keys_of[n] if not c.get("unloads_after")]
    resident: dict[tuple, list] = {}  # (env, key) -> [gb, used]
    for i, n in enumerate(fx.get("resident", [])):
        for j, (c, k) in enumerate(kept(n)):
            resident[plans[n]["env"], k] = [cgb(c["gb"]), (i - len(fx["resident"])) * 1000 + j]
    runs, seq = [], [0]

    def add(run: dict) -> None:
        seq[0] += 1
        runs.append(run | {"seq": seq[0], "seconds": run.get("seconds", 10)})

    for r in fx.get("runs", []):
        add(r)
    batches = {b["id"]: b | {"started": 1, "ended": 0} for b in fx.get("batches", [])}
    for b in batches.values():
        add({"id": f"{b['id']}.1.1", "owner": b["owner"], "lane": "batch", "plan": b["steps"][0], "at": b["at"],
             "seconds": b["seconds"][0], "row": (b["id"], 1, 1)})

    def is_resident(r: dict) -> bool:
        p = plans[r["plan"]]
        return any((p["env"], k) in resident for c, k in keys_of[r["plan"]] if c["component"] == p["lora_component"])

    t, last, skips, first_skip = 0, {}, {}, {}
    releases, unloads, loads, fails = [], [], {}, {}
    while runs:
        ready = [r for r in runs if r["at"] <= t]
        if not ready:
            t = min(r["at"] for r in runs)
            continue
        lane = "interactive" if any(r["lane"] == "interactive" for r in ready) else "batch"
        owners: dict[str, list] = {}
        for r in sorted((r for r in ready if r["lane"] == lane), key=lambda r: (r["at"], r["seq"])):
            owners.setdefault(r["owner"], []).append(r)
        order = sorted(owners, key=lambda o: (o in last, last.get(o, 0), owners[o][0]["at"], owners[o][0]["seq"]))
        heads = [next((r for r in owners[o] if is_resident(r)), owners[o][0]) for o in order]
        overdue = lambda r: skips.get(r["id"], 0) >= SWAP_SKIPS or t - first_skip.get(r["id"], t) >= SWAP_WAIT_S
        pick = next((r for r in heads if is_resident(r) or overdue(r)), heads[0])
        for r in heads[: heads.index(pick)]:
            skips[r["id"]] = skips.get(r["id"], 0) + 1
            first_skip.setdefault(r["id"], t)
        plan = plans[pick["plan"]]
        entries = [(env, k, gb, False, True, used) for (env, k), (gb, used) in resident.items()]
        referenced = {(plans[r["plan"]]["env"], k) for r in ready if r is not pick for _, k in keys_of[r["plan"]]}
        got = admit("mac", entries, budget, plan["env"], cgb(plan["peak_gb"]), {k for _, k in keys_of[pick["plan"]]},
                    referenced)
        runs.remove(pick)
        end = t
        if got["decision"] == "fail":
            fails[pick["id"]] = got["error"]
        else:
            assert got["decision"] != "wait", "nothing in a simulation is stale"
            for env, ks in got["unload"].items():
                unloads.append({"before": pick["id"], "env": env, "components": [k[0] for k in ks]})
                for k in ks:
                    del resident[env, k]
            releases.append(pick["id"])
            if any((plan["env"], k) not in resident for _, k in kept(pick["plan"])):
                loads[pick["plan"]] = loads.get(pick["plan"], 0) + 1
            end = t + pick["seconds"]
            for j, (c, k) in enumerate(kept(pick["plan"])):
                resident[plan["env"], k] = [cgb(c["gb"]), end * 1000 + j]
            last[pick["owner"]] = t
        if "row" in pick:
            bid, row, step = pick["row"]
            b = batches[bid]
            if step < len(b["steps"]) and got["decision"] != "fail":
                add({"id": f"{bid}.{row}.{step + 1}", "owner": b["owner"], "lane": "batch", "plan": b["steps"][step],
                     "at": end, "seconds": b["seconds"][step], "row": (bid, row, step + 1)})
            else:
                b["ended"] += 1
                while b["started"] < b["rows"] and b["started"] - b["ended"] < WINDOW:  # row 1 has ended
                    b["started"] += 1
                    add({"id": f"{bid}.{b['started']}.1", "owner": b["owner"], "lane": "batch", "plan": b["steps"][0],
                         "at": end, "seconds": b["seconds"][0], "row": (bid, b["started"], 1)})
        t = end
    return {"releases": releases, "unloads": unloads, "loads": loads, "fails": fails}


def check_dispatch(registry: Registry) -> None:
    g = HERE / "fixtures" / "dispatch"
    reg = "https://helmstudio.local/contracts/registry.schema.json"
    unload = "https://helmstudio.local/contracts/messages/req.runner.unload.schema.json"
    entry_v = Draft202012Validator({"$ref": f"{reg}#/$defs/host/properties/resident/items"}, registry=registry,
                                   format_checker=FormatChecker())
    comp_v = Draft202012Validator({"$ref": f"{unload}#/$defs/component"}, registry=registry)
    for p in sorted(g.glob("*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/dispatch/{p.name}"
        if p.name.startswith("admit."):
            shapes = [(entry_v, e) for k in fx["keys"].values() for e in k.get("resident", [])]
            shapes += [(comp_v, n) for q in [fx["run"], *fx.get("queued", [])] for n in q["needs"]]
            shapes += [(comp_v, c) for cs in fx["expect"]["unload"].values() for c in cs]
            got = admit_fixture(fx)
        elif p.name.startswith("place."):
            shapes = [(entry_v, e) for x in fx["hosts"].values() for k in x["keys"].values() for e in k.get("resident", [])]
            shapes += [(comp_v, n) for n in fx["run"]["needs"]]
            got = place(fx)
        elif p.name.startswith("order."):
            shapes = [(comp_v, c | {"model": pl["model"]}) for pl in fx["plans"].values()
                      for c in ({k: v for k, v in c.items() if k in ("component", "variant", "precision")}
                                for c in pl["components"])]
            got = simulate(fx)
            got = {k: v for k, v in got.items() if k in fx["expect"] or v}
        else:
            fail(f"{name}: not admit.*, place.* or order.*")
            continue
        if bad := [e for v, x in shapes for e in v.iter_errors(x)]:
            fail(f"{name}: {bad[0].json_path}: {bad[0].message}")
        elif got != fx["expect"]:
            fail(f"{name}: got {json.dumps(got)}, want {json.dumps(fx['expect'])}")
        else:
            print(f"ok   {name}")


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
    check_decisions()
    check_mentions(registry, palette)
    check_loras()
    check_lora_detection()
    check_video(registry, palette)
    check_voices(registry, palette)
    check_timelines(registry, palette)
    check_dispatch(registry)
    check_openapi()
    check_names()
    print(f"\n{len(errors)} failure(s)" if errors else "\nall contract checks passed")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
