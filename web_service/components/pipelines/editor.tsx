"use client";
// The pipeline editor (docs/mock/PipelineGraph.dc.html, plan 16.2): the pipeline's graph (components/pipelines/canvas,
// loaded only here), the selected node's settings in the side panel, and its last batch's run table. Edits are
// checked by lib/pipeline and POST /pipelines/validate, saved with debounce by PUT with the version (409 reloads),
// and undone with ⌘Z / ⇧⌘Z. Run opens New batch with the pipeline's inputs as columns. The mock's director chat,
// Studio | Graph switch, memory meter and judge panel belong to later milestones and are left out.
import dynamic from "next/dynamic";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { Activity, ChevronLeft, Plus, X } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { FullScreen } from "@/components/media";
import { Head, Problems } from "@/components/graph/panel";
import { NewBatch } from "@/components/pipelines/new-batch";
import { followBatch } from "@/components/pipelines/pipelines";
import { RunTable } from "@/components/pipelines/run-table";
import type { CanvasProps } from "@/components/pipelines/canvas";
import { api, ApiError, errorText, message, ok, type S } from "@/lib/api";
import { applyBatchEvent, type BatchView } from "@/lib/batches";
import { docOf, mergeProblems, paletteOf, type Doc, type Palette, type Problem } from "@/lib/graph";
import { history, historyOf } from "@/lib/graph-edit";
import {
  bindCandidates,
  chooseWorkflow,
  connectNodes,
  disconnectNodes,
  edgeOut,
  gateCandidates,
  nodeLabel,
  order,
  outputCandidates,
  pipelineProblems,
  removePipelineNodes,
  setCap,
  setNode,
  settable,
  stepNumbers,
  type PDoc,
  type PProblem,
} from "@/lib/pipeline";

type Pipeline = S<"Pipeline">;
type Workflow = S<"Workflow">;
type Edit = (f: (d: PDoc) => PDoc) => void;

const PipelineCanvas = dynamic<CanvasProps>(
  () => import("@/components/pipelines/canvas").then((m) => m.PipelineCanvas),
  {
    ssr: false,
    loading: () => (
      <div className="flex h-full items-center justify-center text-t2">Loading the graph…</div>
    ),
  },
);

const SAVE_MS = 700;
const docOfPipeline = (p: Pipeline): PDoc => ({ title: p.title, nodes: p.nodes, edges: p.edges });
const asProblems = (ps: PProblem[]) => ps as unknown as Problem[];

/** The palette from the workflows' own node schemas, when GET /nodes can't answer. */
function paletteFromWorkflows(ws: Workflow[]): Palette {
  const out: Palette = {};
  for (const w of ws)
    for (const s of Object.values(w.node_schemas))
      out[s.kind] = { ...s, envs: [...new Set([...(out[s.kind]?.envs ?? []), w.env])] };
  return out;
}

/** A source as the panel offers it: "Input · prompt", "Step 1 · Qwen Image · takes", "Gate · picked". */
export function sourceLabel(doc: PDoc, src: string, titles: Record<string, string>) {
  if (src.startsWith("$")) {
    const name = src.slice(1);
    const i = Object.values(doc.nodes)
      .flatMap((n) => (n.kind === "inputs" ? n.inputs : []))
      .find((x) => x.name === name);
    return `Input · ${i?.label ?? name}`;
  }
  const [id, what] = src.split(".");
  const n = stepNumbers(doc)[id];
  return `${n ? `Step ${n} · ` : ""}${nodeLabel(doc, id, titles)} · ${what}`;
}

export function PipelineEditor({
  pipeline,
  workflows,
  nodes,
}: {
  pipeline: Pipeline;
  workflows: Workflow[];
  nodes: S<"NodePalette"> | null;
}) {
  const [h, dispatch] = useReducer(history<PDoc>, pipeline, (p) => historyOf(docOfPipeline(p)));
  const doc = h.present;
  const palette = useMemo(
    () => (nodes ? paletteOf(nodes) : paletteFromWorkflows(workflows)),
    [nodes, workflows],
  );
  const wfDocs = useMemo(
    () => Object.fromEntries(workflows.map((w) => [w.id, docOf(w)])) as Record<string, Doc>,
    [workflows],
  );
  const titles = useMemo(
    () => ({
      ...pipeline.workflow_titles,
      ...Object.fromEntries(workflows.map((w) => [w.id, w.title])),
    }),
    [pipeline.workflow_titles, workflows],
  );

  const [cur, setCur] = useState(pipeline);
  const base = useRef(pipeline);
  const saved = useRef(doc);
  const latest = useRef(doc);
  useEffect(() => {
    latest.current = doc;
  });
  const [server, setServer] = useState<{ doc: PDoc; problems: PProblem[] } | null>(null);
  const [saving, setSaving] = useState<"" | "saving" | "failed">("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [flowKey, setFlowKey] = useState(0);
  const [running, setRunning] = useState(false);

  const client = useMemo(() => pipelineProblems(doc, wfDocs, palette), [doc, wfDocs, palette]);
  const problems = useMemo(
    () =>
      (server?.doc === doc
        ? mergeProblems(asProblems(client), asProblems(server.problems))
        : asProblems(client)) as unknown as PProblem[],
    [client, server, doc],
  );

  const edit: Edit = useCallback((f) => {
    const next = f(latest.current);
    if (next !== latest.current) dispatch({ type: "edit", doc: next });
  }, []);

  // ---------------------------------------------------------------- check and save
  const save = useCallback(
    async (d: PDoc) => {
      let canSave = !pipelineProblems(d, wfDocs, palette).some((p) => p.blocks === "save");
      try {
        const v = await ok(api.POST("/v1/studio/pipelines/validate", { body: d }));
        setServer({ doc: d, problems: v.problems });
        canSave = v.can_save;
      } catch {
        // studio can't check now: the client's check stands
      }
      if (d === saved.current || !canSave) return;
      setSaving("saving");
      try {
        const { data, error, response } = await api.PUT("/v1/studio/pipelines/{pipeline_id}", {
          params: { path: { pipeline_id: base.current.id } },
          body: { ...d, version: base.current.version },
        });
        if (response.status === 409) {
          const fresh = (await ok(
            api.GET("/v1/studio/pipelines/{pipeline_id}", {
              params: { path: { pipeline_id: base.current.id } },
            }),
          )) as Pipeline;
          base.current = fresh;
          setCur(fresh);
          saved.current = docOfPipeline(fresh);
          dispatch({ type: "reset", doc: saved.current });
          setFlowKey((k) => k + 1);
          setNotice(
            `This pipeline was changed elsewhere; it is reloaded at version ${fresh.version}. Your last edits were not saved.`,
          );
        } else if (response.status === 422 && error && "problems" in error && error.problems) {
          setServer({ doc: d, problems: error.problems });
        } else if (!response.ok || !data) {
          throw new ApiError(response.status, errorText(response.status, error));
        } else {
          base.current = data as Pipeline;
          setCur(data as Pipeline);
          saved.current = d;
          if (data.problems) setServer({ doc: d, problems: data.problems });
        }
        setSaving("");
      } catch (e) {
        setSaving("failed");
        setError(message(e));
      }
    },
    [wfDocs, palette],
  );
  const queue = useRef<Promise<void>>(Promise.resolve());
  const flush = useCallback(() => {
    const d = latest.current;
    queue.current = queue.current.then(() => save(d));
    return queue.current;
  }, [save]);
  const checked = useRef<PDoc | null>(null);
  useEffect(() => {
    if (checked.current === doc) return;
    const t = setTimeout(() => {
      checked.current = doc;
      void flush();
    }, SAVE_MS);
    return () => clearTimeout(t);
  }, [doc, flush]);
  const flushRef = useRef(flush);
  useEffect(() => {
    flushRef.current = flush;
  });
  useEffect(
    () => () => {
      if (latest.current !== saved.current) void flushRef.current();
    },
    [],
  );

  // undo / redo while focus is not in a field
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select, [contenteditable], dialog")) return;
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "z") {
        e.preventDefault();
        dispatch({ type: e.shiftKey ? "redo" : "undo" });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // ---------------------------------------------------------------- the last batch's run table, live
  const last = cur.last_batch;
  const lastId = last?.id ?? null;
  const [run, setRun] = useState<BatchView | null>(null);
  const [viewer, setViewer] = useState<{ takes: S<"Take">[]; i: number } | null>(null);
  useEffect(() => {
    if (!lastId) return;
    let stop = false;
    let unfollow: (() => void) | undefined;
    const path = { path: { batch_id: lastId } };
    Promise.all([
      ok(api.GET("/v1/studio/batches/{batch_id}", { params: path })),
      ok(
        api.GET("/v1/studio/batches/{batch_id}/rows", {
          params: { ...path, query: { limit: 100 } },
        }),
      ),
    ])
      .then(([batch, page]) => {
        if (stop) return;
        setRun({ batch, rows: page.rows });
        unfollow = followBatch(lastId, ["batch", "row"], (e) =>
          setRun((v) => v && applyBatchEvent(v, e)),
        );
      })
      .catch(() => undefined);
    return () => {
      stop = true;
      unfollow?.();
    };
  }, [lastId]);
  async function retryRow(r: S<"BatchRow">) {
    if (!lastId) return;
    try {
      const row = await ok(
        api.POST("/v1/studio/batches/{batch_id}/rows/{row}/retry", {
          params: { path: { batch_id: lastId, row: String(r.row) } },
        }),
      );
      setRun((v) => v && applyBatchEvent(v, { seq: 0, type: "row", ts: "", data: row }));
    } catch (e) {
      setError(message(e));
    }
  }
  async function openTakes(ids: string[]) {
    try {
      const takes = await Promise.all(
        ids.map((t) =>
          ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: t } } })),
        ),
      );
      if (takes.length) setViewer({ takes, i: 0 });
    } catch (e) {
      setError(message(e));
    }
  }

  const ns = Object.values(doc.nodes);
  const count = (k: string) => ns.filter((n) => n.kind === k).length;
  const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
  const badge = [
    "Unattended",
    plural(count("step"), "step"),
    plural(count("gate"), "gate"),
    problems.length ? plural(problems.length, "problem") : "valid",
    saving === "saving" ? "saving…" : saving === "failed" ? "not saved" : "",
  ]
    .filter(Boolean)
    .join(" · ");
  const firstWorkflow = workflows[0]?.id;
  const sel = selected && doc.nodes[selected] ? selected : null;
  const blocked = problems.length > 0;

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        <div className="flex flex-wrap items-center gap-3">
          <Link
            href="/pipelines"
            aria-label="Pipelines"
            className="grid size-9 place-items-center rounded-[10px] text-t2 hover:bg-s2"
          >
            <ChevronLeft size={18} aria-hidden />
          </Link>
          <TitleField title={doc.title} onCommit={(t) => edit((d) => ({ ...d, title: t }))} />
          <span className="rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2">
            {badge}
          </span>
          {last && (
            <Link
              href={`/pipelines/batches/${last.id}/rows/1`}
              aria-label="Execution view"
              title="Execution view (row 1 of the last batch)"
              className="grid h-9 w-10 place-items-center rounded-[10px] bg-s4 text-t1"
            >
              <Activity size={18} aria-hidden />
            </Link>
          )}
          <div className="grow" />
          <span className="mono text-[12px] text-t2">v{cur.version}</span>
        </div>
        {notice && (
          <p role="status" className="rounded-xl bg-s2 px-3 py-2 text-[13px]">
            {notice}
            <button type="button" className="chip ml-3 h-7" onClick={() => setNotice("")}>
              OK
            </button>
          </p>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <div
          className={`panel relative overflow-hidden ${last && run ? "h-[440px]" : "min-h-[440px] flex-1"}`}
          style={{ background: "var(--in)" }}
        >
          <PipelineCanvas
            doc={doc}
            titles={titles}
            problems={problems}
            selected={sel}
            onSelect={setSelected}
            edit={edit}
            defaultWorkflow={firstWorkflow}
            flowKey={String(flowKey)}
          />
        </div>
        {last && run && (
          <>
            <div className="flex items-center gap-2">
              <h2 className="heading">Last batch</h2>
              <Link href={`/pipelines/batches/${last.id}`} className="text-[12px] text-t2">
                {run.batch.title}
              </Link>
            </div>
            <div className="panel overflow-x-auto px-2 py-1.5">
              <RunTable
                batch={run.batch}
                rows={run.rows}
                doc={doc}
                titles={titles}
                busy=""
                onRetry={(r) => void retryRow(r)}
                onOpen={(ids) => void openTakes(ids)}
              />
            </div>
          </>
        )}
      </main>
      <aside
        aria-label="Inspector"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-[18px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        {sel ? (
          <NodePanel
            key={sel}
            doc={doc}
            id={sel}
            problems={problems.filter((p) => p.node === sel)}
            workflows={workflows}
            wfDocs={wfDocs}
            palette={palette}
            titles={titles}
            edit={edit}
            onRemoved={() => setSelected(null)}
          />
        ) : (
          <>
            <Head
              title={doc.title}
              sub={`Yours · version ${cur.version}. Edits change only new batches.`}
            />
            <div id="pipeline-problems">
              <Problems list={asProblems(problems.filter((p) => !p.node))} />
            </div>
            {problems.some((p) => p.node) && (
              <p className="m-0 text-[12px] text-bad">
                {plural(problems.filter((p) => p.node).length, "problem")} on nodes; they are marked
                on the cards.
              </p>
            )}
            <p className="m-0 text-[13px] text-t2">
              Select a node to edit it. Add one with Add node, A, a double-click or a right-click;
              drag from a port to connect it, or drop the wire on the canvas to add the next node. A
              gate&apos;s fail port loops back for new seeds, at most the cap&apos;s times. ⌘Z
              undoes.
            </p>
            <div className="grow" />
            <button
              type="button"
              className="key h-12 text-[14px]"
              disabled={blocked}
              aria-describedby={blocked ? "pipeline-problems" : undefined}
              onClick={() => void flush().then(() => setRunning(true))}
            >
              {blocked ? plural(problems.length, "problem") : "Run"}
            </button>
          </>
        )}
      </aside>
      {running && <NewBatch open onClose={() => setRunning(false)} pipeline={{ ...cur, ...doc }} />}
      {viewer && (
        <FullScreen
          take={viewer.takes[viewer.i]}
          onClose={() => setViewer(null)}
          onStep={(d) =>
            setViewer((v) => v && { ...v, i: (v.i + d + v.takes.length) % v.takes.length })
          }
        />
      )}
    </>
  );
}

/** The title in the top bar: edited in place, saved on Enter or when it loses focus (blank goes back). */
function TitleField({ title, onCommit }: { title: string; onCommit: (t: string) => void }) {
  const [v, setV] = useState(title);
  const [was, setWas] = useState(title);
  if (title !== was) {
    setWas(title);
    setV(title);
  }
  const commit = () => {
    const t = v.trim();
    if (t && t !== title) onCommit(t.slice(0, 80));
    else setV(title);
  };
  return (
    <input
      aria-label="Pipeline title"
      className="h-10 min-w-0 rounded-[10px] border-0 bg-transparent px-3 font-display text-[18px] font-bold tracking-[0.01em] text-t1 hover:bg-s2 focus:bg-s2"
      style={{ width: `${Math.max(8, Math.min(40, v.length + 2))}ch` }}
      maxLength={80}
      value={v}
      onChange={(e) => setV(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
    />
  );
}

// ---------------------------------------------------------------- the side panel

type PanelProps = {
  doc: PDoc;
  id: string;
  problems: PProblem[];
  workflows: Workflow[];
  wfDocs: Record<string, Doc>;
  palette: Palette;
  titles: Record<string, string>;
  edit: Edit;
  onRemoved: () => void;
};

/** A labelled select, the panel's one control for a choice. */
function Choice({
  id,
  label,
  value,
  options,
  onChange,
  error,
  disabled,
}: {
  id: string;
  label: string;
  value: string;
  options: [string, string][];
  onChange: (v: string) => void;
  error?: string;
  disabled?: boolean;
}) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id}>{label}</label>
        <select
          id={id}
          value={value}
          disabled={disabled}
          aria-invalid={!!error || undefined}
          onChange={(e) => onChange(e.target.value)}
          className="h-[34px] max-w-[62%] rounded-lg border border-line bg-s1 px-2 text-[13px] text-t1"
        >
          {options.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      </div>
      {error && <span className="text-[12px] text-bad">{error}</span>}
    </div>
  );
}

const range = (n: number) => Array.from({ length: n }, (_, i) => i + 1);

function NodePanel(props: PanelProps) {
  const { doc, id, problems, titles, edit, onRemoved } = props;
  const n = doc.nodes[id];
  const kindName = { inputs: "Inputs", step: "Step", gate: "Gate", output: "Output" }[n.kind];
  const nums = stepNumbers(doc);
  const targets: [string, string][] = order(doc)
    .filter((x) => x !== id && doc.nodes[x].kind !== "inputs")
    .map((x) => [x, `${nums[x] ? `${nums[x]} · ` : ""}${nodeLabel(doc, x, titles)}`]);
  const out = edgeOut(doc, id)?.[1];
  const next = typeof out === "string" ? out : (out?.pass ?? "");
  const general = problems.filter((p) => !p.port && !p.param);
  return (
    <>
      <Head
        title={`${n.kind === "step" ? `${nums[id]} · ` : ""}${nodeLabel(doc, id, titles)}`}
        sub={`${kindName} · ${id}`}
      />
      <Problems list={asProblems(general)} />
      {n.kind !== "inputs" && (
        <label className="flex flex-col gap-1.5">
          <span className="label">Title on the card</span>
          <input
            className="in"
            maxLength={60}
            placeholder={nodeLabel(
              { ...doc, nodes: { ...doc.nodes, [id]: { ...n, title: undefined } } },
              id,
              titles,
            )}
            value={n.title ?? ""}
            onChange={(e) => {
              const t = e.target.value;
              const { title: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
              edit((d) => setNode(d, id, (t.trim() ? { ...rest, title: t } : rest) as typeof n));
            }}
          />
        </label>
      )}
      {n.kind === "inputs" && <InputsPanel {...props} />}
      {n.kind === "step" && <StepPanel {...props} />}
      {n.kind === "gate" && <GatePanel {...props} targets={targets} />}
      {n.kind === "output" && <OutputPanel {...props} />}
      {(n.kind === "inputs" || n.kind === "step") && (
        <Choice
          id="pp-next"
          label="Then goes to"
          value={next}
          options={[["", "Nowhere yet"], ...targets]}
          onChange={(v) =>
            edit((d) => (v ? connectNodes(d, id, "next", v) : disconnectNodes(d, id, "next")))
          }
        />
      )}
      <button
        type="button"
        className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
        onClick={() => {
          edit((d) => removePipelineNodes(d, [id]));
          onRemoved();
        }}
      >
        Remove this node
      </button>
    </>
  );
}

function InputsPanel({ doc, id, problems, edit }: PanelProps) {
  const n = doc.nodes[id] as S<"PipelineInputsNode">;
  const set = (inputs: S<"PipelineInput">[]) => edit((d) => setNode(d, id, { ...n, inputs }));
  const at = (i: number, v: Partial<S<"PipelineInput">>) =>
    set(n.inputs.map((x, j) => (j === i ? { ...x, ...v } : x)));
  return (
    <section aria-labelledby="pp-inputs" className="flex flex-col gap-2.5">
      <h3 id="pp-inputs" className="heading flex items-center">
        Columns
        <Info>
          Each input is a column of the batch (lines, a CSV table or a grid). Steps bind it as
          $name.
        </Info>
      </h3>
      {n.inputs.map((x, i) => {
        const bad = problems.find((p) => p.param === x.name);
        return (
          <div key={i} className="flex flex-col gap-1 rounded-xl bg-s1 p-2.5">
            <div className="flex items-center gap-2">
              <input
                aria-label={`Input ${i + 1} name`}
                className="in mono h-[34px] flex-1"
                value={x.name}
                pattern="[a-z][a-z0-9_]*"
                onChange={(e) =>
                  at(i, {
                    name:
                      e.target.value
                        .toLowerCase()
                        .replace(/[^a-z0-9_]/g, "_")
                        .slice(0, 64) || "x",
                  })
                }
              />
              <select
                aria-label={`Input ${i + 1} type`}
                className="h-[34px] rounded-lg border border-line bg-s1 px-2 text-[13px] text-t1"
                value={x.type}
                onChange={(e) => at(i, { type: e.target.value as S<"PipelineInputType"> })}
              >
                {["Text", "Image", "Int", "Float", "Seed"].map((t) => (
                  <option key={t}>{t}</option>
                ))}
              </select>
              {n.inputs.length > 1 && (
                <button
                  type="button"
                  aria-label={`Remove input ${x.name}`}
                  className="grid size-[34px] shrink-0 place-items-center rounded-lg bg-s3"
                  onClick={() => set(n.inputs.filter((_, j) => j !== i))}
                >
                  <X size={14} aria-hidden />
                </button>
              )}
            </div>
            <input
              aria-label={`Input ${i + 1} label`}
              className="in h-[34px]"
              placeholder="Label (optional)"
              maxLength={60}
              value={x.label ?? ""}
              onChange={(e) => {
                const { label: _drop, ...rest } = x; // eslint-disable-line @typescript-eslint/no-unused-vars
                set(
                  n.inputs.map((y, j) =>
                    j === i ? (e.target.value ? { ...rest, label: e.target.value } : rest) : y,
                  ),
                );
              }}
            />
            {bad && <span className="text-[12px] text-bad">{bad.message}</span>}
          </div>
        );
      })}
      {n.inputs.length < 16 && (
        <button
          type="button"
          className="flex h-10 items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
          onClick={() => {
            let k = n.inputs.length + 1;
            while (n.inputs.some((x) => x.name === `input_${k}`)) k++;
            set([...n.inputs, { name: `input_${k}`, type: "Text" }]);
          }}
        >
          <Plus size={14} aria-hidden /> Add input
        </button>
      )}
    </section>
  );
}

function StepPanel({ doc, id, problems, workflows, wfDocs, palette, titles, edit }: PanelProps) {
  const n = doc.nodes[id] as S<"PipelineStepNode">;
  const wf = workflows.find((w) => w.id === n.workflow);
  const wd = wfDocs[n.workflow];
  const params = wd ? settable(wd, palette) : {};
  const presets = Object.keys(wf?.studio?.presets ?? {});
  const bind = n.bind ?? {};
  const rows = Object.entries(params)
    .map(([k, p]) => ({ k, p, c: bindCandidates(doc, id, p) }))
    .filter((r) => r.c.length || r.k in bind);
  const set = (v: Partial<S<"PipelineStepNode">>) => edit((d) => setNode(d, id, { ...n, ...v }));
  const nodeTitle = (k: string) =>
    palette[wd?.nodes[k.split(".")[0]]?.kind ?? ""]?.title ?? k.split(".")[0];
  const unknown = !wf && n.workflow;
  return (
    <>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="pp-wf" className="label">
          Workflow
        </label>
        <select
          id="pp-wf"
          className="in"
          value={wf ? n.workflow : ""}
          onChange={(e) =>
            edit((d) => chooseWorkflow(d, id, e.target.value, wfDocs[e.target.value], palette))
          }
        >
          {!wf && (
            <option value="">{unknown ? `${n.workflow} (gone)` : "Choose a workflow"}</option>
          )}
          {(["template", "mine"] as const).map((src) => (
            <optgroup key={src} label={src === "template" ? "Templates" : "Mine"}>
              {workflows
                .filter((w) => w.source === src)
                .map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.title}
                  </option>
                ))}
            </optgroup>
          ))}
        </select>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <label className="set flex flex-col gap-1">
          <small className="text-[12px] text-t2">Seeds per visit</small>
          <select
            className="bg-transparent font-semibold text-t1"
            value={n.seeds ?? 1}
            onChange={(e) => set({ seeds: Number(e.target.value) })}
          >
            {range(8).map((i) => (
              <option key={i} value={i}>
                {i}
              </option>
            ))}
          </select>
        </label>
        {presets.length > 0 && (
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Preset</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={n.preset ?? ""}
              onChange={(e) => {
                const { preset: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
                edit((d) =>
                  setNode(d, id, e.target.value ? { ...rest, preset: e.target.value } : rest),
                );
              }}
            >
              <option value="">None</option>
              {presets.map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
          </label>
        )}
      </div>
      {Object.keys(n.inputs ?? {}).length > 0 && (
        <p className="m-0 text-[12px] text-t2">
          Fixed settings:{" "}
          {Object.entries(n.inputs ?? {})
            .map(([k, v]) => `${k} ${JSON.stringify(v)}`)
            .join(", ")}
        </p>
      )}
      {problems
        .filter((p) => p.param)
        .map((p) => (
          <span key={p.param} className="text-[12px] text-bad">
            {p.message}
          </span>
        ))}
      {wd && (
        <section aria-labelledby="pp-bind" className="flex flex-col gap-3">
          <h3 id="pp-bind" className="heading flex items-center">
            Inputs of {wf?.title}
            <Info>
              Bind a setting to a column of the batch or to an earlier step&apos;s takes (or a
              gate&apos;s pick); only sources of its type are offered. Unbound, it keeps the
              workflow&apos;s value.
            </Info>
          </h3>
          {rows.length === 0 && (
            <p className="m-0 text-[12px] text-t2">Nothing here takes a column or a take.</p>
          )}
          {rows.map(({ k, p, c }) => {
            const now = bind[k] ?? "";
            const opts = now && !c.includes(now) ? [now, ...c] : c;
            const needed = p.type === "image" || (p.type === "rows" && (p.min ?? 0) >= 1);
            return (
              <Choice
                key={k}
                id={`pp-b-${k}`}
                label={`${nodeTitle(k)} · ${p.label ?? p.name}`}
                value={now}
                error={problems.find((x) => x.port === k)?.message}
                options={[
                  ["", needed ? "Not bound (needed)" : "Workflow's value"],
                  ...opts.map((s) => [s, sourceLabel(doc, s, titles)] as [string, string]),
                ]}
                onChange={(v) => {
                  const b = { ...bind };
                  if (v) b[k] = v;
                  else delete b[k];
                  set({ bind: b });
                }}
              />
            );
          })}
          {Object.keys(bind)
            .filter((k) => !(k in params))
            .map((k) => (
              <div key={k} className="flex items-center justify-between gap-2 text-[12px] text-bad">
                {k} ← {bind[k]}: not a setting of this workflow
                <button
                  type="button"
                  className="rounded-md px-1.5 py-0.5 text-t2 hover:bg-s3 hover:text-t1"
                  onClick={() => {
                    const b = { ...bind };
                    delete b[k];
                    set({ bind: b });
                  }}
                >
                  Remove
                </button>
              </div>
            ))}
        </section>
      )}
    </>
  );
}

function GatePanel({
  doc,
  id,
  titles,
  edit,
  targets,
}: PanelProps & { targets: [string, string][] }) {
  const n = doc.nodes[id] as S<"PipelineGateNode">;
  const steps = gateCandidates(doc, id);
  const opts = n.takes && !steps.includes(n.takes) ? [n.takes, ...steps] : steps;
  const made =
    doc.nodes[n.takes]?.kind === "step"
      ? ((doc.nodes[n.takes] as S<"PipelineStepNode">).seeds ?? 1)
      : 8;
  const out = edgeOut(doc, id)?.[1];
  const branch = typeof out === "object" ? out : null;
  const pass = typeof out === "string" ? out : (branch?.pass ?? "");
  return (
    <>
      <Choice
        id="pp-takes"
        label="Pick from"
        value={n.takes}
        options={[
          ...(n.takes ? [] : ([["", "Choose a step"]] as [string, string][])),
          ...opts.map((s) => [s, sourceLabel(doc, `${s}.takes`, titles)] as [string, string]),
        ]}
        onChange={(v) => edit((d) => setNode(d, id, { ...n, takes: v }))}
      />
      <div className="grid grid-cols-2 gap-2">
        <label className="set flex flex-col gap-1">
          <small className="text-[12px] text-t2">Keep</small>
          <select
            className="bg-transparent font-semibold text-t1"
            value={n.keep ?? 1}
            onChange={(e) => edit((d) => setNode(d, id, { ...n, keep: Number(e.target.value) }))}
          >
            {range(Math.max(made, n.keep ?? 1)).map((i) => (
              <option key={i} value={i}>
                {i === 1 ? `best 1 of ${made}` : `up to ${i} of ${made}`}
              </option>
            ))}
          </select>
        </label>
        <label className="set flex flex-col gap-1">
          <small className="text-[12px] text-t2">If picked</small>
          <select
            className="bg-transparent font-semibold text-t1"
            value={pass}
            onChange={(e) =>
              edit((d) =>
                e.target.value
                  ? connectNodes(d, id, "next", e.target.value)
                  : disconnectNodes(d, id, "next"),
              )
            }
          >
            <option value="">Nowhere yet</option>
            {targets.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label className="set flex flex-col gap-1">
          <small className="text-[12px] text-t2">If none pass</small>
          <select
            className="bg-transparent font-semibold text-t1"
            value={branch?.fail ?? ""}
            disabled={!pass}
            onChange={(e) =>
              edit((d) =>
                e.target.value
                  ? connectNodes(d, id, "fail", e.target.value)
                  : disconnectNodes(d, id, "fail"),
              )
            }
          >
            <option value="">Fail the row</option>
            {targets.map(([v, l]) => (
              <option key={v} value={v}>
                Retry at {l}
              </option>
            ))}
          </select>
        </label>
        {branch && (
          <>
            <label className="set flex flex-col gap-1">
              <small className="text-[12px] text-t2">Retry at most</small>
              <select
                className="bg-transparent font-semibold text-t1"
                value={branch.max}
                onChange={(e) => edit((d) => setCap(d, id, Number(e.target.value)))}
              >
                {range(10).map((i) => (
                  <option key={i} value={i}>
                    {i} time{i === 1 ? "" : "s"}
                  </option>
                ))}
              </select>
            </label>
            <label className="set flex flex-col gap-1">
              <small className="text-[12px] text-t2">Then</small>
              <select
                className="bg-transparent font-semibold text-t1"
                value={branch.then}
                onChange={(e) => edit((d) => connectNodes(d, id, "then", e.target.value))}
              >
                {targets.map(([v, l]) => (
                  <option key={v} value={v}>
                    {l}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
      </div>
      <p className="m-0 text-[12px] text-t2">
        The row parks here until you pick from the review grid; parked rows never hold up the queue.
      </p>
    </>
  );
}

function OutputPanel({ doc, id, problems, titles, edit }: PanelProps) {
  const n = doc.nodes[id] as S<"PipelineOutputNode">;
  const c = outputCandidates(doc, id);
  const opts = c.includes(n.from) ? c : [n.from, ...c];
  return (
    <>
      <Choice
        id="pp-from"
        label="Ends with"
        value={n.from}
        error={problems.find((p) => p.port === "from")?.message}
        options={opts.map((s) => [s, sourceLabel(doc, s, titles)])}
        onChange={(v) => edit((d) => setNode(d, id, { ...n, from: v }))}
      />
      <label className="flex flex-col gap-1.5">
        <span className="label">Collection</span>
        <input
          className="in"
          maxLength={80}
          value={n.collection}
          onChange={(e) =>
            e.target.value && edit((d) => setNode(d, id, { ...n, collection: e.target.value }))
          }
        />
      </label>
    </>
  );
}
