"""contracts/check.py, voices (README, "Voices").
Moved out of check.py as is; check.py re-exports every name."""

import copy
import json
import re

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry

from check_core import HERE, OPENAPI, fail, rnd, script_target, voice_field
from check_graph import family, workflow_media, workflow_sound
from check_mentions import HANDLE, look_fit
from check_video import media_value

# ---------------------------------------------------------------- voices (README, "Voices")
VOICE_SOURCE = {"preset": "preset", "cloned": "clip", "designed": "design"}  # a voice's group → what `accepts` names
VOICE_REFUSED = {"preset": "this workflow cannot use a preset voice", "clip": "this workflow cannot clone a voice",
                 "design": "this workflow cannot use a designed voice"}
SPEECH_CPS = 15  # characters per second of planned speech (the peak_gb variable `speech_seconds`)


def voice_params(nodes: dict, palette: dict) -> list[tuple[str, dict]]:
    """Every voice param of a known, not bypassed node, node id order then schema order (it takes no wire), its
    accepts narrowed to the node's `only` (1.30); from 1.32 a rows param's one voice field (a dialogue's lines) counts
    as one, named `<param>.<field>` and narrowed by `only` on the rows param."""
    def one(nid: str, p: dict) -> dict | None:
        f = voice_field(p) if p["type"] == "rows" else None
        q = p if p["type"] == "voice" else p["item"][f] | {"name": f"{p['name']}.{f}"} if f else None
        if q and p["name"] in nodes[nid].get("only", {}):
            q = q | {"accepts": [a for a in q["accepts"] if a in nodes[nid]["only"][p["name"]]]}
        return q
    return [(nid, q) for nid in sorted(nodes) if (s := palette.get(nodes[nid]["kind"]))
            and not nodes[nid].get("bypass") for p in s["params"] if (q := one(nid, p))]


def voice_sources(item: dict) -> list[str]:
    """What a saved voice offers a voice param, its own source first; from 1.31 a designed voice with a clip also
    offers `clip` (README, Voices: cloned voices)."""
    own = VOICE_SOURCE[item["group"]]
    return [own] + (["clip"] if own != "clip" and "clip" in item["voice"] else [])


def voice_source(param: dict, item: dict) -> str | None:
    """The defs voice `source` studio sends for this param: the first of voice_sources it accepts (None: it refuses)."""
    return next((x for x in voice_sources(item) if x in param["accepts"]), None)


def voice_reason(kind: str, param: dict, item: dict) -> str | None:
    """Why one voice param cannot take this saved voice (item: a LibraryItem of kind voice), None when it can. Studio's
    run 422 is `<node>.<param>: <reason>`."""
    src = VOICE_SOURCE[item["group"]]
    if voice_source(param, item) is None:
        return VOICE_REFUSED[src]
    if src == "preset" and item["voice"]["family"] != family(kind):
        return f"voice {item['name']} is for {item['voice']['family']}"
    return None


def voice_fit(nodes: dict, palette: dict, item: dict) -> str | None:
    """GET /library?workflow= for a voice: None when some voice param takes it, else the first param's reason."""
    params = voice_params(nodes, palette)
    if not params:
        return "this workflow takes no voice"
    reasons = [voice_reason(nodes[nid]["kind"], p, item) for nid, p in params]
    return None if None in reasons else reasons[0]


VOICE_CLIP = {"accepts": ["audio"], "trim": True, "min_s": 3, "max_s": 30}  # a voice's clip, checked as this media param
CONSENT = 'voice.consent: tick "I have the right to use this voice"'


def save_voice(body: dict, takes: dict, voices: dict) -> dict:
    """POST /library with kind voice, the 1.31 part (README, Voices: cloned voices), after the 1.30 checks up to
    `loras: a voice has no LoRAs` and before language, style and name: {status, detail} or {voice, from_take_id}.
    takes: the caller's live takes by id ({id, kind, duration_s, has_audio?, voices?: made_with.voices}); voices: the
    caller's voices by id (LibraryItems). In / out are taken as already snapped."""
    g, v, ft = body["group"], dict(body.get("voice") or {}), body.get("from_take")
    err = lambda d, status=422: {"status": status, "detail": d}
    if g == "cloned" and "design" in v:
        return err("voice.design: a cloned voice has no design")
    if "clip" in v and ft:
        return err("voice.clip: give the clip or from_take, not both")
    path, ref = ("voice.clip", v.pop("clip")) if "clip" in v else \
        ("from_take", {k: ft[k] for k in ("take_id", "in", "out") if k in ft}) if ft else (None, None)
    if g == "cloned" and ref is None:
        return err("voice: a cloned voice needs voice.clip")
    if ref is not None and (take := takes.get(ref["take_id"])) is None:
        return err(f"{path}: take {ref['take_id']} not found", 404)
    if g == "designed" and "design" not in v:
        if not ft:
            return err("voice: a designed voice needs voice.design")
        made = next((x for x in take.get("voices", []) if x["source"] == "designed"), None)
        if made is None:
            return err(f"from_take: take {take['id']} was not made with a designed voice")
        if (src := voices.get(made["id"])) is None:
            return err(f"from_take: voice {made['name']} was deleted")
        v |= {k: src["voice"][k] for k in ("design", "language", "style") if k in src["voice"] and k not in v}
    out = {k: v[k] for k in ("design", "language", "style") if v.get(k)}
    if ref is not None:
        e, ref = media_value(VOICE_CLIP, ref, take)
        if e:
            return err(f"{path}: {e}")
        if v.get("consent") is not True:
            return err(CONSENT)
        out |= {"clip": ref, "consent": True}
    return {"voice": {"source": g} | out, "from_take_id": ref["take_id"] if ref else None}


def sweep(takes: list[dict], voices: list[dict]) -> list[str]:
    """The deleted takes whose objects the sweep may delete now: none that one of the same owner's voices keeps as its
    clip (README, Voices: cloned voices)."""
    kept = {(x["owner_id"], x["voice"]["clip"]["take_id"]) for x in voices if "clip" in x["voice"]}
    return [t["id"] for t in takes if t["deleted"] and (t["owner_id"], t["id"]) not in kept]


def speech_seconds(nodes: dict, palette: dict, stage: str, max_seconds: float | None = None) -> float:
    """The peak_gb variable `speech_seconds` of node `stage` (README, Voices: memory): code points of the text reaching its
    first Text input (a prompt node's merged text; through any other node, the nearest upstream prompt's), else of
    its nearest upstream prompt, ÷ 15, capped at max_seconds; 0 without text. Nearest = fewest wires, then node id."""
    val = lambda nid: nodes[nid].get("params", {}).get("text", next(
        p["default"] for p in palette[nodes[nid]["kind"]]["params"] if p["name"] == "text"))
    s = palette[nodes[stage]["kind"]]
    if script_target(s):  # API 1.32, a dialogue: its lines' text and the pauses between them (README, Dialogue)
        rows = nodes[stage].get("params", {}).get("lines", [])
        sec = sum(len(r.get("text", "")) for r in rows) / SPEECH_CPS + sum(r.get("pause_s", 0) for r in rows[:-1])
        return min(sec, max_seconds) if max_seconds is not None else sec
    port = next((i["name"] for i in s["inputs"] if i["type"] == "Text" and i["name"] in nodes[stage].get("in", {})), None)
    level = [nodes[stage]["in"][port].split(".")[0]] if port else [stage]
    seen = set(level)
    while level:
        if hits := sorted(n for n in level if nodes[n]["kind"] == "prompt" and n != stage):
            chars = len(val(hits[0]))
            break
        level = sorted({ref.split(".")[0] for n in level for ref in nodes[n].get("in", {}).values()} - seen)
        seen |= set(level)
    else:
        chars = 0
    sec = chars / SPEECH_CPS
    return min(sec, max_seconds) if max_seconds is not None else sec


SCRIPT_LINE = re.compile(r"^@([A-Za-z][A-Za-z0-9_-]*)\s*(?:\(([^()]*)\))?\s*:(.*)$")
SCRIPT_FORM = "write @name: text or @name (style): text"


def script_lines(script: str) -> list[dict]:
    """A script's lines (README, Dialogue: scripts): each line of the text (split on \\n, a \\r before it dropped),
    trimmed, from 1: blank = a pause; else `@handle: text` or `@handle (style): text` with the handle lower-cased and
    style and text trimmed, or {line, error: SCRIPT_FORM}."""
    out = []
    for k, raw in enumerate(script.replace("\r\n", "\n").split("\n"), 1):
        t = raw.strip()
        m = SCRIPT_LINE.match(t) if t else None
        if not t:
            out.append({"line": k, "blank": True})
        elif not m or not HANDLE.match(m.group(1).lower()):
            out.append({"line": k, "error": SCRIPT_FORM})
        else:
            out.append({"line": k, "handle": m.group(1).lower(), "style": (m.group(2) or "").strip(),
                        "text": m.group(3).strip()})
    return out


def expand_script(nodes: dict, palette: dict, library: list[dict], voices: dict) -> dict:
    """Studio's script expansion at create time (README, Dialogue: scripts), on merged, type-checked values: every
    known, not bypassed node with a `script` param whose value is not blank writes its `lines` from it (the script is
    then sent empty), each line's voice = its element's `voice_id`, checked with voice_reason against the lines'
    voice field (narrowed by `only`). library: the caller's elements as studio reads them ({id, handle, name,
    version, voice_id}, a deleted voice already null); voices: voice id → LibraryItem (the caller's own and the live
    starter voices). Answers {errors} or {inputs, typed, elements, voices, parents} (parents: the clip takes the lines
    send, first use order)."""
    by_handle, errs = {e["handle"]: e for e in library}, []
    res = {"inputs": {}, "typed": {}, "elements": [], "voices": [], "parents": []}
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        sp = next((p for p in s["params"] if p.get("script")), None) if s and not n.get("bypass") else None
        if not sp:
            continue
        lp, vals = script_target(s), {p["name"]: p["default"] for p in s["params"]} | n.get("params", {})
        script, given = vals[sp["name"]], vals["lines"]
        if not script.strip():
            if not given:
                errs.append(f"{nid}.{sp['name']}: the script is empty")
            continue  # typed lines are resolved as voice values, not here
        if given:
            errs.append(f"{nid}.lines: give the script or the lines, not both")
            continue
        field = next(q for x, q in voice_params({nid: n}, palette) if q["name"] == "lines.voice")
        it, pause = lp["item"], vals.get("pause", lp["item"]["pause_s"]["default"])
        parsed, rows, mine = script_lines(script), [], []
        for ln in parsed:
            if ln.get("blank"):
                if rows:
                    rows[-1][1] += 1
                continue
            at = f"{nid}.{sp['name']} line {ln['line']}: "
            el = by_handle.get(ln.get("handle"))
            item = voices.get(el["voice_id"]) if el and el.get("voice_id") else None
            why = item and voice_reason(n["kind"], field, item)
            e = ln.get("error") or ("nothing to say" if not ln["text"] else None) \
                or (f"at most {it['text']['max']} characters ({len(ln['text'])})" if len(ln["text"]) > it["text"]["max"] else None) \
                or (f"the style is at most {it['style']['max']} characters ({len(ln['style'])})"
                    if len(ln["style"]) > it["style"]["max"] else None) \
                or (f"unknown element @{ln['handle']}" if not el else None) \
                or (f"@{ln['handle']} has no voice" if not item else None) \
                or (f"voice {item['name']} does not fit this workflow ({why})" if why else None)
            if e:
                errs.append(at + e)
                continue
            rows.append([{"voice": {"voice_id": item["id"]}, "text": ln["text"], "style": ln["style"]}, 0])
            mine.append((el, item))
        if errs:
            continue
        if not rows:
            errs.append(f"{nid}.{sp['name']}: the script is empty")
            continue
        if len(rows) > lp["max"]:
            errs.append(f"{nid}.lines: at most {lp['max']} lines")
            continue
        last = len(rows) - 1
        lines = [r | {"pause_s": 0 if i == last else min(it["pause_s"]["max"], rnd(pause * (1 + b) * 1000) / 1000)}
                 for i, (r, b) in enumerate(rows)]
        res["inputs"] |= {f"{nid}.{sp['name']}": "", f"{nid}.lines": lines}
        res["typed"] |= {f"{nid}.{sp['name']}": script, f"{nid}.lines": given}
        for el, item in mine:
            if el["id"] not in [x["id"] for x in res["elements"]]:
                res["elements"].append({k: el[k] for k in ("id", "handle", "name", "version")})
            if item["id"] not in [x["id"] for x in res["voices"]]:
                res["voices"].append({"id": item["id"], "name": item["name"], "version": item["version"],
                                      "source": item["group"]})
            clip = item["voice"].get("clip", {}).get("take_id") if voice_source(field, item) == "clip" else None
            if clip and clip not in res["parents"]:
                res["parents"].append(clip)
    return {"errors": errs} if errs else res


def echo_dialogue(lines: list[dict], rate: int = 24000) -> dict:
    """The fake echo/dialogue's take (README, Dialogue: echo): each line one sine tone per character (rate / 15
    samples each), then rnd(pause_s × rate) samples of silence before the next line; cues from 1 in seconds."""
    cues, at = [], 0
    for i, r in enumerate(lines, 1):
        text = r["text"].strip()  # the text spoken: a typed line's spaces are not
        end = at + len(text) * rate // SPEECH_CPS
        cues.append({"line": i, "start_s": at / rate, "end_s": end / rate, "voice_id": r["voice"]["voice_id"],
                     "text": text})
        at = end + (rnd(r["pause_s"] * rate) if i < len(lines) else 0)
    return {"duration_s": at / rate, "cues": cues}


def script_check(script: str, cast: list[str], max_lines: int = 40, text_max: int = 600,
                 style_max: int = 200) -> str | None:
    """A write-script/v1 answer's check in pipelines (README, Dialogue: agents): None, or the failed check's text
    (the write is re-asked once): the first bad line in expand_script's order (malformed, nothing to say, text over
    text_max, style over style_max, a handle not in the cast), no line, too many lines."""
    parsed = [x for x in script_lines(script) if not x.get("blank")]
    for x in parsed:
        e = x.get("error") or ("nothing to say" if not x["text"] else None) \
            or (f"at most {text_max} characters ({len(x['text'])})" if len(x["text"]) > text_max else None) \
            or (f"the style is at most {style_max} characters ({len(x['style'])})" if len(x["style"]) > style_max
                else None) \
            or (f"@{x['handle']} is not in the cast" if x["handle"] not in cast else None)
        if e:
            return f"line {x['line']}: {e}"
    if not parsed:
        return "the script is empty"
    return f"more than {max_lines} lines" if len(parsed) > max_lines else None


def check_voices(registry: Registry, palette: dict) -> None:
    g = HERE / "fixtures" / "voices"
    doc_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/WorkflowDoc"}, registry=registry)
    item_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/LibraryItem"}, registry=registry,
                                  format_checker=FormatChecker())
    for name in ("media.json", "fit.json", "seconds.json"):
        fx = json.loads((g / name).read_text())
        for wid, wf in fx["workflows"].items():
            if errs := list(doc_v.iter_errors(wf)):
                fail(f"fixtures/voices/{name} {wid}: not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
        for case in fx["cases"]:
            nodes = fx["workflows"][case["workflow"]]["nodes"]
            if name == "media.json":
                got = workflow_media(nodes, palette)
                if "sound" in case and (sound := workflow_sound(nodes, palette)) != case["sound"]:  # API 1.79
                    fail(f"fixtures/voices/media.json {case['description']!r}: sound {sound!r}, want {case['sound']!r}")
            elif name == "fit.json":
                if errs := list(item_v.iter_errors(case["item"])):
                    fail(f"fixtures/voices/fit.json {case['description']!r}: item: {errs[0].message}")
                    continue
                item = case["item"]
                got = voice_fit(nodes, palette, item) if item["kind"] == "voice" else look_fit(nodes, palette, item, {})
                if "sends" in case:  # 1.31: the defs voice source of the first param that takes it
                    sent = next((voice_source(p, item) for nid, p in voice_params(nodes, palette)
                                 if voice_reason(nodes[nid]["kind"], p, item) is None), None)
                    if sent != case["sends"]:
                        fail(f"fixtures/voices/fit.json {case['description']!r}: sends {sent!r}, want {case['sends']!r}")
            else:
                got = speech_seconds(nodes, palette, case["stage"], case.get("max_seconds"))
            if got != case["expect"]:
                fail(f"fixtures/voices/{name} {case['description']!r}: got {got!r}, want {case['expect']!r}")
        print(f"ok   fixtures/voices/{name}")
    fx = json.loads((g / "clone.json").read_text())  # 1.31: cloned voices, Save as voice, the sweep
    body_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/LibraryItemCreate"}, registry=registry)
    for case in fx["cases"]:
        if errs := list(body_v.iter_errors(case["body"])):
            fail(f"fixtures/voices/clone.json {case['description']!r}: body: {errs[0].message}")
        elif (got := save_voice(case["body"], fx["takes"], fx["voices"])) != case["expect"]:
            fail(f"fixtures/voices/clone.json {case['description']!r}: got {got!r}, want {case['expect']!r}")
    for case in fx["sweep"]:
        if (got := sweep(case["takes"], case["voices"])) != case["expect"]:
            fail(f"fixtures/voices/clone.json sweep {case['description']!r}: got {got!r}, want {case['expect']!r}")
    print("ok   fixtures/voices/clone.json")
    fx = json.loads((g / "scripts.json").read_text())  # 1.32: scripts, dialogue lines, echo cues, write-script checks
    inputs_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/RunInputs"}, registry=registry)
    for vid, item in fx["voices"].items():
        if errs := list(item_v.iter_errors(item)):
            fail(f"fixtures/voices/scripts.json voice {vid}: {errs[0].json_path}: {errs[0].message}")
    for wid, wf in fx["workflows"].items():
        if errs := list(doc_v.iter_errors(wf)):
            fail(f"fixtures/voices/scripts.json {wid}: not a WorkflowDoc: {errs[0].json_path}: {errs[0].message}")
    for case in fx["cases"]:
        where = f"fixtures/voices/scripts.json {case['description']!r}"
        nodes = copy.deepcopy(fx["workflows"][case["workflow"]]["nodes"])
        for k, v in case.get("params", {}).items():
            nodes[k.split(".")[0]].setdefault("params", {})[k.split(".")[1]] = v
        got = expand_script(nodes, palette, fx["library"], fx["voices"])
        if got != case["expect"]:
            fail(f"{where}: got {json.dumps(got)}, want {json.dumps(case['expect'])}")
            continue
        if "inputs" in got and (errs := list(inputs_v.iter_errors(got["inputs"]))):
            fail(f"{where}: inputs are not RunInputs: {errs[0].message}")
        if "take" in case and (t := echo_dialogue(next(v for k, v in got["inputs"].items() if k.endswith(".lines")))) != case["take"]:
            fail(f"{where}: echo take {json.dumps(t)}, want {json.dumps(case['take'])}")
    for case in fx["checks"]:
        if (got := script_check(case["script"], case["cast"])) != case["expect"]:
            fail(f"fixtures/voices/scripts.json check {case['description']!r}: got {got!r}, want {case['expect']!r}")
    print("ok   fixtures/voices/scripts.json")
    # starter voices: a family's voice ids are not its look ids (they share the /library/<family>/<id> URL)
    reg = HERE / "fixtures" / "registry"
    looks = {x["id"] for x in json.loads((reg / "looks.valid.json").read_text())["looks"]}
    for f in sorted(reg.glob("*voices.valid.json")):
        voices = json.loads(f.read_text())["voices"]
        if clash := sorted({v["id"] for v in voices} & looks) or [v["id"] for v in voices
                                                                   if v["id"].split("/")[0] != v["family"]]:
            fail(f"fixtures/registry/{f.name}: ids clash with looks or name another family: {clash}")
        else:
            print(f"ok   fixtures/registry/{f.name} ids")
