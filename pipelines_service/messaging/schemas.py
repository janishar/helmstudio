"""JSON Schema validation against the contracts copied into gen/ (`make gen`): incoming commands and events,
outgoing events and the studio.runs.create request and reply, so nothing off-contract crosses the boundary."""

import functools
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

GEN = Path(__file__).resolve().parent.parent / "gen"
BASE = "https://helmstudio.local/contracts/messages/"


@functools.cache
def _registry() -> Registry:
    """The message schemas from gen/, loaded once."""
    resources = []
    for p in GEN.glob("messages/*.schema.json"):
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


def message_errors(msg) -> list[str]:
    """Errors of an enveloped message against messages/<type>.schema.json ([] = valid). Pipeline messages
    carry no object keys, so the owner-prefix rule has nothing to check."""
    t = msg.get("type") if isinstance(msg, dict) else None
    if not isinstance(t, str) or not (GEN / "messages" / f"{t}.schema.json").is_file():
        return [f"unknown message type {t!r}"]
    return errors(f"{BASE}{t}.schema.json", msg)


def runs_create_errors(part: str, value) -> list[str]:
    """Errors of a studio.runs.create `request` or `reply`."""
    return errors(f"{BASE}req.studio.runs.create.schema.json#/$defs/{part}", value)


def state_get_errors(part: str, value) -> list[str]:
    """Errors of a pipelines.state.get `request` or `reply`."""
    return errors(f"{BASE}req.pipelines.state.get.schema.json#/$defs/{part}", value)
