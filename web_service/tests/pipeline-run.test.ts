import { describe, expect, it } from "vitest";
import rowFixture from "../../contracts/fixtures/http/BatchRow.valid.3.json";
import execFixture from "../../contracts/fixtures/http/RowExecution.valid.json";
import batchFixture from "../../contracts/fixtures/http/Batch.valid.3.json";
import row5 from "../../contracts/fixtures/http/BatchRow.valid.5.json";
import decision1 from "../../contracts/fixtures/http/Decision.valid.1.json";
import memory1 from "../../contracts/fixtures/http/MemoryItem.valid.1.json";
import review2 from "../../contracts/fixtures/http/BatchReview.valid.2.json";
import exec2 from "../../contracts/fixtures/http/RowExecution.valid.2.json";
import decision3 from "../../contracts/fixtures/http/Decision.valid.3.json";
import type { S } from "@/lib/api";
import {
  agentEnd,
  asRun,
  boundsLine,
  canPick,
  criterionLines,
  decisionModel,
  initialPick,
  judgeLine,
  judgeSummary,
  memorySource,
  wordDiff,
  canRetryFrom,
  cellLines,
  cellSummary,
  countFacts,
  gridMove,
  rejectLabel,
  togglePick,
  rowTakes,
  runColumns,
  settledCell,
  stepDetail,
  stepLines,
  attemptWords,
  stepProgress,
  unreached,
} from "@/lib/pipeline-run";

const row = rowFixture as unknown as S<"BatchRow">;
const exec = execFixture as unknown as S<"RowExecution">;
const batch = batchFixture as unknown as S<"Batch">;
const doc = exec.pipeline;

describe("run table", () => {
  it("has a column per step and gate in row order", () => {
    expect(runColumns(doc, [row], { "qwen-image": "Qwen Image" })).toEqual([
      { node: "still", kind: "step", title: "Step 1 · Qwen Image" },
      { node: "review", kind: "gate", title: "Pick one still to edit" },
      { node: "edit", kind: "step", title: "Step 2 · Qwen edit" },
    ]);
    // without the document: the cells' nodes as reached
    expect(runColumns(null, [row]).map((c) => c.node)).toEqual(["still", "review"]);
  });
  it("stacks loop attempts, oldest first, with seeds and time", () => {
    const cell: S<"BatchCell"> = {
      node: "still",
      kind: "step",
      attempts: [
        { ...row.cells![0].attempts[0] },
        {
          ...row.cells![0].attempts[0],
          visit: 2,
          status: "running",
          take_ids: [],
          seconds: null,
          seeds: [46],
        },
      ],
    };
    const [a, b] = cellLines(cell);
    expect(a).toMatchObject({
      visit: 1,
      text: "done · 4 takes",
      seed: "seeds 42–45",
      time: "32 s",
      tone: "",
    });
    expect(b).toMatchObject({ visit: 2, text: "running", tone: "run", seed: "seed 46", time: "" });
    expect(cellLines(row.cells![1])[0]).toMatchObject({
      text: "waiting for your pick",
      tone: "wait",
    });
    expect(cellLines(row.cells![1])[0].takes).toHaveLength(4);
    const failed = {
      ...cell.attempts[0],
      status: "failed" as const,
      error: "did not fit in memory",
    };
    expect(cellLines({ ...cell, attempts: [failed] })[0]).toMatchObject({
      text: "failed · did not fit in memory",
      tone: "warn",
    });
  });
  it("names step columns by what they make, and sums a cell up in one line", () => {
    const wf = (doc.nodes.still as S<"PipelineStepNode">).workflow;
    expect(runColumns(doc, [row], {}, { [wf]: "image" })[0].title).toBe("Step 1 · images");
    const cell = row.cells![0];
    const a = cell.attempts[0];
    expect(cellSummary(cell).text).toBe("done · 4 takes");
    expect(cellSummary({ ...cell, attempts: [{ ...a, take_ids: [a.take_ids[0]] }] }).text).toBe(
      "done",
    );
    expect(cellSummary({ ...cell, attempts: [a, { ...a, visit: 2 }] })).toMatchObject({
      text: "done · 2 attempts",
      takes: a.take_ids,
    });
    expect(
      cellSummary({
        ...cell,
        attempts: [{ ...a, status: "failed", error: "did not fit in memory" }],
      }),
    ).toMatchObject({ text: "failed · did not fit in memory", tone: "warn" });
    expect(
      cellSummary({
        ...cell,
        attempts: [
          { ...a, branch: 0 },
          { ...a, branch: 1, status: "failed" },
        ],
      }),
    ).toMatchObject({ text: "2 shots · 1 done · 1 failed", tone: "warn" });
  });
  it("counts each step's progress over the last batch's rows", () => {
    const p = stepProgress(doc, [row, { ...row, row: 2, cells: [] }], 2);
    expect(p.still).toEqual({ done: 1, total: 2, state: "" });
    expect(p.edit).toEqual({ done: 0, total: 2, state: "" });
    expect(stepProgress(doc, [row], 1).still.state).toBe("done");
    const running = {
      ...row,
      cells: [
        {
          ...row.cells![0],
          attempts: [{ ...row.cells![0].attempts[0], status: "running" as const }],
        },
      ],
    };
    expect(stepProgress(doc, [running], 1).still).toEqual({ done: 0, total: 1, state: "running" });
  });
  it("marks unreached cells, takes and counts", () => {
    expect(unreached(row)).toBe("parked"); // waiting for a pick
    expect(unreached({ ...row, status: "running" })).toBe("—");
    expect(unreached({ ...row, status: "failed" })).toBe("skipped");
    expect(rowTakes(row)).toEqual({ ids: [], thumb: null });
    expect(
      countFacts({
        total: 8,
        queued: 1,
        running: 0,
        waiting: 3,
        succeeded: 4,
        failed: 0,
        cancelled: 0,
      }),
    ).toEqual(["4 done", "3 waiting for a pick", "1 queued"]);
    expect(canPick({ ...batch, counts: { ...batch.counts, waiting: 1 } })).toBe(
      batch.control !== "stop",
    );
    expect(canPick({ ...batch, counts: { ...batch.counts, waiting: 0 } })).toBe(false);
  });
});

describe("execution view", () => {
  it("names each graph step by node and part", () => {
    expect(stepLines(exec).map((s) => [s.step, s.label, s.status])).toEqual([
      [1, "Inputs", "done"],
      [2, "Step 1 · submit", "done"],
      [3, "Step 1 · wait", "resumed"],
      [4, "Pick one still to edit", "now"],
    ]);
    expect(stepLines(exec)[2].changed).toBe("takes.still = 4");
    expect(stepLines(exec).map((s) => s.tone)).toEqual(["", "", "", "now"]);
    expect(attemptWords(1)).toBe("first attempt");
    expect(attemptWords(7)).toBe("attempt 7");
  });
  it("marks the graph as run", () => {
    const g = asRun(exec);
    expect(g.nodes.review).toEqual({ visits: 1, status: "waiting", current: true });
    expect(g.nodes.edit).toBeUndefined();
    expect(g.taken("still", "review", "next")).toBe(1);
    expect(g.taken("review", "still", "fail")).toBe(0);
  });
  it("details a gate with its step's takes as candidates", () => {
    const d = stepDetail(exec, 4)!;
    expect(d.kind).toBe("gate");
    expect("candidates" in d && d.candidates).toHaveLength(4);
    expect("picked" in d && d.picked).toBeNull();
    expect(stepDetail(exec, 3)).toMatchObject({ kind: "step" });
  });
  it("offers Retry row from here when the state before the step can be read", () => {
    const running = { ...batch, status: "running" as const, control: "run" as const };
    expect(canRetryFrom(exec, 4, running)).toBe(true);
    expect(canRetryFrom(exec, 1, running)).toBe(true);
    expect(canRetryFrom(exec, 5, running)).toBe(false);
    expect(canRetryFrom(exec, 4, { ...running, status: "done" })).toBe(false);
    expect(canRetryFrom({ ...exec, attempts: 2 }, 4, running)).toBe(false);
  });
});

describe("review grid", () => {
  it("toggles picks up to keep, replacing at keep 1", () => {
    expect(togglePick([], "a", 1)).toEqual(["a"]);
    expect(togglePick(["a"], "b", 1)).toEqual(["b"]);
    expect(togglePick(["a"], "a", 1)).toEqual([]);
    expect(togglePick(["a", "b"], "c", 2)).toEqual(["a", "b"]);
    expect(togglePick(["a"], "c", 2)).toEqual(["a", "c"]);
  });
  it("moves focus by arrows, stopping at the edges", () => {
    expect(gridMove(0, "ArrowRight", 4, 2)).toBe(1);
    expect(gridMove(1, "ArrowRight", 4, 2)).toBe(2);
    expect(gridMove(0, "ArrowDown", 4, 2)).toBe(2);
    expect(gridMove(2, "ArrowDown", 4, 2)).toBe(2);
    expect(gridMove(0, "ArrowLeft", 4, 2)).toBe(0);
    expect(gridMove(1, "End", 4, 2)).toBe(3);
  });
  it("names what rejecting does", () => {
    expect(rejectLabel({ on_reject: "retry", rejects_left: 2 })).toBe("None, try again (2 left)");
    expect(rejectLabel({ on_reject: "then", rejects_left: 0 })).toBe("None, move on");
    expect(rejectLabel({ on_reject: "fail", rejects_left: 0 })).toBe("None, fail the row");
  });
});

describe("agent nodes in the run table, execution view and review", () => {
  const judgeRow = row5 as unknown as S<"BatchRow">;
  const cell = judgeRow.cells!.find((c) => c.kind === "judge")!;
  it("the judge's cell: suggestion, pick, retries, give-up", () => {
    expect(judgeSummary(cell)).toEqual({ text: "suggests take 3 · waiting for you", tone: "wait" });
    const a = cell.attempts[0];
    const picked = { ...a, status: "picked" as const, take_ids: [a.judged!.suggested[0]] };
    expect(judgeLine(picked)).toEqual({ text: "picked take 3 · all checks pass", tone: "ok" });
    const missedNice = {
      ...picked,
      judged: {
        ...a.judged!,
        takes: a.judged!.takes.map((t) => ({ ...t, checks: [true, true, true, false] })),
      },
    };
    expect(judgeLine(missedNice)).toEqual({ text: "picked take 3", tone: "ok" }); // a nice one missed
    const failed = {
      ...a,
      visit: 1,
      status: "rejected" as const,
      judged: { ...a.judged!, verdict: "fail" as const, first_failed: "extra limb", suggested: [] },
    };
    const retried = {
      ...cell,
      loop: { ...cell.loop!, turns: 1 },
      attempts: [failed, { ...picked, visit: 2 }],
    };
    expect(judgeSummary(retried)).toEqual({ text: "retried 1 of 2 · extra limb", tone: "warn" });
    const gaveUp = {
      ...retried,
      loop: { ...retried.loop, turns: 2, gave_up: true },
      attempts: [failed],
    };
    expect(judgeSummary(gaveUp, (id) => (id === "review" ? "Review" : id))).toEqual({
      text: "gave up → Review",
      tone: "warn",
    });
    expect(judgeSummary({ ...gaveUp, attempts: [{ ...failed, status: "waiting" }] }).text).toBe(
      "none passed · waiting for you",
    );
    expect(runColumns(null, [judgeRow]).map((c) => c.kind)).toEqual(["step", "judge"]);
  });
  it("criteria over the takes, as the Verdict tile says them", () => {
    const j = (decision1 as unknown as S<"Decision">).judge!;
    const lines = criterionLines(j);
    expect(lines.map((l) => [l.criterion, l.text, l.bad])).toEqual(
      lines.map((l) => {
        const k = j.takes.filter(
          (t) => t.checks.find((c) => c.criterion === l.criterion)?.pass,
        ).length;
        return [l.criterion, l.text, l.level === "must" && k < j.takes.length];
      }),
    );
    const one = (pass: boolean[]) =>
      criterionLines({
        ...j,
        criteria: { must: ["a"], nice: ["b"] },
        takes: pass.map((p, i) => ({
          ...j.takes[0],
          take_id: String(i),
          checks: [
            { criterion: "a", level: "must", pass: p },
            { criterion: "b", level: "nice", pass: !p },
          ],
        })),
      }).map((l) => l.text);
    expect(one([false, false, false, false])).toEqual(["failed on all 4", "passed"]);
    expect(one([true, true, true, false])).toEqual(["failed on 1", "passed on 1"]);
  });
  it("a write's diff, the bounds line, memory sources, the review's first pick", () => {
    expect(wordDiff("@fox in snow", "@fox in deep snow")).toEqual([
      { text: "@fox in ", op: "same" },
      { text: "deep ", op: "add" },
      { text: "snow", op: "same" },
    ]);
    const d = wordDiff("a red fox", "a blue fox");
    expect(
      d
        .filter((x) => x.op !== "add")
        .map((x) => x.text)
        .join(""),
    ).toBe("a red fox");
    expect(
      d
        .filter((x) => x.op !== "del")
        .map((x) => x.text)
        .join(""),
    ).toBe("a blue fox");
    expect(d.find((x) => x.op === "del")?.text).toBe("red");
    expect(boundsLine({ runs: 13, llm_calls: 6 }, 10)).toBe(
      "at most 13 runs and 6 LLM calls per row (130 runs, 60 calls in all)",
    );
    expect(boundsLine({ runs: 1, llm_calls: 0 }, 1)).toBe("at most 1 run per row");
    const m = memory1 as unknown as S<"MemoryItem">;
    expect(memorySource(m, () => "Judge")).toBe(
      "your pick at Judge · row 6 · you changed its pick",
    );
    expect(memorySource({ ...m, source: { ...m.source, kind: "user" } })).toBe("added by you");
    const item = (review2 as unknown as S<"BatchReview">).items[0];
    expect(initialPick(item)).toEqual(item.suggested);
    expect(initialPick({ ...item, kind: "gate" })).toEqual([]);
  });
  it("a step with a decision shows it", () => {
    const x = exec2 as unknown as S<"RowExecution">;
    const s = x.steps.find((t) => t.decision)!;
    expect(stepDetail(x, s.step)).toMatchObject({ kind: s.decision!.kind, decision: s.decision });
  });
});

describe("a row that ended while parked", () => {
  it("shows its never-picked judge attempt cancelled", () => {
    const cell = (row5 as unknown as S<"BatchRow">).cells!.find((c) => c.kind === "judge")!;
    expect(settledCell(cell, "waiting")).toBe(cell);
    const ended = settledCell(cell, "cancelled")!;
    expect(ended.attempts.map((a) => a.status)).toEqual(cell.attempts.map(() => "cancelled"));
    expect(judgeSummary(ended)).toEqual({ text: "cancelled", tone: "" });
  });
});

describe("stage B cells and agent steps", () => {
  const attempt = (o: Partial<S<"BatchCellAttempt">>): S<"BatchCellAttempt"> => ({
    visit: 1,
    status: "succeeded",
    run_ids: [],
    take_ids: ["t"],
    seeds: [40],
    seconds: 3,
    error: null,
    thumb_url: null,
    ...o,
  });
  it("lists a fanned-out step's first shots, then sums up the rest", () => {
    const cell: S<"BatchCell"> = {
      node: "shot",
      kind: "step",
      attempts: [0, 1, 2, 3, 4].map((b) =>
        attempt({ branch: b, seeds: [40 + b], status: b === 4 ? "failed" : "succeeded" }),
      ),
    };
    const lines = cellLines(cell);
    expect(lines.map((l) => l.text)).toEqual([
      "shot 1 · done · 1 take",
      "shot 2 · done · 1 take",
      "shot 3 · done · 1 take",
      "+ 2 shots · 1 done · 1 failed",
    ]);
    expect(lines.map((l) => l.branch)).toEqual([0, 1, 2, undefined]);
    expect(lines[3].tone).toBe("warn");
  });
  it("says an agent attempt in counts", () => {
    const cell: S<"BatchCell"> = {
      node: "director",
      kind: "agent",
      attempts: [
        attempt({
          take_ids: [],
          seeds: [],
          agent: { turns: 4, tool_calls: 6, tokens: 9210, items: 14, route: null },
        }),
      ],
    };
    expect(cellLines(cell)[0].text).toBe("4 turns · 6 tools · 14 shots");
  });
  it("tells an agent visit's end and where its model ran", () => {
    const d = decision3 as unknown as S<"Decision">;
    expect(agentEnd(d.agent!)).toBe("answered 1 item");
    expect(agentEnd({ ...d.agent!, stopped: "tool_calls" })).toBe("stopped at its tool calls cap");
    expect(agentEnd({ ...d.agent!, route: "done", forced: true })).toBe(
      "routed done (forced by the cap)",
    );
    expect(
      decisionModel({ ...d, source: { kind: "remote", provider: "fakeai", model: "x" } }),
    ).toBe("Remote · fakeai · x");
  });
  it("counts agents' tool calls in Submit's worst case", () => {
    expect(boundsLine({ runs: 24, llm_calls: 6, tool_calls: 12 }, 1)).toBe(
      "at most 24 runs and 6 LLM calls per row; agents make at most 12 tool calls a row",
    );
  });
  it("shows a director's chat and the rows agents may add, per batch", () => {
    const b = { runs: 24, llm_calls: 6, tool_calls: 12, added_rows: 100 };
    expect(boundsLine({ ...b, chat: { calls: 1200, tokens: 1600000, tool_calls: 1200 } }, 1)).toBe(
      "at most 24 runs and 6 LLM calls per row; agents make at most 12 tool calls a row; the director's chat " +
        "asks a model at most 1200 times in all; agents may add up to 100 rows, each with the same worst case",
    );
  });
});
