// Magic's standard request in the web (plan 39 phase D): the board's takes made from studio's answer (done takes,
// running and failed runs as placeholders, out of date when studio says so or the request changes here), the
// statuses they give, the pack's estimate and a run history line.
import { describe, expect, it } from "vitest";
import { statusOf, type Doc } from "@/lib/magic";
import { emptyDoc } from "@/lib/magic-fixture";
import {
  modelOf,
  packSeconds,
  runLine,
  takesOf,
  FAKE_KINDS,
  type BlockRun,
} from "@/lib/magic-standard";

const T = "11111111-1111-4111-8111-111111111111";
const run = (o: Partial<BlockRun>): BlockRun => ({
  block: "b1",
  run_id: "r1",
  status: "succeeded",
  takes: [],
  seed: 7,
  current: true,
  by: "user",
  created_at: "2026-11-27T05:00:00Z",
  render_s: 112,
  ...o,
});
function doc(takes: string[]): Doc {
  const d = emptyDoc();
  d.lanes = [{ id: "l1", name: "Shots", role: "shots" }];
  d.blocks = [
    {
      id: "b1",
      lane: "l1",
      at: 0,
      length: 96,
      kind: "video",
      name: "Shot 1",
      request: { kind: "video", prompt: "rain" },
      takes,
      effects: [],
    },
  ];
  return d;
}

describe("the board's takes from studio", () => {
  it("running, failed, done, then out of date", () => {
    let d = doc([]);
    let t = takesOf(d, [run({ status: "running", progress: 62 })], {});
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "running", progress: 62 });
    t = takesOf(d, [run({ status: "failed", error: "out of memory" })], {});
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "failed", error: "out of memory" });
    d = doc([T]);
    const wire = { [T]: { id: T, kind: "video" as const, url: "u", thumb_url: "th", seed: 7 } };
    t = takesOf(d, [run({ takes: [T] })], wire);
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "done" });
    expect(t[T].art).toBe('url("th")');
    // the request changed here: out of date at once; studio saying so: the same
    const edited = {
      ...d,
      blocks: [{ ...d.blocks[0], request: { kind: "video" as const, prompt: "more" } }],
    };
    expect(statusOf(edited, edited.blocks[0], t)).toEqual({ s: "old" });
    t = takesOf(d, [run({ takes: [T], current: false })], wire);
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "old" });
  });

  it("the pack's estimate, the default model and a run history line", () => {
    const d = doc([]);
    const m = modelOf(FAKE_KINDS, { kind: "video" });
    expect(m?.name).toBe("LTX 2.5");
    expect(packSeconds(m, d.blocks[0], d)).toBe(56); // 14 s a second × 4 s at 720p
    expect(packSeconds(m, { ...d.blocks[0], request: { kind: "video", count: 2 } }, d)).toBe(112);
    expect(runLine(run({}), 2).title).toBe("Run 2");
    expect(runLine(run({}), 2).sub).toMatch(/^seed 7 · 1:52 · /);
  });
});
