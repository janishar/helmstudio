"use client";
// Graph view (docs/mock/Graph.dc.html): the open workflow as typed node cards and wires on @xyflow/react, laid out
// left to right by elkjs when it has no positions. Edits go through lib/graph-edit (undo with ⌘Z / ⇧⌘Z), are checked
// by lib/graph and POST /workflows/validate, and saved with debounce (PUT with the version; a template's first edit
// makes "<title> (mine)"). Loaded only when Graph is chosen (next/dynamic), so Create's bundle stays as it was.
import "@xyflow/react/dist/base.css";
import "./graph.css";
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import {
  Handle,
  Position,
  ReactFlow,
  ReactFlowProvider,
  applyEdgeChanges,
  applyNodeChanges,
  useConnection,
  useReactFlow,
  type Connection,
  type Edge,
  type FinalConnectionState,
  type FitViewOptions,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import { Check, Plus } from "lucide-react";
import { Thumb } from "@/components/media";
import { Head, Panel, Problems, nodeName, valueText } from "@/components/graph/panel";
import { api, ApiError, errorText, message, ok, type S } from "@/lib/api";
import {
  WIREABLE,
  canConnect,
  docOf,
  family,
  graphProblems,
  mergeProblems,
  outType,
  paletteFrom,
  portType,
  splitRef,
  wireDrop,
  type Doc,
  type Palette,
  type ParamValue,
  type Problem,
  type WireEnd,
} from "@/lib/graph";
import {
  NODE_W,
  addFromDrop,
  addNode,
  connect,
  elkInput,
  elkPositions,
  history,
  historyOf,
  mergedParam,
  moveNodes,
  removeNodes,
  removeWires,
} from "@/lib/graph-edit";
import { etaText, isActive, segments, type RunTrack, type Segment } from "@/lib/runs";
import { useLoras } from "@/lib/lora-library";
import { loraFieldOf, rowIds, strengthOf, usable } from "@/lib/loras";

type Workflow = S<"Workflow">;
type Schema = S<"NodeSchema">;
type Pos = { x: number; y: number };

/** The wire and port colour of each type (the mock's legend). */
const WIRE: Record<string, string> = {
  Text: "var(--t1b)",
  Int: "#86cdea",
  Float: "#86cdea",
  Seed: "#86cdea",
  Canvas: "#86cdea",
  Model: "var(--a1)",
  Conditioning: "#c1a8f5",
  Latent: "#f2c14e",
  Image: "#f3a0bd",
};
const LEGEND: [string, string][] = [
  ["Text", WIRE.Text],
  ["Value", WIRE.Int],
  ["Model", WIRE.Model],
  ["Conditioning", WIRE.Conditioning],
  ["Latent", WIRE.Latent],
  ["Image", WIRE.Image],
];
// the toolbar and the preview float over the canvas's bottom (as in the mock): keep the fitted graph above them
const FIT: FitViewOptions = { maxZoom: 0.9, padding: { top: "24px", x: "24px", bottom: "280px" } };
const SAVE_MS = 700;

/** What the cards read: the document, the kinds, run states and problems by node. */
type Ctx = {
  doc: Doc;
  schemas: Record<string, Schema>;
  palette: Palette;
  states: Record<string, Segment["state"]>;
  progress?: S<"RunProgress"> | null;
  problems: Problem[];
  overrides: Record<string, ParamValue>;
  short: Record<string, string>;
  /** the library's names, for a stack's rows */
  loras: S<"Lora">[] | null;
  /** a card's Add LoRA: select the node and open its picker in the inspector */
  addLora: (node: string) => void;
};
const GraphCtx = createContext<Ctx | null>(null);

export type GraphProps = {
  workflow: Workflow;
  /** the live palette (GET /v1/studio/nodes) and its groups; null when it can't be read (the workflow's own kinds
   * stand in, so the graph can still be seen) */
  palette: { map: Palette; groups: S<"NodePalette">["groups"] } | null;
  topBar: (badge: string) => React.ReactNode;
  overrides: Record<string, ParamValue>;
  onParamSet: (key: string) => void;
  /** a workflow saved, copied or reloaded here; `open`: it is the one this view holds (open it) */
  onWorkflow: (w: Workflow, open: boolean) => void;
  onProblems: (workflowId: string, problems: Problem[]) => void;
  onError: (msg: string) => void;
  run?: S<"Run">;
  track?: RunTrack;
  take?: S<"Take">;
  generate: (w: Workflow) => Promise<void>;
  busy: boolean;
  anyActive: boolean;
};

export function GraphView(props: GraphProps) {
  return (
    <ReactFlowProvider>
      <Graph {...props} />
    </ReactFlowProvider>
  );
}

function Graph({
  workflow,
  palette: live,
  topBar,
  overrides,
  onParamSet,
  onWorkflow,
  onProblems,
  onError,
  run,
  track,
  take,
  generate,
  busy,
  anyActive,
}: GraphProps) {
  const rf = useReactFlow();
  const [h, dispatch] = useReducer(history, workflow, (w) => historyOf(docOf(w)));
  const doc = h.present;
  const palette = useMemo(() => live?.map ?? paletteFrom(workflow), [live, workflow]);
  const schemas = useMemo(
    () => ({ ...workflow.node_schemas, ...palette }),
    [workflow.node_schemas, palette],
  );
  const short = useMemo(
    () =>
      Object.fromEntries(
        (live?.groups ?? []).flatMap((g) => (g.family ? [[g.family, g.title.split(" ")[0]]] : [])),
      ),
    [live],
  );

  // the saved workflow this edits (its id and version), the document as saved, and the one adopted from a save
  const base = useRef(workflow);
  const saved = useRef(doc);
  const adopted = useRef<string | null>(null);
  const [auto, setAuto] = useState<Record<string, Pos> | null>(null);
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState<"" | "saving" | "failed">("");
  const [server, setServer] = useState<{ doc: Doc; problems: Problem[] } | null>(null);
  const [flowKey, setFlowKey] = useState(workflow.id);
  // what renders from the refs above: the workflow held and the document as saved
  const [cur, setCur] = useState(workflow);
  const [savedDoc, setSavedDoc] = useState(doc);
  const setBase = (w: Workflow) => {
    base.current = w;
    setCur(w);
  };
  const setSaved = (d: Doc) => {
    saved.current = d;
    setSavedDoc(d);
  };
  const queue = useRef<Promise<void>>(Promise.resolve());
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const latest = useRef(doc);
  useEffect(() => {
    latest.current = doc;
  });
  // the freshest saved version of each workflow this view has held, and the copy each template became
  const known = useRef<Record<string, Workflow>>({ [workflow.id]: workflow });
  const copies = useRef<Record<string, string>>({});
  const fresher = (w: Workflow) => {
    const k = known.current[w.id];
    if (!k || (w.version ?? 0) >= (k.version ?? 0)) known.current[w.id] = w;
  };
  const holds = (id: string) => base.current.id === id;

  const [search, setSearch] = useState<{ x: number; y: number; at: Pos; from?: WireEnd } | null>(
    null,
  );

  // a document without positions is laid out by elkjs; the positions join the document with its first edit
  const missing = Object.values(doc.nodes).some((n) => !n.position);
  useEffect(() => {
    if (!missing || auto) return;
    let stop = false;
    void layout(doc, schemas).then((p) => !stop && setAuto(p));
    return () => {
      stop = true;
    };
  }, [missing, auto, doc, schemas]);
  const ready = !missing || !!auto;

  const edit = useCallback(
    (f: (d: Doc) => Doc) => {
      const withPos = auto && missing ? moveNodes(doc, auto) : doc;
      const next = f(withPos);
      if (next !== withPos) dispatch({ type: "edit", doc: next });
    },
    [doc, auto, missing],
  );

  // ---------------------------------------------------------------- check and save
  const loras = useLoras();
  const library = useMemo(() => (loras ? usable(loras) : undefined), [loras]);
  const [loraPick, setLoraPick] = useState<string | null>(null);
  const client = useMemo(() => graphProblems(doc, palette, library), [doc, palette, library]);
  const problems = useMemo(
    () => (server?.doc === doc ? mergeProblems(client, server.problems) : client),
    [client, server, doc],
  );
  // a block body: whatever the callback returns never becomes the effect's cleanup
  useEffect(() => {
    onProblems(cur.id, problems);
  }, [cur.id, problems, onProblems]);

  /** Checks `d` (a document of workflow `id`) with studio and saves it when it changed and nothing blocks saving;
   * a template is first copied as "<title> (mine)". Answers about a workflow no longer open only update the list. */
  const save = useCallback(
    async (d: Doc, id: string) => {
      let canSave = !graphProblems(d, palette).some((p) => p.blocks === "save");
      try {
        const v = await ok(api.POST("/v1/studio/workflows/validate", { body: d }));
        if (holds(id)) setServer({ doc: d, problems: v.problems });
        canSave = v.can_save;
      } catch {
        // an older studio has no validate: the client's check stands
      }
      if ((holds(id) && d === saved.current) || !canSave) return;
      if (holds(id)) setSaving("saving");
      try {
        let w = known.current[copies.current[id] ?? id];
        if (w.source !== "mine") {
          w = await ok(
            api.POST("/v1/studio/workflows", {
              body: { based_on: w.id, title: `${w.title} (mine)`.slice(0, 80) },
            }),
          );
          copies.current[id] = w.id;
          fresher(w);
          if (holds(id)) {
            adopted.current = w.id;
            setBase(w);
            setNotice(`Saved as your own workflow, “${w.title}”.`);
          }
          onWorkflow(w, holds(w.id));
        }
        const { data, error, response } = await api.PUT("/v1/studio/workflows/{workflow_id}", {
          params: { path: { workflow_id: w.id } },
          body: { ...d, title: w.title, version: w.version ?? 1 },
        });
        if (response.status === 409 && holds(w.id)) {
          const fresh = await ok(
            api.GET("/v1/studio/workflows/{workflow_id}", {
              params: { path: { workflow_id: w.id } },
            }),
          );
          fresher(fresh);
          setBase(fresh);
          setSaved(docOf(fresh));
          dispatch({ type: "reset", doc: saved.current });
          onWorkflow(fresh, true);
          setNotice(
            `This workflow was changed elsewhere; it is reloaded at version ${fresh.version}. Your last edits were not saved.`,
          );
        } else if (response.status === 422 && error && "problems" in error && error.problems) {
          if (holds(w.id)) setServer({ doc: d, problems: error.problems });
        } else if (!response.ok || !data) {
          throw new ApiError(response.status, errorText(response.status, error));
        } else {
          fresher(data);
          if (holds(w.id)) {
            setBase(data);
            setSaved(d);
            if (data.problems) setServer({ doc: d, problems: data.problems });
          }
          onWorkflow(data, holds(w.id));
        }
        if (holds(w.id)) setSaving("");
      } catch (e) {
        if (holds(id) || holds(copies.current[id] ?? "")) setSaving("failed");
        onError(message(e));
      }
    },
    [palette, onWorkflow, onError],
  );

  // the document last checked: each is checked (and saved) once, so a failing check is not retried in a loop
  const checked = useRef<Doc | null>(null);
  const flush = useCallback(() => {
    clearTimeout(timer.current);
    const d = latest.current;
    const id = base.current.id;
    queue.current = queue.current.then(() => save(d, id));
    return queue.current;
  }, [save]);
  const flushRef = useRef(flush);
  useEffect(() => {
    flushRef.current = flush;
  });
  // another workflow chosen (not one this view just saved or copied): start over on it
  useEffect(() => {
    fresher(workflow);
    if (workflow.id === base.current.id) {
      setBase(workflow); // a rename elsewhere bumps the version
      return;
    }
    const mineNow = adopted.current === workflow.id;
    if (!mineNow && latest.current !== saved.current) void flushRef.current();
    setBase(workflow);
    if (mineNow) return;
    const d = docOf(workflow);
    setSaved(d);
    dispatch({ type: "reset", doc: d });
    setAuto(null);
    setServer(null);
    setNotice("");
    setFlowKey(workflow.id);
  }, [workflow]);

  useEffect(() => {
    if (checked.current === doc) return;
    clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      checked.current = doc;
      void flush();
    }, SAVE_MS);
    return () => clearTimeout(timer.current);
  }, [doc, flush]);
  // leaving Graph view saves what is pending
  useEffect(
    () => () => {
      if (latest.current !== saved.current) void flushRef.current();
    },
    [],
  );

  // ---------------------------------------------------------------- the canvas
  const shown = run && run.workflow_id === cur.id ? run : undefined;
  const states = useMemo(
    () =>
      shown
        ? Object.fromEntries(segments(workflow, shown, track).map((s) => [s.node, s.state]))
        : {},
    [shown, track, workflow],
  );
  const ctx: Ctx = {
    doc,
    schemas,
    palette,
    states,
    progress: shown && isActive(shown.status) ? shown.progress : null,
    problems,
    overrides,
    short,
    loras,
    addLora: setLoraPick,
  };

  const [nodes, setNodes] = useState<Node[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const pick = useRef<string | null>(null);
  useEffect(() => {
    setNodes((prev) => {
      const old = new Map(prev.map((n) => [n.id, n]));
      const sel = pick.current;
      pick.current = null;
      return Object.entries(doc.nodes).map(([id, n]) => ({
        ...old.get(id),
        id,
        type: "card",
        position: n.position ?? auto?.[id] ?? { x: 0, y: 0 },
        data: {},
        selected: sel ? id === sel : (old.get(id)?.selected ?? false),
        ariaLabel: `${nodeName(doc, schemas, id)} node`,
      }));
    });
    setEdges((prev) => {
      const old = new Map(prev.map((e) => [e.id, e]));
      return Object.entries(doc.nodes).flatMap(([id, n]) =>
        Object.entries(n.in ?? {}).flatMap(([key, ref]) => {
          const [src, out] = splitRef(ref);
          const sn = doc.nodes[src];
          const t = sn && outType(schemas[sn.kind], out);
          const ds = schemas[n.kind];
          if (!t || !ds || !portType(ds, key)) return [];
          const eid = `${id}.${key}`;
          return [
            {
              id: eid,
              source: src,
              sourceHandle: `o:${out}`,
              target: id,
              targetHandle: `i:${key}`,
              selected: old.get(eid)?.selected ?? false,
              ariaLabel: `Wire from ${nodeName(doc, schemas, src)} ${out} to ${nodeName(doc, schemas, id)} ${key}`,
              style: {
                stroke: WIRE[t] ?? "var(--t2)",
                strokeWidth: 2,
                strokeDasharray: sn.bypass || n.bypass ? "4 4" : undefined,
              },
            },
          ];
        }),
      );
    });
  }, [doc, auto, schemas]);

  const selected = nodes.find((n) => n.selected)?.id ?? null;
  const canvas = useRef<HTMLDivElement>(null);

  /** Opens the node search at a point of the screen (the canvas centre by default). */
  const openSearch = useCallback(
    (from?: WireEnd, client?: { x: number; y: number }) => {
      const r = canvas.current!.getBoundingClientRect();
      const c = client ?? { x: r.left + r.width / 2 - 140, y: r.top + r.height / 3 };
      setSearch({
        x: Math.max(8, Math.min(c.x - r.left, r.width - 300)),
        y: Math.max(8, Math.min(c.y - r.top, r.height - 340)),
        at: rf.screenToFlowPosition(c),
        from,
      });
    },
    [rf],
  );

  // undo / redo, add node (A) and generate (⌘↵), while the graph is open and focus is not in a field
  const generateNow = useCallback(async () => {
    await flush();
    await generate(base.current);
  }, [flush, generate]);
  const blocked = problems.length > 0 && (cur.source === "mine" || doc !== savedDoc);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select, [contenteditable], dialog, [popover]")) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "z") {
        e.preventDefault();
        dispatch({ type: e.shiftKey ? "redo" : "undo" });
      } else if (mod && e.key === "Enter") {
        e.preventDefault();
        if (!blocked && !busy) void generateNow();
      } else if (!mod && !e.altKey && e.key.toLowerCase() === "a") {
        e.preventDefault();
        openSearch();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openSearch, generateNow, blocked, busy]);

  const add = (kind: string) => {
    if (!search) return;
    const at = { x: search.at.x, y: search.at.y };
    edit((d) => {
      const r = search.from
        ? addFromDrop(d, palette, search.from, kind, at)
        : addNode(d, palette, kind, selected, at);
      pick.current = r.id;
      return r.doc;
    });
    setSearch(null);
  };

  const tidy = async () => {
    const p = await layout(doc, schemas);
    edit((d) => moveNodes(d, p));
    requestAnimationFrame(() => void rf.fitView({ ...FIT, duration: 200 }));
  };

  const count = Object.keys(doc.nodes).length;
  const badge = [
    cur.title,
    `${count} node${count === 1 ? "" : "s"}`,
    problems.length ? `${problems.length} problem${problems.length === 1 ? "" : "s"}` : "valid",
    saving === "saving" ? "saving…" : saving === "failed" ? "not saved" : "",
  ]
    .filter(Boolean)
    .join(" · ");
  const p = shown?.progress;
  const out = take ?? shown?.outputs[0];
  const groupTitle = (kind: string) =>
    live?.groups.find((g) => g.family === family(kind))?.title ??
    (family(kind) ? undefined : "Core");

  return (
    <GraphCtx value={ctx}>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        {topBar(badge)}
        {notice && (
          <p role="status" className="rounded-xl bg-s2 px-3 py-2 text-[13px]">
            {notice}
            <button type="button" className="chip ml-3 h-7" onClick={() => setNotice("")}>
              OK
            </button>
          </p>
        )}
        <div
          ref={canvas}
          className="gv panel relative min-h-[620px] grow overflow-hidden"
          style={{ background: "var(--in)" }}
          onDoubleClick={(e) => {
            if ((e.target as Element).classList.contains("react-flow__pane"))
              openSearch(undefined, { x: e.clientX, y: e.clientY });
          }}
        >
          {ready && (
            <ReactFlow
              key={flowKey}
              nodes={nodes}
              edges={edges}
              nodeTypes={NODE_TYPES}
              onNodesChange={(c) =>
                setNodes((ns) =>
                  applyNodeChanges(
                    c.filter((x) => x.type !== "remove"),
                    ns,
                  ),
                )
              }
              onEdgesChange={(c) =>
                setEdges((es) =>
                  applyEdgeChanges(
                    c.filter((x) => x.type !== "remove"),
                    es,
                  ),
                )
              }
              onNodeDragStop={(_, __, moved) =>
                edit((d) => moveNodes(d, Object.fromEntries(moved.map((n) => [n.id, n.position]))))
              }
              onDelete={({ nodes: ns, edges: es }) =>
                edit((d) =>
                  removeWires(
                    removeNodes(
                      d,
                      ns.map((n) => n.id),
                    ),
                    es.map((e) => e.id),
                  ),
                )
              }
              onConnect={(c: Connection) =>
                edit((d) =>
                  connect(
                    d,
                    c.target,
                    c.targetHandle!.slice(2),
                    `${c.source}.${c.sourceHandle!.slice(2)}`,
                  ),
                )
              }
              isValidConnection={(c) =>
                canConnect(
                  doc,
                  palette,
                  c.source,
                  String(c.sourceHandle).slice(2),
                  c.target,
                  String(c.targetHandle).slice(2),
                )
              }
              onConnectEnd={(e, s: FinalConnectionState) => {
                if (s.isValid || s.toNode || !s.fromNode || !s.fromHandle?.id) return;
                const pt = "changedTouches" in e ? e.changedTouches[0] : e;
                const target = document.elementFromPoint(pt.clientX, pt.clientY);
                if (!target?.classList.contains("react-flow__pane")) return;
                openSearch(
                  {
                    node: s.fromNode.id,
                    port: s.fromHandle.id.slice(2),
                    side: s.fromHandle.type === "source" ? "output" : "input",
                  },
                  { x: pt.clientX, y: pt.clientY },
                );
              }}
              onPaneContextMenu={(e) => {
                e.preventDefault();
                openSearch(undefined, { x: e.clientX, y: e.clientY });
              }}
              onPaneClick={() => setSearch(null)}
              deleteKeyCode={["Backspace", "Delete"]}
              zoomOnDoubleClick={false}
              minZoom={0.2}
              maxZoom={2}
              fitView
              fitViewOptions={FIT}
              proOptions={{ hideAttribution: true }}
              ariaLabelConfig={{
                "node.a11yDescription.default":
                  "Press Enter or Space to select a node, Delete to remove it, arrow keys to move it. Press A to add a node.",
              }}
            />
          )}
          {search && (
            <NodeSearch
              x={search.x}
              y={search.y}
              doc={doc}
              palette={palette}
              groups={live?.groups}
              from={search.from}
              onPick={add}
              onClose={() => setSearch(null)}
            />
          )}
          <div className="pointer-events-none absolute inset-x-4 bottom-4 flex flex-wrap items-end justify-between gap-3">
            <div className="pointer-events-auto flex flex-wrap items-center gap-2">
              <button
                type="button"
                className="bevel flex h-10 items-center gap-1.5 rounded-[10px] border border-line bg-s2 px-3.5 text-[13px]"
                title="Add node (A, or double-click the canvas)"
                onClick={() => openSearch()}
              >
                <Plus size={16} strokeWidth={2} aria-hidden />
                Add node
              </button>
              <button
                type="button"
                className="bevel h-10 rounded-[10px] border border-line bg-s2 px-3.5 text-[13px]"
                onClick={() => void tidy()}
              >
                Tidy layout
              </button>
              <button
                type="button"
                className="key h-10 px-4 text-[13px]"
                title="Generate (⌘ or Ctrl + Enter)"
                disabled={busy || blocked}
                aria-describedby={blocked ? "graph-problems" : undefined}
                onClick={() => void generateNow()}
              >
                {busy
                  ? "Sending…"
                  : blocked
                    ? `${problems.length} problem${problems.length === 1 ? "" : "s"}`
                    : anyActive
                      ? "Add to queue"
                      : "Generate"}
              </button>
              <span className="flex flex-wrap items-center gap-2.5 px-1.5 text-[12px] text-t2">
                {LEGEND.map(([l, c]) => (
                  <span key={l} className="flex items-center gap-[5px]">
                    <i className="block size-2 rounded-full" style={{ background: c }} />
                    {l}
                  </span>
                ))}
              </span>
            </div>
            <div
              className="pointer-events-auto box-border w-[280px] shrink-0 rounded-xl border border-line bg-s2 p-2"
              style={{ boxShadow: "0 12px 32px color-mix(in srgb, var(--shd) 50%, transparent)" }}
            >
              {out?.kind === "image" || out?.kind === "video" ? (
                <Thumb
                  take={out}
                  imgClassName="aspect-video w-full rounded-lg object-contain"
                  className="rounded-lg bg-inset"
                />
              ) : (
                <div className="flex aspect-video items-center justify-center rounded-lg border border-dashed border-dash text-[12px] text-t2">
                  {shown?.status === "failed" ? "Failed" : "Live preview"}
                </div>
              )}
              <div className="mono flex justify-between px-1 pt-2 pb-0.5 text-[11px] text-t2">
                <span>
                  {p && shown?.status === "running"
                    ? `${nodeName(doc, schemas, p.node)} ${p.step} / ${p.of}`
                    : shown
                      ? {
                          queued: "Queued",
                          running: "Starting",
                          succeeded: "Done",
                          failed: "Failed",
                          cancelled: "Stopped",
                        }[shown.status]
                      : ""}
                </span>
                <span>{shown && etaText(shown)}</span>
              </div>
            </div>
          </div>
        </div>
      </main>
      <aside
        aria-label="Inspector"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-5 px-[18px] pt-4 pb-6 md:ml-0"
      >
        {selected && doc.nodes[selected] ? (
          <Panel
            doc={doc}
            schemas={schemas}
            palette={palette}
            id={selected}
            problems={problems}
            overrides={overrides}
            groupTitle={groupTitle(doc.nodes[selected].kind)}
            edit={edit}
            onParamSet={onParamSet}
            loraPick={loraPick === selected}
            onLoraPicked={() => setLoraPick(null)}
          />
        ) : (
          <>
            <Head
              title={cur.title}
              sub={
                cur.source === "mine"
                  ? `Yours · runs on ${doc.env}`
                  : `A template · runs on ${doc.env}. Your first edit saves your own copy, “${cur.title} (mine)”.`
              }
            />
            <div id="graph-problems">
              <Problems list={problems} />
            </div>
            <p className="m-0 text-[13px] text-t2">
              Select a node to edit it. Add one with Add node, A, a double-click or a right-click on
              the canvas; drag from a port to connect, or drop a wire on the canvas to pick a node
              for it. ⌘Z undoes.
            </p>
          </>
        )}
      </aside>
    </GraphCtx>
  );
}

/** elkjs's positions for a document (the bundled build, loaded with the graph). */
async function layout(doc: Doc, schemas: Record<string, Schema>) {
  const { default: ELK } = await import("elkjs/lib/elk.bundled.js");
  return elkPositions(await new ELK().layout(elkInput(doc, schemas)));
}

// ---------------------------------------------------------------- the card

const NODE_TYPES = { card: Card };

/** A node card: header (title, then its state, bypassed or its family), a row per input/output pair with their
 * typed ports, a row per param (a wireable value has an input port; wired, it shows its source), problems. */
function Card({ id }: NodeProps) {
  const g = use(GraphCtx)!;
  const n = g.doc.nodes[id];
  const s = n && g.schemas[n.kind];
  const from = useConnection((c) =>
    c.inProgress ? `${c.fromHandle?.type}|${c.fromNode?.id}|${c.fromHandle?.id}` : "",
  );
  if (!n) return null;
  const mine = g.problems.filter((p) => p.node === id);
  const state = g.states[id];
  const [ftype, fnode, fhandle] = from.split("|");
  const dim = (side: "source" | "target", name: string) => {
    if (!from || (fnode === id && fhandle?.slice(2) === name)) return false;
    if ((ftype === "source") === (side === "source")) return true;
    return side === "target"
      ? !canConnect(g.doc, g.palette, fnode, fhandle.slice(2), id, name)
      : !canConnect(g.doc, g.palette, id, name, fnode, fhandle.slice(2));
  };
  const port = (side: "source" | "target", name: string, type: string) => (
    <Handle
      type={side}
      position={side === "target" ? Position.Left : Position.Right}
      id={(side === "target" ? "i:" : "o:") + name}
      className="gp"
      style={{ background: WIRE[type] ?? "var(--t2)", color: WIRE[type] ?? "var(--t2)" }}
      data-dim={dim(side, name) || undefined}
      data-bad={(side === "target" && mine.some((p) => p.port === name)) || undefined}
      aria-label={`${side === "target" ? "input" : "output"} ${name}, ${type}`}
    />
  );
  const tag =
    state === "active"
      ? "running"
      : state === "done"
        ? "done"
        : state === "stopped"
          ? "stopped"
          : state === "pending" && g.progress !== null
            ? "queued"
            : n.bypass
              ? "bypassed"
              : family(n.kind)
                ? g.short[family(n.kind)!]
                : undefined;
  if (!s)
    return (
      <div className="gn" data-bad>
        <div className="gn-h">
          <span>{n.kind}</span>
          <span>unknown</span>
        </div>
        <div className="gn-p">{mine[0]?.message ?? "No runner offers this kind now."}</div>
      </div>
    );
  const wires = n.in ?? {};
  const rows = Math.max(s.inputs.length, s.outputs.length);
  const value = (p: S<"ParamSchema">) =>
    valueText(p, g.overrides[`${id}.${p.name}`] ?? n.params?.[p.name] ?? p.default);
  const merged = s.params.find((p) => mergedParam(s, p) && !(p.name in wires));
  const p = g.progress;
  return (
    <div
      className="gn"
      style={{ width: NODE_W }}
      data-state={state === "active" ? "running" : undefined}
      data-bypass={n.bypass || undefined}
      data-bad={mine.length ? true : undefined}
    >
      <div className="gn-h">
        <span title={s.title}>{s.title}</span>
        {tag && (
          <span>
            {state === "done" && (
              <Check size={12} strokeWidth={2.5} className="text-a1" aria-hidden />
            )}
            {tag}
          </span>
        )}
      </div>
      {Array.from({ length: rows }, (_, i) => {
        const inp = s.inputs[i];
        const o = s.outputs[i];
        return (
          <div key={i} className="gn-r">
            {inp && port("target", inp.name, inp.type)}
            {merged && !inp ? (
              <>
                <span>{merged.name}</span>
                <b>{value(merged)}</b>
              </>
            ) : (
              <span data-opt={inp?.optional || undefined}>{inp ? inp.name : o.name}</span>
            )}
            {inp && o && <span>{o.name}</span>}
            {o && port("source", o.name, o.type)}
          </div>
        );
      })}
      {s.params
        .filter((x) => x !== merged)
        .map((x) => {
          if (loraFieldOf(x) && !wires[x.name]) {
            const v = g.overrides[`${id}.${x.name}`] ?? n.params?.[x.name] ?? x.default;
            const rows = Array.isArray(v) ? v : [];
            const st = strengthOf(x);
            const bad = mine.some((q) => q.param === x.name);
            return (
              <div key={x.name}>
                {rowIds(x, rows).map((lid, i) => (
                  <div key={lid || i} className="gn-r">
                    <span className="truncate">
                      {g.loras?.find((l) => l.id === lid)?.name ??
                        (lid ? lid.slice(0, 8) : "no LoRA")}
                    </span>
                    <b style={bad ? { color: "var(--bad)" } : undefined}>
                      {st ? Number(rows[i][st[0]] ?? st[1].default).toFixed(2) : ""}
                    </b>
                  </div>
                ))}
                {rows.length < (x.max ?? 8) && (
                  <div className="px-3.5 pt-1.5">
                    <button
                      type="button"
                      className="nodrag h-[30px] w-full rounded-lg border border-dashed border-dash bg-transparent text-[12px]"
                      onClick={() => g.addLora(id)}
                    >
                      Add LoRA
                    </button>
                  </div>
                )}
              </div>
            );
          }
          const wireable = WIREABLE[x.type];
          const ref = wires[x.name];
          const text = x.type === "text" && !ref;
          return (
            <div key={x.name} className={text ? "gn-t" : "gn-r"}>
              {wireable && port("target", x.name, wireable)}
              {text ? (
                value(x) || <span style={{ opacity: 0.7 }}>{x.label ?? x.name}</span>
              ) : (
                <>
                  <span>{x.name}</span>
                  <b
                    style={
                      mine.some((q) => q.param === x.name) ? { color: "var(--bad)" } : undefined
                    }
                  >
                    {ref ? `← ${splitRef(ref)[0]}` : value(x)}
                  </b>
                </>
              )}
            </div>
          );
        })}
      {state === "active" && p && p.node === id && (
        <div className="flex items-center gap-2 px-3.5 pt-1.5 pb-0.5">
          <span className="block h-1 grow rounded-[2px] bg-s5">
            <span
              className="block h-1 rounded-[2px] bg-a1"
              style={{ width: `${Math.min(100, (p.step / p.of) * 100)}%` }}
            />
          </span>
          <span className="mono text-[11px] text-t2">
            {p.step} / {p.of}
          </span>
        </div>
      )}
      {mine.length > 0 && (
        <div className="gn-p">
          {mine.slice(0, 2).map((q) => (
            <div key={[q.code, q.port, q.param].join(".")}>{q.message}</div>
          ))}
          {mine.length > 2 && <div>and {mine.length - 2} more</div>}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- node search

/** The node search: the palette by family (only kinds this workflow may use), or, for a dropped wire, the kinds
 * that take it in the contracts' order; type to filter, arrows and Enter to pick, Escape to close. */
function NodeSearch({
  x,
  y,
  doc,
  palette,
  groups,
  from,
  onPick,
  onClose,
}: {
  x: number;
  y: number;
  doc: Doc;
  palette: Palette;
  groups?: S<"NodePalette">["groups"];
  from?: WireEnd;
  onPick: (kind: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const usable = (k: string) =>
    palette[k]?.envs.includes(doc.env) && [null, doc.family].includes(family(k));
  const sections: { title: string; kinds: string[] }[] = from
    ? [{ title: "Takes this wire", kinds: wireDrop(doc, palette, from) }]
    : groups
      ? groups.map((g) => ({ title: g.title, kinds: g.nodes.map((n) => n.kind).filter(usable) }))
      : [{ title: "Nodes", kinds: Object.keys(palette).filter(usable) }];
  const match = (k: string) =>
    !q.trim() || `${palette[k].title} ${k}`.toLowerCase().includes(q.trim().toLowerCase());
  const shown = sections
    .map((s) => ({ ...s, kinds: s.kinds.filter(match) }))
    .filter((s) => s.kinds.length);
  const flat = shown.flatMap((s) => s.kinds);
  const at = Math.min(active, Math.max(0, flat.length - 1));
  return (
    <div
      role="dialog"
      aria-label={from ? "Add a node for this wire" : "Add node"}
      className="pop absolute z-20 flex w-[280px] flex-col gap-2"
      style={{ left: x, top: y }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
          e.preventDefault();
          setActive((a) =>
            Math.max(0, Math.min(flat.length - 1, a + (e.key === "ArrowDown" ? 1 : -1))),
          );
        } else if (e.key === "Enter" && flat[at]) {
          e.preventDefault();
          onPick(flat[at]);
        }
      }}
    >
      <input
        autoFocus
        className="in"
        placeholder="Search nodes"
        aria-label="Search nodes"
        role="combobox"
        aria-expanded
        aria-controls="node-results"
        aria-activedescendant={flat[at] ? `nk-${at}` : undefined}
        value={q}
        onChange={(e) => {
          setQ(e.target.value);
          setActive(0);
        }}
        onBlur={(e) => {
          if (!e.currentTarget.parentElement?.contains(e.relatedTarget)) onClose();
        }}
      />
      <div
        id="node-results"
        role="listbox"
        className="flex max-h-72 flex-col gap-0.5 overflow-y-auto"
      >
        {!flat.length && (
          <span className="px-2 py-1 text-[12px] text-t2">
            {from ? "No node takes this wire." : "No node matches."}
          </span>
        )}
        {shown.map((s) => (
          <div key={s.title} role="group" aria-label={s.title} className="flex flex-col gap-0.5">
            <span className="heading px-2 pt-1">{s.title}</span>
            {s.kinds.map((k) => {
              const i = flat.indexOf(k);
              return (
                <div
                  key={k}
                  id={`nk-${i}`}
                  role="option"
                  aria-selected={i === at}
                  tabIndex={-1}
                  className="flex cursor-pointer justify-between gap-2 rounded-[10px] px-2 py-1.5 text-[13px] aria-selected:bg-s4"
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => onPick(k)}
                >
                  <span className="truncate">{palette[k].title}</span>
                  <span className="mono truncate text-[11px] text-t2">{k}</span>
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
