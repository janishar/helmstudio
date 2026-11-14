"""Contract self-check: every fixture against its schema, and a lint of openapi.yaml.

    python contracts/check.py

Needs jsonschema (+ rfc3339-validator for date-time), PyYAML, openapi-spec-validator. Fixtures are
fixtures/{messages,registry}/<name>.{valid,invalid}[.N].json: every valid one must pass, every invalid one must fail.
Exit code 1 on any failure."""

import json
import sys
from pathlib import Path

import yaml
from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate as validate_openapi
from referencing import Registry, Resource

HERE = Path(__file__).parent
MESSAGE_TYPES = [
    "cmd.catalog.process",
    "cmd.catalog.run",
    "evt.catalog.status",
    "evt.catalog.log",
    "evt.catalog.step",
    "evt.catalog.question",
    "evt.catalog.skus",
    "evt.catalog.image",
    "evt.catalog.run",
    "evt.auth.revoked",
]  # docs/ARCHITECTURE.md, section 6
REQ_REPLY = [
    "req.auth.validate.token",
    "req.auth.validate.role",
    "req.catalog.interpret",
    "req.catalog.poses.suggest",
]  # fixtures: <type>.{request,reply}.{valid,invalid}
REGISTRY_KEYS = ["catalog_library", "catalog_prices", "catalog_checks"]
METHODS = {"get", "post", "put", "patch", "delete"}

errors: list[str] = []


def fail(msg: str) -> None:
    errors.append(msg)
    print("FAIL", msg)


def load_schemas() -> tuple[dict[str, dict], Registry]:
    schemas = {
        p.name.removesuffix(".schema.json"): json.loads(p.read_text())
        for p in sorted((HERE / "messages").glob("*.schema.json"))
    }
    schemas["registry"] = json.loads((HERE / "registry.schema.json").read_text())
    for name, s in schemas.items():
        try:
            Draft202012Validator.check_schema(s)
        except Exception as e:  # SchemaError
            fail(f"schema {name}: {e}")
    registry = Registry().with_resources(
        (s["$id"], Resource.from_contents(s)) for s in schemas.values()
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


def check_fixtures(schemas: dict[str, dict], registry: Registry) -> None:
    def validator(schema: dict) -> Draft202012Validator:
        return Draft202012Validator(schema, registry=registry, format_checker=FormatChecker())

    reg = schemas["registry"]
    targets = {
        **{t: validator(schemas[t]) for t in MESSAGE_TYPES if t in schemas},
        **{k: validator({"$ref": f"{reg['$id']}#/$defs/{k}"}) for k in REGISTRY_KEYS},
        **{
            f"{t}.{d}": validator({"$ref": f"{schemas[t]['$id']}#/$defs/{d}"})
            for t in REQ_REPLY
            if t in schemas
            for d in ("request", "reply")
        },
    }
    for t in MESSAGE_TYPES + REQ_REPLY:
        if t not in schemas:
            fail(f"missing messages/{t}.schema.json")
    for k in REGISTRY_KEYS:
        if k not in reg["$defs"]:
            fail(f"registry.schema.json has no $defs/{k}")

    seen: dict[tuple[str, str], int] = {}
    for sub in ("messages", "registry"):
        for p in sorted((HERE / "fixtures" / sub).glob("*.json")):
            parts = p.name.removesuffix(".json").split(".")
            kind_at = next((i for i, x in enumerate(parts) if x in ("valid", "invalid")), None)
            name = ".".join(parts[:kind_at]) if kind_at else None
            if name not in targets:
                fail(f"{p.relative_to(HERE)}: no schema for {name!r}")
                continue
            kind = parts[kind_at]
            seen[name, kind] = seen.get((name, kind), 0) + 1
            doc = json.loads(p.read_text())
            errs = list(targets[name].iter_errors(doc))
            if sub == "messages" and not errs:
                errs = foreign_keys(doc)
            if kind == "valid" and errs:
                fail(f"{p.relative_to(HERE)} should pass: {errs[0].json_path}: {errs[0].message}")
            elif kind == "invalid" and not errs:
                fail(f"{p.relative_to(HERE)} should fail but passes")
            else:
                why = f"  ({errs[0].json_path}: {errs[0].message[:90]})" if errs else ""
                print(f"ok   {p.relative_to(HERE)}{why}")
    for name in targets:
        for kind in ("valid", "invalid"):
            if not seen.get((name, kind)):
                fail(f"no {kind} fixture for {name}")


def check_openapi() -> None:
    spec = yaml.safe_load((HERE / "openapi.yaml").read_text())
    try:
        validate_openapi(spec)
        print("ok   openapi.yaml is a valid OpenAPI", spec["openapi"], "document")
    except Exception as e:
        fail(f"openapi.yaml: {str(e).splitlines()[0]}")
        return
    for path, item in spec["paths"].items():
        for method, op in item.items():
            if method not in METHODS:
                continue
            where = f"{method.upper()} {path}"
            auth = op.get("tags") == ["auth"]
            if path.startswith("/auth/") != auth or auth != (
                op.get("x-status") in ("built", "deferred")
            ):
                fail(
                    f"{where}: auth routes, and only they, are tagged auth with x-status: built|deferred"
                )
            responses = op.get("responses", {})
            ok = [c for c in responses if c.startswith("2")]
            if not ok:
                fail(f"{where}: no 2xx response")
            for code in ok:
                r = responses[code]
                if code != "204" and "$ref" not in r and "content" not in r:
                    fail(f"{where} {code}: response without a schema")
            if op.get("security", spec["security"]) and "401" not in responses:
                fail(f"{where}: secured but no 401")
            for code, r in responses.items():
                if code[0] == "4" and "$ref" not in r and code != "422":
                    fail(f"{where} {code}: use a components/responses error")


def main() -> int:
    schemas, registry = load_schemas()
    check_fixtures(schemas, registry)
    check_openapi()
    print(f"\n{len(errors)} failure(s)" if errors else "\nall contract checks passed")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
