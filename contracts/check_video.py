"""contracts/check.py, video rules, media inputs, minimax-h3 generate (README, "Video", "Media inputs").
Moved out of check.py as is; check.py re-exports every name."""

import copy
import json
import math
from decimal import ROUND_HALF_UP, Decimal

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry

from check_core import HERE, OPENAPI, fail, graph_values, image_field, media_field, optional_media, rnd
from check_graph import workflow_sound
from check_mentions import excluded
from check_rules import check_rules


# ---------------------------------------------------------------- video rules (README, "Video")
def auto_frames(seconds: float, fps: float, min_s: float, max_s: float, param: dict) -> int:
    """Auto length: the predicted seconds as frames, clamped to min_s..max_s and the param's max, floored onto the
    grid min + k·step and snapped up one step when that fell below min_s (round = half to even, as the old studio)."""
    top = param["min"] + (param["max"] - param["min"]) // param["step"] * param["step"]
    lo, hi = round(min_s * fps), min(round(max_s * fps), top)
    f = min(max(round(seconds * fps), lo), hi)
    f = param["min"] + max(f - param["min"], 0) // param["step"] * param["step"]
    return min(f + param["step"] if f < lo else f, top)


def clip_of(nodes: dict, palette: dict) -> tuple | None:
    """(frames or "auto", fps) of the workflow's one unwired frames param on a known, not bypassed node whose fps
    param is unwired; None when there is not exactly one."""
    found = []
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            if p["type"] == "frames" and p["name"] not in n.get("in", {}) and p["fps"] not in n.get("in", {}):
                val = lambda name: n.get("params", {}).get(name, next(q["default"] for q in s["params"] if q["name"] == name))
                found.append((val(p["name"]), val(p["fps"])))
    return found[0] if len(found) == 1 else None


def position_errors(nodes: dict, palette: dict) -> list[str]:
    """Keyframe positions (README, Video: keyframes): each `position: seconds` item field of an unwired rows param
    of a known, not bypassed node is the pixel frame min(round(s·fps), F − 1); past the clip (round(s·fps) > F) or a
    frame an earlier row of that param has is a 422. With auto length (or no single clip) the past-the-clip check
    is left to the runner."""
    clip, out = clip_of(nodes, palette), []
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            fields = [k for k, v in p.get("item", {}).items() if isinstance(v, dict) and v.get("position") == "seconds"]
            if p["type"] != "rows" or p["name"] in n.get("in", {}) or not fields or not clip:
                continue
            frames, fps = clip
            for f in fields:
                seen = {}
                for i, row in enumerate(n.get("params", {}).get(p["name"], p["default"]), 1):
                    fr = round(row.get(f, p["item"][f]["default"]) * fps)
                    where = f"{nid}.{p['name']}[{i}].{f}"
                    if isinstance(frames, int) and fr > frames:
                        out.append(f"{where}: frame {fr} is past the clip's last frame {frames - 1}")
                        continue
                    fr = min(fr, frames - 1) if isinstance(frames, int) else fr
                    if fr in seen:
                        out.append(f"{where}: frame {fr} is already used by row {seen[fr]}")
                    else:
                        seen[fr] = i
    return out


# ---------------------------------------------------------------- media inputs (README, "Media inputs")
LTX_MAX_FRAMES = 321  # ltx/duration's max: the longest clip retake, extend and audio to video make


def ltx_floor(n: int) -> int:
    """A source's frames floored to LTX's 8k + 1 (k ≥ 1), as the old studio reads it."""
    return 8 * max(1, (n - 1) // 8) + 1


def retake_map(start: float, end: float, fps: float, frames: int) -> dict:
    """ltx/retake on whole frames (studio snaps the span to the take's frames): f = rnd(t·fps), S = ⌊(f_start + 4)/8⌋,
    E = max(⌊(f_end + 4)/8⌋, S + 1) clamped to the F latent frames (end exclusive), in integer arithmetic: f/8 rounded
    half up, as the old UI's Math.round (tasks.js). Pixels max(8S − 7, 0) … 8(E − 1) are regenerated. An end at or past
    the clip's end (f_end ≥ n) is the last latent frame (ours: the old UI's round(duration·fps/8) left the last 8
    frames out). The output is the whole source (floored to 8k + 1), offset 0."""
    n, fs, fe = ltx_floor(frames), rnd(start * fps), rnd(end * fps)
    s, last = (fs + 4) // 8, (n - 1) // 8 + 1
    e = last if fe >= n else min(max((fe + 4) // 8, s + 1), last)
    if s >= e:
        return {"error": "retake.span: nothing to regenerate on the 8-frame grid"}
    return {"latent": [s, e], "frames": n,
            "map": {"offset_s": 0, "new_in_s": max(8 * s - 7, 0) / fps, "new_out_s": (8 * (e - 1) + 1) / fps}}


def extend_map(seconds: float, direction: str, fps: float, frames: int) -> dict:
    """ltx/extend: N = max(1, ceil(seconds·fps/8)) latent frames (8N pixel frames) after or before the source floored
    to 8k + 1; the output is the whole extended clip, at most 321 frames."""
    n = ltx_floor(frames)
    k = max(1, math.ceil(seconds * fps / 8 - 1e-9))
    total, add = n + 8 * k, 8 * k / fps
    if total > LTX_MAX_FRAMES:
        return {"error": f"extend.seconds: the clip would be {total} frames; at most {LTX_MAX_FRAMES}"}
    m = {"offset_s": 0, "new_in_s": n / fps, "new_out_s": total / fps} if direction == "after" else \
        {"offset_s": add, "new_in_s": 0, "new_out_s": add}
    return {"latent": k, "frames": total, "map": m}


def audio_map(in_s: float, out_s: float, fps: float) -> dict:
    """ltx/encode-audio: the clip's frames = the part in..out at fps, floored to 8k + 1 (at most 321); the output's 0
    is the source's `in`."""
    f = min(LTX_MAX_FRAMES, (math.floor((out_s - in_s) * fps + 1e-6) - 1) // 8 * 8 + 1)
    if f < 9:
        return {"error": "audio.source: the part from in to out is shorter than 9 frames"}
    return {"frames": f, "map": {"offset_s": -in_s, "new_in_s": 0, "new_out_s": f / fps}}


def media_fit(p: dict, take: dict) -> str | None:
    """Why a take does not fit a `media` param (the 422 text after `<node>.<param>: `), else None. A trim part longer
    than max_s is the value's own 422 (README, Media inputs); an offer caps it instead."""
    tid, kinds = take["id"], p["accepts"]
    picture = take["kind"] == "video" and "video" in kinds
    if not picture and not ("audio" in kinds and take["kind"] in ("audio", "video") and take.get("has_audio", True)):
        return f"take {tid} has no sound" if kinds == ["audio"] else f"take {tid} is not a video"
    if picture and p.get("grid") and (take["width"] % p["grid"] or take["height"] % p["grid"]):
        return f"take {tid} is {take['width']} × {take['height']}; this input needs sides on a {p['grid']} px grid"
    if picture and p.get("max_frames") and take["frames"] > p["max_frames"]:
        return f"take {tid} has {take['frames']} frames; this input takes at most {p['max_frames']}"
    if p.get("min_s") and take["duration_s"] < p["min_s"]:  # API 1.22; a trim cannot make it longer
        return f"take {tid} lasts {take['duration_s']:.1f} s; this input takes at least {p['min_s']:.1f} s"
    if p.get("max_s") and take["duration_s"] > p["max_s"] and not p.get("trim"):
        return f"take {tid} lasts {take['duration_s']:.1f} s; this input takes at most {p['max_s']:.1f} s"
    return None


def placed_paths(wf: dict) -> set[str] | None:
    """The paths a workflow's studio block places (composer, quick, inspector); None without one (every param shows).
    Create shows exactly those, so "Use in…" fills no other (API 1.22)."""
    st = wf.get("studio")
    if st is None:
        return None
    return set(st.get("composer", []) + st.get("quick", [])) | {x for sec in st.get("inspector", []) for x in sec["items"]}


def media_value(spec: dict, ref: dict, take: dict | None) -> tuple[str | None, dict]:
    """Studio's check of one media value (a media param or, API 1.22, a rows item's media field): the 422 text after
    `<path>: ` or None, and the value with a trim field's in / out filled (in 0, out min(duration, max_s)). In / out
    are taken as already snapped (README, Media inputs: Values)."""
    if take is None:
        return f"take {ref['take_id']} not found", ref
    if err := media_fit(spec, take):
        return err, ref
    if not spec.get("trim"):
        return ("this input takes the whole take (no in or out)" if {"in", "out"} & set(ref) else None), ref
    d = take["duration_s"]
    a, b = ref.get("in", 0), ref.get("out", min(d, spec.get("max_s", d)))
    if a >= b:
        return "in must be before out", ref
    if b > d:
        return f"out {b:.1f} is past the take's end {d:.1f}", ref
    if spec.get("max_s") and b - a > spec["max_s"]:
        return f"the part lasts {b - a:.1f} s; this input takes at most {spec['max_s']:.1f} s", ref
    if spec.get("min_s") and b - a < spec["min_s"]:
        return f"the part lasts {b - a:.1f} s; this input takes at least {spec['min_s']:.1f} s", ref
    return None, ref | {"in": a, "out": b}


def media_rows(nodes: dict, palette: dict, takes: dict) -> dict:
    """Studio's run checks of API 1.22 (README, Media inputs: rows of media) on merged, type-checked values: an
    `optional` node left empty is dropped with its wires, then a rows param with a value while an input its
    `not_with` names is wired is refused, then every rows param with one media field is checked item by item
    (`media_value`, items from 1) and over `max_total_s`; from 1.37 then each item's optional media fields, checked
    the same way when set (item, then field order), never counted toward `max_total_s`. Answers {error}
    or {error: null, dropped, values: path → the rows with in / out filled}."""
    drop = sorted(nid for nid, n in nodes.items() if n.get("optional") and n.get("params", {}).get("image") is None)
    nodes = {nid: n | {"in": {k: r for k, r in n.get("in", {}).items() if r.split(".")[0] not in drop}}
             for nid, n in nodes.items() if nid not in drop}
    values = {}
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            path, v = f"{nid}.{p['name']}", n.get("params", {}).get(p["name"], p["default"])
            if p["type"] != "rows" or not v or p["name"] in n.get("in", {}):
                continue
            if wired := next((i for i in p.get("not_with", []) if i in n["in"]), None):
                return {"error": f"{path}: must stay empty while {nid}.{wired} is wired"}
            if not (f := media_field(p)):
                continue
            rows, total = [], 0.0
            for i, row in enumerate(v, 1):
                take = takes.get(row[f]["take_id"])
                err, ref = media_value(p["item"][f], row[f], take)
                if err:
                    return {"error": f"{path}[{i}].{f}: {err}"}
                rows.append(row | {f: ref})
                total += ref["out"] - ref["in"] if "in" in ref else take["duration_s"]
            if "max_total_s" in p and total > p["max_total_s"] + 1e-9:
                return {"error": f"{path}: the takes last {total:.1f} s in all; at most {p['max_total_s']:.1f} s"}
            for i, row in enumerate(rows, 1):  # API 1.37: an optional media field, empty or checked like the other
                for o in optional_media(p):
                    if row.get(o) is not None:
                        err, row[o] = media_value(p["item"][o], row[o], takes.get(row[o]["take_id"]))
                        if err:
                            return {"error": f"{path}[{i}].{o}: {err}"}
            values[path] = rows
    return {"error": None, "dropped": drop, "values": values}


def extend_room(take: dict, rng: tuple | None) -> dict | None:
    """ltx/extend's pre-fill for a video take (None: not offered): the default 2 s, or the most whole 8-frame steps
    under 321 frames as seconds (a 0.1 s step, rounded down); a Timeline range gives the direction: after when it ends
    at the take's end (within one frame), else before when it starts at 0, else none."""
    fps, n = take["fps"], ltx_floor(take["frames"])
    k = (LTX_MAX_FRAMES - n) // 8
    if k < 1:
        return None
    out = {} if n + 8 * math.ceil(2 * fps / 8) <= LTX_MAX_FRAMES else {"seconds": math.floor(8 * k / fps * 10) / 10}
    if rng:
        if take["duration_s"] - rng[1] < 1 / fps:
            out["direction"] = "after"
        elif rng[0] < 0.5 / fps:
            out["direction"] = "before"
        else:
            return None
    return out


def offer_values_by(wf: dict, palette: dict, inputs: dict) -> dict:
    """The offers rule (API 1.39): a pre-filled value that a cross-node `values_by` refuses under the workflow's value of
    the named param (Extend before on Distilled) also sets that param, to the first value of its enum (the node's
    `only`, else the schema's) that allows the pre-fill (model.variant dev)."""
    out, g = {}, graph_values(wf["nodes"], palette, inputs)
    for nid in sorted(wf["nodes"]):
        for p in palette.get(wf["nodes"][nid]["kind"], {}).get("params", []):
            vb, path = p.get("values_by"), f"{nid}.{p['name']}"
            if not vb or "." not in vb["param"] or path not in inputs:
                continue
            on, _, name = vb["param"].partition(".")
            ref = wf["nodes"].get(on)
            spec = next((q for q in palette.get(ref["kind"], {}).get("params", []) if q["name"] == name), None) if ref else None
            ok = lambda v: inputs[path] in vb["values"].get(v, [inputs[path]])  # noqa: E731 (not listed: allows all)
            if spec and not ok(g[on][name]):
                first = next((v for v in ref.get("only", {}).get(name, spec.get("enum", [])) if ok(v)), None)
                out |= {vb["param"]: first} if first else {}
    return out


def take_uses(take: dict, workflows: list[dict], palette: dict, rng: tuple | None = None) -> list[dict]:
    """"Use in…" offers (README, Media inputs: offers): every workflow with exactly one unwired input of the take's
    sort on a known, not bypassed, not optional node (an image: an image param or Image rows; a video or audio take: a
    media param whose `accepts` names its kind, or `audio` for a video with sound) that the take fits, with the inputs
    pre-filled. From API 1.22 an input counts only when the workflow's studio block places it (none: every input) and
    a rows param excluded by a wired `not_with` input does not count.
    From API 1.79 (README, Music and sound): a workflow with a `sound` class takes an audio take into a `media` param
    as sound only when the take is an audio take of that class (`sound`; a video's sound never; a rows param still
    takes any class; a video into a param that accepts video is no sound), and every
    `span` is filled for an audio take into an audio input too; ltx's extend room is a video source's rule only.
    rng: (in, out) in the take's own seconds (a Timeline clip or selection), else the whole take."""
    out = []
    for wf in workflows:
        if wf.get("media") == "text":  # API 1.107: a text workflow (listen) answers, it makes no take: never a use
            continue
        found, shown = [], placed_paths(wf)
        cls = workflow_sound(wf["nodes"], palette)
        for nid in sorted(wf["nodes"]):
            n, s = wf["nodes"][nid], palette.get(wf["nodes"][nid]["kind"])
            for p in (s["params"] if s and not n.get("bypass") and not n.get("optional") else []):
                if p["name"] in n.get("in", {}) or excluded(n, p) or (shown is not None and f"{nid}.{p['name']}" not in shown):
                    continue
                if take["kind"] == "image" and (p["type"] == "image" or image_field(p)):
                    found.append((nid, p))
                elif take["kind"] != "image" and p["type"] == "media" and (
                        take["kind"] in p["accepts"] or (take["kind"] == "video" and "audio" in p["accepts"])):
                    if cls and "video" not in p["accepts"] and take.get("sound") != cls:
                        continue  # 1.79: offers by class (no Repaint · ACE-Step on a voice take nor a video's sound)
                    found.append((nid, p))
                elif take["kind"] != "image" and media_field(p) and take["kind"] in p["item"][media_field(p)]["accepts"]:
                    found.append((nid, p))  # 1.32: rows of one media field, by the take's own kind only
        spec = (found[0][1]["item"][media_field(found[0][1])] if media_field(found[0][1]) else found[0][1]) if found else None
        if len(found) != 1 or (take["kind"] != "image" and media_fit(spec, take)):
            continue
        (nid, p), path = found[0], f"{found[0][0]}.{found[0][1]['name']}"
        a, b = rng or (0, take.get("duration_s"))
        if p["type"] == "image":
            inputs = {path: {"take_id": take["id"]}}
        elif p["type"] == "rows" and image_field(p):
            inputs = {path: [{image_field(p): {"take_id": take["id"]}}]}
        elif p["type"] == "rows":  # 1.32: the take as item 1 of a rows param of one media field
            longest = min(spec.get("max_s", math.inf), p.get("max_total_s", math.inf))
            part = {"in": a, "out": min(b, a + longest)} if spec.get("trim") else {}
            n = part["out"] - a if part else b - a  # the part offered (a Timeline range) holds the field's limits
            if n < spec.get("min_s", 0) - 1e-9 or n > spec.get("max_s", math.inf) + 1e-9:
                continue
            inputs = {path: [{media_field(p): {"take_id": take["id"]} | part}]}
        else:
            cap = min(b, a + p["max_s"]) if p.get("max_s") else b
            inputs = {path: {"take_id": take["id"]} | ({"in": a, "out": cap} if p.get("trim") else {})}
            if take["kind"] == "video" and "video" in p["accepts"]:
                inputs |= {f"{x}.{q['name']}": {"start": a, "end": b} for x in sorted(wf["nodes"])
                           for q in palette.get(wf["nodes"][x]["kind"], {}).get("params", []) if q["type"] == "span"}
                ext = [x for x in sorted(wf["nodes"]) if wf["nodes"][x]["kind"].endswith("/extend")]  # ltx/extend, echo/extend
                if ext and (room := extend_room(take, rng)) is None:
                    continue
                inputs |= {f"{x}.{k}": v for x in ext for k, v in room.items()} if ext else {}
            elif take["kind"] == "audio" and "audio" in p["accepts"]:  # 1.79: a span on an audio source (Repaint)
                inputs |= {f"{x}.{q['name']}": {"start": a, "end": b} for x in sorted(wf["nodes"])
                           for q in palette.get(wf["nodes"][x]["kind"], {}).get("params", []) if q["type"] == "span"}
        inputs |= offer_values_by(wf, palette, inputs)
        out.append({"workflow_id": wf["id"], "title": wf["title"]} | ({"action": wf["action"]} if "action" in wf else {})
                   | {"family": wf["family"], "media": wf.get("media", "image"), "param": path, "inputs": inputs})
    return out


# ---------------------------------------------------------------- minimax-h3 generate (README, the minimax-h3 family)
H3_MAX_PIXELS = 768 * 1344  # 1,032,192: the h3 binary's largest canvas
H3_MAX_REFS, H3_MAX_SOUNDS, H3_MAX_SOUND_S, H3_SOUND_FRAMES = 12, 3, 15.0, 56  # h3.c's Ref2VA limits


H3_RENDER = {"Full": 1.0, "75%": 0.75, "62.5%": 0.625}  # minimax-h3/generate.render (API 1.37)


def h3_render_size(width: int, height: int, render: str) -> tuple[int, int] | None:
    """The size h3 renders at for `render` (API 1.37, README the minimax-h3 family: Render): the canvas at Full;
    else with g = gcd(w / 32, h / 32) and k = g × scale rounded half up, capped at g − 1, (w k / g, h k / g), the
    canvas's exact shape on the 32 px grid (h3 accepts no other). None when there is none (g = 1, or k = 0)."""
    if render == "Full":
        return width, height
    g = math.gcd(width // 32, height // 32)
    k = min(int(Decimal(g * H3_RENDER[render]).quantize(Decimal(1), ROUND_HALF_UP)), g - 1)
    return (width * k // g, height * k // g) if k > 0 else None


def h3_generate_error(node: str, refs: int, first: bool, last: bool, width: int, height: int, frames: int = 124,
                      videos: list | None = None, audios: list | None = None,
                      speed: list | None = None) -> str | None:
    """minimax-h3/generate's refusals, before the child starts (the run fails with the text). refs = image
    references; videos = [seconds read (out - in), sound] per video reference, sound true when the row keeps it and
    the take has sound; audios = seconds read per audio reference (API 1.22). In order: any reference with a first
    or last frame (Ref2VA takes no anchors); audio references without an image or video one; more than 12
    references; more than 3 sound inputs (audio references and videos with sound); a video with sound under 56
    frames, or whose part reads under 56 frames (h3 reads a video reference up to the clip's frames, keeps 5 + 17k of
    them and reads its sound only that long; it needs 2 s); more than 15 s of sound in
    all (an audio's part, a video's sound up to the clip's length); a side off 256..1344 or off the 32 px grid;
    more than 768 × 1344 pixels. Studio checks each row (2-15 s), the counts and each kind's 15 s; these cross the
    params, need the clip's frames or the take's sound, or a canvas that may come from any Canvas output.
    From 1.37 a video row is [seconds read, sound, soundtrack seconds or null], sound "replace" for Sound: replace
    (a sound input like a kept one, reading min(soundtrack part, video part, frames / 24)); right after the anchors,
    Replace without a soundtrack and a soundtrack without Replace are refused under `<node>.videos[<i>].soundtrack`.
    speed = [layers, reuse, token reduction, render] on Base (null on Base fp8, which ignores them), checked last: a
    render with no smaller same-shape size, token reduction with layers ≤ 40 and reuse 3, token reduction when h3
    renders 256 × 256."""
    videos = [(v[0], v[1], v[2] if len(v) > 2 else None) for v in videos or []]
    audios = audios or []
    sounds = [s for _, s, _ in videos if s]
    if (refs or videos or audios) and (first or last):
        return f"{node}: references cannot go with a first or last frame"
    for i, (_, s, t) in enumerate(videos, 1):
        if s == "replace" and t is None:
            return f"{node}.videos[{i}].soundtrack: Replace needs a soundtrack"
        if s != "replace" and t is not None:
            return f"{node}.videos[{i}].soundtrack: a soundtrack is read only with Sound: Replace"
    if audios and not (refs or videos):
        return f"{node}: audio references need an image or video reference"
    if (n := refs + len(videos) + len(audios)) > H3_MAX_REFS:
        return f"{node}: at most {H3_MAX_REFS} references in all ({n})"
    if (n := len(audios) + len(sounds)) > H3_MAX_SOUNDS:
        return f"{node}: at most {H3_MAX_SOUNDS} sound references (audio references and videos with sound; {n})"
    if sounds and frames < H3_SOUND_FRAMES:
        return f"{node}: a video reference with sound needs a clip of at least {H3_SOUND_FRAMES} frames ({frames})"
    for i, (x, s, _) in enumerate(videos, 1):
        if s and (n := round(x * 24)) < H3_SOUND_FRAMES:  # h3 keeps 5 + 17k frames, reads the sound that long
            return (f"{node}: a video reference with sound needs a part of at least {H3_SOUND_FRAMES} frames at 24 fps "
                    f"(video {i} reads {n})")
    heard = sum(min(x, frames / 24, *([t] if s == "replace" else [])) for x, s, t in videos if s)
    if (t := sum(audios) + heard) > H3_MAX_SOUND_S + 1e-9:
        return f"{node}: the sound references last {t:.1f} s in all; at most {H3_MAX_SOUND_S:.1f} s"
    if any(not 256 <= x <= 1344 or x % 32 for x in (width, height)):
        return f"{node}: the canvas is {width} × {height}; sides must be multiples of 32 from 256 to 1344"
    if width * height > H3_MAX_PIXELS:
        return f"{node}: the canvas is {width} × {height}; at most {H3_MAX_PIXELS} pixels (768 × 1344)"
    if speed:
        layers, reuse, reduce, render = speed
        size = h3_render_size(width, height, render)
        if size is None:
            return f"{node}: the canvas {width} × {height} has no smaller size of the same shape; use Full"
        if reduce and layers <= 40 and reuse == 3:
            return (f"{node}: token reduction with 40 layers or fewer and reuse 3 gives colour ringing; "
                    "use reuse 2 or more layers")
        if reduce and size == (256, 256):
            return f"{node}: token reduction does not run when h3 renders 256 × 256; switch it off or render larger"
    return None


def check_media(registry: Registry) -> None:
    g, reg = HERE / "fixtures" / "video", HERE / "fixtures" / "registry"
    fx = json.loads((g / "sources.json").read_text())
    for c in fx["cases"]:
        fn = {"retake": retake_map, "extend": extend_map, "audio": audio_map}[c["kind"]]
        if (got := fn(*c["args"])) != c["expect"]:
            fail(f"fixtures/video/sources.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/sources.json")
    for c in json.loads((g / "minimax-h3.json").read_text())["cases"]:
        if (got := h3_generate_error(*c["args"])) != c["expect"]:
            fail(f"fixtures/video/minimax-h3.json {c['description']!r}: got {json.dumps(got)}")
    for c in json.loads((g / "minimax-h3.json").read_text())["render"]:  # API 1.37
        got = h3_render_size(*c["args"])
        if (list(got) if got else None) != c["expect"]:
            fail(f"fixtures/video/minimax-h3.json render {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/minimax-h3.json")
    pres = ("ltx.", "qwen-image.", "minimax-h3-image.", "minimax-h3.")
    pal = {n["kind"]: n for pre in pres for n in json.loads((reg / f"{pre}nodes.valid.json").read_text())["nodes"]}
    wfs = [w for pre in pres for w in json.loads((reg / f"{pre}workflows.valid.json").read_text())["workflows"]]
    uses_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/TakeUses"}, registry=registry,
                                  format_checker=FormatChecker())
    fx = json.loads((g / "uses.json").read_text())
    for c in fx["cases"]:
        take = fx["takes"][c["take"]] | {"id": c["take"]}
        cw, cp = (wfs, pal) if "registries" not in c else registries(c["registries"])  # 1.79: echo's or the voices'
        got = {"uses": take_uses(take, cw, cp, tuple(c["range"]) if "range" in c else None)}
        if c.get("param"):
            got = {"error": media_fit(c["param"], take)}
        elif errs := list(uses_v.iter_errors(got)):
            fail(f"fixtures/video/uses.json {c['description']!r}: not TakeUses: {errs[0].message}")
        if got != c["expect"]:
            fail(f"fixtures/video/uses.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/uses.json")
    inputs_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/RunInputs"}, registry=registry,
                                    format_checker=FormatChecker())
    fx, by_id = json.loads((g / "media-rows.json").read_text()), {w["id"]: w for w in wfs}
    takes = {k: t | {"id": k} for k, t in fx["takes"].items()}
    for c in fx["cases"]:
        if errs := list(inputs_v.iter_errors(c["inputs"])):
            fail(f"fixtures/video/media-rows.json {c['description']!r}: not RunInputs: {errs[0].message}")
        nodes = copy.deepcopy(by_id[c["workflow"]]["nodes"])
        for k, v in c["inputs"].items():
            nodes[k.split(".")[0]].setdefault("params", {})[k.split(".")[1]] = v
        if (got := media_rows(nodes, pal, takes)) != c["expect"]:
            fail(f"fixtures/video/media-rows.json {c['description']!r}: got {json.dumps(got)}")
    print("ok   fixtures/video/media-rows.json")


def registries(prefixes: list[str]) -> tuple[list[dict], dict]:
    """The workflows and palette of these canonical registry fixtures ("" is echo's)."""
    reg = HERE / "fixtures" / "registry"
    pal = {n["kind"]: n for pre in prefixes for n in json.loads((reg / f"{pre}nodes.valid.json").read_text())["nodes"]}
    return [w for pre in prefixes for w in json.loads((reg / f"{pre}workflows.valid.json").read_text())["workflows"]], pal


def check_video(registry: Registry, palette: dict) -> None:
    check_media(registry)
    check_rules()
    g = HERE / "fixtures" / "video"
    fx = json.loads((g / "auto-frames.json").read_text())
    for c in fx["cases"]:
        got = auto_frames(c["seconds"], c["fps"], c["min_s"], c["max_s"], fx["param"] | {"max": c["max"]})
        if got != c["frames"]:
            fail(f"fixtures/video/auto-frames.json {c}: got {got}")
    print("ok   fixtures/video/auto-frames.json")
    doc_v = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/WorkflowDoc"}, registry=registry)
    for c in json.loads((g / "keyframes.json").read_text())["cases"]:
        if errs := list(doc_v.iter_errors(c["workflow"])):
            fail(f"fixtures/video/keyframes.json {c['description']!r}: not a WorkflowDoc: {errs[0].message}")
        elif (got := position_errors(c["workflow"]["nodes"], palette)) != c["errors"]:
            fail(f"fixtures/video/keyframes.json {c['description']!r}: got {got}, want {c['errors']}")
    print("ok   fixtures/video/keyframes.json")
