// Video params (contracts/README.md "Video", 1.12): auto length on a `frames` param, keyframe positions (a float item
// field with `position: seconds`), ints bounded to `values` / `values_by` (LTX's stage steps) and the one-value
// enum (a bake-only LoRA mode). Generic: read from the node schemas, nothing of one family.
import type { S } from "@/lib/api";
import { gridOf } from "@/lib/media";
import { fpsKey, toRaw, type Field, type Raw } from "@/lib/workflow";

type Param = S<"ParamSchema">;
type Row = S<"RowItem">;
/** an item field's schema (a ParamSchema without name) */
type Spec = Partial<Param> & { position?: string };

/** Python's round(): halves go to the even neighbour, as the runner rounds. */
export function roundEven(x: number) {
  const f = Math.floor(x);
  const d = x - f;
  return d > 0.5 ? f + 1 : d < 0.5 ? f : f % 2 === 0 ? f : f + 1;
}

/** The frames the runner resolves an auto length to (README auto_frames): `seconds` clamped to the param's auto
 * seconds and its max, floored onto the grid, one step up when that fell below the minimum. */
export function autoFrames(p: Param, seconds: number, fps: number) {
  const g = gridOf(p);
  const auto = p.auto ?? { min_s: 0, max_s: Infinity };
  const top = g.min + Math.floor((g.max - g.min) / g.step) * g.step;
  const lo = roundEven(auto.min_s * fps);
  const hi = Math.min(roundEven(auto.max_s * fps), top);
  let f = Math.min(Math.max(roundEven(seconds * fps), lo), hi);
  f = g.min + Math.floor(Math.max(f - g.min, 0) / g.step) * g.step;
  return Math.min(f < lo ? f + g.step : f, top);
}

/** The shortest and longest clip an auto length can come out as, in frames (estimates use `hi`: "up to"). */
export const autoBounds = (p: Param, fps: number) => ({
  lo: autoFrames(p, 0, fps),
  hi: autoFrames(p, Infinity, fps),
});

/** The clip the form makes: its one frames field's value (a frame count, or "auto"), its frame rate, the last
 * frame a keyframe can sit on (for auto, the longest clip's) and `end`, the last frame every clip it can come out
 * as has (for auto, the shortest clip's): a new keyframe goes no later. Null without exactly one frames field and a
 * rate. */
export type Clip = { frames: number | "auto"; fps: number; last: number; end?: number };
export function clipOf(fs: Field[], raws: Record<string, Raw>): Clip | null {
  const all = fs.filter((f) => f.param.type === "frames");
  if (all.length !== 1) return null;
  const f = all[0];
  const raw = raws[f.key] ?? toRaw(f.initial, f.param);
  const rate = fs.find((x) => x.key === fpsKey(f));
  const fps = Number(
    rate ? (raws[rate.key] ?? toRaw(rate.initial, rate.param)) : f.siblings?.[f.param.fps ?? ""],
  );
  if (!(fps > 0)) return null;
  if (raw === "auto") {
    const b = autoBounds(f.param, fps);
    return { frames: "auto", fps, last: b.hi - 1, end: b.lo - 1 };
  }
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? { frames: n, fps, last: n - 1 } : null;
}

/** A rows param's item fields that are param schemas (not Image / Lora), with their specs. */
export const itemSpecs = (p: Param): [string, Spec][] =>
  Object.entries(p.item ?? {}).flatMap(([k, v]) =>
    typeof v === "object" ? [[k, v as unknown as Spec] as [string, Spec]] : [],
  );

/** The item field that is a time on the clip (`position: seconds`), if the item has one. */
export const positionOf = (p: Param) =>
  p.type === "rows" ? itemSpecs(p).find(([, s]) => s.position === "seconds") : undefined;

/** A row's value of an item field, its default when left out. */
export const itemValue = (row: Row, [k, s]: [string, Spec]) => row[k] ?? s.default;

/** The pixel frame a time lands on: round(s · fps), at most the clip's last frame. */
export const frameAt = (s: number, clip: Clip) => Math.min(roundEven(s * clip.fps), clip.last);

/** Each row's problem with its position, worded as studio words it after `<node>.<param>[i].<field>: ` (null: none):
 * past the clip's last frame, or a frame an earlier row has. With auto length only the second (the runner checks
 * the first once the length is known). */
export function positionErrors(p: Param, rows: Row[], clip: Clip | null): (string | null)[] {
  const pos = positionOf(p);
  if (!pos || !clip) return rows.map(() => null);
  const seen = new Map<number, number>();
  return rows.map((row, i) => {
    let fr = roundEven(Number(itemValue(row, pos)) * clip.fps);
    if (clip.frames !== "auto") {
      if (fr > clip.frames) return `frame ${fr} is past the clip's last frame ${clip.frames - 1}`;
      fr = Math.min(fr, clip.frames - 1);
    }
    const first = seen.get(fr);
    if (first) return `frame ${fr} is already used by row ${first}`;
    seen.set(fr, i + 1);
    return null;
  });
}

/** The frame a new keyframe goes on: the first frame when free, else the last, else the middle of the widest gap. */
export function freeFrame(used: number[], last: number) {
  if (!used.includes(0)) return 0;
  if (!used.includes(last)) return last;
  const s = [...new Set(used)].sort((a, b) => a - b);
  let best = [0, 0];
  for (let i = 1; i < s.length; i++)
    if (s[i] - s[i - 1] > best[1] - best[0]) best = [s[i - 1], s[i]];
  return Math.floor((best[0] + best[1]) / 2);
}

/** Rows a pick, upload or drop just added (an image not in `old`) placed on a free frame of the clip; the others as
 * they are. */
export function placeRows(p: Param, rows: Row[], old: Raw | undefined, clip: Clip | null): Row[] {
  const pos = positionOf(p);
  const img = Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Image");
  if (!pos || !img || !clip) return rows;
  const idOf = (r: Row) => (r[img] as { take_id?: string } | undefined)?.take_id;
  const had = new Set((Array.isArray(old) ? old : []).map(idOf));
  const used = rows
    .filter((r) => had.has(idOf(r)))
    .map((r) => frameAt(Number(itemValue(r, pos)), clip));
  return rows.map((r) => {
    if (had.has(idOf(r))) return r;
    const f = freeFrame(used, clip.end ?? clip.last);
    used.push(f);
    return { ...r, [pos[0]]: f / clip.fps };
  });
}

/** The values an int field takes now (`values`, narrowed by `values_by` to the node's value of its enum param, a
 * value not listed allowing all), or null when it has none. */
export function allowedInts(f: Field, raws: Record<string, Raw>): number[] | null {
  const p = f.param;
  if (p.type !== "int" || !p.values) return null;
  const vb = p.values_by;
  if (!vb) return p.values;
  const v = raws[`${f.node}.${vb.param}`] ?? f.siblings?.[vb.param];
  return vb.values[String(v)] ?? p.values;
}

/** Whether an enum item field offers a choice (a one-value enum, as a bake-only LoRA mode, is not drawn). */
export const choosable = (s: Spec | undefined) => (s?.enum?.length ?? 0) > 1;

/** Whether a row sits on frame 0 (a row of an item without a position always does). */
function onFirstFrame(p: Param, r: Row, clip: Clip | null) {
  const pos = positionOf(p);
  if (!pos) return true;
  const s = Number(itemValue(r, pos));
  return (clip ? frameAt(s, clip) : s) === 0;
}

/** The row a "First frame" tile shows: the one on frame 0, if any. */
export const firstFrameRow = (p: Param, rows: Row[], clip: Clip | null) =>
  rows.find((r) => onFirstFrame(p, r, clip));

/** Rows with `takeId` as the first frame: the row on frame 0 gets the image, else a new row goes there (first). */
export function setFirstFrame(p: Param, rows: Row[], takeId: string, clip: Clip | null): Row[] {
  const pos = positionOf(p);
  const img = Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Image") ?? "image";
  const ref = { take_id: takeId };
  const i = rows.findIndex((r) => onFirstFrame(p, r, clip));
  if (i >= 0) return rows.map((r, j) => (j === i ? { ...r, [img]: ref } : r));
  return [{ [img]: ref, ...(pos && { [pos[0]]: 0 }) }, ...rows];
}

/** A frames field's frame rate now (its `fps` field's value, else the node's set value when the workflow does not
 * show that field, as MiniMax H3's fixed 24), 0 when it names none. */
export const fpsOf = (f: Field, fs: Field[], raws: Record<string, Raw>) => {
  const r = fs.find((x) => x.key === fpsKey(f));
  return (
    Number(r ? (raws[r.key] ?? toRaw(r.initial, r.param)) : f.siblings?.[f.param.fps ?? ""]) || 0
  );
};
