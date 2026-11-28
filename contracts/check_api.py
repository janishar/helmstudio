"""contracts/check.py, message names, fixtures, openapi.yaml, tools, presets, plugins.
Moved out of check.py as is; check.py re-exports every name."""

import ipaddress
import json
import re
from urllib.parse import urlsplit

import yaml
from jsonschema import Draft202012Validator, FormatChecker
from jsonschema.exceptions import SchemaError, ValidationError
from openapi_spec_validator import validate as validate_openapi
from referencing import Registry

from check_core import (
    FORBIDDEN,
    HERE,
    HTTP_SCHEMAS,
    MESSAGE_TYPES,
    METHODS,
    OPENAPI,
    OPERATION_PREFIX,
    REGISTRY_KEYS,
    REQ_REPLY,
    TWINS,
    fail,
    foreign_keys,
)


def check_names() -> None:
    for p in sorted(HERE.rglob("*")):
        if p.suffix in (".json", ".yaml", ".md") and not any(
            x.startswith(".venv") for x in p.parts
        ):
            for i, line in enumerate(p.read_text().splitlines(), 1):
                if FORBIDDEN.search(line):
                    fail(
                        f"{p.relative_to(HERE)}:{i}: a reference product name: {line.strip()[:80]}"
                    )


def check_fixtures(schemas: dict[str, dict], registry: Registry) -> None:
    def validator(schema: dict) -> Draft202012Validator:
        return Draft202012Validator(schema, registry=registry, format_checker=FormatChecker())

    reg = schemas["registry"]
    targets = {
        **{t: validator(schemas[t]) for t in MESSAGE_TYPES},
        **{k: validator({"$ref": f"{reg['$id']}#/$defs/{k}"}) for k in REGISTRY_KEYS},
        **{
            f"{t}.{d}": validator({"$ref": f"{schemas[t]['$id']}#/$defs/{d}"})
            for t in REQ_REPLY
            for d in ("request", "reply")
        },
        **{n: validator({"$ref": f"{OPENAPI}#/components/schemas/{n}"}) for n in HTTP_SCHEMAS},
    }
    for k in REGISTRY_KEYS:
        if k not in reg["$defs"]:
            fail(f"registry.schema.json has no $defs/{k}")

    seen: dict[tuple[str, str], int] = {}
    for sub in ("messages", "registry", "http"):
        for p in sorted((HERE / "fixtures" / sub).glob("*.json")):
            parts = p.name.removesuffix(".json").split(".")
            kind_at = next((i for i, x in enumerate(parts) if x in ("valid", "invalid")), None)
            name = ".".join(parts[:kind_at]) if kind_at else None
            if sub == "registry" and name:
                name = name.rsplit(".", 1)[-1]  # drop a family prefix
            if name not in targets:
                fail(f"{p.relative_to(HERE)}: no schema for {name!r}")
                continue
            kind = parts[kind_at]
            seen[name, kind] = seen.get((name, kind), 0) + 1
            doc = json.loads(p.read_text())
            errs = list(targets[name].iter_errors(doc))
            if sub == "messages" and not errs:
                errs = foreign_keys(doc)
            if sub == "http" and name in TWINS:
                twin = validator({"$ref": f"{schemas['assistant']['$id']}#/$defs/{TWINS[name]}"})
                if twin.is_valid(doc) != (not errs):
                    fail(f"{p.relative_to(HERE)}: openapi.yaml {name} and assistant.schema.json {TWINS[name]} disagree")
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


def openapi_inline(node, components: dict):
    """node with every #/components/schemas $ref replaced by its schema, and descriptions/examples dropped."""
    if isinstance(node, list):
        return [openapi_inline(x, components) for x in node]
    if not isinstance(node, dict):
        return node
    if "$ref" in node and node["$ref"].startswith("#/components/schemas/"):
        return openapi_inline(components[node["$ref"].rsplit("/", 1)[1]], components)
    return {k: openapi_inline(v, components) for k, v in node.items() if k not in ("description", "examples")}


def check_openapi() -> None:
    spec = yaml.safe_load((HERE / "openapi.yaml").read_text())
    # the registry's copy of StudioLayout (a template's studio block) is the API's, ParamValue aside
    registry = json.loads((HERE / "registry.schema.json").read_text())["$defs"]["studioLayout"]
    paramvalue = {"$ref": "messages/defs.schema.json#/$defs/paramValue"}
    components = spec["components"]["schemas"] | {"ParamValue": paramvalue}
    if openapi_inline(registry, {}) != openapi_inline({"$ref": "#/components/schemas/StudioLayout"}, components):
        fail("registry.schema.json $defs/studioLayout differs from openapi.yaml StudioLayout")
    else:
        print("ok   registry.schema.json studioLayout = openapi.yaml StudioLayout")
    try:
        validate_openapi(spec)
        print("ok   openapi.yaml is a valid OpenAPI", spec["openapi"], "document")
    except ValidationError as e:  # OpenAPIValidationError
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
            prefix = next((v for k, v in OPERATION_PREFIX.items() if path.startswith(k)), None)
            if not prefix or not op.get("operationId", "").startswith(prefix):
                fail(f"{where}: operationId must start with {prefix!r}")
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


# API 1.93: an assistant write with no card: create_board makes an empty board (nothing is lost or run); 1.95:
# cancel_runs stops runs (only work in flight is lost; owner, 2026-11-27: "yes build it"); 1.107: listen makes a run
# that answers (nothing is lost or made; it costs a model run, the director skill says when)
CARDLESS_WRITES = {"create_board", "cancel_runs", "listen"}
ASSISTANT_CARD_KINDS = {"delete", "plugin"}  # cards the server asks without a catalog tool: Clear all (a delete card); API 1.61 a plugin tool with a risk flag


def refs(node, out=None) -> list[str]:
    """Every $ref in a JSON tree."""
    out = [] if out is None else out
    if isinstance(node, dict):
        out += [node["$ref"]] if isinstance(node.get("$ref"), str) else []
        for v in node.values():
            refs(v, out)
    elif isinstance(node, list):
        for v in node:
            refs(v, out)
    return out


SKILL_TOOLS = ("use_skill", "read_skill_file")  # API 1.64: offered only while a skill is on
GUIDE_TOOL = "read_model_guide"  # API 1.71: offered only while some model guide is published


def offered_tools(catalog: dict, roles: list[str], web_search: bool, look_at: bool, skills: bool = False,
                  guides: bool = False, listen: bool = True) -> list[str]:
    """The catalog tools a turn offers (README, Assistant: tools): surface assistant, every role it needs held,
    web_search only when on, look_at only when an ask workflow can run now, from API 1.108 listen only when a listen
    workflow can, from API 1.64 use_skill and
    read_skill_file only while a skill is on, from API 1.71 read_model_guide only while a model guide is published.
    Studio (Go) runs the same fixtures."""
    return sorted(
        n for n, t in catalog["tools"].items()
        if "assistant" in t["surfaces"] and set(t["roles"]) <= set(roles)
        and (n != "web_search" or web_search) and (n != "look_at" or look_at) and (n not in SKILL_TOOLS or skills)
        and (n != "listen" or listen)
        and (n != GUIDE_TOOL or guides)
    )


def screen_actions(actions: dict, screen: str, roles: list[str]) -> list[str]:
    """The actions a screen registers: the shell's and its own, none for a screen whose roles are not all held."""
    own = actions["screens"].get(screen, {})
    ok = set(own.get("roles", [])) <= set(roles)
    return sorted({*actions["screens"]["shell"]["actions"], *(own.get("actions", {}) if ok else {})})


def check_tools(schemas: dict[str, dict], registry: Registry) -> None:
    """tools/catalog.json and tools/actions.json (README, Assistant: tools)."""
    shape = schemas["tools/catalog.schema.json"]["$id"]
    catalog, actions = schemas["tools/catalog.json"], schemas["tools/actions.json"]
    for doc, d in ((catalog, "toolsFile"), (actions, "actionsFile")):
        if bad := list(Draft202012Validator({"$ref": f"{shape}#/$defs/{d}"}, registry=registry).iter_errors(doc)):
            fail(f"tools: {doc['$id'].rsplit('/', 1)[1]}: {bad[0].json_path}: {bad[0].message}")
            return
    spec = yaml.safe_load((HERE / "openapi.yaml").read_text())
    ops = {f"{m.upper()} {path}" for path, item in spec["paths"].items() for m in item if m in METHODS}
    cards, tags = actions["cards"], actions["tags"]
    # the Timeline edit's ops carry each op's own fields, as openapi TimelineOp (plus studio's fill), so the model
    # never guesses field names (owner, 2026-11-25: the assistant tried take, take_id, takeId before take_ids)
    edit = actions["screens"]["timeline"]["actions"]["edit"]["input"]["properties"]["ops"]["items"]
    want = {v["title"]: (sorted(v["required"]), sorted(v["properties"]))
            for v in spec["components"]["schemas"]["TimelineOp"]["oneOf"]}
    have = {v.get("title"): (sorted(v.get("required", [])), sorted(v.get("properties", {})))
            for v in edit.get("oneOf", [])}
    have.pop("fill", None)
    if have != want:
        fail("tools/actions.json timeline edit: ops must match openapi TimelineOp's variants "
             f"(differ: {sorted(k for k in set(want) | set(have) if want.get(k) != have.get(k))}); "
             "regenerate them from TimelineOp")
    used = set()

    def schema_ok(where: str, sch: dict, model_facing: bool) -> None:
        try:
            Draft202012Validator.check_schema(sch)
        except SchemaError as e:
            fail(f"{where}: not a JSON Schema: {e.message}")
        for r in refs(sch):
            if model_facing:
                fail(f"{where}: model-facing, so self-contained: no $ref ({r})")
                continue
            try:
                registry.resolver(catalog["$id"]).lookup(r)
            except Exception as e:  # noqa: BLE001 - any resolution failure is the finding
                fail(f"{where}: $ref {r} does not resolve ({type(e).__name__})")

    for n, t in catalog["tools"].items():
        w = f"tools/catalog.json {n}"
        schema_ok(f"{w} input", t["input"], True)
        schema_ok(f"{w} output", t["output"], False)
        for r in t["routes"]:
            if r not in ops:
                fail(f"{w}: route {r!r} is not in openapi.yaml")
        if (t["served_by"] == "studio") != bool(t["routes"]):
            fail(f"{w}: a studio tool names its routes, a pipelines tool none")
        if any(r.split(" ")[1].startswith("/v1/studio/admin/") for r in t["routes"]) != ("admin" in t["roles"]):
            fail(f"{w}: an admin route needs roles [admin], and roles [admin] an admin route")
        both = "mcp" in t["surfaces"] and "assistant" in t["surfaces"]
        if not t["read_only"] and both and "mcp_description" not in t:  # plan 16 §20.8: Magic over MCP
            fail(f"{w}: a write on both surfaces says what it does over MCP, with no card: mcp_description")
        if "mcp_description" in t and not both:
            fail(f"{w}: mcp_description is for a tool on both surfaces")
        if t.get("destructive") and (t["read_only"] or "mcp" not in t["surfaces"]):
            fail(f"{w}: destructive is a write helm mcp serves (plan 16)")
        if (not t["read_only"] and "assistant" in t["surfaces"] and "card" not in t and t["served_by"] != "pipelines"
                and n not in CARDLESS_WRITES):
            fail(f"{w}: an assistant write needs a card (only memory writes and {sorted(CARDLESS_WRITES)} go without)")
        if "card" in t:
            used.add(t["card"])
            if t["card"] not in cards:
                fail(f"{w}: no card {t['card']!r} in actions.json")
    union = sorted(o["properties"]["op"]["const"] for o in spec["components"]["schemas"]["TimelineOp"]["oneOf"])
    for n in ("try_timeline_edit", "edit_timeline"):  # 1.49: the ops written out are openapi's TimelineOp
        if n in catalog["tools"]:
            ops = catalog["tools"][n]["input"]["properties"]["ops"]["items"]["oneOf"]
            if sorted(o["properties"]["op"]["const"] for o in ops) != union:
                fail(f"tools/catalog.json {n}: ops must be openapi TimelineOp's: {union}")
            if any(not o.get("description") for o in ops):
                fail(f"tools/catalog.json {n}: every op needs a one-line description")
    # 1.87: Magic's edit tools write out openapi MagicOp's variants (each op's required fields and fields)
    magic = {v["properties"]["op"]["const"]: (sorted(v["required"]), sorted(v["properties"]))
             for v in spec["components"]["schemas"]["MagicOp"]["oneOf"]}
    for n in ("try_board_edit", "edit_board"):
        if n in catalog["tools"]:
            ops = catalog["tools"][n]["input"]["properties"]["ops"]["items"]["oneOf"]
            have = {o["properties"]["op"]["const"]: (sorted(o.get("required", [])), sorted(o["properties"])) for o in ops}
            if have != magic:
                fail(f"tools/catalog.json {n}: ops must be openapi MagicOp's variants "
                     f"(differ: {sorted(k for k in set(magic) | set(have) if magic.get(k) != have.get(k))})")
            if any(not o.get("description") for o in ops):
                fail(f"tools/catalog.json {n}: every op needs a one-line description")
    # 1.93: every copy of the standard request's description is one text, and it names a dialogue's turns, Run on
    # (host) and loop, so the model never applies Create's "@name: text" script to Magic (owner, 2026-11-27)
    texts = set()

    def requests(node) -> None:
        if isinstance(node, dict):
            r = node.get("request")
            if isinstance(r, dict) and r.get("type") == "object" and "standard request" in r.get("description", ""):
                texts.add(r["description"])
            for v in node.values():
                requests(v)
        elif isinstance(node, list):
            for v in node:
                requests(v)

    requests({n: catalog["tools"][n] for n in ("try_board_edit", "edit_board") if n in catalog["tools"]})
    if len(texts) != 1 or not all(w in next(iter(texts)) for w in ("turns [{speaker", "host (Run on", "loop (")):
        fail("tools/catalog.json: the Magic request's descriptions must be one text naming turns, host and loop")
    shell = set(actions["screens"]["shell"]["actions"])
    to = set(actions["screens"]["shell"]["actions"]["navigate"]["input"]["properties"]["to"]["enum"])
    for sn, sc in actions["screens"].items():
        for r in sc["routes"]:
            if r != "*" and r not in to:
                fail(f"tools/actions.json {sn}: route {r} is not a navigate target")
        for an, a in sc["actions"].items():
            w = f"tools/actions.json {sn}.{an}"
            schema_ok(f"{w} input", a["input"], True)
            if an in catalog["tools"] and "assistant" in catalog["tools"][an]["surfaces"]:
                fail(f"{w}: named like an assistant tool")  # an mcp-only tool (plan 16's pick) is another namespace
            if sn != "shell" and an in shell:
                fail(f"{w}: named like a shell action")
            if "card" in a:
                used.add(a["card"])
                if a["card"] not in cards:
                    fail(f"{w}: no card {a['card']!r}")
    for k, c in cards.items():
        if k not in used:
            fail(f"tools/actions.json card {k}: no action or tool asks it")
        for g in c["tags"]:
            if g not in tags:
                fail(f"tools/actions.json card {k}: no tag {g!r}")
    server = {t["card"] for t in catalog["tools"].values() if "card" in t} | ASSISTANT_CARD_KINDS
    if set(schemas["assistant"]["$defs"]["card"]["properties"]["kind"]["enum"]) != server:
        fail(f"assistant.schema.json card.kind must be the catalog tools' cards and Clear all's: {sorted(server)}")
    studio = sorted(n for n, t in catalog["tools"].items() if "assistant" in t["surfaces"] and t["served_by"] == "studio")
    req = schemas["req.studio.tools"]["$defs"]
    for d, part in (("assistant", "input"), ("assistantOk", "output")):
        if sorted(req[d]["properties"]["tool"]["enum"]) != studio:
            fail(f"req.studio.tools $defs/{d}: tool must be the catalog's assistant tools served by studio: {studio}")
        key = "args" if part == "input" else "result"
        got = {c["if"]["properties"]["tool"]["const"]: c["then"]["properties"][key]["$ref"] for c in req[d]["allOf"]}
        want = {n: f"../tools/catalog.json#/tools/{n}/{part}" for n in studio}
        if got != want:
            fail(f"req.studio.tools $defs/{d}: each tool's {key} must $ref its catalog {part}")
    fx = json.loads((HERE / "fixtures" / "tools" / "offered.json").read_text())
    for c in fx["cases"]:
        got = (offered_tools(catalog, c["roles"], c.get("web_search", False), c.get("look_at", False), c.get("skills", False),
                             c.get("guides", False), c.get("listen", True))
               if "screen" not in c else screen_actions(actions, c["screen"], c["roles"]))
        if got != c["expect"]:
            fail(f"fixtures/tools/offered.json {c['description']!r}: got {got}, want {c['expect']}")
    print("ok   tools/catalog.json, tools/actions.json, fixtures/tools/offered.json")


THINKING_LEVELS = ("off", "low", "medium", "high")


def thinking_problems(doc: dict, shape: str, registry: Registry) -> list[str]:
    """API 1.53 (plan 28): a presets file's thinking rules, schema then what a schema cannot say: each match compiles
    (and stays RE2 and JavaScript alike: no lookaround or backreferences), default is one of the levels, levels are
    off + on or some of off, low, medium, high, a thinking.budget_tokens stays below thinking_tokens of its level."""
    if bad := list(Draft202012Validator({"$ref": shape}, registry=registry).iter_errors(doc)):
        return [f"{bad[0].json_path}: {bad[0].message}"]
    out, tokens = [], doc["thinking_tokens"]
    for p in doc["presets"]:
        if v := p.get("vision"):  # API 1.65: a regular expression on the model id, as a thinking match
            try:
                re.compile(v)
            except re.error as e:
                out.append(f"{p['id']} vision: does not compile ({e})")
            if re.search(r"\(\?[=!<]|\\[1-9]", v):
                out.append(f"{p['id']} vision: uses lookaround or a backreference")
        rules = p.get("thinking")
        for i, r in enumerate(rules if isinstance(rules, list) else []):
            w = f"{p['id']} thinking[{i}]"
            try:
                re.compile(r["match"])
            except re.error as e:
                out.append(f"{w}: match does not compile ({e})")
            if re.search(r"\(\?[=!<]|\\[1-9]", r["match"]):
                out.append(f"{w}: match uses lookaround or a backreference")
            levels = set(r.get("levels", {}))
            if "on" in levels and levels != {"off", "on"}:
                out.append(f"{w}: levels are off and on, or some of off, low, medium, high")
            if r.get("default") and r["default"] not in levels:
                out.append(f"{w}: default {r['default']} is not one of its levels")
            for lv, frag in r.get("levels", {}).items():
                budget = (frag.get("thinking") or {}).get("budget_tokens")
                if budget is not None and (lv == "off" or budget >= tokens[lv]):
                    out.append(f"{w}: {lv}'s budget_tokens {budget} is not below thinking_tokens {lv}")
    return out


def check_presets(schemas: dict[str, dict], registry: Registry) -> None:
    """providers/presets.json (README, Model sources: presets): its schema, then what a schema cannot say."""
    doc = json.loads((HERE / "providers" / "presets.json").read_text())
    shape = schemas["providers/presets.schema.json"]["$id"]
    if bad := thinking_problems(doc, shape, registry):
        fail(f"providers/presets.json: {bad[0]}")
        return
    # each thinking check catches its case (a bad file is refused, never only the real file's lucky shape)
    rule = lambda r: {"thinking_tokens": doc["thinking_tokens"], "presets": [doc["presets"][0] | {"thinking": [r]}]}
    lv = {"off": {"reasoning_effort": "none"}, "high": {"reasoning_effort": "high"}}
    for why, bad_doc in (
        ("a fragment key outside the four", rule({"match": "^a", "levels": lv | {"low": {"temperature": 1}}})),
        ("a bad level", rule({"match": "^a", "levels": lv | {"max": {"reasoning_effort": "max"}}})),
        ("on with low", rule({"match": "^a", "levels": lv | {"on": {"reasoning_effort": "low"}}})),
        ("a match that does not compile", rule({"match": "^(a", "levels": lv})),
        ("a lookahead", rule({"match": "^a(?!b)", "levels": lv})),
        ("a default not in levels", rule({"match": "^a", "default": "low", "levels": lv})),
        ("always with levels", rule({"match": "^a", "always": True, "levels": lv})),
        ("a budget above thinking_tokens",
         rule({"match": "^a", "levels": lv | {"high": {"thinking": {"type": "enabled", "budget_tokens": 9000}}}})),
    ):  # fmt: skip
        if not thinking_problems(bad_doc, shape, registry):
            fail(f"check.py: thinking rules: {why} passes")
    put, item = (Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/{n}"}, registry=registry)
                 for n in ("ProviderPut", "ProviderPreset"))  # fmt: skip
    # API 1.55: compat.headers takes only allow-listed safe names (never auth, cookies or the host)
    base = {"kind": "remote", "base_url": "https://api.example.com/v1", "models": ["m"], "key_secret": "x"}
    if not list(put.iter_errors(base | {"compat": {"headers": {"anthropic-version": "2023-06-01"}}})):
        for h in ("authorization", "Authorization", "x-api-key", "cookie", "host", "x-anything"):
            if not list(put.iter_errors(base | {"compat": {"headers": {h: "v"}}})):
                fail(f"check.py: compat.headers: {h} passes")
        if not list(put.iter_errors(base | {"compat": {"headers": {"anthropic-version": "a\nb"}}})):
            fail("check.py: compat.headers: a value with a newline passes")
    else:
        fail("check.py: compat.headers: anthropic-version is refused")
    seen: set[str] = set()
    for p in doc["presets"]:
        w = f"providers/presets.json {p['id']}"
        if p["id"] in seen:
            fail(f"{w}: the id is used twice")
        seen.add(p["id"])
        host = urlsplit(p["base_url"]).hostname or ""
        try:
            ip = ipaddress.ip_address(host)
        except ValueError:
            ip = None
        if p["kind"] == "remote":
            if not p["base_url"].startswith("https://") or host == "localhost" or (
                ip and (ip.is_private or ip.is_loopback or ip.is_link_local)
            ):
                fail(f"{w}: a remote preset is https to a public host")
        elif not (host == "host.docker.internal" or (ip and (ip.is_loopback or ip.is_private) and not ip.is_link_local)):
            fail(f"{w}: a server preset's host is host.docker.internal, a loopback or a private address")
        body = {"kind": p["kind"], "base_url": p["base_url"], "models": p["models"] or ["placeholder"],
                "preset": p["id"]} | {k: p[k] for k in ("key_secret", "compat") if k in p}  # fmt: skip
        for v, what in ((put, "as a ProviderPut body"), (item, "as openapi ProviderPreset")):
            if bad := list(v.iter_errors(body if v is put else p)):
                fail(f"{w}: {what}: {bad[0].json_path}: {bad[0].message}")
    print(f"ok   providers/presets.json ({len(seen)} presets, thinking rules)")


def check_plugins(schemas: dict[str, dict], registry: Registry) -> None:
    """API 1.61 (README, Plugins): every plugins/*/plugin.json in the repo (and the test stack's fake plugins) passes
    the manifest schema, names its folder, keeps url on a local server's host, and each tool's input is a
    self-contained JSON Schema of an object."""
    v = Draft202012Validator({"$ref": schemas["plugins/manifest.schema.json"]["$id"]}, registry=registry)
    root = HERE.parent
    found = sorted([*root.glob("plugins/*/plugin.json"), *root.glob("contract_tests/fake_plugins/*/plugin.json"),
                    *root.glob("contract_tests/fake_helm/plugins/*/plugin.json")])
    for p in found:
        w, doc = str(p.relative_to(root)), json.loads(p.read_text())
        if doc.get("kind") == "skill" and doc.get("format", 1) > 1:
            continue  # API 1.64: a newer format (the test stack's future-skill) is listed needs_newer, never read
        if bad := list(v.iter_errors(doc)):
            fail(f"{w}: {bad[0].json_path}: {bad[0].message}")
            continue
        if doc["name"] != p.parent.name:
            fail(f"{w}: name {doc['name']!r} is not its folder's")
        if doc["kind"] == "skill":  # API 1.64: SKILL.md and at most 16 small text files beside the manifest
            extra = sorted(f.name for f in p.parent.iterdir() if f.name not in ("plugin.json", "SKILL.md"))
            if not (p.parent / "SKILL.md").is_file() or len(extra) > 15:
                fail(f"{w}: a skill has SKILL.md and at most 15 more files")
            if bad := [f for f in [*extra, "SKILL.md"] if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}\.(md|txt|json)", f)
                       or (p.parent / f).stat().st_size > 65536]:
                fail(f"{w}: skill files are .md, .txt or .json under 64 KB with plain names: {bad}")
            continue
        cmd0 = (doc.get("run") or {}).get("cmd", ["x"])[0]  # API 1.67: inside the folder or a program on PATH
        if cmd0.startswith("/") or ".." in cmd0.split("/"):
            fail(f"{w}: run.cmd starts inside the plugin's folder (./x) or names a program, never / or ..")
        host = urlsplit(doc["url"]).hostname or ""
        try:
            ip = ipaddress.ip_address(host)
        except ValueError:
            ip = None
        if ip and (ip.is_link_local or not (ip.is_loopback or ip.is_private)):
            fail(f"{w}: url is a local server's: host.docker.internal, a name, a loopback or a private address")
        for t in doc.get("tools", []):
            try:
                Draft202012Validator.check_schema(t["input"])
            except SchemaError as e:
                fail(f"{w} {t['name']}: input is not a JSON Schema: {e.message}")
            if refs(t["input"]):
                fail(f"{w} {t['name']}: input is model-facing, so self-contained: no $ref")
    if not found:
        fail("plugins/: no plugin.json found")
    # a plugin's preset (presets.json `plugin`) names a model-provider plugin, and its rules cover every model of it
    manifests = {p.parent.name: json.loads(p.read_text()) for p in found}
    for pre in json.loads((HERE / "providers" / "presets.json").read_text())["presets"]:
        if "plugin" not in pre:
            continue
        m = manifests.get(pre["plugin"])
        if not m or m["kind"] != "model-provider":
            fail(f"providers/presets.json {pre['id']}: plugin {pre['plugin']} is not a model-provider plugin")
            continue
        rules = pre.get("thinking") if isinstance(pre.get("thinking"), list) else []
        for model in m["provider"]["models"]:
            if not any(re.search(r["match"], model) for r in rules):
                fail(f"providers/presets.json {pre['id']}: no thinking rule matches {pre['plugin']}'s {model}")
    print(f"ok   plugins ({len(found)} manifests)")
