"""This runner's part of KV `registry`: keys <host>.<env>.nodes, .workflows, .models, .loras and .looks, written once
at start (`loras`: the shared LoRAs core.lora.Library indexed, `looks`: the packs' starter looks with their LoRAs
looked up in that index; main adds both with `scanned_value`).

The values are built from the packs whose runtime env is RUNNER_ENV (core.pack) plus the core nodes their
workflows use or their model.yaml lists in `publish`, with this runner's published_at and published_by. Each is checked against registry.schema.json,
and every workflow against the node schemas, so a bad pack stops the runner instead of reaching studio."""

import json
import logging

from config import ENV, HOST, PUBLISHED_BY
from core import graph, models
from core import pack as packs
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger(__name__)

MODELS_BUDGET = 768 * 1024  # bytes of the models value (NATS max_payload 1 MB)
KINDS = ("nodes", "workflows", "models", "loras", "looks")


def build(env: str = ENV) -> tuple[dict[str, dict], dict, list]:
    """(kind → registry value, node kind → core.pack.Node, the packs). ValueError when anything is
    off-contract or inconsistent."""
    loaded = packs.for_env(env)
    core = packs.core_nodes()
    pack_nodes = {kind: n for _, nodes in loaded for kind, n in nodes.items()}
    workflows = [w for p, _ in loaded for w in p.workflows]
    used = {n["kind"] for w in workflows for n in w["nodes"].values()}
    offered = {
        k for p, _ in loaded for k in p.spec.get("publish", [])
    }  # core nodes a pack offers unused
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
    for kind, value in values.items():
        if errs := schemas.registry_errors(kind, value):
            raise ValueError(f"registry {kind} is off-contract: {'; '.join(errs[:3])}")
    ids = [f"{p.family}/{x['id']}" for p, _ in loaded for x in p.looks]
    if dup := {i for i in ids if ids.count(i) > 1}:
        raise ValueError(f"looks: ids {sorted(dup)} are shipped by two packs")
    for w in workflows:
        if w["env"] != env:
            raise ValueError(f"workflow {w['id']}: env {w['env']}, this runner is {env}")
        if errs := graph.validate(w["nodes"], node_schemas, run=False):
            raise ValueError(f"workflow {w['id']} does not fit its nodes: {'; '.join(errs[:3])}")
    return values, impls, [p for p, _ in loaded]


def models_value(loaded, impls: dict, node_schemas: dict, env: str = ENV) -> dict:
    """The `models` value as the disk is now (core.models.entry per pack, `memory` for present variants, the
    licences); `prefused` stays empty until fill_prefused. Rebuilt by every rescan."""
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


ASK_IMAGES = (
    4  # a text workflow is planned at 4 thumbnails of 1024 px (contracts/README.md, memory plans)
)


def memory_plans(pack, variant: str, precision: str, impls: dict, node_schemas: dict) -> list[dict]:
    """The `memory` items of (variant, precision): one over the media workflows, and one per text (ask) workflow,
    named by `workflow` and planned at ASK_IMAGES thumbnails; a pack with only text workflows (llm) has one item
    without `workflow`."""
    text = [w for w in pack.workflows if w.get("media") == "text"]
    media = [w for w in pack.workflows if w.get("media") != "text"]
    if not media:
        return [memory_plan(pack, variant, precision, impls, node_schemas, text)]
    plans = [memory_plan(pack, variant, precision, impls, node_schemas, media)]
    plans += [memory_plan(pack, variant, precision, impls, node_schemas, [w]) | {"workflow": w["id"]}
              for w in text]  # fmt: skip
    # a workflow that loads nothing (echo-ask) has no plan
    return [p for p in plans if p["components"]]


def memory_plan(
    pack, variant: str, precision: str, impls: dict, node_schemas: dict, workflows=None
) -> dict:
    """The models key's `memory` item of (variant, precision) (contracts/README.md, Two envs on one host: the
    hand-over): over the pack's workflows at their default sizes, run in `graph.run`'s order, the components a run
    loads in the order of their last use (`unloads_after` when the stage that last uses one unloads it) and
    peak_gb = max over stages of (kept components + the stage's own + its peak_gb formula), starting warm; planned_at:
    the largest mp and frames those formulas saw."""
    comps: dict[tuple, dict] = {}  # residency key → component item, in order of last use
    stages = []  # (keys, peak) per stage
    at = {"mp": 0.0, "frames": 0}  # the largest sizes a stage's formula saw (planned_at)
    if workflows is None:  # the media workflows' plan (memory_plans adds the text ones')
        workflows = [w for w in pack.workflows if w.get("media") != "text"]
    asks = any(w.get("media") == "text" for w in workflows)
    for w in workflows:
        nodes = json.loads(json.dumps(w["nodes"]))
        for node in nodes.values():
            params = node.setdefault("params", {})
            for spec in node_schemas[node["kind"]]["params"]:
                if "default" in spec:
                    params.setdefault(spec["name"], spec["default"])
                if (
                    spec["type"] == "rows"
                    and "Media" in spec["item"].values()
                    and w.get("media") == "text"
                ):
                    thumb = {
                        "take_id": "",
                        "key": "",
                        "content_type": "image/png",
                        "width": 1024,
                        "height": 1024,
                    }
                    params[spec["name"]] = [
                        {"take": thumb}
                    ] * ASK_IMAGES  # sized as planned_at.images says
            if node["kind"] == f"{pack.family}/model":
                params |= {"variant": variant, "precision": precision}
        sinks = [n for n in graph.order(nodes) if not node_schemas[nodes[n]["kind"]]["outputs"]]
        for nid in graph.pull_order(nodes, sinks):
            impl = impls[nodes[nid]["kind"]]
            if not (impl.uses or impl.peak is not None):
                continue
            keys = graph._components(nodes, impls, node_schemas, nid)
            peak = graph.stage_peak(nodes, node_schemas, nid, impl, nodes[nid]["params"], {})
            stages.append((set(keys), peak))
            if impl.peak is not None:
                sizes = graph.stage_sizes(nodes, node_schemas, nid, {})
                at = {
                    "mp": max(at["mp"], sizes["mp"]),
                    "frames": max(at["frames"], sizes["frames"]),
                }
            for key, model in keys.items():
                comps.pop(key, None)
                comps[key] = {"component": key[2], "variant": key[1], "precision": key[3],
                              "gb": model.gb(key[2])[0]} | ({"unloads_after": True} if key[2] in impl.unload_after else {})  # fmt: skip
    kept = {k for k, c in comps.items() if not c.get("unloads_after")}
    peak = max((sum(comps[k]["gb"] for k in kept | keys) + p for keys, p in stages), default=0.0)
    items = [c | {"gb": round(c["gb"], 2)} for c in comps.values()]
    at = {"mp": round(at["mp"], 4), "frames": at["frames"]} | (
        {"images": ASK_IMAGES} if asks else {}
    )
    return {"precision": precision, "peak_gb": round(max(peak, 0.01), 2), "components": items,
            "planned_at": at}  # fmt: skip


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
        found = [(x, packs.models_root() / p.spec["variants"][x["variant"]]["dir"] / x["file"])
                 for x in p.spec["lora"]["prefused"]]  # fmt: skip
        block["prefused"] = [
            {"lora_id": files[f], "variant": x["variant"]} for x, f in found if f in files
        ]
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
