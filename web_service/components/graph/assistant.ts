// Graph view's screen actions and summary for the assistant (plan 18 §22.5; contracts/tools/actions.json `graph`):
// the edit helpers of lib/graph-edit, each checked first as the canvas checks a hand edit (the kind is in the
// palette, canConnect for a wire, valueError for a param) and answered with the graph's problems after it. All of a
// chat's edits come back with one "Undo assistant changes" above the graph, so no line has its own Undo. Generate is
// Create's card; its Yes is the graph's own Generate (it saves first).
import { done, failed, type Asked, type Outcome } from "@/lib/assistant";
import type { ScreenSpec } from "@/components/assistant/tools";
import { nodeName, valueText } from "@/components/graph/panel";
import {
  canConnect,
  graphProblems,
  splitRef,
  valueError,
  type Doc,
  type Palette,
  type ParamValue,
  type Problem,
} from "@/lib/graph";
import {
  NODE_W,
  addNode,
  connect,
  disconnect,
  removeNodes,
  setBypass,
  setParam,
} from "@/lib/graph-edit";
import type { S } from "@/lib/api";

type Pos = { x: number; y: number };

/** What Graph view already works out, handed to its tools each render. */
export type GraphCtx = {
  workflow: S<"Workflow">;
  /** the freshest document (several edits run before a render) */
  doc: () => Doc;
  palette: Palette;
  schemas: Record<string, S<"NodeSchema">>;
  loras?: Record<string, string>;
  selected: string | null;
  select: (id: string | null) => void;
  /** an assistant edit (lib/graph-edit on the freshest document); returns the new document */
  edit: (f: (d: Doc) => Doc) => Doc;
  /** a param set here wins over Create's form value of it */
  onParamSet: (key: string) => void;
  problems: Problem[];
  unsaved: boolean;
  /** Create's Generate card, its Yes the graph's Generate */
  generate: () => Promise<Outcome | Asked>;
};

/** Where an added node goes: right of `near` (else of the rightmost node), down until no card is there. */
export function spot(doc: Doc, near: string | null): Pos {
  const ps = Object.values(doc.nodes).flatMap((n) => (n.position ? [n.position] : []));
  const at = near ? doc.nodes[near]?.position : undefined;
  let p = at
    ? { x: at.x + NODE_W + 48, y: at.y }
    : { x: Math.max(16 - NODE_W - 48, ...ps.map((q) => q.x)) + NODE_W + 48, y: 40 };
  while (ps.some((q) => Math.abs(q.x - p.x) < NODE_W && Math.abs(q.y - p.y) < 120))
    p = { ...p, y: p.y + 140 };
  return p;
}

export function graphTools(c: GraphCtx): ScreenSpec {
  const name = (d: Doc, id: string) => nodeName(d, c.schemas, id);
  /** The outcome of an edit: its line, the problems left (the model fixes what is red from these). */
  const after = (d: Doc, text: string): Outcome => {
    const ps = graphProblems(d, c.palette, c.loras);
    return done(text, {
      result: {
        valid: !ps.length,
        problems: ps.slice(0, 12).map((p) => ({ node: p.node ?? null, message: p.message })),
      },
    });
  };
  const kindOf = (k: string) => {
    const v = k.toLowerCase();
    return (
      (c.palette[k] && k) ??
      Object.values(c.palette).find((s) => s.title.toLowerCase() === v)?.kind ??
      null
    );
  };
  const doc = c.doc();
  const sel = c.selected && doc.nodes[c.selected] ? c.selected : null;
  const selSchema = sel ? c.schemas[doc.nodes[sel].kind] : undefined;
  return {
    screen: "graph",
    sees: [
      "Create · Graph",
      `${c.workflow.title} · v${c.workflow.version ?? 1}`,
      ...(sel ? [name(doc, sel)] : []),
    ],
    ids: { workflow_id: c.workflow.id, node: sel ?? "" },
    examples: [
      { text: "Add my rain LoRA at 0.8", sub: "adds and wires the node" },
      { text: "Fix whatever is red", sub: "reads the problems and fixes them" },
      { text: "What does this node do?", sub: "explains the selected node" },
    ],
    summary: () => {
      const d = c.doc();
      return {
        view: "graph",
        workflow: {
          id: c.workflow.id,
          title: c.workflow.title,
          version: c.workflow.version ?? 1,
          source: c.workflow.source,
        },
        node_count: Object.keys(d.nodes).length,
        nodes: Object.entries(d.nodes).map(([id, n]) => ({
          id,
          kind: n.kind,
          title: name(d, id),
          ...(n.bypass && { bypass: true }),
          ...(n.in && { in: n.in }),
        })),
        selected: sel && {
          id: sel,
          kind: d.nodes[sel]?.kind,
          params: d.nodes[sel]?.params ?? {},
          inputs: selSchema?.inputs.map((p) => p.name) ?? [],
          outputs: selSchema?.outputs.map((p) => p.name) ?? [],
          param_names: selSchema?.params.map((p) => p.name) ?? [],
        },
        problems: c.problems.map((p) => ({ node: p.node ?? null, message: p.message })),
        unsaved: c.unsaved,
      };
    },
    actions: {
      addNode: ({ kind, near }) => {
        const k = kindOf(String(kind));
        if (!k) return failed(`Add · no node ${String(kind)} in the palette`);
        const d = c.doc();
        if (near !== undefined && !d.nodes[String(near)])
          return failed(`Add · no node ${String(near)}`);
        const by = near !== undefined ? String(near) : sel;
        let id = "";
        const next = c.edit((x) => {
          const r = addNode(x, c.palette, k, by, spot(x, by));
          id = r.id;
          return r.doc;
        });
        c.select(id);
        const wired = Object.keys(next.nodes[id]?.in ?? {}).length;
        const o = after(
          next,
          `Added ${name(next, id)}${wired ? ` · wired ${wired} input${wired === 1 ? "" : "s"}` : ""}`,
        );
        return { ...o, result: { node: id, ...o.result } };
      },
      removeNodes: ({ ids }) => {
        const d = c.doc();
        const list = (ids as string[]).map(String);
        const missing = list.find((x) => !d.nodes[x]);
        if (missing) return failed(`Remove · no node ${missing}`);
        const names = list.map((x) => name(d, x)).join(", ");
        const next = c.edit((x) => removeNodes(x, list));
        if (sel && list.includes(sel)) c.select(null);
        return after(next, `Removed ${names}`);
      },
      connect: ({ node, key, ref }) => {
        const d = c.doc();
        const dst = String(node);
        const [src, out] = splitRef(String(ref));
        if (!d.nodes[dst]) return failed(`Wire · no node ${dst}`);
        if (!src || !out || !d.nodes[src]) return failed(`Wire · no output ${String(ref)}`);
        if (!canConnect(d, c.palette, src, out, dst, String(key)))
          return failed(
            `Wire · ${name(d, src)} ${out} can't go into ${name(d, dst)} ${String(key)}: the types differ or it makes a loop`,
          );
        const next = c.edit((x) => connect(x, dst, String(key), `${src}.${out}`));
        return after(
          next,
          `Wired ${name(next, src)} ${out} into ${name(next, dst)} ${String(key)}`,
        );
      },
      disconnect: ({ node, key }) => {
        const d = c.doc();
        const n = d.nodes[String(node)];
        if (!n?.in?.[String(key)])
          return failed(`Unwire · nothing goes into ${String(node)} ${String(key)}`);
        const next = c.edit((x) => disconnect(x, String(node), String(key)));
        return after(next, `Unwired ${name(next, String(node))} ${String(key)}`);
      },
      setParam: ({ node, key, value }) => {
        const d = c.doc();
        const id = String(node);
        const n = d.nodes[id];
        if (!n) return failed(`Set · no node ${id}`);
        const p = c.schemas[n.kind]?.params.find((x) => x.name === key);
        if (!p) return failed(`Set · ${name(d, id)} has no ${String(key)}`);
        if (p.name in (n.in ?? {}))
          return failed(`Set · ${name(d, id)} ${p.name} is wired: unwire it first`);
        const why = valueError(p, value);
        if (why) return failed(`Set ${name(d, id)} ${p.label ?? p.name}: ${why}`);
        const next = c.edit((x) => setParam(x, id, p.name, value as ParamValue));
        c.onParamSet(`${id}.${p.name}`);
        return after(next, `Set ${name(next, id)} ${p.label ?? p.name}: ${valueText(p, value)}`);
      },
      setBypass: ({ node, on }) => {
        const id = String(node);
        if (!c.doc().nodes[id]) return failed(`Bypass · no node ${id}`);
        const next = c.edit((x) => setBypass(x, id, !!on));
        return after(next, `${on ? "Bypassed" : "Turned on"} ${name(next, id)}`);
      },
      generate: () => c.generate(),
    },
  };
}
