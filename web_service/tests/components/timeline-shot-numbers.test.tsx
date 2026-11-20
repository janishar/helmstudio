// @vitest-environment jsdom
// Shot numbers (API 1.29; docs/mock/TakeActions.dc.html): a V1 clip's lane and inspector title is "Shot 4 · <take
// title>", its own sound "Shot 4 sound"; a clip without one keeps its title.
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Lanes, clipText, shotName } from "@/components/timeline/lanes";
import { Grid, factsOf, type Norm } from "@/lib/timeline";
import type { S } from "@/lib/api";

afterEach(cleanup);

const A = "00000000-0000-4000-8000-0000000000a1";
const B = "00000000-0000-4000-8000-0000000000b1";
const take = (id: string, title: string): S<"TimelineTake"> => ({
  id,
  kind: "video",
  source: "create",
  title,
  url: "/files/x",
  thumb_url: "/files/x",
  width: 256,
  height: 128,
  video: { duration_s: 2, fps: 24, frames: 48, has_audio: true, poster_url: "/files/x" },
  seed: 42,
  workflow_id: null,
  workflow_title: "LTX Text to video",
});
const takes = { [A]: take(A, "birches"), [B]: take(B, "tree line") };
const doc: Norm = {
  target: { width: 256, height: 128, fps: 24, sample_rate: 48000 },
  tracks: [
    {
      name: "V1",
      kind: "video",
      clips: [
        { id: "a", take_id: A, in: 0, out: 2, at: 0, number: 4, alternatives: [A] },
        { id: "b", take_id: B, in: 0, out: 2, at: 2, alternatives: [B] },
      ],
    },
  ],
  duration_s: 4,
  frames: 96,
};

it("names a numbered clip Shot n · its take, in the lanes and their sound", () => {
  const g = new Grid(doc.target);
  const [a, b] = doc.tracks[0].clips;
  expect(clipText(a, false, takes, factsOf(takes), g).title).toBe("Shot 4 · birches");
  expect(clipText(b, false, takes, factsOf(takes), g).title).toBe("tree line");
  expect(shotName(a)).toBe("Shot 4");
  expect(shotName(b, "tree line")).toBe("tree line");
  const noop = vi.fn();
  render(
    <Lanes
      doc={doc}
      takes={takes}
      facts={factsOf(takes)}
      pps={100}
      frame={0}
      sel={[]}
      track={null}
      progress={{}}
      onSelect={noop}
      onTrack={noop}
      onSeek={noop}
      onPreview={noop}
      onCommit={noop}
      onZoom={noop}
    />,
  );
  expect(screen.getByText("Shot 4 · birches")).toBeTruthy();
  expect(screen.getByText(/^Shot 4 sound · /)).toBeTruthy();
  expect(screen.getAllByText(/^clip audio · /)).toHaveLength(1);
});
