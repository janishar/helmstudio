"""This runner's part of KV `registry`: keys <host>.<env>.nodes, .workflows, .models, .voices, .guides, .loras and
.looks, written once at start (`loras`: the shared LoRAs core.lora.Library indexed, `looks`: the packs' starter looks
with their LoRAs looked up in that index; main adds both with `scanned_value`; `guides`, API 1.71: each pack's
assistant.md and model.yaml `recipes`).

The values are built from the packs whose runtime env is RUNNER_ENV (core.pack) plus the core nodes their
workflows use or their model.yaml lists in `publish`, with this runner's published_at and published_by. Each is checked against registry.schema.json,
and every workflow against the node schemas, so a bad pack stops the runner instead of reaching studio."""

import copy
import json
import logging
import math
import re

from config import ENV, HOST, PUBLISHED_BY
from core import graph, models, voices
from core import pack as packs
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger(__name__)

MODELS_BUDGET = 768 * 1024  # bytes of the models value (NATS max_payload 1 MB)
KINDS = ("nodes", "workflows", "models", "voices", "loras", "looks", "guides")
CARD_MAX, GUIDE_MAX = 800, 24576  # characters of a guide's card and of the whole guide (API 1.71)


def build(env: str = ENV) -> tuple[dict[str, dict], dict, list]:
    """(kind → registry value, node kind → core.pack.Node, the packs). ValueError when anything is
    off-contract or inconsistent."""
    loaded = packs.for_env(env)
    core = packs.core_nodes()
    pack_nodes = {kind: n for _, nodes in loaded for kind, n in nodes.items()}
    # a pack with two runtimes ships each workflow for one of them (ltx's are MLX's): the other's are not published
    workflows = [w for p, _ in loaded for w in p.workflows if w["env"] == env or len(p.spec["runtimes"]) == 1]
    used = {n["kind"] for w in workflows for n in w["nodes"].values()}
    offered = {k for p, _ in loaded for k in p.spec.get("publish", [])}  # core nodes a pack offers unused
    if unknown := offered - core.keys():
        raise ValueError(f"publish: unknown core nodes {sorted(unknown)}")
    used |= offered
    sources = [k for k, (s, _) in core.items() if k in used and s["outputs"]]
    sinks = [k for k, (s, _) in core.items() if k in used and not s["outputs"]]
    every = {k: core[k] for k in sources} | pack_nodes | {k: core[k] for k in sinks}
    meta = {"host": HOST, "env": env, "published_at": now(), "published_by": PUBLISHED_BY}
    values = {
        "nodes": {"schema_version": 1} | meta | {"nodes": [s for s, _ in every.values()]},
        "workflows": {"schema_version": 1} | meta | {"workflows": workflows},
    }
    node_schemas = {k: s for k, (s, _) in every.items()}
    impls = {k: n for k, (_, n) in every.items()}
    values["models"] = models_value([p for p, _ in loaded], impls, node_schemas, env)
    values["voices"] = {"schema_version": 1} | meta | {"voices": voices.entries([p for p, _ in loaded])}
    for kind, value in values.items():
        if errs := schemas.registry_errors(kind, value):
            raise ValueError(f"registry {kind} is off-contract: {'; '.join(errs[:3])}")
    for kind in ("looks", "voices"):
        ids = [f"{p.family}/{x['id']}" for p, _ in loaded for x in getattr(p, kind)]
        if dup := {i for i in ids if ids.count(i) > 1}:
            raise ValueError(f"{kind}: ids {sorted(dup)} are shipped by two packs")
    look_ids = {f"{p.family}/{x['id']}" for p, _ in loaded for x in p.looks}
    if clash := look_ids & {f"{p.family}/{x['id']}" for p, _ in loaded for x in p.voices}:
        raise ValueError(f"voices: ids {sorted(clash)} are also look ids (one /library/<family>/<id> URL)")
    for w in workflows:
        if w["env"] != env:
            raise ValueError(f"workflow {w['id']}: env {w['env']}, this runner is {env}")
        if (want := graph.workflow_media(w["nodes"], node_schemas)) != w.get("media", "image"):
            raise ValueError(f"workflow {w['id']}: media {w.get('media', 'image')}, its graph makes {want}")
        if errs := graph.validate(w["nodes"], node_schemas, run=False):
            raise ValueError(f"workflow {w['id']} does not fit its nodes: {'; '.join(errs[:3])}")
        if errs := standard_errors(w, node_schemas):
            raise ValueError(f"workflow {w['id']}: {'; '.join(errs[:3])}")
    values["guides"] = (
        {"schema_version": 1} | meta | {"guides": guides([p for p, _ in loaded], workflows, node_schemas)}
    )
    if errs := schemas.registry_errors("guides", values["guides"]):
        raise ValueError(f"registry guides is off-contract: {'; '.join(errs[:3])}")
    return values, impls, [p for p, _ in loaded]


def card(text: str) -> str | None:
    """A guide's `## Card` section without its heading (API 1.71), or None."""
    m = re.search(r"^## Card\n(.*?)(?=^## |\Z)", text, re.DOTALL | re.MULTILINE)
    return m.group(1).strip() if m else None


# API 1.81: a workflow's `standard` block (Magic's standard request; contracts/README.md, Magic: the standard request)
STANDARD_MEDIA = {
    "image": "image",
    "video": "video",
    "voice": "audio",
    "music": "audio",
    "sound": "audio",
}
STANDARD_TYPES = {
    "prompt": {"text", "string"},
    "line": {"text", "string"},
    "length": {"frames", "float"},
    "width": {"int"},
    "height": {"int"},
    "seed": {"seed"},
    "voice": {"voice"},
    # API 1.85 (plan 39 phase H): music and sound
    "lyrics": {"text", "string"},
    "instrumental": {"bool"},
    "key": {"enum"},
    "bpm": {"int"},
}


def standard_errors(workflow: dict, node_schemas: dict) -> list[str]:
    """Why a workflow's standard block does not fit it: the kind against its media, each field's param (unwired, of a
    type the field fills), each `set` key a param."""
    st = workflow.get("standard")
    if not st:
        return []
    errs = []
    if STANDARD_MEDIA[st["kind"]] != workflow.get("media", "image"):
        errs.append(f"standard: kind {st['kind']} does not make {workflow.get('media', 'image')} takes")

    def param(path: str) -> tuple[dict | None, str | None]:
        nid, name = path.split(".", 1)
        node = workflow["nodes"].get(nid)
        if node is None:
            return None, f"no node {nid}"
        if name in (node.get("in") or {}):
            return None, f"{path} is wired"
        p = next((x for x in node_schemas[node["kind"]]["params"] if x["name"] == name), None)
        return p, None if p else f"{node['kind']} has no param {name}"

    for field, path in st["fields"].items():
        p, why = param(path)
        if why:
            errs.append(f"standard.fields.{field}: {why}")
            continue
        item = p.get("item") or {}
        image_rows = p["type"] == "rows" and "Image" in item.values()
        timed = any(isinstance(v, dict) and v.get("position") == "seconds" for v in item.values())
        if field in STANDARD_TYPES:
            ok = p["type"] in STANDARD_TYPES[field]
        elif field == "refs":
            ok = image_rows
        elif field == "from":  # a shot's video (MMAudio's video to sound)
            ok = p["type"] == "media" and "video" in (p.get("accepts") or [])
        else:  # first, last
            ok = p["type"] == "image" or (image_rows and timed)
        if not ok:
            errs.append(f"standard.fields.{field}: {path} is a {p['type']} param")
    for path in st.get("set") or {}:
        if why := param(path)[1]:
            errs.append(f"standard.set.{path}: {why}")
    return errs


def recipe_errors(recipe: dict, workflow: dict, node_schemas: dict) -> list[str]:
    """Why a recipe does not fit its workflow: a node not in it, a param not in the node's schema, a value the graph
    rules refuse (graph.validate on the workflow with the recipe's values set); `<node>.bypass` sets the bypass."""
    nodes, errs = copy.deepcopy(workflow["nodes"]), []
    for key, value in recipe["params"].items():
        nid, param = key.split(".", 1)
        if (node := nodes.get(nid)) is None:
            errs.append(f"{key}: no node {nid}")
        elif param == "bypass":
            if not isinstance(value, bool):
                errs.append(f"{key}: not true or false")
            node["bypass"] = value
        elif param not in {x["name"] for x in node_schemas[node["kind"]]["params"]}:
            errs.append(f"{key}: {node['kind']} has no param {param}")
        else:
            node.setdefault("params", {})[param] = value
    return errs or graph.validate(nodes, node_schemas, run=False)


def guides(loaded, workflows: list, node_schemas: dict) -> list[dict]:
    """The registry guides (API 1.71): per pack with an assistant.md, its card, the guide and model.yaml `recipes`.
    A recipe of a workflow published here is checked against its node schemas; one of the pack's other runtime
    passes as written (the pack tests check every one), so every runner of a family publishes the same guide.
    ValueError when a guide or a recipe is off."""
    here, out = {w["id"]: w for w in workflows}, []
    for p in loaded:
        if not (f := p.dir / "assistant.md").is_file():
            continue
        text = f.read_text()
        if not (c := card(text)) or len(c) > CARD_MAX or len(text) > GUIDE_MAX:
            raise ValueError(
                f"packs/{p.name}/assistant.md: needs a ## Card of 1-{CARD_MAX} characters, {GUIDE_MAX} in all"
            )
        for r in p.spec.get("recipes", []):
            if r["workflow"] not in {w["id"] for w in p.workflows}:
                raise ValueError(f"{p.name} recipe {r['name']}: no workflow {r['workflow']} in the pack")
            if r["workflow"] in here and (errs := recipe_errors(r, here[r["workflow"]], node_schemas)):
                raise ValueError(f"{p.name} recipe {r['name']}: {'; '.join(errs[:3])}")
        out.append({"family": p.family, "title": p.spec["title"], "card": c, "guide": text,
                    "recipes": p.spec.get("recipes", [])})  # fmt: skip
    return out


def models_value(loaded, impls: dict, node_schemas: dict, env: str = ENV) -> dict:
    """The `models` value as the disk is now (core.models.entry per pack, `memory` for present variants, the
    licences); `prefused` stays empty until fill_prefused. Rebuilt by every rescan."""
    models.refresh_seen()  # API 1.78: what each connected model folder holds, for `unplugged`
    entries = [packs.models_entry(p) for p in loaded]
    for p, entry in zip(loaded, entries, strict=True):
        for variant in entry.get("variants", []):
            if variant["presence"] == "present" and p.spec.get("components"):
                variant["memory"] = [
                    plan
                    for x in variant["precisions"]
                    for plan in memory_plans(p, variant["id"], x, impls, node_schemas)
                ]
    value = {"schema_version": 1, "host": HOST, "env": env, "published_at": now(),
             "published_by": PUBLISHED_BY, "models": entries}  # fmt: skip
    if lic := models.licences(loaded):
        value["licences"] = lic
    # NATS max_payload is 1 MB: the per-file manifests go first, largest first (the variant keeps its bytes)
    variants = sorted((v for e in entries for v in e.get("variants", []) if "files" in v),
                      key=lambda v: -len(v["files"]))  # fmt: skip
    for v in variants:
        if len(json.dumps(value)) <= MODELS_BUDGET:
            break
        del v["files"]
    return value


def changed(old: dict | None, new: dict) -> bool:
    """Whether two values of a key differ beyond published_at."""
    strip = lambda v: {k: x for k, x in (v or {}).items() if k != "published_at"}
    return strip(old) != strip(new)


ASK_IMAGES = 4  # a text workflow is planned at 4 thumbnails of 1024 px (contracts/README.md, memory plans)


def memory_plans(pack, variant: str, precision: str, impls: dict, node_schemas: dict) -> list[dict]:
    """The `memory` items of (variant, precision), over the workflows whose `only` allows them: one over the media
    workflows, and one per text (ask) workflow, named by `workflow` and planned at ASK_IMAGES thumbnails, and one per
    media workflow model.yaml `planned_alone` names; a pack with only text workflows (llm) has one item without
    `workflow`. A media param is planned at model.yaml `plan_media` (its accepted kind's facts)."""
    allowed = [w for w in pack.workflows if allows(pack, w, variant, precision, impls, node_schemas)]
    alone = set(pack.spec.get("planned_alone", []))  # model.yaml: media workflows with an item of their own
    text = [w for w in allowed if w.get("media") == "text" or w["id"] in alone]
    media = [w for w in allowed if w not in text]
    if not media and not text:
        return []
    if not media:
        if all(w.get("media") == "text" for w in text):
            return [memory_plan(pack, variant, precision, impls, node_schemas, text)]
        plans = []
    else:
        plans = [memory_plan(pack, variant, precision, impls, node_schemas, media)]
    plans += [memory_plan(pack, variant, precision, impls, node_schemas, [w]) | {"workflow": w["id"]} for w in text]
    # a workflow that loads nothing (echo-ask) has no plan; a native stage (`uses: []`, a child process) has its
    # peak_gb only (contracts/README.md, The native runtime)
    return [p for p in plans if p["components"] or p["peak_gb"] > 0.01]


def spliced(nodes: dict, impls: dict) -> dict:
    """nodes without the kinds this runner lacks (another runtime's Model -> Model node: ltx/lora-stack on torch,
    model.yaml `lora.runtime: mlx`), each consumer wired to that node's same-named input, as studio splices a
    bypassed node."""
    for nid in [n for n, x in nodes.items() if x["kind"] not in impls]:
        ins = nodes.pop(nid).get("in", {})
        for x in nodes.values():
            x["in"] = {p: ins.get(r.partition(".")[2], r) if r.partition(".")[0] == nid else r
                       for p, r in x.get("in", {}).items()}  # fmt: skip
    return nodes


def allows(pack, workflow: dict, variant: str, precision: str, impls: dict, node_schemas: dict) -> bool:
    """Whether a workflow is planned at (variant, precision): its model node's `only` (README, Media inputs) allows
    both and no stage's pack check refuses that model (LTX text to video needs the distilled variant)."""
    nodes = spliced(json.loads(json.dumps(workflow["nodes"])), impls)
    for n in nodes.values():
        if n["kind"] == f"{pack.family}/model":
            only = n.get("only", {})
            if variant not in only.get("variant", [variant]) or precision not in only.get("precision", [precision]):
                return False
            n["params"] = n.get("params", {}) | {"variant": variant, "precision": precision}
    for n in nodes.values():
        impl = impls[n["kind"]]
        if impl.check is None:
            continue
        types = {i["name"]: i["type"] for i in node_schemas[n["kind"]]["inputs"]}
        models = {p: graph._model(nodes, impls, node_schemas, r) for p, r in n.get("in", {}).items() if types.get(p) == "Model"}  # fmt: skip
        params = {s["name"]: s.get("default") for s in node_schemas[n["kind"]]["params"]} | n.get("params", {})
        if impl.check(params, set(n.get("in", {})), models):
            return False
    return True


def memory_plan(pack, variant: str, precision: str, impls: dict, node_schemas: dict, workflows=None) -> dict:
    """The models key's `memory` item of (variant, precision) (contracts/README.md, Two envs on one host: the
    hand-over): over the pack's workflows at their default sizes, run in `graph.run`'s order, the components a run
    loads in the order of their last use (`unloads_after` when the stage that last uses one unloads it) and
    peak_gb = max over stages of (kept components + the stage's own + its peak_gb formula), starting warm (an `alone`
    stage: without the kept ones); planned_at:
    the largest mp and frames those formulas saw."""
    comps: dict[tuple, dict] = {}  # residency key → component item, in order of last use
    stages = []  # (keys, peak) per stage
    at = {"mp": 0.0, "frames": 0}  # the largest sizes a stage's formula saw (planned_at)
    if workflows is None:  # the media workflows' plan (memory_plans adds the text ones')
        workflows = [w for w in pack.workflows if w.get("media") != "text"]
    asks = any(w.get("media") == "text" for w in workflows)
    for w in workflows:
        nodes = spliced(json.loads(json.dumps(w["nodes"])), impls)
        for node in nodes.values():
            params = node.setdefault("params", {})
            for spec in node_schemas[node["kind"]]["params"]:
                if "default" in spec:
                    params.setdefault(spec["name"], spec["default"])
                if spec["type"] == "media" and params.get(spec["name"]) is None:  # planned at model.yaml plan_media
                    params[spec["name"]] = {"take_id": "", "key": "", "kind": spec["accepts"][0]} | pack.spec.get(
                        "plan_media", {}).get(spec["accepts"][0], {})  # fmt: skip
                if spec["type"] == "rows" and "Media" in spec["item"].values() and w.get("media") == "text":
                    thumb = {
                        "take_id": "",
                        "key": "",
                        "content_type": "image/png",
                        "width": 1024,
                        "height": 1024,
                    }
                    params[spec["name"]] = [{"take": thumb}] * ASK_IMAGES  # sized as planned_at.images says
            if node["kind"] == f"{pack.family}/model":
                params |= {"variant": variant, "precision": precision}
        sinks = [n for n in graph.order(nodes) if not node_schemas[nodes[n]["kind"]]["outputs"]]
        for nid in graph.pull_order(nodes, sinks):
            impl = impls[nodes[nid]["kind"]]
            if not (impl.uses or impl.peak is not None):
                continue
            keys = graph._components(nodes, impls, node_schemas, nid)
            peak = graph.stage_peak(nodes, node_schemas, nid, impl, nodes[nid]["params"], {}, plan=True)
            stages.append((set(keys), peak, impl.alone))
            if impl.peak is not None:
                sizes = graph.stage_sizes(nodes, node_schemas, nid, {})
                if nodes[nid]["kind"].endswith("/extend"):  # README Media inputs: planned_at counts an extend's + 8N
                    p = nodes[nid]["params"]
                    sizes["frames"] += 8 * math.ceil(p["seconds"] * sizes["fps"] / 8)
                at = {
                    "mp": max(at["mp"], sizes["mp"]),
                    "frames": max(at["frames"], sizes["frames"]),
                }
            for key, model in keys.items():
                comps.pop(key, None)
                comps[key] = {"component": key[2], "variant": key[1], "precision": key[3],
                              "gb": model.gb(key[2])[0]} | ({"unloads_after": True} if key[2] in impl.unload_after else {})  # fmt: skip
    kept = {k for k, c in comps.items() if not c.get("unloads_after")}
    # an `alone` stage (model.yaml) runs with none of the kept ones resident: admission evicts them (MiniMax: no two
    # of its models fit together)
    peak = max((sum(comps[k]["gb"] for k in (set() if alone else kept) | keys) + p for keys, p, alone in stages),
               default=0.0)  # fmt: skip
    # one item per (component, variant, precision): a baked copy (ltx/distill) that replaces the plain one in the run
    # is the same entry to studio, as the old in-place fuse (README Media inputs); the later use's place and flags win
    merged: dict[tuple, dict] = {}
    for c in comps.values():
        merged.pop(k := (c["component"], c["variant"], c["precision"]), None)
        merged[k] = c
    items = [c | {"gb": round(c["gb"], 2)} for c in merged.values()]
    at = {"mp": round(at["mp"], 4), "frames": at["frames"]} | ({"images": ASK_IMAGES} if asks else {})
    return {"precision": precision, "peak_gb": round(max(peak, 0.01), 2), "components": items, "planned_at": at}


def scanned_value(kind: str, entries: list[dict], env: str = ENV) -> dict:
    """The `loras` value (the shared LoRAs core.lora.Library.scan indexed) or the `looks` value (core.looks.entries).
    ValueError when off-contract."""
    value = {"schema_version": 1, "host": HOST, "env": env, "published_at": now(),
             "published_by": PUBLISHED_BY, kind: entries}  # fmt: skip
    if errs := schemas.registry_errors(kind, value):
        raise ValueError(f"registry {kind} is off-contract: {'; '.join(errs[:3])}")
    return value


def lora_blocks(loaded) -> dict[str, dict]:
    """family → its registry lora block, for the loaded packs that publish one."""
    return {p.family: b for p in loaded if (b := packs.lora_block(p))}


def fill_prefused(values: dict[str, dict], loaded, files: dict) -> None:
    """models[].lora.prefused (contracts/README.md, LoRAs: pre-fused): the shared index's id (`files`: path →
    sha256, core.lora.Library.files) of each file model.yaml `lora.prefused` names inside a variant's folder; a file
    not on this host gives no entry. The models value holds the same block, so it is updated in place."""
    for p in loaded:
        if (block := packs.lora_block(p)) is None or "prefused" not in block:
            continue
        found = [(x, packs.place(p.spec["variants"][x["variant"]]["dir"]) / x["file"])
                 for x in p.spec["lora"]["prefused"]]  # fmt: skip
        block["prefused"] = [{"lora_id": files[f], "variant": x["variant"]} for x, f in found if f in files]
    if errs := schemas.registry_errors("models", values["models"]):
        raise ValueError(f"registry models is off-contract: {'; '.join(errs[:3])}")


def node_schemas(values: dict[str, dict]) -> dict[str, dict]:
    """kind → node schema, from the `nodes` value."""
    return {n["kind"]: n for n in values["nodes"]["nodes"]}


async def publish(js, values: dict[str, dict]) -> None:
    """Write the values to the `registry` KV bucket."""
    kv = await js.key_value("registry")
    for kind, value in values.items():
        if errs := schemas.registry_errors(kind, value):
            raise ValueError(f"registry {kind} is off-contract: {'; '.join(errs[:3])}")
        await kv.put(f"{HOST}.{ENV}.{kind}", json.dumps(value).encode())
    log.info("registry published: %s.%s.{%s}", HOST, ENV, ",".join(values))
