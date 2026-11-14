import { describe, expect, it, vi } from "vitest";
import nodesFixture from "../../contracts/fixtures/registry/nodes.valid.json";
import workflowsFixture from "../../contracts/fixtures/registry/workflows.valid.json";
import type { S } from "@/lib/api";
import { applyEvent, followSession, fraction, mergeRuns } from "@/lib/runs";
import { buildInputs, fields, readValue } from "@/lib/workflow";

/** The echo workflow as GET /v1/studio/workflows/echo serves it: the registry's, with its node schemas. */
const echo = {
  ...workflowsFixture.workflows[0],
  node_schemas: Object.fromEntries(nodesFixture.nodes.map((n) => [n.kind, n])),
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
  it("has the prompt text, then steps, delay and fail, keyed node.param, starting from the workflow's values", () => {
    expect(fields(echo).map((f) => [f.key, f.param.type, f.initial])).toEqual([
      ["prompt.text", "text", ""],
      ["echo.steps", "int", 10],
      ["echo.delay_ms", "int", 300],
      ["echo.fail", "bool", false],
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

describe("folding session events into runs", () => {
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
});
