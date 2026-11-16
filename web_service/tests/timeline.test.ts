// The timeline rules against the shared fixtures (contracts/fixtures/timelines): what check.py and studio compute.
import { describe, expect, it } from "vitest";
import rates from "../../contracts/fixtures/timelines/rates.json";
import normalizeFx from "../../contracts/fixtures/timelines/normalize.json";
import editsFx from "../../contracts/fixtures/timelines/edits.json";
import snapFx from "../../contracts/fixtures/timelines/snap.json";
import frameFx from "../../contracts/fixtures/timelines/frame-at.json";
import {
  Grid,
  apply,
  clipAt,
  cuts,
  dropIndex,
  frameAt,
  newId,
  normalize,
  rnd,
  snapPoint,
  timecode,
  writable,
  type Doc,
  type Op,
  type Takes,
} from "@/lib/timeline";

type Fx = { takes?: Takes; cases: Record<string, unknown>[] };
const takesOf = (fx: Fx, c: Record<string, unknown>) => ({
  ...(fx.takes ?? {}),
  ...((c.takes as Takes) ?? {}),
});

describe("rates", () => {
  for (const c of rates.cases)
    it(`${c.fps} fps, ${c.seconds} s`, () => {
      const g = new Grid({ fps: c.fps, sample_rate: 48000 });
      expect({ frames: g.f(c.seconds), snapped: g.snap(c.seconds) }).toEqual({
        frames: c.frames,
        snapped: c.snapped,
      });
    });
  it("rounds halves away from zero", () => {
    expect([rnd(0.5), rnd(-0.5), rnd(1.5), rnd(-1.5), rnd(-0.4)]).toEqual([1, -1, 2, -2, 0]);
  });
});

const run = (name: string, fx: Fx, f: (c: Record<string, unknown>, takes: Takes) => unknown) =>
  describe(name, () => {
    for (const c of fx.cases)
      it(String(c.description), () => expect(f(c, takesOf(fx, c))).toEqual(c.expect));
  });

run("normalize", normalizeFx as unknown as Fx, (c, takes) =>
  normalize(c.document as Doc, takes, c.previous as Doc | undefined),
);
run("edits", editsFx as unknown as Fx, (c, takes) => apply(c.document as Doc, c.op as Op, takes));
run("snap", snapFx as unknown as Fx, (c, takes) => {
  const doc = c.document as Doc;
  return "t" in c
    ? {
        to: snapPoint(
          c.t as number,
          cuts(doc, takes, c.exclude as string | undefined),
          c.px_per_s as number,
          new Grid(doc.target),
        ),
      }
    : { index: dropIndex(doc, c.clip as string, c.mid as number, takes) };
});
run("frame-at", frameFx as unknown as Fx, (c, takes) =>
  frameAt(c.document as Doc, c.frame as number, takes),
);

describe("editor helpers", () => {
  const doc = (editsFx as unknown as Fx).cases[0].document as Doc;
  const takes = (editsFx as unknown as Fx).takes!;
  it("a written document normalizes to itself", () => {
    const r = normalize(writable(doc) as Doc, takes, doc);
    expect("document" in r && r.document.tracks).toEqual(doc.tracks);
  });
  it("finds the V1 clip under a frame", () => {
    expect([clipAt(doc, 0, takes), clipAt(doc, 49, takes), clipAt(doc, 10_000, takes)]).toEqual([
      0, 1, -1,
    ]);
  });
  it("makes unused ids", () => {
    expect(newId(doc)).toMatch(/^[a-z0-9]{1,16}$/);
  });
  it("writes timecode", () => {
    expect([timecode(0), timecode(5.12), timecode(61.005)]).toEqual([
      "0:00.00",
      "0:05.12",
      "1:01.01",
    ]);
  });
});
