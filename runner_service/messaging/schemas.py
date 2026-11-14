"""JSON Schema validation against the contracts copied into gen/ (`make gen`): incoming commands, outgoing
events and KV values are all checked, so nothing off-contract crosses the boundary."""

import functools
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

GEN = Path(__file__).resolve().parent.parent / "gen"
BASE = "https://helmstudio.local/contracts/"


@functools.cache
def _registry() -> Registry:
    """The contract schemas from gen/, loaded once."""
    resources = []
    for p in [*GEN.glob("messages/*.schema.json"), GEN / "registry.schema.json"]:
        schema = json.loads(p.read_text())
        resources.append((schema["$id"], Resource.from_contents(schema)))
    return Registry().with_resources(resources)


@functools.cache
def _validator(ref: str) -> Draft202012Validator:
    """A cached validator for one schema `$ref`, with format checks on."""
    return Draft202012Validator({"$ref": ref}, registry=_registry(), format_checker=FormatChecker())


def errors(ref: str, value) -> list[str]:
    """Schema errors of `value` as `path: message` strings; empty when valid."""
    return [
        f"{'/'.join(map(str, e.absolute_path)) or '<root>'}: {e.message}"[:300]
        for e in _validator(ref).iter_errors(value)
    ]


def foreign_keys(msg: dict) -> list[str]:
    """The claim-check rule JSON Schema can't express: every object key in `data` is under
    u/<owner_id>/."""
    prefix, out = f"u/{msg.get('owner_id')}/", []

    def walk(v, path: str) -> None:
        if isinstance(v, dict):
            for k, x in v.items():
                walk(x, f"{path}/{k}")
        elif isinstance(v, list):
            for i, x in enumerate(v):
                walk(x, f"{path}/{i}")
        elif isinstance(v, str) and v.startswith("u/") and not v.startswith(prefix):
            out.append(f"{path}: {v[:200]!r} is not under the owner's prefix {prefix}")

    walk(msg.get("data"), "data")
    return out


def message_errors(msg) -> list[str]:
    """Errors of an enveloped message against messages/<type>.schema.json and the owner-prefix
    rule ([] = valid)."""
    t = msg.get("type") if isinstance(msg, dict) else None
    if not isinstance(t, str) or not (GEN / "messages" / f"{t}.schema.json").is_file():
        return [f"unknown message type {t!r}"]
    return errors(f"{BASE}messages/{t}.schema.json", msg) or foreign_keys(msg)


def registry_errors(kind: str, value: dict) -> list[str]:
    """Errors of a KV value against registry.schema.json#/$defs/<kind> (nodes, workflows, models,
    host)."""
    return errors(f"{BASE}registry.schema.json#/$defs/{kind}", value)
