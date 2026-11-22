// The Timeline's tools (plan 23 §27.2, §27.5, §27.7, §27.9; board docs/mock/TimelineTools.dc.html, option B): the five
// tools, the key map (matched on e.code: macOS Option and Shift change e.key), held vs tapped tool keys, Trim's grab
// zones, snapping with what it snapped to, clamping a drag to its bounds, merging key repeats into one op, the ids an
// op makes, the live region's words and the zoom field's text. Pure: the components draw and send.
import {
  Grid,
  apply,
  newId,
  snapPoint,
  span,
  timecode,
  type Norm,
  type Op,
  type Takes,
} from "@/lib/timeline";

export type Tool = "select" | "trim" | "blade" | "range" | "overwrite";

/** The tool picker's buttons in order (the board's names and keys). */
export const TOOLS: { id: Tool; name: string; key: string }[] = [
  { id: "select", name: "Select", key: "A" },
  { id: "trim", name: "Trim", key: "T" },
  { id: "blade", name: "Blade", key: "B" },
  { id: "range", name: "Range", key: "R" },
  { id: "overwrite", name: "Overwrite", key: "P" },
];

/** A tool key held longer than this is temporary (back on release); a tap is sticky. */
export const HOLD_MS = 250;
/** Key repeats (holding `[`) closer than this merge into one op. */
export const MERGE_MS = 400;
/** The max zoom: 64 px per frame (owner, open point 9). */
export const MAX_PX_PER_FRAME = 64;

/** A tool key's release: true when the tool stays (a tap), false when it reverts (held past HOLD_MS). */
export const sticky = (downAt: number, upAt: number) => upAt - downAt <= HOLD_MS;

export type KeyId =
  | `tool:${Tool}`
  | "escape"
  | "split"
  | "blade"
  | "blade_all"
  | "delete"
  | "lift"
  | "trim_in"
  | "trim_out"
  | "trim_in_back"
  | "trim_out_back"
  | "trim_in_here"
  | "trim_out_here"
  | "roll_back"
  | "roll_fwd"
  | "slip_back"
  | "slip_fwd"
  | "slide_back"
  | "slide_fwd"
  | "undo"
  | "redo"
  | "play"
  | "back"
  | "stop"
  | "fwd"
  | "frame_back"
  | "frame_fwd"
  | "second_back"
  | "second_fwd"
  | "move_back"
  | "move_fwd"
  | "cut_prev"
  | "cut_next"
  | "mark_prev"
  | "mark_next"
  | "start"
  | "end"
  | "mark_in"
  | "mark_out"
  | "mark_clip"
  | "clear_range"
  | "zoom_in"
  | "zoom_out"
  | "fit"
  | "snap"
  | "scrub"
  | "marker"
  | "sheet";

type Bind = { id: KeyId; code: string; shift?: true; alt?: true; mod?: true };
const b = (id: KeyId, code: string, m = ""): Bind => ({
  id,
  code,
  ...(m.includes("s") && { shift: true }),
  ...(m.includes("a") && { alt: true }),
  ...(m.includes("m") && { mod: true }),
});

/** Every key the Timeline takes (`mod`: ⌘ on a Mac, Ctrl elsewhere). */
export const KEYMAP: Bind[] = [
  b("tool:select", "KeyA"),
  b("tool:trim", "KeyT"),
  b("tool:blade", "KeyB"),
  b("tool:range", "KeyR"),
  b("tool:overwrite", "KeyP"),
  b("escape", "Escape"),
  b("split", "KeyS"),
  b("blade", "KeyB", "m"),
  b("blade_all", "KeyB", "sm"),
  b("delete", "Delete"),
  b("delete", "Backspace"),
  b("lift", "Delete", "s"),
  b("lift", "Backspace", "s"),
  b("trim_in", "BracketLeft"),
  b("trim_out", "BracketRight"),
  b("trim_in_back", "BracketLeft", "s"),
  b("trim_out_back", "BracketRight", "s"),
  b("trim_in_here", "BracketLeft", "a"),
  b("trim_out_here", "BracketRight", "a"),
  b("roll_back", "Comma", "a"),
  b("roll_fwd", "Period", "a"),
  b("slip_back", "Comma"),
  b("slip_fwd", "Period"),
  b("slide_back", "Comma", "s"),
  b("slide_fwd", "Period", "s"),
  b("undo", "KeyZ", "m"),
  b("redo", "KeyZ", "sm"),
  b("play", "Space"),
  b("back", "KeyJ"),
  b("stop", "KeyK"),
  b("fwd", "KeyL"),
  b("frame_back", "ArrowLeft"),
  b("frame_fwd", "ArrowRight"),
  b("second_back", "ArrowLeft", "s"),
  b("second_fwd", "ArrowRight", "s"),
  b("move_back", "ArrowLeft", "a"),
  b("move_fwd", "ArrowRight", "a"),
  b("cut_prev", "ArrowUp"),
  b("cut_next", "ArrowDown"),
  b("mark_prev", "ArrowUp", "a"),
  b("mark_next", "ArrowDown", "a"),
  b("start", "Home"),
  b("end", "End"),
  b("mark_in", "KeyI"),
  b("mark_out", "KeyO"),
  b("mark_clip", "KeyX"),
  b("clear_range", "KeyX", "a"),
  b("zoom_in", "Equal"),
  b("zoom_in", "Equal", "s"),
  b("zoom_in", "Equal", "m"),
  b("zoom_out", "Minus"),
  b("zoom_out", "Minus", "m"),
  b("fit", "KeyZ", "s"),
  b("snap", "KeyN"),
  b("scrub", "KeyS", "s"),
  b("marker", "KeyM"),
  b("sheet", "Slash", "s"),
];

/** The key map's action for a key event, or null. */
export function keyOf(e: {
  code: string;
  shiftKey: boolean;
  altKey: boolean;
  metaKey: boolean;
  ctrlKey: boolean;
}): KeyId | null {
  const mod = e.metaKey || e.ctrlKey;
  return (
    KEYMAP.find(
      (k) =>
        k.code === e.code && !!k.shift === e.shiftKey && !!k.alt === e.altKey && !!k.mod === mod,
    )?.id ?? null
  );
}

/** Trim's grab zones on a clip (x from its left edge, w its width): the cut with an abutting neighbour = roll (of
 * this clip's cut, or the next one's), near an edge = ripple trim, the body = slip (⌥: slide). */
export type Zone = "roll" | "roll_next" | "in" | "out" | "body";
export const ROLL_PX = 4;
export const EDGE_PX = 10;
export function trimZone(x: number, w: number, prev: boolean, next: boolean): Zone {
  const edge = Math.min(EDGE_PX, w / 3);
  const roll = Math.min(ROLL_PX, edge / 2);
  if (x < roll && prev) return "roll";
  if (w - x < roll && next) return "roll_next";
  if (x < edge) return "in";
  if (w - x < edge) return "out";
  return "body";
}

/** What a snap point is, named on the snap chip ("Snap · playhead 0:13.00"). */
export type SnapKind = "cut" | "playhead" | "range" | "marker";
export type SnapPt = { t: number; kind: SnapKind };
const RANK: SnapKind[] = ["playhead", "marker", "range", "cut"];

/** snapPoint over the points, saying which it held on (null: none, or snapping off: the frame only). */
export function snapTo(
  t: number,
  pts: SnapPt[],
  pps: number,
  g: Grid,
  on: boolean,
): { t: number; to: SnapPt | null } {
  if (!on) return { t: g.snap(t), to: null };
  const s = snapPoint(
    t,
    pts.map((p) => p.t),
    pps,
    g,
  );
  const hit = pts
    .filter((p) => p.t === s)
    .sort((p, q) => RANK.indexOf(p.kind) - RANK.indexOf(q.kind));
  return { t: s, to: hit[0] ?? null };
}

/** A drag's frames clamped to its bounds: the largest |k| ≤ |df| (same sign) the op accepts, by halving (`ok` is
 * monotone in |k|: past a handle stays past it). */
export function clampFrames(
  df: number,
  ok: (k: number) => boolean,
): { df: number; clamped: boolean } {
  if (!df || ok(df)) return { df, clamped: false };
  let [lo, hi] = [0, Math.abs(df)]; // lo fits (0: no change), hi does not
  const s = Math.sign(df);
  while (hi - lo > 1) {
    const mid = Math.floor((lo + hi) / 2);
    if (ok(s * mid)) lo = mid;
    else hi = mid;
  }
  return { df: s * lo, clamped: true };
}

/** `ok` for clampFrames: the op applies to the document. */
export const fits = (doc: Norm, op: Op, takes: Takes) => "document" in apply(doc, op, takes);

/** Two key presses' ops as one (§27.7): the same trim edge, roll, slip or slide of the same clip add their frames;
 * else null. */
export function merge(prev: Op | null, next: Op, g: Grid): Op | null {
  if (!prev || prev.op !== next.op) return null;
  const sum = (a: number, c: number) => g.s(g.f(a) + g.f(c));
  if (prev.op === "trim" && next.op === "trim")
    return prev.clip === next.clip && prev.edge === next.edge
      ? { ...prev, delta: sum(prev.delta, next.delta) }
      : null;
  if (
    (prev.op === "roll" || prev.op === "slip" || prev.op === "slide") &&
    next.op === prev.op &&
    prev.clip === next.clip
  )
    return { ...prev, delta: sum(prev.delta, next.delta) };
  return null;
}

/** The op with the clip ids it makes filled in (a split's new clip, an add's clips, a slot and its gap; from 1.50
 * the `ids` of the tools' ops, as many as any cut could need, and a marker's id). The same ids on a 409 re-apply. */
export function withIds(o: Op, d: Norm): Op {
  const id = () => newId(d);
  const fresh = (n: number) => {
    const ids: string[] = [];
    while (ids.length < n) {
      const x = id();
      if (!ids.includes(x)) ids.push(x);
    }
    return ids;
  };
  const many = 2 * d.tracks.length + 3; // a range cuts each track twice, then a slot; unused ones are ignored
  if (o.op === "split") {
    const [first, ...rest] = fresh(many + 1);
    return { ...o, id: o.id || first, ids: o.ids?.length ? o.ids : rest };
  }
  if (
    o.op === "split_all" ||
    o.op === "remove_range" ||
    o.op === "lift_range" ||
    o.op === "overwrite"
  )
    return { ...o, ids: o.ids?.length ? o.ids : fresh(many) };
  if (o.op === "marker" && !o.id) {
    let x = id();
    while (d.markers?.some((m) => m.id === x)) x = id();
    return { ...o, id: x };
  }
  if (o.op === "insert") return { ...o, id: o.id || id() };
  if (o.op === "place") return { ...o, id: o.id || id(), gap_id: o.gap_id || id() };
  if (o.op === "add") {
    const ids: string[] = [];
    for (let k = 0; k < (o.take_ids ?? []).length; k++) {
      let x = id();
      while (ids.includes(x)) x = id();
      ids.push(x);
    }
    return { ...o, ids: o.ids?.length === o.take_ids?.length ? o.ids : ids };
  }
  return o;
}

/** "+12 f" / "−1.00 s": a delta in frames under a second, else seconds. */
export function signed(s: number, g: Grid): string {
  const f = g.f(s);
  const sign = f < 0 ? "−" : "+";
  return Math.abs(f) < g.f(1) ? `${sign}${Math.abs(f)} f` : `${sign}${Math.abs(s).toFixed(2)} s`;
}

/** The live region's words for an edit made on `doc` (§27.9: "Cut Shot 3 at 0:10.12"); `name` names a clip by id. */
export function describe(op: Op, doc: Norm, takes: Takes, name: (id: string) => string): string {
  const g = new Grid(doc.target);
  switch (op.op) {
    case "split": {
      const ti = doc.tracks.findIndex((t) => t.name === op.track);
      const c = doc.tracks[ti]?.clips.find((x) => {
        const [a, e] = span(x, ti, takes, g);
        return a <= op.at && op.at < e;
      });
      return `Cut ${c ? name(c.id) : op.track} at ${timecode(op.at)}`;
    }
    case "split_all":
      return `Cut every track at ${timecode(op.at)}`;
    case "trim":
      return `Trimmed ${name(op.clip)}'s ${op.edge === "in" ? "start" : "end"} ${signed(op.delta, g)}`;
    case "roll":
      return `Rolled the cut before ${name(op.clip)} ${signed(op.delta, g)}`;
    case "slip":
      return `Slipped ${name(op.clip)} ${signed(op.delta, g)}`;
    case "slide":
      return `Slid ${name(op.clip)} ${signed(op.delta, g)}`;
    case "move":
      return `Moved ${name(op.clip)}`;
    case "overwrite":
      return `Overwrote at ${timecode(op.at)} with ${name(op.clip)}`;
    case "remove":
      return `Removed ${name(op.clip)}`;
    case "lift":
      return `Lifted ${name(op.clip)}`;
    case "remove_range":
      return `Removed ${timecode(op.from)} – ${timecode(op.to)}`;
    case "lift_range":
      return `Lifted ${timecode(op.from)} – ${timecode(op.to)}`;
    case "marker":
      return `Marker at ${timecode(op.at)}`;
    case "remove_marker":
      return "Removed the marker";
    default:
      return "";
  }
}

/** The 409 notice's noun for an op ("your slide no longer fits"). */
export function noun(op: Op): string {
  const n: Partial<Record<Op["op"], string>> = {
    split: "cut",
    split_all: "cut",
    remove_range: "range delete",
    lift_range: "range lift",
    remove: "delete",
    remove_marker: "marker removal",
    set: "change",
    add_track: "new track",
    remove_track: "track removal",
    track: "track change",
  };
  return n[op.op] ?? op.op;
}

/** The zoom field's text (§27.4): the visible span, "22 s", "9.0 s", "10 f", or "Fit · 3:45" at fit. */
export function spanText(visible: number, fps: number, fit: number | null): string {
  if (fit !== null)
    return `Fit · ${Math.floor(fit / 60)}:${String(Math.floor(fit % 60)).padStart(2, "0")}`;
  if (visible < 1) return `${Math.max(1, Math.round(visible * fps))} f`;
  return visible < 10 ? `${visible.toFixed(1)} s` : `${Math.round(visible)} s`;
}

/** The zoom's bounds in px per second: fit (the sequence, at least 5 s, fills `width`) up to 64 px per frame. */
export function zoomBounds(width: number, duration: number, fps: number): [number, number] {
  const fit = width / Math.max(duration, 5);
  return [Math.min(5, fit), MAX_PX_PER_FRAME * fps];
}
