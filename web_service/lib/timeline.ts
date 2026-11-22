// The timeline rules (contracts/README.md "Timeline rules"): a TypeScript port of contracts/check.py's reference
// (t_normalize, t_edit / t_apply, t_cuts, snap_point, drop_index, frame_at), tested on contracts/fixtures/timelines so
// the editor draws exactly what studio stores. Ported in spirit from the old helmstudio's sequence.js (MIT, same
// owner). Pure: no React, no fetch.
import type { S } from "@/lib/api";

export type Target = { width: number; height: number; fps: number; sample_rate: number };
export type Dissolve = { type: "dissolve"; frames: number };
export type ShotRef = {
  run_id?: string;
  batch_id?: string;
  step?: string;
  status?: string;
  error?: string | null;
};
export type Clip = {
  id: string;
  take_id: string | null;
  in?: number;
  out?: number;
  hold?: number;
  at?: number;
  gain_db?: number;
  mute?: boolean;
  transition_in?: Dissolve;
  alternatives?: string[];
  shot?: ShotRef;
  /** V1: the shot number (API 1.29), given by studio */
  number?: number;
  offline?: boolean;
  /** 1.50, an audio-track clip: the V1 clip it is linked to (its sound) */
  link?: string;
};
export type MarkerColor = "red" | "orange" | "yellow" | "green" | "blue" | "purple";
/** 1.50: a named point on the sequence. */
export type Marker = { id: string; at: number; name?: string; color?: MarkerColor };
export type Track = {
  name?: string;
  kind: "video" | "audio";
  gain_db?: number;
  mute?: boolean;
  clips: Clip[];
};
export type Doc = {
  target: Target;
  tracks: Track[];
  duration_s?: number;
  frames?: number;
  /** 1.50: absent = none */
  markers?: Marker[];
};
/** A normalized document: names, V1 `at`, defaults filled, every time snapped. */
export type Norm = Doc & { duration_s: number; frames: number };
/** What the rules need of a take. */
export type SourceMap = { take_id: string; offset_s: number; new_in_s: number; new_out_s: number };
export type Facts = {
  kind: string;
  duration_s?: number;
  has_audio?: boolean;
  source_map?: SourceMap;
};
export type Takes = Record<string, Facts>;
export type Problem = [string, string | null, string | null];
export type Kind = "slot" | "video" | "image" | "audio";

/** The rate table: fps as JSON writes it → the exact rate num/den. */
export const RATES: [number, number, number][] = [
  [23.976, 24000, 1001],
  [24, 24, 1],
  [25, 25, 1],
  [29.97, 30000, 1001],
  [30, 30, 1],
  [48, 48, 1],
  [50, 50, 1],
  [59.94, 60000, 1001],
  [60, 60, 1],
];
export const MAGNET_PX = 6;
export const IMAGE_HOLD_S = 3;
export const MAX_AUDIO_TRACKS = 8;
const MAX_CLIPS = 1000;
const MAX_BYTES = 262144;
const MAX_ALTERNATIVES = 32;
const MAX_MARKERS = 200; // 1.50

/** Round half away from zero, exactly (Go math.Round); never JS Math.round on a negative half. */
export const rnd = (x: number) => Math.sign(x) * Math.round(Math.abs(x)) + 0;

/** The target's frame and sample grids: f(s) = rnd(s × num / den), s(f) = f × den / num, in that order. */
export class Grid {
  num: number;
  den: number;
  sr: number;
  constructor(t: Pick<Target, "fps" | "sample_rate">) {
    const r = RATES.find(([fps]) => Math.abs(fps - t.fps) < 1e-6) ?? [24, 24, 1];
    this.num = r[1];
    this.den = r[2];
    this.sr = t.sample_rate;
  }
  f = (s: number) => rnd((s * this.num) / this.den);
  s = (f: number) => (f * this.den) / this.num;
  snap = (s: number) => this.s(this.f(s));
  smp = (s: number) => rnd(s * this.sr);
}

const tk = (takes: Takes, id: string | null | undefined) => (id ? takes[id] : undefined);

/** A clip whose shot is queued or running: it cannot be edited until the shot ends (a failed one can, and retried). */
export const shotBusy = (c: Clip) => !!c.shot && c.shot.status !== "failed";

/** slot (no take: an empty slot, or from 1.32 a New line's placeholder on an audio track), video, image or (1.32)
 * audio; an offline clip by its fields (hold = image). */
export function kindOf(c: Clip, takes: Takes): Kind {
  if (!c.take_id) return "slot";
  const t = takes[c.take_id];
  if (t) return t.kind === "image" || t.kind === "audio" ? t.kind : "video";
  return "hold" in c ? "image" : "video";
}

/** A clip's length in frames on the sequence (an audio-track clip: its end's frame minus its start's). */
export function lenOf(c: Clip, kind: Kind, g: Grid, audio = false): number {
  if (audio) return g.f(c.at! + ("hold" in c ? c.hold! : c.out! - c.in!)) - g.f(c.at!);
  return kind === "video" ? g.f(c.out!) - g.f(c.in!) : g.f(c.hold!);
}

/** An audio-track clip's length in target samples: out − in, or (1.32) a line placeholder's hold. */
export const aLen = (c: Clip, g: Grid) =>
  "hold" in c ? g.smp(c.hold!) : g.smp(c.out!) - g.smp(c.in!);

const shotKey = (s: ShotRef) => JSON.stringify([s.run_id, s.batch_id, s.step]);
const shotRef = (s: ShotRef): ShotRef => {
  const o: ShotRef = {};
  if (s.run_id !== undefined) o.run_id = s.run_id;
  if (s.batch_id !== undefined) o.batch_id = s.batch_id;
  if (s.step !== undefined) o.step = s.step;
  return o;
};

/** A written document → the normalized document, or its problems sorted by code, track, clip (check.py
 * t_normalize). `previous` (the stored revision) names takes that may be offline now. */
export function normalize(
  doc: Doc,
  takes: Takes,
  previous?: Doc | null,
): { document: Norm } | { problems: Problem[] } {
  const probs: Problem[] = [];
  const add = (code: string, track: string | null = null, clip: string | null = null) =>
    probs.push([code, track, clip]);
  if (new TextEncoder().encode(JSON.stringify(doc)).length > MAX_BYTES)
    return { problems: [["too_large", null, null]] };
  const tracks = doc.tracks;
  if (
    !tracks.length ||
    tracks[0].kind !== "video" ||
    tracks.slice(1).some((t) => t.kind !== "audio") ||
    tracks.length > 1 + MAX_AUDIO_TRACKS
  )
    return { problems: [["track_layout", null, null]] };
  if (tracks.reduce((n, t) => n + t.clips.length, 0) > MAX_CLIPS)
    return { problems: [["too_many_clips", null, null]] };
  const g = new Grid(doc.target);
  const known = new Set<string | null>();
  const shots: Record<string, string> = {};
  for (const t of previous?.tracks ?? [])
    for (const c of t.clips) {
      known.add(c.take_id ?? null);
      for (const a of c.alternatives ?? []) known.add(a);
      if (c.shot) shots[c.id] = shotKey(c.shot);
    }
  const ids = new Set<string>();
  const out: Track[] = [];
  const ends: [string, string, number][] = [];
  const v1ids = new Set(tracks[0].clips.map((c) => c.id));
  tracks.forEach((t, ti) => {
    const name = ti === 0 ? "V1" : `A${ti + 1}`;
    const video = ti === 0;
    const clips: [Clip, Kind][] = [];
    for (const c of t.clips) {
      const cid = c.id;
      const tid = c.take_id ?? null;
      const take = tk(takes, tid);
      const kind = kindOf(c, takes);
      if (ids.has(cid)) add("duplicate_clip", name, cid);
      ids.add(cid);
      if (kind === "audio" && video) {
        add("wrong_track", name, cid); // 1.32: an audio take goes on A2-A9 only
        continue;
      }
      if (
        [...(tid ? [tid] : []), ...(c.alternatives ?? [])].some((x) => !takes[x] && !known.has(x))
      )
        add("take_not_found", name, cid);
      if (c.shot && shots[cid] !== shotKey(c.shot)) add("shot_invalid", name, cid);
      const n: Clip = { id: cid, take_id: tid };
      if (kind === "video" || kind === "audio") {
        if ("hold" in c) add("hold_on_video", name, cid);
        const end = "out" in c ? c.out : take ? take.duration_s : undefined;
        if (end === undefined) {
          add("out_required", name, cid);
          continue;
        }
        if (video) {
          const i_ = g.f(c.in ?? 0);
          const o_ = g.f(end);
          if (take && !(0 <= i_ && o_ - i_ >= 1 && o_ <= g.f(take.duration_s!)))
            add("trim", name, cid);
          n.in = g.s(i_);
          n.out = g.s(o_);
        } else {
          const i_ = g.smp(c.in ?? 0);
          const o_ = g.smp(end);
          if (take && !(0 <= i_ && i_ < o_ && o_ <= g.smp(take.duration_s!)))
            add("trim", name, cid);
          if (take && kind === "video" && !take.has_audio) add("no_audio", name, cid);
          if (!("at" in c)) {
            add("at_required", name, cid);
            continue;
          }
          n.in = i_ / g.sr;
          n.out = o_ / g.sr;
          n.at = g.snap(c.at!);
        }
      } else if (!video && !(kind === "slot" && c.shot)) {
        add("wrong_track", name, cid); // 1.32: a New line placeholder is the exception
        continue;
      } else if (!video) {
        if ("in" in c || "out" in c) add("image_trim", name, cid);
        if (!("hold" in c) || !("at" in c)) {
          add(!("hold" in c) ? "hold_required" : "at_required", name, cid);
          continue;
        }
        if (g.f(c.hold!) < 1) add("hold", name, cid);
        n.at = g.snap(c.at!);
        n.hold = g.snap(c.hold!);
      } else {
        if ("in" in c || "out" in c) add("image_trim", name, cid);
        if (!("hold" in c)) {
          add("hold_required", name, cid);
          continue;
        }
        if (g.f(c.hold!) < 1) add("hold", name, cid);
        n.hold = g.snap(c.hold!);
      }
      n.gain_db = c.gain_db ?? 0;
      n.mute = c.mute ?? false;
      if (c.link !== undefined) {
        // 1.50: an audio-track clip linked to a V1 clip
        if (video) add("link_track", name, cid);
        else if (!v1ids.has(c.link)) add("link_not_v1", name, cid);
        n.link = c.link;
      }
      if (c.transition_in) {
        if (video) n.transition_in = c.transition_in;
        else add("dissolve_track", name, cid);
      }
      const alts = [...new Set([...(c.alternatives ?? []), ...(tid ? [tid] : [])])];
      while (alts.length > MAX_ALTERNATIVES)
        alts.splice(
          alts.findIndex((a) => a !== tid),
          1,
        );
      n.alternatives = alts;
      if (c.shot) n.shot = shotRef(c.shot);
      if (video && c.number !== undefined) n.number = c.number; // 1.29: studio gives it when it stores
      if (tid && !take) n.offline = true;
      clips.push([n, kind]);
    }
    if (video) {
      let pos = 0;
      for (const [n, kind] of clips) {
        n.at = g.s(pos);
        pos += lenOf(n, kind, g);
      }
      const din = [...clips.map(([n]) => n.transition_in?.frames ?? 0), 0];
      clips.forEach(([n, kind], k) => {
        const d = din[k];
        if (d && k === 0) add("dissolve_first", name, n.id);
        else if (d && (kind === "slot" || clips[k - 1][1] === "slot"))
          add("dissolve_gap", name, n.id);
        else if (d) {
          const [p, pk] = clips[k - 1];
          const ptk = tk(takes, p.take_id);
          if (
            (pk === "video" && ptk && g.f(p.out!) + d / 2 > g.f(ptk.duration_s!)) ||
            (kind === "video" && g.f(n.in!) - d / 2 < 0)
          )
            add("dissolve_handles", name, n.id);
        }
        const half = Math.floor((k ? din[k] : 0) / 2) + Math.floor(din[k + 1] / 2);
        if (half && half > lenOf(n, kind, g)) add("dissolve_length", name, n.id);
      });
    } else {
      clips.sort((a, b) => a[0].at! - b[0].at!);
      let prevEnd: number | null = null;
      for (const [n] of clips) {
        const a = g.smp(n.at!);
        const e = a + aLen(n, g);
        if (prevEnd !== null && a < prevEnd) add("overlap", name, n.id);
        prevEnd = Math.max(e, prevEnd ?? 0);
        ends.push([name, n.id, e]);
      }
    }
    out.push({
      name,
      kind: t.kind,
      gain_db: t.gain_db ?? 0,
      mute: t.mute ?? false,
      clips: clips.map(([n]) => n),
    });
  });
  const v1 = out[0].clips;
  let frames: number;
  if (v1.length) {
    const last = v1[v1.length - 1];
    frames = g.f(last.at!) + lenOf(last, kindOf(last, takes), g);
    for (const [name, cid, e] of ends) if (e > g.smp(g.s(frames))) add("past_end", name, cid);
  } else frames = Math.max(0, ...ends.map(([, , e]) => g.f(e / g.sr)));
  // 1.50: markers snapped to frames, sorted by time then id; none = no `markers`
  const marks: Marker[] = [];
  const mids = new Set<string>();
  if ((doc.markers ?? []).length > MAX_MARKERS) add("too_many_markers");
  for (const m of doc.markers ?? []) {
    if (mids.has(m.id)) add("duplicate_marker", null, m.id);
    mids.add(m.id);
    if (v1.length && g.f(m.at) > frames) add("marker_past_end", null, m.id);
    const nm: Marker = { id: m.id, at: g.snap(m.at) };
    if (m.name !== undefined) nm.name = m.name;
    if (m.color !== undefined) nm.color = m.color;
    marks.push(nm);
  }
  if (probs.length) {
    const key = (p: Problem) => [p[0], p[1] ?? "", p[2] ?? ""];
    return {
      problems: probs.sort((a, b) => {
        const [x, y] = [key(a), key(b)];
        for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1;
        return 0;
      }),
    };
  }
  const res: Norm = { target: doc.target, tracks: out, duration_s: g.s(frames), frames };
  if (marks.length)
    res.markers = marks.sort((a, b) => a.at - b.at || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  return { document: res };
}

/** An edit (contracts/README.md, Timeline rules: Edits). `delta` and every time are seconds. */
export type Op =
  | { op: "move"; clip: string; index: number }
  | { op: "move"; clip: string; at: number; track?: string }
  | { op: "trim"; clip: string; edge: "in" | "out"; delta: number }
  | { op: "split"; track: string; at: number; id: string; ids?: string[] }
  | { op: "remove" | "lift"; clip: string; ripple?: boolean }
  | { op: "replace"; clip: string; take_id: string }
  | {
      op: "set";
      clip: string;
      gain_db?: number | null;
      mute?: boolean | null;
      hold?: number | null;
      transition_in?: Dissolve | null;
      link?: string | null;
    }
  | { op: "add"; take_ids: string[]; track?: string; ids: string[] }
  | { op: "insert"; at: number; hold: number; id: string }
  | { op: "place"; at: number; hold: number; id: string; gap_id: string }
  | { op: "fill"; clip: string; take_id: string }
  | { op: "add_track" }
  | { op: "remove_track"; track: string }
  | { op: "track"; track: string; gain_db?: number; mute?: boolean }
  // 1.50: the Timeline tools' ops and the markers'
  | { op: "roll" | "slip" | "slide"; clip: string; delta: number }
  | { op: "overwrite"; clip: string; at: number; track?: string; ids?: string[] }
  | { op: "split_all"; at: number; ids?: string[] }
  | { op: "remove_range"; from: number; to: number; ids?: string[] }
  | { op: "lift_range"; from: number; to: number; ids?: string[] }
  | { op: "marker"; id: string; at: number; name?: string; color?: MarkerColor }
  | { op: "remove_marker"; id: string };

/** The ops that cut and move every track themselves (1.50): no linked clip follows after them. */
const ALL_TRACKS = new Set(["split_all", "remove_range", "lift_range"]);

/** One edit on a normalized document → the document as written (normalize it next), or an error code (t_edit). From
 * 1.50 linked sound clips follow their V1 clip (t_follow). */
export function edit(doc: Doc, op: Op, takes: Takes): Doc | string {
  const raw = editOp(doc, op, takes);
  return typeof raw === "string" ? raw : follow(doc, raw, op, takes);
}

/** t_follow (1.50): an audio-track clip whose V1 clip the edit removed goes too; one whose V1 clip moved moves by as
 * many frames (its content's place: start − in for a video that stays a video, else its start), except after
 * ALL_TRACKS. Moved before 0: `link_start`. */
function follow(before: Doc, raw: Doc, op: Op, takes: Takes): Doc | string {
  if (!raw.tracks.slice(1).some((t) => t.clips.some((c) => c.link !== undefined))) return raw;
  const g = new Grid(raw.target);
  const anchors = (d: Doc) => {
    // a field an edit removed counts 0 (normalize refuses that document anyway)
    const out = new Map<string, [Kind, number, number]>();
    let pos = 0;
    for (const c of d.tracks[0].clips) {
      const kind = kindOf(c, takes);
      const i_ = g.f(c.in ?? 0);
      out.set(c.id, [kind, pos, kind === "video" ? pos - i_ : pos]);
      pos += kind === "video" ? g.f(c.out ?? 0) - i_ : g.f(c.hold ?? 0);
    }
    return out;
  };
  const [b, a] = [anchors(before), anchors(raw)];
  for (const t of raw.tracks.slice(1)) {
    const keep: Clip[] = [];
    for (const c of t.clips) {
      const ln = c.link;
      if (ln !== undefined && b.has(ln) && !a.has(ln)) continue;
      if (ln !== undefined && b.has(ln) && !ALL_TRACKS.has(op.op)) {
        const [kb, sb, nb] = b.get(ln)!;
        const [ka, sa, na] = a.get(ln)!;
        const sh = ka === kb ? na - nb : sa - sb;
        if (sh) {
          if (g.f(c.at!) + sh < 0) return "link_start";
          c.at = g.s(g.f(c.at!) + sh);
        }
      }
      keep.push(c);
    }
    t.clips = keep;
  }
  return raw;
}

/** V1's clip starts in frames, and its end (the clips' `at` may be stale while an edit runs). */
function v1Starts(clips: Clip[], takes: Takes, g: Grid): [number[], number] {
  const out: number[] = [];
  let pos = 0;
  for (const c of clips) {
    out.push(pos);
    pos += lenOf(c, kindOf(c, takes), g);
  }
  return [out, pos];
}

/** A V1 clip's end moves by df frames: a video's out, else its hold. */
function grow(c: Clip, kind: Kind, df: number, g: Grid) {
  if (kind === "video") c.out = g.s(g.f(c.out!) + df);
  else c.hold = g.s(g.f(c.hold!) + df);
}

/** A V1 clip's start moves by df frames into it: a video's in, else its hold shrinks. */
function front(c: Clip, kind: Kind, df: number, g: Grid) {
  if (kind === "video") c.in = g.s(g.f(c.in!) + df);
  else c.hold = g.s(g.f(c.hold!) - df);
}

/** The clip of t strictly around frame f: [index, start, length], else null. */
function inside(t: Track, f: number, onV1: boolean, takes: Takes, g: Grid): [number, number, number] | null {
  const starts = onV1 ? v1Starts(t.clips, takes, g)[0] : null;
  let hit: [number, number, number] | null = null;
  t.clips.forEach((c, k) => {
    const a = starts ? starts[k] : g.f(c.at!);
    const ln = lenOf(c, kindOf(c, takes), g, !onV1);
    if (a < f && f < a + ln) hit = [k, a, ln];
  });
  return hit;
}

/** Cut clip k of t (start a, length ln frames) at frame f: the right part, inserted after it, gets id, keeps the take,
 * alternatives and link, never the dissolve or the shot. */
function cutClip(t: Track, k: number, f: number, a: number, ln: number, id: string, onV1: boolean, takes: Takes, g: Grid) {
  const c = t.clips[k];
  const c2: Clip = { ...structuredClone(c), id };
  delete c2.transition_in;
  delete c2.shot;
  if (!onV1) {
    const cut = g.smp(g.s(f)) - g.smp(c.at!) + g.smp(c.in!); // the left part ends on the right part's start sample
    c.out = c2.in = cut / g.sr;
    c2.at = g.s(f);
  } else if (kindOf(c, takes) === "video") c.out = c2.in = g.s(g.f(c.in!) + f - a);
  else {
    c.hold = g.s(f - a);
    c2.hold = g.s(ln - f + a);
  }
  t.clips.splice(k + 1, 0, c2);
}

/** t_relink (1.50): after V1 clips were cut (root: each new part's id → the clip it was cut from), a sound clip linked
 * to one of the parts links to the part it starts in (the last starting at or before it; else the first). */
function relink(d: Doc, root: Map<string, string>, takes: Takes, g: Grid) {
  if (!root.size) return;
  const v1 = d.tracks[0].clips;
  const starts = v1Starts(v1, takes, g)[0];
  const parts = new Map<string, [number, string][]>();
  v1.forEach((c, i) => {
    const r = root.get(c.id) ?? c.id;
    parts.set(r, [...(parts.get(r) ?? []), [starts[i], c.id]]);
  });
  for (const t of d.tracks.slice(1))
    for (const c of t.clips) {
      const ps = c.link !== undefined ? parts.get(root.get(c.link) ?? c.link) : undefined;
      if (!ps) continue;
      const at = ps.filter(([a]) => a <= g.f(c.at!));
      c.link = (at.length ? at[at.length - 1] : ps[0])[1];
    }
}

/** t_edit's op itself. */
function editOp(doc: Doc, op: Op, takes: Takes): Doc | string {
  const d: Doc = structuredClone(doc);
  const g = new Grid(d.target);
  const tracks: Record<string, Track> = Object.fromEntries(d.tracks.map((t) => [t.name, t]));
  const v1 = d.tracks[0].clips;
  const din = (i: number) => (0 < i && i < v1.length ? (v1[i].transition_in?.frames ?? 0) : 0);
  const cutAt = (i: number) => {
    if (i < v1.length) delete v1[i].transition_in;
  };

  if (op.op === "add") {
    const t = tracks[op.track ?? "V1"];
    op.take_ids.forEach((tid, k) => {
      const cid = op.ids[k];
      if (cid === undefined) return;
      const take = takes[tid];
      if (t.name !== "V1") {
        // at the first frame at or after the track's end (in samples): never an overlap
        const end = Math.max(0, ...t.clips.map((c) => g.smp(c.at!) + aLen(c, g)));
        let f = g.f(end / g.sr);
        if (g.smp(g.s(f)) < end) f += 1;
        t.clips.push({ id: cid, take_id: tid, in: 0, out: take.duration_s, at: g.s(f) });
      } else if (take.kind === "image") t.clips.push({ id: cid, take_id: tid, hold: IMAGE_HOLD_S });
      else t.clips.push({ id: cid, take_id: tid, in: 0, out: take.duration_s });
    });
    return d;
  }
  const cuts = [0];
  for (const c of v1) cuts.push(cuts[cuts.length - 1] + lenOf(c, kindOf(c, takes), g));
  if (op.op === "place") {
    // 1.32, Make shot: an empty slot at exactly frames(at) (README, Timeline: Make shot)
    const [f, n, end] = [g.f(op.at), g.f(op.hold), cuts[cuts.length - 1]];
    const made: Clip = { id: op.id, take_id: null, hold: g.s(n) };
    if (f >= end) {
      if (f > end) v1.push({ id: op.gap_id, take_id: null, hold: g.s(f - end) });
      v1.push(made);
      return d;
    }
    let k = 0;
    cuts.forEach((c, i) => {
      if (i < v1.length && c <= f) k = i;
    });
    const gap = v1[k];
    if (gap.take_id || gap.shot)
      return editOp(doc, { op: "insert", at: op.at, hold: g.s(n), id: op.id }, takes);
    const [left, right] = [f - cuts[k], cuts[k + 1] - f - n];
    const parts: Clip[] = [
      ...(left ? [{ ...gap, hold: g.s(left) }] : []),
      made,
      ...(right > 0 ? [{ id: left ? op.gap_id : gap.id, take_id: null, hold: g.s(right) }] : []),
    ];
    v1.splice(k, 1, ...parts);
    cutAt(k + parts.length);
    // longer than the gap: an insert of what overflows, every track moving on together (V1 rippled; the audio too)
    if (right < 0)
      for (const t of d.tracks.slice(1))
        for (const c of t.clips) if (g.f(c.at!) >= cuts[k + 1]) c.at = g.s(g.f(c.at!) - right);
    return d;
  }
  if (op.op === "insert") {
    const f = g.f(op.at);
    let i = 0;
    cuts.forEach((c, k) => {
      if (Math.abs(c - f) <= Math.abs(cuts[i] - f)) i = k; // a tie: the later cut
    });
    v1.splice(i, 0, { id: op.id, take_id: null, hold: op.hold });
    cutAt(i + 1);
    return d;
  }
  if (op.op === "add_track" || op.op === "remove_track" || op.op === "track") {
    if (op.op === "add_track") {
      if (d.tracks.length > MAX_AUDIO_TRACKS) return "too_many_tracks";
      d.tracks.push({ kind: "audio", gain_db: 0, mute: false, clips: [] });
    } else if (op.op === "remove_track") {
      if (op.track === "V1") return "video_track";
      d.tracks.splice(d.tracks.indexOf(tracks[op.track]), 1);
    } else {
      if (op.gain_db !== undefined) tracks[op.track].gain_db = op.gain_db;
      if (op.mute !== undefined) tracks[op.track].mute = op.mute;
    }
    for (const t of d.tracks) delete t.name; // names follow the position
    return d;
  }
  if (op.op === "split") return split(d, tracks[op.track], op, takes, g);
  if (op.op === "split_all") {
    // 1.50: blade on every track at frame `at`
    const f = g.f(op.at);
    const ids = [...(op.ids ?? [])];
    const root = new Map<string, string>();
    let made = false;
    for (const [ti, t] of d.tracks.entries()) {
      const hit = inside(t, f, ti === 0, takes, g);
      if (!hit || t.clips[hit[0]].shot) continue;
      const id = ids.shift();
      if (id === undefined) return "ids_short";
      cutClip(t, hit[0], f, hit[1], hit[2], id, ti === 0, takes, g);
      if (ti === 0) root.set(id, t.clips[hit[0]].id);
      made = true;
    }
    if (!made) return "split_none";
    relink(d, root, takes, g);
    return d;
  }
  if (op.op === "remove_range" || op.op === "lift_range") {
    // 1.50: a range on every track, cut at both ends
    const [lo, hi] = [g.f(op.from), g.f(op.to)];
    if (hi <= lo) return "range_empty";
    for (const [ti, t] of d.tracks.entries()) {
      const starts = v1Starts(t.clips, takes, g)[0];
      for (const [k, c] of t.clips.entries()) {
        const a = ti === 0 ? starts[k] : g.f(c.at!);
        if (c.shot && a < hi && a + lenOf(c, kindOf(c, takes), g, ti > 0) > lo) return "range_shot";
      }
    }
    const ids = [...(op.ids ?? [])];
    const root = new Map<string, string>();
    for (const [ti, t] of d.tracks.entries())
      for (const f of [lo, hi]) {
        const hit = inside(t, f, ti === 0, takes, g);
        if (!hit) continue;
        const id = ids.shift();
        if (id === undefined) return "ids_short";
        cutClip(t, hit[0], f, hit[1], hit[2], id, ti === 0, takes, g);
        if (ti === 0) {
          const r = t.clips[hit[0]].id;
          root.set(id, root.get(r) ?? r);
        }
      }
    relink(d, root, takes, g);
    const [starts, end] = v1Starts(v1, takes, g);
    const gone = starts.flatMap((a, k) => (lo <= a && a < hi ? [k] : []));
    if (gone.length) {
      v1.splice(gone[0], gone.length);
      if (op.op === "lift_range") {
        const id = ids.shift();
        if (id === undefined) return "ids_short";
        v1.splice(gone[0], 0, { id, take_id: null, hold: g.s(Math.min(hi, end) - lo) });
      }
      cutAt(gone[0] + (op.op === "lift_range" ? 1 : 0));
    }
    for (const t of d.tracks.slice(1))
      t.clips = t.clips.filter((c) => {
        const a = g.f(c.at!);
        if (lo <= a && a < hi) return false;
        if (a >= hi && op.op === "remove_range") c.at = g.s(a - (hi - lo)); // open point 4: audio tracks ripple
        return true;
      });
    return d;
  }
  if (op.op === "marker") {
    // 1.50: a marker put (a new id adds one; a known id is replaced whole)
    const m: Marker = { id: op.id, at: op.at };
    if (op.name !== undefined) m.name = op.name;
    if (op.color !== undefined) m.color = op.color;
    d.markers = [...(d.markers ?? []).filter((x) => x.id !== op.id), m];
    return d;
  }
  if (op.op === "remove_marker") {
    if (!(d.markers ?? []).some((x) => x.id === op.id)) return "unknown_marker";
    d.markers = d.markers!.filter((x) => x.id !== op.id);
    return d;
  }

  const t = d.tracks.find((x) => x.clips.some((c) => c.id === op.clip));
  if (!t) return "clip_not_found";
  const i = t.clips.findIndex((c) => c.id === op.clip);
  const c = t.clips[i];
  const kind = kindOf(c, takes);
  const onV1 = t === d.tracks[0];

  if (op.op === "move") {
    if (onV1 && "index" in op) {
      v1.splice(i, 1);
      cutAt(i);
      v1.splice(op.index, 0, c);
      delete c.transition_in;
      cutAt(op.index + 1);
    } else if (!onV1 && "at" in op) {
      t.clips.splice(i, 1);
      tracks[op.track ?? t.name!].clips.push({ ...c, at: op.at });
    } else return "bad_move";
  } else if (op.op === "trim") {
    const df = g.f(op.delta);
    const take = tk(takes, c.take_id);
    const src = kind === "video" && take ? g.f(take.duration_s!) : null;
    if (onV1) {
      const dIn = din(i);
      const dOut = din(i + 1);
      const least = Math.max(1, Math.floor(dIn / 2) + Math.floor(dOut / 2));
      if (kind === "video") {
        let i_ = g.f(c.in!);
        let o_ = g.f(c.out!);
        if (op.edge === "in") i_ = Math.min(Math.max(i_ + df, Math.floor(dIn / 2)), o_ - least);
        else {
          o_ = Math.max(o_ + df, i_ + least);
          if (src !== null) o_ = Math.min(o_, src - Math.floor(dOut / 2));
        }
        c.in = g.s(i_);
        c.out = g.s(o_);
      } else c.hold = g.s(Math.max(g.f(c.hold!) + (op.edge === "out" ? df : -df), least));
    } else if (kind === "slot") {
      c.hold = g.s(Math.max(g.f(c.hold!) + (op.edge === "out" ? df : -df), 1)); // 1.32: a line placeholder
    } else {
      const i_ = g.smp(c.in!);
      const o_ = g.smp(c.out!);
      const frame = g.smp(g.s(1));
      if (op.edge === "in") {
        const dd = Math.max(df, -g.f(c.at!));
        const ni = Math.min(Math.max(i_ + g.smp(g.s(dd)), 0), o_ - frame);
        c.at = g.snap(c.at! + (ni - i_) / g.sr);
        c.in = ni / g.sr;
      } else {
        const no = Math.max(o_ + g.smp(g.s(df)), i_ + frame);
        // a video's sound or an audio take: out never past the take's end, on the sample grid
        const end = take?.duration_s != null && (kind === "video" || kind === "audio");
        c.out = (end ? Math.min(no, g.smp(take.duration_s!)) : no) / g.sr;
      }
    }
  } else if (op.op === "remove" || op.op === "lift") {
    if (op.op === "lift" && !onV1) return "lift_track";
    if (op.op === "lift") t.clips[i] = { id: c.id, take_id: null, hold: g.s(lenOf(c, kind, g)) };
    else t.clips.splice(i, 1);
    if (onV1) cutAt(op.op === "remove" ? i : i + 1);
    else if (op.op === "remove" && op.ripple) {
      // 1.50: the later clips of its audio track move left by its length
      const [a, ln] = [g.f(c.at!), lenOf(c, kind, g, true)];
      for (const x of t.clips) if (g.f(x.at!) >= a) x.at = g.s(g.f(x.at!) - ln);
    }
  } else if (op.op === "roll") {
    // 1.50: the cut before the clip moves by delta; the length is kept
    if (!onV1) return "roll_track";
    if (i === 0) return "roll_first";
    const [df, p] = [g.f(op.delta), v1[i - 1]];
    const pk = kindOf(p, takes);
    if (
      lenOf(p, pk, g) + df < Math.max(1, Math.floor(din(i - 1) / 2) + Math.floor(din(i) / 2)) ||
      lenOf(c, kind, g) - df < Math.max(1, Math.floor(din(i) / 2) + Math.floor(din(i + 1) / 2))
    )
      return "least";
    grow(p, pk, df, g);
    front(c, kind, df, g);
  } else if (op.op === "slip") {
    // 1.50: in and out move by delta; place and length kept
    if (kind === "image" || kind === "slot") return "slip_still";
    const df = g.f(op.delta);
    if (onV1) [c.in, c.out] = [g.s(g.f(c.in!) + df), g.s(g.f(c.out!) + df)];
    else {
      const ds = g.smp(g.s(df)); // on the sample grid
      [c.in, c.out] = [(g.smp(c.in!) + ds) / g.sr, (g.smp(c.out!) + ds) / g.sr];
    }
  } else if (op.op === "slide") {
    // 1.50: the clip moves along V1 by delta; its neighbours give and take the frames
    if (!onV1) return "slide_track";
    if (i === 0 || i === v1.length - 1) return "slide_end";
    const [df, p, nx] = [g.f(op.delta), v1[i - 1], v1[i + 1]];
    const [pk, nk] = [kindOf(p, takes), kindOf(nx, takes)];
    if (
      lenOf(p, pk, g) + df < Math.max(1, Math.floor(din(i - 1) / 2) + Math.floor(din(i) / 2)) ||
      lenOf(nx, nk, g) - df < Math.max(1, Math.floor(din(i + 1) / 2) + Math.floor(din(i + 2) / 2))
    )
      return "least";
    grow(p, pk, df, g);
    front(nx, nk, df, g);
  } else if (op.op === "overwrite") {
    return overwrite(d, t, i, op, takes, g);
  } else if (op.op === "replace") {
    if (!(c.alternatives ?? []).includes(op.take_id)) return "not_alternative";
    const take = takes[op.take_id];
    if (!take) return "take_not_found";
    const ln = lenOf(c, kind, g, !onV1);
    const oldIn = kind === "video" || kind === "audio" ? g.f(c.in!) : 0;
    delete c.in;
    delete c.out;
    delete c.hold;
    if (take.kind === "image" && onV1) c.hold = g.s(ln);
    else {
      const src = g.f(take.duration_s!);
      const i_ = Math.min(oldIn, src - 1);
      c.in = g.s(i_);
      c.out = g.s(Math.min(i_ + ln, src));
    }
    c.take_id = op.take_id;
  } else if (op.op === "set") {
    for (const k of ["gain_db", "mute", "hold", "transition_in", "link"] as const) // link: 1.50
      if (k in op) {
        if (op[k] === null) delete c[k];
        else (c as Record<string, unknown>)[k] = op[k];
      }
  } else if (op.op === "fill") {
    const take = takes[op.take_id];
    const ln = lenOf(c, kind, g);
    const cur = tk(takes, c.take_id);
    const [i0, o0] = [c.in, c.out];
    const sm = take.source_map;
    delete c.in;
    delete c.out;
    delete c.hold;
    delete c.shot;
    if (!onV1) {
      // 1.32, an audio track (New line, a line's re-render): the whole take at the slot's start
      c.in = 0;
      c.out = take.duration_s;
    } else if (take.kind === "image") c.hold = g.s(ln);
    else if (sm) {
      // the generated part; replacing its source (or a take made from it): the clip's window, grown on a side only
      // where the generated part lies outside the source (extend), never by the 8-frame snap (retake)
      let [a, b] = [sm.new_in_s, sm.new_out_s];
      const off = sm.offset_s;
      const mine =
        c.take_id === sm.take_id
          ? 0
          : cur?.source_map?.take_id === sm.take_id
            ? cur.source_map.offset_s
            : null;
      if (mine !== null && o0 !== undefined) {
        const end = off + takes[sm.take_id].duration_s!;
        if (!(g.f(a) < g.f(off))) a = i0! - mine + off;
        if (!(g.f(b) > g.f(end))) b = o0 - mine + off;
      }
      c.in = g.s(Math.max(g.f(a), 0));
      c.out = g.s(Math.min(g.f(b), g.f(take.duration_s!)));
    } else {
      c.in = 0;
      c.out = g.s(Math.min(g.f(take.duration_s!), ln));
    }
    c.take_id = op.take_id;
    c.alternatives = [...(c.alternatives ?? []), op.take_id];
  } else return "unknown_op";
  return d;
}

/** Split at the playhead `at` the clip of `t` under it; the right part gets `id`, keeps the take and alternatives,
 * never the dissolve or the shot. From 1.50 a V1 split cuts the clip's linked sound clips at that frame too (`ids`, in
 * track order) and relinks them. */
function split(
  d: Doc,
  t: Track,
  op: { at: number; id: string; ids?: string[] },
  takes: Takes,
  g: Grid,
): Doc | string {
  const f = g.f(op.at);
  const onV1 = t === d.tracks[0];
  let hit: [number, Clip, number, number] | null = null;
  t.clips.forEach((c, k) => {
    const a = g.f(c.at!);
    const ln = lenOf(c, kindOf(c, takes), g, !onV1);
    if (a <= f && f < a + ln) hit = [k, c, f - a, ln];
  });
  if (!hit) return "split_none";
  const [k, c, p, ln] = hit as [number, Clip, number, number];
  if (p === 0) return "split_edge";
  if (c.shot) return "split_shot";
  cutClip(t, k, f, f - p, ln, op.id, onV1, takes, g);
  if (onV1) {
    const ids = [...(op.ids ?? [])];
    for (const at of d.tracks.slice(1)) {
      const hit = inside(at, f, false, takes, g);
      if (!hit || at.clips[hit[0]].link !== c.id || at.clips[hit[0]].shot) continue;
      const id = ids.shift();
      if (id === undefined) return "ids_short";
      cutClip(at, hit[0], f, hit[1], hit[2], id, false, takes, g);
    }
    relink(d, new Map([[op.id, c.id]]), takes, g);
  }
  return d;
}

/** t_overwrite (1.50, Overwrite): the clip goes in at `at` over what is there. V1: it leaves an empty slot of its
 * length, the frames [at, at + length) are cut out of V1 and it goes in there, an empty slot first past V1's end; no
 * ripple. Audio (on `track`, default its own): the clips under it are cut around it, a right part starting on the first
 * frame at or after its end. New ids from `ids`: a right part, then (V1) the slot it leaves, then the gap. */
function overwrite(
  d: Doc,
  t: Track,
  i: number,
  op: { at: number; track?: string; ids?: string[] },
  takes: Takes,
  g: Grid,
): Doc | string {
  const c = t.clips[i];
  const ids = [...(op.ids ?? [])];
  const v1 = d.tracks[0].clips;
  if (t === d.tracks[0]) {
    if ((op.track ?? "V1") !== "V1") return "overwrite_track";
    const kind = kindOf(c, takes);
    const [n, lo] = [lenOf(c, kind, g), g.f(op.at)];
    const hi = lo + n;
    const moved: Clip = { ...c };
    delete moved.transition_in;
    const [starts, end] = v1Starts(v1, takes, g);
    const hole: Clip = { id: "", take_id: null, hold: g.s(n) };
    v1[i] = hole;
    if (i + 1 < v1.length) delete v1[i + 1].transition_in;
    const parts = v1.map((x, k) => {
      const xk = kindOf(x, takes);
      return [x, starts[k], starts[k] + lenOf(x, xk, g), xk] as [Clip, number, number, Kind];
    });
    if (parts.some(([x, a, b]) => x.shot && a < hi && b > lo)) return "overwrite_shot";
    const root = new Map<string, string>();
    let right: Clip | null = null;
    const span = parts.find(([, a, b]) => a < lo && b > hi);
    if (span) {
      const id = ids.shift();
      if (id === undefined) return "ids_short";
      right = { ...structuredClone(span[0]), id };
      delete right.transition_in;
      delete right.shot;
      front(right, span[3], hi - span[1], g);
      root.set(id, span[0].id);
    }
    const holeId = ids.shift();
    if (holeId === undefined) return "ids_short";
    hole.id = holeId;
    const gap: Clip[] = [];
    if (lo > end) {
      const id = ids.shift();
      if (id === undefined) return "ids_short";
      gap.push({ id, take_id: null, hold: g.s(lo - end) });
    }
    const before: Clip[] = [];
    const after: Clip[] = right ? [right] : [];
    for (const [x, a, b, xk] of parts) {
      if (b <= lo) before.push(x);
      else if (a >= hi) after.push(x);
      else if (a < lo) {
        grow(x, xk, lo - b, g);
        before.push(x);
      } else if (b > hi) {
        front(x, xk, hi - a, g);
        after.push(x);
      }
    }
    if (after.length) delete after[0].transition_in;
    v1.splice(0, v1.length, ...before, ...gap, moved, ...after);
    relink(d, root, takes, g);
    return d;
  }
  const tt = d.tracks.find((x) => x.name === (op.track ?? t.name));
  if (!tt) return "unknown_track";
  if (tt === d.tracks[0]) return "overwrite_track";
  t.clips.splice(i, 1);
  const at = g.snap(op.at);
  const lo = g.smp(at);
  const hi = lo + aLen(c, g);
  let fe = g.f(hi / g.sr);
  if (g.smp(g.s(fe)) < hi) fe += 1; // the first frame at or after its end
  const re = g.smp(g.s(fe));
  const under = (x: Clip) => g.smp(x.at!) < hi && g.smp(x.at!) + aLen(x, g) > lo;
  if (tt.clips.some((x) => under(x) && x.shot)) return "overwrite_shot";
  const keep: Clip[] = [];
  for (const x of [...tt.clips].sort((p, q) => g.smp(p.at!) - g.smp(q.at!))) {
    if (!under(x)) {
      keep.push(x);
      continue;
    }
    const [xs, is] = [g.smp(x.at!), g.smp(x.in!)];
    const xe = xs + aLen(x, g);
    if (xs < lo) {
      const x2: Clip = { ...structuredClone(x), in: (is + re - xs) / g.sr, at: g.s(fe) };
      x.out = (is + lo - xs) / g.sr;
      keep.push(x);
      if (xe > re) {
        const id = ids.shift();
        if (id === undefined) return "ids_short";
        keep.push({ ...x2, id });
      }
    } else if (xe > re) {
      x.in = (is + re - xs) / g.sr;
      x.at = g.s(fe);
      keep.push(x);
    }
  }
  tt.clips = [...keep, { ...c, at }];
  return d;
}

export type Applied = { document: Norm } | { problems: Problem[] } | { error: string };

/** An edit, then normalize against the edited document as the previous revision (t_apply). `fill` then drops a
 * dissolve the new take cannot carry and cuts an audio-track clip a shorter V1 now ends inside (removed when it
 * starts at or after the end), so a fill never fails. */
export function apply(doc: Doc, op: Op, takes: Takes): Applied {
  const raw = edit(doc, op, takes);
  if (typeof raw === "string") return { error: raw };
  let r = normalize(raw, takes, doc);
  if (op.op === "fill" && "problems" in r) {
    const bad = new Set(
      r.problems
        .filter((p) => p[0] === "dissolve_handles" || p[0] === "dissolve_length")
        .map((p) => p[2]),
    );
    const probs = r.problems;
    const past = new Set(probs.filter((p) => p[0] === "past_end").map((p) => p[2]));
    const g = new Grid(raw.target);
    for (const t of raw.tracks.slice(1)) {
      // 1.32: a filled line running into the next clip of its track is cut at its start
      const order = [...t.clips].sort((a, b) => g.smp(a.at!) - g.smp(b.at!));
      order.slice(1).forEach((c, k) => {
        const prev = order[k];
        if (probs.some((p) => p[0] === "overlap" && p[2] === c.id) && "out" in prev)
          prev.out = (g.smp(prev.in!) + g.smp(c.at!) - g.smp(prev.at!)) / g.sr;
      });
    }
    let frames = 0;
    for (const c of raw.tracks[0].clips) {
      if (bad.has(c.id)) delete c.transition_in;
      frames += lenOf(c, kindOf(c, takes), g);
    }
    const end = g.smp(g.s(frames));
    for (const t of raw.tracks.slice(1)) {
      t.clips = t.clips.filter((c) => !past.has(c.id) || g.smp(c.at!) < end);
      for (const c of t.clips)
        if (!past.has(c.id)) continue;
        else if ("hold" in c)
          c.hold = Math.min(g.smp(c.hold!), end - g.smp(c.at!)) / g.sr; // 1.32: a line placeholder
        else c.out = Math.min(g.smp(c.out!), g.smp(c.in!) + end - g.smp(c.at!)) / g.sr; // never longer
    }
    r = normalize(raw, takes, doc);
  }
  return r;
}

/** What an edge snaps to: 0 and every clip's start and end on every track, but the dragged clip's own. */
export function cuts(doc: Doc, takes: Takes, exclude?: string): number[] {
  const g = new Grid(doc.target);
  const out = new Set([0]);
  doc.tracks.forEach((t, ti) => {
    for (const c of t.clips)
      if (c.id !== exclude) {
        out.add(c.at!);
        out.add(
          ti === 0
            ? g.s(g.f(c.at!) + lenOf(c, kindOf(c, takes), g))
            : c.at! + ("hold" in c ? c.hold! : c.out! - c.in!),
        );
      }
  });
  return [...out].sort((a, b) => a - b);
}

/** A dragged edge: the nearest cut when within MAGNET_PX on screen (a tie: the earlier), else its frame. */
export function snapPoint(t: number, list: number[], pxPerS: number, g: Grid): number {
  let best: number | undefined;
  for (const c of list)
    if (
      best === undefined ||
      Math.abs(c - t) < Math.abs(best - t) ||
      (Math.abs(c - t) === Math.abs(best - t) && c < best)
    )
      best = c;
  return best !== undefined && Math.abs(best - t) * pxPerS <= MAGNET_PX ? best : g.snap(t);
}

/** Moving a V1 clip: its index among the others = how many others have their midpoint before `mid`. */
export function dropIndex(doc: Doc, clip: string, mid: number, takes: Takes): number {
  const g = new Grid(doc.target);
  const m2 = 2 * g.f(mid);
  return doc.tracks[0].clips.filter(
    (c) => c.id !== clip && 2 * g.f(c.at!) + lenOf(c, kindOf(c, takes), g) < m2,
  ).length;
}

export type Picture = { clip: string; t: number | null; opacity: number; offline?: true };
export type Sound = { clip: string; t: number; gain_db: number; fade: number };

/** What the preview shows and plays at sequence frame f (frame_at): pictures bottom first and sounds. */
export function frameAt(
  doc: Doc,
  f: number,
  takes: Takes,
): { pictures: Picture[]; sounds: Sound[] } {
  const g = new Grid(doc.target);
  const pics: Picture[] = [];
  const sounds: Sound[] = [];
  const v1t = doc.tracks[0];
  const v1 = v1t.clips;
  const dins = [...v1.map((c) => c.transition_in?.frames ?? 0), 0];
  v1.forEach((c, k) => {
    const kind = kindOf(c, takes);
    const a = g.f(c.at!);
    const ln = lenOf(c, kind, g);
    const start = a - Math.floor(dins[k] / 2);
    const end = a + ln + Math.floor(dins[k + 1] / 2);
    if (kind === "slot" || !(start <= f && f < end)) return;
    const mixIn = dins[k] && f < a + Math.floor(dins[k] / 2) ? (f - start) / dins[k] : 1;
    const outFrom = a + ln - Math.floor(dins[k + 1] / 2);
    const mixOut = dins[k + 1] && f >= outFrom ? (f - outFrom) / dins[k + 1] : 0;
    const src = kind === "video" ? g.s(g.f(c.in!) + f - a) : null;
    const pic: Picture = { clip: c.id, t: src, opacity: mixIn };
    if (c.offline) {
      pics.push({ ...pic, offline: true });
      return;
    }
    pics.push(pic);
    if (kind === "video" && takes[c.take_id!]?.has_audio && !c.mute && !v1t.mute)
      sounds.push({
        clip: c.id,
        t: src!,
        gain_db: (v1t.gain_db ?? 0) + (c.gain_db ?? 0),
        fade: mixIn < 1 ? mixIn : 1 - mixOut,
      });
  });
  for (const t of doc.tracks.slice(1))
    for (const c of t.clips) {
      const a = g.f(c.at!);
      if (
        c.take_id &&
        a <= f &&
        f < a + lenOf(c, "video", g, true) &&
        !c.offline &&
        !c.mute &&
        !t.mute
      )
        sounds.push({
          clip: c.id,
          t: c.in! + g.s(f - a),
          gain_db: (t.gain_db ?? 0) + (c.gain_db ?? 0),
          fade: 1,
        });
    }
  return { pictures: pics, sounds };
}

// ---------------------------------------------------------------- editor helpers (not in check.py)

/** The rules' facts of a sequence's takes (Timeline.takes): an offline one is absent. */
export function factsOf(takes: Record<string, S<"TimelineTake">>): Takes {
  return Object.fromEntries(
    Object.entries(takes).map(([id, t]) => [
      id,
      {
        kind: t.kind,
        duration_s: t.video?.duration_s ?? t.audio?.duration_s,
        has_audio: t.kind === "audio" || (t.video?.has_audio ?? false),
      },
    ]),
  );
}

/** A new clip id, unique in the document. */
export function newId(doc: Doc): string {
  const used = new Set(doc.tracks.flatMap((t) => t.clips.map((c) => c.id)));
  for (;;) {
    const id = Math.random().toString(36).slice(2, 10);
    if (/^[a-z0-9]{1,16}$/.test(id) && !used.has(id)) return id;
  }
}

/** A clip's start and end on the sequence, in seconds (V1 and audio tracks alike). */
export function span(c: Clip, track: number, takes: Takes, g: Grid): [number, number] {
  const len = lenOf(c, kindOf(c, takes), g, track > 0);
  return [c.at!, g.s(g.f(c.at!) + len)];
}

/** "0:05.12": minutes, seconds and hundredths. */
export function timecode(s: number): string {
  const cs = Math.max(0, Math.round(s * 100));
  const m = Math.floor(cs / 6000);
  const sec = Math.floor((cs % 6000) / 100);
  return `${m}:${String(sec).padStart(2, "0")}.${String(cs % 100).padStart(2, "0")}`;
}

/** "0:21": minutes and whole seconds. */
export const shortTime = (s: number) =>
  `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

/** The document a write sends (TimelineUpdate): what the server ignores or answers only is left out. */
export function writable(doc: Doc): { target: Target; tracks: Track[] } {
  return {
    target: doc.target,
    tracks: doc.tracks.map((t) => ({
      kind: t.kind,
      gain_db: t.gain_db,
      mute: t.mute,
      clips: t.clips.map((c) => {
        const out: Clip = { ...c };
        delete out.offline;
        if (t.kind === "video") delete out.at;
        if (out.shot) out.shot = shotRef(out.shot);
        return out;
      }),
    })),
  };
}

/** The V1 clip under sequence frame f (its index), or -1. */
export function clipAt(doc: Doc, f: number, takes: Takes): number {
  const g = new Grid(doc.target);
  return doc.tracks[0].clips.findIndex((c) => {
    const a = g.f(c.at!);
    return a <= f && f < a + lenOf(c, kindOf(c, takes), g);
  });
}
