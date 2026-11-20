import { describe, expect, it, vi } from "vitest";
import nodesFixture from "../../contracts/fixtures/registry/nodes.valid.json";
import workflowsFixture from "../../contracts/fixtures/registry/workflows.valid.json";
import batchFixture from "../../contracts/fixtures/http/Batch.valid.1.json";
import rowFixture from "../../contracts/fixtures/http/BatchRow.valid.1.json";
import stillToClip from "../../contracts/fixtures/pipelines/valid.still-to-clip.json";
import batch9 from "../../contracts/fixtures/http/BatchCreate.valid.9.json";
import { api, type S } from "@/lib/api";
import {
  appendRows,
  applyBatchEvent,
  batchState,
  buildTable,
  canPause,
  canResume,
  canRetry,
  canStop,
  doneRows,
  gridSize,
  inputSummary,
  lineValues,
  matchColumn,
  parseCsv,
  rowError,
  retryFailedRows,
  rowState,
  sizeError,
  summary,
  typedLines,
} from "@/lib/batches";
import { fields } from "@/lib/workflow";
import { pipelineFields } from "@/lib/pipeline";

const echo = {
  ...workflowsFixture.workflows[0],
  node_schemas: Object.fromEntries(nodesFixture.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;
const fs = fields(echo);
const field = (key: string) => fs.find((f) => f.key === key)!;

const batch = batchFixture as S<"Batch">;
const row = (over: Partial<S<"BatchRow">> = {}) => ({ ...(rowFixture as S<"BatchRow">), ...over });

describe("CSV", () => {
  it("parses quotes, separators and newlines inside quotes, CRLF, and drops blank lines", () => {
    expect(parseCsv('prompt,steps\r\n"a fox, at dusk",20\n\n"say ""hi""\nthere",5\n')).toEqual([
      ["prompt", "steps"],
      ["a fox, at dusk", "20"],
      ['say "hi"\nthere', "5"],
    ]);
  });
  it("takes tab-separated text pasted from a spreadsheet", () => {
    expect(parseCsv("text\tsteps\na, b\t3")).toEqual([
      ["text", "steps"],
      ["a, b", "3"],
    ]);
  });
  it("maps headers by key, param name or label, any case", () => {
    expect(matchColumn("echo.steps", fs)).toBe("echo.steps");
    expect(matchColumn(" TEXT ", fs)).toBe("prompt.text");
    expect(matchColumn("colour", fs)).toBe("");
  });
  it("types cells by their param, blank is null, and reports errors by data row", () => {
    const cells = parseCsv("text,steps,fail,note\na fox,20,yes,x\nb,,no,y\nc,999,maybe,z");
    const { table, errors } = buildTable(cells, ["prompt.text", "echo.steps", "echo.fail", ""], fs);
    expect(table.columns).toEqual(["prompt.text", "echo.steps", "echo.fail"]);
    expect(table.rows).toEqual([
      ["a fox", 20, true],
      ["b", null, false],
      ["c", null, null],
    ]);
    expect(errors.map((e) => e.row)).toEqual([3, 3]);
    expect(errors[0].text).toMatch(/between 1 and 200/);
  });
  it("needs one mapped column; a short line's missing cells are blank", () => {
    expect(buildTable([["a"], ["x"]], [""], fs).errors[0].row).toBe(0);
    expect(
      buildTable([["text", "steps"], ["x"]], ["prompt.text", "echo.steps"], fs).table.rows,
    ).toEqual([["x", null]]);
  });
});

describe("lines, grid and run maths", () => {
  it("drops blank lines and types values by line", () => {
    expect(lineValues("a\n\n  b  \r\n")).toEqual(["a", "b"]);
    const r = typedLines(field("echo.steps"), "5\nlots\n7");
    expect(r.values).toEqual([5, 7]);
    expect(r.errors).toEqual([{ row: 2, text: "Steps must be a number" }]);
  });
  it("counts a grid as the product of its axes", () => {
    expect(gridSize([{ values: [1, 2, 3] }, { values: ["a", "b"] }])).toBe(6);
    expect(gridSize([])).toBe(0);
  });
  it("sums up rows × seeds and refuses more than 500 runs or none", () => {
    expect(summary(12, 2)).toBe("12 rows × 2 seeds = 24 runs");
    expect(summary(1, 1)).toBe("1 row × 1 seed = 1 run");
    expect(summary(3, null)).toBe("3 rows = 3 runs");
    expect(sizeError(250, 2)).toBe("");
    expect(sizeError(251, 2)).toMatch(/502/);
    expect(sizeError(0, 1)).toBe("Add at least one row.");
  });
  it("reads the input row out of a Submit 422", () => {
    expect(rowError("row 3: echo.steps: must be between 1 and 200")).toEqual({
      row: 3,
      text: "echo.steps: must be between 1 and 200",
    });
    expect(rowError("unknown workflow x")).toBeNull();
  });
});

describe("batch events", () => {
  const view = { batch, rows: [row({ row: 1 }), row({ row: 3, status: "running", take_ids: [] })] };
  it("a batch event is the whole batch", () => {
    const next = { ...batch, status: "paused" as const, control: "pause" as const };
    expect(applyBatchEvent(view, { seq: 2, type: "batch", ts: "", data: next }).batch).toBe(next);
  });
  it("a row event replaces a loaded row, not an unloaded one or a later attempt", () => {
    const done = row({ row: 3 });
    expect(applyBatchEvent(view, { seq: 3, type: "row", ts: "", data: done }).rows[1]).toBe(done);
    expect(applyBatchEvent(view, { seq: 4, type: "row", ts: "", data: row({ row: 9 }) })).toBe(
      view,
    );
    const later = { ...view, rows: [row({ row: 1, attempt: 2 })] };
    expect(
      applyBatchEvent(later, { seq: 5, type: "row", ts: "", data: row({ row: 1, attempt: 1 }) }),
    ).toBe(later);
  });
  it("appends a page in row order, keeping loaded rows", () => {
    const a = row({ row: 2, status: "failed" });
    expect(
      appendRows([a], [row({ row: 3 }), row({ row: 2 })]).map((r) => [r.row, r.status]),
    ).toEqual([
      [2, "failed"],
      [3, "succeeded"],
    ]);
  });
});

describe("batch display and controls", () => {
  it("shows state and rows done", () => {
    expect(doneRows(batch.counts)).toBe(3);
    expect(doneRows({ ...batch.counts, queued: 0, running: 0, cancelled: 9 })).toBe(3);
    expect(batchState(batch)).toEqual({ label: "Pausing", tone: "" });
    expect(batchState({ ...batch, control: "run" })).toEqual({ label: "Running", tone: "run" });
    expect(batchState({ ...batch, control: "run", status: "done" }).label).toBe("Done · 1 failed");
  });
  it("offers only the controls the contract allows", () => {
    const running = { ...batch, control: "run" as const };
    expect([canPause(running), canResume(running), canStop(running)]).toEqual([true, false, true]);
    expect([canPause(batch), canResume(batch)]).toEqual([false, true]);
    const stopped = { ...batch, status: "stopped" as const, control: "stop" as const };
    expect([canResume(stopped), canStop(stopped)]).toEqual([false, false]);
    expect(canRetry(running, row({ status: "failed" }))).toBe(true);
    expect(canRetry(stopped, row({ status: "failed" }))).toBe(false);
  });
  it("retries every failed row on every page, past a refused one", async () => {
    const running = { ...batch, control: "run" as const };
    const pages: Record<string, { rows: S<"BatchRow">[]; next_cursor: string | null }> = {
      "": { rows: [row({ row: 1, status: "failed" }), row({ row: 2 })], next_cursor: "c2" },
      c2: {
        rows: [row({ row: 3, status: "failed" }), row({ row: 4, status: "failed" })],
        next_cursor: null,
      },
    };
    const reply = (status: number, data?: unknown) =>
      Promise.resolve({
        data: status < 300 ? data : undefined,
        error: status < 300 ? undefined : { detail: "Too many open batches." },
        response: new Response(null, { status }),
      });
    vi.spyOn(api, "GET").mockImplementation(((
      _: string,
      init: { params: { query: { cursor?: string } } },
    ) => reply(200, pages[init.params.query.cursor ?? ""])) as never);
    const tried: string[] = [];
    vi.spyOn(api, "POST").mockImplementation(((
      _: string,
      init: { params: { path: { row: string } } },
    ) => {
      const n = init.params.path.row;
      tried.push(n);
      return n === "1" ? reply(409) : reply(200, row({ row: +n, status: "queued" }));
    }) as never);
    const got: number[] = [];
    const errors = await retryFailedRows(running, (r) => got.push(r.row));
    expect(tried).toEqual(["1", "3", "4"]);
    expect(got).toEqual([3, 4]);
    expect(errors).toEqual(["Row 1: Too many open batches."]);
    vi.restoreAllMocks();
  });
  it("words a row's state", () => {
    expect(rowState(row())).toBe("done · 1 take");
    expect(rowState(row({ status: "failed", error: "out of memory" }))).toBe(
      "failed · out of memory",
    );
  });
  it("summarises what differs between rows, text first", () => {
    const rows = [
      row({ row: 1, inputs: { "prompt.text": "a fox", "seed.seed": 1, "s.steps": 8 } }),
      row({ row: 2, inputs: { "prompt.text": "a heron", "seed.seed": 2, "s.steps": 8 } }),
    ];
    expect(inputSummary(rows)(rows[1])).toBe("a heron · seed 2");
    expect(inputSummary(rows.slice(0, 1))(rows[0])).toBe("a fox");
  });
});

describe("a still-to-clip pipeline's batch (API 1.13)", () => {
  const doc = stillToClip.pipeline;
  const want = batch9.rows.table;
  it("has both prompts as columns, matched from a CSV header", () => {
    const fs = pipelineFields(doc as unknown as S<"PipelineDoc">);
    const cells = [["prompt", "prompt_2"], ...want.rows];
    const t = buildTable(
      cells,
      cells[0].map((h) => matchColumn(h, fs)),
      fs,
    );
    expect(t.errors).toEqual([]);
    expect(t.table).toEqual(want);
  });
});
