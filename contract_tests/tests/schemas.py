"""Optional response-shape check: with CONTRACT_SCHEMAS=1 every JSON response is validated against the schema that
contracts/openapi.yaml (OpenAPI 3.1, JSON Schema 2020-12) declares for its path, method and status. A response whose
operation or status isn't in the spec fails: an undocumented route or status code is a contract break too. Off by default."""

import os
import re
from functools import cache
from pathlib import Path

import httpx
import yaml
from jsonschema import Draft202012Validator
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

ENABLED = os.environ.get("CONTRACT_SCHEMAS") == "1"
SPEC = Path(
    os.environ.get("OPENAPI_PATH", Path(__file__).parents[2] / "contracts" / "openapi.yaml")
)
URI = "urn:ora:openapi"


@cache
def _spec() -> tuple[dict, Registry, list[tuple[re.Pattern, str]]]:
    if not SPEC.is_file():
        raise RuntimeError(f"CONTRACT_SCHEMAS=1 but {SPEC} does not exist (set OPENAPI_PATH)")
    doc = yaml.safe_load(SPEC.read_text())
    registry = Registry().with_resource(
        URI, Resource.from_contents(doc, default_specification=DRAFT202012)
    )
    # literal paths before templated ones: /v1/catalog/sessions/{session_id} must not swallow a literal sibling
    templates = sorted(doc.get("paths", {}), key=lambda p: (p.count("{"), -len(p)))
    # {path} (/files/{path}) is an object key and spans slashes; every other parameter is one segment
    patterns = [
        (
            re.compile(
                "^"
                + re.sub(
                    r"\\\{[^/]+?\\\}",
                    lambda m: ".+" if m[0] == r"\{path\}" else "[^/]+",
                    re.escape(t),
                )
                + "$"
            ),
            t,
        )
        for t in templates
    ]
    return doc, registry, patterns


def _escape(s: str) -> str:
    return s.replace("~", "~0").replace("/", "~1")


def check(response: httpx.Response) -> None:
    doc, registry, patterns = _spec()
    template = next((t for p, t in patterns if p.match(response.request.url.path)), None)
    method = response.request.method.lower()
    op = doc["paths"].get(template, {}).get(method) if template else None
    if op is None:
        raise AssertionError(
            f"{method.upper()} {response.request.url.path}: operation is not in the contract"
        )
    status = str(response.status_code)
    responses = op.get("responses", {})
    key = (
        status
        if status in responses
        else f"{status[0]}XX"
        if f"{status[0]}XX" in responses
        else "default"
    )
    if key not in responses:
        raise AssertionError(
            f"{method.upper()} {response.request.url.path}: status {status} is not in the contract"
        )
    resp, base = (
        responses.get(key) or {},
        "/".join(["#", "paths", _escape(template), method, "responses", key]),
    )
    if ref := resp.get("$ref"):  # a shared response: #/components/responses/<name>
        base, resp = ref, doc
        for part in ref.lstrip("#/").split("/"):
            resp = resp.get(part.replace("~1", "/").replace("~0", "~"), {})
    if "json" not in response.headers.get("content-type", "") or "application/json" not in resp.get(
        "content", {}
    ):
        return
    pointer = base + "/content/application~1json/schema"
    response.read()
    validator = Draft202012Validator({"$ref": URI + pointer}, registry=registry)
    errors = sorted(validator.iter_errors(response.json()), key=lambda e: list(e.path))
    if errors:
        raise AssertionError(
            f"{method.upper()} {response.request.url.path} {status}: response breaks the contract: "
            + "; ".join(
                f"{'/'.join(map(str, e.path)) or '<root>'}: {e.message}" for e in errors[:5]
            )
        )
