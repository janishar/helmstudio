// The pipeline screens' actions and summaries for the assistant (plan 18 §22.5, §22.6; contracts/tools/actions.json
// `pipeline_editor` and `batches`): the editor's lib/pipeline edits (checked by pipelineProblems after each, all of
// them taken back by one "Undo assistant changes"), the Studio view's Run step (a card) and pick (Undo), the batch
// controls (start, resume, retry, again as Batch cards with what leaves the machine, stop as a card, pause a line),
// a gate's pick (a card) and the judge test (a card with its egress line). Every card's Yes calls the screen's own
// function at the click.
import { api, ok, okOr, type S } from "@/lib/api";
import { done, failed, type Asked, type Outcome } from "@/lib/assistant";
import { AWAIT_CAP_MS, until, type ScreenSpec } from "@/components/assistant/tools";
import { testSummary } from "@/components/pipelines/judge-test";
import { egressLines } from "@/lib/egress";
import { boundsLine } from "@/lib/pipeline-run";
import {
  KIND_NAMES,
  addPipelineNode,
  chooseWorkflow,
  connectNodes,
  disconnectNodes,
  nodeLabel,
  pipelineBounds,
  pipelineProblems,
  removePipelineNodes,
  setNode,
  stepNumbers,
  type PDoc,
  type PNode,
  type RuleEnv,
  type Via,
} from "@/lib/pipeline";
import { canPause, canResume, canRetry, canStop, rowState, type BatchView } from "@/lib/batches";
import { reasonOf, type Chosen } from "@/lib/looks";
import type { Doc, Palette } from "@/lib/graph";

type Pipeline = S<"Pipeline">;
type Batch = S<"Batch">;
type Kind = PNode["kind"];

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const GPU = "uses the GPU";

// ---------------------------------------------------------------- what leaves the machine

/** A Batch card's tags and egress line (board: "What leaves the machine: Decide sends its question, routes and the
 * Judge's reasons to DeepSeek; no thumbnails. Worst case 60 calls, 539,520 tokens."), from the pipeline's egress. */
export function egressNote(
  p: Pick<Pipeline, "nodes" | "edges" | "title" | "egress" | "workflow_titles"> | null | undefined,
  rows: number,
): { tags: string[]; note?: string } {
  const eg = p?.egress ?? [];
  if (!p || !eg.length) return { tags: [GPU] };
  const name = (id: string) => nodeLabel(p as PDoc, id, p.workflow_titles);
  const source = (src: string) =>
    src.startsWith("$")
      ? `the ${src.slice(1)} input`
      : `the ${name(src.split(".")[0])}'s ${src.split(".")[1]}`;
  const lines = egressLines(eg, Math.max(1, rows), source);
  const to = (i: number) => eg[i].provider ?? "the network";
  const said = lines
    .map((l, i) => `${name(l.node)} sends ${l.sends.replace(/;? ?no thumbnails$/, "")} to ${to(i)}`)
    .join("; ");
  const remote = eg.filter((e) => e.where === "remote");
  const calls = remote.reduce((x, e) => x + e.per_row.calls * rows + (e.chat?.calls ?? 0), 0);
  const tokens = remote.reduce((x, e) => x + e.per_row.tokens * rows + (e.chat?.tokens ?? 0), 0);
  const worst = calls
    ? ` Worst case ${plural(calls, "call")}, ${tokens.toLocaleString("en-US")} tokens.`
    : "";
  const providers = [...new Set(remote.map((e) => e.provider).filter((x): x is string => !!x))];
  return {
    tags: [GPU, ...providers.map((x) => `sends to ${x}`)],
    note: `What leaves the machine: ${said}; no thumbnails.${worst}`,
  };
}

const batchCard = (
  title: string,
  rows: [string, string][],
  egress: { tags: string[]; note?: string },
  go: string,
  fine: string,
  yes: () => Promise<Outcome>,
): Asked => ({
  card: { kind: "batch", title, rows, tags: egress.tags, note: egress.note, go, fine },
  yes,
});

const deleteCard = (
  title: string,
  goes: string,
  stays: string,
  fine: string,
  yes: () => Promise<Outcome>,
  go = "DELETE",
): Asked => ({
  card: {
    kind: "delete",
    title,
    rows: [
      ["Goes", goes],
      ["Stays", stays],
    ],
    tags: [],
    go,
    danger: true,
    fine,
  },
  yes,
});

// ---------------------------------------------------------------- the pipeline editor

/** What the editor already works out, handed to its tools each render. */
export type EditorCtx = {
  pipeline: Pipeline;
  /** the freshest document (several edits run before a render) */
  doc: () => PDoc;
  /** an assistant edit (lib/pipeline on the freshest document); returns the new document */
  edit: (f: (d: PDoc) => PDoc) => PDoc;
  wfDocs: Record<string, Doc>;
  palette: Palette;
  env: RuleEnv;
  titles: Record<string, string>;
  models: Record<string, string>;
  problems: S<"PipelineProblem">[];
  selected: string | null;
  select: (id: string | null) => void;
  view: "studio" | "graph";
  unsaved: () => boolean;
  defaultWorkflow?: string;
  custom: S<"CustomNode">[];
  /** saves the document now (a judge test reads the saved judge) */
  save: () => Promise<void>;
  newPipeline: (title: string) => Promise<Pipeline>;
  deletePipeline: () => Promise<void>;
  memory: S<"MemoryItem">[] | null;
  reloadMemory: () => Promise<void>;
  /** opens Add note prefilled; resolves on close with the new item's id, or null */
  addNote: (text: string) => Promise<string | null>;
};

const VIAS = new Set(["next", "fail", "then", "examples"]);

export function editorTools(c: EditorCtx): ScreenSpec {
  const label = (d: PDoc, id: string) => nodeLabel(d, id, c.titles);
  const problemsOf = (d: PDoc) =>
    pipelineProblems(d, c.wfDocs, c.palette, undefined, {}, undefined, c.env);
  const after = (d: PDoc, text: string, more: Record<string, unknown> = {}): Outcome => {
    const ps = problemsOf(d);
    return done(text, {
      result: {
        ...more,
        valid: !ps.length,
        problems: ps.slice(0, 12).map((p) => ({ node: p.node ?? null, message: p.message })),
      },
    });
  };
  /** A node's fields merged in (null drops one); its kind and position stay; a step's workflow rebinds its inputs. */
  const merged = (d: PDoc, id: string, fields: Record<string, unknown>): PDoc => {
    const n = d.nodes[id];
    const { kind: _k, position: _p, workflow, ...rest } = fields; // eslint-disable-line @typescript-eslint/no-unused-vars
    let next = d;
    if (n.kind === "step" && typeof workflow === "string" && workflow !== n.workflow)
      next = chooseWorkflow(d, id, workflow, c.wfDocs[workflow], c.palette);
    const m = { ...next.nodes[id] } as Record<string, unknown>;
    for (const [k, v] of Object.entries(rest))
      if (v === null) delete m[k];
      else m[k] = v;
    return setNode(next, id, m as PNode);
  };
  const doc = c.doc();
  const sel = c.selected && doc.nodes[c.selected] ? c.selected : null;
  return {
    screen: "pipeline_editor",
    sees: [
      `Pipeline · ${c.view === "studio" ? "Studio" : "Graph"}`,
      `${doc.title} · v${c.pipeline.version}`,
      ...(sel ? [label(doc, sel)] : []),
    ],
    ids: { pipeline_id: c.pipeline.id, node: sel ?? "" },
    examples: [
      { text: "Add a judge after step 1 that keeps the sharpest take", sub: "adds and wires it" },
      { text: "Fix whatever is red", sub: "reads the problems and fixes them" },
      { text: "Test the judge on my last 4 takes", sub: "a card first" },
    ],
    summary: () => {
      const d = c.doc();
      const nums = stepNumbers(d);
      return {
        view: c.view,
        pipeline: { id: c.pipeline.id, title: d.title, version: c.pipeline.version },
        nodes: Object.entries(d.nodes).map(([id, n]) => ({
          id,
          kind: n.kind,
          title: label(d, id),
          ...(nums[id] && { step: nums[id] }),
          ...(n.kind === "step" && { workflow: n.workflow }),
        })),
        edges: d.edges,
        selected: sel && { id: sel, node: d.nodes[sel] },
        problems: c.problems.map((p) => ({ node: p.node ?? null, message: p.message })),
        unsaved: c.unsaved(),
        memory: (c.memory ?? [])
          .slice(0, 20)
          .map((m) => ({ id: m.id, kind: m.kind, text: m.text.slice(0, 200) })),
      };
    },
    actions: {
      addNode: ({ kind, id, fields }) => {
        const k = String(kind) as Kind;
        if (!(k in KIND_NAMES))
          return failed(`Add · no node kind ${k}: ${Object.keys(KIND_NAMES).join(", ")}`);
        const d = c.doc();
        const want = id === undefined ? null : String(id);
        if (want && (d.nodes[want] || !/^[a-z][a-z0-9_]{0,63}$/.test(want)))
          return failed(`Add · ${want} is taken or not an id (a-z, 0-9, _)`);
        const f = (fields ?? {}) as Record<string, unknown>;
        const custom = k === "custom" ? c.custom.find((x) => x.name === f.node) : undefined;
        if (k === "custom" && !custom) return failed(`Add · no custom node ${String(f.node)}`);
        const ps = Object.values(d.nodes).flatMap((n) => (n.position ? [n.position] : []));
        const at = { x: Math.max(-204, ...ps.map((p) => p.x)) + 220, y: 40 };
        let nid = "";
        const next = c.edit((x) => {
          const r = addPipelineNode(x, k, at, {
            workflow: typeof f.workflow === "string" ? f.workflow : c.defaultWorkflow,
            selected: sel,
            custom,
          });
          let y = r.doc;
          nid = r.id;
          if (want && want !== r.id) {
            const { [r.id]: node, ...others } = y.nodes;
            y = { ...y, nodes: { ...others, [want]: node } };
            nid = want;
          }
          return Object.keys(f).length ? merged(y, nid, f) : y;
        });
        c.select(nid);
        return after(next, `Added ${label(next, nid)}`, { node: nid });
      },
      setNode: ({ id, fields }) => {
        const d = c.doc();
        const nid = String(id);
        if (!d.nodes[nid]) return failed(`Change · no node ${nid}`);
        const f = fields as Record<string, unknown>;
        if (f.kind !== undefined && f.kind !== d.nodes[nid].kind)
          return failed(`Change · a node keeps its kind: remove it and add a ${String(f.kind)}`);
        const next = c.edit((x) => merged(x, nid, f));
        return after(next, `Set ${label(next, nid)}: ${Object.keys(f).join(", ")}`);
      },
      removeNode: ({ id }) => {
        const d = c.doc();
        const nid = String(id);
        if (!d.nodes[nid]) return failed(`Remove · no node ${nid}`);
        const name = label(d, nid);
        const next = c.edit((x) => removePipelineNodes(x, [nid]));
        if (sel === nid) c.select(null);
        return after(next, `Removed ${name}`);
      },
      connect: ({ from, to, branch }) => {
        const d = c.doc();
        const [a, b] = [String(from), String(to)];
        if (!d.nodes[a] || !d.nodes[b]) return failed(`Connect · no node ${d.nodes[a] ? b : a}`);
        const via = (
          branch === undefined ? "next" : VIAS.has(String(branch)) ? branch : `r:${String(branch)}`
        ) as Via;
        const next = c.edit((x) => connectNodes(x, a, via, b));
        if (next === d)
          return failed(
            `Connect · ${label(d, a)} can't go to ${label(d, b)}${branch ? ` on ${String(branch)}` : ""}`,
          );
        return after(
          next,
          `Connected ${label(next, a)} → ${label(next, b)}${branch ? ` (${String(branch)})` : ""}`,
        );
      },
      disconnect: ({ from, to }) => {
        const d = c.doc();
        const [a, b] = [String(from), String(to)];
        const e = d.edges.find((x) => x[0] === a)?.[1];
        const via: Via | null =
          e === undefined
            ? null
            : typeof e === "string"
              ? e === b
                ? "next"
                : null
              : "routes" in e
                ? Object.values(e.routes).includes(b)
                  ? "next"
                  : null
                : e.pass === b
                  ? "next"
                  : e.fail === b
                    ? "fail"
                    : e.then === b
                      ? "then"
                      : null;
        if (!via) return failed(`Disconnect · ${a} does not go to ${b}`);
        const next = c.edit((x) => disconnectNodes(x, a, via));
        return after(next, `Disconnected ${label(next, a)} → ${label(next, b)}`);
      },
      newPipeline: async ({ title }) => {
        const p = await c.newPipeline(
          String(title ?? "Untitled pipeline")
            .trim()
            .slice(0, 80),
        );
        return done(`Opened new pipeline · ${p.title}`, { result: { pipeline_id: p.id } });
      },
      renamePipeline: ({ title }) => {
        const t = String(title).trim().slice(0, 80);
        if (!t) return failed("Rename · the title is empty");
        c.edit((x) => ({ ...x, title: t }));
        return done(`Renamed pipeline: ${t}`);
      },
      deletePipeline: () =>
        deleteCard(
          `Delete ${c.doc().title}?`,
          `pipeline ${c.doc().title}`,
          "its batches and their takes",
          "This can't be undone. Same as Delete on the pipeline menu.",
          async () => {
            await c.deletePipeline();
            return done(`Deleted pipeline ${c.doc().title}`);
          },
        ),
      judgeTest: async ({ node, take_ids }) => {
        const d = c.doc();
        const id = String(node);
        const n = d.nodes[id];
        if (n?.kind !== "judge") return failed(`Test · ${id} is not a judge`);
        const ids = ((take_ids as string[] | undefined) ?? []).map(String);
        if (!ids.length || ids.length > 8)
          return failed("Test · name 1 to 8 takes (search_takes finds them)");
        const takes = await Promise.all(
          ids.map((t) =>
            ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: t } } })),
          ),
        );
        c.select(id);
        const model = c.models[id] ?? "";
        const provider = model.startsWith("remote:") ? model.slice(7).split("/")[0] : null;
        const crit = [...(n.criteria.must ?? []), ...(n.criteria.nice ?? [])];
        return {
          card: {
            kind: "judge_test",
            title: "Test the judge?",
            rows: [
              ["Pipeline", `${d.title} · ${label(d, id)}`],
              ["Takes", `${takes.length} · ${takes.map((t) => t.title).join(", ")}`.slice(0, 160)],
              ["Criteria", crit.join(", ").slice(0, 160) || "none yet"],
            ],
            tags: [GPU, ...(provider ? [`sends to ${provider}`] : [])],
            thumbs: takes.map((t) => t.video?.poster_url ?? t.thumb_url ?? "").filter(Boolean),
            note: provider
              ? `Sends the criteria and the judge's reasons to ${provider}; no thumbnails.`
              : undefined,
            go: "TEST",
            fine: "Same as Test on the Judge node.",
          },
          yes: async () => {
            await c.save();
            let t = await ok(
              api.POST("/v1/studio/pipelines/{pipeline_id}/judge-tests", {
                params: { path: { pipeline_id: c.pipeline.id } },
                body: { node: id, take_ids: ids },
              }),
            );
            for (const end = Date.now() + AWAIT_CAP_MS; ["queued", "running"].includes(t.status);) {
              if (Date.now() > end)
                return {
                  status: "still_running",
                  text: "Judge test still running · 10 min passed",
                };
              await sleep(1500);
              t = await ok(
                api.GET("/v1/studio/judge-tests/{test_id}", {
                  params: { path: { test_id: t.id } },
                }),
              );
            }
            const out = {
              test_id: t.id,
              status: t.status,
              verdict: t.verdict ?? null,
              takes: t.takes.map((x) => ({
                take_id: x.take_id,
                passes: x.passes,
                picked: x.picked,
                reason: x.reason,
              })),
            };
            return t.status === "succeeded"
              ? done(`You clicked TEST · ${testSummary(t)}`, { result: out })
              : { ...failed(`Judge test ${testSummary(t)}`), result: out };
          },
        };
      },
      addMemory: async ({ text }, { progress }) => {
        progress("Waiting for you · Add note in Memory");
        const id = await c.addNote(String(text ?? ""));
        return id
          ? done("You added a note to Memory", { result: { item_id: id } })
          : { status: "cancelled", text: "Add note · cancelled" };
      },
      deleteMemory: ({ item_id }) => {
        const m = c.memory?.find((x) => x.id === item_id);
        if (!m) return failed("Delete · no such memory item");
        const what = `${m.kind === "judge_example" ? "example" : "note"} “${m.text.slice(0, 60)}${m.text.length > 60 ? "…" : ""}”`;
        return deleteCard(
          "Delete from Memory?",
          what,
          "the other notes and examples",
          "This can't be undone. Same as Delete in Memory.",
          async () => {
            await ok(
              api.DELETE("/v1/studio/memory/{item_id}", { params: { path: { item_id: m.id } } }),
            );
            await c.reloadMemory();
            return done(`Deleted ${what}`);
          },
        );
      },
    },
  };
}

// ---------------------------------------------------------------- the Studio view

/** What the Studio view already works out, read when an action runs. */
export type StudioLive = {
  /** the pipeline's title */
  title: string;
  step: string | null;
  steps: string[];
  label: (id: string) => string;
  nums: Record<string, number>;
  seeds: number;
  /** the steps whose pick this one waits on */
  missing: string[];
  estimate: string;
  workflow?: string;
  picks: Record<string, string | undefined>;
  takesOf: (id: string) => S<"Take">[];
  states: Record<string, string>;
  choose: (id: string) => void;
  pick: (step: string, take: string | undefined) => void;
  /** Run step's own function; the runs it queued */
  run: () => Promise<string[]>;
};

/** `s`: the view as rendered (the chips); `live`: as it is when an action runs. */
export function studioTools(s: StudioLive, live: () => StudioLive): ScreenSpec {
  return {
    screen: "pipeline_editor",
    sees: [
      "Pipeline · Studio",
      s.title,
      ...(s.step ? [`Step ${s.nums[s.step]} · ${s.label(s.step)}`] : []),
    ],
    ids: { step: s.step ?? "" },
    summary: () => {
      const x = live();
      return {
        studio: {
          step: x.step,
          steps: x.steps.map((id) => ({
            id,
            step: x.nums[id],
            title: x.label(id),
            state: x.states[id],
            picked: x.picks[id] ?? null,
            takes: x
              .takesOf(id)
              .slice(0, 8)
              .map((t) => ({ id: t.id, title: t.title })),
          })),
        },
      };
    },
    actions: {
      runStep: async ({ node }) => {
        const id = String(node);
        if (!live().steps.includes(id)) return failed(`Run step · ${id} is not a step`);
        if (live().step !== id) {
          live().choose(id);
          await until(() => live().step === id, 2000);
        }
        const x = live();
        const n = x.nums[id];
        if (x.missing.length)
          return failed(`Run step ${n} · pick a take from step ${x.nums[x.missing[0]]} first`);
        return {
          card: {
            kind: "generate",
            title: `Run step ${n}?`,
            rows: [
              ["Step", `${n} · ${x.label(id)}`],
              ["Seeds", String(x.seeds)],
            ],
            tags: [GPU],
            est: x.estimate || undefined,
            go: `RUN STEP ${n}`,
            fine: "Same as Run step on the screen. Change anything there first if you like.",
          },
          yes: async () => {
            const y = live();
            if (y.step !== id) return failed(`Run step ${n} · another step is open now`);
            if (y.missing.length)
              return failed(`Run step ${n} · pick a take from step ${y.nums[y.missing[0]]} first`);
            const ids = await y.run();
            return ids.length
              ? done(`You clicked RUN STEP ${n}`, { result: { run_ids: ids } })
              : failed(`Run step ${n} failed · see the screen`);
          },
        };
      },
      pickTake: ({ node, take_id }) => {
        const x = live();
        const id = String(node);
        if (!x.steps.includes(id)) return failed(`Pick · ${id} is not a step`);
        const t = x.takesOf(id).find((y) => y.id === take_id);
        if (!t) return failed(`Pick · not a take of step ${x.nums[id]}`);
        const prev = x.picks[id];
        x.pick(id, t.id);
        return done(`Picked ${t.title} from step ${x.nums[id]}`, {
          undo: { undo: () => live().pick(id, prev), redo: () => live().pick(id, t.id) },
        });
      },
    },
  };
}

// ---------------------------------------------------------------- a batch

/** What the batch view already works out. */
export type BatchCtx = {
  view: BatchView;
  pipeline: Pipeline | null;
  control: (op: "pause" | "resume" | "stop") => Promise<Batch>;
  retry: (row: number, fromStep?: number) => Promise<S<"BatchRow">>;
  again: (decisions: "same" | "again") => Promise<Batch>;
};

const batchSummary = (b: Batch) => ({
  id: b.id,
  title: b.title,
  status: b.status,
  control: b.control,
  pipeline: b.pipeline_id ? { id: b.pipeline_id, title: b.pipeline_title } : null,
  workflow: b.workflow_title ?? null,
  counts: b.counts,
});

export function batchTools(c: BatchCtx): ScreenSpec {
  const b = c.view.batch;
  const left = b.counts.total - b.counts.succeeded - b.counts.failed - b.counts.cancelled;
  const bounds = (rows: number) =>
    c.pipeline ? ` · ${boundsLine(c.pipeline.bounds ?? pipelineBounds(c.pipeline), rows)}` : "";
  const source: [string, string] = c.pipeline
    ? ["Pipeline", `${c.pipeline.title} · v${b.pipeline_version}`]
    : ["Workflow", b.workflow_title ?? ""];
  return {
    screen: "batches",
    sees: [`Batch · ${b.title}`, b.status],
    ids: { batch_id: b.id, pipeline_id: b.pipeline_id ?? "" },
    summary: () => ({
      batch: batchSummary(c.view.batch),
      rows: c.view.rows.slice(0, 40).map((r) => ({
        row: r.row,
        status: r.status,
        attempt: r.attempt,
        ...(r.error && { error: r.error.slice(0, 200) }),
        takes: r.take_ids.length,
      })),
      can: {
        pause: canPause(c.view.batch),
        resume: canResume(c.view.batch),
        stop: canStop(c.view.batch),
      },
    }),
    actions: {
      pause: async () => {
        if (!canPause(b)) return failed(`Pause · the batch is ${b.status}`);
        await c.control("pause");
        return done("Paused the batch");
      },
      resume: () => {
        if (!canResume(b)) return failed(`Resume · the batch is ${b.status}`);
        return batchCard(
          "Resume the batch?",
          [source, ["Rows", `${left} to go${bounds(left)}`]],
          egressNote(c.pipeline, left),
          "RESUME",
          "Same as Resume on the batch.",
          async () => {
            const x = await c.control("resume");
            return done("You clicked RESUME", { result: { status: x.status } });
          },
        );
      },
      retry: ({ row, from_step }) => {
        const r = c.view.rows.find((x) => x.row === Number(row));
        if (!r) return failed(`Retry · no row ${String(row)} shown`);
        if (!canRetry(b, r)) return failed(`Retry · row ${r.row} is ${rowState(r)}`);
        const step = from_step === undefined ? undefined : Number(from_step);
        if (step !== undefined && (!Number.isInteger(step) || step < 1))
          return failed("Retry · from_step is a step number (1, 2 …)");
        if (step !== undefined && !b.pipeline_id)
          return failed("Retry · a workflow batch retries the whole row");
        return batchCard(
          `Retry row ${r.row}?`,
          [
            source,
            ["Row", `${r.row} · ${rowState(r)}`.slice(0, 160)],
            ["From", step ? `step ${step}` : "the start"],
          ],
          egressNote(c.pipeline, 1),
          "RETRY",
          "Same as Retry on the row.",
          async () => {
            const x = await c.retry(r.row, step);
            return done(`You clicked RETRY · row ${x.row}`, { result: { status: x.status } });
          },
        );
      },
      again: () => {
        if (!b.pipeline_id || !["done", "failed", "stopped"].includes(b.status))
          return failed("Run again · only a pipeline batch that has ended");
        return batchCard(
          "Run the batch again?",
          [
            source,
            ["Rows", `${b.counts.total}${bounds(b.counts.total)}`],
            ["Decisions", "the same, where their inputs match"],
          ],
          egressNote(c.pipeline, b.counts.total),
          "RUN AGAIN",
          "Same as Run again on the batch (same decisions).",
          async () => {
            const x = await c.again("same");
            return done("You clicked RUN AGAIN", { result: { batch_id: x.id } });
          },
        );
      },
      stop: () => {
        if (!canStop(b)) return failed(`Stop · the batch is ${b.status}`);
        return {
          card: {
            kind: "stop",
            title: b.pipeline_id ? "Cancel the remaining rows?" : "Stop the batch?",
            rows: [
              ["Stops", `${plural(left, "row")} not finished, rows waiting for a pick too`],
              ["Stays", "finished rows and their takes"],
            ],
            tags: [],
            go: "STOP",
            danger: true,
            fine: "This can't be undone. Same as Stop on the batch.",
          },
          yes: async () => {
            const x = await c.control("stop");
            return done("You clicked STOP", { result: { status: x.status } });
          },
        };
      },
    },
  };
}

// ---------------------------------------------------------------- review: a gate's pick

export type ReviewCtx = {
  batch: Batch | null;
  items: S<"BatchReviewItem">[] | null;
  /** the row's pick sent, as the row's Pick button sends it */
  pick: (item: S<"BatchReviewItem">, takeIds: string[]) => Promise<void>;
};

export function reviewTools(c: ReviewCtx): ScreenSpec {
  return {
    screen: "batches",
    sees: [`Review · ${c.batch?.title ?? ""}`, plural(c.items?.length ?? 0, "row") + " waiting"],
    ids: { batch_id: c.batch?.id ?? "" },
    summary: () => ({
      batch: c.batch && batchSummary(c.batch),
      parked: (c.items ?? []).slice(0, 20).map((x) => ({
        row: x.row,
        gate: x.gate_title ?? x.gate,
        kind: x.kind,
        keep: x.keep,
        suggested: x.suggested ?? [],
        candidates: x.candidates.map((k, i) => ({ take_id: k.take_id, n: i + 1, seed: k.seed })),
      })),
    }),
    actions: {
      pick: ({ row, take_ids }) => {
        const it = c.items?.find((x) => x.row === Number(row));
        if (!it) return failed(`Pick · row ${String(row)} is not waiting for a pick`);
        const ids = (take_ids as string[]).map(String);
        const nums = ids.map((t) => it.candidates.findIndex((k) => k.take_id === t) + 1);
        if (nums.includes(0)) return failed(`Pick · not a candidate of row ${it.row}`);
        if (ids.length > it.keep) return failed(`Pick · row ${it.row} keeps at most ${it.keep}`);
        return {
          card: {
            kind: "pick",
            title: `Pick for row ${it.row}?`,
            rows: [
              ["Row", `${it.row} · ${it.gate_title ?? "Gate"}`],
              [
                ids.length === 1 ? "Take" : "Takes",
                `${nums.map((x) => `take ${x}`).join(", ")} of ${it.candidates.length}`,
              ],
              ["Then", "the row goes on with it"],
            ],
            tags: [GPU],
            thumbs: ids
              .map((t) => it.candidates.find((k) => k.take_id === t)?.thumb_url ?? "")
              .filter(Boolean),
            go: "PICK",
            fine: "Same as Pick on the row.",
          },
          yes: async () => {
            await c.pick(it, ids);
            return done(`You clicked PICK · row ${it.row}`);
          },
        };
      },
    },
  };
}

/** A gate's pick sent (409: the row moved on meanwhile; it leaves the grid either way). */
export const sendPick = (batchId: string, it: S<"BatchReviewItem">, takeIds: string[]) =>
  okOr(
    api.POST("/v1/studio/batches/{batch_id}/rows/{row}/pick", {
      params: { path: { batch_id: batchId, row: String(it.row) } },
      body: { gate: it.gate, take_ids: takeIds },
    }),
    409,
  );

// ---------------------------------------------------------------- the pipelines list and New batch

/** The open New batch draft, as NewBatch exposes it (fill and Submit are its own). */
export type Draft = {
  source: { kind: "pipeline" | "workflow"; id: string; title: string };
  rows: number;
  seeds: number | null;
  looks: string[];
  errors: string[];
  summary: string;
  pipeline?: Pipeline;
  fill: (a: { rows?: Record<string, unknown>[]; seeds?: number; looks?: string[] }) => Outcome;
  /** Submit's own function; the new batch, or why not */
  submit: () => Promise<{ id: string } | { error: string }>;
  /** why Submit can't run now */
  cannot: () => string | null;
};

/** The CSV a New batch table reads (header: the field keys or names; quoted where needed). */
export function toCsv(rows: Record<string, unknown>[]): string {
  const keys = [...new Set(rows.flatMap((r) => Object.keys(r)))];
  const cell = (v: unknown) => {
    const s = v == null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v);
    return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return [keys.map(cell), ...rows.map((r) => keys.map((k) => cell(r[k])))]
    .map((l) => l.join(","))
    .join("\n");
}

/** The looks named (by id or name) as New batch's choice, or why one can't be used. */
export function looksChosen(
  names: string[],
  looks: S<"LibraryItem">[] | null,
  workflow: S<"Workflow"> | undefined,
  loraFamilies: Record<string, string>,
): Chosen | { error: string } {
  const out: Record<string, string[]> = {};
  for (const v of names) {
    const it = (looks ?? []).find(
      (x) => x.kind === "look" && (x.id === v || x.name.toLowerCase() === v.toLowerCase()),
    );
    if (!it) return { error: `no look ${v}` };
    const why = workflow && reasonOf(workflow, it, loraFamilies);
    if (why) return { error: `${it.name} · ${why}` };
    (out[it.group ?? ""] ??= []).push(it.id);
  }
  return out as Chosen;
}

export type ListCtx = {
  pipelines: Pipeline[] | null;
  batches: Batch[] | null;
  selected: Pipeline | null;
  /** New batch is open */
  drafting: boolean;
  draft: () => Draft | null;
  /** opens New batch (for the selected pipeline, else a workflow) */
  openDraft: () => void;
};

export function listTools(c: ListCtx): ScreenSpec {
  return {
    screen: "batches",
    sees: [
      "Pipelines",
      ...(c.selected ? [c.selected.title] : []),
      ...(c.drafting ? ["New batch"] : []),
    ],
    ids: { pipeline_id: c.selected?.id ?? "" },
    summary: () => {
      const x = c.draft();
      return {
        pipelines: (c.pipelines ?? []).slice(0, 30).map((p) => ({
          id: p.id,
          title: p.title,
          version: p.version,
          last_batch: p.last_batch ? { id: p.last_batch.id, status: p.last_batch.status } : null,
        })),
        selected: c.selected?.id ?? null,
        recent_batches: (c.batches ?? []).slice(0, 8).map(batchSummary),
        new_batch: x && {
          source: x.source,
          rows: x.rows,
          seeds: x.seeds,
          looks: x.looks,
          errors: x.errors,
          summary: x.summary,
        },
      };
    },
    actions: {
      newBatch: async (args) => {
        if (!c.draft()) {
          c.openDraft();
          if (!(await until(() => !!c.draft(), 3000))) return failed("New batch · did not open");
        }
        return c.draft()!.fill(args as Parameters<Draft["fill"]>[0]);
      },
      start: async () => {
        await sleep(0); // a fill just before this has rendered
        const x = c.draft();
        if (!x) return failed("Start · open New batch first (newBatch fills it)");
        const why = x.cannot();
        if (why) return failed(`Start · ${why}`);
        const rows = x.rows * (x.seeds ?? 1);
        return batchCard(
          "Start the batch?",
          [
            [x.source.kind === "pipeline" ? "Pipeline" : "Workflow", x.source.title],
            [
              "Rows",
              x.pipeline
                ? `${x.rows} · ${boundsLine(x.pipeline.bounds ?? pipelineBounds(x.pipeline), rows)}`
                : x.summary,
            ],
          ],
          x.pipeline ? egressNote(x.pipeline, rows) : { tags: [GPU] },
          "START BATCH",
          "Same as Submit on New batch. Change the rows there first if you like.",
          async () => {
            const now = c.draft();
            if (!now) return failed("Start · New batch was closed");
            const no = now.cannot();
            if (no) return failed(`Start · ${no}`);
            const r = await now.submit();
            return "id" in r
              ? done("You clicked START BATCH", { result: { batch_id: r.id } })
              : failed(`Start failed · ${r.error}`);
          },
        );
      },
    },
  };
}
