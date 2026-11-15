// Saved pipelines (contracts/README.md "Saved pipelines", "Pipeline rules (normative)"): the rules as check.py's
// `pipeline_problems` implements them (tested against contracts/fixtures/pipelines), the sources a step may bind by
// type, and the editor's pure edits of a pipeline document.
import type { S } from "@/lib/api";
import {
  graphProblems,
  passthrough,
  sortProblems,
  valueError,
  type Doc,
  type Palette,
} from "@/lib/graph";

export type PDoc = S<"PipelineDoc">;
export type PNode = S<"PipelineNode">;
export type PEdge = S<"PipelineEdge">;
export type PProblem = S<"PipelineProblem">;
type Code = S<"PipelineProblemCode">;
type Param = S<"ParamSchema">;
type Kind = PNode["kind"];
type InputType = S<"PipelineInputType">;

export const P_MAX_NODES = 24;
export const P_MAX_BYTES = 65536;
export const P_MAX_RUNS = 64;
const RUN_ONLY = new Set<Code>([
  "unreachable",
  "dead_end",
  "no_output",
  "unknown_workflow",
  "workflow_cannot_run",
  "unknown_preset",
  "step_input_invalid",
  "bind_unknown",
  "bind_type",
  "unbound_input",
  "step_seeds",
]);
/** the param types a pipeline input of each type fills */
const INPUT_FITS: Record<InputType, string[]> = {
  Text: ["string", "text"],
  Int: ["int"],
  Float: ["float"],
  Seed: ["seed"],
  Image: ["image"],
};

/** A rows param's one Image field (a list of takes binds to it), else null. */
export function imageField(p: Param): string | null {
  if (p.type !== "rows") return null;
  const fs = Object.entries(p.item ?? {})
    .filter(([, v]) => v === "Image")
    .map(([k]) => k);
  return fs.length === 1 ? fs[0] : null;
}

/** `<node>.<param>` → schema for every unwired param of a known kind: what a step may set or bind. */
export function settable(wf: Doc, palette: Palette): Record<string, Param> {
  const out: Record<string, Param> = {};
  for (const [id, n] of Object.entries(wf.nodes))
    for (const p of palette[n.kind]?.params ?? [])
      if (!(p.name in (n.in ?? {}))) out[`${id}.${p.name}`] = p;
  return out;
}

/** The node ids an edge leads to: one for a plain edge, pass / fail / then for a branch. */
export const targets = (to: PEdge[1]) =>
  typeof to === "string" ? [to] : [to.pass, to.fail, to.then];

/** The edges as the rules read them (refused edges dropped), the inputs, reach and dominance. */
export function analyse(doc: PDoc) {
  const nodes = doc.nodes;
  const ids = Object.keys(nodes);
  const roots = ids.filter((id) => nodes[id].kind === "inputs");
  const inputs: Record<string, InputType> = {};
  for (const r of roots)
    for (const i of (nodes[r] as S<"PipelineInputsNode">).inputs) inputs[i.name] = i.type;
  const succ: Record<string, [string, boolean][]> = Object.fromEntries(ids.map((id) => [id, []]));
  const capped: [string, string, number][] = [];
  const refused: [Code, string][] = [];
  const outdeg: Record<string, number> = {};
  for (const [from, to] of doc.edges) {
    const ts = targets(to);
    if (!(from in nodes) || ts.some((t) => !(t in nodes))) {
      refused.push(["unknown_node", from]);
      continue;
    }
    if (typeof to !== "string" && nodes[from].kind !== "gate") {
      refused.push(["branch_not_gate", from]);
      continue;
    }
    outdeg[from] = (outdeg[from] ?? 0) + 1;
    if (
      outdeg[from] > 1 ||
      nodes[from].kind === "output" ||
      ts.some((t) => nodes[t].kind === "inputs")
    ) {
      refused.push(["edge_invalid", from]);
      continue;
    }
    if (typeof to === "string") succ[from].push([to, false]);
    else {
      succ[from].push([to.pass, false], [to.fail, true], [to.then, false]);
      capped.push([from, to.fail, to.max]);
    }
  }
  /** nodes reachable from src in one edge or more, never through `avoid` */
  const reach = (src: string, avoid?: string, uncappedOnly = false) => {
    const seen = new Set<string>();
    const todo = [src];
    while (todo.length)
      for (const [t, cap] of succ[todo.pop()!] ?? [])
        if (t !== avoid && !seen.has(t) && !(uncappedOnly && cap)) {
          seen.add(t);
          todo.push(t);
        }
    return seen;
  };
  const root = roots.length === 1 ? roots[0] : null;
  /** a is on every path from the inputs node to b (unchecked without exactly one inputs node) */
  const dominates = (a: string, b: string) =>
    root === null || a === root || (a !== b && !reach(root, a).has(b));
  return { nodes, ids, roots, root, inputs, succ, capped, refused, reach, dominates };
}

/** What a source gives: an input of a type, or at most n takes; null when it names nothing of the right kind. */
function sourceOf(
  a: ReturnType<typeof analyse>,
  src: string,
): { input: InputType } | { takes: number } | null {
  if (src.startsWith("$"))
    return src.slice(1) in a.inputs ? { input: a.inputs[src.slice(1)] } : null;
  const [id, what] = src.split(".");
  const n = a.nodes[id];
  if (!n || n.kind !== (what === "takes" ? "step" : "gate")) return null;
  return { takes: n.kind === "step" ? (n.seeds ?? 1) : n.kind === "gate" ? (n.keep ?? 1) : 1 };
}

/** Whether a source fits a workflow param (README, `bind_type`). */
export function fits(p: Param, src: { input: InputType } | { takes: number }): boolean {
  if ("input" in src)
    return INPUT_FITS[src.input].includes(p.type) || (src.input === "Image" && !!imageField(p));
  return (
    (p.type === "image" && src.takes === 1) || (!!imageField(p) && src.takes <= (p.max ?? 100))
  );
}

/** Every problem of a pipeline document; `workflows`: id → the workflow documents the caller can run. Sorted as the
 * API sorts them. */
export function pipelineProblems(
  doc: PDoc,
  workflows: Record<string, Doc>,
  palette: Palette,
): PProblem[] {
  const out = new Map<string, PProblem>();
  const add = (code: Code, message: string, node?: string, port?: string, param?: string) => {
    const key = [code, node, port, param].join("\u0000");
    if (out.has(key)) return;
    const p: PProblem = { code, message, blocks: RUN_ONLY.has(code) ? "run" : "save" };
    if (node !== undefined) p.node = node;
    if (port !== undefined) p.port = port;
    if (param !== undefined) p.param = param;
    out.set(key, p);
  };
  const done = () => sortProblems([...out.values()] as S<"Problem">[]) as PProblem[];
  if (Object.keys(doc.nodes).length > P_MAX_NODES)
    add("too_many_nodes", `more than ${P_MAX_NODES} nodes`);
  if (new TextEncoder().encode(JSON.stringify(doc)).length > P_MAX_BYTES)
    add("too_large", "the pipeline is larger than 64 KB");
  if (out.size) return done();

  const a = analyse(doc);
  const { nodes, ids, root, reach, dominates } = a;
  const of = (k: Kind) => ids.filter((id) => nodes[id].kind === k);
  const name = (id: string) => nodes[id]?.title ?? id;
  if (a.roots.length !== 1) add("inputs_node", "a pipeline needs exactly one Inputs node");
  for (const r of a.roots) {
    const names = (nodes[r] as S<"PipelineInputsNode">).inputs.map((i) => i.name);
    for (const x of new Set(names.filter((x, i) => names.indexOf(x) !== i)))
      add("duplicate_input", `${name(r)}: the input ${x} is there twice`, r, undefined, x);
  }
  for (const [code, from] of a.refused)
    add(
      code,
      {
        unknown_node: `${from}: an edge names a node that is not in the pipeline`,
        branch_not_gate: `${from}: only a gate can branch`,
        edge_invalid: `${from}: this edge is not allowed (one edge out of a node, none out of an output, none into the inputs)`,
      }[code as "unknown_node"],
      from,
    );
  for (const id of ids)
    if (reach(id, undefined, true).has(id))
      add("uncapped_cycle", `${name(id)}: it loops back without a capped fail edge`, id);
  const runs = ids
    .filter((id) => nodes[id].kind === "step")
    .reduce((sum, id) => {
      const n = nodes[id] as S<"PipelineStepNode">;
      const loops = a.capped
        .filter(([g, f]) => (id === f || reach(f).has(id)) && reach(id).has(g))
        .reduce((s, [, , m]) => s + m, 0);
      return sum + (n.seeds ?? 1) * (1 + loops);
    }, 0);
  if (runs > P_MAX_RUNS)
    add("too_many_runs", `a row could make ${runs} runs; at most ${P_MAX_RUNS}`);
  if (root) {
    const r = reach(root);
    for (const id of ids)
      if (id !== root && !r.has(id))
        add("unreachable", `${name(id)}: nothing leads to it from the inputs`, id);
  }
  const outputs = of("output");
  if (!outputs.length) add("no_output", "the pipeline has no Output node");
  for (const id of ids)
    if (
      outputs.length &&
      nodes[id].kind !== "output" &&
      ![...reach(id)].some((x) => outputs.includes(x))
    )
      add("dead_end", `${name(id)}: it leads to no Output`, id);

  for (const id of of("gate")) {
    const g = nodes[id] as S<"PipelineGateNode">;
    const t = nodes[g.takes];
    if (!t || t.kind !== "step" || !dominates(g.takes, id))
      add("gate_takes", `${name(id)}: its takes must come from a step every row passes first`, id);
    else if ((g.keep ?? 1) > (t.seeds ?? 1))
      add("gate_keep", `${name(id)}: it keeps more than ${name(g.takes)} makes`, id);
  }
  for (const id of outputs) {
    const src = (nodes[id] as S<"PipelineOutputNode">).from;
    if (!sourceOf(a, src) || (root && !reach(src.split(".")[0]).has(id)))
      add("bind_source", `${name(id)}: ${src} does not lead here`, id, "from");
  }
  for (const id of of("step")) {
    const n = nodes[id] as S<"PipelineStepNode">;
    const binds: Record<string, ReturnType<typeof sourceOf>> = {};
    for (const [k, v] of Object.entries(n.bind ?? {})) {
      binds[k] = sourceOf(a, v);
      if (!binds[k] || (!v.startsWith("$") && !dominates(v.split(".")[0], id))) {
        add("bind_source", `${name(id)}: ${k} ← ${v} is not set before this step`, id, k);
        binds[k] = null;
      }
    }
    const wf = workflows[n.workflow];
    if (!wf) {
      add("unknown_workflow", `${name(id)}: the workflow ${n.workflow} is gone`, id);
      continue;
    }
    const wp = graphProblems(wf, palette);
    if (wp.length) add("workflow_cannot_run", `${name(id)}: ${wf.title}: ${wp[0].message}`, id);
    if (n.preset !== undefined && !(n.preset in (wf.studio?.presets ?? {})))
      add("unknown_preset", `${name(id)}: ${wf.title} has no preset ${n.preset}`, id);
    const params = settable(wf, palette);
    for (const [k, v] of Object.entries(n.inputs ?? {})) {
      const [wn, p] = k.split(".");
      let bad: boolean;
      if (p === "bypass") {
        const s = palette[wf.nodes[wn]?.kind ?? ""];
        bad = !s || passthrough(s) === null || typeof v !== "boolean";
      } else bad = !(k in params) || valueError(params[k], v) !== null;
      if (bad)
        add("step_input_invalid", `${name(id)}: ${k} cannot be set to that`, id, undefined, k);
    }
    for (const [k, src] of Object.entries(binds)) {
      if (!(k in params)) {
        add("bind_unknown", `${name(id)}: ${k} is not a setting of ${wf.title}`, id, k);
        continue;
      }
      if (src && !fits(params[k], src))
        add("bind_type", `${name(id)}: ${n.bind![k]} does not fit ${k}`, id, k);
    }
    for (const [k, p] of Object.entries(params))
      if ((p.type === "image" || (imageField(p) && (p.min ?? 0) >= 1)) && !(k in binds))
        add("unbound_input", `${name(id)}: ${k} needs an image; bind it`, id, k);
    if ((n.seeds ?? 1) > 1 && !Object.values(params).some((p) => p.type === "seed"))
      add("step_seeds", `${name(id)}: ${wf.title} has no seed, so it makes one take`, id);
  }
  return done();
}

// ---------------------------------------------------------------- what the editor offers

/** The sources a step may bind to a workflow param, in order: the inputs, then upstream steps' takes and gates'
 * picks (those every row passes before the step), each only when it fits the param's type. */
export function bindCandidates(doc: PDoc, step: string, p: Param): string[] {
  const a = analyse(doc);
  const out: string[] = [];
  for (const [name, type] of Object.entries(a.inputs))
    if (fits(p, { input: type })) out.push(`$${name}`);
  for (const id of order(doc)) {
    const n = a.nodes[id];
    if (id === step || !a.dominates(id, step)) continue;
    if (n.kind === "step" && fits(p, { takes: n.seeds ?? 1 })) out.push(`${id}.takes`);
    if (n.kind === "gate" && fits(p, { takes: n.keep ?? 1 })) out.push(`${id}.picked`);
  }
  return out;
}

/** The steps a gate may review (each a step every row passes before it). */
export const gateCandidates = (doc: PDoc, gate: string) => {
  const a = analyse(doc);
  return order(doc).filter((id) => a.nodes[id].kind === "step" && a.dominates(id, gate));
};

/** What an output may end with: a step's takes or a gate's picks that lead to it. */
export const outputCandidates = (doc: PDoc, out: string) => {
  const a = analyse(doc);
  return order(doc).flatMap((id) => {
    const k = a.nodes[id].kind;
    if ((k !== "step" && k !== "gate") || !a.reach(id).has(out)) return [];
    return [`${id}.${k === "step" ? "takes" : "picked"}`];
  });
};

/** The nodes in the order a row meets them: from the inputs node along the edges (pass, then fail, then then),
 * the rest after them by id. */
export function order(doc: PDoc): string[] {
  const a = analyse(doc);
  const seen: string[] = [];
  const visit = (id: string) => {
    if (seen.includes(id) || !(id in a.nodes)) return;
    seen.push(id);
    for (const [t] of a.succ[id]) visit(t);
  };
  for (const r of a.roots) visit(r);
  for (const id of [...a.ids].sort()) visit(id);
  return seen;
}

/** A node's name on cards and lists: its title, else the step's workflow title, else its kind. */
export function nodeLabel(doc: PDoc, id: string, titles: Record<string, string> = {}): string {
  const n = doc.nodes[id];
  if (!n) return id;
  if (n.title) return n.title;
  if (n.kind === "step") return titles[n.workflow] ?? n.workflow;
  return { inputs: "Inputs", gate: "Gate", output: "Output" }[n.kind];
}

/** Steps numbered in row order: "1 · Qwen Image". */
export function stepNumbers(doc: PDoc): Record<string, number> {
  const steps = order(doc).filter((id) => doc.nodes[id].kind === "step");
  return Object.fromEntries(steps.map((id, i) => [id, i + 1]));
}

/** A one-line description of a pipeline for its card tags: steps, gates, loops. */
export function facts(doc: PDoc): string[] {
  const ns = Object.values(doc.nodes);
  const count = (k: Kind) => ns.filter((n) => n.kind === k).length;
  const s = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
  const out = [s(count("step"), "step"), s(count("gate"), "gate")];
  for (const [, to] of doc.edges)
    if (typeof to !== "string") out.push(`retry loop · cap ${to.max}`);
  return out;
}

/** The batch columns of a pipeline: `<inputs node>.<name>` with the input's type. */
export function pipelineColumns(doc: PDoc) {
  return Object.entries(doc.nodes).flatMap(([id, n]) =>
    n.kind === "inputs"
      ? n.inputs.map((i) => ({ key: `${id}.${i.name}`, node: id, input: i }))
      : [],
  );
}

/** The param schema a batch column of a pipeline input is typed as (the batch form reads it like a workflow param). */
export function inputParam(i: S<"PipelineInput">): Param {
  const type = { Text: "text", Int: "int", Float: "float", Seed: "seed", Image: "image" }[i.type];
  return {
    name: i.name,
    type,
    label: i.label ?? i.name,
    default: type === "seed" ? -1 : type === "text" ? "" : type === "image" ? null : 0,
  } as Param;
}

// ---------------------------------------------------------------- edits (pure: the editor saves the result)

type Pos = { x: number; y: number };

/** A new node's id: its kind (`step`, `step_2`, …), the first free one. */
export function newNodeId(doc: PDoc, base: string): string {
  if (!(base in doc.nodes)) return base;
  for (let i = 2; ; i++) if (!(`${base}_${i}` in doc.nodes)) return `${base}_${i}`;
}

/** A new pipeline: inputs with a prompt, no steps yet. */
export const blankPipeline = (title: string): PDoc => ({
  title,
  nodes: {
    inputs: {
      kind: "inputs",
      inputs: [{ name: "prompt", type: "Text" }],
      position: { x: 16, y: 40 },
    },
  },
  edges: [],
});

/** A node of `kind` added at `at`, filled so it is close to valid: a step runs `workflow`, a gate reviews the
 * selected step (else the last one), an output ends with the selected step's takes or gate's picks. */
export function addPipelineNode(
  doc: PDoc,
  kind: Kind,
  at: Pos,
  opts: { workflow?: string; selected?: string | null } = {},
): { doc: PDoc; id: string } {
  const id = newNodeId(doc, kind);
  const position = { x: Math.round(at.x), y: Math.round(at.y) };
  // never drop a card on top of another: step right until the spot is free
  const taken = Object.values(doc.nodes).flatMap((n) => (n.position ? [n.position] : []));
  while (taken.some((p) => Math.abs(p.x - position.x) < 200 && Math.abs(p.y - position.y) < 100))
    position.x += 220;
  const steps = order(doc).filter((x) => doc.nodes[x].kind === "step");
  const sel = opts.selected && doc.nodes[opts.selected] ? opts.selected : null;
  let node: PNode;
  if (kind === "inputs") node = { kind, inputs: [{ name: "prompt", type: "Text" }], position };
  else if (kind === "step") node = { kind, workflow: opts.workflow ?? "", seeds: 1, position };
  else if (kind === "gate")
    node = {
      kind,
      takes: sel && doc.nodes[sel].kind === "step" ? sel : (steps.at(-1) ?? ""),
      keep: 1,
      position,
    };
  else {
    const from =
      sel && doc.nodes[sel].kind === "gate"
        ? `${sel}.picked`
        : `${sel && doc.nodes[sel].kind === "step" ? sel : (steps.at(-1) ?? "step")}.takes`;
    node = { kind, from, collection: doc.title.slice(0, 80) || "Collection", position };
  }
  return { doc: { ...doc, nodes: { ...doc.nodes, [id]: node } }, id };
}

/** One node changed (its kind stays). */
export const setNode = (doc: PDoc, id: string, n: PNode): PDoc => ({
  ...doc,
  nodes: { ...doc.nodes, [id]: n },
});

/** The edge out of a node, or undefined. */
export const edgeOut = (doc: PDoc, id: string) => doc.edges.find((e) => e[0] === id);

/** How a gate's out-edge is connected: `next` (a plain edge or a branch's pass), `fail`, or `then`. */
export type Via = "next" | "fail" | "then";

/** A connection from node `from` (its port `via`) to `to`: `next` replaces the node's out-edge (keeping a branch's
 * fail and then); `fail` turns the gate's plain edge into a branch (cap 2, then: the first output, else the pass
 * node); `then` sets a branch's then. A fail or then needs a pass edge first (the editor offers it only then). */
export function connectNodes(doc: PDoc, from: string, via: Via, to: string): PDoc {
  const i = doc.edges.findIndex((e) => e[0] === from);
  const cur = i >= 0 ? doc.edges[i][1] : undefined;
  let next: PEdge[1];
  if (via === "next") next = typeof cur === "object" ? { ...cur, pass: to } : to;
  else if (cur === undefined) return doc;
  else if (via === "fail") {
    const pass = typeof cur === "string" ? cur : cur.pass;
    const out = order(doc).find((x) => doc.nodes[x].kind === "output");
    next =
      typeof cur === "string"
        ? { pass, fail: to, max: 2, then: out ?? pass }
        : { ...cur, fail: to };
  } else next = typeof cur === "string" ? cur : { ...cur, then: to };
  const edges = doc.edges.slice();
  if (i >= 0) edges[i] = [from, next];
  else edges.push([from, next]);
  return { ...doc, edges };
}

/** A connection removed: `next` drops the whole out-edge; `fail` or `then` collapses a branch to its pass edge. */
export function disconnectNodes(doc: PDoc, from: string, via: Via): PDoc {
  const i = doc.edges.findIndex((e) => e[0] === from);
  if (i < 0) return doc;
  const cur = doc.edges[i][1];
  const edges = doc.edges.slice();
  if (via === "next" || typeof cur === "string") edges.splice(i, 1);
  else edges[i] = [from, cur.pass];
  return { ...doc, edges };
}

/** A branch's cap changed (1..10). */
export function setCap(doc: PDoc, gate: string, max: number): PDoc {
  return {
    ...doc,
    edges: doc.edges.map((e) =>
      e[0] === gate && typeof e[1] === "object"
        ? [gate, { ...e[1], max: Math.max(1, Math.min(10, Math.round(max))) }]
        : e,
    ),
  };
}

/** The nodes gone, with the edges into and out of them (a branch losing its fail or then becomes its pass edge)
 * and every bind from them. */
export function removePipelineNodes(doc: PDoc, ids: string[]): PDoc {
  const gone = new Set(ids);
  if (![...gone].some((id) => id in doc.nodes)) return doc;
  const nodes: PDoc["nodes"] = {};
  for (const [id, n] of Object.entries(doc.nodes)) {
    if (gone.has(id)) continue;
    if (n.kind === "step" && n.bind) {
      const bind = Object.fromEntries(
        Object.entries(n.bind).filter(([, v]) => v.startsWith("$") || !gone.has(v.split(".")[0])),
      );
      nodes[id] = { ...n, bind };
    } else nodes[id] = n;
  }
  const edges: PEdge[] = [];
  for (const [from, to] of doc.edges) {
    if (gone.has(from)) continue;
    if (typeof to === "string") {
      if (!gone.has(to)) edges.push([from, to]);
    } else if (!gone.has(to.pass))
      edges.push(gone.has(to.fail) || gone.has(to.then) ? [from, to.pass] : [from, to]);
  }
  return { ...doc, nodes, edges };
}

/** Positions remembered in the document. */
export function movePipelineNodes(doc: PDoc, positions: Record<string, Pos>): PDoc {
  const nodes = { ...doc.nodes };
  for (const [id, p] of Object.entries(positions))
    if (nodes[id])
      nodes[id] = { ...nodes[id], position: { x: Math.round(p.x), y: Math.round(p.y) } };
  return { ...doc, nodes };
}

/** A step's workflow chosen: binds kept where the new workflow has the param, then each image param and the
 * prompt's text bound when exactly one source fits (README: an unbound image is a problem). */
export function chooseWorkflow(
  doc: PDoc,
  step: string,
  workflow: string,
  wf: Doc | undefined,
  palette: Palette,
): PDoc {
  const n = doc.nodes[step] as S<"PipelineStepNode">;
  const params = wf ? settable(wf, palette) : {};
  const bind = Object.fromEntries(Object.entries(n.bind ?? {}).filter(([k]) => k in params));
  const draft = setNode(doc, step, { ...n, workflow, bind });
  for (const [k, p] of Object.entries(params)) {
    if (k in bind) continue;
    const isPrompt = wf!.nodes[k.split(".")[0]]?.kind === "prompt" && p.name === "text";
    if (p.type !== "image" && !(imageField(p) && (p.min ?? 0) >= 1) && !isPrompt) continue;
    const c = bindCandidates(draft, step, p);
    const pick = isPrompt ? c.find((x) => x.startsWith("$")) : c.length === 1 ? c[0] : undefined;
    if (pick) bind[k] = pick;
  }
  const { preset: _drop, inputs: _old, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
  return setNode(doc, step, { ...rest, workflow, bind });
}
