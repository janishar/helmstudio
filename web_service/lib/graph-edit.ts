// Edits of a workflow document in Graph view, its undo history and its elkjs layout. Pure functions: the view
// calls them and saves the result.
import {
  autowire,
  dropPort,
  endType,
  newId,
  splitRef,
  type Doc,
  type DocNode,
  type Palette,
  type ParamValue,
  type WireEnd,
} from "@/lib/graph";
import { unplace } from "@/lib/studio";
import type { S } from "@/lib/api";

type Pos = { x: number; y: number };

const withNode = (doc: Doc, id: string, f: (n: DocNode) => DocNode): Doc => ({
  ...doc,
  nodes: { ...doc.nodes, [id]: f(doc.nodes[id]) },
});

/** A node of `kind` added (its id from the kind), auto-wired (README, Auto-wiring) around `selected`. */
export function addNode(
  doc: Doc,
  palette: Palette,
  kind: string,
  selected: string | null,
  position?: Pos,
): { doc: Doc; id: string } {
  const id = newId(doc, kind);
  const sel = selected && doc.nodes[selected] ? selected : null;
  const wires = autowire(doc, palette, sel, id, kind);
  const node: DocNode = { kind };
  if (Object.keys(wires).length) node.in = wires;
  if (position) node.position = round(position);
  return { doc: { ...doc, nodes: { ...doc.nodes, [id]: node } }, id };
}

/** A node picked from a wire-drop search: added, the dragged wire connected to its first port of the type, then
 * its other inputs auto-wired (the dragged node selected when it was dragged from an output). */
export function addFromDrop(
  doc: Doc,
  palette: Palette,
  from: WireEnd,
  kind: string,
  position?: Pos,
): { doc: Doc; id: string } {
  const t = endType(doc, palette, from);
  const port = t && dropPort(palette[kind], from.side, t);
  const id = newId(doc, kind);
  const node: DocNode = { kind };
  if (position) node.position = round(position);
  let next: Doc = { ...doc, nodes: { ...doc.nodes, [id]: node } };
  if (port && from.side === "output") next = connect(next, id, port, `${from.node}.${from.port}`);
  if (port && from.side === "input") next = connect(next, from.node, from.port, `${id}.${port}`);
  // dragged from an input, from.node consumes the new node: it is no candidate source, so nothing is selected
  const auto = autowire(next, palette, from.side === "output" ? from.node : null, id, kind);
  const have = next.nodes[id].in ?? {};
  const add = Object.fromEntries(Object.entries(auto).filter(([k]) => !(k in have)));
  if (Object.keys(add).length)
    next = withNode(next, id, (n) => ({ ...n, in: { ...have, ...add } }));
  return { doc: next, id };
}

/** The nodes gone, with every wire from them and their Studio paths and preset keys. */
export function removeNodes(doc: Doc, ids: string[]): Doc {
  if (!ids.length) return doc;
  const gone = new Set(ids);
  const nodes: Doc["nodes"] = {};
  for (const [id, n] of Object.entries(doc.nodes)) {
    if (gone.has(id)) continue;
    const kept = Object.entries(n.in ?? {}).filter(([, ref]) => !gone.has(splitRef(ref)[0]));
    const { in: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
    nodes[id] = kept.length ? { ...rest, in: Object.fromEntries(kept) } : rest;
  }
  const out: Doc = { ...doc, nodes };
  if (doc.studio) out.studio = unplace(doc.studio, (p) => gone.has(p.split(".")[0]));
  return out;
}

/** A wire into `node`'s key from `ref` (`<node>.<out>`), replacing the one there; a wired param leaves the Studio. */
export function connect(doc: Doc, node: string, key: string, ref: string): Doc {
  const next = withNode(doc, node, (n) => ({ ...n, in: { ...n.in, [key]: ref } }));
  const path = `${node}.${key}`;
  if (doc.studio) next.studio = unplace(doc.studio, (p) => p === path);
  return next;
}

/** The wire into `node`'s key gone. */
export const disconnect = (doc: Doc, node: string, key: string): Doc =>
  withNode(doc, node, (n) => {
    const wires = { ...n.in };
    delete wires[key];
    const { in: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
    return Object.keys(wires).length ? { ...rest, in: wires } : rest;
  });

/** A wire id as the view names edges: `<node>.<key>`. */
export function removeWires(doc: Doc, wires: string[]): Doc {
  return wires.reduce((d, w) => {
    const [node, key] = splitRef(w);
    return d.nodes[node] ? disconnect(d, node, key) : d;
  }, doc);
}

/** A param set (undefined: back to the schema default). */
export const setParam = (
  doc: Doc,
  node: string,
  name: string,
  value: ParamValue | undefined,
): Doc =>
  withNode(doc, node, (n) => {
    const params = { ...n.params };
    if (value === undefined) delete params[name];
    else params[name] = value;
    return { ...n, params };
  });

/** Bypass on or off (off drops the key). */
export const setBypass = (doc: Doc, node: string, on: boolean): Doc =>
  withNode(doc, node, (n) => {
    const { bypass: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
    return on ? { ...rest, bypass: true } : rest;
  });

const round = (p: Pos) => ({ x: Math.round(p.x), y: Math.round(p.y) });

/** Positions remembered in the document. */
export function moveNodes(doc: Doc, positions: Record<string, Pos>): Doc {
  const nodes = { ...doc.nodes };
  for (const [id, p] of Object.entries(positions))
    if (nodes[id]) nodes[id] = { ...nodes[id], position: round(p) };
  return { ...doc, nodes };
}

/** Whether every node has a position (else the view lays it out). */
export const placed = (doc: Doc) => Object.values(doc.nodes).every((n) => n.position);

// ---------------------------------------------------------------- undo

/** The document (a workflow's, or a pipeline's) and its undo / redo stacks. */
export type History<D = Doc> = { past: D[]; present: D; future: D[] };
export type HistoryAction<D = Doc> =
  { type: "edit"; doc: D } | { type: "undo" } | { type: "redo" } | { type: "reset"; doc: D };

const DEPTH = 100;

export const historyOf = <D = Doc>(doc: D): History<D> => ({ past: [], present: doc, future: [] });

/** The history after an action: an edit pushes the present (and clears redo), undo and redo step, reset starts
 * over. An edit that changes nothing is no step. */
export function history<D = Doc>(h: History<D>, a: HistoryAction<D>): History<D> {
  switch (a.type) {
    case "edit":
      if (a.doc === h.present) return h;
      return { past: [...h.past, h.present].slice(-DEPTH), present: a.doc, future: [] };
    case "undo":
      if (!h.past.length) return h;
      return {
        past: h.past.slice(0, -1),
        present: h.past.at(-1)!,
        future: [h.present, ...h.future],
      };
    case "redo":
      if (!h.future.length) return h;
      return { past: [...h.past, h.present], present: h.future[0], future: h.future.slice(1) };
    case "reset":
      return historyOf(a.doc);
  }
}

// ---------------------------------------------------------------- layout

/** The card's width (docs/mock/Graph.dc.html `.n`). */
export const NODE_W = 172;
const HEADER = 45; // 36 + its border and 8 below
const ROW = 26;

/** The card's height as the view draws it: header, a row per port pair, a row per param (a text param is a preview
 * line), padding; a source node with one value param shows it on its output row. */
export function nodeHeight(s: S<"NodeSchema"> | undefined, node: DocNode): number {
  if (!s) return HEADER + ROW + 8;
  const ports = Math.max(s.inputs.length, s.outputs.length);
  const folded = sizeParams(s, node);
  const params = s.params.filter(
    (p) => !mergedParam(s, p) && !(p.name in (node.in ?? {})) && !folded.includes(p),
  );
  const rows = ports + params.filter((p) => p.type !== "text").length + (folded.length ? 1 : 0);
  const text = params.filter((p) => p.type === "text").length;
  return HEADER + rows * ROW + text * 22 + 8;
}

/** A source node (no inputs, one output) with one param of the output's port type draws it on the output row
 * ("value 45" on Seed). */
export function mergedParam(s: S<"NodeSchema">, p: S<"ParamSchema">): boolean {
  return (
    !s.inputs.length &&
    s.outputs.length === 1 &&
    s.params.length === 1 &&
    s.params[0] === p &&
    p.type !== "text"
  );
}

/** A canvas's size params (width, height and a `mode` enum), drawn as one "size 16:9 · 1 MP" row; none when it has
 * no width and height or one of them is wired. */
export function sizeParams(s: S<"NodeSchema">, node: DocNode): S<"ParamSchema">[] {
  const wired = node.in ?? {};
  const by = (name: string) => s.params.find((p) => p.name === name);
  const w = by("width");
  const h = by("height");
  if (w?.type !== "int" || h?.type !== "int" || "width" in wired || "height" in wired) return [];
  const mode = by("mode");
  return mode?.type === "enum" && !("mode" in wired) ? [mode, w, h] : [w, h];
}

/** The elkjs graph for a document: layered left to right from the sources (every source in the first column), the
 * cards' sizes, one edge per wire. */
export function elkInput(doc: Doc, schemas: Record<string, S<"NodeSchema">>) {
  return {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.layered.spacing.nodeNodeBetweenLayers": "24",
      "elk.spacing.nodeNode": "24",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      "elk.layered.layering.strategy": "LONGEST_PATH_SOURCE",
      "elk.padding": "[top=0,left=0,bottom=0,right=0]",
    },
    children: Object.entries(doc.nodes).map(([id, n]) => ({
      id,
      width: NODE_W,
      height: nodeHeight(schemas[n.kind], n),
    })),
    edges: Object.entries(doc.nodes).flatMap(([id, n]) =>
      Object.entries(n.in ?? {})
        .filter(([, ref]) => splitRef(ref)[0] in doc.nodes)
        .map(([key, ref]) => ({ id: `${id}.${key}`, sources: [splitRef(ref)[0]], targets: [id] })),
    ),
  };
}

const GAP = 40; // the mock's Output: 40 under Decode

/** The positions elkjs chose, offset to start at (16, 40) as the mock's first column does. A node that feeds nothing,
 * wired from one node, sits under that node instead of in a column of its own (the mock's Output under Decode) when
 * the spot is free. */
export function elkPositions(result: {
  children?: { id: string; x?: number; y?: number; height?: number }[];
  edges?: { sources: string[]; targets: string[] }[];
}): Record<string, Pos> {
  const kids = result.children ?? [];
  const edges = result.edges ?? [];
  const at = Object.fromEntries(
    kids.map((c) => [c.id, { x: c.x ?? 0, y: c.y ?? 0, h: c.height ?? 0 }]),
  );
  for (const c of kids) {
    const from = new Set(edges.filter((e) => e.targets[0] === c.id).map((e) => e.sources[0]));
    if (from.size !== 1 || edges.some((e) => e.sources[0] === c.id)) continue;
    const f = at[[...from][0]];
    const spot = { x: f.x, y: f.y + f.h + GAP, h: at[c.id].h };
    const free = kids.every(({ id }) => {
      const o = at[id];
      return (
        id === c.id || o.x !== spot.x || o.y >= spot.y + spot.h + GAP || o.y + o.h + GAP <= spot.y
      );
    });
    if (free) at[c.id] = spot;
  }
  return Object.fromEntries(
    Object.entries(at).map(([id, p]) => [id, { x: 16 + p.x, y: 40 + p.y }]),
  );
}
