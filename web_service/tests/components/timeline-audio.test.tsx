// @vitest-environment jsdom
// Audio takes on the Timeline (API 1.32, docs/mock/TimelineAudio.dc.html): a dialogue's clip on A2 with its title and
// length, a cue tick per line inside its in..out (a trimmed-out one not drawn) named by the speaker, the ⋯ menu, and
// a New line's placeholder on A3 (running, then failed with its error).
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Lanes, cueTicks } from "@/components/timeline/lanes";
import { factsOf, type Norm } from "@/lib/timeline";
import type { S } from "@/lib/api";

afterEach(cleanup);

const D = "00000000-0000-4000-8000-0000000000d1";
const cue = (line: number, s: number, e: number, voice_id: string) => ({
  line,
  start_s: s,
  end_s: e,
  voice_id,
  text: `line ${line}`,
});
const takes: Record<string, S<"TimelineTake">> = {
  [D]: {
    id: D,
    kind: "audio",
    source: "create",
    title: "Dialogue · harbour scene",
    url: "/files/d.wav",
    thumb_url: "/files/d.wav",
    width: null,
    height: null,
    audio: {
      duration_s: 9,
      sample_rate: 24000,
      cues: [
        cue(1, 0.2, 2.7, "echo/ada"),
        cue(2, 3.0, 5.4, "echo/bo"),
        cue(3, 8.5, 8.9, "echo/ada"),
      ],
    },
    seed: 7,
    workflow_id: "echo-dialogue",
    workflow_title: "Echo dialogue",
  },
};
const doc = (shot: S<"TimelineClip">["shot"]): Norm => ({
  target: { width: 256, height: 128, fps: 24, sample_rate: 48000 },
  tracks: [
    { name: "V1", kind: "video", clips: [{ id: "g", take_id: null, hold: 20, at: 0 }] },
    {
      name: "A2",
      kind: "audio",
      clips: [{ id: "d", take_id: D, in: 0, out: 8, at: 5, alternatives: [D] }],
    },
    { name: "A3", kind: "audio", clips: [{ id: "p", take_id: null, at: 15, hold: 3, shot }] },
  ],
  duration_s: 20,
  frames: 480,
});

it("draws a dialogue's clip with its cues and menu, and a line's placeholder", () => {
  const d = doc({ run_id: "r1", status: "running" }).tracks[1].clips[0];
  expect(cueTicks(d, takes[D]).map((t) => t.line)).toEqual([1, 2]);
  const noop = vi.fn();
  const props = {
    takes,
    facts: factsOf(takes),
    pps: 40,
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
    onMenu: noop,
    nameOf: (v: string) => (v === "echo/ada" ? "Mara" : "Jon"),
  };
  const { rerender } = render(<Lanes doc={doc({ run_id: "r1", status: "running" })} {...props} />);
  expect(screen.getByText("Dialogue · harbour scene · 0:08")).toBeTruthy();
  expect(screen.getByText("1 Mara")).toBeTruthy();
  expect(screen.getByText("2 Jon")).toBeTruthy();
  expect(screen.queryByText("3 Mara")).toBeNull();
  expect(screen.getByRole("button", { name: "Clip menu" })).toBeTruthy();
  expect(screen.getByText("Speaking line")).toBeTruthy();
  rerender(
    <Lanes
      doc={doc({ run_id: "r1", status: "failed", error: "speak.voice: a voice is required" })}
      {...props}
    />,
  );
  expect(screen.getByText("New line failed")).toBeTruthy();
  expect(screen.getByText("speak.voice: a voice is required")).toBeTruthy();
});
