"""contracts/check.py, timeline rules and the edit replays (README, "Timeline rules"; Magic: edits by ops).
Moved out of check.py as is; check.py re-exports every name."""

import copy
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry

from check_core import HERE, OPENAPI, fail, image_field, ratio, rnd
from check_video import take_uses

# ---------------------------------------------------------------- timeline rules (README, "Timeline rules")
# The frame-rate table: fps as JSON writes it → the exact rate num/den.
RATES = [(23.976, 24000, 1001), (24, 24, 1), (25, 25, 1), (29.97, 30000, 1001), (30, 30, 1), (48, 48, 1), (50, 50, 1),
         (59.94, 60000, 1001), (60, 60, 1)]
T_MAGNET_PX = 6  # an edge lands on a cut within this many screen pixels
T_IMAGE_HOLD_S = 3.0  # an image take added to V1 holds this long
T_MAX_AUDIO_TRACKS, T_MAX_CLIPS, T_MAX_BYTES, T_MAX_ALTERNATIVES, T_MAX_RERENDER = 8, 1000, 262144, 32, 22
T_MAX_MARKERS = 200  # 1.50: markers on a sequence
T_DUCK_DB, T_DUCK_EASE = -12, 0.3  # 1.106: ducking's default amount and the ease each side of a voice span (s)
# an export: at most 2 hours, an estimated 16 GiB written and 32 GiB of distinct takes read (cmd.media.job, runner)
T_MAX_EXPORT_S, T_MAX_EXPORT_BYTES, T_MAX_EXPORT_INPUTS = 7200, 16 << 30, 32 << 30


class Grid:
    """The target's frame and sample grids: f(s) = rnd(s × num / den), s(f) = f × den / num (in that order)."""

    def __init__(self, target: dict) -> None:
        self.num, self.den = next((n, d) for fps, n, d in RATES if abs(fps - target["fps"]) < 1e-6)
        self.sr = target["sample_rate"]

    def f(self, s: float) -> int:
        return rnd(s * self.num / self.den)

    def s(self, f: int) -> float:
        return f * self.den / self.num

    def snap(self, s: float) -> float:
        return self.s(self.f(s))

    def smp(self, s: float) -> int:
        return rnd(s * self.sr)


def t_kind(c: dict, takes: dict) -> str:
    """slot (no take: an empty slot, or from 1.32 a New line's placeholder on an audio track), video, image or
    (1.32) audio; an offline clip by its fields (hold = image)."""
    tid = c.get("take_id")
    return "slot" if not tid else takes[tid]["kind"] if tid in takes else "image" if "hold" in c else "video"


def t_len(c: dict, kind: str, g: Grid, audio: bool = False) -> int:
    """A clip's length in frames on the sequence (an audio-track clip: its end's frame minus its start's)."""
    if audio:
        return g.f(c["at"] + (c["hold"] if "hold" in c else c["out"] - c["in"])) - g.f(c["at"])
    return g.f(c["out"]) - g.f(c["in"]) if kind == "video" else g.f(c["hold"])


def a_len(c: dict, g: Grid) -> int:
    """An audio-track clip's length in target samples: out − in, or (1.32) a line placeholder's hold."""
    return g.smp(c["hold"]) if "hold" in c else g.smp(c["out"]) - g.smp(c["in"])


def shot_ref(shot: dict) -> dict:
    return {k: shot[k] for k in ("run_id", "batch_id", "step") if k in shot}


def t_normalize(doc: dict, takes: dict, previous: dict | None = None) -> tuple[dict | None, list]:
    """A written document (`TimelineDoc`) → (the normalized document, []) or (None, problems): every time snapped,
    V1 laid end to end from 0, defaults filled, track names given. takes: the caller's takes the document names
    (id → facts); a take that is missing is offline when `previous` (the stored revision) named it, else
    `take_not_found`. A `shot` must be the previous revision's for that clip (only studio makes one)."""
    probs = []
    add = lambda code, track=None, clip=None: probs.append([code, track, clip])
    if len(json.dumps(doc, separators=(",", ":"), ensure_ascii=False).encode()) > T_MAX_BYTES:
        return None, [["too_large", None, None]]
    tracks = doc["tracks"]
    if (not tracks or tracks[0]["kind"] != "video" or any(t["kind"] != "audio" for t in tracks[1:])
            or len(tracks) > 1 + T_MAX_AUDIO_TRACKS):
        return None, [["track_layout", None, None]]
    if sum(len(t["clips"]) for t in tracks) > T_MAX_CLIPS:
        return None, [["too_many_clips", None, None]]
    g = Grid(doc["target"])
    known, shots = set(), {}
    for t in (previous or {}).get("tracks", []):
        for c in t["clips"]:
            known |= {c.get("take_id")} | set(c.get("alternatives", []))
            if "shot" in c:
                shots[c["id"]] = shot_ref(c["shot"])
    ids, out, ends = set(), [], []
    v1ids = {c["id"] for c in tracks[0]["clips"]}
    for ti, t in enumerate(tracks):
        name, video, clips = ("V1" if ti == 0 else f"A{ti + 1}"), ti == 0, []
        for c in t["clips"]:
            cid, tid = c["id"], c.get("take_id")
            tk, kind = takes.get(tid), t_kind(c, takes)
            if cid in ids:
                add("duplicate_clip", name, cid)
            ids.add(cid)
            if kind == "audio" and video:  # 1.32: an audio take goes on A2-A9 only (README, Timeline rules)
                add("wrong_track", name, cid)
                continue
            if any(x not in takes and x not in known for x in [tid] * bool(tid) + c.get("alternatives", [])):
                add("take_not_found", name, cid)
            if "shot" in c and shots.get(cid) != shot_ref(c["shot"]):
                add("shot_invalid", name, cid)
            n = {"id": cid, "take_id": tid}
            if kind in ("video", "audio"):
                if "hold" in c:
                    add("hold_on_video", name, cid)
                end = c.get("out", tk["duration_s"] if tk else None)
                if end is None:
                    add("out_required", name, cid)
                    continue
                if video:
                    i_, o_ = g.f(c.get("in", 0)), g.f(end)
                    if tk and not (0 <= i_ and o_ - i_ >= 1 and o_ <= g.f(tk["duration_s"])):
                        add("trim", name, cid)
                    n |= {"in": g.s(i_), "out": g.s(o_)}
                else:
                    i_, o_ = g.smp(c.get("in", 0)), g.smp(end)
                    if tk and not (0 <= i_ < o_ <= g.smp(tk["duration_s"])):
                        add("trim", name, cid)
                    if tk and kind == "video" and not tk["has_audio"]:
                        add("no_audio", name, cid)
                    if "at" not in c:
                        add("at_required", name, cid)
                        continue
                    n |= {"in": i_ / g.sr, "out": o_ / g.sr, "at": g.snap(c["at"])}
            elif not video and not (kind == "slot" and "shot" in c):  # 1.32: a New line placeholder is the exception
                add("wrong_track", name, cid)
                continue
            elif not video:
                if "in" in c or "out" in c:
                    add("image_trim", name, cid)
                if "hold" not in c or "at" not in c:
                    add("hold_required" if "hold" not in c else "at_required", name, cid)
                    continue
                if g.f(c["hold"]) < 1:
                    add("hold", name, cid)
                n |= {"at": g.snap(c["at"]), "hold": g.snap(c["hold"])}
            else:
                if "in" in c or "out" in c:
                    add("image_trim", name, cid)
                if "hold" not in c:
                    add("hold_required", name, cid)
                    continue
                if g.f(c["hold"]) < 1:
                    add("hold", name, cid)
                n["hold"] = g.snap(c["hold"])
            n |= {"gain_db": c.get("gain_db", 0), "mute": c.get("mute", False)}
            if "link" in c:  # 1.50: an audio-track clip linked to a V1 clip (README, Timeline rules: linked clips)
                if video:
                    add("link_track", name, cid)
                elif c["link"] not in v1ids:
                    add("link_not_v1", name, cid)
                n["link"] = c["link"]
            if "transition_in" in c:
                if video:
                    n["transition_in"] = c["transition_in"]
                else:
                    add("dissolve_track", name, cid)
            for k in ("fade_in", "fade_out", "crossfade"):  # 1.106: on a clip with sound; a crossfade off V1
                if not c.get(k):
                    continue
                if k == "crossfade" and video:
                    add("crossfade_track", name, cid)
                elif kind not in ("video", "audio"):
                    add("fade_clip", name, cid)
                else:
                    n[k] = c[k]
            alts = list(dict.fromkeys(c.get("alternatives", []) + [tid] * bool(tid)))
            while len(alts) > T_MAX_ALTERNATIVES:  # the oldest that is not the current one goes
                alts.remove(next(a for a in alts if a != tid))
            n["alternatives"] = alts
            if "shot" in c:
                n["shot"] = shot_ref(c["shot"])
            if video and "number" in c:  # 1.29: studio gives it when it stores (README, Timeline rules: shot numbers)
                n["number"] = c["number"]
            if tid and not tk:
                n["offline"] = True
            clips.append((n, kind))
        if video:
            pos = 0
            for n, kind in clips:
                n["at"] = g.s(pos)
                pos += t_len(n, kind, g)
                t_fades(n, t_len(n, kind, g), g)
            din = [n.get("transition_in", {}).get("frames", 0) for n, _ in clips] + [0]
            for k, (n, kind) in enumerate(clips):
                d = din[k]
                if d and k == 0:
                    add("dissolve_first", name, n["id"])
                elif d and "slot" in (kind, clips[k - 1][1]):
                    add("dissolve_gap", name, n["id"])
                elif d:
                    p, pk = clips[k - 1]
                    ptk = takes.get(p["take_id"])
                    if (pk == "video" and ptk and g.f(p["out"]) + d // 2 > g.f(ptk["duration_s"])) or (
                            kind == "video" and g.f(n["in"]) - d // 2 < 0):
                        add("dissolve_handles", name, n["id"])
                half = (din[k] if k else 0) // 2 + din[k + 1] // 2
                if half and half > t_len(n, kind, g):
                    add("dissolve_length", name, n["id"])
        else:
            clips.sort(key=lambda x: x[0]["at"])
            prev_end = None
            for n, _ in clips:
                a, e = g.smp(n["at"]), g.smp(n["at"]) + a_len(n, g)
                if prev_end is not None and a < prev_end:
                    add("overlap", name, n["id"])
                prev_end = max(e, prev_end or 0)
                ends.append((name, n["id"], e))
            for n, _ in clips:  # 1.106: fades clamped; a crossfade into the clip on its end frame, else dropped
                ln = t_len(n, "video", g, audio=True)
                t_fades(n, ln, g)
                if "crossfade" in n:
                    nx = next((m for m, _ in clips if m is not n and g.f(m["at"]) == g.f(n["at"]) + ln), None)
                    d = min(g.f(n["crossfade"]), ln, t_len(nx, "video", g, audio=True)) if nx else 0
                    if d > 0:
                        n["crossfade"] = g.s(d)
                    else:
                        del n["crossfade"]
        out.append({"name": name, "kind": t["kind"], "gain_db": t.get("gain_db", 0), "mute": t.get("mute", False),
                    "clips": [n for n, _ in clips]})
    v1 = out[0]["clips"]
    if v1:
        frames = g.f(v1[-1]["at"]) + t_len(v1[-1], t_kind(v1[-1], takes), g)
        for name, cid, e in ends:
            if e > g.smp(g.s(frames)):
                add("past_end", name, cid)
    else:
        frames = max([g.f(e / g.sr) for _, _, e in ends], default=0)
    marks, mids = [], set()  # 1.50: markers, snapped to frames, sorted by time then id; none = no `markers`
    if len(doc.get("markers", [])) > T_MAX_MARKERS:
        add("too_many_markers")
    for m in doc.get("markers", []):
        if m["id"] in mids:
            add("duplicate_marker", None, m["id"])
        mids.add(m["id"])
        if v1 and g.f(m["at"]) > frames:
            add("marker_past_end", None, m["id"])
        marks.append({"id": m["id"], "at": g.snap(m["at"])} | {k: m[k] for k in ("name", "color") if k in m})
    if probs:
        return None, sorted(probs, key=lambda p: (p[0], p[1] or "", p[2] or ""))
    res = {"target": doc["target"], "tracks": out, "duration_s": g.s(frames), "frames": frames}
    if "ducking" in doc:  # 1.106
        res["ducking"] = doc["ducking"]
    if marks:
        res["markers"] = sorted(marks, key=lambda m: (m["at"], m["id"]))
    return res, []


def t_fades(n: dict, ln: int, g: Grid) -> None:
    """1.106: a clip's fades snapped to frames and clamped (fade_in ≤ its length ln, fade_out ≤ ln − fade_in); 0 is
    absent (README, Audio fades, crossfades and ducking)."""
    fi = min(g.f(n.get("fade_in", 0)), ln)
    fo = min(g.f(n.get("fade_out", 0)), ln - fi)
    for k, v in (("fade_in", fi), ("fade_out", fo)):
        if v > 0:
            n[k] = g.s(v)
        else:
            n.pop(k, None)


T_ALL_TRACKS = ("split_all", "remove_range", "lift_range")  # 1.50: ops that cut every track themselves


def t_edit(doc: dict, op: dict, takes: dict) -> dict | str:
    """One edit on a normalized document → the new document as written (normalize it next), or an error code when
    the edit cannot apply. The web draws with it; studio runs every op of POST …/edits with it. From 1.50 linked
    sound clips follow their V1 clip (t_follow)."""
    raw = t_edit_op(doc, op, takes)
    return raw if isinstance(raw, str) else t_follow(doc, raw, op, takes)


def t_follow(before: dict, raw: dict, op: dict, takes: dict) -> dict | str:
    """1.50, linked sound clips (README, Timeline rules: linked clips): an audio-track clip whose V1 clip the edit
    removed goes too; one whose V1 clip moved moves by as many frames (its content's place: start − in for a video
    that stays a video, else its start), except after the ops that cut every track themselves (T_ALL_TRACKS).
    Moved before 0: `link_start`."""
    if not any("link" in c for t in raw["tracks"][1:] for c in t["clips"]):
        return raw
    g = Grid(raw["target"])

    def anchors(d: dict) -> dict:  # a field an edit removed counts 0 (normalize refuses that document anyway)
        out, pos = {}, 0
        for c in d["tracks"][0]["clips"]:
            kind = t_kind(c, takes)
            i_ = g.f(c.get("in", 0))
            out[c["id"]] = (kind, pos, pos - i_ if kind == "video" else pos)
            pos += g.f(c.get("out", 0)) - i_ if kind == "video" else g.f(c.get("hold", 0))
        return out

    b, a = anchors(before), anchors(raw)
    for t in raw["tracks"][1:]:
        keep = []
        for c in t["clips"]:
            ln = c.get("link")
            if ln in b and ln not in a:
                continue
            if ln in b and op["op"] not in T_ALL_TRACKS:
                (kb, sb, nb), (ka, sa, na) = b[ln], a[ln]
                if sh := (na - nb if ka == kb else sa - sb):
                    if g.f(c["at"]) + sh < 0:
                        return "link_start"
                    c["at"] = g.s(g.f(c["at"]) + sh)
            keep.append(c)
        t["clips"] = keep
    return raw


def v1_starts(clips: list, takes: dict, g: Grid) -> tuple[list[int], int]:
    """V1's clip starts in frames, and its end (the clips' `at` may be stale while an edit runs)."""
    out, pos = [], 0
    for c in clips:
        out.append(pos)
        pos += t_len(c, t_kind(c, takes), g)
    return out, pos


def t_grow(c: dict, kind: str, df: int, g: Grid) -> None:
    """A V1 clip's end moves by df frames: a video's out, else its hold."""
    if kind == "video":
        c["out"] = g.s(g.f(c["out"]) + df)
    else:
        c["hold"] = g.s(g.f(c["hold"]) + df)


def t_front(c: dict, kind: str, df: int, g: Grid) -> None:
    """A V1 clip's start moves by df frames into it: a video's in, else its hold shrinks."""
    if kind == "video":
        c["in"] = g.s(g.f(c["in"]) + df)
    else:
        c["hold"] = g.s(g.f(c["hold"]) - df)


def t_inside(t: dict, f: int, on_v1: bool, takes: dict, g: Grid) -> tuple | None:
    """The clip of t strictly around frame f (f is not its first frame): (index, start, length), else None."""
    starts, hit = v1_starts(t["clips"], takes, g)[0] if on_v1 else None, None
    for k, c in enumerate(t["clips"]):
        a = starts[k] if on_v1 else g.f(c["at"])
        ln = t_len(c, t_kind(c, takes), g, audio=not on_v1)
        if a < f < a + ln:
            hit = (k, a, ln)
    return hit


def t_cut(t: dict, k: int, f: int, a: int, ln: int, nid: str, on_v1: bool, takes: dict, g: Grid) -> dict:
    """Cut clip k of t (start a, length ln frames) at frame f: the right part, inserted after it, gets nid, keeps the
    take, alternatives and link, never the dissolve or the shot."""
    c = t["clips"][k]
    c2 = {x: v for x, v in copy.deepcopy(c).items() if x not in ("transition_in", "shot")} | {"id": nid}
    if not on_v1:
        cut = g.smp(g.s(f)) - g.smp(c["at"]) + g.smp(c["in"])  # the left part ends on the right part's start sample
        c["out"], c2["in"], c2["at"] = cut / g.sr, cut / g.sr, g.s(f)
    elif t_kind(c, takes) == "video":
        c["out"] = c2["in"] = g.s(g.f(c["in"]) + f - a)
    else:
        c["hold"], c2["hold"] = g.s(f - a), g.s(ln - f + a)
    t["clips"].insert(k + 1, c2)
    return c2


def t_relink(d: dict, root: dict, takes: dict, g: Grid) -> None:
    """1.50: after V1 clips were cut (root: each new part's id → the clip it was cut from), a sound clip linked to one
    of the parts links to the part it starts in (the last starting at or before it; else the first)."""
    if not root:
        return
    v1 = d["tracks"][0]["clips"]
    parts = {}
    for c, a in zip(v1, v1_starts(v1, takes, g)[0]):
        parts.setdefault(root.get(c["id"], c["id"]), []).append((a, c["id"]))
    for t in d["tracks"][1:]:
        for c in t["clips"]:
            if "link" in c and (ps := parts.get(root.get(c["link"], c["link"]))):
                c["link"] = ([pid for a, pid in ps if a <= g.f(c["at"])] or [ps[0][1]])[-1]


def t_edit_op(doc: dict, op: dict, takes: dict) -> dict | str:
    """t_edit's op itself."""
    d, g = copy.deepcopy(doc), Grid(doc["target"])
    tracks = {t["name"]: t for t in d["tracks"]}
    v1 = d["tracks"][0]["clips"]

    def find(cid: str) -> tuple[dict, int]:
        return next((t, i) for t in d["tracks"] for i, c in enumerate(t["clips"]) if c["id"] == cid)

    def din(i: int) -> int:
        return v1[i].get("transition_in", {}).get("frames", 0) if 0 < i < len(v1) else 0

    def cut_at(i: int) -> None:  # the cut before v1[i] changed: its dissolve goes
        if i < len(v1):
            v1[i].pop("transition_in", None)

    o = op["op"]
    if o == "add":
        t = tracks[op.get("track", "V1")]
        for tid, cid in zip(op["take_ids"], op["ids"]):
            tk = takes[tid]
            if t["name"] != "V1":  # at the first frame at or after the track's end (in samples): never an overlap
                end = max((g.smp(c["at"]) + g.smp(c["out"]) - g.smp(c["in"]) for c in t["clips"]), default=0)
                f = g.f(end / g.sr)
                f += g.smp(g.s(f)) < end
                t["clips"].append({"id": cid, "take_id": tid, "in": 0, "out": tk["duration_s"], "at": g.s(f)})
            elif tk["kind"] == "image":
                t["clips"].append({"id": cid, "take_id": tid, "hold": T_IMAGE_HOLD_S})
            else:
                t["clips"].append({"id": cid, "take_id": tid, "in": 0, "out": tk["duration_s"]})
        return d
    cuts = [0]
    for c in v1:
        cuts.append(cuts[-1] + t_len(c, t_kind(c, takes), g))
    if o == "place":  # 1.32, Make shot: an empty slot at exactly frames(at) (README, Timeline: Make shot)
        f, n = g.f(op["at"]), g.f(op["hold"])
        new = {"id": op["id"], "take_id": None, "hold": g.s(n)}
        if f >= cuts[-1]:  # past V1's end: a gap up to it first
            v1 += [{"id": op["gap_id"], "take_id": None, "hold": g.s(f - cuts[-1])}] * (f > cuts[-1]) + [new]
            return d
        k = max(i for i in range(len(v1)) if cuts[i] <= f)
        gap = v1[k]
        if gap.get("take_id") or "shot" in gap:  # inside a take's clip or a running shot: the insert edit
            o, op = "insert", {"at": op["at"], "hold": g.s(n), "id": op["id"]}
        else:  # inside an empty slot: split it around the new slot
            left, right = f - cuts[k], cuts[k + 1] - f - n
            parts = ([gap | {"hold": g.s(left)}] if left else []) + [new] + \
                ([{"id": op["gap_id"] if left else gap["id"], "take_id": None, "hold": g.s(right)}] if right > 0 else [])
            v1[k:k + 1] = parts
            cut_at(k + len(parts))
            for t in d["tracks"][1:] if right < 0 else []:  # longer than the gap: an insert of what overflows, so
                for c in t["clips"]:  # every track moves on together after the gap (V1 rippled; the audio too)
                    if g.f(c["at"]) >= cuts[k + 1]:
                        c["at"] = g.s(g.f(c["at"]) - right)
            return d
    if o == "insert":  # an empty slot at the V1 cut nearest the playhead (a tie: the later cut)
        f = g.f(op["at"])
        i = min(range(len(cuts)), key=lambda k: (abs(cuts[k] - f), -k))
        v1.insert(i, {"id": op["id"], "take_id": None, "hold": op["hold"]})
        cut_at(i + 1)
        return d
    if o in ("add_track", "remove_track", "track"):
        if o == "add_track":
            if len(d["tracks"]) > T_MAX_AUDIO_TRACKS:
                return "too_many_tracks"
            d["tracks"].append({"kind": "audio", "gain_db": 0, "mute": False, "clips": []})
        elif o == "remove_track":
            if op["track"] == "V1":
                return "video_track"
            d["tracks"].remove(tracks[op["track"]])
        else:
            tracks[op["track"]] |= {k: op[k] for k in ("gain_db", "mute") if k in op}
        for t in d["tracks"]:
            t.pop("name", None)  # names follow the position
        return d
    if o == "split":
        return t_split(d, tracks[op["track"]], op, takes, g)
    if o == "split_all":  # 1.50: blade on every track at frame `at` (README, Timeline rules: tools)
        f, ids, root, cut = g.f(op["at"]), iter(op.get("ids", [])), {}, False
        for ti, t in enumerate(d["tracks"]):
            hit = t_inside(t, f, ti == 0, takes, g)
            if hit is None or "shot" in t["clips"][hit[0]]:
                continue
            if (nid := next(ids, None)) is None:
                return "ids_short"
            t_cut(t, hit[0], f, hit[1], hit[2], nid, ti == 0, takes, g)
            if ti == 0:
                root[nid] = t["clips"][hit[0]]["id"]
            cut = True
        if not cut:
            return "split_none"
        t_relink(d, root, takes, g)
        return d
    if o in ("remove_range", "lift_range"):  # 1.50: a range on every track, cut at both ends
        lo, hi = g.f(op["from"]), g.f(op["to"])
        if hi <= lo:
            return "range_empty"
        for ti, t in enumerate(d["tracks"]):
            starts = v1_starts(t["clips"], takes, g)[0] if ti == 0 else None
            for k, c in enumerate(t["clips"]):
                a = starts[k] if ti == 0 else g.f(c["at"])
                if "shot" in c and a < hi and a + t_len(c, t_kind(c, takes), g, audio=ti > 0) > lo:
                    return "range_shot"
        ids, root = iter(op.get("ids", [])), {}
        for ti, t in enumerate(d["tracks"]):
            for f in (lo, hi):
                if hit := t_inside(t, f, ti == 0, takes, g):
                    if (nid := next(ids, None)) is None:
                        return "ids_short"
                    t_cut(t, hit[0], f, hit[1], hit[2], nid, ti == 0, takes, g)
                    if ti == 0:
                        root[nid] = root.get(t["clips"][hit[0]]["id"], t["clips"][hit[0]]["id"])
        t_relink(d, root, takes, g)
        starts, end = v1_starts(v1, takes, g)
        gone = [k for k, a in enumerate(starts) if lo <= a < hi]
        if gone:
            del v1[gone[0]:gone[-1] + 1]
            if o == "lift_range":
                if (nid := next(ids, None)) is None:
                    return "ids_short"
                v1.insert(gone[0], {"id": nid, "take_id": None, "hold": g.s(min(hi, end) - lo)})
            cut_at(gone[0] + (o == "lift_range"))
        for t in d["tracks"][1:]:
            keep = []
            for c in t["clips"]:
                a = g.f(c["at"])
                if lo <= a < hi:
                    continue
                if a >= hi and o == "remove_range":  # open point 4: audio tracks ripple too
                    c["at"] = g.s(a - (hi - lo))
                keep.append(c)
            t["clips"] = keep
        return d
    if o == "marker":  # 1.50: a marker put (a new id adds one; a known id is replaced whole)
        d["markers"] = [m for m in d.get("markers", []) if m["id"] != op["id"]] + [
            {"id": op["id"], "at": op["at"]} | {k: op[k] for k in ("name", "color") if k in op}]
        return d
    if o == "remove_marker":
        if not any(m["id"] == op["id"] for m in d.get("markers", [])):
            return "unknown_marker"
        d["markers"] = [m for m in d["markers"] if m["id"] != op["id"]]
        return d
    if o == "ducking":  # 1.106: the sequence's Duck music (absent fields kept; a sequence without it is off)
        was = d.get("ducking") or {"on": False, "db": T_DUCK_DB}
        d["ducking"] = {"on": op.get("on", was["on"]), "db": op.get("db", was.get("db", T_DUCK_DB))}
        return d
    t, i = find(op["clip"])
    c, kind, on_v1 = t["clips"][i], t_kind(t["clips"][i], takes), t is d["tracks"][0]
    if o == "move":
        if on_v1:
            v1.pop(i)
            cut_at(i)
            v1.insert(op["index"], c)
            c.pop("transition_in", None)
            cut_at(op["index"] + 1)
        else:
            t["clips"].pop(i)
            tracks[op.get("track", t["name"])]["clips"].append(c | {"at": op["at"]})
    elif o == "trim":
        df = g.f(op["delta"])
        src = g.f(takes[c["take_id"]]["duration_s"]) if kind in ("video", "audio") and c.get("take_id") in takes else None
        if on_v1:
            d_in, d_out = din(i), din(i + 1)
            least = max(1, d_in // 2 + d_out // 2)
            if kind == "video":
                i_, o_ = g.f(c["in"]), g.f(c["out"])
                if op["edge"] == "in":
                    i_ = min(max(i_ + df, d_in // 2), o_ - least)
                else:
                    o_ = max(o_ + df, i_ + least)
                    o_ = o_ if src is None else min(o_, src - d_out // 2)
                c["in"], c["out"] = g.s(i_), g.s(o_)
            else:
                c["hold"] = g.s(max(g.f(c["hold"]) + (df if op["edge"] == "out" else -df), least))
        elif kind == "slot":  # 1.32: a line placeholder's hold, its start kept
            c["hold"] = g.s(max(g.f(c["hold"]) + (df if op["edge"] == "out" else -df), 1))
        else:
            i_, o_, frame = g.smp(c["in"]), g.smp(c["out"]), g.smp(g.s(1))
            if op["edge"] == "in":
                df = max(df, -g.f(c["at"]))
                ni = min(max(i_ + g.smp(g.s(df)), 0), o_ - frame)
                c["at"], c["in"] = g.snap(c["at"] + (ni - i_) / g.sr), ni / g.sr
            else:
                no = max(o_ + g.smp(g.s(df)), i_ + frame)  # 1.32: a video's sound or an audio take ends at the take's end
                c["out"] = (no if src is None else min(no, g.smp(takes[c["take_id"]]["duration_s"]))) / g.sr
    elif o in ("remove", "lift"):
        if o == "lift" and not on_v1:
            return "lift_track"
        if o == "lift":
            t["clips"][i] = {"id": c["id"], "take_id": None, "hold": g.s(t_len(c, kind, g))}
        else:
            t["clips"].pop(i)
        if on_v1:
            cut_at(i if o == "remove" else i + 1)
        elif o == "remove" and op.get("ripple"):  # 1.50: the later clips of its audio track move left by its length
            a, ln = g.f(c["at"]), t_len(c, kind, g, audio=True)
            for x in t["clips"]:
                if g.f(x["at"]) >= a:
                    x["at"] = g.s(g.f(x["at"]) - ln)
    elif o == "roll":  # 1.50: the cut before the clip moves by delta; the sequence's length is kept
        if not on_v1:
            return "roll_track"
        if i == 0:
            return "roll_first"
        df, p = g.f(op["delta"]), v1[i - 1]
        pk = t_kind(p, takes)
        if (t_len(p, pk, g) + df < max(1, din(i - 1) // 2 + din(i) // 2)
                or t_len(c, kind, g) - df < max(1, din(i) // 2 + din(i + 1) // 2)):
            return "least"
        t_grow(p, pk, df, g)
        t_front(c, kind, df, g)
    elif o == "slip":  # 1.50: in and out move by delta; place and length kept
        if kind in ("image", "slot"):
            return "slip_still"
        df = g.f(op["delta"])
        if on_v1:
            c["in"], c["out"] = g.s(g.f(c["in"]) + df), g.s(g.f(c["out"]) + df)
        else:  # on the sample grid
            ds = g.smp(g.s(df))
            c["in"], c["out"] = (g.smp(c["in"]) + ds) / g.sr, (g.smp(c["out"]) + ds) / g.sr
    elif o == "slide":  # 1.50: the clip moves along V1 by delta; its neighbours give and take the frames
        if not on_v1:
            return "slide_track"
        if i == 0 or i == len(v1) - 1:
            return "slide_end"
        df, p, nx = g.f(op["delta"]), v1[i - 1], v1[i + 1]
        pk, nk = t_kind(p, takes), t_kind(nx, takes)
        if (t_len(p, pk, g) + df < max(1, din(i - 1) // 2 + din(i) // 2)
                or t_len(nx, nk, g) - df < max(1, din(i + 1) // 2 + din(i + 2) // 2)):
            return "least"
        t_grow(p, pk, df, g)
        t_front(nx, nk, df, g)
    elif o == "overwrite":
        return t_overwrite(d, t, i, op, takes, g)
    elif o == "replace":
        if op["take_id"] not in c.get("alternatives", []):
            return "not_alternative"
        if op["take_id"] not in takes:
            return "take_not_found"
        tk, ln = takes[op["take_id"]], t_len(c, kind, g, audio=not on_v1)
        old_in = g.f(c["in"]) if kind in ("video", "audio") else 0
        for k in ("in", "out", "hold"):
            c.pop(k, None)
        if tk["kind"] == "image" and on_v1:
            c["hold"] = g.s(ln)
        else:
            src = g.f(tk["duration_s"])
            i_ = min(old_in, src - 1)
            c["in"], c["out"] = g.s(i_), g.s(min(i_ + ln, src))
        c["take_id"] = op["take_id"]
    elif o == "set":
        for k in ("gain_db", "mute", "hold", "transition_in", "link", "fade_in", "fade_out", "crossfade"):
            if k in op:  # link: 1.50; the fades and crossfade: 1.106 (0 removes them too)
                if op[k] is None or (op[k] == 0 and "fade" in k):
                    c.pop(k, None)
                else:
                    c[k] = op[k]
    elif o == "fill":  # studio: a shot's take fills its slot (README, Timeline: New shot; take actions)
        tk, ln, cur = takes[op["take_id"]], t_len(c, kind, g), takes.get(c.get("take_id")) or {}
        sm, i0, o0 = tk.get("source_map"), c.get("in"), c.get("out")
        for k in ("in", "out", "hold", "shot"):
            c.pop(k, None)
        if not on_v1:  # 1.32, an audio track (New line, a line's re-render): the whole take at the slot's start
            c["in"], c["out"] = 0, tk["duration_s"]
        elif tk["kind"] == "image":
            c["hold"] = g.s(ln)
        elif sm:  # the generated part; replacing its source (or a take made from it): the clip's window, grown on a
            # side only where the generated part lies outside the source (extend), never by the 8-frame snap (retake)
            a, b, off = sm["new_in_s"], sm["new_out_s"], sm["offset_s"]
            mine = 0 if c.get("take_id") == sm["take_id"] else \
                cur.get("source_map", {}).get("offset_s") if cur.get("source_map", {}).get("take_id") == sm["take_id"] else None
            if mine is not None and o0 is not None:
                end = off + takes[sm["take_id"]]["duration_s"]
                a = a if g.f(a) < g.f(off) else i0 - mine + off
                b = b if g.f(b) > g.f(end) else o0 - mine + off
            c["in"], c["out"] = g.s(max(g.f(a), 0)), g.s(min(g.f(b), g.f(tk["duration_s"])))
        else:
            c["in"], c["out"] = 0, g.s(min(g.f(tk["duration_s"]), ln))
        c["take_id"] = op["take_id"]
        c["alternatives"] = c.get("alternatives", []) + [op["take_id"]]
    else:
        return "unknown_op"
    return d


def t_split(d: dict, t: dict, op: dict, takes: dict, g: Grid) -> dict | str:
    """Split at the playhead (`at`, sequence seconds) the clip of `track` under it; the right part gets `id`, keeps
    the take and alternatives, never the dissolve or the shot. From 1.50 a V1 split cuts the clip's linked sound
    clips at that frame too (`ids`, in track order) and relinks them (t_relink)."""
    f, on_v1 = g.f(op["at"]), t is d["tracks"][0]
    hit = None
    for k, c in enumerate(t["clips"]):
        a, ln = g.f(c["at"]), t_len(c, t_kind(c, takes), g, audio=not on_v1)
        if a <= f < a + ln:
            hit = (k, c, f - a, ln)
    if hit is None:
        return "split_none"
    k, c, p, ln = hit
    if p == 0:
        return "split_edge"
    if "shot" in c:
        return "split_shot"
    t_cut(t, k, f, f - p, ln, op["id"], on_v1, takes, g)
    if on_v1:
        ids = iter(op.get("ids", []))
        for at in d["tracks"][1:]:
            hit = t_inside(at, f, False, takes, g)
            if hit and at["clips"][hit[0]].get("link") == c["id"] and "shot" not in at["clips"][hit[0]]:
                if (nid := next(ids, None)) is None:
                    return "ids_short"
                t_cut(at, hit[0], f, hit[1], hit[2], nid, False, takes, g)
        t_relink(d, {op["id"]: c["id"]}, takes, g)
    return d


def t_overwrite(d: dict, t: dict, i: int, op: dict, takes: dict, g: Grid) -> dict | str:
    """1.50, Overwrite: the clip goes in at `at` over what is there. V1: it leaves an empty slot of its length, the
    frames [at, at + length) are cut out of V1 (a clip across both ends is cut in two) and it goes in there, an empty
    slot first past V1's end; no ripple. Audio (on `track`, default its own): the clips under it are cut around it, a
    right part starting on the first frame at or after its end. New ids from `ids`: a right part, then (V1) the slot
    it leaves, then the gap past the end."""
    c, ids = t["clips"][i], iter(op.get("ids", []))
    v1 = d["tracks"][0]["clips"]
    if t is d["tracks"][0]:
        if op.get("track", "V1") != "V1":
            return "overwrite_track"
        kind = t_kind(c, takes)
        n, lo = t_len(c, kind, g), g.f(op["at"])
        hi = lo + n
        moved = {k: v for k, v in c.items() if k != "transition_in"}
        starts, end = v1_starts(v1, takes, g)
        hole = {"id": None, "take_id": None, "hold": g.s(n)}
        v1[i] = hole
        if i + 1 < len(v1):
            v1[i + 1].pop("transition_in", None)
        parts = []
        for x, a in zip(v1, starts):
            xk = t_kind(x, takes)
            parts.append((x, a, a + t_len(x, xk, g), xk))
        if any("shot" in x and a < hi and b > lo for x, a, b, _ in parts):
            return "overwrite_shot"
        right, root = None, {}
        if span := next((p for p in parts if p[1] < lo and p[2] > hi), None):
            if (nid := next(ids, None)) is None:
                return "ids_short"
            right = {k: v for k, v in copy.deepcopy(span[0]).items() if k not in ("transition_in", "shot")} | {"id": nid}
            t_front(right, span[3], hi - span[1], g)
            root[nid] = span[0]["id"]
        if (hole_id := next(ids, None)) is None:
            return "ids_short"
        hole["id"], gap = hole_id, []
        if lo > end:
            if (gid := next(ids, None)) is None:
                return "ids_short"
            gap = [{"id": gid, "take_id": None, "hold": g.s(lo - end)}]
        before, after = [], [right] if right else []
        for x, a, b, xk in parts:
            if b <= lo:
                before.append(x)
            elif a >= hi:
                after.append(x)
            elif a < lo:
                t_grow(x, xk, lo - b, g)
                before.append(x)
            elif b > hi:
                t_front(x, xk, hi - a, g)
                after.append(x)
        if after:
            after[0].pop("transition_in", None)
        v1[:] = before + gap + [moved] + after
        t_relink(d, root, takes, g)
        return d
    name = op.get("track", t["name"])
    if name not in [x["name"] for x in d["tracks"]]:
        return "unknown_track"
    tt = next(x for x in d["tracks"] if x["name"] == name)
    if tt is d["tracks"][0]:
        return "overwrite_track"
    t["clips"].pop(i)
    at = g.snap(op["at"])
    lo = g.smp(at)
    hi = lo + a_len(c, g)
    fe = g.f(hi / g.sr)
    fe += g.smp(g.s(fe)) < hi  # the first frame at or after its end
    re = g.smp(g.s(fe))
    under = lambda x: g.smp(x["at"]) < hi and g.smp(x["at"]) + a_len(x, g) > lo
    if any("shot" in x for x in tt["clips"] if under(x)):
        return "overwrite_shot"
    keep = []
    for x in sorted(tt["clips"], key=lambda x: g.smp(x["at"])):
        xs, xe = g.smp(x["at"]), g.smp(x["at"]) + a_len(x, g)
        if not under(x):
            keep.append(x)
            continue
        i_s = g.smp(x["in"])
        if xs < lo:
            x2 = copy.deepcopy(x) | {"in": (i_s + re - xs) / g.sr, "at": g.s(fe)}
            x["out"] = (i_s + lo - xs) / g.sr
            keep.append(x)
            if xe > re:
                if (nid := next(ids, None)) is None:
                    return "ids_short"
                keep.append(x2 | {"id": nid})
        elif xe > re:
            x["in"], x["at"] = (i_s + re - xs) / g.sr, g.s(fe)
            keep.append(x)
    tt["clips"] = keep + [c | {"at": at}]
    return d


def t_apply(doc: dict, op: dict, takes: dict) -> dict:
    """An edit, then normalize against the edited document as the previous revision. `fill` then drops a dissolve
    the new take cannot carry and cuts an audio-track clip a shorter V1 now ends inside (removed when it starts at
    or after the end), so a fill never fails."""
    raw = t_edit(doc, op, takes)
    if isinstance(raw, str):
        return {"error": raw}
    n, probs = t_normalize(raw, takes, doc)
    if op["op"] == "fill" and probs:
        g = Grid(raw["target"])
        bad = {p[2] for p in probs if p[0] in ("dissolve_handles", "dissolve_length")}
        past = {p[2] for p in probs if p[0] == "past_end"}
        for t in raw["tracks"][1:]:  # 1.32: a filled line running into the next clip of its track is cut at its start
            order = sorted(t["clips"], key=lambda c: g.smp(c["at"]))
            for prev, c in zip(order, order[1:]):
                if [p for p in probs if p[0] == "overlap" and p[2] == c["id"]] and "out" in prev:
                    prev["out"] = (g.smp(prev["in"]) + g.smp(c["at"]) - g.smp(prev["at"])) / g.sr
        end = g.smp(g.s(sum(t_len(c, t_kind(c, takes), g) for c in raw["tracks"][0]["clips"])))
        for c in raw["tracks"][0]["clips"]:
            if c["id"] in bad:
                c.pop("transition_in", None)
        for t in raw["tracks"][1:]:
            for c in [c for c in t["clips"] if c["id"] in past]:
                if (a := g.smp(c["at"])) >= end:
                    t["clips"].remove(c)
                elif "hold" in c:  # 1.32: a line placeholder (never longer than it is: it may be cut already)
                    c["hold"] = min(g.smp(c["hold"]), end - a) / g.sr
                else:  # never past the out it has (a line cut at the next clip above stays cut)
                    c["out"] = min(g.smp(c["out"]), g.smp(c["in"]) + end - a) / g.sr
        n, probs = t_normalize(raw, takes, doc)
    return {"document": n} if n else {"problems": probs}


def t_cuts(doc: dict, takes: dict, exclude: str | None = None) -> list[float]:
    """What an edge snaps to: 0 and every clip's start and end on every track, but the dragged clip's own."""
    g, cuts = Grid(doc["target"]), {0.0}
    for ti, t in enumerate(doc["tracks"]):
        for c in t["clips"]:
            if c["id"] != exclude:
                cuts |= {c["at"], g.s(g.f(c["at"]) + t_len(c, t_kind(c, takes), g)) if ti == 0
                         else c["at"] + (c["hold"] if "hold" in c else c["out"] - c["in"])}
    return sorted(cuts)


def snap_point(t: float, cuts: list[float], px_per_s: float, g: Grid) -> float:
    """A dragged edge: the nearest cut when within T_MAGNET_PX on screen (a tie: the earlier), else its frame."""
    best = min(cuts, key=lambda c: (abs(c - t), c), default=None)
    return best if best is not None and abs(best - t) * px_per_s <= T_MAGNET_PX else g.snap(t)


def drop_index(doc: dict, clip: str, mid: float, takes: dict) -> int:
    """Moving a V1 clip: its index among the others = how many others have their midpoint before the dragged
    clip's midpoint `mid` (sequence seconds)."""
    g = Grid(doc["target"])
    m2 = 2 * g.f(mid)
    return sum(1 for c in doc["tracks"][0]["clips"] if c["id"] != clip
               and 2 * g.f(c["at"]) + t_len(c, t_kind(c, takes), g) < m2)


def frame_at(doc: dict, f: int, takes: dict) -> dict:
    """What the preview shows and plays at sequence frame f: pictures bottom first ({clip, t (source seconds; null
    for an image), opacity, offline?}) and sounds ({clip, t, gain_db = track + clip, fade}); a dissolve of d frames
    centred on its cut runs over [cut − d/2, cut + d/2): the incoming picture's opacity and sound fade are
    (f − (cut − d/2)) / d, the outgoing sound's 1 minus that. Muted clips and tracks, empty slots, images and takes
    without sound play no sound; an offline clip shows as offline and plays nothing."""
    g, pics, sounds = Grid(doc["target"]), [], []
    v1t = doc["tracks"][0]
    v1 = v1t["clips"]
    dins = [c.get("transition_in", {}).get("frames", 0) for c in v1] + [0]
    for k, c in enumerate(v1):
        kind, a = t_kind(c, takes), g.f(c["at"])
        ln = t_len(c, kind, g)
        start, end = a - dins[k] // 2, a + ln + dins[k + 1] // 2
        if kind == "slot" or not start <= f < end:
            continue
        mix_in = (f - start) / dins[k] if dins[k] and f < a + dins[k] // 2 else 1
        mix_out = (f - (a + ln - dins[k + 1] // 2)) / dins[k + 1] if dins[k + 1] and f >= a + ln - dins[k + 1] // 2 else 0
        src = g.s(g.f(c["in"]) + f - a) if kind == "video" else None
        pic = {"clip": c["id"], "t": src, "opacity": mix_in}
        if c.get("offline"):
            pics.append(pic | {"offline": True})
            continue
        pics.append(pic)
        if kind == "video" and takes[c["take_id"]]["has_audio"] and not c["mute"] and not v1t["mute"]:
            sounds.append({"clip": c["id"], "t": src, "gain_db": v1t["gain_db"] + c["gain_db"],
                           "fade": (mix_in if mix_in < 1 else 1 - mix_out) * t_ramp(f, a, a + ln, c, g)})
    duck = t_duck(doc, takes)
    for t in doc["tracks"][1:]:
        for c in t["clips"]:
            if not c.get("take_id") or c.get("offline") or c["mute"] or t["mute"]:
                continue
            a = g.f(c["at"])
            end = a + t_len(c, "video", g, audio=True)
            x, prev = t_xfade(doc, c, takes), t_into(doc, t, c, takes)
            if not a <= f < (end + x[0] if x and x[1] == "blend" else end):
                continue
            fade = 1
            if prev:  # 1.106: the incoming side of a crossfade (its own fade in does not play)
                h = prev[0] if prev[1] == "blend" else prev[0] / 2
                if f < a + h:
                    fade = (f - a) / h
            else:
                fade = t_ramp(f, a, end, {"fade_in": c.get("fade_in")}, g)
            if x and x[1] == "blend":  # the outgoing side plays on past its end (its own fade out does not play)
                if f >= end:
                    fade *= (end + x[0] - f) / x[0]
            elif x:
                if f >= end - x[0] / 2:
                    fade *= (end - f) / (x[0] / 2)
            else:
                fade *= t_ramp(f, a, end, {"fade_out": c.get("fade_out")}, g)
            if duck and takes[c["take_id"]].get("sound") == "music":
                fade *= 10 ** (duck[0] * t_envelope(g.s(f), duck[1]) / 20)
            sounds.append({"clip": c["id"], "t": c["in"] + g.s(f - a) if f < end else c["out"] + g.s(f - end),
                           "gain_db": t["gain_db"] + c["gain_db"], "fade": fade})
    return {"pictures": pics, "sounds": sounds}


def t_ramp(f: int, a: int, end: int, c: dict, g: Grid) -> float:
    """1.106: a clip's own fade in and out at frame f over [a, end), linear in amplitude, never below 0 (a dissolve's
    handle before a or after end is silent under a fade)."""
    fi, fo, k = g.f(c.get("fade_in") or 0), g.f(c.get("fade_out") or 0), 1
    if fi and f < a + fi:
        k *= max(0, (f - a) / fi)
    if fo and f >= end - fo:
        k *= max(0, (end - f) / fo)
    return k


def t_xfade(doc: dict, c: dict, takes: dict) -> tuple | None:
    """1.106: an audio-track clip's Crossfade into next as (frames, blend | dip): a blend when its take has that much
    sound past `out`, in samples (an offline take dips); None without one."""
    if not c.get("crossfade") or not c.get("take_id") or "hold" in c:
        return None
    g, tk = Grid(doc["target"]), None if c.get("offline") else takes.get(c["take_id"])
    blend = bool(tk and tk.get("duration_s")) and g.smp(c["out"]) + g.smp(c["crossfade"]) <= g.smp(tk["duration_s"])
    return g.f(c["crossfade"]), "blend" if blend else "dip"


def t_into(doc: dict, t: dict, c: dict, takes: dict) -> tuple | None:
    """1.106: the crossfade of the clip on track t that ends on c's start frame, if it has one."""
    g = Grid(doc["target"])
    p = next((x for x in t["clips"] if x is not c and x.get("crossfade")
              and g.f(x["at"]) + t_len(x, "video", g, audio=True) == g.f(c["at"])), None)
    return t_xfade(doc, p, takes) if p else None


def t_envelope(t: float, spans: list) -> float:
    """1.106: the duck envelope at t (seconds) over the voice spans [a, b): 1 inside, 0 away, linear over the ease."""
    e = 0
    for a, b in spans:
        e = max(e, min(1, max(0, min((t - (a - T_DUCK_EASE)) / T_DUCK_EASE, (b + T_DUCK_EASE - t) / T_DUCK_EASE))))
    return e


def t_duck(doc: dict, takes: dict) -> tuple | None:
    """1.106: the sequence's ducking when on: (db, the voice clips' sounding spans in seconds, a blend's tail in), else
    None. A voice clip is an audio-track clip whose take's class is voice, not muted (clip or track), not offline."""
    if not doc.get("ducking", {}).get("on"):
        return None
    g, spans = Grid(doc["target"]), []
    for t in doc["tracks"][1:]:
        for c in t["clips"]:
            if t["mute"] or c["mute"] or c.get("offline") or not c.get("take_id") \
                    or takes.get(c["take_id"], {}).get("sound") != "voice":
                continue
            x, a = t_xfade(doc, c, takes), g.f(c["at"])
            spans.append([g.s(a), g.s(a + t_len(c, "video", g, audio=True) + (x[0] if x and x[1] == "blend" else 0))])
    return doc["ducking"].get("db", T_DUCK_DB), spans


DIFF_FIELDS = ("take_id", "in", "out", "hold", "at", "gain_db", "mute", "transition_in", "alternatives", "shot",
               "offline", "track", "link", "fade_in", "fade_out", "crossfade")  # link: 1.50; fades: 1.106


def t_diff(before: dict, after: dict) -> dict:
    """What changed from one normalized document to another (README, Timeline: edits by ops), by clip id: added (in
    the new document's order), removed (in the old one's), changed fields as [before, after] (absent = null; V1 has
    `at`, so a ripple shows as `at` changes), tracks by name (gain_db, mute) and [before, after] duration_s."""
    def clips(d: dict) -> dict:
        return {c["id"]: c | {"track": t["name"]} for t in d["tracks"] for c in t["clips"]}

    a, b = clips(before), clips(after)
    changed = []
    for cid, c in b.items():
        if cid in a and (f := {k: [a[cid].get(k), c.get(k)] for k in DIFF_FIELDS if a[cid].get(k) != c.get(k)}):
            changed.append({"clip": cid, "fields": f})
    ta, tb = ({t["name"]: t for t in d["tracks"]} for d in (before, after))
    tchanged = []
    for n, t in tb.items():
        if n in ta and (f := {k: [ta[n].get(k), t.get(k)] for k in ("gain_db", "mute") if ta[n].get(k) != t.get(k)}):
            tchanged.append({"track": n, "fields": f})
    out = {"added": [i for i in b if i not in a], "removed": [i for i in a if i not in b], "changed": changed,
           "tracks": {"added": [n for n in tb if n not in ta], "removed": [n for n in ta if n not in tb],
                      "changed": tchanged},
           "duration": [before["duration_s"], after["duration_s"]], "markers": t_diff_markers(before, after)}
    if before.get("ducking") != after.get("ducking"):  # 1.106: only when it changed
        out["ducking"] = [before.get("ducking"), after.get("ducking")]
    return out


def t_diff_markers(before: dict, after: dict) -> dict:
    """1.50: markers by id: added (in the new order), removed, changed fields (at, name, color) as [before, after]."""
    ma, mb = ({m["id"]: m for m in d.get("markers", [])} for d in (before, after))
    changed = [{"marker": i, "fields": f} for i, m in mb.items() if i in ma and (
        f := {k: [ma[i].get(k), m.get(k)] for k in ("at", "name", "color") if ma[i].get(k) != m.get(k)})]
    return {"added": [i for i in mb if i not in ma], "removed": [i for i in ma if i not in mb], "changed": changed}


def t_agent_view(doc: dict, takes: dict) -> dict:
    """A normalized document flat for an agent (README, Timeline: edits by ops; studio adds id, name, revision and
    each clip's take_title): every clip track by track with its index, kind, times in seconds and frames (target
    grid; an audio-track clip's length in seconds is its own, hold or out − in), dissolve frames, shot and number;
    from 1.50 each clip's link, the linked groups ({V1 clip: [sound clips]}) and the markers (with at_f)."""
    g, tracks, clips = Grid(doc["target"]), [], []
    for ti, t in enumerate(doc["tracks"]):
        tracks.append({"name": t["name"], "kind": t["kind"], "gain_db": t["gain_db"], "mute": t["mute"],
                       "clips": len(t["clips"])})
        for i, c in enumerate(t["clips"]):
            kind, a = t_kind(c, takes), g.f(c["at"])
            ln = t_len(c, kind, g, audio=ti > 0)
            length = (c["hold"] if "hold" in c else c["out"] - c["in"]) if ti else g.s(ln)
            clips.append({"id": c["id"], "track": t["name"], "index": i, "take_id": c.get("take_id"), "kind": kind,
                          "in": c.get("in"), "out": c.get("out"), "hold": c.get("hold"), "at": c["at"],
                          "end": c["at"] + length if ti else g.s(a + ln), "length": length,
                          "at_f": a, "end_f": a + ln, "length_f": ln, "gain_db": c["gain_db"], "mute": c["mute"],
                          "transition_in": c.get("transition_in", {}).get("frames"),
                          "alternatives": c.get("alternatives", []), "shot": c.get("shot"),
                          "number": c.get("number"), "offline": c.get("offline", False), "link": c.get("link")}
                         | {k: c[k] for k in ("fade_in", "fade_out", "crossfade") if k in c})  # 1.106
            if ti and (x := t_xfade(doc, c, takes)):
                clips[-1]["crossfade_mode"] = x[1]
            if ti and kind == "audio" and (snd := takes.get(c["take_id"], {}).get("sound")):
                clips[-1]["sound"] = snd
    links = {}  # 1.50: each V1 clip's linked sound clips, in track order
    for c in clips:
        if c["link"]:
            links.setdefault(c["link"], []).append(c["id"])
    marks = [{"id": m["id"], "at": m["at"], "at_f": g.f(m["at"]), "name": m.get("name"), "color": m.get("color")}
             for m in doc.get("markers", [])]
    out = {"target": doc["target"], "duration_s": doc["duration_s"], "frames": doc["frames"], "tracks": tracks,
           "clips": clips, "links": links, "markers": marks}
    if "ducking" in doc:  # 1.106
        out["ducking"] = doc["ducking"]
    return out


def frame_at_time(doc: dict, t: float, takes: dict) -> dict:
    """What V1 shows at sequence seconds t, for an agent to look (README, Timeline: edits by ops): the V1 clip under
    frame frames(t), no dissolve composited. Past the end or an empty slot: empty; offline: the clip, no image; an
    image: the image; a video: the source time wanted (as frame_at maps it) and the nearer stored frame, first (0)
    or last (the take's last frame on the target grid, seconds(frames(duration) − 1); a tie: first)."""
    g = Grid(doc["target"])
    f = g.f(t)
    out = {"empty": True, "clip": None, "take_id": None, "kind": None, "t": None, "shown": None, "shown_t": None}
    for c in doc["tracks"][0]["clips"]:
        kind, a = t_kind(c, takes), g.f(c["at"])
        if not a <= f < a + t_len(c, kind, g):
            continue
        out["clip"] = c["id"]
        if kind == "slot":
            return out
        out |= {"empty": False, "take_id": c["take_id"], "kind": kind}
        if c.get("offline"):
            return out | {"offline": True}
        if kind == "image":
            return out | {"shown": "image"}
        src, last = g.s(g.f(c["in"]) + f - a), g.s(g.f(takes[c["take_id"]]["duration_s"]) - 1)
        first = 2 * g.f(src) <= g.f(last)
        return out | {"t": src, "shown": "first" if first else "last", "shown_t": 0 if first else last}
    return out


def export_plan(doc: dict, takes: dict) -> dict:
    """The export plan of a normalized document (README, Timeline: export): stream copy only when every V1 clip is a
    whole, probed H.264 take in MP4/MOV matching the target and the first clip; sound is always re-encoded."""
    g, tg = Grid(doc["target"]), doc["target"]
    blocked = [["offline", c["id"]] for t in doc["tracks"] for c in t["clips"] if c.get("offline")]
    if not doc["tracks"][0]["clips"]:  # sound alone has no picture to export
        blocked.append(["empty", None])
    reasons, first = [], None
    why = lambda code, cid: reasons.append([code, cid])
    for c in doc["tracks"][0]["clips"]:
        cid, tk = c["id"], takes.get(c.get("take_id"))
        if not c.get("take_id"):
            why("gap", cid)
            continue
        if not tk:
            continue
        if tk["kind"] == "image":
            why("image", cid)
            continue
        if "transition_in" in c:
            why("transition", cid)
        s = tk.get("stream")
        if not s:
            why("not_probed", cid)
            continue
        fr = ratio(s["frame_rate"])
        for code, bad in (("container", s["container"] not in ("mp4", "mov")), ("codec", s["codec"] != "h264"),
                          ("pixel_format", s["pix_fmt"] != "yuv420p"),
                          ("size", (s["width"], s["height"]) != (tg["width"], tg["height"])),
                          ("aspect", s["sar"] not in (None, "1:1")),
                          ("field_order", s["field_order"] not in (None, "progressive", "unknown")),
                          ("frame_rate", fr[0] * g.den != fr[1] * g.num),
                          ("trimmed", g.f(c["in"]) != 0 or g.f(c["out"]) != g.f(tk["duration_s"]))):
            if bad:
                why(code, cid)
        if first is None:
            first = s
            continue
        for code, key in (("profile", "profile"), ("level", "level"), ("time_base", "time_base"),
                          ("colour", "color"), ("parameter_sets", "extradata_sha256")):
            if s[key] != first[key]:
                why(code, cid)
    copy_ok = not reasons and not blocked
    bitrate = min(max(rnd(tg["width"] * tg["height"] * g.num / g.den * 0.15), 1_000_000), 40_000_000)
    dur = g.s(doc["frames"])
    audio = rnd(192000 * dur / 8)
    est = (sum(takes[c["take_id"]]["bytes"] for c in doc["tracks"][0]["clips"]) + audio if copy_ok
           else rnd((bitrate + 192000) * dur / 8))
    inputs = sum(takes[x]["bytes"] for x in {c.get("take_id") for t in doc["tracks"] for c in t["clips"]} if x in takes)
    if dur > T_MAX_EXPORT_S or est > T_MAX_EXPORT_BYTES or inputs > T_MAX_EXPORT_INPUTS:
        blocked.append(["too_large", None])
    sounds = []  # 1.32: every sound the mix reads, with the facts it is resampled from (to target, stereo)
    for ti, t in enumerate(doc["tracks"]):
        for c in t["clips"]:
            tk = takes.get(c.get("take_id"))
            if not tk or c.get("offline") or c["mute"] or t["mute"] or (ti == 0 and not (
                    tk["kind"] == "video" and tk["has_audio"])):
                continue
            sounds.append({"clip": c["id"], "track": t["name"], "sample_rate": tk.get("sample_rate"),
                           "channels": 1 if tk["kind"] == "audio" else None})
    return {"stream_copy": copy_ok, "reasons": reasons, "blocked": blocked,
            "video_bitrate": None if copy_ok else bitrate, "estimated_bytes": est, "sounds": sounds}


def shot_keyframes(nodes: dict, palette: dict, studio: dict | None = None) -> dict | None:
    """Which frames a New shot can bind (Workflow.keyframes): from 1.37 the workflow's `studio.keyframes` {first,
    last} when `first` names an unwired image param on a known, not bypassed node (`last` the same, else null);
    else the workflow's one rows param, unwired on a known, not bypassed node, with one Image field and a `position:
    seconds` field. `last` when it takes 2 rows or more."""
    def image_param(path) -> bool:
        nid, _, name = (path or "").partition(".")
        n = nodes.get(nid)
        s = palette.get(n["kind"]) if n else None
        return bool(s) and not n.get("bypass") and name not in n.get("in", {}) and any(
            p["name"] == name and p["type"] == "image" for p in s["params"])
    kf = (studio or {}).get("keyframes")
    if kf and image_param(kf["first"]):
        return {"first": kf["first"], "last": kf.get("last") if image_param(kf.get("last")) else None}
    found = []
    for nid in sorted(nodes):
        n, s = nodes[nid], palette.get(nodes[nid]["kind"])
        for p in (s["params"] if s and not n.get("bypass") else []):
            pos = [k for k, v in p.get("item", {}).items() if isinstance(v, dict) and v.get("position") == "seconds"]
            if image_field(p) and pos and p["name"] not in n.get("in", {}):
                found.append({"param": f"{nid}.{p['name']}", "image": image_field(p), "seconds": pos[0],
                              "last": p["max"] >= 2})
    return found[0] if len(found) == 1 else None


def rerender_steps(doc: dict, takes: dict, clips: list[str] | None, keyframes: dict, media: dict | None = None) -> dict:
    """Re-render's pipeline order and binds (README, Timeline: re-render): a step `clip_<id>` per selected V1 clip;
    a keyframe row whose image is a frame take of a take in another selected clip's alternatives binds that clip's
    step (`<param>[<row>]` ← `clip_<other>.first_frame|last_frame`) and runs after it; ties in timeline order; from
    1.37 keyframes {first, last} (image params) bind the param path itself (`first.image` ← `clip_<other>.last_frame`).
    keyframes: workflow id → its `shot_keyframes` (absent: none). From 1.19 a media input (media: workflow id → its
    `media` param paths) holding a take of another selected clip binds `<param>` ← `clip_<other>.takes` (in / out
    kept) and runs after it: a retake or extend re-renders on its source clip's new take. From 1.32 `clips` may name
    audio-track clips (a line made by a voice workflow): a V1 clip whose media input holds such a clip's take (Make
    shot) binds `clip_<line>.takes` and runs after it."""
    v1 = [c for ti, t in enumerate(doc["tracks"]) for c in t["clips"]  # 1.32: named audio-track clips too
          if (c["id"] in clips if clips is not None else ti == 0 and c.get("take_id"))]
    if len(v1) > T_MAX_RERENDER:
        return {"error": "too_many", "clip": None}
    for c in v1:
        tk = takes.get(c.get("take_id"))
        code = "empty" if not c.get("take_id") else "offline" if not tk else "not_made" if not tk.get("made_with") else None
        if code:
            return {"error": code, "clip": c["id"]}
    owner = {a: c["id"] for c in v1 for a in c["alternatives"]}
    deps, binds = {c["id"]: set() for c in v1}, {c["id"]: {} for c in v1}
    for c in v1:
        mw = takes[c["take_id"]]["made_with"]
        kf = keyframes.get(mw["workflow_id"])
        for path in [kf["first"], kf["last"]] if kf and "first" in kf else []:  # 1.37: frames as image params
            fo = takes.get((mw["inputs"].get(path) or {}).get("take_id"), {}).get("frame_of") if path else None
            src = owner.get(fo["take_id"]) if fo else None
            if src and src != c["id"]:
                binds[c["id"]][path] = f"clip_{src}.{fo['at']}_frame"
                deps[c["id"]].add(src)
        for r, row in enumerate(mw["inputs"].get(kf["param"], []) if kf and "param" in kf else [], 1):
            fo = takes.get((row.get(kf["image"]) or {}).get("take_id"), {}).get("frame_of")
            src = owner.get(fo["take_id"]) if fo else None
            if src and src != c["id"]:
                binds[c["id"]][f"{kf['param']}[{r}]"] = f"clip_{src}.{fo['at']}_frame"
                deps[c["id"]].add(src)
        for path in (media or {}).get(mw["workflow_id"], []):
            src = owner.get((mw["inputs"].get(path) or {}).get("take_id"))
            if src and src != c["id"]:
                binds[c["id"]][path] = f"clip_{src}.takes"
                deps[c["id"]].add(src)
    order, left = [], [c["id"] for c in v1]
    while left:
        ready = next((x for x in left if deps[x] <= set(order)), None)
        if ready is None:
            return {"error": "rerender_cycle", "clip": left[0]}
        order.append(ready)
        left.remove(ready)
    return {"steps": [{"step": f"clip_{x}", "clip": x, "binds": binds[x]} for x in order]}


def make_shot(doc: dict, clip: str, takes: dict, workflows: list[dict], palette: dict, rng: tuple | None = None) -> dict:
    """An audio-track clip's actions (1.32, Make shot; README, Timeline: Make shot): the offers of its take (every
    family) into one `media` param accepting audio of a workflow whose media is video, the range = the clip's in..out in the take's
    own time (an audio take snapped to whole ms, a video's sound to its frames; capped at the param's max_s by the
    offer), each with `slot {at: the clip's at + (the range's start − its in), length: out − in, exact: true}`.
    rng: a selection inside the clip in the take's own seconds (else the clip's in..out). A V1 clip has none here."""
    t, c = next((t, c) for t in doc["tracks"] for c in t["clips"] if c["id"] == clip)
    tk = takes.get(c.get("take_id"))
    if t is doc["tracks"][0] or not tk or c.get("offline"):
        return {"actions": []}
    snap = (lambda x: rnd(x * 1000) / 1000) if tk["kind"] == "audio" else (lambda x: rnd(x * tk["fps"]) / tk["fps"])
    acts, by_id = [], {w["id"]: w for w in workflows}
    a, b = rng or (c["in"], c["out"])
    at = c["at"] + (a - c["in"])  # a selection starts the slot where its sound sits on the track
    for o in take_uses(tk, [w for w in workflows if w.get("media") == "video"], palette, (snap(a), snap(b))):
        v, (nid, name) = o["inputs"][o["param"]], o["param"].split(".")
        spec = next(q for q in palette[by_id[o["workflow_id"]]["nodes"][nid]["kind"]]["params"] if q["name"] == name)
        if isinstance(v, dict) and "audio" in spec.get("accepts", []):  # the clip's sound drives the shot
            acts.append(o | {"slot": {"at": at, "length": v["out"] - v["in"], "exact": True}})
    return {"actions": acts}


def check_timelines(registry: Registry, palette: dict) -> None:
    g_ = HERE / "fixtures" / "timelines"
    ref = lambda n: Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/{n}"}, registry=registry,
                                         format_checker=FormatChecker())
    doc_v, out_v = ref("TimelineDoc"), ref("TimelineNormalized")

    def shape(name: str, v, x) -> bool:
        if errs := list(v.iter_errors(x)):
            fail(f"{name}: {errs[0].json_path}: {errs[0].message}")
            return False
        return True

    for c in json.loads((g_ / "rates.json").read_text())["cases"]:
        g = Grid({"fps": c["fps"], "sample_rate": 48000})
        got = {"frames": g.f(c["seconds"]), "snapped": g.snap(c["seconds"])}
        if got != {k: c[k] for k in got}:
            fail(f"fixtures/timelines/rates.json {c}: got {got}")
    print("ok   fixtures/timelines/rates.json")
    for p in sorted(g_.glob("*.json")):
        name, fx = f"fixtures/timelines/{p.name}", json.loads(p.read_text())
        if p.name == "rates.json":
            continue
        for c in fx["cases"]:
            where, takes = f"{name} {c['description']!r}", fx.get("takes", {}) | c.get("takes", {})
            if p.name.startswith("normalize."):
                if not shape(where, doc_v, c["document"]):
                    continue
                n, probs = t_normalize(c["document"], takes, c.get("previous"))
                got = {"document": n} if n else {"problems": probs}
                if n and not shape(where + " (result)", out_v, n):
                    continue
            elif p.name.startswith("edits."):
                if not shape(where, out_v, c["document"]):
                    continue
                got = t_apply(c["document"], c["op"], takes)
            elif p.name.startswith("snap."):
                g = Grid(c["document"]["target"])
                got = {"to": snap_point(c["t"], t_cuts(c["document"], takes, c.get("exclude")) + c.get("points", []),
                                        c["px_per_s"], g)} \
                    if "t" in c else {"index": drop_index(c["document"], c["clip"], c["mid"], takes)}
            elif p.name.startswith("frame-at."):
                got = frame_at(c["document"], c["frame"], takes)
            elif p.name.startswith("diffs."):  # 1.49
                got = t_diff(c["before"], c["after"])
                if not shape(where + " (result)", ref("TimelineDiff"), got):
                    continue
            elif p.name.startswith("agent_view."):  # 1.49
                got = t_agent_view(c["document"], takes)
            elif p.name.startswith("frame_time."):  # 1.49
                got = frame_at_time(c["document"], c["t"], takes)
            elif p.name.startswith("plan."):
                got = export_plan(c["document"], takes)
            elif p.name.startswith("shot-keyframes."):
                got = {"keyframes": shot_keyframes(c["workflow"]["nodes"], palette, c["workflow"].get("studio"))}
            elif p.name.startswith("rerender."):
                got = rerender_steps(c["document"], takes, c.get("clips"), fx["keyframes"], fx.get("media"))
            elif p.name.startswith("make-shot."):  # 1.32
                reg = HERE / "fixtures" / "registry"
                wfs = [w for pre in ("", "ltx.") for w in json.loads((reg / f"{pre}workflows.valid.json").read_text())["workflows"]]
                got = make_shot(c["document"], c["clip"], {k: v | {"id": k} for k, v in takes.items()}, wfs, palette,
                                tuple(c["range"]) if "range" in c else None)
                if errs := list(ref("ClipActions").iter_errors(got)):
                    fail(f"{where}: not ClipActions: {errs[0].message}")
            else:
                fail(f"{name}: unknown timeline fixture")
                break
            if got != c["expect"]:
                fail(f"{where}: got {json.dumps(got)}, want {json.dumps(c['expect'])}")
        print(f"ok   {name}")


def canon(x):
    """JSON by value: an integral float is its int (Go and JS write 2, Python 2.0)."""
    if isinstance(x, float) and x.is_integer():
        return int(x)
    if isinstance(x, list):
        return [canon(v) for v in x]
    if isinstance(x, dict):
        return {k: canon(v) for k, v in x.items()}
    return x


def replay(name: str, want: list, against: str, env: str, runs: dict) -> None:
    """Every case of fixtures/<name> through each of `runs` (who → (cmd, cwd, its output file under `env`)), each
    answer compared with `want` (canonical JSON) case by case."""
    fx = json.loads((HERE / name).read_text())
    with tempfile.TemporaryDirectory() as tmp:
        for who, (cmd, cwd, out) in runs.items():
            out = f"{tmp}/{out}"
            if not shutil.which(cmd[0]):
                fail(f"{name} replay: {cmd[0]} is not on PATH ({who})")
                continue
            r = subprocess.run(cmd, cwd=cwd, env=os.environ | {env: out}, capture_output=True, text=True, timeout=600)
            if r.returncode != 0 or not Path(out).exists():
                fail(f"{name} replay: {who} failed: {(r.stdout + r.stderr).strip()[-800:]}")
                continue
            got = json.loads(Path(out).read_text())
            if len(got) != len(want):
                fail(f"{name} replay: {who} answered {len(got)} cases, not {len(want)}")
                continue
            for c, g, w in zip(fx["cases"], got, want):
                if json.dumps(canon(g), sort_keys=True) != json.dumps(w, sort_keys=True):
                    fail(f"{name} replay {c['description']!r}: {who} differs from {against}: {json.dumps(canon(g))[:600]}")


def check_edit_replay() -> None:
    """The three implementations of the edits (README, Timeline rules; plan 24 28.9): every fixtures/timelines/
    edits.json case through check.py's t_apply, studio's edit.go Apply (go test TestEditReplay) and the web's
    lib/timeline.ts apply (node, its types stripped), compared as canonical JSON, byte for byte."""
    root, name = HERE.parent, "fixtures/timelines/edits.json"
    fx = json.loads((HERE / name).read_text())
    want = [canon(t_apply(c["document"], c["op"], fx.get("takes", {}) | c.get("takes", {}))) for c in fx["cases"]]
    replay(name, want, "check.py", "TIMELINE_REPLAY_OUT", {
        "studio_service (edit.go)": (["go", "test", "-count=1", "-run", "^TestEditReplay$", "./api/timeline/"],
                                     root / "studio_service", "go.json"),
        "web_service (lib/timeline.ts)": (["node", "--no-warnings", str(HERE / "replay_edits.mjs"),
                                           str(root / "web_service" / "lib" / "timeline.ts"), str(HERE / name)],
                                          root, "ts.json"),
    })
    print(f"ok   {name} replayed through check.py, edit.go and lib/timeline.ts")


def check_magic_replay(registry: Registry) -> None:
    """Magic edits by ops (README, Magic: edits by ops; API 1.80): every fixtures/magic/edits.json case through studio's
    api/magic Apply (go test TestMagicReplay) and the web's lib/magic.ts apply (node, its types stripped), each
    compared with the case's `expect` as canonical JSON."""
    root, name = HERE.parent, "fixtures/magic/edits.json"
    fx = json.loads((HERE / name).read_text())
    replay(name, [canon(c["expect"]) for c in fx["cases"]], "expect", "MAGIC_REPLAY_OUT", {
        "studio_service (api/magic)": (["go", "test", "-count=1", "-run", "^TestMagicReplay$", "./api/magic/"],
                                       root / "studio_service", "go.json"),
        "web_service (lib/magic.ts)": (["node", "--no-warnings", str(HERE / "replay_magic.mjs"),
                                        str(root / "web_service" / "lib" / "magic.ts"), str(HERE / name)],
                                       root, "ts.json"),
    })
    magic_doc = Draft202012Validator({"$ref": f"{OPENAPI}#/components/schemas/MagicDoc"}, registry=registry,
                                     format_checker=FormatChecker())
    for c in [{"description": "documents." + k, "expect": v} for k, v in fx["documents"].items()] + fx["cases"]:
        if "error" not in c["expect"] and (errs := list(magic_doc.iter_errors(c["expect"]))):
            fail(f"{name} {c['description']!r}: not a MagicDoc: {errs[0].json_path}: {errs[0].message}")
    print(f"ok   {name} replayed through api/magic and lib/magic.ts")
