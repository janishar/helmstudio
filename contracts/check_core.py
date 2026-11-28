"""contracts/check.py, schemas, fixtures and shared helpers (fail, errors, load_schemas, fits, rnd).
Moved out of check.py as is; check.py re-exports every name."""

import json
import math
import re
from decimal import ROUND_HALF_UP, Decimal
from pathlib import Path

import yaml
from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

HERE = Path(__file__).parent
SCHEMA_FILES = sorted((HERE / "messages").glob("*.schema.json"))
TOOL_FILES = sorted((HERE / "tools").glob("*.json"))  # the assistant's tools/catalog.json, actions.json and their schema
PRESETS_SCHEMA = HERE / "providers" / "presets.schema.json"  # API 1.51: the model provider presets' shape
MANIFEST_SCHEMA = HERE / "plugins" / "manifest.schema.json"  # API 1.61: a plugin's plugin.json
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
CANONICAL = ["", "qwen-image.", "ltx.", "llm.", "server.", "minimax-h3-image.", "minimax-h3.", "qwen3-tts.", "zonos3.", "minimax-music3.", "stable-audio-3.", "mmaudio.", "ace-step."]
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
    "plugin_runs",  # API 1.67: host_settings <host>.plugins
    "plugins",  # API 1.67: registry <host>.<env>.plugins
    "guides",  # API 1.71: registry <host>.<env>.guides
]  # registry.schema.json $defs; fixtures/registry/<key>.*
# API 1.44: openapi.yaml schemas that copy a messages/assistant.schema.json $def; their fixtures must agree on both
TWINS = {
    "AssistantScreen": "screen", "AssistantAction": "action", "AssistantActionResult": "result", "AssistantLine": "line",
    "AssistantCard": "card", "AssistantCaps": "caps", "AssistantError": "error", "AssistantLimits": "limits",
}
# openapi.yaml components/schemas with body fixtures in fixtures/http/<name>.*
HTTP_SCHEMAS = [
    "Run", "RunRequest", "RunPlan", "Queue", "Take", "TakePage", "TakePatch", "TakeReuse", "TakeUse", "UploadCreate", "Upload", "UploadLimits",
    "Batch", "BatchCreate", "BatchRow", "BatchEvent",
    "Workflow", "WorkflowCreate", "WorkflowUpdate", "WorkflowPatch", "WorkflowValidation", "WorkflowError", "NodePalette",
    "Pipeline", "PipelineCreate", "PipelineUpdate", "PipelinePatch", "PipelineValidation", "PipelineError", "TakePipeline",
    "BatchPick", "BatchRowRetry", "BatchReview", "RowExecution", "RowState",
    "LibraryItem", "LibraryItemList", "LibraryItemCreate", "LibraryItemUpdate",
    "Lora", "LoraList", "LoraPatch", "LoraUploadCreate", "LoraUpload",
    "HostList",
    "Decision", "DecisionList", "MemoryItem", "MemoryList", "MemoryCreate", "BatchAgain",
    "ChatMessage", "ChatPage", "ChatPost", "Provider", "ProviderPut", "ProviderList", "ProviderTest", "ModelSourceList",
    "ProviderPreset", "ProviderPresetList", "ProviderFetch", "ProviderKey",  # 1.51
    "ProviderKeySave", "ProviderKeySaved",  # 1.52
    "CustomNodeList",
    "ModelList", "ModelDownload", "ModelJob", "ModelJobList", "HostSettings", "HostSettingsPut", "ModelFoldersPut", "HostPins", "HostUnload",
    "HostUnloadAccepted", "ImportTakeCreate", "ImportTake",
    "Timeline", "TimelineList", "TimelineCreate", "TimelineUpdate", "TimelineAddClips", "TimelineShotCreate",
    "TimelineRerender", "ExportPlan", "MediaJob", "TakeFrame", "TakeMux", "TakeMuxRequest", "TakeUses", "ClipActions", "TimelineLineCreate",
    "TimelineEdits", "TimelineEditResult", "TimelineAgentView", "TimelineRevisionList", "TimelineFrame",  # 1.49
    "SessionList", "SessionNames", "SessionNamesPut",
    "AssistantPost", "AssistantThreadList", "AssistantThreadDetail", "AssistantThreadPatch", "AssistantEvent", "AssistantMemoryList",
    "AssistantSettings", "AssistantSettingsPut", "AssistantCacheStats", *TWINS,
    "PluginList", "PluginRescan", "PluginPut", "PluginProviderCreate",  # 1.61
    "Skill", "SkillList", "SkillFile",  # 1.64
    "MagicDoc", "MagicBoard", "MagicBoardList", "MagicBoardCreate", "MagicEdits", "MagicEditResult", "MagicAgentView",
    "MagicRevisionList", "MagicSnapshot", "MagicSnapshotList",  # 1.80
    "MagicKinds", "MagicSettings", "MagicPick", "MagicRunCreate", "MagicRunStarted", "MagicPlan",  # 1.81
    "MagicSendCreate", "MagicSendResult", "MagicSendList",  # 1.82
]
OPENAPI = "https://helmstudio.local/contracts/openapi.yaml"
OPERATION_PREFIX = {"/v1/studio/": "studio", "/files/": "files", "/auth/": "auth"}
# Names of the reference product that must not come back.
FORBIDDEN = re.compile(r"\bcatalog(?!(\.schema)?\.json)|\bora[._]|\bORA\b|agents_service|\bagents\.", re.IGNORECASE)
METHODS = {"get", "post", "put", "patch", "delete"}

errors: list[str] = []


def fail(msg: str) -> None:
    errors.append(msg)
    print("FAIL", msg)


def load_schemas() -> tuple[dict[str, dict], Registry]:
    schemas = {p.name.removesuffix(".schema.json"): json.loads(p.read_text()) for p in SCHEMA_FILES}
    schemas["registry"] = json.loads((HERE / "registry.schema.json").read_text())
    schemas |= {f"tools/{p.name}": json.loads(p.read_text()) for p in TOOL_FILES}
    schemas["providers/presets.schema.json"] = json.loads(PRESETS_SCHEMA.read_text())
    schemas["plugins/manifest.schema.json"] = json.loads(MANIFEST_SCHEMA.read_text())
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
    merged = graph_values(nodes, kinds)
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
        out += [f"{nid}.{k}: {err}" for k, err in values_by_errors(schema, node.get("params", {}), merged)]
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
    """A rows param's one required media field (API 1.22: video or audio references; from 1.37 `optional` media
    fields aside, MiniMax H3's soundtrack), else None."""
    fields = [k for k, v in param.get("item", {}).items()
              if isinstance(v, dict) and v["type"] == "media" and not v.get("optional")]
    return fields[0] if param["type"] == "rows" and len(fields) == 1 else None


def optional_media(param: dict) -> list[str]:
    """A rows param's optional media fields (API 1.37), in item order."""
    return [k for k, v in param.get("item", {}).items() if isinstance(v, dict) and v["type"] == "media" and v.get("optional")]


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
    if t in ("int", "float") and param.get("auto") is True and v == 0:  # API 1.79: 0 is Auto, below min too
        return None
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


def values_by_errors(schema: dict, values: dict, graph: dict | None = None) -> list[tuple[str, str]]:
    """(param, message) for every int or (API 1.39) enum param whose `values_by` refuses the node's merged value (schema
    defaults, then `values`): its allowed subset for the value of `values_by.param`, the node's own param or (1.39)
    `<node>.<param>` of another node, read from `graph` (node id → its merged values; absent: allows all) (README,
    Video: steps; Video: Distilled actions)."""
    merged = {p["name"]: p["default"] for p in schema["params"]} | values
    out = []
    for p in schema["params"]:
        if not (vb := p.get("values_by")):
            continue
        pool = p.get("values") or p.get("enum", [])
        nid, _, name = vb["param"].rpartition(".")
        by = (graph or {}).get(nid, {}).get(name) if nid else merged.get(name)
        allowed = vb["values"].get(by, pool) if isinstance(by, str) else pool
        if merged.get(p["name"]) in pool and merged[p["name"]] not in allowed:
            out.append((p["name"], f"must be one of {', '.join(map(str, allowed))}"))
    return out


def graph_values(nodes: dict, kinds: dict, overlay: dict | None = None) -> dict[str, dict]:
    """Each node's merged values (its kind's defaults, its params, then `overlay` paths "<node>.<param>"), what a
    cross-node `values_by` reads (API 1.39)."""
    out = {nid: {p["name"]: p["default"] for p in kinds.get(n["kind"], {}).get("params", [])} | n.get("params", {})
           for nid, n in nodes.items()}
    for path, v in (overlay or {}).items():
        nid, _, name = path.partition(".")
        if nid in out and name != "bypass":
            out[nid][name] = v
    return out


def schema_errors(n: dict) -> list[str]:
    """What JSON Schema can't say about one node schema's `values`, `values_by`, `auto`."""
    out, params = [], {p["name"]: p for p in n["params"]}
    for p in n["params"]:
        vs, vb, auto = p.get("values"), p.get("values_by"), p.get("auto")
        if vs and (vs != sorted(vs) or p["default"] not in vs or not all(p.get("min", x) <= x <= p.get("max", x) for x in vs)):
            out.append(f"{p['name']}: values must ascend, lie within min..max and hold the default")
        if vb:  # API 1.39: on an enum too, and `<node>.<param>` names another node's (checked in the graph)
            pool, cross = vs or p.get("enum") or [], "." in vb["param"]
            ep = None if cross else params.get(vb["param"])
            if (not cross and (not ep or ep["type"] != "enum" or set(vb["values"]) - set(ep["enum"]))) or any(
                    set(x) - set(pool) for x in vb["values"].values()):
                out.append(f"{p['name']}: values_by must name an enum param, its values, subsets of the param's values")
            elif cross and any(p["default"] not in x for x in vb["values"].values()):
                out.append(f"{p['name']}: a cross-node values_by must allow the default for every value")
            elif not cross and p["default"] not in vb["values"].get(ep["default"], pool):
                out.append(f"{p['name']}: the default is not allowed for {vb['param']} {ep['default']}")
        if isinstance(auto, dict) and not 0 < auto["min_s"] < auto["max_s"]:
            out.append(f"{p['name']}: auto needs 0 < min_s < max_s")
        if set(p.get("not_with", [])) - {i["name"] for i in n["inputs"] if i.get("optional")}:
            out.append(f"{p['name']}: not_with must name optional inputs of the node")
        if set(p.get("addons", {})) - set(p.get("enum", [])):  # API 1.41: an add-on per value
            out.append(f"{p['name']}: addons must key values of its enum")
        named = {i["name"] for i in n["inputs"] if i.get("optional")} | {q["name"] for q in n["params"] if q["type"] == "rows"}
        for w, by in p.get("addons_with", {}).items():
            if w not in named:
                out.append(f"{p['name']}: addons_with must name optional inputs or rows params of the node ({w})")
            elif set(by) - set(p.get("addons", {})):
                out.append(f"{p['name']}: addons_with {w} must key values of its addons")
        if "max_total_s" in p and not media_field(p):
            out.append(f"{p['name']}: max_total_s needs an item with exactly one required media field")
        for f, x in p.get("item", {}).items():  # API 1.37
            if isinstance(x, dict) and x.get("optional") and (x["type"] != "media" or x["default"] is not None):
                out.append(f"{p['name']}: item field {f}: optional is for a media field with default null")
        for x in [p] + [v for v in p.get("item", {}).values() if isinstance(v, dict)]:
            if x.get("min_s", 0) > x.get("max_s", math.inf):
                out.append(f"{p['name']}: min_s is above max_s")
        if p.get("script") and not script_target(n):  # API 1.32 (README, Dialogue)
            out.append(f"{p['name']}: a script needs a rows param `lines` with voice, text, style and pause_s fields")
    if n.get("sound") and not any(o["type"] == "Audio" for o in n["outputs"]):  # API 1.79
        out.append("sound: only a node with an Audio output has a sound class")
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


# API 1.81: a workflow's `standard` block (registry.schema.json standard; README, Magic: the standard request)
STANDARD_MEDIA = {"image": "image", "video": "video", "voice": "audio", "music": "audio", "sound": "audio"}
STANDARD_TYPES = {"prompt": {"text", "string"}, "line": {"text", "string"}, "length": {"frames", "float"},
                  "width": {"int"}, "height": {"int"}, "seed": {"seed"}, "voice": {"voice"},
                  # API 1.85 (plan 39 phase H): music and sound
                  "lyrics": {"text", "string"}, "instrumental": {"bool"}, "key": {"enum"}, "bpm": {"int"}}


def standard_errors(wf: dict, kinds: dict) -> list[str]:
    """Why a workflow's standard block does not fit it: its kind against the media, each field's param (unwired, of
    a type that field fills), each `set` key a param of the workflow."""
    st = wf.get("standard")
    if not st:
        return []
    errs = []
    if STANDARD_MEDIA[st["kind"]] != wf.get("media", "image"):
        errs.append(f"standard: kind {st['kind']} does not make {wf.get('media', 'image')} takes")

    def param(path: str):
        nid, name = path.split(".", 1)
        node = wf["nodes"].get(nid)
        if node is None:
            return None, f"no node {nid}"
        if name in (node.get("in") or {}):
            return None, f"{path} is wired"
        p = next((x for x in kinds.get(node["kind"], {}).get("params", []) if x["name"] == name), None)
        return p, None if p else f"{node['kind']} has no param {name}"

    for field, path in st["fields"].items():
        p, why = param(path)
        if why:
            errs.append(f"standard.fields.{field}: {why}")
            continue
        item = p.get("item") or {}
        image_rows = p["type"] == "rows" and "Image" in item.values()
        timed = any(isinstance(v, dict) and v.get("position") == "seconds" for v in item.values())
        ok = (p["type"] in STANDARD_TYPES[field] if field in STANDARD_TYPES
              else image_rows if field == "refs"
              else p["type"] == "media" and "video" in (p.get("accepts") or []) if field == "from"
              else p["type"] == "image" or (image_rows and timed))
        if not ok:
            errs.append(f"standard.fields.{field}: {path} is a {p['type']} param")
    for path in st.get("set") or {}:
        if (why := param(path)[1]):
            errs.append(f"standard.set.{path}: {why}")
    return errs


def group_errors(studio: dict) -> list[str]:
    """API 1.79 (README, Music and sound): each `groups` item is a quick param path, in at most one group, and a
    `count` default is at most its max."""
    items = [x for g in studio.get("groups", []) for x in g["items"]]
    out = [f"studio.groups: {x} is not a quick chip" for x in items if x not in studio.get("quick", [])]
    out += [f"studio.groups: {x} is in two groups" for x in sorted({x for x in items if items.count(x) > 1})]
    if (c := studio.get("count")) and c["default"] > c["max"]:
        out.append("studio.count: the default is above the max")
    return out


def upload_limits(nodes: list[dict]) -> dict:
    """GET /v1/studio/uploads/limits (API 1.38, README Optimised uploads): the largest max of a width or height param
    over the node schemas with a Canvas output; 16384 (the upload cap) when there is none."""
    sides = [p["max"] for n in nodes if any(o["type"] == "Canvas" for o in n["outputs"])
             for p in n["params"] if p["name"] in ("width", "height") and "max" in p]  # fmt: skip
    return {"image_max_side": int(max(sides, default=16384))}


HANDLES = {"Model", "Conditioning", "Latent"}


MEDIA = {"Image", "Video", "Audio"}  # what an input of type Media accepts (Audio from API 1.30)


def fits(got: str | None, want: str) -> bool:
    """A wire of type `got` may feed a port of type `want` (README, Graph rules: fits); handles also need one family."""
    return got is not None and (got == want or (want == "Media" and got in MEDIA))


LORA_ID = re.compile(r"^([0-9a-f]{64}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$")


def lora_field(param: dict) -> str | None:
    """A rows param's Lora field (the lora-stack's `lora`), else None."""
    return next((k for k, v in param.get("item", {}).items() if v == "Lora"), None) if param["type"] == "rows" else None


def image_field(param: dict) -> str | None:
    """A rows param's one Image field (a list of takes binds to it), else None."""
    fields = [k for k, v in param.get("item", {}).items() if v == "Image"] if param["type"] == "rows" else []
    return fields[0] if len(fields) == 1 else None


def rnd(x: float) -> int:
    """Round half away from zero, exactly: Go math.Round; JS Math.sign(x) * Math.round(Math.abs(x))."""
    return int(Decimal(x).quantize(Decimal(1), rounding=ROUND_HALF_UP))


def ratio(s: str) -> tuple[int, int]:
    a, _, b = s.partition("/")
    return int(a), int(b or 1)
