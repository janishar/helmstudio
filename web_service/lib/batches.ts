// Batches (contracts/openapi.yaml `BatchCreate`, `Batch`, `BatchRow`, `BatchEvent`): building the rows in the
// browser (lines, a CSV table, a grid), the run maths, and folding the batch's events into what the screens show.
import { api, message, ok, type S } from "@/lib/api";
import { cellRef, imageFieldOf } from "@/lib/refs";
import { readValue, type Field } from "@/lib/workflow";

type Value = S<"ParamValue">;
type Batch = S<"Batch">;
type Row = S<"BatchRow">;

/** A batch has 1 to 500 rows (after seed repeats). */
export const MAX_ROWS = 500;
/** A grid has at most this many axes. */
export const MAX_AXES = 2;
/** Seeds per row: 1 to this. */
export const MAX_SEEDS = 8;

/** A row's problem, by input row (a line, a table row or a grid combination, from 1). */
export type RowError = { row: number; text: string };

/** The params a whole row can set: every field but the seed (seeds per row set it). */
export const rowFields = (fs: Field[]) => fs.filter((f) => f.param.type !== "seed");
/** The params pasted lines can set: one value of text per line, or one take id per line for an image param or a
 * rows param whose item is one Image (gallery "Use in…" with several takes). */
export const lineFields = (fs: Field[]) =>
  fs.filter(
    (f) =>
      ["text", "string", "enum", "image"].includes(f.param.type) ||
      (imageFieldOf(f.param) && Object.keys(f.param.item ?? {}).length === 1),
  );

/** Pasted lines as values: blank lines are dropped (the contract asks clients to). */
export const lineValues = (text: string) =>
  text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);

/** A text cell as param f's value: blank is null (the input stays unset for that row), a switch takes
 * true/false/yes/no/1/0, the rest is checked as the form checks it. */
export function cellValue(f: Field, cell: string): { value: Value } | { error: string } {
  const t = cell.trim();
  if (t === "") return { value: null };
  if (f.param.type === "image" || f.param.type === "rows") return cellRef(f, t);
  if (f.param.type === "bool") {
    const b = { true: true, yes: true, "1": true, false: false, no: false, "0": false }[
      t.toLowerCase()
    ];
    return b === undefined ? { error: "must be true or false" } : { value: b };
  }
  return readValue(f.param, f.param.type === "text" || f.param.type === "string" ? cell : t);
}

/** Values typed one per line for param f, and the errors by line (from 1). */
export function typedLines(f: Field, text: string) {
  const values: Value[] = [];
  const errors: RowError[] = [];
  lineValues(text).forEach((line, i) => {
    const r = cellValue(f, line);
    if ("error" in r) errors.push({ row: i + 1, text: `${label(f)} ${r.error}` });
    else values.push(r.value);
  });
  return { values, errors };
}

/** CSV (or tab-separated text pasted from a spreadsheet) as rows of cells: quoted cells may hold the separator,
 * newlines and "" for a quote; blank lines are dropped. */
export function parseCsv(text: string): string[][] {
  const first = text.split(/\r?\n/, 1)[0] ?? "";
  const sep = first.includes("\t") && !first.includes(",") ? "\t" : ",";
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  const endRow = () => {
    row.push(cell);
    if (row.some((c) => c.trim() !== "")) rows.push(row);
    row = [];
    cell = "";
  };
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') quoted = false;
      else cell += c;
    } else if (c === '"' && cell === "") quoted = true;
    else if (c === sep) {
      row.push(cell);
      cell = "";
    } else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      endRow();
    } else cell += c;
  }
  if (cell !== "" || row.length) endRow();
  return rows;
}

/** The field a CSV header names: its key (`prompt.text`), else its param name or label (any case); "" for none. */
export function matchColumn(header: string, fs: Field[]): string {
  const h = header.trim().toLowerCase();
  const by = (get: (f: Field) => string | undefined) =>
    fs.find((f) => get(f)?.toLowerCase() === h)?.key;
  return by((f) => f.key) ?? by((f) => f.param.name) ?? by((f) => f.param.label) ?? "";
}

/** A parsed CSV (header row first) as the contract's table: `columns` maps each CSV column to a field key ("" leaves
 * it out). A short line's missing cells are blank. Errors by data row (from 1). */
export function buildTable(cells: string[][], columns: string[], fs: Field[]) {
  const used = columns
    .map((key, i) => ({ i, f: fs.find((f) => f.key === key) }))
    .filter((c): c is { i: number; f: Field } => !!c.f);
  const errors: RowError[] = [];
  const rows = cells.slice(1).map((line, r) =>
    used.map(({ i, f }) => {
      const v = cellValue(f, line[i] ?? "");
      if ("value" in v) return v.value;
      errors.push({ row: r + 1, text: `${label(f)} ${v.error}` });
      return null;
    }),
  );
  if (!used.length) errors.push({ row: 0, text: "Map at least one column to a setting." });
  return { table: { columns: used.map((c) => c.f.key), rows }, errors };
}

/** How many rows a grid makes: the product of its axes' sizes. */
export const gridSize = (axes: { values: unknown[] }[]) =>
  axes.length ? axes.reduce((n, a) => n * a.values.length, 1) : 0;

/** "12 rows × 2 seeds = 24 runs" (one run per row and seed); "12 rows = 12 runs" for a workflow without a seed
 * (`seeds` null). A pipeline counts rows (seed repeats are rows): its runs per row depend on its steps and loops. */
export function summary(rows: number, seeds: number | null, pipeline = false) {
  const s = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
  // a pipeline row makes as many runs as its steps, seeds and loops take
  if (pipeline)
    return seeds === null
      ? s(rows, "row")
      : `${s(rows, "row")} × ${seeds} = ${s(rows * seeds, "row")}`;
  if (seeds === null) return `${s(rows, "row")} = ${s(rows, "run")}`;
  return `${s(rows, "row")} × ${s(seeds, "seed")} = ${s(rows * seeds, "run")}`;
}

/** Why the batch can't be submitted for its size, or "". */
export const sizeError = (rows: number, seeds: number) =>
  rows * seeds > MAX_ROWS
    ? `A batch has 1 to ${MAX_ROWS} runs; this one has ${rows * seeds}.`
    : rows === 0
      ? "Add at least one row."
      : "";

/** A Submit 422 that names its input row ("row 3: echo.steps: must be …") as that row and the rest; else null. */
export function rowError(detail: string): RowError | null {
  const m = /^row (\d+): ([\s\S]*)$/.exec(detail);
  return m ? { row: Number(m[1]), text: m[2] } : null;
}

const label = (f: Field) => f.param.label ?? f.param.name;

/** A batch and the rows loaded so far, in row order. */
export type BatchView = { batch: Batch; rows: Row[] };

/** The view after one event: a `batch` event is the whole batch; a `row` event replaces that row if it is loaded
 * (a later page brings the others), unless the row already holds a later attempt; a new row right after every row
 * loaded (one the director added) is appended. `chat` events are the chat's (lib/chat). */
export function applyBatchEvent(v: BatchView, e: S<"BatchEvent">): BatchView {
  if (e.type === "batch") return e.data.id === v.batch.id ? { ...v, batch: e.data } : v;
  if (e.type !== "row") return v;
  const i = v.rows.findIndex((r) => r.row === e.data.row);
  if (i < 0 && e.data.row === v.rows.length + 1 && v.rows.at(-1)?.row === v.rows.length)
    return { ...v, rows: [...v.rows, e.data] };
  if (i < 0 || v.rows[i].attempt > e.data.attempt) return v;
  const rows = v.rows.slice();
  rows[i] = e.data;
  return { ...v, rows };
}

/** A page of rows appended; a row already loaded keeps its (event-fed) state. */
export function appendRows(rows: Row[], page: Row[]): Row[] {
  const have = new Set(rows.map((r) => r.row));
  return [...rows, ...page.filter((r) => !have.has(r.row))].sort((a, b) => a.row - b.row);
}

/** Rows that ran to an end (succeeded or failed); cancelled rows are not done, so a stopped batch reads 1 / 4. */
export const doneRows = (c: S<"BatchCounts">) => c.succeeded + c.failed;

/** The batch's state chip: its words and tone (the mock's .st classes: run, ok, bad, or plain). */
export function batchState(b: Batch): { label: string; tone: "" | "run" | "ok" | "bad" | "wait" } {
  if (b.control === "stop" && !["done", "failed", "stopped"].includes(b.status))
    return { label: "Stopping", tone: "" };
  if (b.control === "pause" && ["running", "queued", "waiting"].includes(b.status))
    return { label: "Pausing", tone: "" };
  switch (b.status) {
    case "queued":
      return { label: "Queued", tone: "" };
    case "running":
      return { label: "Running", tone: "run" };
    case "waiting":
      return { label: `Waiting for your pick · ${b.counts.waiting ?? 0}`, tone: "wait" };
    case "paused":
      return { label: "Paused", tone: "" };
    case "done":
      return {
        label: b.counts.failed ? `Done · ${b.counts.failed} failed` : "Done",
        tone: "ok",
      };
    case "failed":
      return { label: "Failed", tone: "bad" };
    case "stopped":
      return { label: "Stopped", tone: "" };
    default:
      return { label: String(b.status), tone: "" };
  }
}

const ended = (b: Batch) => ["done", "failed", "stopped"].includes(b.status);
/** Which controls the contract allows now (else 409). */
export const canPause = (b: Batch) =>
  ["queued", "running", "waiting"].includes(b.status) && b.control === "run";
export const canResume = (b: Batch) =>
  (b.status === "paused" || b.control === "pause") && b.control !== "stop";
export const canStop = (b: Batch) => !ended(b) && b.control !== "stop";
export const canRetry = (b: Batch, r: Row) =>
  r.status === "failed" && b.status !== "failed" && b.status !== "stopped" && b.control !== "stop";

/** Retry failed: a retry for each row `canRetry` allows, page by page; a row that is refused (a 409: the open-batch
 * cap, a row already queued) does not stop the rest. `onRow` gets each retried row; the answer is the refusals. */
export async function retryFailedRows(b: Batch, onRow?: (r: Row) => void): Promise<string[]> {
  const errors: string[] = [];
  let cursor: string | null | undefined;
  do {
    const p = await ok(
      api.GET("/v1/studio/batches/{batch_id}/rows", {
        params: { path: { batch_id: b.id }, query: { limit: 200, ...(cursor ? { cursor } : {}) } },
      }),
    );
    for (const r of p.rows.filter((x) => canRetry(b, x)))
      try {
        const row = await ok(
          api.POST("/v1/studio/batches/{batch_id}/rows/{row}/retry", {
            params: { path: { batch_id: b.id, row: String(r.row) } },
          }),
        );
        onRow?.(row);
      } catch (e) {
        errors.push(`Row ${r.row}: ${message(e)}`);
      }
    cursor = p.next_cursor;
  } while (cursor);
  return errors;
}

/** A row's state as the run table shows it: "done · 4 takes", "failed · <error>", "queued". */
export function rowState(r: Row): string {
  if (r.status === "succeeded")
    return `done · ${r.take_ids.length} take${r.take_ids.length === 1 ? "" : "s"}`;
  if (r.status === "failed") return r.error ? `failed · ${r.error}` : "failed";
  return r.status;
}

/** How to summarise the rows' inputs: the inputs that differ between the rows loaded (each row's own values and
 * seed), text values as they are and others as "steps 20"; when nothing differs, the row's text values. */
export function inputSummary(rows: Row[]): (r: Row) => string {
  const keys = new Set(rows.flatMap((r) => Object.keys(r.inputs)));
  const varying = [...keys].filter((k) =>
    rows.some((r) => JSON.stringify(r.inputs[k]) !== JSON.stringify(rows[0].inputs[k])),
  );
  const shown = (r: Row, k: string) => {
    const v = r.inputs[k];
    if (v == null) return "";
    const n = Array.isArray(v) ? v.length : 1; // an image, or a list of them
    if (typeof v === "object") return `${k.split(".").pop()} ×${n}`;
    return typeof v === "string" ? v : `${k.split(".").pop()} ${v}`;
  };
  return (r) => {
    const ks = varying.length ? varying : Object.keys(r.inputs);
    // text first (the prompt), then the rest in key order
    const parts = ks
      .filter((k) => varying.length || typeof r.inputs[k] === "string")
      .sort((a, b) => +(typeof r.inputs[b] === "string") - +(typeof r.inputs[a] === "string"))
      .map((k) => shown(r, k))
      .filter(Boolean);
    return parts.join(" · ");
  };
}
