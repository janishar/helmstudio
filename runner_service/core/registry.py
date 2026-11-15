"""This runner's part of KV `registry`: keys <host>.<env>.nodes, .workflows, .models, .loras and .looks, written once
at start (`loras`: the shared LoRAs core.lora.Library indexed, `looks`: the packs' starter looks with their LoRAs
looked up in that index; main adds both with `scanned_value`).

The values are built from the packs whose runtime env is RUNNER_ENV (core.pack) plus the core nodes their
workflows use or their model.yaml lists in `publish`, with this runner's published_at and published_by. Each is checked against registry.schema.json,
and every workflow against the node schemas, so a bad pack stops the runner instead of reaching studio."""

import json
import logging

from config import ENV, HOST, PUBLISHED_BY
from core import graph
from core import pack as packs
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger(__name__)

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
        "models": {"schema_version": 1}
        | meta
        | {"models": [packs.models_entry(p) for p, _ in loaded]},
    }
    for kind, value in values.items():
        if errs := schemas.registry_errors(kind, value):
            raise ValueError(f"registry {kind} is off-contract: {'; '.join(errs[:3])}")
    ids = [f"{p.family}/{x['id']}" for p, _ in loaded for x in p.looks]
    if dup := {i for i in ids if ids.count(i) > 1}:
        raise ValueError(f"looks: ids {sorted(dup)} are shipped by two packs")
    node_schemas = {k: s for k, (s, _) in every.items()}
    for w in workflows:
        if w["env"] != env:
            raise ValueError(f"workflow {w['id']}: env {w['env']}, this runner is {env}")
        if errs := graph.validate(w["nodes"], node_schemas):
            raise ValueError(f"workflow {w['id']} does not fit its nodes: {'; '.join(errs[:3])}")
    return values, {k: n for k, (_, n) in every.items()}, [p for p, _ in loaded]


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


def node_schemas(values: dict[str, dict]) -> dict[str, dict]:
    """kind → node schema, from the `nodes` value."""
    return {n["kind"]: n for n in values["nodes"]["nodes"]}


async def publish(js, values: dict[str, dict]) -> None:
    """Write the values to the `registry` KV bucket."""
    kv = await js.key_value("registry")
    for kind, value in values.items():
        await kv.put(f"{HOST}.{ENV}.{kind}", json.dumps(value).encode())
    log.info("registry published: %s.%s.{%s}", HOST, ENV, ",".join(values))
