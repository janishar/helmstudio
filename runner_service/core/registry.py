"""This runner's part of KV `registry`: keys <host>.<env>.nodes, .workflows and .models, written once at start.
The values come from the pack with this runner's published_at and published_by; each is checked against
registry.schema.json, and every node kind it lists must have an implementation, so a bad value stops the runner
instead of reaching studio."""

import json
import logging

from config import ENV, HOST, PUBLISHED_BY
from core import nodes
from core.graph import Node
from messaging import schemas
from messaging.publisher import now
from packs import echo

log = logging.getLogger(__name__)

KINDS = ("nodes", "workflows", "models")
IMPLS: dict[str, Node] = nodes.NODES | echo.NODES


def values() -> dict[str, dict]:
    """kind → registry value. ValueError for one that is off-contract or lists a node kind with no
    implementation."""
    meta = {"host": HOST, "env": ENV, "published_at": now(), "published_by": PUBLISHED_BY}
    out = {kind: echo.registry_value(kind) | meta for kind in KINDS}
    for kind, value in out.items():
        if errs := schemas.registry_errors(kind, value):
            raise ValueError(f"registry {kind} is off-contract: {'; '.join(errs[:3])}")
    if missing := {n["kind"] for n in out["nodes"]["nodes"]} - IMPLS.keys():
        raise ValueError(f"node kinds with no implementation: {sorted(missing)}")
    return out


def node_schemas(values: dict[str, dict]) -> dict[str, dict]:
    """kind → node schema, from the `nodes` value."""
    return {n["kind"]: n for n in values["nodes"]["nodes"]}


async def publish(js, values: dict[str, dict]) -> None:
    """Write the values to the `registry` KV bucket."""
    kv = await js.key_value("registry")
    for kind, value in values.items():
        await kv.put(f"{HOST}.{ENV}.{kind}", json.dumps(value).encode())
    log.info("registry published: %s.%s.{%s}", HOST, ENV, ",".join(values))
