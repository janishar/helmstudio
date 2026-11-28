"""contracts/check.py, mention rules (README, "Mentions").
Moved out of check.py as is; check.py re-exports every name."""

import json
import math
import re

from jsonschema import Draft202012Validator
from referencing import Registry

from check_core import HERE, OPENAPI, fail, image_field, lora_field
from check_graph import family, spoken

# ---------------------------------------------------------------- mention rules (README, "Mentions")
HANDLE = re.compile(r"^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$")  # an element's handle
WORD = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_")  # ASCII only: bytes, runes, UTF-16 agree


def scan(text: str) -> list[dict]:
    """The prompt as parts: {text} (raw, `@@` kept) and {mention: handle, raw}; the raw parts join to the text."""
    parts, buf, i = [], "", 0
    while i < len(text):
        if text.startswith("@@", i):
            buf, i = buf + "@@", i + 2
            continue
        if text[i] == "@" and (i == 0 or text[i - 1] not in WORD):
            j = i + 1
            while j < len(text) and (text[j] in WORD or text[j] == "-"):
                j += 1
            tok = text[i + 1:j].rstrip("-_")
            if tok and tok[0].isascii() and tok[0].isalpha():
                if buf:
                    parts.append({"text": buf})
                parts.append({"mention": tok.lower(), "raw": "@" + tok})
                buf, i = "", i + 1 + len(tok)
                continue
        buf, i = buf + text[i], i + 1
    return parts + ([{"text": buf}] if buf else [])


def excluded(n: dict, p: dict) -> bool:
    """A rows param that must stay empty in this graph: an input its `not_with` names is wired (API 1.22)."""
    return any(i in n.get("in", {}) for i in p.get("not_with", []))


def reference_rows(nodes: dict, palette: dict) -> tuple[str, dict] | None:
    """Where element images go: the first (node id, then schema order) rows param with a `{n}` mention and one
    Image field, on a known, not bypassed node, not excluded by a wired `not_with` input; None = the family takes
    text only."""
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            if p["type"] == "rows" and "{n}" in p.get("mention", "") and image_field(p) and p["name"] not in n.get("in", {}) \
                    and not excluded(n, p):
                return nid, p
    return None


def auto_size(width: int, height: int) -> tuple[int, int]:
    """Canvas `auto` with references: about 1 MP at the aspect of width x height, sides rounded (half to even, as
    Python's round) to 32, as the runner sizes it."""
    ratio = width / height
    w = math.sqrt(1024 * 1024 * ratio)
    return round(w / 32) * 32, round(w / ratio / 32) * 32


def pin_canvases(nodes: dict, palette: dict, before: list, field: str, sizes: dict, changed: dict, typed: dict) -> None:
    """Mentions step 2, Canvas: element rows must not change the output size. Each `canvas` on `auto` is pinned to
    `fixed` at the size auto gives without them: its own width x height without user rows, else the auto size of the
    last user row's take (clamped to the params' min and max; unknown size: mode only). A `minimax-h3-image/canvas`
    (auto follows the first row) is pinned only without user rows, its width x height kept."""
    last = before[-1].get(field) if before else None
    size = sizes.get(last["take_id"]) if isinstance(last, dict) else None
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        first = n["kind"] == "minimax-h3-image/canvas"
        if n["kind"] != "canvas" and not first or not s or n.get("bypass") or first and before:
            continue
        ps = {p["name"]: p for p in s["params"]}
        val = {name: n.get("params", {}).get(name, p["default"]) for name, p in ps.items()}
        if val["mode"] != "auto":
            continue
        changed[f"{nid}.mode"], typed[f"{nid}.mode"] = "fixed", "auto"
        for name, v in zip(("width", "height"), auto_size(*size) if size else ()):
            v = min(max(v, ps[name].get("min", v)), ps[name].get("max", v))
            if v != val[name]:
                changed[f"{nid}.{name}"], typed[f"{nid}.{name}"] = v, val[name]


def prompt_params(nodes: dict, palette: dict) -> list[tuple[str, dict]]:
    """Mentions "Where": every unwired `text` param of a known, not bypassed node, node id order then schema order."""
    return [(nid, p) for nid in sorted(nodes) if (s := palette.get(nodes[nid]["kind"])) and not nodes[nid].get("bypass")
            for p in s["params"] if p["type"] == "text" and p["name"] not in nodes[nid].get("in", {})]


def look_stack(nodes: dict, palette: dict) -> tuple[str, dict] | None:
    """Where look LoRAs go (README, Looks): among the known, not bypassed `<family>/lora-stack` nodes with an unwired
    rows param that has a Lora field, the first by node id from which no other such node is reachable downstream."""
    cands = {}
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        if s and not n.get("bypass") and n["kind"].endswith("/lora-stack"):
            if p := next((p for p in s["params"] if lora_field(p) and p["name"] not in n.get("in", {})), None):
                cands[nid] = p
    succ = {nid: {m for m, x in nodes.items() for ref in x.get("in", {}).values() if ref.split(".")[0] == nid}
            for nid in nodes}

    def down(nid: str) -> set[str]:
        seen, todo = set(), list(succ[nid])
        while todo:
            if (x := todo.pop()) not in seen:
                seen.add(x)
                todo += succ.get(x, ())
        return seen

    return next(((nid, p) for nid, p in cands.items() if not down(nid) & set(cands)), None)


def look_needs(item: dict) -> list[str]:
    """LibraryItem.needs: text (non-empty), images (at least one), lora (at least one), in that order."""
    return [k for k, v in (("text", item["text"]), ("images", item["images"]), ("lora", item.get("loras"))) if v]


def look_fit(nodes: dict, palette: dict, item: dict, loras: dict[str, str]) -> str | None:
    """Why the item cannot be applied to this graph (README, Looks: fit), None when it can. `loras`: LoRA id → detected
    family of every LoRA the caller can use now in the workflow's env."""
    needs = look_needs(item)
    if spoken(nodes, palette):  # API 1.30: spoken text is never expanded (README, Voices); 1.79: class voice
        return "mentions are not read aloud; use a dialogue script" if item.get("kind") == "element" \
            else "looks do not apply to speech"
    if ("text" in needs or item.get("kind") == "element") and not prompt_params(nodes, palette):
        return "this workflow has no prompt"
    if item.get("kind") == "element":
        return None  # an element's images are dropped where there are no reference rows (Mentions)
    if "images" in needs and not reference_rows(nodes, palette):
        return "this workflow takes no reference images"
    if "lora" in needs:
        if not (stack := look_stack(nodes, palette)):
            return "this workflow has no LoRA stack"
        for x in item["loras"]:
            fam = loras.get(x["lora_id"]) if x["lora_id"] else None
            if x["lora_id"] is None:
                return f"LoRA {x['name']} is on no live host"
            if fam is None:
                return f"LoRA {x['name']} is not available"
            if fam == "unknown":
                return f"LoRA {x['name']} fits no known model"
            if fam != family(nodes[stack[0]]["kind"]):
                return f"LoRA {x['name']} is for {fam}"
    return None


def resolve_mentions(nodes: dict, palette: dict, library: list[dict], sizes: dict | None = None,
                     looks: list[dict] | None = None, chosen: list[str] | None = None,
                     loras: dict[str, str] | None = None) -> dict:
    """Expand every mention and apply the chosen looks (README, Mentions and Looks) on a graph whose params hold the
    merged values (defaults, workflow, preset, inputs). `sizes`: take id → [width, height] of the user's own row
    takes; `looks`: the looks the caller can use now; `chosen`: RunRequest.looks; `loras`: as look_fit's.
    Answers {inputs: changed path → resolved value, typed: changed path → value as set, elements, looks, errors}."""
    lib, found, chosen = {e["handle"]: e for e in library}, {x["id"]: x for x in looks or []}, chosen or []
    val = lambda nid, p: nodes[nid].get("params", {}).get(p["name"], p["default"])
    prompts = [(nid, f"{nid}.{p['name']}", p, scan(val(nid, p))) for nid, p in prompt_params(nodes, palette)]
    mentioned = lambda parts: list(dict.fromkeys(x["mention"] for x in parts if "mention" in x))
    first = {}  # look id → its first place in the request, from 1
    for i, lid in enumerate(chosen, 1):
        first.setdefault(lid, i)
    if spoken(nodes, palette):  # API 1.30: nothing is expanded; a mention is refused (README, Voices); 1.79: class voice
        errors = [f"{path}: mentions are not read aloud; use a dialogue script" for _, path, _, parts in prompts
                  if mentioned(parts)]
        prompts = []
    else:
        errors = [f"{path}: unknown element @{h}" for _, path, _, parts in prompts for h in mentioned(parts)
                  if h not in lib]
    errors += [f"looks[{i}]: unknown look {lid}" for lid, i in first.items() if lid not in found]
    if errors:
        return {"errors": errors}
    picked = [found[lid] for lid in first]
    errors = [f"looks[{first[x['id']]}]: look {x['name']} does not fit this workflow ({r})" for x in picked
              if (r := look_fit(nodes, palette, x | {"kind": "look"}, loras or {}))]
    if errors:
        return {"errors": errors}
    used = list(dict.fromkeys(h for *_, parts in prompts for h in mentioned(parts)))
    adds = [(f"@{h}", ("e", h), lib[h]["images"]) for h in used] + [(f"look {x['name']}", ("l", x["id"]), x["images"])
                                                                     for x in picked]
    tokens, changed, typed = {k: [] for _, k, _ in adds}, {}, {}
    if (target := reference_rows(nodes, palette)) and adds:
        tid, tp = target
        field, cap, path = image_field(tp), tp.get("max", 100), f"{tid}.{tp['name']}"
        rows = list(val(tid, tp))
        at = {}  # take id → its row number (the user's rows first)
        for i, r in enumerate(rows, 1):
            if isinstance(r.get(field), dict):
                at.setdefault(r[field]["take_id"], i)
        over, full = [], None
        for label, key, images in adds:
            new = [t for t in dict.fromkeys(images) if t not in at]
            if full is not None or len(rows) + len(new) > cap:
                full = len(rows) if full is None else full
                over += [f"{label} +{len(new)}"] if new else []
                continue
            for t in new:
                rows.append({field: {"take_id": t}})
                at[t] = len(rows)
            tokens[key] = [tp["mention"].replace("{n}", str(at[t])) for t in images]
        if over:
            return {"errors": [f"{path}: at most {cap} images; {full} used, {', '.join(over)} do not fit"]}
        if len(rows) > len(val(tid, tp)):
            changed[path], typed[path] = rows, val(tid, tp)
            pin_canvases(nodes, palette, val(tid, tp), field, sizes or {}, changed, typed)
    if any(x["loras"] for x in picked) and (stack := look_stack(nodes, palette)):
        sid, sp = stack
        field, cap, path, before = lora_field(sp), sp.get("max", 100), f"{sid}.{sp['name']}", val(sid, stack[1])
        present = {r[f]["lora_id"] for nid in nodes if (s := palette.get(nodes[nid]["kind"])) and not nodes[nid].get("bypass")
                   for p in s["params"] if (f := lora_field(p))
                   for r in val(nid, p) if isinstance(r.get(f), dict)}  # the user's rows win
        st = sp["item"].get("strength")
        clamp = lambda v: min(max(v, st.get("min", v)), st.get("max", v)) if isinstance(st, dict) else v
        rows, over, full = list(before), [], None
        for x in picked:
            new = []
            for y in x["loras"]:
                if y["lora_id"] not in present and all(y["lora_id"] != z["lora_id"] for z in new):
                    new.append(y)
            if full is not None or len(rows) + len(new) > cap:
                full = len(rows) if full is None else full
                over += [f"look {x['name']} +{len(new)}"] if new else []
                continue
            for y in new:
                rows.append({field: {"lora_id": y["lora_id"]}} | ({"strength": clamp(y["strength"])} if st else {}))
                present.add(y["lora_id"])
        if over:
            return {"errors": [f"{path}: at most {cap} LoRAs; {full} used, {', '.join(over)} do not fit"]}
        if len(rows) > len(before):
            changed[path], typed[path] = rows, before
    look_lines = list(dict.fromkeys(line for x in picked
                                    if (line := " ".join(([x["text"]] if x["text"] else []) + tokens[("l", x["id"])]))))
    for i, (nid, path, p, parts) in enumerate(prompts):
        out = "".join(x["text"].replace("@@", "@") if "text" in x
                      else " ".join([lib[x["mention"]]["name"]] + tokens[("e", x["mention"])]) for x in parts)
        notes = [f"{lib[h]['name']}: {lib[h]['text']}" for h in mentioned(parts) if lib[h]["text"]]
        out += "\n\n" + "\n".join(notes) if notes else ""
        extra = look_lines if i == 0 else []  # look text goes to the first prompt param only
        out = "\n\n".join(x for x in (out, "\n".join(extra)) if x)
        if len(out) > p.get("max", len(out)):
            what = "elements and looks" if extra else "elements"
            errors.append(f"{path}: at most {p['max']} characters once {what} are added ({len(out)})")
        elif out != val(nid, p):
            changed[path], typed[path] = out, val(nid, p)
    if errors:
        return {"errors": errors}
    elements = [{k: lib[h][k] for k in ("id", "handle", "name", "version")} for h in used]
    applied = [{k: x[k] for k in ("id", "source", "name", "version")} for x in picked]
    return {"inputs": changed, "typed": typed, "elements": elements, "looks": applied, "errors": []}


LOOK_ID = re.compile(r"^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9][a-z0-9_-]{0,63}/[a-z0-9][a-z0-9-]{0,63})$")


def check_mentions(registry: Registry, palette: dict) -> None:
    g = HERE / "fixtures" / "mentions"
    for case in json.loads((g / "scan.json").read_text())["cases"]:
        got = scan(case["text"])
        if got != case["parts"] or "".join(x.get("text") or x["raw"] for x in got) != case["text"]:
            fail(f"fixtures/mentions/scan.json {case['text']!r}: got {got}, want {case['parts']}")
    print("ok   fixtures/mentions/scan.json")
    doc_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/WorkflowDoc"}, registry=registry)

    def bad_looks(name: str, looks: list[dict]) -> bool:
        """The looks as studio reads them: own (UUID) or starter ids, 0-4 images, 0-4 LoRAs (null id: starter only)."""
        bad = [x["id"] for x in looks if not LOOK_ID.match(x["id"]) or x["source"] != ("mine" if "/" not in x["id"] else "starter")
               or len(x["images"]) > 4 or len(x["loras"]) > 4 or not look_needs(x)
               or any(y["lora_id"] is None and x["source"] == "mine" for y in x["loras"])]
        if bad:
            fail(f"{name}: looks break the look rules: {bad}")
        return bool(bad)

    fit = json.loads((g / "fit.json").read_text())
    for wid, wf in fit["workflows"].items():
        if errs := list(doc_v.iter_errors(wf)):
            fail(f"fixtures/mentions/fit.json {wid}: not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
    for case in fit["cases"]:
        item = case["item"]
        if item.get("kind") == "look" and bad_looks("fixtures/mentions/fit.json", [item]):
            continue
        got = look_fit(fit["workflows"][case["workflow"]]["nodes"], palette, item, case.get("loras", {}))
        if got != case["expect"]:
            fail(f"fixtures/mentions/fit.json {case['description']!r}: got {got!r}, want {case['expect']!r}")
    print("ok   fixtures/mentions/fit.json")
    for p in sorted(g.glob("resolve.*.json")):
        fx, name = json.loads(p.read_text()), f"fixtures/mentions/{p.name}"
        if errs := list(doc_v.iter_errors(fx["workflow"])):
            fail(f"{name}: workflow is not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
            continue
        # the library as studio reads it: images of deleted takes already left out (so 0 to 4)
        bad = [e["handle"] for e in fx["library"] if not HANDLE.match(e["handle"]) or len(e["images"]) > 4]
        if bad:
            fail(f"{name}: library items break the element rules: {bad}")
        if bad_looks(name, fx.get("looks", [])):
            continue
        got = resolve_mentions(fx["workflow"]["nodes"], palette, fx["library"], fx.get("sizes"), fx.get("looks"),
                               fx.get("chosen"), fx.get("loras"))
        want = fx["expect"]
        if got != (want if want.get("errors") else {"inputs": {}, "typed": {}, "elements": [], "looks": [],
                                                     "errors": []} | want):
            fail(f"{name}: got {json.dumps(got)}, want {json.dumps(want)}")
        else:
            print(f"ok   {name}")
