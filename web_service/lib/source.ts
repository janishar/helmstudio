// Media inputs (contracts/README.md "Media inputs", "Timeline: Take actions"): a video or audio take as a workflow's
// input, read from the node schemas only. The family's frame grid (its `frames` param: LTX 9 + 8k up to 321) gives
// the readouts what the runner does with a span, an extension or an audio part (check.py retake_map, extend_map,
// audio_map); the timeline maps a selection to a take's own time. Pure: no React, no fetch.
import type { S } from "@/lib/api";
import { rnd } from "@/lib/timeline";

type Param = S<"ParamSchema">;
type Take = S<"Take">;

/** A family's frame grid: `min + k × step` up to `max` (its `frames` param). */
export type FrameGrid = { min: number; step: number; max: number };

/** The frame grid of a family, from the palette's first `frames` param of a node of it; null when it has none. */
export function familyGrid(
  nodes: { family: string | null; params: Param[] }[],
  family: string | undefined,
): FrameGrid | null {
  const p = nodes
    .filter((n) => n.family === family)
    .flatMap((n) => n.params)
    .find((x) => x.type === "frames");
  return p ? { min: p.min ?? 1, step: p.step ?? 1, max: p.max ?? Infinity } : null;
}

/** A source's frames floored onto the grid (LTX: 8k + 1, at least 9): what the runner reads. */
export const floorGrid = (n: number, g: FrameGrid) =>
  g.min + g.step * Math.max(0, Math.floor((n - g.min) / g.step));

export type SourceMap = { offset_s: number; new_in_s: number; new_out_s: number };
export type Mapped =
  { latent?: number[] | number; frames: number; map: SourceMap } | { error: string };

/** retake_map: the span on whole frames f = rnd(t·fps), latent S = ⌊(f_start + step/2) / step⌋, E likewise (at
 * least S + 1, an end at or past the clip's end = the last), pixels max(step·S − step + 1, 0) … step·(E − 1). */
export function retakeMap(
  start: number,
  end: number,
  fps: number,
  frames: number,
  g: FrameGrid,
): Mapped {
  const n = floorGrid(frames, g);
  const [fs, fe, h] = [rnd(start * fps), rnd(end * fps), g.step / 2];
  const s = Math.floor((fs + h) / g.step);
  const last = Math.floor((n - 1) / g.step) + 1;
  const e = fe >= n ? last : Math.min(Math.max(Math.floor((fe + h) / g.step), s + 1), last);
  if (s >= e) return { error: `nothing to regenerate on the ${g.step}-frame grid` };
  const from = Math.max(g.step * s - g.step + 1, 0);
  return {
    latent: [s, e],
    frames: n,
    map: { offset_s: 0, new_in_s: from / fps, new_out_s: (g.step * (e - 1) + 1) / fps },
  };
}

/** extend_map: N = max(1, ⌈s·fps / step⌉) grid steps after or before the floored source; at most `max` frames. */
export function extendMap(
  seconds: number,
  direction: string,
  fps: number,
  frames: number,
  g: FrameGrid,
  max = g.max,
): Mapped {
  const n = floorGrid(frames, g);
  const k = Math.max(1, Math.ceil((seconds * fps) / g.step - 1e-9));
  const total = n + g.step * k;
  const add = (g.step * k) / fps;
  if (total > max) return { error: `the clip would be ${total} frames; at most ${max}` };
  const map =
    direction === "after"
      ? { offset_s: 0, new_in_s: n / fps, new_out_s: total / fps }
      : { offset_s: add, new_in_s: 0, new_out_s: add };
  return { latent: k, frames: total, map };
}

/** audio_map: the clip's frames = the part in..out at fps, floored onto the grid (at most its max). */
export function audioMap(inS: number, outS: number, fps: number, g: FrameGrid): Mapped {
  const base = g.min - g.step; // LTX: 1, so 8k + 1
  const raw = Math.floor((outS - inS) * fps + 1e-6);
  const f = Math.min(g.max, Math.floor((raw - base) / g.step) * g.step + base);
  if (f < g.min) return { error: `the part from in to out is shorter than ${g.min} frames` };
  return { frames: f, map: { offset_s: 0 - inS, new_in_s: 0, new_out_s: f / fps } };
}

/** What a take is as a media input needs it (Take's video or audio facts). */
export type Facts = {
  id: string;
  kind: string;
  width?: number | null;
  height?: number | null;
  fps?: number;
  frames?: number;
  duration_s?: number;
  has_audio?: boolean;
};
export const factsOf = (t: Take): Facts => ({
  id: t.id,
  kind: t.kind,
  width: t.width,
  height: t.height,
  fps: t.video?.fps,
  frames: t.video?.frames,
  duration_s: t.video?.duration_s ?? t.audio?.duration_s,
  has_audio: t.video ? t.video.has_audio : t.kind === "audio",
});

const s1 = (x: number) => x.toFixed(1);

/** media_fit: why a take does not fit a media param (studio's 422 after `<node>.<param>: `), else null. */
export function mediaFit(p: Param, t: Facts): string | null {
  const kinds = p.accepts ?? [];
  const picture = t.kind === "video" && kinds.includes("video");
  const sound =
    kinds.includes("audio") && (t.kind === "audio" || t.kind === "video") && (t.has_audio ?? true);
  if (!picture && !sound)
    return kinds.length === 1 && kinds[0] === "audio"
      ? `take ${t.id} has no sound`
      : `take ${t.id} is not a video`;
  if (picture && p.grid && ((t.width ?? 0) % p.grid || (t.height ?? 0) % p.grid))
    return `take ${t.id} is ${t.width} × ${t.height}; this input needs sides on a ${p.grid} px grid`;
  if (picture && p.max_frames && (t.frames ?? 0) > p.max_frames)
    return `take ${t.id} has ${t.frames} frames; this input takes at most ${p.max_frames}`;
  // API 1.22: a trim cannot make a take longer
  if (p.min_s && (t.duration_s ?? 0) < p.min_s)
    return `take ${t.id} lasts ${s1(t.duration_s!)} s; this input takes at least ${s1(p.min_s)} s`;
  if (p.max_s && (t.duration_s ?? 0) > p.max_s && !p.trim)
    return `take ${t.id} lasts ${s1(t.duration_s!)} s; this input takes at most ${s1(p.max_s)} s`;
  return null;
}

// ---------------------------------------------------------------- rows of media (API 1.22)

/** A rows param's one required `media` item field and its schema (MiniMax H3's videos and audios), else null. */
export function mediaItem(p: Param): [string, Param] | null {
  if (p.type !== "rows") return null;
  const m = Object.entries(p.item ?? {}).filter(
    ([, v]) =>
      typeof v === "object" &&
      (v as unknown as Param).type === "media" &&
      !(v as unknown as Param).optional,
  );
  return m.length === 1 ? [m[0][0], { ...(m[0][1] as unknown as Param), name: m[0][0] }] : null;
}

/** A rows param's optional `media` item fields (API 1.37: MiniMax H3's soundtrack), in item order. */
export function optionalMedia(p: Param): [string, Param][] {
  return Object.entries(p.item ?? {}).flatMap(([k, v]) =>
    typeof v === "object" &&
    (v as unknown as Param).type === "media" &&
    (v as unknown as Param).optional
      ? [[k, { ...(v as unknown as Param), name: k }] as [string, Param]]
      : [],
  );
}

/** The part a media value reads: in..out with trim (in 0, out min(duration, max_s) when left out), else the whole
 * take. */
export function partOf(v: S<"MediaRef">, d: number, p: Param): [number, number] {
  return p.trim ? [v.in ?? 0, v.out ?? Math.min(d, p.max_s ?? d)] : [0, d];
}

/** media_value: why one media value does not fit (the 422 text after its path), else null. */
export function mediaValueError(p: Param, v: S<"MediaRef">, t: Facts | null): string | null {
  if (!t) return `take ${v.take_id} not found`;
  const unfit = mediaFit(p, t);
  if (unfit) return unfit;
  if (!p.trim)
    return v.in != null || v.out != null ? "this input takes the whole take (no in or out)" : null;
  const d = t.duration_s ?? 0;
  const [a, b] = partOf(v, d, p);
  if (a >= b) return "in must be before out";
  if (b > d) return `out ${s1(b)} is past the take's end ${s1(d)}`;
  // within 1e-9 s of a limit is at it (studio's snapMedia: 4.1 − 1.1 is 2.9999999999999996)
  if (p.max_s && b - a > p.max_s + 1e-9)
    return `the part lasts ${s1(b - a)} s; this input takes at most ${s1(p.max_s)} s`;
  if (p.min_s && b - a < p.min_s - 1e-9)
    return `the part lasts ${s1(b - a)} s; this input takes at least ${s1(p.min_s)} s`;
  return null;
}

/** media_rows for one rows param of media (studio's 422 with its path): each item (from 1), then `max_total_s`.
 * `facts`: a take's facts, null when it is gone, undefined while it loads (not checked yet). */
export function mediaRowsError(
  path: string,
  p: Param,
  rows: S<"RowItem">[],
  facts: (id: string) => Facts | null | undefined,
): string | null {
  const m = mediaItem(p);
  if (!m) return null;
  const [k, spec] = m;
  let total = 0;
  for (const [i, row] of rows.entries()) {
    const v = row[k] as S<"MediaRef"> | undefined;
    if (!v?.take_id) return `${path}[${i + 1}].${k}: a take is required`;
    const t = facts(v.take_id);
    if (t === undefined) return null;
    const err = mediaValueError(spec, v, t);
    if (err) return `${path}[${i + 1}].${k}: ${err}`;
    const [a, b] = partOf(v, t!.duration_s ?? 0, spec);
    total += b - a;
  }
  if (p.max_total_s && total > p.max_total_s + 1e-9)
    return `${path}: the takes last ${s1(total)} s in all; at most ${s1(p.max_total_s)} s`;
  // API 1.37: then each item's optional media fields, empty or checked the same way (check.py media_rows)
  for (const [i, row] of rows.entries())
    for (const [o, ospec] of optionalMedia(p)) {
      const v = row[o] as S<"MediaRef"> | null | undefined;
      if (!v?.take_id) continue;
      const t = facts(v.take_id);
      if (t === undefined) return null;
      const err = mediaValueError(ospec, v, t);
      if (err) return `${path}[${i + 1}].${o}: ${err}`;
    }
  return null;
}

/** Whether a take shows in a media param's picker: its kind fits (an audio input takes a video with sound). */
export const pickable = (p: Param, t: Take) => {
  const kinds = p.accepts ?? [];
  return (
    (t.kind === "video" && kinds.includes("video")) ||
    (kinds.includes("audio") &&
      (t.kind === "audio" || (t.kind === "video" && !!t.video?.has_audio)))
  );
};

/** A media value as the trim control sets it: in / out snapped (a video's frames, audio to the ms), out within
 * the take (an off-grid end: its last grid point) and at most `max_s` past in. */
export function trimmed(
  v: S<"MediaRef">,
  t: Facts,
  p: Param,
  edge: "in" | "out",
  to: number,
): S<"MediaRef"> {
  const d = t.duration_s ?? 0;
  const u = t.kind === "video" && t.fps ? t.fps : 1000;
  const a = v.in ?? 0;
  const b = v.out ?? Math.min(d, p.max_s ?? d);
  // snapped, never past the last grid point at or before the end (studio's and the runner's bound)
  const x = Math.min(rnd(Math.max(0, Math.min(d, to)) * u), Math.floor(d * u + 1e-9)) / u;
  const m = p.max_s ?? Infinity; // the moved edge stops max_s from the other, on the grid
  return edge === "in"
    ? { ...v, in: Math.max(Math.min(x, b), Math.ceil((b - m) * u - 1e-9) / u), out: b }
    : { ...v, in: a, out: Math.min(Math.max(x, a), Math.floor((a + m) * u + 1e-9) / u) };
}

/** A span with one edge moved to `to` (take seconds), snapped to the take's frames and kept within it. */
export function spanEdge(
  v: S<"SpanValue">,
  edge: "start" | "end",
  to: number,
  fps: number,
  duration: number,
): S<"SpanValue"> {
  const x = rnd(Math.max(0, Math.min(duration, to)) * fps) / fps;
  return edge === "start"
    ? { start: Math.min(x, v.end), end: v.end }
    : { start: v.start, end: Math.max(x, v.start) };
}

// ---------------------------------------------------------------- the timeline's take actions

/** The sequence range [a, b) on a V1 clip at `at` (sequence seconds) showing [in, out) of its take: clamped to
 * the clip, mapped to the take's own time t − at + in and snapped to its frames. Null when it misses the clip. */
export function rangeInTake(
  range: [number, number] | null,
  clip: { at: number; in: number; out: number },
  fps: number,
): [number, number] | null {
  const end = clip.at + clip.out - clip.in;
  const [a, b] = range ?? [clip.at, end];
  const snap = (t: number) => rnd((t - clip.at + clip.in) * fps) / fps;
  const [lo, hi] = [snap(Math.max(a, clip.at)), snap(Math.min(b, end))];
  return lo < hi ? [lo, hi] : null;
}

/** Where an action's result goes: Replace the clip, or a new slot after it (before it: an extend before). */
export type Placement = "replace" | "insert";
export const insertsBefore = (offer: S<"TakeOffer">) =>
  Object.entries(offer.inputs).some(([k, v]) => k.endsWith(".direction") && v === "before");

/** The shot's slot (TimelineSlot) for an action's result: Replace = the clip; insert = after it, or at its start
 * for an extend before (never `after`), `length` the estimate. */
export function actionSlot(
  offer: S<"TakeOffer">,
  clip: { id: string; at: number },
  placement: Placement,
  length: number,
  id: string,
): S<"TimelineSlot"> {
  if (placement === "replace") return { clip: clip.id };
  return insertsBefore(offer) ? { at: clip.at, length, id } : { after: clip.id, length, id };
}

/** The placeholder's length for an insert: an extension's seconds (whole grid steps), a span's or a part's
 * length. */
export function estimateLength(
  inputs: S<"RunInputs">,
  fps: number,
  g: FrameGrid | null,
  maxS?: number,
): number {
  for (const [k, v] of Object.entries(inputs)) {
    if (k.endsWith(".seconds") && typeof v === "number")
      return g ? (g.step * Math.max(1, Math.ceil((v * fps) / g.step - 1e-9))) / fps : v;
    if (v && typeof v === "object" && "start" in v && "end" in v) return v.end - v.start;
    if (v && typeof v === "object" && "take_id" in v && "out" in v && v.out != null)
      return Math.min(v.out - (v.in ?? 0), maxS ?? Infinity);
  }
  return 3;
}

/** "Retake a section" → "Retaking", "Extend" → "Extending": the placeholder's title; other actions as they are. */
export function progressive(action: string): string {
  const [w, next] = action.split(" ");
  if (next && next !== "a" && next !== "the") return action;
  return (w.endsWith("e") ? w.slice(0, -1) : w) + "ing";
}

// ---------------------------------------------------------------- removing an audio track

/** Removing audio track `name` (A2…A9; A1 is V1's own sound and V1 stays): null when it cannot go, else the
 * confirm to ask first (null: an empty track goes at once). */
export function removeTrack(
  tracks: { name?: string; kind: string; clips: unknown[] }[],
  name: string,
): { confirm: string | null } | null {
  const t = tracks.find((x) => x.name === name);
  if (!t || t.kind !== "audio") return null;
  const n = t.clips.length;
  return { confirm: n ? `Remove ${name} and its ${n} clip${n === 1 ? "" : "s"}?` : null };
}

/** An offer as "Use in…" lists it: "<action> · <title>", or the title. */
export const offerLabel = (o: Pick<S<"TakeOffer">, "action" | "title">) =>
  o.action ? `${o.action} · ${o.title}` : o.title;

/** The timeline's range after Mark in (I) or Mark out (O) at the playhead `at` (sequence seconds, on frames): the
 * other edge stays unless the new one passes it (then it goes to the sequence's start or end). */
export function markRange(
  range: [number, number] | null,
  edge: "in" | "out",
  at: number,
  end: number,
): [number, number] | null {
  const [a, b] = range ?? [0, end];
  const r: [number, number] = edge === "in" ? [at, b > at ? b : end] : [a < at ? a : 0, at];
  return r[0] < r[1] ? r : null;
}

/** API 1.47 (contracts/README.md "Sound limits"): the seconds and inputs of sound a node's rows of media carry
 * together: each audio row's part; each video row whose `sound` is keep (the take has sound) or replace (with its
 * soundtrack), reading min(its part, the soundtrack's part, the clip's seconds). */
export function soundCount(
  rows: { p: Param; items: S<"RowItem">[] }[],
  facts: (id: string) => Facts | null | undefined,
  clipS: number,
): { s: number; n: number } {
  let s = 0;
  let n = 0;
  const len = (v: unknown, spec: Param) => {
    if (!v || typeof v !== "object" || !("take_id" in v)) return null;
    const ref = v as S<"MediaRef">;
    const [a, b] = partOf(ref, facts(ref.take_id)?.duration_s ?? 0, spec);
    return b - a;
  };
  for (const { p, items } of rows) {
    const m = mediaItem(p);
    if (!m) continue;
    const [k, spec] = m;
    const sound = (p.item?.sound as Param | undefined)?.default;
    for (const r of items) {
      const part = len(r[k], spec);
      if (part == null) continue;
      if (!spec.accepts?.includes("video")) {
        s += part;
        n++;
        continue;
      }
      const how = (r.sound as string | undefined) ?? sound;
      const ref = r[k] as S<"MediaRef">;
      if (how === "keep" && facts(ref.take_id)?.has_audio) {
        s += Math.min(part, clipS);
        n++;
      } else if (how === "replace") {
        const [o, ospec] = optionalMedia(p)[0] ?? [];
        const st = o && ospec ? len(r[o], ospec) : null;
        if (st == null) continue;
        s += Math.min(part, st, clipS);
        n++;
      }
    }
  }
  return { s, n };
}

// ---------------------------------------------------------------- music and sound (plan 38, API 1.79)

/** The take actions of a music or sound take, in the strip's order (docs/mock/CreateMusicSound.dc.html frames 1, 4):
 * Repaint, Cover on music or Vary on sound, Extend. */
export const soundActions = (cls: "music" | "sound") =>
  cls === "music" ? ["Repaint", "Cover", "Extend"] : ["Repaint", "Vary", "Extend"];

/** The offer for each action among a take's offers (by `action`): the take's own family's first, else the first. */
export function actionOffers(
  offers: S<"TakeOffer">[],
  actions: string[],
  family?: string,
): Record<string, S<"TakeOffer"> | undefined> {
  return Object.fromEntries(
    actions.map((a) => [
      a,
      offers
        .filter((o) => o.action === a)
        .sort((x, y) => +(y.family === family) - +(x.family === family))[0],
    ]),
  );
}

/** "0:48" from seconds. */
const mmss = (s: number) => {
  const n = Math.round(s);
  return `${Math.floor(n / 60)}:${String(n % 60).padStart(2, "0")}`;
};
/** A range's readout on the Repaint stage: "0:48 – 1:05 · 17 s" (RangeBar in the Design System). */
export const rangeText = (a: number, b: number) =>
  `${mmss(a)} – ${mmss(b)} · ${Math.round(b - a)} s`;

/** A span on an audio take: the edge set to `t`, kept in the take and at least 0.5 s long, to the millisecond (studio
 * snaps a span on audio to whole ms). */
export function audioSpanEdge(
  v: S<"SpanValue">,
  edge: "start" | "end",
  t: number,
  duration: number,
): S<"SpanValue"> {
  const ms = (x: number) => Math.round(Math.max(0, Math.min(duration, x)) * 1000) / 1000;
  const min = Math.min(0.5, duration);
  return edge === "start"
    ? { start: ms(Math.min(t, v.end - min)), end: v.end }
    : { start: v.start, end: ms(Math.max(t, v.start + min)) };
}

/** The video take a sound was made from (MMAudio from a video, frame 5): the take its workflow's media param that
 * takes video held when it was made; undefined for a sound from words. */
export function videoOf(
  t: Pick<Take, "made_with">,
  w?: Pick<S<"Workflow">, "nodes" | "node_schemas">,
): string | undefined {
  if (!w || !t.made_with) return undefined;
  for (const [id, n] of Object.entries(w.nodes))
    for (const p of w.node_schemas[n.kind]?.params ?? [])
      if (p.type === "media" && p.accepts?.includes("video")) {
        const v = t.made_with.inputs[`${id}.${p.name}`];
        if (v && typeof v === "object" && "take_id" in v) return (v as S<"MediaRef">).take_id;
      }
  return undefined;
}
