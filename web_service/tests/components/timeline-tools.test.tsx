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
