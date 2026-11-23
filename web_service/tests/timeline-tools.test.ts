// The Timeline tools' pure parts (plan 23 §27.11): the key map (no collisions; held vs tapped), Trim's grab zones,
// snapping with what it held on, the clamp at a bound, the repeat merge, the ids an op makes and the words said; the
// zoom (about the pointer, fit, the tick step, the virtual window), the shuttle and the detail's buckets and peaks.
import { describe, expect, it, vi } from "vitest";
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
  bucketFrame,
  cutText,
  rulerTicks,
  shuttle,
  tickStep,
  tileWidth,
  virtualWindow,
  wheelZoom,
  zoomAbout,
} from "@/lib/timeline-tools";
import { peakAt, peaksOf, pyramid } from "@/lib/timeline-detail";

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
    expect(keyOf({ ...ev("Minus", "s"), key: "?" })).toBe("sheet"); // "?" on a German layout (⇧ß)
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
  // fit: a sequence under 5 s fills the width as 5 s would; never past 64 px per frame
  expect(zoomBounds(800, 2, 24)[0]).toBe(160);
  expect(zoomBounds(1e6, 1, 24)).toEqual([64 * 24, 64 * 24]);
});

it("zooms about the pointer: the time under it moves under half a pixel", () => {
  const HEAD = 72;
  for (const [scroll, x, pps, next] of [
    [0, 300, 40, 50],
    [1234, 17, 40, 1536],
    [90000, 640, 1536, 3.2],
    [500, 72, 12.5, 15.625],
  ]) {
    const t = (scroll + x - HEAD) / pps;
    const s2 = zoomAbout(scroll, x, pps, next, HEAD);
    if (s2 > 0) expect(Math.abs((s2 + x - HEAD) / next - t) * next).toBeLessThan(0.5);
  }
  // at the start nothing scrolls before 0
  expect(zoomAbout(0, 100, 40, 20, 72)).toBe(0);
  // a wheel notch ×1.25 / ×0.8; a pinch's small deltas smoothly, in and out by the same steps
  expect(wheelZoom(-100)).toBe(1.25);
  expect(wheelZoom(3, 1)).toBe(0.8);
  expect(wheelZoom(-10) * wheelZoom(10)).toBeCloseTo(1, 9);
  expect(wheelZoom(-10)).toBeGreaterThan(1);
});

it("picks the ruler's step: frames with room for a number, then 1 s … 10 min (the board's rulers)", () => {
  expect(tickStep(56 * 24, 24)).toEqual({ frames: 1 }); // "Zoomed in to frames"
  expect(tickStep(500, 24)).toEqual({ frames: 2 });
  expect(tickStep(100, 24)).toEqual({ frames: 10 });
  expect(tickStep(94, 24)).toEqual({ seconds: 1 });
  expect(tickStep(60.5, 24)).toEqual({ seconds: 2 });
  expect(tickStep(39.5, 24)).toEqual({ seconds: 2 }); // the board's 2 s ruler at 79 px
  expect(tickStep(2.42, 24)).toEqual({ seconds: 30 }); // "Zoomed out to fit": 3:45 in 544 px
  expect(tickStep(0.01, 24)).toEqual({ seconds: 600 });
  // frame zoom: each frame's number in its second, the second itself "0:10"
  const f = rulerTicks(10, 10 + 9.5 / 24, 56 * 24, g);
  expect(f.map((k) => k.label)).toEqual(["0:10", "1", "2", "3", "4", "5", "6", "7", "8", "9"]);
  expect(f[0].major && !f[1].major).toBe(true);
  // seconds: a labelled major per step and 4 minors between
  const s = rulerTicks(0, 4, 41.9, g);
  expect(s.filter((k) => k.label).map((k) => k.label)).toEqual(["0:00", "0:02", "0:04"]);
  expect(s.filter((k) => !k.label).map((k) => k.t)).toEqual([
    0.4, 0.8, 1.2, 1.6, 2.4, 2.8, 3.2, 3.6,
  ]);
  expect(
    rulerTicks(0, 225, 2.42, g)
      .filter((k) => k.label)
      .at(-1)?.label,
  ).toBe("3:30");
  // the blade's tag names the frame in its second at frame zoom
  expect(cutText(10 + 7 / 24, 56 * 24, g, false)).toBe("0:10 · 7");
  expect(cutText(10.5, 40, g, false)).toBe("0:10.50 · frame 252");
  expect(cutText(10.5, 40, g, true)).toBe("0:10.50 · all tracks");
});

it("the virtual window: the screen and one each side, stepped by screens", () => {
  expect(virtualWindow(0, 1000)).toEqual([-1000, 2000]);
  expect(virtualWindow(999, 1000)).toEqual([-1000, 2000]);
  expect(virtualWindow(1000, 1000)).toEqual([0, 3000]);
  // what is on screen (track px = scroll − the name column … + the width) is always inside
  for (const scroll of [0, 72, 1500, 919_000]) {
    const [a, b] = virtualWindow(scroll, 800);
    expect(a <= scroll - 72 && scroll + 800 <= b).toBe(true);
  }
  expect(virtualWindow(500, 0)).toEqual([-Infinity, Infinity]); // not laid out: everything
});

it("the shuttle: L again ×2, ×4, ×8, J the same backward, K stops", () => {
  let r = 0;
  const seq = (["fwd", "fwd", "fwd", "fwd", "fwd", "back", "back", "stop"] as const).map(
    (k) => (r = shuttle(r, k)),
  );
  expect(seq).toEqual([1, 2, 4, 8, 8, -1, -2, 0]);
});

it("filmstrip tiles and peaks", () => {
  expect(tileWidth(40, 24)).toBe(44);
  expect(tileWidth(56 * 24, 24)).toBe(56); // one tile per frame
  // the bucket: a power-of-two frame step at least the tile's span; frame zoom: the frame itself
  expect(bucketFrame(10 + 7 / 24, 1 / 24, 24)).toBe(247);
  expect(bucketFrame(1, 1.1, 24) % 32).toBe(0);
  expect(bucketFrame(1.02, 1.1, 24)).toBe(bucketFrame(1, 1.1, 24));
  const sine = new Float32Array(8000).map((_, i) => (i < 4000 ? 0.1 : 0.9) * Math.sin(i));
  const p = pyramid([sine]);
  expect(p[0].length).toBe(125);
  expect(p.at(-1)!.length).toBe(1);
  expect(peakAt(p, 0, 0.4)).toBeLessThanOrEqual(0.1);
  expect(peakAt(p, 0.6, 0.9)).toBeGreaterThan(0.8);
  expect(peakAt(p, 0, 1)).toBeCloseTo(p.at(-1)![0], 6);
});

it("decodes at most 2 takes' peaks at once, however many strips ask in one render (§27.8)", async () => {
  const fetch = vi.fn(() => new Promise<Response>(() => {})); // never answers: the decoders stay busy
  vi.stubGlobal("fetch", fetch);
  vi.stubGlobal("OfflineAudioContext", class {});
  try {
    for (let i = 0; i < 6; i++) expect(peaksOf(`cap-${i}`, `/m/${i}.wav`, 10)).toBeNull();
    await new Promise((r) => setTimeout(r, 20));
    expect(fetch).toHaveBeenCalledTimes(2);
  } finally {
    vi.unstubAllGlobals();
  }
});
