import { describe, expect, it } from "vitest";
import { sliceFrom, windowOf } from "@/components/gallery/window";

describe("take grid windowing (alpha.2 item 32)", () => {
  it("draws the rows near the screen and pads for the rest", () => {
    // 1000 takes, 5 columns, rows 200 px apart, the screen at rows 50 to 54
    const w = windowOf(1000, 5, 200, 10_000, 10_900);
    expect(w).toEqual({ start: 230, end: 295, padTop: 46 * 200, padBottom: (200 - 59) * 200 });
    // drawn rows plus padding keep the grid's full height
    expect(w.padTop + ((w.end - w.start) / 5) * 200 + w.padBottom).toBe(200 * 200);
  });
  it("slices the takes after the uploads: none when the window ends among the uploads", () => {
    // 40 uploads, 200 takes, 4 columns, the window on the first rows only
    const w = windowOf(240, 4, 200, 0, 900);
    expect(w.end).toBeLessThan(40);
    expect(sliceFrom(w, 40)).toEqual([0, 0]);
    expect(Array.from({ length: 200 }).slice(...sliceFrom(w, 40))).toHaveLength(0);
    // further down: the takes from the window's start
    expect(sliceFrom(windowOf(240, 4, 200, 4000, 4900), 40)).toEqual([24, 76]);
  });
  it("draws everything until measured, and nothing (height kept) when off the screen", () => {
    expect(windowOf(60, 0, 0, 0, 900)).toEqual({ start: 0, end: 60, padTop: 0, padBottom: 0 });
    const off = windowOf(60, 4, 100, 5000, 6000);
    expect(off.end - off.start).toBe(0);
    expect(off.padTop + off.padBottom).toBe(1500);
  });
});
