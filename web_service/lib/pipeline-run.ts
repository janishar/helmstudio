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

type Kind = Cell["kind"];
/** A run table column: one per step, gate, judge, write and decide, in row order. */
export type Column = { node: string; kind: Kind; title: string };
/** the kinds with a column (a memory node and the output have none: the takes column is the output's) */
const COLUMN_KINDS = new Set<Kind>(["step", "gate", "judge", "write", "decide", "agent", "custom"]);

/** The columns: the pipeline's steps and gates in the order a row meets them ("Step 1 · Qwen Image", the gate's
 * title), then any node a row reached that the document no longer has (cells name it). Without the document, the
 * cells' nodes in first-reached order. */
export function runColumns(
  doc: PDoc | null,
  rows: Row[],
  titles: Record<string, string> = {},
  /** workflow id → what it makes: a step's column is then named by it ("Step 1 · images") */
  media: Record<string, string | undefined> = {},
): Column[] {
  const out: Column[] = [];
  if (doc) {
    const n = stepNumbers(doc);
    for (const id of order(doc)) {
      const node = doc.nodes[id];
      const k = node.kind;
      const made = node.kind === "step" ? MEDIA_WORDS[media[node.workflow] ?? ""] : undefined;
      if (k === "step")
        out.push({
          node: id,
          kind: k,
          title: `Step ${n[id]} · ${made ?? nodeLabel(doc, id, titles)}`,
        });
      else if (COLUMN_KINDS.has(k as Kind))
        out.push({ node: id, kind: k as Kind, title: nodeLabel(doc, id, titles) });
    }
  }
  for (const r of rows)
    for (const c of r.cells ?? [])
      if (COLUMN_KINDS.has(c.kind) && !out.some((x) => x.node === c.node))
        out.push({ node: c.node, kind: c.kind, title: c.node });
  return out;
}

/** A workflow's `media` as a run table column names it. */
export const MEDIA_WORDS: Record<string, string> = {
  image: "images",
  video: "video",
  text: "text",
};

/** One attempt of a cell as a line of the run table. Tones: ok (picked), run (running), warn (failed, rejected),
 * wait (waiting for a pick), plain. */
export type CellLine = {
  visit: number;
  /** a fanned-out step's branch (the item index, from 0); a line summing the branches not shown has none */
  branch?: number;
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
/** A cell as its row shows it: a row that ended while parked (cancelled, stopped) keeps its gate's or judge's
 * attempt `waiting`, which was never picked and now never will be: cancelled. */
export function settledCell(
  cell: Cell | undefined,
  row: S<"BatchRow">["status"],
): Cell | undefined {
  if (!cell || row === "waiting" || !cell.attempts.some((a) => a.status === "waiting")) return cell;
  return {
    ...cell,
    attempts: cell.attempts.map((a) =>
      a.status === "waiting" ? { ...a, status: "cancelled" as const } : a,
    ),
  };
}

/** Branches shown per visit of a fanned-out step before the rest are summed up in one line. */
export const SHOWN_BRANCHES = 3;

export function cellLines(cell: Cell | undefined): CellLine[] {
  if (!cell) return [];
  const out: CellLine[] = [];
  const visits = [...new Set(cell.attempts.map((a) => a.visit))];
  for (const v of visits) {
    const as = cell.attempts.filter((a) => a.visit === v);
    if (as.every((a) => a.branch === undefined)) {
      out.push(...as.map((a) => lineOf(cell, a)));
      continue;
    }
    // a fanned-out step: a line per shot, the first few, then the rest summed up
    const lines = as
      .toSorted((a, b) => (a.branch ?? 0) - (b.branch ?? 0))
      .map((a) => {
        const l = lineOf(cell, a);
        return { ...l, branch: a.branch, text: `shot ${(a.branch ?? 0) + 1} · ${l.text}` };
      });
    out.push(...lines.slice(0, SHOWN_BRANCHES));
    const rest = as.toSorted((a, b) => (a.branch ?? 0) - (b.branch ?? 0)).slice(SHOWN_BRANCHES);
    if (rest.length) {
      const done = rest.filter((a) => a.status === "succeeded").length;
      const failed = rest.filter((a) => a.status === "failed").length;
      out.push({
        visit: v,
        text: `+ ${plural(rest.length, "shot")} · ${done} done${failed ? ` · ${failed} failed` : ""}`,
        tone: failed ? "warn" : "",
        thumb: null,
        seed: "",
        time: "",
        takes: rest.flatMap((a) => a.take_ids),
        error: null,
      });
    }
  }
  return out;
}

/** A cell as the run table's one line (docs/mock/PipelineGraph: "done · 4 takes", "done · 2 attempts", "failed · did
 * not fit in memory"): its last visit, a fanned-out one summed up, and the takes of that visit. */
export function cellSummary(cell: Cell): Pick<CellLine, "text" | "tone" | "takes" | "error"> {
  const lines = cellLines(cell);
  const last = lines.at(-1);
  if (!last) return { text: "—", tone: "", takes: [], error: null };
  const visits = new Set(lines.map((l) => l.visit)).size;
  const shots = cell.attempts.filter((a) => a.visit === last.visit);
  const takes = shots.flatMap((a) => a.take_ids);
  if (shots.length > 1) {
    const done = shots.filter((a) => a.status === "succeeded").length;
    const failed = shots.filter((a) => a.status === "failed").length;
    const running = shots.some((a) => a.status === "running");
    return {
      text: `${plural(shots.length, "shot")} · ${done} done${failed ? ` · ${failed} failed` : ""}`,
      tone: running ? "run" : failed ? "warn" : "",
      takes,
      error: null,
    };
  }
  const text =
    last.text.startsWith("done") && visits > 1
      ? `done · ${visits} attempts`
      : last.text === "done · 1 take" || last.text === "done · 0 takes"
        ? "done"
        : last.text;
  return { text, tone: last.tone, takes: last.takes, error: last.error };
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
  if (cell.kind === "judge") return { ...base, ...judgeLine(a) };
  if (cell.kind === "decide" && a.route)
    return { ...base, text: `${a.route.label} → ${a.route.to}`, tone: "" };
  if (cell.kind === "write" && a.status === "succeeded")
    return { ...base, text: "rewritten", tone: "" };
  if (cell.kind === "agent" && a.agent && a.status !== "running" && a.status !== "queued") {
    const g = a.agent;
    const parts = [
      plural(g.turns, "turn"),
      plural(g.tool_calls, "tool"),
      g.items ? plural(g.items, "shot") : "",
      g.route ? `→ ${g.route}` : "",
    ].filter(Boolean);
    return a.status === "failed"
      ? { ...base, text: a.error ? `failed · ${a.error}` : "failed", tone: "warn" }
      : { ...base, text: parts.join(" · "), tone: "" };
  }
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

/** A judge attempt in words: "picked take 3 · all checks pass", "failed · extra limb", "suggests take 2". */
export function judgeLine(a: Attempt): Pick<CellLine, "text" | "tone"> {
  const j = a.judged;
  if (a.status === "cancelled") return { text: "cancelled", tone: "" };
  if (a.status === "queued" || a.status === "running" || !j)
    return a.status === "failed"
      ? { text: a.error ? `failed · ${a.error}` : "failed", tone: "warn" }
      : {
          text: a.status === "running" ? "judging" : String(a.status),
          tone: a.status === "running" ? "run" : "",
        };
  const idx = (ids: string[]) => ids.map((t) => j.takes.findIndex((x) => x.take_id === t) + 1);
  const which = (ids: string[]) =>
    ids.length === 1 ? `take ${idx(ids)[0]}` : `${ids.length} of ${j.of}`;
  if (a.status === "waiting")
    return {
      text: j.suggested.length
        ? `suggests ${which(j.suggested)} · waiting for you`
        : "none passed · waiting for you",
      tone: "wait",
    };
  if (j.verdict === "fail" || a.status === "rejected")
    return { text: j.first_failed ? `failed · ${j.first_failed}` : "none passed", tone: "warn" };
  const kept = a.take_ids.length ? a.take_ids : j.suggested;
  // every check, nice ones too: `passes` is only the musts, and a kept take that missed a nice one is not "all"
  // (`checks` is absent in rows recorded before it: then the musts are all there is to go on)
  const clean = j.takes
    .filter((t) => kept.includes(t.take_id))
    .every((t) => t.passes && (t.checks ?? []).every(Boolean));
  return {
    text: `picked ${which(kept)}${clean ? " · all checks pass" : ""}`,
    tone: "ok",
  };
}

/** A judge's cell as the run table's one line (docs/mock/PipelineGraph: "picked take 3 · all checks pass",
 * "retried 1 of 2 · extra limb", "none passed · waiting for you", "gave up → Review"); its attempts open side by
 * side. */
export function judgeSummary(
  cell: Cell,
  name: (id: string) => string = (id) => id,
): Pick<CellLine, "text" | "tone"> {
  const last = cell.attempts.at(-1);
  if (!last) return { text: "—", tone: "" };
  const now = judgeLine(last);
  const loop = cell.loop;
  if (loop?.gave_up && last.status !== "waiting")
    return { text: `gave up → ${loop.then ? name(loop.then) : "the row ends"}`, tone: "warn" };
  if (!loop?.turns || now.tone === "wait" || now.tone === "run") return now;
  const failed = cell.attempts.filter((x) => x.judged?.verdict === "fail").at(-1)?.judged;
  return {
    text: `retried ${loop.turns} of ${loop.max}${failed?.first_failed ? ` · ${failed.first_failed}` : ""}`,
    tone: "warn",
  };
}

/** One criterion over a judge's takes (docs/mock/Execution's Verdict): "passed", "failed on all 4", "passed on 3". */
export function criterionLines(
  j: NonNullable<S<"Decision">["judge"]>,
): { criterion: string; level: "must" | "nice"; text: string; bad: boolean }[] {
  const n = j.takes.length;
  const all = [
    ...(j.criteria.must ?? []).map((c) => [c, "must"] as const),
    ...(j.criteria.nice ?? []).map((c) => [c, "nice"] as const),
  ];
  return all.map(([criterion, level]) => {
    const k = j.takes.filter((t) => t.checks.find((c) => c.criterion === criterion)?.pass).length;
    const text =
      k === n
        ? "passed"
        : k === 0
          ? n > 1
            ? `failed on all ${n}`
            : "failed"
          : level === "must"
            ? `failed on ${n - k}`
            : `passed on ${k}`;
    return { criterion, level, text, bad: level === "must" && k < n };
  });
}

/** A text before and after (a write's rewrite) as a word diff: runs of kept, removed and added text. */
export function wordDiff(
  before: string,
  after: string,
): { text: string; op: "same" | "del" | "add" }[] {
  const a = before.split(/(\s+)/).filter(Boolean);
  const b = after.split(/(\s+)/).filter(Boolean);
  // ponytail: O(n·m) LCS table; fine for a prompt (≤ a few hundred words)
  const L = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--)
      L[i][j] = a[i] === b[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  const out: { text: string; op: "same" | "del" | "add" }[] = [];
  const push = (text: string, op: "same" | "del" | "add") => {
    const last = out.at(-1);
    if (last?.op === op) last.text += text;
    else out.push({ text, op });
  };
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      push(a[i++], "same");
      j++;
    } else if (j < b.length && (i === a.length || L[i][j + 1] >= L[i + 1][j])) push(b[j++], "add");
    else push(a[i++], "del");
  }
  return out;
}

/** An agent visit's end in words (Decision.agent): "answered 14 items", "routed done", "stopped at its turns cap". */
export function agentEnd(a: NonNullable<S<"Decision">["agent"]>): string {
  if (a.stopped)
    return a.stopped === "remote_cap"
      ? "stopped: the batch's remote cap"
      : `stopped at its ${a.stopped.replace("_", " ")} cap`;
  if (a.route) return `routed ${a.route}${a.forced ? " (forced by the cap)" : ""}`;
  if (a.items.length) return `answered ${plural(a.items.length, "item")}`;
  return "answered";
}

/** Where a decision's model ran: "Remote · fakeai · fake-large", "Local server · ollama · qwen2.5:7b", else the ask
 * workflow's title. */
export function decisionModel(d: S<"Decision">): string {
  const s = d.source;
  if (s?.kind === "remote") return `Remote · ${s.provider} · ${s.model}`;
  if (s?.kind === "server") return `Local server · ${s.provider} · ${s.model}`;
  return d.workflow_title ?? d.workflow;
}

/** Submit's worst case: "at most 13 runs and 6 LLM calls per row (130 runs, 60 calls in all)", then a director's
 * chat and the rows agents may add (per batch). */
export function boundsLine(b: S<"PipelineBounds">, rows: number): string {
  const s = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
  const per = [s(b.runs, "run"), b.llm_calls ? s(b.llm_calls, "LLM call") : ""].filter(Boolean);
  const all =
    rows > 1
      ? ` (${[s(b.runs * rows, "run"), b.llm_calls ? s(b.llm_calls * rows, "call") : ""].filter(Boolean).join(", ")} in all)`
      : "";
  const tools = b.tool_calls ? `; agents make at most ${s(b.tool_calls, "tool call")} a row` : "";
  const chat = b.chat
    ? `; the director's chat asks a model at most ${s(b.chat.calls, "time")} in all`
    : "";
  const added = b.added_rows
    ? `; agents may add up to ${s(b.added_rows, "row")}, each with the same worst case`
    : "";
  return `at most ${per.join(" and ")} per row${all}${tools}${chat}${added}`;
}

/** Where a memory item came from, for the Memory panel. */
export function memorySource(
  m: S<"MemoryItem">,
  name: (node: string) => string = (x) => x,
): string {
  const s = m.source;
  if (s.kind === "user") return "added by you";
  const at = [
    s.row != null && `row ${s.row}`,
    s.visit != null && s.visit > 1 && `attempt ${s.visit}`,
  ]
    .filter(Boolean)
    .join(", ");
  const node = s.node ? name(s.node) : "";
  if (s.kind === "pick")
    return `your pick at ${node || "a judge"}${at ? ` · ${at}` : ""}${m.agreed === false ? " · you changed its pick" : m.agreed ? " · you agreed" : ""}`;
  return `written by ${node || "a memory node"}${at ? ` · ${at}` : ""}`;
}

/** The pick a review row opens with: the judge's suggestion (Suggest mode), else nothing. */
export const initialPick = (item: S<"BatchReviewItem">) =>
  item.kind === "judge" ? (item.suggested ?? []) : [];

/** A cell the row has not reached: skipped once the row ended, parked while it waits for a pick, else nothing yet. */
export const unreached = (r: Row) =>
  ["failed", "cancelled", "succeeded"].includes(r.status)
    ? "skipped"
    : r.status === "waiting"
      ? "parked"
      : "—";

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

/** Per step, the last batch's progress for its card (docs/mock/PipelineGraph: a bar, "5 / 12", running or done): the
 * rows whose latest visit of it succeeded, of the batch's rows; running while any row runs it. */
export function stepProgress(
  doc: PDoc,
  rows: Row[],
  total: number,
): Record<string, { done: number; total: number; state: "done" | "running" | "" }> {
  const out: Record<string, { done: number; total: number; state: "done" | "running" | "" }> = {};
  for (const [id, n] of Object.entries(doc.nodes)) {
    if (n.kind !== "step") continue;
    let done = 0;
    let running = false;
    for (const r of rows) {
      const c = r.cells?.find((x) => x.node === id);
      const last = c?.attempts.at(-1);
      if (!c || !last) continue;
      const visit = c.attempts.filter((a) => a.visit === last.visit);
      if (visit.every((a) => a.status === "succeeded")) done++;
      if (visit.some((a) => a.status === "running" || a.status === "queued")) running = true;
    }
    out[id] = { done, total, state: total && done >= total ? "done" : running ? "running" : "" };
  }
  return out;
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
  /** What happened's colour (docs/mock/Execution): warn (an error, a judge that failed), ok (a judge that passed),
   * now (where the row is) */
  tone: "warn" | "ok" | "now" | "";
};

/** The step list of an execution: each graph step named by its pipeline node ("Step 1", as the board: no workflow
 * title) and part, for a step's submit and wait or a gate's route. */
export function stepLines(x: Exec, titles: Record<string, string> = {}): StepLine[] {
  const nums = stepNumbers(x.pipeline);
  const name = (id: string) =>
    x.pipeline.nodes[id]?.kind === "step" ? `Step ${nums[id]}` : nodeLabel(x.pipeline, id, titles);
  const tone = (s: Exec["steps"][number]): StepLine["tone"] => {
    const v = s.decision?.judge?.verdict;
    if (s.error || v === "fail") return "warn";
    if (s.status === "current") return "now";
    return v === "pass" ? "ok" : "";
  };
  return x.steps.map((s) => ({
    step: s.step,
    node: s.node,
    label:
      (["submit", "wait", "route", "tools"].includes(s.part)
        ? `${name(s.node)} · ${s.part}`
        : name(s.node)) +
      (s.branch !== undefined ? ` · shot ${s.branch + 1}` : "") +
      (s.turn ? ` · turn ${s.turn}` : ""),
    what: s.what,
    changed: s.changed.length ? s.changed.join(", ") : "—",
    status: s.status === "finished" ? "done" : s.status === "resumed" ? "resumed" : "now",
    error: s.error,
    tone: tone(s),
  }));
}

/** A line of the step list and, for a judge's decision, the checkpoints folded under it. */
export type StepGroup = { line: StepLine; inner: StepLine[] };

/** The step list with one line per judge decision (owner's Q8): a judge visit's checkpoints (submit, wait, verdict
 * with the memory it read, gate, route) fold under one "Judge" line, named by the verdict, that opens on click; each
 * stays selectable, so Retry row from here still works from any of them. */
export function stepGroups(x: Exec, titles: Record<string, string> = {}): StepGroup[] {
  const lines = stepLines(x, titles);
  const groups: (StepGroup & { key: string })[] = [];
  x.steps.forEach((s, i) => {
    const l = lines[i];
    const judge = x.pipeline.nodes[s.node]?.kind === "judge";
    const key = judge ? `${s.node}:${s.visit}:${s.branch ?? ""}` : "";
    const read = s.memory?.read.length ?? 0;
    const inner = { ...l, label: s.part + (read ? ` · memory read ${read}` : "") };
    const last = groups.at(-1);
    if (key && last?.key === key) last.inner.push(inner);
    else groups.push({ key, line: l, inner: key ? [inner] : [] });
  });
  return groups.map(({ line, inner }) => {
    if (!inner.length) return { line, inner };
    const verdict = inner.find((l) => x.steps.find((s) => s.step === l.step)?.decision);
    const end = inner.at(-1)!;
    const changed = [
      ...new Set(inner.flatMap((l) => (l.changed === "—" ? [] : l.changed.split(", ")))),
    ];
    return {
      line: {
        ...line,
        step: (verdict ?? end).step,
        label: line.label.replace(/ · (submit|wait|route)(?= ·|$)/, ""),
        what: (verdict ?? end).what,
        changed: changed.join(", ") || "—",
        status: end.status,
        error: inner.find((l) => l.error)?.error ?? null,
        tone: end.status === "now" ? "now" : (verdict ?? end).tone,
      },
      inner,
    };
  });
}

/** Whether the execution view lays the graph out itself (owner's Q10): it draws the saved positions unless a node
 * has none, two cards overlap, or they spread wider or taller than the view shows at its smallest zoom. */
export function needsLayout(doc: PDoc, maxW = 3000, maxH = 900): boolean {
  const ps = Object.values(doc.nodes).map((n) => n.position);
  if (ps.some((p) => !p)) return true;
  const at = ps as { x: number; y: number }[];
  // ponytail: a card's nominal size (176 × 100); measured sizes would need the canvas mounted first
  if (
    at.some((a, i) =>
      at.slice(i + 1).some((b) => Math.abs(a.x - b.x) < 176 && Math.abs(a.y - b.y) < 100),
    )
  )
    return true;
  const xs = at.map((p) => p.x);
  const ys = at.map((p) => p.y);
  return Math.max(...xs) - Math.min(...xs) > maxW || Math.max(...ys) - Math.min(...ys) > maxH;
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
    /** times the edge from → to was taken (`via`: next, pass, fail, then; `*` any) */
    taken: (from: string, to: string, via: string) =>
      via === "*" // a decide's route: the contract's edge has no route label, so any edge from → to
        ? x.edges.filter((e) => e.from === from && e.to === to).reduce((n, e) => n + e.count, 0)
        : (taken[`${from}>${to}>${via}`] ?? 0),
    loops: x.loops,
  };
}

/** What a step's details show: a step's runs and takes; a gate's candidates (its step's takes from the visit
 * before) and, after its route, what was picked (none: rejected all). */
export function stepDetail(x: Exec, step: number) {
  const s = x.steps.find((t) => t.step === step);
  if (!s) return null;
  const n = x.pipeline.nodes[s.node];
  if (s.decision)
    return { kind: s.decision.kind, step: s, decision: s.decision, runs: s.run_ids, takes: [] };
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
