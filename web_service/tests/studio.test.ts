import { describe, expect, it, vi } from "vitest";
import nodesFixture from "../../contracts/fixtures/registry/nodes.valid.json";
import workflowsFixture from "../../contracts/fixtures/registry/workflows.valid.json";
import qwenNodes from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import h3Nodes from "../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import type { S } from "@/lib/api";
import {
  applyEvent,
  etaText,
  followSession,
  fraction,
  lastDuration,
  pinnedNote,
  mergeRuns,
  seedOf,
  segments,
  track,
  type RunTrack,
} from "@/lib/runs";
import {
  ASPECTS,
  megapixelChoices,
  aspectOf,
  buildInputs,
  display,
  evaluate,
  fields,
  graphOrder,
  megapixelsOf,
  pretty,
  prettyModel,
  queueInputs,
  readValue,
  sections,
  sizeFor,
  visible,
} from "@/lib/workflow";

/** The echo workflow as GET /v1/studio/workflows/echo serves it: the registry's, with its node schemas. */
const echo = {
  ...workflowsFixture.workflows[0],
  node_schemas: Object.fromEntries(nodesFixture.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;

/** The qwen-image workflow, likewise. */
const qwen = {
  ...qwenWorkflows.workflows[0],
  node_schemas: Object.fromEntries(qwenNodes.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;

const run = (over: Partial<S<"Run">> = {}): S<"Run"> => ({
  id: "r1",
  session_id: "s1",
  workflow_id: "echo",
  inputs: {},
  status: "queued",
  progress: null,
  error: null,
  outputs: [],
  host: "fake",
  lane: "interactive",
  attempt: 1,
  created_at: "2026-11-14T10:00:00Z",
  started_at: null,
  finished_at: null,
  ...over,
});
const base = { run_id: "r1", ts: "2026-11-14T10:00:01Z" };
const status = (
  seq: number,
  s: S<"RunStatus">,
  error: string | null = null,
): S<"SessionEvent"> => ({
  ...base,
  seq,
  type: "status",
  data: { status: s, error, host: "fake" },
});
const progress = (seq: number, step: number): S<"SessionEvent"> => ({
  ...base,
  seq,
  type: "progress",
  data: { node: "echo", step, of: 10 },
});
const output: S<"SessionEvent"> = {
  ...base,
  seq: 9,
  type: "output",
  data: {
    index: 0,
    key: "u/o/runs/r1/0.png",
    kind: "image",
    url: "/files/u/o/runs/r1/0.png?exp=1&sig=2",
    width: 512,
    height: 512,
  },
};

describe("the form derived from the echo workflow", () => {
  it("has the prompt text, then steps, delay, fail and refs, keyed node.param, starting from the workflow's values", () => {
    expect(fields(echo).map((f) => [f.key, f.param.type, f.initial])).toEqual([
      ["prompt.text", "text", ""],
      ["echo.steps", "int", 10],
      ["echo.delay_ms", "int", 300],
      ["echo.fail", "bool", false],
      ["echo.refs", "rows", []],
    ]);
  });

  it("reads values and words errors as studio_service does", () => {
    const steps = fields(echo)[1].param;
    expect(readValue(steps, "20")).toEqual({ value: 20 });
    expect(readValue(steps, "0")).toEqual({ error: "must be between 1 and 200" });
    expect(readValue(steps, "2.5")).toEqual({ error: "must be a whole number" });
    expect(readValue(steps, "")).toEqual({ error: "must be a number" });
    expect(readValue({ name: "t", type: "text", default: "", max: 3 }, "abcd")).toEqual({
      error: "must be at most 3 characters",
    });
    expect(readValue({ name: "e", type: "enum", default: "a", enum: ["a", "b"] }, "c")).toEqual({
      error: "pick one of the options",
    });
  });

  it("sends only what changed, and nothing while a field is wrong", () => {
    const fs = fields(echo);
    expect(buildInputs(fs, {})).toEqual({ inputs: {}, errors: {} });
    expect(
      buildInputs(fs, {
        "prompt.text": "a red fox",
        "echo.steps": "20",
        "echo.fail": true,
        "echo.delay_ms": "300",
      }),
    ).toEqual({
      inputs: { "prompt.text": "a red fox", "echo.steps": 20, "echo.fail": true },
      errors: {},
    });
    expect(buildInputs(fs, { "echo.steps": "500" }).errors).toEqual({
      "echo.steps": "must be between 1 and 200",
    });
  });
});

describe("the form derived from the qwen-image workflow", () => {
  const fs = fields(qwen);
  const seed = fs.find((f) => f.key === "seed.seed")!.param;

  it("has a seed, enums, stepped ints and a bool, and keeps when", () => {
    expect(fs.map((f) => [f.key, f.param.type])).toEqual(
      expect.arrayContaining([
        ["seed.seed", "seed"],
        ["model.variant", "enum"],
        ["canvas.width", "int"],
        ["encode.transparent", "bool"],
      ]),
    );
    expect(fs.find((f) => f.key === "canvas.width")!.param.step).toBe(32);
    expect(fs.find((f) => f.key === "sample.steps")!.param.when).toBe(
      "model.variant != 'turbo' && model.variant != 'turbo-fp8'",
    );
  });

  it("reads a seed: blank or -1 is random, else a whole number in range", () => {
    expect(readValue(seed, "")).toEqual({ value: -1 });
    expect(readValue(seed, "-1")).toEqual({ value: -1 });
    expect(readValue(seed, "42")).toEqual({ value: 42 });
    for (const bad of ["-2", "1.5", "x", "9007199254740992"])
      expect(readValue(seed, bad)).toEqual({
        error: "must be a whole number from 0 to 9007199254740991",
      });
  });

  it("sends a fixed seed and the enum picked, but not a random seed (the default)", () => {
    expect(buildInputs(fs, {}).inputs).toEqual({});
    expect(
      buildInputs(fs, { "seed.seed": "7", "model.variant": "turbo", "canvas.width": "768" }).inputs,
    ).toEqual({ "seed.seed": 7, "model.variant": "turbo", "canvas.width": 768 });
  });
});

describe("folding session events into runs", () => {
  it("keeps the phase, the ETA and the resolved seed", () => {
    let runs = [run({ status: "running" })];
    runs = applyEvent(runs, {
      ...base,
      seq: 2,
      type: "progress",
      data: { node: "sample", step: 3, of: 8, phase: "sampling", eta_s: 11.6 },
    }).runs;
    expect(etaText(runs[0])).toBe("0:12 left");
    expect(seedOf(runs[0])).toBeUndefined();
    runs = applyEvent(runs, {
      ...base,
      seq: 3,
      type: "status",
      data: { status: "succeeded", error: null, host: "mac", resolved: { seed: 1234 } },
    }).runs;
    expect(seedOf(runs[0])).toBe(1234);
    expect(seedOf(run({ outputs: [{ ...output.data, seed: 9 } as S<"RunOutput">] }))).toBe(9);
    expect(etaText(run())).toBe("");
  });

  it("walks a run through progress, its output and the end", () => {
    let runs = [run()];
    runs = applyEvent(runs, status(1, "running")).runs;
    runs = applyEvent(runs, progress(2, 3)).runs;
    expect(runs[0].status).toBe("running");
    expect(fraction(runs[0])).toBeCloseTo(0.3);
    runs = applyEvent(runs, output).runs;
    runs = applyEvent(runs, output).runs; // replayed: added once
    expect(runs[0].outputs).toHaveLength(1);
    const end = applyEvent(runs, status(10, "succeeded"));
    expect(end).toMatchObject({ known: true, ended: true });
    expect(end.runs[0].status).toBe("succeeded");
  });

  it("never moves a run backwards, and keeps the error of a failed one", () => {
    const failed = applyEvent([run()], status(3, "failed", "no live host for env fake"));
    expect(failed.runs[0]).toMatchObject({ status: "failed", error: "no live host for env fake" });
    for (const e of [status(1, "queued"), status(2, "running"), progress(4, 5)]) {
      const r = applyEvent(failed.runs, e);
      expect(r.runs).toBe(failed.runs);
      expect(r.ended).toBe(false);
    }
  });

  it("puts a run back in the queue when it is retried, and has it fetched", () => {
    let runs = [run({ status: "running", host: "mac" })];
    runs = applyEvent(runs, progress(2, 5)).runs;
    runs = applyEvent(runs, output).runs;
    const retried = applyEvent(runs, {
      ...status(4, "queued"),
      data: { status: "queued", error: null, host: null, attempt: 2 },
    } as S<"SessionEvent">);
    expect(retried.ended).toBe(true);
    expect(retried.runs[0]).toMatchObject({
      status: "queued",
      attempt: 2,
      host: null,
      progress: null,
      outputs: [],
    });
    expect(applyEvent(retried.runs, status(5, "queued")).runs).toBe(retried.runs); // attempt 1's own queued: nothing
    const stale = [run({ status: "running" })];
    expect(mergeRuns(stale, [run({ status: "queued", attempt: 2 })])[0].attempt).toBe(2);
  });

  it("flags a run it doesn't know, so the list is fetched", () => {
    const r = applyEvent([], status(1, "queued"));
    expect(r).toEqual({ runs: [], known: false, ended: false });
  });

  it("merges a fetched list without losing what the events already told", () => {
    const local = [run({ id: "new" }), run({ status: "running" })];
    const fetched = [run({ status: "queued" }), run({ id: "old", status: "succeeded" })];
    expect(mergeRuns(local, fetched).map((r) => [r.id, r.status])).toEqual([
      ["new", "queued"],
      ["r1", "running"],
      ["old", "succeeded"],
    ]);
  });
});

describe("following a session's events", () => {
  /** A stand-in EventSource that is already closed, so followSession falls back to polling. */
  class ClosedSource {
    static CLOSED = 2;
    static last: ClosedSource;
    readyState = 2;
    onerror: (() => void) | null = null;
    constructor() {
      ClosedSource.last = this;
    }
    addEventListener() {}
    close() {}
  }

  it("drops a poll answer that arrives after unsubscribing", async () => {
    vi.stubGlobal("EventSource", ClosedSource);
    try {
      let answer: (p: S<"EventPage">) => void = () => {};
      const seen: number[] = [];
      const stop = followSession(
        "s1",
        (e) => seen.push(e.seq),
        () => new Promise((r) => (answer = r)),
      );
      ClosedSource.last.onerror?.(); // the stream can't be held: the first poll goes out
      stop(); // the session is closed while it is in flight
      answer({ events: [status(1, "running")] } as S<"EventPage">);
      await Promise.resolve();
      await Promise.resolve();
      expect(seen).toEqual([]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  /** A stand-in EventSource whose tests decide when it opens or closes. */
  class Source {
    static CLOSED = 2;
    static all: Source[] = [];
    readyState = 0;
    onerror: (() => void) | null = null;
    onopen: (() => void) | null = null;
    constructor(public url: string) {
      Source.all.push(this);
    }
    addEventListener() {}
    close() {
      this.readyState = 2;
    }
    fail() {
      this.readyState = 2;
      this.onerror?.();
    }
  }
  const flush = async () => {
    for (let i = 0; i < 5; i++) await Promise.resolve();
  };

  it("polls with backoff while pages fail, then goes back to a stream from the last event", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", Source);
    Source.all = [];
    try {
      const calls: number[] = [];
      let up = false;
      const seen: number[] = [];
      const stop = followSession(
        "s1",
        (e) => seen.push(e.seq),
        async (since) => {
          calls.push(Date.now());
          if (!up) throw new Error("502");
          return { events: [status(since + 1, "running")] } as S<"EventPage">;
        },
      );
      const t0 = Date.now();
      Source.all[0].fail(); // a refused reconnect: polling starts at once
      await flush();
      await vi.advanceTimersByTimeAsync(4_000); // failed pages: the next waits 4 s, then 8 s
      await vi.advanceTimersByTimeAsync(8_000);
      expect(calls.map((t) => t - t0)).toEqual([0, 4_000, 12_000]);
      up = true;
      await vi.advanceTimersByTimeAsync(16_000);
      expect(seen).toEqual([1]);
      // a page came: a new stream from ?since=1; once it opens, polling stops
      expect(Source.all).toHaveLength(2);
      expect(Source.all[1].url).toContain("since=1");
      Source.all[1].onopen?.();
      const n = calls.length;
      await vi.advanceTimersByTimeAsync(60_000);
      expect(calls).toHaveLength(n);
      stop();
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });
});

describe("the inspector and the canvas", () => {
  it("puts the canvas node's params in Canvas, enums and numbers in tiles, bools in switches", () => {
    const s = sections(fields(qwen));
    expect(s.canvas.map((f) => f.key)).toEqual(["canvas.mode", "canvas.width", "canvas.height"]);
    expect(s.refs.map((f) => f.key)).toEqual(["encode.refs"]);
    expect(s.tiles.map((f) => f.key)).toEqual([
      "model.variant",
      "model.precision",
      "sample.steps",
      "decode.tiled",
      "seed.seed",
    ]);
    expect(s.switches.map((f) => f.key)).toEqual(["enhance.think", "encode.transparent"]);
    const e = sections(fields(echo));
    expect(e.canvas).toEqual([]);
    expect(e.switches.map((f) => f.key)).toEqual(["echo.fail"]);
  });

  it("maps an aspect and megapixels to a size on the grid, within bounds, and back", () => {
    expect(sizeFor(1, 1, 32, 256, 4096)).toEqual({ width: 992, height: 992 });
    const wide = sizeFor(16 / 9, 4, 32, 256, 4096);
    expect(wide).toEqual({ width: 2624, height: 1472 });
    expect((wide.width % 32) + (wide.height % 32)).toBe(0);
    expect(sizeFor(9 / 16, 4, 32, 256, 1024)).toEqual({ width: 576, height: 1024 });
    expect(aspectOf(wide.width, wide.height)).toBe("16:9");
    expect(megapixelsOf(wide.width, wide.height)).toBe(4);
    expect(aspectOf(1000, 700)).toBeNull();
  });

  it("keeps every aspect and size on the grid and still that aspect", () => {
    for (const grid of [1, 8, 16, 32, 64])
      for (const [label, r] of ASPECTS)
        for (const [m, max] of [4096, 8192].flatMap((x) =>
          megapixelChoices(256, x, Infinity).map((m) => [m, x]),
        )) {
          const s = sizeFor(r, m, grid, 256, max);
          expect([label, m, aspectOf(s.width, s.height), megapixelsOf(s.width, s.height)]).toEqual([
            label,
            m,
            label,
            m,
          ]);
          expect((s.width % grid) + (s.height % grid)).toBe(0);
        }
  });
});

describe("when expressions and tile labels", () => {
  const vals: Record<string, string | number | boolean> = {
    "model.variant": "turbo",
    "sample.steps": 40,
    "x.on": true,
  };
  const get = (r: string) => vals[r];

  it("evaluates references, literals, comparisons, logic and parentheses", () => {
    expect(evaluate("model.variant != 'turbo'", get)).toBe(false);
    expect(evaluate('model.variant == "turbo"', get)).toBe(true);
    expect(evaluate("sample.steps == 40 && !x.on", get)).toBe(false);
    expect(evaluate("sample.steps == 41 || x.on", get)).toBe(true);
    expect(evaluate("!(model.variant == 'base') && (x.on == true)", get)).toBe(true);
    expect(evaluate("a.b == null", get)).toBe(false);
    for (const bad of ["model.variant ==", "(x.on", "x.on = 1", "x.on x.on"])
      expect(() => evaluate(bad, get)).toThrow();
  });

  it("hides steps for turbo and does not send them", () => {
    const fs = fields(qwen);
    expect(visible(fs, {}).some((f) => f.key === "sample.steps")).toBe(true);
    const turbo = visible(fs, { "model.variant": "turbo", "sample.steps": "12" });
    expect(turbo.some((f) => f.key === "sample.steps")).toBe(false);
    expect(buildInputs(turbo, { "model.variant": "turbo", "sample.steps": "12" }).inputs).toEqual({
      "model.variant": "turbo",
    });
  });

  it("reads an unplaced field in a placed field's when (the scope)", () => {
    const fs = fields(qwen);
    const shown = fs.filter((f) => f.key === "sample.steps");
    expect(visible(shown, { "model.variant": "turbo" }).length).toBe(1); // model.variant out of reach: shown
    expect(visible(shown, { "model.variant": "turbo" }, fs).length).toBe(0);
  });

  it("labels tiles as the mock does, from the schema", () => {
    const f = (k: string) => fields(qwen).find((x) => x.key === k)!;
    expect(display(f("model.variant"), "base")).toEqual({ label: "Model", value: "Base" });
    expect(display(f("model.precision"), "int8")).toEqual({ label: "Precision", value: "int8" });
    expect(display(f("decode.tiled"), "auto")).toEqual({ label: "Decode", value: "Tiled · auto" });
    expect(display(f("sample.steps"), "40")).toEqual({ label: "Steps", value: "40" });
  });
});

describe("queueing several seeds", () => {
  const fs = fields(qwen);
  it("queues consecutive seeds from the one set, or all random", () => {
    expect(queueInputs(fs, { "seed.seed": 42 }, 3).runs.map((i) => i["seed.seed"])).toEqual([
      42, 43, 44,
    ]);
    expect(queueInputs(fs, {}, 2).runs).toEqual([{ "seed.seed": -1 }, { "seed.seed": -1 }]);
    expect(queueInputs(fs, { "model.variant": "turbo" }, 1).runs).toEqual([
      { "model.variant": "turbo" },
    ]);
  });

  it("queues nothing when the last seed would pass the seed's max", () => {
    const capped = fs.map((f) =>
      f.param.type === "seed" ? { ...f, param: { ...f.param, max: 100 } } : f,
    );
    expect(queueInputs(capped, { "seed.seed": 97 }, 4).runs).toHaveLength(4);
    const over = queueInputs(capped, { "seed.seed": 98 }, 4);
    expect(over.runs).toEqual([]);
    expect(over.errors["seed.seed"]).toMatch(/at most 97/);
    expect(queueInputs(capped, {}, 4).runs).toHaveLength(4); // random seeds have no max to pass
  });
});

describe("the progress strip", () => {
  const at = (s: number) => `2026-11-14T10:00:${String(s).padStart(2, "0")}Z`;
  const ev = (
    seq: number,
    s: number,
    node: string,
    step: number,
    of: number,
  ): S<"SessionEvent"> => ({
    seq,
    run_id: "r1",
    ts: at(s),
    type: "progress",
    data: { node, step, of },
  });
  const states = (r: S<"Run">, t?: RunTrack) =>
    segments(qwen, r, t).map((x) => [x.key, x.state, x.seconds]);

  it("orders the working nodes by the graph", () => {
    expect(graphOrder(qwen).indexOf("sample")).toBeGreaterThan(graphOrder(qwen).indexOf("canvas"));
    expect(segments(echo, run()).map((x) => [x.title, x.state])).toEqual([
      ["Echo", "pending"],
      ["Saving", "pending"],
    ]);
  });

  it("names no step count the form sets: Qwen's Sample reads Sample (docs/mock/Main.dc.html)", () => {
    const sample = segments(qwen, run()).find((x) => x.node === "sample");
    expect([sample?.title, sample?.state, sample?.steps]).toEqual(["Sample", "pending", undefined]);
  });

  it("marks nodes done (with their time), active and pending from the events", () => {
    let t: Record<string, RunTrack> = {};
    for (const e of [
      { ...status(1, "running"), ts: at(0) },
      ev(2, 4, "encode", 1, 1),
      ev(3, 6, "sample", 1, 40),
      ev(4, 9, "sample", 12, 40),
    ])
      t = track(t, e);
    const r = run({ status: "running", progress: { node: "sample", step: 12, of: 40 } });
    expect(states(r, t.r1)).toEqual([
      ["enhance", "bypassed", undefined], // bypassed by default: no time of its own, none taken from encode
      ["encode", "done", 4],
      ["sample", "active", undefined],
      ["decode", "pending", undefined],
      ["saving", "pending", undefined],
    ]);
    expect(states({ ...r, status: "cancelled" }, t.r1)[2][1]).toBe("stopped");
    expect(states({ ...r, status: "succeeded" }, t.r1).map((x) => x[1])).toEqual([
      "bypassed",
      "done",
      "done",
      "done",
      "done",
    ]);
    expect(states(run()).map((x) => x[1])).toEqual([
      "bypassed",
      "pending",
      "pending",
      "pending",
      "pending",
    ]);
  });

  it("keeps reported nodes done when an earlier node reports again, and times a node after a silent one alone", () => {
    let t: Record<string, RunTrack> = {};
    for (const e of [
      { ...status(1, "running"), ts: at(0) },
      ev(2, 6, "sample", 1, 40),
      ev(3, 9, "sample", 40, 40),
      ev(4, 10, "model", 1, 1),
    ])
      t = track(t, e);
    const r = run({ status: "running", progress: { node: "model", step: 1, of: 1 } });
    expect(states(r, t.r1)).toEqual([
      ["model", "active", undefined],
      ["enhance", "bypassed", undefined],
      ["encode", "done", undefined],
      ["sample", "done", 3],
      ["decode", "pending", undefined],
      ["saving", "pending", undefined],
    ]);
  });

  it("times a node that ran before an earlier piece alone, never negative", () => {
    let t: Record<string, RunTrack> = {};
    for (const e of [
      { ...status(1, "running"), ts: at(0) },
      ev(2, 4, "encode", 1, 1),
      ev(3, 9, "model", 1, 1), // reports after encode, though the graph has it first
      ev(4, 12, "sample", 1, 40),
    ])
      t = track(t, e);
    const r = run({ status: "running", progress: { node: "sample", step: 1, of: 40 } });
    expect(states(r, t.r1).slice(0, 3)).toEqual([
      ["model", "done", 9],
      ["enhance", "bypassed", undefined],
      ["encode", "done", 0],
    ]);
  });

  it("gives a node the graph does not have its own piece", () => {
    const r = run({ status: "failed", progress: { node: "vae-load", step: 1, of: 2 } });
    expect(states(r).at(-2)).toEqual(["vae-load", "stopped", undefined]);
  });

  it("includes a node that reported progress though it takes no inputs (loading the model)", () => {
    const t = track({}, ev(1, 1, "model", 1, 1));
    const r = run({ status: "running", progress: { node: "model", step: 1, of: 1 } });
    expect(states(r, t.r1)[0]).toEqual(["model", "active", undefined]);
  });

  it("ends an image or video strip with Saving: lit while the output uploads, then timed to the run's end", () => {
    const save = (seq: number, s: number): S<"SessionEvent"> => ({
      seq,
      run_id: "r1",
      ts: at(s),
      type: "progress",
      data: { node: "output", step: 1, of: 1, phase: "saving" },
    });
    let t: Record<string, RunTrack> = {};
    for (const e of [
      { ...status(1, "running"), ts: at(0) },
      ev(2, 4, "encode", 1, 1),
      ev(3, 9, "sample", 40, 40),
      ev(4, 10, "decode", 1, 1),
      save(5, 12),
    ])
      t = track(t, e);
    const r = run({
      status: "running",
      progress: { node: "output", step: 1, of: 1, phase: "saving" },
    });
    const last = segments(qwen, r, t.r1).at(-1)!;
    // "Saving…", no bar for its one step (docs/mock/CreateMediaRefs.dc.html)
    expect([last.key, last.title, last.state, last.bar]).toEqual([
      "saving",
      "Saving",
      "active",
      false,
    ]);
    expect(segments(qwen, r, t.r1).some((x) => x.node === "output" && x.key !== "saving")).toBe(
      false,
    );
    t = track(t, { ...status(6, "succeeded"), ts: at(14) });
    expect(states({ ...r, status: "succeeded" }, t.r1).at(-1)).toEqual(["saving", "done", 4]);
    // an upload under 2 s never reports: Saving still takes the time to the run's end
    let quick: Record<string, RunTrack> = {};
    for (const e of [
      { ...status(1, "running"), ts: at(0) },
      ev(2, 10, "decode", 1, 1),
      { ...status(3, "succeeded"), ts: at(11) },
    ])
      quick = track(quick, e);
    expect(states(run({ status: "succeeded" }), quick.r1).at(-1)).toEqual(["saving", "done", 1]);
    // a run that failed before saving leaves it pending
    expect(
      states(run({ status: "failed", progress: { node: "sample", step: 1, of: 40 } }), quick.r1).at(
        -1,
      )?.[1],
    ).toBe("pending");
    // an audio workflow has none (docs/mock/CreateAudio.dc.html)
    expect(segments({ ...qwen, media: "audio" }, run()).some((x) => x.key === "saving")).toBe(
      false,
    );
  });

  it("draws a node with phases (H3's generate) as one piece per phase, timed by phase", () => {
    const h3 = {
      ...h3Workflows.workflows[0],
      node_schemas: Object.fromEntries(
        [...nodesFixture.nodes, ...h3Nodes.nodes].map((n) => [n.kind, n]),
      ),
    } as unknown as S<"Workflow">;
    const gen = Object.keys(h3.nodes).find((id) => h3.nodes[id].kind === "minimax-h3/generate")!;
    const ph = (seq: number, s: number, phase_id: string, step = 1, of = 1): S<"SessionEvent"> => ({
      seq,
      run_id: "r1",
      ts: at(s),
      type: "progress",
      data: { node: gen, step, of, phase_id },
    });
    let t: Record<string, RunTrack> = {};
    for (const e of [
      { ...status(1, "running"), ts: at(0) },
      ph(2, 10, "encode"),
      ph(3, 12, "sample", 1, 6),
      ph(4, 20, "sample", 4, 6),
    ])
      t = track(t, e);
    const r = run({
      workflow_id: h3.id,
      status: "running",
      progress: { node: gen, step: 4, of: 6, phase_id: "sample" },
    });
    const pieces = segments(h3, r, t.r1).filter((x) => x.node === gen);
    expect(pieces.map((x) => [x.title, x.state, x.seconds, x.bar])).toEqual([
      ["Encode", "done", 10, undefined],
      ["Sample", "active", undefined, true],
      ["Decode audio", "pending", undefined, undefined],
      ["Decode video", "pending", undefined, undefined],
    ]);
    expect(segments(h3, r, t.r1).at(-1)?.title).toBe("Saving");
    // a phase of one step shows no bar
    const enc = { ...r, progress: { node: gen, step: 1, of: 1, phase_id: "encode" } };
    expect(segments(h3, enc).find((x) => x.state === "active")).toMatchObject({
      title: "Encode",
      bar: false,
    });
    t = track(t, ph(5, 23, "decode_audio"));
    t = track(t, ph(6, 44, "decode_video"));
    t = track(t, { ...status(7, "succeeded"), ts: at(48) });
    expect(
      segments(h3, { ...r, status: "succeeded" }, t.r1)
        .filter((x) => x.node === gen || x.key === "saving")
        .map((x) => [x.title, x.state, x.seconds]),
    ).toEqual([
      ["Encode", "done", 10],
      ["Sample", "done", 10],
      ["Decode audio", "done", 3],
      ["Decode video", "done", 21],
      ["Saving", "done", 4],
    ]);
  });

  it("knows how long the last finished run of a workflow took", () => {
    const done = run({ status: "succeeded", started_at: at(0), finished_at: at(52) });
    expect(lastDuration([run(), done], "echo")).toBe(52);
    expect(lastDuration([done], "qwen-image")).toBeNull();
  });

  it("says which pinned entries a queued run would unload first", () => {
    const pin = { env: "torch", model: "qwen-image-2.1", variant: "turbo", precision: "int8" };
    const unloads_pinned = [
      { ...pin, component: "transformer", gb: 6.76 },
      { ...pin, component: "vae", gb: 0.3 },
    ];
    expect(pinnedNote(run({ status: "queued", unloads_pinned }))).toBe(
      "unloads pinned transformer, VAE, reloads ≈ 7.1 GB after",
    );
    expect(pinnedNote(run({ status: "running", unloads_pinned }))).toBe("");
    expect(pinnedNote(run({ status: "queued" }))).toBe("");
    expect(pinnedNote(undefined)).toBe("");
  });
});

describe("pretty", () => {
  it("shows enum values as words, ids as they are", () => {
    expect(
      ["base", "custom_voice", "int8", "base-fp8", "turbo-fp8", "en-GB", "stage1"].map(pretty),
    ).toEqual(["Base", "Custom voice", "int8", "Base fp8", "Turbo fp8", "en-GB", "stage1"]);
  });
  it("shows a variant's precision once", () => {
    expect(prettyModel(["base-fp8", "fp8"])).toEqual(["Base", "fp8"]);
    expect(prettyModel(["base", "int8"])).toEqual(["Base", "int8"]);
    expect(prettyModel(["turbo-fp8"])).toEqual(["Turbo fp8"]);
  });
});
