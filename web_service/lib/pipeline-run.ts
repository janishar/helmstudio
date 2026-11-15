// A pipeline batch as the screens show it (contracts/README.md "Batches of a pipeline", "Execution view"): the run
// table's columns and cells (loop attempts stacked), and one row's execution (the graph as run, the step list).
import type { S } from "@/lib/api";
import { clock } from "@/lib/runs";
import { nodeLabel, order, stepNumbers, type PDoc } from "@/lib/pipeline";

type Row = S<"BatchRow">;
type Cell = S<"BatchCell">;
type Attempt = S<"BatchCellAttempt">;
type Exec = S<"RowExecution">;
type Batch = S<"Batch">;

/** A run table column: one per step and gate, in row order. */
export type Column = { node: string; kind: "step" | "gate"; title: string };

/** The columns: the pipeline's steps and gates in the order a row meets them ("Step 1 · Qwen Image", the gate's
 * title), then any node a row reached that the document no longer has (cells name it). Without the document, the
 * cells' nodes in first-reached order. */
export function runColumns(
  doc: PDoc | null,
  rows: Row[],
  titles: Record<string, string> = {},
): Column[] {
  const out: Column[] = [];
  if (doc) {
    const n = stepNumbers(doc);
    for (const id of order(doc)) {
      const k = doc.nodes[id].kind;
      if (k === "step")
        out.push({ node: id, kind: k, title: `Step ${n[id]} · ${nodeLabel(doc, id, titles)}` });
      if (k === "gate") out.push({ node: id, kind: k, title: nodeLabel(doc, id, titles) });
    }
  }
  for (const r of rows)
    for (const c of r.cells ?? [])
      if (c.kind !== "output" && !out.some((x) => x.node === c.node))
        out.push({ node: c.node, kind: c.kind, title: c.node });
  return out;
}

/** One attempt of a cell as a line of the run table. Tones: ok (picked), run (running), warn (failed, rejected),
 * wait (waiting for a pick), plain. */
export type CellLine = {
  visit: number;
  text: string;
  tone: "" | "ok" | "run" | "warn" | "wait";
  thumb: string | null;
  /** "seed 42" or "seeds 42–45"; "" when it has none */
  seed: string;
  /** the runs' time, "31 s" or "1:02"; "" until one finished */
  time: string;
  takes: string[];
  error: string | null;
};

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;

/** A cell's attempts, oldest first. */
export function cellLines(cell: Cell | undefined): CellLine[] {
  if (!cell) return [];
  return cell.attempts.map((a) => lineOf(cell, a));
}

function lineOf(cell: Cell, a: Attempt): CellLine {
  const seeds = a.seeds ?? [];
  const base = {
    visit: a.visit,
    thumb: a.thumb_url ?? null,
    seed: !seeds.length
      ? ""
      : seeds.length === 1
        ? `seed ${seeds[0]}`
        : `seeds ${seeds[0]}–${seeds[seeds.length - 1]}`,
    time: a.seconds == null ? "" : a.seconds < 60 ? `${Math.round(a.seconds)} s` : clock(a.seconds),
    takes: a.take_ids,
    error: a.error,
  };
  const n = a.take_ids.length;
  switch (a.status) {
    case "succeeded":
      return {
        ...base,
        text: cell.kind === "output" ? plural(n, "take") : `done · ${plural(n, "take")}`,
        tone: "",
      };
    case "running":
      return { ...base, text: "running", tone: "run" };
    case "queued":
      return { ...base, text: "queued", tone: "" };
    case "failed":
      return { ...base, text: a.error ? `failed · ${a.error}` : "failed", tone: "warn" };
    case "cancelled":
      return { ...base, text: "cancelled", tone: "" };
    case "waiting":
      return { ...base, takes: a.candidates ?? [], text: "waiting for your pick", tone: "wait" };
    case "picked":
      return { ...base, text: n === 1 ? "picked 1" : `picked ${n}`, tone: "ok" };
    case "rejected":
      return { ...base, takes: a.candidates ?? [], text: "rejected all", tone: "warn" };
    default:
      return { ...base, text: String(a.status), tone: "" };
  }
}

/** A cell the row has not reached: skipped once the row ended, else nothing yet. */
export const unreached = (r: Row) =>
  ["failed", "cancelled", "succeeded"].includes(r.status) ? "skipped" : "—";

/** The takes the row ended with (its output's), and their first thumbnail. */
export function rowTakes(r: Row): { ids: string[]; thumb: string | null } {
  const out = r.cells?.find((c) => c.kind === "output")?.attempts.at(-1);
  return out
    ? { ids: out.take_ids, thumb: out.thumb_url ?? null }
    : { ids: r.take_ids, thumb: r.thumb_url };
}

/** The header's counts: "4 done · 1 failed · 3 waiting for a pick · …" (zero counts left out). */
export function countFacts(c: S<"BatchCounts">): string[] {
  return [
    `${c.succeeded} done`,
    c.failed ? `${c.failed} failed` : "",
    c.waiting ? `${c.waiting} waiting for a pick` : "",
    c.running ? `${c.running} running` : "",
    c.queued ? `${c.queued} queued` : "",
    c.cancelled ? `${c.cancelled} cancelled` : "",
  ].filter(Boolean);
}

/** Whether picks can be sent now (contracts: 409 on a stopped or failed batch, or while stopping). */
export const canPick = (b: Batch) =>
  !!b.counts.waiting && b.status !== "stopped" && b.status !== "failed" && b.control !== "stop";

// ---------------------------------------------------------------- the execution view

/** One line of the step list: "Step 1 · submit", what happened, the state it changed, done / resumed / now. */
export type StepLine = {
  step: number;
  node: string;
  label: string;
  what: string;
  changed: string;
  status: "done" | "resumed" | "now";
  error: string | null;
};

/** The step list of an execution: each graph step named by its pipeline node (and part, for a step's submit and
 * wait or a gate's route). */
export function stepLines(x: Exec, titles: Record<string, string> = {}): StepLine[] {
  const nums = stepNumbers(x.pipeline);
  const name = (id: string) =>
    x.pipeline.nodes[id]?.kind === "step"
      ? `Step ${nums[id]} · ${nodeLabel(x.pipeline, id, titles)}`
      : nodeLabel(x.pipeline, id, titles);
  return x.steps.map((s) => ({
    step: s.step,
    node: s.node,
    label: ["submit", "wait", "route"].includes(s.part)
      ? `${name(s.node)} · ${s.part}`
      : name(s.node),
    what: s.what,
    changed: s.changed.length ? s.changed.join(", ") : "—",
    status: s.status === "finished" ? "done" : s.status === "resumed" ? "resumed" : "now",
    error: s.error,
  }));
}

/** The graph as run: per node whether the row passed it, its visits and last status, and whether it is the node
 * the row is at now; per edge how many times it was taken. */
export function asRun(x: Exec) {
  const nodes: Record<string, { visits: number; status: string; current: boolean }> = {};
  for (const [id, n] of Object.entries(x.nodes))
    nodes[id] = { visits: n.visits, status: n.status, current: x.current === id };
  if (x.current && !nodes[x.current])
    nodes[x.current] = { visits: 1, status: "running", current: true };
  const taken: Record<string, number> = {};
  for (const e of x.edges)
    taken[`${e.from}>${e.to}>${e.via}`] = (taken[`${e.from}>${e.to}>${e.via}`] ?? 0) + e.count;
  return {
    nodes,
    /** times the edge from → to was taken (`via`: next, pass, fail, then) */
    taken: (from: string, to: string, via: string) => taken[`${from}>${to}>${via}`] ?? 0,
    loops: x.loops,
  };
}

/** What a step's details show: a step's runs and takes; a gate's candidates (its step's takes from the visit
 * before) and, after its route, what was picked (none: rejected all). */
export function stepDetail(x: Exec, step: number) {
  const s = x.steps.find((t) => t.step === step);
  if (!s) return null;
  const n = x.pipeline.nodes[s.node];
  if (n?.kind !== "gate")
    return { kind: n?.kind ?? "step", step: s, runs: s.run_ids, takes: s.take_ids };
  const before = x.steps
    .filter((t) => t.step < step && t.node === n.takes && t.part === "wait")
    .at(-1);
  const route = x.steps.find(
    (t) => t.step >= step && t.node === s.node && t.part === "route" && t.visit === s.visit,
  );
  return {
    kind: "gate" as const,
    step: s,
    candidates: before?.take_ids ?? [],
    picked: route ? route.take_ids : null,
  };
}

/** Whether "Retry row from here" may be offered for a step (the server has the last word: 409 / 422): the current
 * attempt is shown, the row is not running or queued, and the state before the step can be read (step 1 needs
 * none). */
export function canRetryFrom(x: Exec, step: number, b: Batch): boolean {
  if (
    x.attempt !== x.attempts ||
    !["failed", "succeeded", "cancelled", "waiting"].includes(x.status)
  )
    return false;
  if (!["queued", "running", "waiting", "paused"].includes(b.status) || b.control === "stop")
    return false;
  if (step === 1) return true;
  return !!x.steps.find((s) => s.step === step - 1)?.has_state;
}

// ---------------------------------------------------------------- the review grid

/** A candidate clicked: on or off; at `keep` 1 a click replaces the pick, else a pick past `keep` is refused. */
export function togglePick(picked: string[], id: string, keep: number): string[] {
  if (picked.includes(id)) return picked.filter((x) => x !== id);
  if (keep === 1) return [id];
  return picked.length >= keep ? picked : [...picked, id];
}

/** The candidate focused after an arrow key in a grid of `n` with `cols` columns (it stops at the edges). */
export function gridMove(i: number, key: string, n: number, cols: number): number {
  const to =
    key === "ArrowRight"
      ? i + 1
      : key === "ArrowLeft"
        ? i - 1
        : key === "ArrowDown"
          ? i + cols
          : key === "ArrowUp"
            ? i - cols
            : key === "Home"
              ? 0
              : key === "End"
                ? n - 1
                : i;
  return to < 0 || to >= n ? i : to;
}

/** What "none" does for a parked row: try again (with the turns left), go on to the cap's then, or fail the row. */
export function rejectLabel(
  item: Pick<S<"BatchReviewItem">, "on_reject" | "rejects_left">,
): string {
  if (item.on_reject === "retry")
    return `None, try again${item.rejects_left ? ` (${item.rejects_left} left)` : ""}`;
  return item.on_reject === "then" ? "None, move on" : "None, fail the row";
}
