// @vitest-environment jsdom
// The tool row (docs/mock/TimelineTools.dc.html, option B) and the tools on the lanes (plan 23 §27.2, §27.9): a radio
// group with one tab stop that arrows move through, the toggles and the zoom field; Blade cuts the clip under the
// pointer at its frame (⇧: every track) and draws its cut line with the timecode; markers sit on the ruler.
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Lanes } from "@/components/timeline/lanes";
import { ToolRow } from "@/components/timeline/tools";
import type { Norm } from "@/lib/timeline";
import type { Tool } from "@/lib/timeline-tools";

afterEach(cleanup);

it("the tool row: five radios, one tab stop, arrows move; toggles and zoom", () => {
  const onTool = vi.fn();
  const fns = {
    onSnap: vi.fn(),
    onScrub: vi.fn(),
    onZoomOut: vi.fn(),
    onZoomIn: vi.fn(),
    onFit: vi.fn(),
    onSheet: vi.fn(),
  };
  render(<ToolRow tool="blade" onTool={onTool} snap scrub={false} zoom="22 s" {...fns} />);
  const radios = screen.getAllByRole("radio");
  expect(radios.map((r) => r.getAttribute("aria-label"))).toEqual([
    "Select",
    "Trim",
    "Blade",
    "Range",
    "Overwrite",
  ]);
  expect(radios.map((r) => r.tabIndex)).toEqual([-1, -1, 0, -1, -1]);
  expect(screen.getByRole("radio", { name: "Blade" }).getAttribute("aria-keyshortcuts")).toBe("B");
  fireEvent.keyDown(radios[2], { key: "ArrowRight" });
  expect(onTool).toHaveBeenLastCalledWith("range");
  fireEvent.keyDown(radios[2], { key: "ArrowLeft" });
  expect(onTool).toHaveBeenLastCalledWith("trim");
  expect(screen.getByRole("button", { name: "Snapping" }).getAttribute("aria-pressed")).toBe(
    "true",
  );
  expect(screen.getByRole("button", { name: "Hover scrub" }).getAttribute("aria-pressed")).toBe(
    "false",
  );
  expect(screen.getByText("22 s")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Zoom to fit" }));
  fireEvent.click(screen.getByRole("button", { name: "Keyboard shortcuts" }));
  expect(fns.onFit).toHaveBeenCalled();
  expect(fns.onSheet).toHaveBeenCalled();
});

const doc: Norm = {
  target: { width: 256, height: 128, fps: 24, sample_rate: 48000 },
  tracks: [
    {
      name: "V1",
      kind: "video",
      clips: [{ id: "a", take_id: null, hold: 4, at: 0 }],
    },
    {
      name: "A2",
      kind: "audio",
      clips: [{ id: "l", take_id: null, hold: 4, at: 0, link: "a" }],
    },
  ],
  markers: [{ id: "m1", at: 3, name: "Beat", color: "red" }],
  duration_s: 4,
  frames: 96,
};
const lanes = (tool: Tool, onCommit = vi.fn()) => {
  const noop = vi.fn();
  render(
    <Lanes
      doc={doc}
      takes={{}}
      facts={{}}
      pps={40}
      frame={0}
      sel={[]}
      track={null}
      progress={{}}
      onSelect={noop}
      onTrack={noop}
      onSeek={noop}
      onPreview={noop}
      onCommit={onCommit}
      onZoom={noop}
      tool={tool}
    />,
  );
  return onCommit;
};

it("Blade cuts the clip under the pointer at its frame; ⇧ every track", () => {
  const onCommit = lanes("blade");
  const v1 = screen.getAllByRole("button", { name: /^Empty/ })[0];
  // the lanes start at x 0 here (jsdom): the name column is 72 px, 40 px a second
  fireEvent.pointerMove(v1, { clientX: 72 + 60 });
  expect(screen.getByText("Cut 0:01.50 · frame 36")).toBeTruthy();
  fireEvent.click(v1, { clientX: 72 + 60 });
  expect(onCommit).toHaveBeenLastCalledWith({ op: "split", track: "V1", at: 1.5, id: "" });
  fireEvent.click(v1, { clientX: 72 + 60, shiftKey: true });
  expect(onCommit).toHaveBeenLastCalledWith({ op: "split_all", at: 1.5 });
});

it("draws a marker on the ruler and a linked sound clip's link", () => {
  lanes("select");
  expect(screen.getByRole("button", { name: "Marker Beat at 0:03.00" })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^Empty, .*, linked$/ })).toBeTruthy();
});

it("drags a marker's flag along the ruler to move it; a click only selects it", () => {
  const onCommit = lanes("select");
  const flag = screen.getByRole("button", { name: "Marker Beat at 0:03.00" });
  fireEvent.pointerDown(flag, { clientX: 72 + 120, buttons: 1 });
  fireEvent.pointerMove(flag, { clientX: 72 + 50, buttons: 1 });
  fireEvent.pointerUp(flag, { clientX: 72 + 50 });
  fireEvent.click(flag);
  expect(onCommit).toHaveBeenCalledTimes(1);
  expect(onCommit).toHaveBeenLastCalledWith({
    op: "marker",
    id: "m1",
    at: 1.25,
    name: "Beat",
    color: "red",
  });
  fireEvent.pointerDown(flag, { clientX: 72 + 120, buttons: 1 });
  fireEvent.pointerUp(flag, { clientX: 72 + 120 });
  fireEvent.click(flag);
  expect(onCommit).toHaveBeenCalledTimes(1);
});

it("keeps the lane names in view when the lanes scroll (board: Zoomed in to frames)", () => {
  lanes("select");
  expect(screen.getByText("V1").className).toContain("sticky");
  expect(screen.getByRole("button", { name: /^Track A2/ }).parentElement!.className).toContain(
    "sticky",
  );
});

it("virtualises the lanes and zooms about the pointer (plan 23 §27.4, §27.8)", () => {
  // laid out: an 800 px track; jsdom has no layout, so the width and the observer are stubbed
  const width = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "clientWidth");
  Object.defineProperty(HTMLElement.prototype, "clientWidth", {
    configurable: true,
    get: () => 872,
  });
  const RO = globalThis.ResizeObserver;
  globalThis.ResizeObserver = class {
    constructor(private f: () => void) {}
    observe() {
      this.f();
    }
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  try {
    // 200 slots of 2 s at 40 px a second: 16 000 px of V1
    const long: Norm = {
      ...doc,
      tracks: [
        {
          name: "V1",
          kind: "video",
          clips: Array.from({ length: 200 }, (_, i) => ({
            id: `c${i}`,
            take_id: null,
            hold: 2,
            at: 2 * i,
          })),
        },
      ],
      markers: [],
      duration_s: 400,
      frames: 9600,
    };
    const noop = vi.fn();
    const props = {
      doc: long,
      takes: {},
      facts: {},
      frame: 0,
      sel: [],
      track: null,
      progress: {},
      onSelect: noop,
      onTrack: noop,
      onSeek: noop,
      onPreview: noop,
      onCommit: noop,
      onZoom: noop,
    };
    const { container, rerender } = render(<Lanes {...props} pps={40} />);
    // the window is the screen and one each side: [−800, 1600] px, 20 clips of 80 px and the one at its edge, not 200
    expect(container.querySelectorAll("[data-clip]").length).toBe(21);
    const scroller = container.querySelector(".overflow-x-auto") as HTMLDivElement;
    scroller.scrollLeft = 4000;
    fireEvent.scroll(scroller);
    expect(container.querySelector('[data-clip="c50"]')).toBeTruthy();
    expect(container.querySelector('[data-clip="c0"]')).toBeNull();
    // a zoom keeps the centre still when the playhead is off screen: t under x = 436 stays there
    const t = (4000 + 436 - 72) / 40;
    rerender(<Lanes {...props} pps={80} />);
    expect(Math.abs((scroller.scrollLeft + 436 - 72) / 80 - t) * 80).toBeLessThan(0.5);
    // a zoom out at the end: the lanes shrink, and a browser clamps scrollLeft to the new width before the zoom
    // reads it; the time under the pointer still stays put
    const content = scroller.firstElementChild as HTMLDivElement;
    let raw = 0;
    Object.defineProperty(scroller, "scrollLeft", {
      configurable: true,
      get: () => Math.max(0, Math.min(raw, parseFloat(content.style.width) - 872)),
      set: (v: number) => (raw = v),
    });
    rerender(<Lanes {...props} pps={40} />);
    scroller.scrollLeft = 1e9;
    fireEvent.scroll(scroller);
    const end = scroller.scrollLeft;
    const at = (end + 800 - 72) / 40;
    fireEvent.wheel(scroller, { ctrlKey: true, deltaY: 100, clientX: 800 });
    rerender(<Lanes {...props} pps={32} />);
    expect(Math.abs((scroller.scrollLeft + 800 - 72) / 32 - at) * 32).toBeLessThan(0.5);
  } finally {
    if (width) Object.defineProperty(HTMLElement.prototype, "clientWidth", width);
    globalThis.ResizeObserver = RO;
  }
});
