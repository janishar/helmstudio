"""Custom nodes (plan 16.5, contracts/README.md "Custom nodes"): a pack in packs/ registers runnables under
`<pack>/<node>` with a typed pydantic input and output. They run in this service as orchestration only: plain
Python on state and the app's services, no model loaded here, no network (`network=True` is refused at load until
such a node goes through the remote queue). `registry()` is the list published at start to the `registry` bucket, key
`pipelines.engine.custom`; `version` is the first 12 hex of the sha256 of the pack's code."""

import hashlib
import importlib
import json
import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from pathlib import Path

from pydantic import BaseModel

import config
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger(__name__)

PACKS = Path(__file__).resolve().parent / "packs"
KEY = "pipelines.engine.custom"


@dataclass
class Custom:
    name: str
    title: str
    help: str
    input: type[BaseModel]
    output: type[BaseModel]
    run: Callable[[BaseModel, object], Awaitable[BaseModel]]  # (input, Context) -> output; or a compiled subgraph
    network: bool = False
    version: str = ""


def _fields(model: type[BaseModel]) -> dict:
    """The model's JSON schema with refs inlined and titles dropped (registry `customFields`)."""
    s = model.model_json_schema()
    defs = s.pop("$defs", {})

    def walk(v):
        if isinstance(v, dict):
            if "$ref" in v:
                return walk(defs[v["$ref"].rsplit("/", 1)[1]])
            return {k: walk(x) for k, x in v.items() if k != "title"}
        return [walk(x) for x in v] if isinstance(v, list) else v

    return walk(s)


def load() -> dict[str, Custom]:
    """Every pack's nodes by name, each with its pack's version."""
    out = {}
    for f in sorted(p for p in PACKS.glob("*.py") if p.stem != "__init__"):
        version = hashlib.sha256(f.read_bytes()).hexdigest()[:12]
        for node in importlib.import_module(f"packs.{f.stem}").NODES:
            if node.network:
                # ponytail: a network node would bypass the remote queue (in-flight limit, caps, Pause and Stop);
                # refused until it is routed through remote.py
                raise ValueError(f"custom node {node.name}: network nodes are not supported yet")
            node.version = version
            out[node.name] = node
    return out


NODES = load()


def entry(c: Custom) -> dict:
    return {"name": c.name, "title": c.title, "help": c.help, "input": _fields(c.input),
            "output": _fields(c.output), "network": c.network, "version": c.version}  # fmt: skip


def registry() -> list[dict]:
    return [entry(c) for c in NODES.values()]


def value() -> dict:
    """The registry key's value. ValueError when off-contract."""
    v = {"schema_version": 1, "host": "pipelines", "env": "engine", "published_at": now(),
         "published_by": f"pipelines {config.VERSION}", "nodes": registry()}  # fmt: skip
    if errs := schemas.registry_errors("custom", v):
        raise ValueError(f"{KEY} is off-contract: {'; '.join(errs[:3])}")
    return v


async def publish(js) -> None:
    kv = await js.key_value("registry")
    await kv.put(KEY, json.dumps(value()).encode())
    log.info("registry published: %s (%s)", KEY, ", ".join(NODES))


async def run(name: str, values: dict, ctx) -> tuple[dict | None, str | None]:
    """Validate the input, await the node, validate its output: (output fields, None) or (None, error)."""
    c = NODES.get(name)
    if c is None:
        return None, f"custom node {name} is not registered"
    try:
        inp = c.input.model_validate(values)
    except ValueError as e:
        return None, f"invalid input: {str(e).splitlines()[0]}"[:2000]
    try:
        got = await (c.run.ainvoke(inp.model_dump()) if hasattr(c.run, "ainvoke") else c.run(inp, ctx))
        out = c.output.model_validate(got if isinstance(got, dict) else got.model_dump())
    except Exception as e:  # noqa: BLE001 - the pack's own code failed: an item failure, never the service
        return None, f"{type(e).__name__}: {e}"[:2000]
    return out.model_dump(), None
