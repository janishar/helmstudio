// The Timeline tools' pure parts (plan 23 §27.11): the key map (no collisions; held vs tapped), Trim's grab zones,
// snapping with what it held on, the clamp at a bound, the repeat merge, the ids an op makes and the words said.
import { describe, expect, it } from "vitest";
import { Grid, type Norm } from "@/lib/timeline";
import {
  HOLD_MS,
  KEYMAP,
  clampFrames,
  describe as said,
  keyOf,
  merge,
  noun,
  snapTo,
  spanText,
  sticky,
  trimZone,
  withIds,
  zoomBounds,
} from "@/lib/timeline-tools";

const g = new Grid({ fps: 24, sample_rate: 48000 });
const ev = (code: string, m = "") => ({
  code,
  shiftKey: m.includes("s"),
  altKey: m.includes("a"),
  metaKey: m.includes("m"),
  ctrlKey: m.includes("c"),
});

describe("the key map", () => {
  it("has no two actions on one key", () => {
    const seen = new Map<string, string>();
    for (const k of KEYMAP) {
      const key = `${k.code}:${!!k.shift}:${!!k.alt}:${!!k.mod}`;
      expect(seen.get(key) ?? k.id, key).toBe(k.id);
      seen.set(key, k.id);
    }
  });
  it("matches on e.code with the exact modifiers", () => {
    expect(keyOf(ev("KeyS"))).toBe("split");
    expect(keyOf(ev("KeyS", "s"))).toBe("scrub");
    expect(keyOf(ev("KeyB"))).toBe("tool:blade");
    expect(keyOf(ev("KeyB", "m"))).toBe("blade");
    expect(keyOf(ev("KeyB", "c"))).toBe("blade"); // Ctrl off a Mac
    expect(keyOf(ev("KeyB", "sm"))).toBe("blade_all");
    expect(keyOf(ev("BracketLeft", "a"))).toBe("trim_in_here"); // ⌥[ types “ on a Mac: e.code holds
    expect(keyOf(ev("Comma", "s"))).toBe("slide_back");
    expect(keyOf(ev("Comma", "a"))).toBe("roll_back");
    expect(keyOf(ev("Period"))).toBe("slip_fwd");
    expect(keyOf(ev("Slash", "s"))).toBe("sheet");
    expect(keyOf(ev("Delete"))).toBe("delete");
    expect(keyOf(ev("Backspace", "s"))).toBe("lift");
    expect(keyOf(ev("KeyZ", "m"))).toBe("undo");
    expect(keyOf(ev("KeyZ", "sm"))).toBe("redo");
    expect(keyOf(ev("KeyZ", "s"))).toBe("fit");
    expect(keyOf(ev("KeyN"))).toBe("snap");
    expect(keyOf(ev("KeyM"))).toBe("marker");
    expect(keyOf(ev("KeyA", "a"))).toBeNull();
    expect(keyOf(ev("KeyQ"))).toBeNull();
  });
  it("keeps a tapped tool and drops a held one on release", () => {
    expect(sticky(1000, 1000 + HOLD_MS)).toBe(true);
    expect(sticky(1000, 1000 + HOLD_MS + 1)).toBe(false);
  });
});

it("Trim's grab zones: the cut rolls, an edge ripples, the body slips", () => {
  expect(trimZone(2, 200, true, true)).toBe("roll");
  expect(trimZone(2, 200, false, true)).toBe("in"); // the first clip: no cut before it
  expect(trimZone(7, 200, true, true)).toBe("in");
  expect(trimZone(197, 200, true, true)).toBe("roll_next");
  expect(trimZone(193, 200, true, true)).toBe("out");
  expect(trimZone(100, 200, true, true)).toBe("body");
  expect(trimZone(10, 24, true, true)).toBe("body"); // a narrow clip keeps a body
});

it("snaps to the nearest point within 6 px and names it; off: the frame only", () => {
  const pts = [
    { t: 2, kind: "cut" as const },
    { t: 3, kind: "cut" as const },
    { t: 3, kind: "playhead" as const },
    { t: 5, kind: "marker" as const },
  ];
  expect(snapTo(3.1, pts, 40, g, true)).toEqual({ t: 3, to: { t: 3, kind: "playhead" } });
  expect(snapTo(4.9, pts, 40, g, true).to?.kind).toBe("marker");
  expect(snapTo(4, pts, 40, g, true)).toEqual({ t: 4, to: null });
  expect(snapTo(3.1, pts, 40, g, false)).toEqual({ t: g.snap(3.1), to: null });
});

it("clamps a drag at the last frame the op accepts", () => {
  const ok = (k: number) => k <= 7 && k >= -3;
  expect(clampFrames(5, ok)).toEqual({ df: 5, clamped: false });
  expect(clampFrames(40, ok)).toEqual({ df: 7, clamped: true });
  expect(clampFrames(-40, ok)).toEqual({ df: -3, clamped: true });
  expect(clampFrames(9, () => false)).toEqual({ df: 0, clamped: true });
});

it("merges key repeats of the same edit into one op", () => {
  const f = g.s(1);
  const a = { op: "trim" as const, clip: "a", edge: "in" as const, delta: f };
  expect(merge(merge(a, a, g), a, g)).toEqual({ ...a, delta: g.s(3) });
  expect(merge(a, { ...a, edge: "out" }, g)).toBeNull();
  expect(merge(a, { ...a, clip: "b" }, g)).toBeNull();
  const s = { op: "slip" as const, clip: "a", delta: f };
  expect(merge(s, { ...s, delta: -f }, g)).toEqual({ ...s, delta: 0 });
  expect(merge(s, { op: "slide", clip: "a", delta: f }, g)).toBeNull();
  expect(merge(null, s, g)).toBeNull();
});

const doc: Norm = {
  target: { width: 256, height: 128, fps: 24, sample_rate: 48000 },
  tracks: [
    {
      name: "V1",
      kind: "video",
      clips: [{ id: "a", take_id: null, hold: 4, at: 0, number: 3 }],
    },
    { name: "A2", kind: "audio", clips: [] },
  ],
  duration_s: 4,
  frames: 96,
};

it("fills the ids an op makes, unique and kept once there", () => {
  const o = withIds({ op: "split_all", at: 1 }, doc) as { ids: string[] };
  expect(o.ids).toHaveLength(2 * 2 + 3);
  expect(new Set(o.ids).size).toBe(o.ids.length);
  expect(withIds({ op: "split_all", at: 1, ids: ["x"] }, doc)).toEqual({
    op: "split_all",
    at: 1,
    ids: ["x"],
  });
  expect((withIds({ op: "split", track: "V1", at: 1, id: "" }, doc) as { id: string }).id).toMatch(
    /^[a-z0-9]{1,16}$/,
  );
  expect((withIds({ op: "marker", id: "", at: 1 }, doc) as { id: string }).id).not.toBe("");
});

it("says each edit, and names a refused re-apply's op", () => {
  const name = (id: string) => (id === "a" ? "Shot 3" : id);
  expect(said({ op: "split", track: "V1", at: 2.5, id: "n" }, doc, {}, name)).toBe(
    "Cut Shot 3 at 0:02.50",
  );
  expect(said({ op: "split_all", at: 1 }, doc, {}, name)).toBe("Cut every track at 0:01.00");
  expect(said({ op: "slip", clip: "a", delta: g.s(12) }, doc, {}, name)).toBe(
    "Slipped Shot 3 +12 f",
  );
  expect(said({ op: "trim", clip: "a", edge: "out", delta: -1 }, doc, {}, name)).toBe(
    "Trimmed Shot 3's end −1.00 s",
  );
  expect(noun({ op: "slide", clip: "a", delta: 1 })).toBe("slide");
  expect(noun({ op: "split_all", at: 1 })).toBe("cut");
});

it("the zoom field's text and bounds", () => {
  expect(spanText(22.4, 24, null)).toBe("22 s");
  expect(spanText(9, 24, null)).toBe("9.0 s");
  expect(spanText(10 / 24, 24, null)).toBe("10 f");
  expect(spanText(225, 24, 225)).toBe("Fit · 3:45");
  expect(zoomBounds(800, 225, 24)).toEqual([800 / 225, 64 * 24]);
  expect(zoomBounds(800, 2, 24)[0]).toBe(5); // fit never above the old 5 px/s floor
});
