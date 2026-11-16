"use client";
// The pipeline editor (docs/mock/PipelineGraph.dc.html, plan 16.2): the pipeline's graph (components/pipelines/canvas,
// loaded only here), the selected node's settings in the side panel, and its last batch's run table. Edits are
// checked by lib/pipeline and POST /pipelines/validate, saved with debounce by PUT with the version (409 reloads),
// and undone with ⌘Z / ⇧⌘Z. Run opens New batch with the pipeline's inputs as columns. Agent nodes (M4b stage A):
// the Judge panel (checklist, keep, retry, mode, model, memory: PipelineGraph.dc.html's aside), Write and Decide
// (docs/mock/AgentNodes.dc.html), Memory (read or write) and the Memory dialog. Stage B (docs/mock/AgentSettings.dc.html,
// DirectorChat.dc.html): the Agent panel (instruction, reads, tools, how it ends, caps, model source, director), a
// step's fan-out, a custom node's fields, the pipeline's Remote calls, and Chat with director for the last batch. The
// mock's Studio | Graph switch, memory meter and "Test on takes" have no backend yet and are left out.
import dynamic from "next/dynamic";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { Activity, ChevronLeft, Plus, X } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { FullScreen } from "@/components/media";
import { Head, Problems } from "@/components/graph/panel";
import { MemoryDialog, useMemory } from "@/components/pipelines/memory";
import { NewBatch } from "@/components/pipelines/new-batch";
import { followBatch } from "@/components/pipelines/pipelines";
import { ChatPanel, useChat } from "@/components/pipelines/chat";
import { rowMarks } from "@/lib/chat";
import { RunTable } from "@/components/pipelines/run-table";
import type { CanvasProps } from "@/components/pipelines/canvas";
import { api, ApiError, errorText, message, ok, type S } from "@/lib/api";
import { applyBatchEvent, type BatchView } from "@/lib/batches";
import { docOf, mergeProblems, paletteOf, type Doc, type Palette, type Problem } from "@/lib/graph";
import { history, historyOf } from "@/lib/graph-edit";
import {
  AGENT_TOOLS,
  agentCaps,
  bindCandidates,
  customOutputs,
  directorOf,
  fanCandidates,
  fieldKind,
  chooseWorkflow,
  connectNodes,
  disconnectNodes,
  edgeOut,
  gateCandidates,
  KIND_NAMES,
  memoryCandidates,
  nodeLabel,
  order,
  outputCandidates,
  pipelineProblems,
  removePipelineNodes,
  resolveModels,
  setCap,
  setNode,
  setRoutes,
  setThen,
  settable,
  stepNumbers,
  stepTakes,
  textCandidates,
  WRITE_INSTRUCTION,
  type PDoc,
  type PProblem,
  type RuleEnv,
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
const docOfPipeline = (p: Pipeline): PDoc => ({
  title: p.title,
  nodes: p.nodes,
  edges: p.edges,
  ...(p.remote ? { remote: p.remote } : {}), // its Remote calls setting is part of the document
});
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
  asks = [],
  nodes,
  sources = null,
  custom = [],
}: {
  pipeline: Pipeline;
  workflows: Workflow[];
  /** the ask workflows a judge, write or decide may run (never listed: read by id) */
  asks?: Workflow[];
  nodes: S<"NodePalette"> | null;
  /** the local servers and remote providers a model may name (GET /model-sources) */
  sources?: S<"ModelSourceList"> | null;
  /** the custom nodes pipelines_service registered (GET /custom-nodes) */
  custom?: S<"CustomNode">[];
}) {
  const [h, dispatch] = useReducer(history<PDoc>, pipeline, (p) => historyOf(docOfPipeline(p)));
  const doc = h.present;
  const palette = useMemo(
    () => (nodes ? paletteOf(nodes) : paletteFromWorkflows(workflows)),
    [nodes, workflows],
  );
  const wfDocs = useMemo(
    () =>
      Object.fromEntries([...workflows, ...asks].map((w) => [w.id, docOf(w)])) as Record<
        string,
        Doc
      >,
    [workflows, asks],
  );
  const titles = useMemo(
    () => ({
      ...pipeline.workflow_titles,
      ...Object.fromEntries([...workflows, ...asks].map((w) => [w.id, w.title])),
      ...Object.fromEntries(custom.map((c) => [`custom:${c.name}`, c.title])),
    }),
    [pipeline.workflow_titles, workflows, asks, custom],
  );
  const env: RuleEnv = useMemo(
    () => ({
      providers: sources?.sources ?? [],
      remoteOn: sources?.remote_enabled ?? false,
      custom,
    }),
    [sources, custom],
  );

  const [cur, setCur] = useState(pipeline);
  const base = useRef(pipeline);
  const saved = useRef(doc);
  const latest = useRef(doc);
  useEffect(() => {
    latest.current = doc;
  });
  const [server, setServer] = useState<{
    doc: PDoc;
    problems: PProblem[];
    models?: Record<string, string>;
  } | null>(null);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const memory = useMemory(pipeline.id);
  const [saving, setSaving] = useState<"" | "saving" | "failed">("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [flowKey, setFlowKey] = useState(0);
  const [running, setRunning] = useState(false);

  const client = useMemo(
    () => pipelineProblems(doc, wfDocs, palette, undefined, {}, undefined, env),
    [doc, wfDocs, palette, env],
  );
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
      let canSave = !pipelineProblems(d, wfDocs, palette, undefined, {}, undefined, env).some(
        (p) => p.blocks === "save",
      );
      try {
        const v = await ok(api.POST("/v1/studio/pipelines/validate", { body: d }));
        setServer({ doc: d, problems: v.problems, models: v.models });
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
    [wfDocs, palette, env],
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
  // the models as studio resolves them (the client's own until it has answered for this document)
  const models =
    server?.doc === doc && server.models ? server.models : resolveModels(doc, wfDocs, env);
  // the director's chat with the last batch (docs/mock/DirectorChat.dc.html)
  const director = directorOf(doc);
  const [chatOpen, setChatOpen] = useState(false);
  const chat = useChat(run?.batch.director ? (lastId ?? null) : null);
  const badge = [
    "Unattended",
    plural(count("step"), "step"),
    count("gate") ? plural(count("gate"), "gate") : "",
    count("judge") ? plural(count("judge"), "judge") : "",
    count("agent") ? plural(count("agent"), "agent") : "",
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
          {director && run?.batch.director && (
            <button
              type="button"
              className={`chip bevel ${chatOpen ? "bg-t1 font-semibold text-panel" : ""}`}
              aria-pressed={chatOpen}
              onClick={() => setChatOpen((o) => !o)}
            >
              Chat with director
            </button>
          )}
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
            custom={custom}
            models={models}
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
                marks={chat.view ? rowMarks(chat.view.messages) : undefined}
              />
            </div>
          </>
        )}
      </main>
      <aside
        aria-label="Inspector"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-[18px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        {chatOpen && run?.batch.director ? (
          <ChatPanel batch={run.batch} chat={chat} onClose={() => setChatOpen(false)} />
        ) : sel ? (
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
            asks={asks}
            models={models}
            memory={memory.list?.counts ?? null}
            onOpenMemory={() => setMemoryOpen(true)}
            sources={sources}
            custom={custom}
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
            <RemotePanel doc={doc} sources={sources} edit={edit} />
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
      <MemoryDialog
        open={memoryOpen}
        onClose={() => {
          setMemoryOpen(false);
          void memory.load();
        }}
        pipelineId={pipeline.id}
        title={doc.title}
        name={(id) => nodeLabel(doc, id, titles)}
      />
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
  asks: Workflow[];
  /** node id → the ask workflow it resolves to now */
  models: Record<string, string>;
  memory: S<"MemoryList">["counts"] | null;
  onOpenMemory: () => void;
  sources: S<"ModelSourceList"> | null;
  custom: S<"CustomNode">[];
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
  const kindName = KIND_NAMES[n.kind];
  const nums = stepNumbers(doc);
  const targets: [string, string][] = order(doc)
    .filter((x) => x !== id && doc.nodes[x].kind !== "inputs")
    .map((x) => [x, `${nums[x] ? `${nums[x]} · ` : ""}${nodeLabel(doc, x, titles)}`]);
  const out = edgeOut(doc, id)?.[1];
  const next = typeof out === "string" ? out : out && "pass" in out ? out.pass : "";
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
      {n.kind === "judge" && <JudgePanel {...props} targets={targets} />}
      {n.kind === "write" && <WritePanel {...props} />}
      {n.kind === "decide" && <DecidePanel {...props} targets={targets} />}
      {n.kind === "memory" && <MemoryNodePanel {...props} />}
      {n.kind === "agent" && <AgentPanel {...props} targets={targets} />}
      {n.kind === "custom" && <CustomPanel {...props} />}
      {n.kind === "output" && <OutputPanel {...props} />}
      {(n.kind === "inputs" ||
        n.kind === "step" ||
        n.kind === "write" ||
        n.kind === "memory" ||
        n.kind === "custom" ||
        (n.kind === "agent" && !n.routes)) && (
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

function StepPanel({
  doc,
  id,
  problems,
  workflows,
  wfDocs,
  palette,
  titles,
  edit,
  custom,
}: PanelProps) {
  const n = doc.nodes[id] as S<"PipelineStepNode">;
  const wf = workflows.find((w) => w.id === n.workflow);
  const wd = wfDocs[n.workflow];
  const params = wd ? settable(wd, palette) : {};
  const presets = Object.keys(wf?.studio?.presets ?? {});
  const bind = n.bind ?? {};
  const rows = Object.entries(params)
    .map(([k, p]) => ({ k, p, c: bindCandidates(doc, id, p, custom) }))
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
      <FanOut {...{ doc, id, problems, titles, edit }} />
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

/** A step's fan-out (docs/mock/AgentSettings.dc.html "Runs once per"): once per row, or once per item of a Write's or
 * Agent's list (one branch per item, at most its declared max; binds read the branch's `<producer>.item`). */
function FanOut({
  doc,
  id,
  problems,
  titles,
  edit,
}: Pick<PanelProps, "doc" | "id" | "problems" | "titles" | "edit">) {
  const n = doc.nodes[id] as S<"PipelineStepNode">;
  const c = fanCandidates(doc, id);
  const each = n.each?.split(".")[0];
  if (!c.length && !each) return null;
  const p = each ? doc.nodes[each] : undefined;
  const max = p && (p.kind === "write" || p.kind === "agent") ? (p.list?.max ?? 0) : 0;
  const seeds = n.seeds ?? 1;
  return (
    <>
      <Choice
        id="pp-each"
        label="Runs once per"
        value={n.each ?? ""}
        error={problems.find((x) => x.port === "each")?.message}
        options={[
          ["", "row"],
          ...[...new Set([...c, ...(each ? [each] : [])])].map(
            (x) => [`${x}.items`, `${nodeLabel(doc, x, titles)} · items`] as [string, string],
          ),
        ]}
        onChange={(v) => {
          const { each: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
          // binds of the old producer's item go with it
          const bind = Object.fromEntries(
            Object.entries(n.bind ?? {}).filter(([, s]) => !s.endsWith(".item")),
          );
          edit((d) => setNode(d, id, v ? { ...rest, bind, each: v } : { ...rest, bind }));
        }}
      />
      {each && max > 0 && (
        <div className="set flex items-center justify-between gap-2 py-2 text-[13px]">
          at most {max} branches × {seeds} seed{seeds === 1 ? "" : "s"}
          <span className="text-[11px] text-t2">{max * seeds} runs a row</span>
        </div>
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
  const n = doc.nodes[id] as S<"PipelineGateNode"> | S<"PipelineJudgeNode">;
  const judge = n.kind === "judge";
  const steps = gateCandidates(doc, id);
  const opts = n.takes && !steps.includes(n.takes) ? [n.takes, ...steps] : steps;
  const made = doc.nodes[n.takes]?.kind === "step" ? stepTakes(doc)[n.takes] : 8; // seeds × fan-out
  const out = edgeOut(doc, id)?.[1];
  const branch = typeof out === "object" && "pass" in out ? out : null;
  const pass = typeof out === "string" ? out : (branch?.pass ?? "");
  return (
    <>
      <Choice
        id="pp-takes"
        label={judge ? "Judges the takes of" : "Pick from"}
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
                {judge || i === 1 ? `best ${i} of ${made}` : `up to ${i} of ${made}`}
              </option>
            ))}
          </select>
        </label>
        <label className="set flex flex-col gap-1">
          <small className="text-[12px] text-t2">{judge ? "If it passes" : "If picked"}</small>
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
            <option value="">{judge ? "Ask me / fail the row" : "Fail the row"}</option>
            {targets.map(([v, l]) => (
              <option key={v} value={v}>
                {judge ? `Retry: ${l}` : `Retry at ${l}`}
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
      {!judge && (
        <p className="m-0 text-[12px] text-t2">
          The row parks here until you pick from the review grid; parked rows never hold up the
          queue.
        </p>
      )}
    </>
  );
}

/** The model a judge, write, decide or agent runs on (docs/mock/AgentSettings.dc.html "Runs on"): Auto (the step's
 * family's own vision model, else the llm pack's), a named ask workflow, and (not for a judge, which sends
 * thumbnails) a local server's or, when this deployment allows them, a remote provider's models. */
function ModelChoice({
  doc,
  id,
  asks,
  models,
  titles,
  edit,
  label,
  sources,
}: PanelProps & { label: string }) {
  const n = doc.nodes[id] as
    | S<"PipelineJudgeNode">
    | S<"PipelineWriteNode">
    | S<"PipelineDecideNode">
    | S<"PipelineAgentNode">;
  const m = n.model ?? "auto";
  const now = models[id];
  const title = (w: string) => titles[w] ?? w;
  const others = n.kind === "judge" ? [] : (sources?.sources ?? []);
  const group = (kind: "server" | "remote") =>
    others
      .filter((p) => p.kind === kind)
      .flatMap((p) =>
        p.models.map((x) => [`${kind}:${p.name}/${x}`, `${p.title || p.name} · ${x}`] as const),
      );
  const servers = group("server");
  const remotes = group("remote");
  const known = [...asks.map((w) => w.id), ...servers, ...remotes].map((x) =>
    typeof x === "string" ? x : x[0],
  );
  return (
    <label className="set flex flex-col gap-1">
      <small className="text-[12px] text-t2">{label}</small>
      <select
        className="bg-transparent font-semibold text-t1"
        value={m}
        onChange={(e) => {
          const { model: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
          edit((d) =>
            setNode(
              d,
              id,
              (e.target.value === "auto" ? rest : { ...rest, model: e.target.value }) as PNodeOf,
            ),
          );
        }}
      >
        <optgroup label="Local">
          <option value="auto">{now && m === "auto" ? `Auto · ${title(now)}` : "Auto"}</option>
          {asks.map((w) => (
            <option key={w.id} value={w.id}>
              {w.title}
            </option>
          ))}
        </optgroup>
        {servers.length > 0 && (
          <optgroup label="Local server">
            {servers.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </optgroup>
        )}
        {remotes.length > 0 && (
          <optgroup label="Remote">
            {remotes.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </optgroup>
        )}
        {m !== "auto" && !known.includes(m) && <option value={m}>{title(m)}</option>}
      </select>
    </label>
  );
}
type PNodeOf = S<"PipelineNode">;

/** A write's, decide's, agent's or memory write's sources (`from`): a list with remove, and Add a source. */
function TextSources(props: PanelProps) {
  const { doc, id, titles, edit, problems, custom } = props;
  const n0 = doc.nodes[id] as { from?: string[] } & PNodeOf;
  const n = { ...n0, from: n0.from ?? [] };
  const c = textCandidates(doc, id, custom).filter((x) => !n.from.includes(x));
  const set = (from: string[]) => edit((d) => setNode(d, id, { ...n, from } as PNodeOf));
  return (
    <section aria-labelledby={`pp-reads-${id}`} className="flex flex-col gap-1.5">
      <h3 id={`pp-reads-${id}`} className="heading">
        Reads
      </h3>
      {n.from.map((src) => (
        <div key={src} className="set flex items-center justify-between gap-2 text-[13px]">
          {sourceLabel(doc, src, titles)}
          <button
            type="button"
            className="rounded-md px-1.5 py-0.5 text-[11px] text-t2 hover:bg-s3 hover:text-t1"
            aria-label={`Remove ${sourceLabel(doc, src, titles)}`}
            onClick={() => set(n.from.filter((x) => x !== src))}
          >
            remove
          </button>
        </div>
      ))}
      {problems.find((p) => p.port === "from") && (
        <span className="text-[12px] text-bad">
          {problems.find((p) => p.port === "from")!.message}
        </span>
      )}
      {n.from.length < 4 && c.length > 0 && (
        <select
          aria-label="Add a source"
          className="h-9 rounded-[10px] border border-dashed border-dash bg-transparent px-2 text-[13px] text-t1"
          value=""
          onChange={(e) => e.target.value && set([...n.from, e.target.value])}
        >
          <option value="">Add a source</option>
          {c.map((x) => (
            <option key={x} value={x}>
              {sourceLabel(doc, x, titles)}
            </option>
          ))}
        </select>
      )}
    </section>
  );
}

/** The Judge panel (docs/mock/PipelineGraph.dc.html's aside): checklist, keep and the retry branch (GatePanel),
 * Trust (Suggest | Auto), the model, and memory. */
function JudgePanel(props: PanelProps & { targets: [string, string][] }) {
  const { doc, id, problems, edit, models, titles, memory, onOpenMemory } = props;
  const n = doc.nodes[id] as S<"PipelineJudgeNode">;
  const set = (v: Partial<S<"PipelineJudgeNode">>) => edit((d) => setNode(d, id, { ...n, ...v }));
  const lines = [
    ...(n.criteria.must ?? []).map((t) => ({ t, must: true })),
    ...(n.criteria.nice ?? []).map((t) => ({ t, must: false })),
  ];
  const setLines = (ls: { t: string; must: boolean }[]) =>
    set({
      criteria: {
        must: ls.filter((l) => l.must).map((l) => l.t),
        nice: ls.filter((l) => !l.must).map((l) => l.t),
      },
    });
  const mems = memoryCandidates(doc, id);
  const seeds = (doc.nodes[n.takes] as S<"PipelineStepNode"> | undefined)?.seeds ?? 1;
  return (
    <>
      <p className="m-0 flex items-center text-[12px] text-t2">
        Looks at each row&apos;s {seeds} take{seeds === 1 ? "" : "s"} and decides what goes on.
        <Info>
          Runs locally on {models[id] ? (titles[models[id]] ?? models[id]) : "no model yet"}, as a
          queue item like any step. On Auto it uses the step&apos;s own vision model when its family
          has one, so no extra model is loaded.
        </Info>
      </p>
      <section aria-labelledby="pp-checks" className="flex flex-col gap-1.5">
        <h3 id="pp-checks" className="heading">
          Checklist
        </h3>
        {lines.map((l, i) => (
          <div key={i} className="set flex items-center gap-2 py-1.5">
            <input
              aria-label={`Check ${i + 1}`}
              className="min-w-0 flex-1 bg-transparent text-[13px] text-t1"
              maxLength={200}
              value={l.t}
              onChange={(e) =>
                setLines(lines.map((x, j) => (j === i ? { ...x, t: e.target.value } : x)))
              }
            />
            <button
              type="button"
              className="rounded-md px-1.5 py-0.5 text-[11px] whitespace-nowrap text-t2 hover:bg-s3"
              aria-label={`Check ${i + 1}: ${l.must ? "must" : "nice to have"}; switch`}
              onClick={() => setLines(lines.map((x, j) => (j === i ? { ...x, must: !x.must } : x)))}
            >
              {l.must ? "must" : "nice to have"}
            </button>
            <button
              type="button"
              aria-label={`Remove check ${i + 1}`}
              className="grid size-6 shrink-0 place-items-center rounded-md text-t2 hover:bg-s3"
              onClick={() => setLines(lines.filter((_, j) => j !== i))}
            >
              <X size={12} aria-hidden />
            </button>
          </div>
        ))}
        {problems
          .filter((p) => p.code === "judge_criteria")
          .map((p) => (
            <span key={p.code} className="text-[12px] text-bad">
              {p.message}
            </span>
          ))}
        {lines.length < 12 && (
          <button
            type="button"
            className="h-9 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
            onClick={() => setLines([...lines, { t: `Check ${lines.length + 1}`, must: true }])}
          >
            Add a check
          </button>
        )}
      </section>
      <GatePanel {...props} />
      <section aria-labelledby="pp-trust" className="flex flex-col gap-2">
        <h3 id="pp-trust" className="heading flex items-center">
          Trust
          <Info>
            Suggest: the judge pre-selects and says why; you confirm each pick. Switch to Auto once
            it agrees with you often enough.
          </Info>
        </h3>
        <div
          role="group"
          aria-label="Judge mode"
          className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
        >
          {(["suggest", "auto"] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={(n.mode ?? "suggest") === m}
              className="h-[34px] flex-1 rounded-lg border-0 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1"
              onClick={() => set({ mode: m })}
            >
              {m === "suggest" ? "Suggest" : "Auto"}
            </button>
          ))}
        </div>
      </section>
      <section aria-labelledby="pp-model" className="flex flex-col gap-2">
        <h3 id="pp-model" className="heading">
          Model
        </h3>
        <ModelChoice {...props} label="Judge runs on" />
        {problems
          .filter((p) => p.code === "unknown_model")
          .map((p) => (
            <span key={p.code} className="text-[12px] text-bad">
              {p.message}
            </span>
          ))}
      </section>
      <section aria-labelledby="pp-memory" className="flex flex-col gap-2">
        <h3 id="pp-memory" className="heading flex items-center">
          Memory
          <Info>
            Read once when the batch starts, so every row is judged the same way. What it learns
            tonight is used next time.
          </Info>
        </h3>
        <Choice
          id="pp-mem"
          label="Learns from"
          value={n.memory ?? ""}
          error={problems.find((p) => p.port === "memory")?.message}
          options={[
            ["", mems.length ? "Off" : "Off (add a Memory node before it)"],
            ...mems.map((m) => [m, nodeLabel(doc, m, titles)] as [string, string]),
            ...(n.memory && !mems.includes(n.memory)
              ? [[n.memory, n.memory] as [string, string]]
              : []),
          ]}
          onChange={(v) => {
            const { memory: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
            edit((d) => setNode(d, id, v ? { ...rest, memory: v } : rest));
          }}
        />
        <div className="set flex items-center justify-between text-[13px]">
          Examples from your picks
          <span className="text-[11px] text-t2">
            {memory ? `${memory.judge_example} takes` : "—"}
          </span>
        </div>
        <div className="set flex items-center justify-between text-[13px]">
          Notes
          <span className="text-[11px] text-t2">{memory ? `${memory.note} notes` : "—"}</span>
        </div>
        <button type="button" className="chip bevel self-start" onClick={onOpenMemory}>
          Open memory
        </button>
      </section>
    </>
  );
}

/** The Write panel (docs/mock/AgentNodes.dc.html): what it reads, the Text input it writes, the instruction, the
 * model. */
function WritePanel(props: PanelProps) {
  const { doc, id, problems, edit } = props;
  const n = doc.nodes[id] as S<"PipelineWriteNode">;
  const a = Object.values(doc.nodes).flatMap((x) => (x.kind === "inputs" ? x.inputs : []));
  const texts = a.filter((i) => i.type === "Text").map((i) => `$${i.name}`);
  return (
    <>
      <p className="m-0 flex items-center text-[12px] text-t2">
        Writes text into a Text input for the rest of the row.
        <Info>
          Later steps that bind the input read the new text. @ mentions in the answer are expanded
          like any prompt; looks are never added.
        </Info>
      </p>
      <TextSources {...props} />
      <Choice
        id="pp-to"
        label="Writes to"
        value={n.list ? "list" : (n.to ?? "")}
        error={problems.find((p) => p.code === "write_target")?.message}
        options={[
          ...(n.to === undefined || texts.includes(n.to)
            ? []
            : ([[n.to, n.to]] as [string, string][])),
          ...texts.map((t) => [t, sourceLabel(doc, t, {})] as [string, string]),
          ["list", "a list (a step fans out over it)"],
        ]}
        onChange={(v) => {
          const { to: _t, list: _l, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
          edit((d) =>
            setNode(
              d,
              id,
              v === "list" ? { ...rest, list: n.list ?? { max: 12 } } : { ...rest, to: v },
            ),
          );
        }}
      />
      {n.list && (
        <Choice
          id="pp-wlist"
          label="Items at most"
          value={String(n.list.max)}
          options={range(24).map((i) => [String(i), String(i)])}
          onChange={(v) => edit((d) => setNode(d, id, { ...n, list: { max: Number(v) } }))}
        />
      )}
      <label className="flex flex-col gap-1.5">
        <span className="heading">Instruction</span>
        <textarea
          className="in h-auto py-2"
          rows={3}
          maxLength={2000}
          value={n.instruction ?? WRITE_INSTRUCTION}
          onChange={(e) => edit((d) => setNode(d, id, { ...n, instruction: e.target.value }))}
        />
      </label>
      <ModelChoice {...props} label="Runs on" />
    </>
  );
}

/** The Decide panel (docs/mock/AgentNodes.dc.html): the question, what it reads, its routes (label, meaning, where
 * each goes), the cap and the route the cap forces. */
function DecidePanel(props: PanelProps & { targets: [string, string][] }) {
  const { doc, id, edit } = props;
  const n = doc.nodes[id] as S<"PipelineDecideNode">;
  return (
    <>
      <label className="flex flex-col gap-1.5">
        <span className="heading">Question</span>
        <textarea
          className="in h-auto py-2"
          rows={2}
          maxLength={1000}
          value={n.question}
          onChange={(e) => edit((d) => setNode(d, id, { ...n, question: e.target.value }))}
        />
      </label>
      <TextSources {...props} />
      <RoutesEditor {...props} />
      <ModelChoice {...props} label="Runs on" />
    </>
  );
}

/** A decide's or an agent's routes: label, meaning and where each goes, the cap and the route the cap forces. */
function RoutesEditor({
  doc,
  id,
  edit,
  targets,
  problems,
}: PanelProps & { targets: [string, string][] }) {
  const n = doc.nodes[id] as S<"PipelineDecideNode"> | S<"PipelineAgentNode">;
  const out = edgeOut(doc, id)?.[1];
  const edge = typeof out === "object" && "routes" in out ? out : null;
  const routes = Object.entries(n.routes ?? {});
  const relabel = (i: number, label: string, meaning: string) => {
    const next = Object.fromEntries(
      routes.map(([l, m], j) => (j === i ? [label, meaning] : [l, m])),
    );
    if (Object.keys(next).length === routes.length) edit((d) => setRoutes(d, id, next));
  };
  return (
    <>
      <section aria-labelledby={`pp-routes-${id}`} className="flex flex-col gap-1.5">
        <h3 id={`pp-routes-${id}`} className="heading">
          Routes
        </h3>
        {routes.map(([label, meaning], i) => (
          <div key={i} className="set flex flex-col gap-1.5">
            <div className="flex gap-1.5">
              <input
                aria-label={`Route ${i + 1} label`}
                className="in mono h-[34px] w-24"
                value={label}
                onChange={(e) =>
                  relabel(
                    i,
                    e.target.value
                      .toLowerCase()
                      .replace(/[^a-z0-9_]/g, "_")
                      .replace(/^[^a-z]+/, "")
                      .slice(0, 32) || "x",
                    meaning,
                  )
                }
              />
              <input
                aria-label={`Route ${i + 1}: what it means`}
                className="in h-[34px]"
                maxLength={200}
                value={meaning}
                onChange={(e) => relabel(i, label, e.target.value)}
              />
              {routes.length > 2 && (
                <button
                  type="button"
                  aria-label={`Remove route ${label}`}
                  className="grid size-[34px] shrink-0 place-items-center rounded-lg bg-s3"
                  onClick={() =>
                    edit((d) =>
                      setRoutes(d, id, Object.fromEntries(routes.filter((_, j) => j !== i))),
                    )
                  }
                >
                  <X size={14} aria-hidden />
                </button>
              )}
            </div>
            <Choice
              id={`pp-r-${i}`}
              label="goes to"
              value={edge?.routes[label] ?? ""}
              options={[["", "Nowhere yet"], ...targets]}
              onChange={(v) => v && edit((d) => connectNodes(d, id, `r:${label}`, v))}
            />
          </div>
        ))}
        {problems
          .filter((p) => p.code === "decide_routes")
          .map((p) => (
            <span key={p.code} className="text-[12px] text-bad">
              {p.message}
            </span>
          ))}
        {routes.length < 6 && (
          <button
            type="button"
            className="h-9 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
            onClick={() => {
              let k = routes.length + 1;
              while (`route_${k}` in (n.routes ?? {})) k++;
              edit((d) => setRoutes(d, id, { ...n.routes, [`route_${k}`]: "" }));
            }}
          >
            Add a route
          </button>
        )}
      </section>
      {edge && (
        <div className="grid grid-cols-2 gap-2">
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Routes at most</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={edge.max}
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
              value={edge.then}
              onChange={(e) => edit((d) => setThen(d, id, e.target.value))}
            >
              {routes.map(([l]) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
    </>
  );
}

const TOKEN_CAPS = [4000, 8000, 16000, 32000, 64000, 100000, 200000];
const WAKES = [10, 25, 50, 100, 200, 500];
type AgentEnd = "next" | "routes" | "list" | "both";

/** The Agent panel (docs/mock/AgentSettings.dc.html): instruction, what it reads, its tools, how a visit ends (the
 * next node, a route, a list for a fan-out), its caps per visit, the model, and whether it is the director. */
function AgentPanel(props: PanelProps & { targets: [string, string][] }) {
  const { doc, id, edit, problems } = props;
  const n = doc.nodes[id] as S<"PipelineAgentNode">;
  const set = (v: Partial<S<"PipelineAgentNode">>) => edit((d) => setNode(d, id, { ...n, ...v }));
  const caps = agentCaps(n);
  const tools = n.tools ?? [];
  const end: AgentEnd = n.routes ? (n.list ? "both" : "routes") : n.list ? "list" : "next";
  const setEnd = (e: AgentEnd) =>
    edit((d) => {
      const { routes: _r, list: _l, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
      const withRoutes = e === "routes" || e === "both";
      const next: S<"PipelineAgentNode"> = {
        ...rest,
        ...(withRoutes && { routes: n.routes ?? { again: "one more try", done: "finish" } }),
        ...((e === "list" || e === "both") && { list: n.list ?? { max: 12 } }),
      };
      // a routes edge needs routes and a plain edge needs none: the old edge goes when that changes
      const old = edgeOut(d, id)?.[1];
      const isRoutes = typeof old === "object" && "routes" in old;
      const keep = old === undefined || isRoutes === withRoutes;
      const d2 = setNode(d, id, next);
      return keep ? d2 : { ...d2, edges: d2.edges.filter((x) => x[0] !== id) };
    });
  const setCaps = (v: Partial<typeof caps>) => set({ caps: { ...caps, ...v } });
  return (
    <>
      <p className="m-0 flex items-center text-[12px] text-t2">
        An LLM with tools, in a loop of turns.
        <Info>
          Each turn is a queue item like any step. A visit ends with no tool call, with a route, or
          with its list; reaching a cap fails the row.
        </Info>
      </p>
      <label className="flex flex-col gap-1.5">
        <span className="heading">Instruction</span>
        <textarea
          className="in h-auto py-2"
          rows={3}
          maxLength={4000}
          value={n.instruction}
          onChange={(e) => set({ instruction: e.target.value })}
        />
      </label>
      <TextSources {...props} />
      <section aria-labelledby={`pp-tools-${id}`} className="flex flex-col gap-1">
        <h3 id={`pp-tools-${id}`} className="heading">
          Tools
        </h3>
        {AGENT_TOOLS.map(([t, label]) => {
          const off = t === "rows.edit" && !n.director && !tools.includes(t);
          return (
            <label key={t} className="flex min-h-[30px] items-center gap-2.5 text-[13px]">
              <input
                type="checkbox"
                className="accent-[var(--a2)]"
                checked={tools.includes(t)}
                disabled={off}
                onChange={(e) =>
                  set({
                    tools: e.target.checked
                      ? AGENT_TOOLS.map(([x]) => x).filter((x) => x === t || tools.includes(x))
                      : tools.filter((x) => x !== t),
                  })
                }
              />
              {label}
            </label>
          );
        })}
        {problems
          .filter((p) => p.code === "agent_tools")
          .map((p) => (
            <span key={p.code} className="text-[12px] text-bad">
              {p.message}
            </span>
          ))}
      </section>
      <Choice
        id="pp-end"
        label="Ends with"
        value={end}
        options={[
          ["next", "one next node"],
          ["routes", "a route"],
          ["list", `a list · at most ${n.list?.max ?? 12}`],
          ...(end === "both" ? ([["both", "a list, then a route"]] as [string, string][]) : []),
        ]}
        onChange={(v) => setEnd(v as AgentEnd)}
      />
      {n.list && (
        <Choice
          id="pp-list"
          label="Items at most"
          value={String(n.list.max)}
          options={range(24).map((i) => [String(i), String(i)])}
          onChange={(v) => set({ list: { max: Number(v) } })}
        />
      )}
      {n.routes && <RoutesEditor {...props} />}
      <section aria-labelledby={`pp-caps-${id}`} className="flex flex-col gap-2">
        <h3 id={`pp-caps-${id}`} className="heading flex items-center">
          Caps per visit
          <Info>
            Hard limits; reaching one ends the visit and fails the row. Submit&apos;s worst case
            counts them.
          </Info>
        </h3>
        <div className="grid grid-cols-3 gap-2">
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Turns</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={caps.max_turns}
              onChange={(e) => setCaps({ max_turns: Number(e.target.value) })}
            >
              {range(16).map((i) => (
                <option key={i}>{i}</option>
              ))}
            </select>
          </label>
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Tool calls</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={caps.max_tool_calls}
              onChange={(e) => setCaps({ max_tool_calls: Number(e.target.value) })}
            >
              {[0, ...range(32)].map((i) => (
                <option key={i}>{i}</option>
              ))}
            </select>
          </label>
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Tokens</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={caps.max_tokens}
              onChange={(e) => setCaps({ max_tokens: Number(e.target.value) })}
            >
              {[...new Set([...TOKEN_CAPS, caps.max_tokens])]
                .sort((a, b) => a - b)
                .map((i) => (
                  <option key={i} value={i}>
                    {i.toLocaleString("en-US")}
                  </option>
                ))}
            </select>
          </label>
        </div>
      </section>
      <ModelChoice {...props} label="Runs on" />
      {problems
        .filter((p) => p.code === "unknown_model" || p.code.startsWith("remote_"))
        .map((p) => (
          <span key={p.code} className="text-[12px] text-bad">
            {p.message}
          </span>
        ))}
      <label className="flex min-h-[30px] items-center gap-2.5 text-[13px] font-semibold">
        <input
          type="checkbox"
          className="accent-[var(--a2)]"
          checked={!!n.director}
          onChange={(e) => {
            const {
              director: _d, // eslint-disable-line @typescript-eslint/no-unused-vars
              wake_on_rows: _w, // eslint-disable-line @typescript-eslint/no-unused-vars
              max_wakes: _m, // eslint-disable-line @typescript-eslint/no-unused-vars
              ...rest
            } = n;
            edit((d) =>
              setNode(
                d,
                id,
                e.target.checked
                  ? { ...rest, director: true }
                  : { ...rest, tools: tools.filter((t) => t !== "rows.edit") },
              ),
            );
          }}
        />
        Director: its batches get a chat
      </label>
      {problems
        .filter((p) => p.code === "director_count")
        .map((p) => (
          <span key={p.code} className="text-[12px] text-bad">
            {p.message}
          </span>
        ))}
      {n.director && (
        <div className="grid grid-cols-2 gap-2">
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Rows that wake it</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={n.wake_on_rows ?? "all"}
              onChange={(e) =>
                set({ wake_on_rows: e.target.value as S<"PipelineAgentNode">["wake_on_rows"] })
              }
            >
              <option value="all">Every row</option>
              <option value="failed">Failed rows</option>
              <option value="none">None (messages only)</option>
            </select>
          </label>
          <label className="set flex flex-col gap-1">
            <small className="text-[12px] text-t2">Wakes at most</small>
            <select
              className="bg-transparent font-semibold text-t1"
              value={n.max_wakes ?? 100}
              onChange={(e) => set({ max_wakes: Number(e.target.value) })}
            >
              {[...new Set([...WAKES, n.max_wakes ?? 100])]
                .sort((a, b) => a - b)
                .map((i) => (
                  <option key={i}>{i}</option>
                ))}
            </select>
          </label>
        </div>
      )}
    </>
  );
}

/** A custom node (docs/mock/AgentSettings.dc.html "Prefix text"): its registered fields, each from a source or a
 * fixed value, and the fields it gives. */
function CustomPanel({ doc, id, problems, titles, edit, custom }: PanelProps) {
  const n = doc.nodes[id] as S<"PipelineCustomNode">;
  const reg = custom.find((c) => c.name === n.node);
  type F = { type?: string; maxItems?: number; maxLength?: number };
  const fields = Object.entries(
    ((reg?.input as { properties?: Record<string, F> } | undefined)?.properties ?? {}) as Record<
      string,
      F
    >,
  );
  const required = new Set((reg?.input as { required?: string[] } | undefined)?.required ?? []);
  const text = textCandidates(doc, id, custom);
  const setField = (f: string, v: { bind: string } | { value: unknown } | null) =>
    edit((d) => {
      const bind = { ...(n.bind ?? {}) };
      const inputs: Record<string, unknown> = { ...(n.inputs ?? {}) };
      delete bind[f];
      delete inputs[f];
      if (v && "bind" in v) bind[f] = v.bind;
      if (v && "value" in v) inputs[f] = v.value;
      return setNode(d, id, { ...n, bind, inputs } as S<"PipelineCustomNode">);
    });
  if (!reg)
    return (
      <p className="m-0 text-[13px] text-t2">
        {n.node} is not registered on this server now, so it can&apos;t run.
      </p>
    );
  return (
    <>
      <p className="m-0 flex items-center text-[12px] text-t2">
        Custom · {reg.name} · {reg.version}
        <Info>
          {reg.help ||
            "A custom node from a pack on this server: plain code on the row's state, no model."}{" "}
          {reg.network
            ? "It calls the network, so it needs the pipeline's remote calls."
            : "It makes no network call."}
        </Info>
      </p>
      <section aria-labelledby={`pp-fields-${id}`} className="flex flex-col gap-2">
        <h3 id={`pp-fields-${id}`} className="heading">
          Fields
        </h3>
        {fields.map(([f, schema]) => {
          const k = fieldKind(schema as Parameters<typeof fieldKind>[0]);
          const bound = n.bind?.[f];
          const fixed = n.inputs?.[f];
          const err = problems.find((p) => p.port === f)?.message;
          const takes =
            k === "takes"
              ? order(doc).flatMap((x) => {
                  const m = doc.nodes[x];
                  if (x === id) return [];
                  if (m.kind === "step") return [`${x}.takes`];
                  if (m.kind === "gate" || m.kind === "judge") return [`${x}.picked`];
                  return [];
                })
              : [];
          const opts = k === "text" ? text : takes;
          return (
            <div key={f} className="set flex flex-col gap-1.5">
              <small className="text-[12px] text-t2">
                {f}
                {required.has(f) ? "" : " · optional"}
              </small>
              <select
                aria-label={`${f} comes from`}
                className="h-[34px] rounded-lg border border-line bg-s1 px-2 text-[13px] text-t1"
                value={bound ?? (fixed !== undefined ? "=" : "")}
                onChange={(e) => {
                  const v = e.target.value;
                  setField(
                    f,
                    v === ""
                      ? null
                      : v === "="
                        ? { value: schema.type === "string" ? "" : 0 }
                        : { bind: v },
                  );
                }}
              >
                <option value="">{required.has(f) ? "Choose" : "Not set"}</option>
                {k !== "takes" && <option value="=">A fixed value</option>}
                {[...new Set([...opts, ...(bound ? [bound] : [])])].map((s) => (
                  <option key={s} value={s}>
                    {sourceLabel(doc, s, titles)}
                  </option>
                ))}
              </select>
              {fixed !== undefined && bound === undefined && (
                <input
                  aria-label={f}
                  className="in h-[34px]"
                  maxLength={schema.maxLength}
                  value={typeof fixed === "string" ? fixed : JSON.stringify(fixed)}
                  onChange={(e) => {
                    const raw = e.target.value;
                    let value: unknown = raw;
                    if (schema.type !== "string")
                      try {
                        value = JSON.parse(raw);
                      } catch {
                        value = raw;
                      }
                    setField(f, { value });
                  }}
                />
              )}
              {err && <span className="text-[12px] text-bad">{err}</span>}
            </div>
          );
        })}
      </section>
      <section aria-labelledby={`pp-gives-${id}`} className="flex flex-col gap-1.5">
        <h3 id={`pp-gives-${id}`} className="heading">
          Gives
        </h3>
        {customOutputs(reg).map((f) => (
          <div key={f} className="set flex items-center justify-between gap-2 py-2 text-[13px]">
            {f}
            <span className="text-[11px] text-t2">
              {fieldKind(
                (
                  reg.output as unknown as {
                    properties: Record<string, Parameters<typeof fieldKind>[0]>;
                  }
                ).properties[f],
              ) === "text"
                ? "a text source"
                : "takes"}
            </span>
          </div>
        ))}
      </section>
    </>
  );
}

const CALL_CAPS = [50, 100, 200, 500, 1000, 5000, 10000];
const BATCH_TOKENS = [100000, 200000, 400000, 1000000, 4000000, 10000000];

/** The pipeline's Remote calls (docs/mock/AgentSettings.dc.html): off by default; on, its nodes may call the remote
 * providers allowed here, within the batch's caps. Disabled while the deployment's remote switch is off. */
function RemotePanel({
  doc,
  sources,
  edit,
}: {
  doc: PDoc;
  sources: S<"ModelSourceList"> | null;
  edit: Edit;
}) {
  const r = doc.remote ?? {};
  const on = !!r.enabled;
  const allowed = sources?.remote_enabled ?? false;
  const remotes = (sources?.sources ?? []).filter((p) => p.kind === "remote");
  const set = (v: Partial<S<"PipelineRemote">>) =>
    edit((d) => ({ ...d, remote: { ...(d.remote ?? {}), ...v } }));
  const calls = r.max_calls ?? 200;
  const tokens = r.max_tokens ?? 400000;
  if (!allowed && !on) return null;
  return (
    <section aria-labelledby="pp-remote" className="flex flex-col gap-2.5">
      <h3 id="pp-remote" className="heading flex items-center">
        Remote calls
        <Info>
          Off by default. On, nodes may call the remote providers you allow here; Submit shows
          exactly what leaves the machine. The caps stop the batch. Remote answers are never reused
          by Run again.
        </Info>
      </h3>
      {!allowed && (
        <p className="m-0 text-[12px] text-bad">Remote models are off on this deployment.</p>
      )}
      <div
        role="group"
        aria-label="Remote calls"
        className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
      >
        {[false, true].map((v) => (
          <button
            key={String(v)}
            type="button"
            aria-pressed={on === v}
            disabled={!allowed && v}
            className="h-[34px] flex-1 rounded-lg border-0 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1"
            onClick={() => on !== v && set({ enabled: v })}
          >
            {v ? "On" : "Off"}
          </button>
        ))}
      </div>
      {on && (
        <>
          <span className="label">Allowed providers</span>
          {remotes.length === 0 && (
            <p className="m-0 text-[12px] text-t2">The admin has set up no remote provider.</p>
          )}
          {remotes.map((p) => (
            <label key={p.name} className="flex min-h-[30px] items-center gap-2.5 text-[13px]">
              <input
                type="checkbox"
                className="accent-[var(--a2)]"
                checked={(r.providers ?? []).includes(p.name)}
                onChange={(e) =>
                  set({
                    providers: e.target.checked
                      ? [...(r.providers ?? []), p.name]
                      : (r.providers ?? []).filter((x) => x !== p.name),
                  })
                }
              />
              {p.name} · {p.title}
            </label>
          ))}
          <div className="grid grid-cols-2 gap-2">
            <label className="set flex flex-col gap-1">
              <small className="text-[12px] text-t2">Calls a batch</small>
              <select
                className="bg-transparent font-semibold text-t1"
                value={calls}
                onChange={(e) => set({ max_calls: Number(e.target.value) })}
              >
                {[...new Set([...CALL_CAPS, calls])]
                  .sort((a, b) => a - b)
                  .map((i) => (
                    <option key={i} value={i}>
                      {i.toLocaleString("en-US")}
                    </option>
                  ))}
              </select>
            </label>
            <label className="set flex flex-col gap-1">
              <small className="text-[12px] text-t2">Tokens a batch</small>
              <select
                className="bg-transparent font-semibold text-t1"
                value={tokens}
                onChange={(e) => set({ max_tokens: Number(e.target.value) })}
              >
                {[...new Set([...BATCH_TOKENS, tokens])]
                  .sort((a, b) => a - b)
                  .map((i) => (
                    <option key={i} value={i}>
                      {i.toLocaleString("en-US")}
                    </option>
                  ))}
              </select>
            </label>
          </div>
        </>
      )}
    </section>
  );
}

/** A Memory node: read (the batch's snapshot: notes by key, the newest few) or write (a note for the next batches),
 * the same controls as the Write panel's. */
function MemoryNodePanel(props: PanelProps) {
  const { doc, id, edit } = props;
  const n = doc.nodes[id] as S<"PipelineMemoryNode">;
  const base = {
    kind: "memory" as const,
    ...(n.title && { title: n.title }),
    ...(n.position && { position: n.position }),
  };
  return (
    <>
      <div
        role="group"
        aria-label="Memory node"
        className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
      >
        {(["read", "write"] as const).map((op) => (
          <button
            key={op}
            type="button"
            aria-pressed={n.op === op}
            className="h-[34px] flex-1 rounded-lg border-0 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1"
            onClick={() =>
              op !== n.op &&
              edit((d) =>
                setNode(
                  d,
                  id,
                  op === "read"
                    ? { ...base, op, limit: 8 }
                    : { ...base, op, key: "notes", from: textCandidates(d, id).slice(0, 1) },
                ),
              )
            }
          >
            {op === "read" ? "Read" : "Write"}
          </button>
        ))}
      </div>
      <label className="flex flex-col gap-1.5">
        <span className="heading">{n.op === "read" ? "Only notes with the key" : "Key"}</span>
        <input
          className="in mono"
          maxLength={40}
          placeholder={n.op === "read" ? "every note" : "notes"}
          value={n.key ?? ""}
          onChange={(e) => {
            const key = e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "_");
            if (n.op === "write") edit((d) => setNode(d, id, { ...n, key: key || "notes" }));
            else {
              const { key: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
              edit((d) => setNode(d, id, key ? { ...rest, key } : rest));
            }
          }}
        />
      </label>
      {n.op === "read" ? (
        <Choice
          id="pp-limit"
          label="Reads the newest"
          value={String(n.limit ?? 8)}
          options={range(16).map((i) => [String(i), `${i}`])}
          onChange={(v) => edit((d) => setNode(d, id, { ...n, limit: Number(v) }))}
        />
      ) : (
        <TextSources {...props} />
      )}
      <p className="m-0 text-[12px] text-t2">
        {n.op === "read"
          ? "Reads the snapshot taken when the batch starts; a judge set to learn from it adds your past picks there."
          : "Kept for the next batches, one note per row; see them in Memory."}
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
