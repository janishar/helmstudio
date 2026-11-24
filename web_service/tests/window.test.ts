import { describe, expect, it } from "vitest";
import { windowOf } from "@/components/gallery/window";

describe("take grid windowing (alpha.2 item 32)", () => {
  it("draws the rows near the screen and pads for the rest", () => {
    // 1000 takes, 5 columns, rows 200 px apart, the screen at rows 50 to 54
    const w = windowOf(1000, 5, 200, 10_000, 10_900);
    expect(w).toEqual({ start: 230, end: 295, padTop: 46 * 200, padBottom: (200 - 59) * 200 });
    // drawn rows plus padding keep the grid's full height
    expect(w.padTop + ((w.end - w.start) / 5) * 200 + w.padBottom).toBe(200 * 200);
  });
  it("draws everything until measured, and nothing (height kept) when off the screen", () => {
    expect(windowOf(60, 0, 0, 0, 900)).toEqual({ start: 0, end: 60, padTop: 0, padBottom: 0 });
    const off = windowOf(60, 4, 100, 5000, 6000);
    expect(off.end - off.start).toBe(0);
    expect(off.padTop + off.padBottom).toBe(1500);
  });
});
