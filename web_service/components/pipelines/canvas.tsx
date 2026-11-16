"use client";
// A pipeline's graph (docs/mock/PipelineGraph.dc.html, Execution.dc.html): its nodes as cards and its edges on
// @xyflow/react, laid out by elkjs when nodes have no positions; a gate's fail edge is the dashed loop over the top
// with its cap. Editable (drag to connect from a port, drop a wire on the canvas to add a node, Backspace deletes,
// A or a double- or right-click adds) or read-only with the marks of one row's execution. Loaded with next/dynamic.
// Stage B (docs/mock/DirectorChat.dc.html): Agent cards (tools, how a visit ends, caps, chat), custom nodes from the
// published list in Add node, and a fanned-out step drawn as a stack of cards with "× item · max N".
import "@xyflow/react/dist/base.css";
import "@/components/graph/graph.css";
import { createContext, use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  BaseEdge,
  EdgeLabelRenderer,
  Handle,
  Position,
  ReactFlow,
  ReactFlowProvider,
  applyEdgeChanges,
  applyNodeChanges,
  getBezierPath,
  useReactFlow,
  type Edge,
  type EdgeProps,
  type FinalConnectionState,
  type FitViewOptions,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import { Check, Plus } from "lucide-react";
import type { S } from "@/lib/api";
import {
  addPipelineNode,
  analyse,
  connectNodes,
  disconnectNodes,
  isAgent,
  movePipelineNodes,
  nodeLabel,
  removePipelineNodes,
  stepNumbers,
  stepTakes,
  type PDoc,
  type PProblem,
  type Via,
} from "@/lib/pipeline";
import type { asRun } from "@/lib/pipeline-run";

type Pos = { x: number; y: number };
type Kind = S<"PipelineNode">["kind"];
export type Marks = ReturnType<typeof asRun>;

const W = 176;
const FIT: FitViewOptions = { maxZoom: 1, padding: { top: "56px", x: "24px", bottom: "72px" } };
const TEXT = "var(--t1b)";
const IMAGE = "#f3a0bd";
const LOOP = "#f2c14e";
const AGENT = "#c1a8f5";

type Ctx = {
  doc: PDoc;
  titles: Record<string, string>;
  problems: PProblem[];
  marks?: Marks;
  readOnly: boolean;
  /** per step: its runs per visit (seeds) for the card's "seeds" row */
  steps: Record<string, number>;
  /** the node the panel shows (outlined; xyflow's own selection stays the user's, so the two never fight) */
  selected: string | null;
  /** node id → the model it resolves to (an agent's "runs on") */
  models?: Record<string, string>;
};
const CanvasCtx = createContext<Ctx | null>(null);

export type CanvasProps = {
  doc: PDoc;
  titles: Record<string, string>;
  problems?: PProblem[];
  /** the marks of one row's execution: read-only */
  marks?: Marks;
  selected: string | null;
  onSelect: (id: string | null) => void;
  /** edits (absent: read-only) */
  edit?: (f: (d: PDoc) => PDoc) => void;
  /** the workflow a new step runs */
  defaultWorkflow?: string;
  /** a key for the flow: a new one fits the view again */
  flowKey?: string;
  /** the custom nodes Add node offers (GET /custom-nodes) */
  custom?: S<"CustomNode">[];
  /** node id → the model it resolves to */
  models?: Record<string, string>;
};

export function PipelineCanvas(props: CanvasProps) {
  return (
    <ReactFlowProvider>
      <Canvas {...props} />
    </ReactFlowProvider>
  );
}

function Canvas({
  doc,
  titles,
  problems = [],
  marks,
  selected,
  onSelect,
  edit,
  defaultWorkflow,
  flowKey,
  custom = [],
  models,
}: CanvasProps) {
  const rf = useReactFlow();
  const readOnly = !edit;
  const [auto, setAuto] = useState<Record<string, Pos> | null>(null);
  const missing = Object.values(doc.nodes).some((n) => !n.position);
  useEffect(() => {
    if (!missing || auto) return;
    let stop = false;
    void layout(doc).then((p) => !stop && setAuto(p));
    return () => {
      stop = true;
    };
  }, [missing, auto, doc]);
  const ready = !missing || !!auto;
  const change = useCallback(
    (f: (d: PDoc) => PDoc) => edit?.((d) => f(auto && missing ? movePipelineNodes(d, auto) : d)),
    [edit, auto, missing],
  );

  const [menu, setMenu] = useState<{ x: number; y: number; at: Pos; from?: [string, Via] } | null>(
    null,
  );
  const box = useRef<HTMLDivElement>(null);
  const openMenu = useCallback(
    (client?: { x: number; y: number }, from?: [string, Via]) => {
      if (readOnly) return;
      const r = box.current!.getBoundingClientRect();
      const c = client ?? { x: r.left + r.width / 2 - 110, y: r.top + r.height / 3 };
      setMenu({
        x: Math.max(8, Math.min(c.x - r.left, r.width - 230)),
        y: Math.max(8, Math.min(c.y - r.top, r.height - 220)),
        at: rf.screenToFlowPosition(c),
        from,
      });
    },
    [rf, readOnly],
  );
  useEffect(() => {
    if (readOnly) return;
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select, [contenteditable], dialog, [popover]")) return;
      if (!e.metaKey && !e.ctrlKey && !e.altKey && e.key.toLowerCase() === "a") {
        e.preventDefault();
        openMenu();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openMenu, readOnly]);

  const add = (kind: Kind, node?: S<"CustomNode">) => {
    if (!menu) return;
    const from = menu.from;
    let added = "";
    change((d) => {
      const r = addPipelineNode(d, kind, menu.at, {
        workflow: defaultWorkflow,
        selected: from?.[0] ?? selected,
        custom: node,
      });
      added = r.id;
      onSelect(r.id);
      return from ? connectNodes(r.doc, from[0], from[1], r.id) : r.doc;
    });
    setMenu(null);
    // a card dropped near the edge, or stepped right of a taken spot, can land outside the view: show the graph
    setTimeout(() => {
      const n = rf.getNode(added);
      const r = box.current?.getBoundingClientRect();
      if (!n || !r) return;
      const p = rf.flowToScreenPosition(n.position);
      const z = rf.getZoom();
      const [w, h] = [(n.measured?.width ?? 176) * z, (n.measured?.height ?? 80) * z];
      if (p.x < r.left || p.y < r.top || p.x + w > r.right || p.y + h > r.bottom)
        void rf.fitView({ ...FIT, duration: 200 });
    }, 50);
  };

  const steps = useMemo(() => stepTakes(doc), [doc]);
  const ctx: Ctx = { doc, titles, problems, marks, readOnly, steps, selected, models };

  const [nodes, setNodes] = useState<Node[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the flow's own node state follows the document
    setNodes((prev) => {
      const old = new Map(prev.map((n) => [n.id, n]));
      return Object.entries(doc.nodes).map(([id, n]) => ({
        ...old.get(id),
        id,
        type: "card",
        position: n.position ?? auto?.[id] ?? { x: 0, y: 0 },
        data: {},
        selected: old.get(id)?.selected ?? false,
        connectable: !readOnly,
        draggable: !readOnly,
        ariaLabel: `${nodeLabel(doc, id, titles)} node`,
      }));
    });
    setEdges(() => edgesOf(doc, marks));
  }, [doc, auto, readOnly, marks, titles]);

  return (
    <CanvasCtx value={ctx}>
      <div
        ref={box}
        className="gv relative h-full min-h-[400px] w-full"
        onDoubleClick={(e) => {
          if ((e.target as Element).classList.contains("react-flow__pane"))
            openMenu({ x: e.clientX, y: e.clientY });
        }}
      >
        {ready && (
          <ReactFlow
            key={flowKey}
            nodes={nodes}
            edges={edges}
            nodeTypes={NODE_TYPES}
            edgeTypes={EDGE_TYPES}
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
            onSelectionChange={({ nodes: ns }) =>
              ns[0] && ns[0].id !== selected && onSelect(ns[0].id)
            }
            onNodeDragStop={(_, __, moved) =>
              change((d) =>
                movePipelineNodes(d, Object.fromEntries(moved.map((n) => [n.id, n.position]))),
              )
            }
            onDelete={({ nodes: ns, edges: es }) =>
              change((d) =>
                es.reduce(
                  (x, e) => disconnectNodes(x, e.source, e.data?.via as Via),
                  removePipelineNodes(
                    d,
                    ns.map((n) => n.id),
                  ),
                ),
              )
            }
            onConnect={(c) =>
              change((d) => connectNodes(d, c.source, c.sourceHandle!.slice(2) as Via, c.target))
            }
            isValidConnection={(c) =>
              c.source !== c.target && doc.nodes[c.target]?.kind !== "inputs"
            }
            onConnectEnd={(e, s: FinalConnectionState) => {
              if (s.isValid || s.toNode || !s.fromNode || !s.fromHandle?.id) return;
              if (s.fromHandle.type !== "source") return;
              const pt = "changedTouches" in e ? e.changedTouches[0] : e;
              const target = document.elementFromPoint(pt.clientX, pt.clientY);
              if (!target?.classList.contains("react-flow__pane")) return;
              openMenu({ x: pt.clientX, y: pt.clientY }, [
                s.fromNode.id,
                s.fromHandle.id.slice(2) as Via,
              ]);
            }}
            onPaneContextMenu={(e) => {
              e.preventDefault();
              openMenu({ x: e.clientX, y: e.clientY });
            }}
            onPaneClick={() => {
              setMenu(null);
              onSelect(null);
            }}
            nodesDraggable={!readOnly}
            nodesConnectable={!readOnly}
            elementsSelectable
            deleteKeyCode={readOnly ? null : ["Backspace", "Delete"]}
            zoomOnDoubleClick={false}
            minZoom={0.3}
            maxZoom={2}
            fitView
            fitViewOptions={FIT}
            proOptions={{ hideAttribution: true }}
            ariaLabelConfig={{
              "node.a11yDescription.default": readOnly
                ? "Press Enter or Space to select a node and see its steps."
                : "Press Enter or Space to select a node, Delete to remove it, arrow keys to move it. Press A to add a node.",
            }}
          />
        )}
        {menu && (
          <AddMenu
            x={menu.x}
            y={menu.y}
            hasInputs={Object.values(doc.nodes).some((n) => n.kind === "inputs")}
            custom={custom}
            onPick={add}
            onClose={() => setMenu(null)}
          />
        )}
        {!readOnly && (
          <button
            type="button"
            className="bevel absolute bottom-4 left-4 flex h-10 items-center gap-1.5 rounded-[10px] border border-line bg-s2 px-3.5 text-[13px]"
            title="Add node (A, or double-click the canvas)"
            onClick={() => openMenu()}
          >
            <Plus size={16} strokeWidth={2} aria-hidden />
            Add node
          </button>
        )}
      </div>
    </CanvasCtx>
  );
}

/** The edges as the canvas draws them: a plain edge or a branch's pass from the `next` port, the fail edge as the
 * dashed loop with its cap, then as a dashed line. With marks, edges the row never took are faint. */
function edgesOf(doc: PDoc, marks?: Marks): Edge[] {
  const out: Edge[] = [];
  const color = (from: string) =>
    ["inputs", "write", "memory", "agent", "custom"].includes(doc.nodes[from]?.kind ?? "")
      ? TEXT
      : IMAGE;
  const a = analyse(doc);
  const ok = (from: string) => !a.refused.some(([, f]) => f === from);
  const add = (from: string, to: string, via: Via | "pass", extra: Partial<Edge>) => {
    if (!(from in doc.nodes) || !(to in doc.nodes)) return;
    const taken = marks ? marks.taken(from, to, via.startsWith("r:") ? "*" : via) : 1;
    out.push({
      id: `${from}:${via === "pass" ? "next" : via}`,
      source: from,
      sourceHandle: `o:${via === "pass" ? "next" : via}`,
      target: to,
      targetHandle: "i:in",
      data: { via: via === "pass" ? "next" : via, taken },
      ariaLabel: `${via === "fail" ? "Retry" : via === "then" ? "Then" : via.startsWith("r:") ? `Route ${via.slice(2)}` : "Edge"} from ${nodeLabel(doc, from)} to ${nodeLabel(doc, to)}`,
      ...extra,
      style: {
        stroke: ok(from) ? (extra.style?.stroke ?? color(from)) : "var(--bad)",
        strokeWidth: 2,
        opacity: marks && !taken ? 0.25 : 1,
        ...extra.style,
      },
    });
  };
  for (const [from, to] of doc.edges) {
    if (typeof to === "string") {
      add(from, to, "next", {});
      continue;
    }
    if ("routes" in to) {
      // a decide: each route from its own port; the capped ones dashed like a loop, with the cap
      for (const [label, t] of Object.entries(to.routes))
        add(from, t, `r:${label}`, {
          type: "loop",
          label: label === to.then ? label : `${label} · at most ${to.max}`,
          style: label === to.then ? { stroke: AGENT } : { stroke: LOOP, strokeDasharray: "5 5" },
        });
      continue;
    }
    add(from, to.pass, "pass", {});
    const loops = marks?.loops[from] ?? 0;
    add(from, to.fail, "fail", {
      type: "loop",
      label: marks
        ? `retried ${loops === 1 ? "once" : loops === 2 ? "twice" : `${loops} times`} of ${to.max}`
        : `retry, at most ${to.max} time${to.max === 1 ? "" : "s"}`,
      style: { stroke: LOOP, strokeDasharray: "5 5" },
    });
    add(from, to.then, "then", { style: { stroke: "var(--t2)", strokeDasharray: "2 4" } });
  }
  // a judge's memory: the examples it reads, from the memory node (a field of the judge, not an edge of the row)
  for (const [id, n] of Object.entries(doc.nodes))
    if (n.kind === "judge" && n.memory && doc.nodes[n.memory])
      out.push({
        id: `${id}:memory`,
        source: n.memory,
        sourceHandle: "o:examples",
        target: id,
        targetHandle: "i:memory",
        deletable: false,
        selectable: false,
        ariaLabel: `Memory from ${nodeLabel(doc, n.memory)} to ${nodeLabel(doc, id)}`,
        style: { stroke: AGENT, strokeWidth: 2, opacity: marks && !marks.nodes[id] ? 0.25 : 1 },
      });
  return out;
}

/** The fail edge: an arc over the cards from the gate back to the step, its cap on a pill at the top. */
function LoopEdge({ id, sourceX, sourceY, targetX, targetY, style, label, markerEnd }: EdgeProps) {
  const back = targetX < sourceX;
  const top = Math.min(sourceY, targetY) - 190;
  const path = back
    ? `M ${sourceX} ${sourceY} C ${sourceX + 60} ${top}, ${targetX - 60} ${top}, ${targetX} ${targetY}`
    : getBezierPath({
        sourceX,
        sourceY,
        targetX,
        targetY,
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
      })[0];
  const lx = (sourceX + targetX) / 2;
  const ly = back ? top + 46 : (sourceY + targetY) / 2;
  return (
    <>
      <BaseEdge id={id} path={path} style={style} markerEnd={markerEnd} />
      {label && (
        <EdgeLabelRenderer>
          <span
            className="nodrag nopan pointer-events-none absolute rounded-full border bg-s2 px-2 py-0.5 text-[11px]"
            style={{
              transform: `translate(-50%, -50%) translate(${lx}px, ${ly}px)`,
              borderColor: LOOP,
              color: LOOP,
              opacity: style?.opacity,
            }}
          >
            {label}
          </span>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

const EDGE_TYPES = { loop: LoopEdge };
const NODE_TYPES = { card: Card };

/** A node card (the mock's .n): its header (number and name, then its state or kind), a row with its ports, its
 * settings, and its problems. */
function Card({ id }: NodeProps) {
  const g = use(CanvasCtx)!;
  const n = g.doc.nodes[id];
  if (!n) return null;
  const mine = g.problems.filter((p) => p.node === id);
  const m = g.marks?.nodes[id];
  const nums = stepNumbers(g.doc);
  const out = g.doc.edges.find((e) => e[0] === id)?.[1];
  const label = nodeLabel(g.doc, id, g.titles);
  const head = n.kind === "step" ? `${nums[id]} · ${label}` : label;
  const fanned = n.kind === "step" && n.each ? n.each.split(".")[0] : null;
  const producer = fanned ? g.doc.nodes[fanned] : undefined;
  const fanMax =
    producer && (producer.kind === "write" || producer.kind === "agent")
      ? (producer.list?.max ?? 0)
      : 0;
  const tag = m
    ? m.current
      ? ({ running: "running", waiting: "parked", queued: "queued" }[m.status] ?? m.status)
      : m.status === "failed"
        ? "failed"
        : m.visits > 1
          ? `done ×${m.visits}`
          : "done"
    : g.marks
      ? "not reached"
      : n.kind === "agent" || n.kind === "custom"
        ? n.kind
        : fanned
          ? `× item · max ${fanMax}`
          : isAgent(n.kind)
            ? "agent node"
            : { inputs: "list", step: "step", gate: "gate", output: "" }[n.kind as "step"];
  const port = (side: "source" | "target", via: string, color: string, label?: string) => (
    <Handle
      type={side}
      position={side === "target" ? Position.Left : Position.Right}
      id={(side === "target" ? "i:" : "o:") + via}
      className="gp"
      isConnectable={!g.readOnly}
      style={{ background: color, color }}
      aria-label={label ?? (side === "target" ? "in" : via)}
    />
  );
  const color = ["inputs", "write", "memory", "agent", "custom"].includes(n.kind) ? TEXT : IMAGE;
  const row = (k: string, v: React.ReactNode, bad?: boolean) => (
    <div className="gn-r">
      <span>{k}</span>
      <b style={bad ? { color: "var(--bad)" } : undefined}>{v}</b>
    </div>
  );
  const ref = (src: string) => {
    const [node, what] = src.split(".");
    return `${nodeLabel(g.doc, node, g.titles)} · ${what}`;
  };
  return (
    <div
      className="gn"
      style={{
        width: W,
        opacity: g.marks && !m ? 0.5 : undefined,
        borderColor: m?.current ? "var(--a2)" : isAgent(n.kind) ? AGENT : undefined,
        outline: g.selected === id ? "2px solid var(--t2)" : undefined,
        outlineOffset: 2,
        // a fanned-out step: two cards behind it, as the mock draws the branches
        boxShadow: fanned
          ? "7px -6px 0 -1px var(--s2), 7px -6px 0 0 var(--ln), 14px -12px 0 -1px var(--s1), 14px -12px 0 0 var(--s5)"
          : undefined,
      }}
      data-bad={mine.length ? true : undefined}
    >
      <div className="gn-h">
        <span title={head}>{head}</span>
        {tag && (
          <span>
            {m && !m.current && m.status !== "failed" && (
              <Check size={12} strokeWidth={2.5} className="text-a1" aria-hidden />
            )}
            {tag}
          </span>
        )}
      </div>
      {n.kind === "inputs" &&
        n.inputs.map((i, k) => (
          <div key={i.name} className="gn-r">
            <span>{i.label ?? i.name}</span>
            <b>{i.type}</b>
            {k === 0 && port("source", "next", color, "out")}
          </div>
        ))}
      {n.kind === "step" && (
        <>
          <div className="gn-r">
            {port("target", "in", IMAGE)}
            <span>runs</span>
            <span>takes</span>
            {port("source", "next", color)}
          </div>
          {fanned && row("each", "items")}
          {row("seeds", n.seeds ?? 1)}
          {n.preset && row("preset", n.preset)}
          {Object.entries(n.bind ?? {}).map(([k, v]) =>
            row(
              k.split(".")[0],
              v.startsWith("$") ? v.slice(1) : ref(v),
              mine.some((p) => p.port === k),
            ),
          )}
        </>
      )}
      {(n.kind === "gate" || n.kind === "judge") && (
        <>
          <div className="gn-r">
            {port("target", "in", IMAGE)}
            <span>takes of</span>
            <b>{g.doc.nodes[n.takes] ? nodeLabel(g.doc, n.takes, g.titles) : n.takes}</b>
          </div>
          {n.kind === "judge" && (
            <>
              {n.memory && (
                <div className="gn-r">
                  {port("target", "memory", AGENT, "examples in")}
                  <span>examples</span>
                </div>
              )}
              {row(
                "checks",
                `${(n.criteria.must ?? []).length} must · ${(n.criteria.nice ?? []).length} nice`,
              )}
            </>
          )}
          {row(
            "keep",
            n.kind === "judge"
              ? `best ${n.keep ?? 1} of ${g.steps[n.takes] || "?"}`
              : `${n.keep ?? 1} of ${g.steps[n.takes] || "?"}`,
          )}
          {n.kind === "judge" && row("mode", n.mode === "auto" ? "Auto" : "Suggest")}
          <div className="gn-r">
            <span>{n.kind === "judge" ? "pass · picked" : "pass"}</span>
            {port("source", "next", IMAGE)}
          </div>
          {out !== undefined && (
            <>
              <div className="gn-r">
                <span>fail · retry</span>
                {typeof out === "object" && <b>max {out.max}</b>}
                {port("source", "fail", LOOP)}
              </div>
              {typeof out === "object" && (
                <div className="gn-r">
                  <span>then</span>
                  {port("source", "then", "var(--t2)")}
                </div>
              )}
            </>
          )}
        </>
      )}
      {n.kind === "write" && (
        <>
          <div className="gn-r">
            {port("target", "in", TEXT)}
            <span>reads</span>
            <b>{n.from.length}</b>
          </div>
          <div className="gn-r">
            <span>{n.list ? "answers" : "writes"}</span>
            <b>{n.list ? `list · max ${n.list.max}` : (n.to ?? "").slice(1)}</b>
            {port("source", "next", TEXT)}
          </div>
        </>
      )}
      {n.kind === "agent" && (
        <>
          <div className="gn-r">
            {port("target", "in", TEXT)}
            <span>reads</span>
            <b>{(n.from ?? []).length}</b>
          </div>
          {row("tools", (n.tools ?? []).length)}
          {n.list && row("list", `max ${n.list.max}`)}
          {n.routes ? (
            Object.keys(n.routes).map((r) => (
              <div key={r} className="gn-r">
                <span>route</span>
                <b>{r}</b>
                {port(
                  "source",
                  `r:${r}`,
                  typeof out === "object" && "routes" in out && out.then === r ? AGENT : LOOP,
                  `route ${r}`,
                )}
              </div>
            ))
          ) : (
            <div className="gn-r">
              <span>then</span>
              {port("source", "next", TEXT)}
            </div>
          )}
          {row("turns", n.caps?.max_turns ?? 6)}
          {g.models?.[id] &&
            row(
              "runs on",
              /^(server|remote):/.test(g.models[id])
                ? g.models[id].split("/").slice(1).join("/")
                : (g.titles[g.models[id]] ?? g.models[id]),
            )}
          {n.director && row("chat", "on")}
        </>
      )}
      {n.kind === "custom" && (
        <>
          <div className="gn-r">
            {port("target", "in", TEXT)}
            <span>node</span>
            <b>{n.node}</b>
            {port("source", "next", TEXT)}
          </div>
          {Object.entries(n.bind ?? {}).map(([k, v]) =>
            row(
              k,
              v.startsWith("$") ? v.slice(1) : ref(v),
              mine.some((p) => p.port === k),
            ),
          )}
        </>
      )}
      {n.kind === "decide" && (
        <>
          <div className="gn-r">
            {port("target", "in", TEXT)}
            <span>reads</span>
            <b>{n.from.length}</b>
          </div>
          {Object.keys(n.routes).map((label) => (
            <div key={label} className="gn-r">
              <span>route</span>
              <b>{label}</b>
              {port(
                "source",
                `r:${label}`,
                typeof out === "object" && "routes" in out && out.then === label ? AGENT : LOOP,
                `route ${label}`,
              )}
            </div>
          ))}
          {typeof out === "object" &&
            "routes" in out &&
            row("at most", `${out.max} · then ${out.then}`)}
        </>
      )}
      {n.kind === "memory" && (
        <>
          <div className="gn-r">
            {port("target", "in", TEXT)}
            <span>{n.op === "read" ? "reads" : "writes"}</span>
            <b>{n.op === "read" ? `${n.limit ?? 8} newest` : n.key}</b>
            {port("source", "next", TEXT)}
          </div>
          {n.op === "read" ? (
            <div className="gn-r">
              <span>examples</span>
              {port("source", "examples", AGENT, "examples out")}
            </div>
          ) : (
            row("from", n.from.length)
          )}
        </>
      )}
      {n.kind === "output" && (
        <>
          <div className="gn-r">
            {port("target", "in", IMAGE)}
            <span>from</span>
            <b>{ref(n.from)}</b>
          </div>
          {row("collection", n.collection)}
        </>
      )}
      {m && m.visits > 1 && n.kind === "step" && (
        <div className="gn-r" style={{ height: 22 }}>
          <span />
          <b>{m.visits} attempts</b>
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

/** Add node: the kinds this version has (Inputs only while there is none); arrows and Enter pick, Escape closes. */
function AddMenu({
  x,
  y,
  hasInputs,
  custom,
  onPick,
  onClose,
}: {
  x: number;
  y: number;
  hasInputs: boolean;
  custom: S<"CustomNode">[];
  onPick: (k: Kind, node?: S<"CustomNode">) => void;
  onClose: () => void;
}) {
  const kinds: [Kind, string, string, S<"CustomNode">?][] = [
    ...(hasInputs ? [] : [["inputs", "Inputs", "the batch's columns"] as [Kind, string, string]]),
    ["step", "Step", "runs a workflow"],
    ["gate", "Gate", "stops the row for your pick"],
    ["judge", "Judge", "checks takes, keeps the best"],
    ["write", "Write", "rewrites a text input"],
    ["decide", "Decide", "routes the row by an answer"],
    ["memory", "Memory", "reads what past batches learnt"],
    ["agent", "Agent", "an LLM with tools; can direct"],
    ["output", "Output", "names the collection"],
    ...custom.map((c): [Kind, string, string, S<"CustomNode">] => [
      "custom",
      c.title,
      c.network ? "custom · calls the network" : "custom",
      c,
    ]),
  ];
  const [active, setActive] = useState(0);
  const first = useRef<HTMLDivElement>(null);
  useEffect(() => first.current?.focus(), []);
  return (
    <div
      ref={first}
      role="listbox"
      tabIndex={-1}
      aria-label="Add node"
      aria-activedescendant={`pk-${active}`}
      className="pop absolute z-20 flex w-[220px] flex-col gap-0.5 outline-none"
      style={{ left: x, top: y }}
      onBlur={(e) => !e.currentTarget.contains(e.relatedTarget) && onClose()}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
          e.preventDefault();
          setActive((a) =>
            Math.max(0, Math.min(kinds.length - 1, a + (e.key === "ArrowDown" ? 1 : -1))),
          );
        } else if (e.key === "Enter") {
          e.preventDefault();
          onPick(kinds[active][0], kinds[active][3]);
        }
      }}
    >
      <span className="heading px-2 pt-1">Add node</span>
      {kinds.map(([k, title, help, c], i) => (
        <div
          key={c ? `custom:${c.name}` : k}
          id={`pk-${i}`}
          role="option"
          aria-selected={i === active}
          className="flex cursor-pointer justify-between gap-2 rounded-[10px] px-2 py-1.5 text-[13px] aria-selected:bg-s4"
          onMouseDown={(e) => e.preventDefault()}
          onMouseEnter={() => setActive(i)}
          onClick={() => onPick(k, c)}
        >
          <span>{title}</span>
          <span className="truncate text-[11px] text-t2">{help}</span>
        </div>
      ))}
    </div>
  );
}

/** elkjs's positions for a pipeline: layered left to right, the loops' fail edges left out so a loop does not
 * push its step to the right. */
async function layout(doc: PDoc): Promise<Record<string, Pos>> {
  const { default: ELK } = await import("elkjs/lib/elk.bundled.js");
  const height = (id: string) => {
    const n = doc.nodes[id];
    const rows =
      n.kind === "inputs"
        ? n.inputs.length
        : n.kind === "step"
          ? 2 + (n.preset ? 1 : 0) + Object.keys(n.bind ?? {}).length
          : n.kind === "gate" || n.kind === "judge"
            ? 7
            : n.kind === "decide"
              ? 2 + Object.keys(n.routes).length
              : n.kind === "agent"
                ? 4 + Object.keys(n.routes ?? {}).length
                : 2;
    return 45 + rows * 26 + 10;
  };
  const edges = doc.edges.flatMap(([from, to], i) =>
    (typeof to === "string"
      ? [to]
      : "routes" in to
        ? [to.routes[to.then]].filter(Boolean)
        : [to.pass, to.then]
    )
      .filter((t) => t in doc.nodes && from in doc.nodes)
      .map((t, j) => ({ id: `e${i}-${j}`, sources: [from], targets: [t] })),
  );
  const r = await new ELK().layout({
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.layered.spacing.nodeNodeBetweenLayers": "40",
      "elk.spacing.nodeNode": "28",
    },
    children: Object.keys(doc.nodes).map((id) => ({ id, width: W, height: height(id) })),
    edges,
  });
  return Object.fromEntries(
    (r.children ?? []).map((c) => [c.id, { x: 16 + (c.x ?? 0), y: 70 + (c.y ?? 0) }]),
  );
}
