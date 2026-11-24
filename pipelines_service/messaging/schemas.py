"""JSON Schema validation against the contracts copied into gen/ (`make gen`): incoming commands and events,
outgoing events and the studio.runs.create request and reply, so nothing off-contract crosses the boundary."""

import functools
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

GEN = Path(__file__).resolve().parent.parent / "gen"
BASE = "https://helmstudio.local/contracts/messages/"
REGISTRY = "https://helmstudio.local/contracts/registry.schema.json"


@functools.cache
def _registry() -> Registry:
    """The message schemas from gen/, loaded once."""
    resources = []
    for p in [
        *GEN.glob("messages/*.schema.json"),
        GEN / "registry.schema.json",
        *GEN.glob("tools/*.json"),
        *GEN.glob("plugins/*.json"),
    ]:
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


def memory_errors(part: str, value) -> list[str]:
    """Errors of a pipelines.memory `request` or `reply`."""
    return errors(f"{BASE}req.pipelines.memory.schema.json#/$defs/{part}", value)


def tools_errors(part: str, value) -> list[str]:
    """Errors of a studio.tools `request` or `reply`."""
    return errors(f"{BASE}req.studio.tools.schema.json#/$defs/{part}", value)


def see_errors(part: str, value) -> list[str]:
    """Errors of a studio.see `request` or `reply` (API 1.65)."""
    return errors(f"{BASE}req.studio.see.schema.json#/$defs/{part}", value)


def providers_test_errors(part: str, value) -> list[str]:
    """Errors of a pipelines.providers.test `request` or `reply`."""
    return errors(f"{BASE}req.pipelines.providers.test.schema.json#/$defs/{part}", value)


def providers_key_errors(part: str, value) -> list[str]:
    """Errors of a pipelines.providers.key `request` or `reply` (API 1.51)."""
    return errors(f"{BASE}req.pipelines.providers.key.schema.json#/$defs/{part}", value)


def providers_key_write_errors(part: str, value) -> list[str]:
    """Errors of a pipelines.providers.key.write `request` or `reply` (API 1.52). The text may quote the value: never
    log it (a request holds a key)."""
    return errors(f"{BASE}req.pipelines.providers.key.write.schema.json#/$defs/{part}", value)


def judge_test_errors(part: str, value) -> list[str]:
    """Errors of a pipelines.judge.test `request` or `reply` (API 1.28)."""
    return errors(f"{BASE}req.pipelines.judge.test.schema.json#/$defs/{part}", value)


def plugin_reply_errors(value) -> list[str]:
    """Errors of a plugin tool's 200 answer against plugins/manifest.schema.json#/$defs/toolReply (API 1.61)."""
    return errors(
        "https://helmstudio.local/contracts/plugins/manifest.schema.json#/$defs/toolReply", value
    )


def tool_errors(name: str, value) -> list[str]:
    """Errors of a value against tools.schema.json#/$defs/<name> (an agent tool's args or result)."""
    return errors(f"{BASE}tools.schema.json#/$defs/{name}", value)


def registry_errors(kind: str, value) -> list[str]:
    """Errors of a registry KV value against registry.schema.json#/$defs/<kind>."""
    return errors(f"{REGISTRY}#/$defs/{kind}", value)
