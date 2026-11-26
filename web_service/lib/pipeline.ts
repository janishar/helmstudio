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
import { lookFit, type ResolvableLook } from "@/lib/mentions";
import type { Field } from "@/lib/workflow";

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
  "unknown_look",
  "look_fit",
  "unknown_model",
  "remote_not_enabled",
  "remote_off",
  "unknown_custom",
  "custom_input",
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

/** A rows param's one required media field (API 1.22: video or audio references; 1.37 optional ones aside), else
 * null. */
export function mediaField(p: Param): string | null {
  if (p.type !== "rows") return null;
  const fs = Object.entries(p.item ?? {})
    .filter(
      ([, v]) =>
        typeof v === "object" &&
        (v as unknown as Param).type === "media" &&
        !(v as unknown as Param).optional,
    )
    .map(([k]) => k);
  return fs.length === 1 ? fs[0] : null;
}

/** Whether a rows param's item has a `position: seconds` field (README, `bind_type`: only one take fits). */
const timed = (p: Param) =>
  Object.values(p.item ?? {}).some(
    (v) => typeof v === "object" && (v as { position?: string }).position === "seconds",
  );

/** `<node>.<param>` → schema for every unwired param of a known kind: what a step may set or bind. */
export function settable(wf: Doc, palette: Palette): Record<string, Param> {
  const out: Record<string, Param> = {};
  for (const [id, n] of Object.entries(wf.nodes))
    for (const p of palette[n.kind]?.params ?? [])
      if (!(p.name in (n.in ?? {}))) out[`${id}.${p.name}`] = p;
  return out;
}

/** The node ids an edge leads to: one for a plain edge, pass / fail / then for a branch (a judge's may have no
 * then: Ask me, API 1.28), each route of a decide. */
export const targets = (to: PEdge[1]) =>
  typeof to === "string"
    ? [to]
    : "routes" in to
      ? Object.values(to.routes)
      : [to.pass, to.fail, ...(to.then ? [to.then] : [])];

/** The kinds that ask a model once per visit (README, The llm pack and agent nodes); an agent asks up to max_turns. */
export const LLM_KINDS: Kind[] = ["judge", "write", "decide"];
/** The kinds with a `model` (README, Model sources). */
export const MODEL_KINDS: Kind[] = ["judge", "write", "decide", "agent"];
export const P_MAX_CALLS = 32;
/** a prompt's token bound for the remote worst case: 32768 characters / 4 (README, Model sources) */
const PROMPT_TOKENS = 8192;
const OUT_TOKENS = { write: 1400, "write-list": 4096, decide: 800 } as const;
/** An agent's caps with the defaults filled (README, Agent node). */
export const agentCaps = (n: S<"PipelineAgentNode">) => ({
  max_turns: 6,
  max_tool_calls: 12,
  max_tokens: 16000,
  ...n.caps,
});
const sameSet = (a: string[], b: string[]) =>
  a.length === b.length && new Set([...a, ...b]).size === a.length;
/** A node that routes: a decide, or an agent with routes. */
const routes = (n: PNode | undefined): Record<string, string> | null =>
  n?.kind === "decide" || (n?.kind === "agent" && n.routes) ? n.routes! : null;

/** The takes a step makes per visit: seeds × its fan-out's list maximum (0 for any other node). */
export function stepTakes(doc: PDoc): Record<string, number> {
  const a = analyse(doc);
  return Object.fromEntries(
    Object.entries(doc.nodes).map(([id, n]) => [
      id,
      n.kind === "step" ? (n.seeds ?? 1) * a.branches(id) : 0,
    ]),
  );
}

/** The edges as the rules read them (refused edges dropped), the inputs, reach and dominance, and the fan-outs. A
 * capped edge is a branch's fail edge or a route other than its `then`. */
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
  const routed = new Set<string>();
  for (const [from, to] of doc.edges) {
    const ts = targets(to);
    if (!(from in nodes) || ts.some((t) => !(t in nodes))) {
      refused.push(["unknown_node", from]);
      continue;
    }
    const fk = nodes[from].kind;
    const kind = typeof to === "string" ? "plain" : "routes" in to ? "routes" : "branch";
    if (
      (kind === "branch" && fk !== "gate" && fk !== "judge") ||
      (kind === "routes" && fk !== "decide" && fk !== "agent")
    ) {
      refused.push(["branch_not_gate", from]);
      continue;
    }
    if (
      kind === "branch" &&
      typeof to === "object" &&
      !("routes" in to) &&
      !to.then &&
      fk !== "judge"
    ) {
      refused.push(["edge_invalid", from]); // API 1.28: only a judge's branch may Ask me
      continue;
    }
    const r = routes(nodes[from]);
    if (
      (r &&
        (typeof to !== "object" ||
          !("routes" in to) ||
          !sameSet(Object.keys(to.routes), Object.keys(r)) ||
          !(to.then in to.routes))) ||
      (fk === "agent" && kind === "routes" && !r)
    ) {
      refused.push(["decide_routes", from]);
      continue;
    }
    outdeg[from] = (outdeg[from] ?? 0) + 1;
    if (outdeg[from] > 1 || fk === "output" || ts.some((t) => nodes[t].kind === "inputs")) {
      refused.push(["edge_invalid", from]);
      continue;
    }
    if (typeof to === "string") succ[from].push([to, false]);
    else if ("routes" in to) {
      routed.add(from);
      for (const [label, t] of Object.entries(to.routes)) {
        succ[from].push([t, label !== to.then]);
        if (label !== to.then) capped.push([from, t, to.max]);
      }
    } else {
      succ[from].push([to.pass, false], [to.fail, true]);
      if (to.then) succ[from].push([to.then, false]);
      capped.push([from, to.fail, to.max]);
    }
  }
  for (const id of ids)
    if (routes(nodes[id]) && !routed.has(id)) refused.push(["decide_routes", id]);
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
  /** how often a row may visit a node: 1 + the caps of the loops it is on */
  const visits = (id: string) =>
    1 +
    capped
      .filter(([g, f]) => (id === f || reach(f).has(id)) && reach(id).has(g))
      .reduce((s, [, , m]) => s + m, 0);
  /** list producers (a write or agent with a list) → their declared maximum */
  const lists: Record<string, number> = {};
  for (const id of ids) {
    const n = nodes[id];
    if ((n.kind === "write" || n.kind === "agent") && n.list) lists[id] = n.list.max;
  }
  /** fanned-out step → its producer; `badFan`: steps whose `each` names no producer before them */
  const fan: Record<string, string> = {};
  const badFan: string[] = [];
  for (const id of ids) {
    const n = nodes[id];
    if (n.kind !== "step" || !n.each) continue;
    const p = n.each.split(".")[0];
    if (!(p in lists) || !dominates(p, id)) badFan.push(id);
    else fan[id] = p;
  }
  const branches = (id: string) => (id in fan ? lists[fan[id]] : 1);
  return {
    nodes,
    ids,
    roots,
    root,
    inputs,
    succ,
    capped,
    refused,
    reach,
    dominates,
    visits,
    lists,
    fan,
    badFan,
    branches,
  };
}

/** The worst case per row (README, Pipeline rules: worst case): generation runs, LLM calls and tool calls. The remote
 * fields need the model sources: pipelineProblems fills them. */
export function pipelineBounds(doc: PDoc, a = analyse(doc)): S<"PipelineBounds"> {
  let runs = 0;
  let calls = 0;
  let tools = 0;
  for (const id of a.ids) {
    const n = a.nodes[id];
    if (n.kind === "step") runs += (n.seeds ?? 1) * a.branches(id) * a.visits(id);
    if (LLM_KINDS.includes(n.kind)) calls += a.visits(id);
    if (n.kind === "agent") {
      const c = agentCaps(n);
      calls += a.visits(id) * c.max_turns;
      tools += a.visits(id) * c.max_tool_calls;
    }
  }
  const out: S<"PipelineBounds"> = {
    runs,
    llm_calls: calls,
    tool_calls: tools,
    remote_calls: 0,
    remote_tokens: 0,
  };
  for (const id of a.ids) {
    const n = a.nodes[id];
    if (n.kind !== "agent") continue;
    if (n.director) {
      // per batch, whatever its model source
      const c = agentCaps(n);
      const w = n.max_wakes ?? 100;
      out.chat = {
        calls: w * c.max_turns * 2,
        tokens: w * c.max_tokens,
        tool_calls: w * c.max_tool_calls,
      };
    }
    if (n.tools?.includes("rows.add")) out.added_rows = 100;
  }
  return out;
}

type Source =
  | { input: InputType }
  | { takes: number }
  | { text: true; custom?: true }
  | { item: string }
  | { items: string }
  | { any: true };
type Custom = S<"CustomNode">;
type JSchema = {
  type?: string;
  format?: string;
  items?: JSchema;
  maxItems?: number;
  minItems?: number;
  maxLength?: number;
  minLength?: number;
  minimum?: number;
  maximum?: number;
  enum?: unknown[];
  properties?: Record<string, JSchema>;
  required?: string[];
};

/** A custom node's field a source may fill: text (a string), takes (a list of take ids, at most 8), else null. */
export function fieldKind(f: JSchema | undefined): "text" | "takes" | null {
  if (f?.type === "string") return "text";
  if (
    f?.type === "array" &&
    f.items?.type === "string" &&
    f.items.format === "uuid" &&
    (f.maxItems ?? 99) <= 8
  )
    return "takes";
  return null;
}

/** Whether a JSON value fails a (small) JSON schema: type, enum, lengths, ranges, items, required (README, custom
 * nodes: an `inputs` value is checked against its field's schema). */
export function schemaError(s: JSchema, v: unknown): boolean {
  const t = s.type;
  if (t === "string" && typeof v !== "string") return true;
  if ((t === "number" || t === "integer") && typeof v !== "number") return true;
  if (t === "integer" && !Number.isInteger(v)) return true;
  if (t === "boolean" && typeof v !== "boolean") return true;
  if (t === "array" && !Array.isArray(v)) return true;
  if (t === "object" && (typeof v !== "object" || v === null || Array.isArray(v))) return true;
  if (s.enum && !s.enum.some((e) => JSON.stringify(e) === JSON.stringify(v))) return true;
  if (typeof v === "string") {
    const n = [...v].length;
    if (n > (s.maxLength ?? Infinity) || n < (s.minLength ?? 0)) return true;
    if (s.format === "uuid" && !UUID.test(v)) return true;
  }
  if (typeof v === "number" && (v > (s.maximum ?? Infinity) || v < (s.minimum ?? -Infinity)))
    return true;
  if (Array.isArray(v)) {
    if (v.length > (s.maxItems ?? Infinity) || v.length < (s.minItems ?? 0)) return true;
    if (s.items && v.some((x) => schemaError(s.items!, x))) return true;
  }
  if (t === "object" && v && typeof v === "object") {
    const o = v as Record<string, unknown>;
    if ((s.required ?? []).some((k) => !(k in o))) return true;
    for (const [k, p] of Object.entries(s.properties ?? {}))
      if (k in o && schemaError(p, o[k])) return true;
  }
  return false;
}

/** What a source gives: an input of a type, at most n takes, text (a judge's reasons, a memory read's notes, a custom
 * string field), a producer's item or items, or anything (a field of an unknown custom node); null when it names
 * nothing of the right kind. */
function sourceOf(
  a: ReturnType<typeof analyse>,
  src: string,
  custom: Record<string, Custom> = {},
): Source | null {
  if (src.startsWith("$"))
    return src.slice(1) in a.inputs ? { input: a.inputs[src.slice(1)] } : null;
  const [id, what] = src.split(".");
  const n = a.nodes[id];
  if (!n) return null;
  if (what === "takes" && n.kind === "step") return { takes: (n.seeds ?? 1) * a.branches(id) };
  if (what === "picked" && (n.kind === "gate" || n.kind === "judge")) return { takes: n.keep ?? 1 };
  if (
    (what === "reasons" && n.kind === "judge") ||
    (what === "notes" && n.kind === "memory" && n.op === "read")
  )
    return { text: true };
  if (what === "item" && id in a.lists) return { item: id };
  if (what === "items" && id in a.lists) return { items: id };
  if (n.kind === "custom") {
    const reg = custom[n.node];
    if (!reg) return { any: true };
    const f = (reg.output as JSchema).properties?.[what];
    const k = fieldKind(f);
    return k === "text"
      ? { text: true, custom: true }
      : k === "takes"
        ? { takes: f!.maxItems! }
        : null;
  }
  return null;
}

/** Whether a workflow answers text (an ask workflow: its `answer` node's text is wired). */
export const isTextWorkflow = (wf: Doc | undefined) =>
  !!wf && Object.values(wf.nodes).some((n) => n.kind === "answer" && "text" in (n.in ?? {}));

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** A provider as the rules read it (ModelSourceList's sources, or the admin's Provider): kind, models, default. */
export type ModelProvider = {
  name: string;
  kind: "server" | "remote";
  models: string[];
  default_model: string;
  enabled?: boolean;
  host?: string;
  memory_gb?: number;
};
/** What the rules need besides the documents (README, Model sources and Custom nodes): the enabled providers, the
 * deployment's remote switch, the registered custom nodes, and (API 1.74) the pipelines default model a write's,
 * decide's or agent's `auto` runs on (`server:<n>/<m>` or `remote:<p>/<m>`, usable now; absent: the local LLM). */
export type RuleEnv = {
  providers?: ModelProvider[];
  remoteOn?: boolean;
  custom?: Custom[];
  auto?: string;
};

/** The model each judge, write, decide and agent node runs (README, Model): a named ask workflow the caller can run;
 * `auto`: a judge → the ask template of its takes step's family, the others → that of the nearest step before them,
 * else `llm-ask`; `server:<n>[/<m>]` → `server:<n>/<m>` (default filled) and `remote:<p>/<m>` when that provider is
 * enabled with that model. A node with none is left out (`unknown_model`); `blocked` gets the remote ones the
 * switches refuse (they are not resolved). */
export function resolveModels(
  doc: PDoc,
  workflows: Record<string, Doc>,
  env: RuleEnv = {},
  blocked?: (id: string, why: "remote_off" | "remote_not_enabled") => void,
): Record<string, string> {
  const a = analyse(doc);
  const text = (w: string) => isTextWorkflow(workflows[w]);
  const familyAsk = (step: string | undefined) => {
    const n = step ? a.nodes[step] : undefined;
    const fam = n?.kind === "step" ? workflows[n.workflow]?.family : undefined;
    const own = Object.keys(workflows)
      .filter((w) => !UUID.test(w) && workflows[w].family === fam && text(w))
      .sort();
    return fam && own.length ? own[0] : text("llm-ask") ? "llm-ask" : undefined;
  };
  const provs = new Map(
    (env.providers ?? []).filter((p) => p.enabled !== false).map((p) => [p.name, p]),
  );
  const steps = a.ids.filter((id) => a.nodes[id].kind === "step");
  const out: Record<string, string> = {};
  for (const id of a.ids) {
    const n = a.nodes[id];
    if (!MODEL_KINDS.includes(n.kind)) continue;
    const m = (n as { model?: string }).model ?? "auto";
    let wid: string | undefined;
    if (m.startsWith("server:") || m.startsWith("remote:")) {
      const [where, rest] = [m.slice(0, 6), m.slice(7)];
      const [name, ...mm] = rest.split("/");
      const model = mm.join("/") || provs.get(name)?.default_model || "";
      if (where === "remote") {
        const why = remoteRefusal(doc, env, name);
        if (why) {
          blocked?.(id, why);
          continue;
        }
      }
      const p = provs.get(name);
      if (p && p.kind === where && p.models.includes(model)) wid = `${where}:${name}/${model}`;
    } else if (m !== "auto") wid = text(m) ? m : undefined;
    else if (n.kind === "judge") wid = familyAsk(n.takes);
    // the admin's default: no pipeline switch (README, Model sources: the pipelines default model)
    else if (env.auto) wid = env.auto;
    else {
      const doms = steps.filter((s) => s !== id && a.dominates(s, id));
      wid = familyAsk(doms.find((d) => doms.every((o) => o === d || a.dominates(o, d))));
    }
    if (wid) out[id] = wid;
  }
  return out;
}

/** Why a remote call (provider `name`; null for a network custom node) is refused: the deployment's switch, then the
 * pipeline's (README, Model sources); null when both allow it. */
function remoteRefusal(doc: PDoc, env: RuleEnv, name: string | null) {
  if (!env.remoteOn) return "remote_off" as const;
  const r = doc.remote;
  if (!r?.enabled || (name !== null && !(r.providers ?? []).includes(name)))
    return "remote_not_enabled" as const;
  return null;
}

/** Whether a source fits a workflow param (README, `bind_type`). */
export function fits(p: Param, src: Source): boolean {
  if ("text" in src || "item" in src) return INPUT_FITS.Text.includes(p.type);
  if ("any" in src) return true;
  if ("items" in src) return false;
  if ("input" in src)
    return INPUT_FITS[src.input].includes(p.type) || (src.input === "Image" && !!imageField(p));
  return (
    ((p.type === "image" || p.type === "media") && src.takes === 1) ||
    (!!mediaField(p) && src.takes === 1) || // 1.32: one take as item 1 (H3 audios)
    (!!imageField(p) && src.takes <= (p.max ?? 100) && (src.takes === 1 || !timed(p)))
  );
}

/** What leaves the machine (README, Model sources: egress) and the local servers' reserved memory, from the resolved
 * models. */
export function egressOf(
  doc: PDoc,
  models: Record<string, string>,
  env: RuleEnv = {},
  a = analyse(doc),
): { egress: S<"PipelineEgress">[]; reserved: S<"PipelineReserved">[] } {
  const custom = Object.fromEntries((env.custom ?? []).map((c) => [c.name, c]));
  const provs = new Map((env.providers ?? []).map((p) => [p.name, p]));
  const egress: S<"PipelineEgress">[] = [];
  const reserved = new Map<string, S<"PipelineReserved">>();
  for (const id of [...a.ids].sort()) {
    const n = a.nodes[id];
    if (n.kind === "custom" && custom[n.node]?.network)
      egress.push({
        node: id,
        where: "network",
        provider: null,
        model: null,
        fields: [...new Set([...Object.keys(n.inputs ?? {}), ...Object.keys(n.bind ?? {})])]
          .sort()
          .map((f) => `input:${f}`),
        thumbnails: 0,
        per_row: { calls: 0, tokens: 0 },
        chat: null,
      });
    const src = models[id] ?? "";
    if (n.kind === "judge" || !/^(server|remote):/.test(src)) continue;
    const where = src.slice(0, 6) as "server" | "remote";
    const [name, ...mm] = src.slice(7).split("/");
    const p = provs.get(name);
    if (where === "server" && p?.host && p.memory_gb)
      reserved.set(name, { provider: name, host: p.host, memory_gb: p.memory_gb });
    const v = a.visits(id);
    let fields: string[];
    let per_row: { calls: number; tokens: number };
    let chat: { calls: number; tokens: number } | null = null;
    if (n.kind === "agent") {
      const c = agentCaps(n);
      fields = [
        "instruction",
        ...(n.from ?? []),
        ...(n.routes ? ["routes"] : []),
        ...(n.tools ?? []).map((t) => `tool:${t}`),
        ...(n.director ? ["chat"] : []),
      ];
      per_row = { calls: v * c.max_turns * 2, tokens: v * c.max_tokens };
      const w = n.max_wakes ?? 100;
      if (n.director) chat = { calls: w * c.max_turns * 2, tokens: w * c.max_tokens };
    } else {
      const k = n as S<"PipelineWriteNode"> | S<"PipelineDecideNode">;
      fields = [...(k.kind === "write" ? ["instruction"] : ["question", "routes"]), ...k.from];
      const out =
        OUT_TOKENS[k.kind === "write" && k.list ? "write-list" : (k.kind as "write" | "decide")];
      per_row = { calls: v * 2, tokens: v * 2 * (PROMPT_TOKENS + out) };
    }
    egress.push({
      node: id,
      where,
      provider: name,
      model: mm.join("/"),
      fields,
      thumbnails: 0,
      per_row,
      chat,
    });
  }
  return { egress, reserved: [...reserved.keys()].sort().map((k) => reserved.get(k)!) };
}

/** Every problem of a pipeline document; `workflows`: id → the workflow documents the caller can run. Sorted as the
 * API sorts them. `looks` / `loras`: the looks and LoRAs (id → family) the caller can use now; without `looks` (not
 * known yet) a step's looks are not checked. `env`: the providers, the remote switch and the custom nodes. */
export function pipelineProblems(
  doc: PDoc,
  workflows: Record<string, Doc>,
  palette: Palette,
  looks?: ResolvableLook[],
  loras: Record<string, string> = {},
  /** receives the worst case per row, the resolved models, egress and reserved memory (as PipelineValidation's) */
  info?: {
    bounds?: S<"PipelineBounds">;
    models?: Record<string, string>;
    egress?: S<"PipelineEgress">[];
    reserved?: S<"PipelineReserved">[];
  },
  env: RuleEnv = {},
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
  const custom = Object.fromEntries((env.custom ?? []).map((c) => [c.name, c]));
  const src = (s: string) => sourceOf(a, s, custom);
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
        branch_not_gate: `${from}: only a gate or a judge can branch, and only a decide or an agent routes`,
        decide_routes: `${from}: it needs one routes edge with its own labels`,
        edge_invalid: `${from}: this edge is not allowed (one edge out of a node, none out of an output, none into the inputs)`,
      }[code as "unknown_node"],
      from,
    );
  for (const id of ids)
    if (reach(id, undefined, true).has(id))
      add("uncapped_cycle", `${name(id)}: it loops back without a capped fail edge`, id);
  const bounds = pipelineBounds(doc, a);
  if (bounds.runs > P_MAX_RUNS)
    add("too_many_runs", `a row could make ${bounds.runs} runs; at most ${P_MAX_RUNS}`);
  if (bounds.llm_calls > P_MAX_CALLS)
    add(
      "too_many_calls",
      `a row could ask a model ${bounds.llm_calls} times; at most ${P_MAX_CALLS}`,
    );
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
  for (const id of a.badFan)
    add(
      "fanout_source",
      `${name(id)}: it fans out over the list of a Write or Agent every row passes first`,
      id,
      "each",
    );

  for (const id of [...of("gate"), ...of("judge")]) {
    const g = nodes[id] as S<"PipelineGateNode"> | S<"PipelineJudgeNode">;
    const t = nodes[g.takes];
    const made = t?.kind === "step" ? (t.seeds ?? 1) * a.branches(g.takes) : 0;
    if (!t || t.kind !== "step" || !dominates(g.takes, id))
      add("gate_takes", `${name(id)}: its takes must come from a step every row passes first`, id);
    else if (g.takes in a.fan && made > 8)
      add("fanout_takes", `${name(id)}: ${name(g.takes)} can make ${made} takes; at most 8`, id);
    else if ((g.keep ?? 1) > made)
      add("gate_keep", `${name(id)}: it keeps more than ${name(g.takes)} makes`, id);
  }
  for (const id of of("judge")) {
    const j = nodes[id] as S<"PipelineJudgeNode">;
    const lines = [...(j.criteria.must ?? []), ...(j.criteria.nice ?? [])];
    if (!lines.length || lines.length > 12 || new Set(lines).size < lines.length)
      add("judge_criteria", `${name(id)}: its checklist needs 1 to 12 checks, none twice`, id);
    const m = j.memory === undefined ? undefined : nodes[j.memory];
    if (
      j.memory !== undefined &&
      (m?.kind !== "memory" || m.op !== "read" || !dominates(j.memory, id))
    )
      add(
        "bind_source",
        `${name(id)}: its memory must be a Memory read node before it`,
        id,
        "memory",
      );
    if (/^(server|remote):/.test(j.model ?? ""))
      add("model_media", `${name(id)}: a judge sends thumbnails, so it needs a local model`, id);
  }
  for (const id of of("write")) {
    const w = nodes[id] as S<"PipelineWriteNode">;
    if (
      !!w.list === (w.to !== undefined) ||
      (w.to && a.inputs[w.to.slice(1)] !== "Text") ||
      (w.form === "script" && w.list) // 1.32: a script is one text
    )
      add(
        "write_target",
        `${name(id)}: it writes a Text input, or answers a list (one of the two)`,
        id,
      );
  }
  for (const id of ids) {
    const n = nodes[id];
    if (
      n.kind !== "write" &&
      n.kind !== "decide" &&
      n.kind !== "agent" &&
      !(n.kind === "memory" && n.op === "write")
    )
      continue;
    for (const s of n.from ?? []) {
      const x = src(s);
      if (
        !x ||
        "takes" in x ||
        "item" in x ||
        "items" in x ||
        ("input" in x && x.input !== "Text") ||
        (!s.startsWith("$") && !dominates(s.split(".")[0], id))
      )
        add("bind_source", `${name(id)}: ${s} is not text set before it`, id, "from");
    }
  }
  const agents = of("agent");
  const directors = agents.filter((x) => (nodes[x] as S<"PipelineAgentNode">).director);
  for (const id of agents) {
    const n = nodes[id] as S<"PipelineAgentNode">;
    if ((n.tools ?? []).includes("rows.edit") && !n.director)
      add("agent_tools", `${name(id)}: only the director may change rows`, id);
    if (directors.length > 1 && n.director)
      add("director_count", `${name(id)}: a pipeline has at most one director`, id);
  }
  const models = resolveModels(doc, workflows, env, (id, why) =>
    add(
      why,
      why === "remote_off"
        ? `${name(id)}: remote models are off on this deployment`
        : `${name(id)}: turn on remote calls for this pipeline and allow its provider`,
      id,
    ),
  );
  for (const id of ids)
    if (
      MODEL_KINDS.includes(nodes[id].kind) &&
      !(id in models) &&
      ![...out.values()].some((p) => p.node === id && p.code.startsWith("remote_"))
    )
      add("unknown_model", `${name(id)}: its model is not one you can run`, id);
  for (const id of of("custom")) {
    const n = nodes[id] as S<"PipelineCustomNode">;
    const reg = custom[n.node];
    if (!reg) {
      add("unknown_custom", `${name(id)}: ${n.node} is not a custom node of this server now`, id);
      continue;
    }
    if (reg.network) {
      const why = remoteRefusal(doc, env, null);
      if (why)
        add(
          why,
          why === "remote_off"
            ? `${name(id)}: it calls the network, and remote calls are off on this deployment`
            : `${name(id)}: it calls the network; turn on remote calls for this pipeline`,
          id,
        );
    }
    const inSchema = reg.input as JSchema;
    const fields = inSchema.properties ?? {};
    const fixed = n.inputs ?? {};
    const bind = n.bind ?? {};
    for (const f of inSchema.required ?? [])
      if (!(f in fixed) && !(f in bind))
        add("custom_input", `${name(id)}: ${f} needs a value or a source`, id, f);
    for (const [f, v] of Object.entries(fixed))
      if (!(f in fields) || schemaError(fields[f], v))
        add("custom_input", `${name(id)}: ${f} cannot be set to that`, id, f);
    for (const [f, s] of Object.entries(bind)) {
      const x = src(s);
      if (
        !x ||
        "item" in x ||
        "items" in x ||
        (!s.startsWith("$") && !dominates(s.split(".")[0], id))
      ) {
        add("bind_source", `${name(id)}: ${f} ← ${s} is not set before it`, id, f);
        continue;
      }
      const fk = fieldKind(fields[f]);
      const isText = "text" in x || ("input" in x && x.input === "Text");
      if (
        !("any" in x) &&
        (fk === null ||
          (fk === "text") !== isText ||
          (fk === "takes" && (!("takes" in x) || x.takes > fields[f].maxItems!)))
      )
        add("custom_input", `${name(id)}: ${s} does not fit ${f}`, id, f);
    }
  }
  if (info) {
    const e = egressOf(doc, models, env, a);
    const remote = e.egress.filter((x) => x.where === "remote").map((x) => x.per_row);
    info.bounds = {
      ...bounds,
      remote_calls: remote.reduce((s, r) => s + r.calls, 0),
      remote_tokens: remote.reduce((s, r) => s + r.tokens, 0),
    };
    info.models = models;
    info.egress = e.egress;
    info.reserved = e.reserved;
  }
  for (const id of outputs) {
    const s = (nodes[id] as S<"PipelineOutputNode">).from;
    if (!src(s) || (root && !reach(s.split(".")[0]).has(id)))
      add("bind_source", `${name(id)}: ${s} does not lead here`, id, "from");
  }
  for (const id of of("step")) {
    const n = nodes[id] as S<"PipelineStepNode">;
    const binds: Record<string, Source | null> = {};
    for (const [k, v] of Object.entries(n.bind ?? {})) {
      const x = src(v);
      binds[k] = x;
      if (
        !x ||
        ("text" in x && !x.custom) ||
        "items" in x ||
        ("item" in x && a.fan[id] !== x.item) ||
        (!v.startsWith("$") && !("item" in x) && !dominates(v.split(".")[0], id))
      ) {
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
    else if (isTextWorkflow(wf))
      add(
        "workflow_cannot_run",
        `${name(id)}: ${wf.title} answers text; only a judge, write, decide or agent runs it`,
        id,
      );
    if (n.preset !== undefined && !(n.preset in (wf.studio?.presets ?? {})))
      add("unknown_preset", `${name(id)}: ${wf.title} has no preset ${n.preset}`, id);
    const stepLooks = (n as { looks?: string[] }).looks ?? [];
    if (looks && stepLooks.length) {
      const found = new Map(looks.map((x) => [x.id, x]));
      const gone = stepLooks.find((l) => !found.has(l));
      const unfit = gone
        ? null
        : stepLooks
            .map((l) => [l, lookFit(wf.nodes, palette, found.get(l)!, loras)] as const)
            .find(([, r]) => r);
      if (gone) add("unknown_look", `${name(id)}: unknown look ${gone}`, id, undefined, "looks");
      else if (unfit)
        add(
          "look_fit",
          `${name(id)}: look ${found.get(unfit[0])!.name} does not fit ${wf.title} (${unfit[1]})`,
          id,
          undefined,
          "looks",
        );
    }
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
    for (const [k, x] of Object.entries(binds)) {
      if (!(k in params)) {
        add("bind_unknown", `${name(id)}: ${k} is not a setting of ${wf.title}`, id, k);
        continue;
      }
      if (x && !fits(params[k], x))
        add("bind_type", `${name(id)}: ${n.bind![k]} does not fit ${k}`, id, k);
    }
    for (const [k, p] of Object.entries(params))
      if (
        (p.type === "image" || (imageField(p) && (p.min ?? 0) >= 1)) &&
        !(k in binds) &&
        !wf.nodes[k.split(".")[0]]?.optional // a param of an optional node may stay empty (API 1.22)
      )
        add("unbound_input", `${name(id)}: ${k} needs an image; bind it`, id, k);
    if ((n.seeds ?? 1) > 1 && !Object.values(params).some((p) => p.type === "seed"))
      add("step_seeds", `${name(id)}: ${wf.title} has no seed, so it makes one take`, id);
  }
  return done();
}

// ---------------------------------------------------------------- what the editor offers

/** The sources a step may bind to a workflow param, in order: the inputs, then upstream steps' takes and gates'
 * picks (those every row passes before the step), each only when it fits the param's type. */
export function bindCandidates(doc: PDoc, step: string, p: Param, custom: Custom[] = []): string[] {
  const a = analyse(doc);
  const reg = Object.fromEntries(custom.map((c) => [c.name, c]));
  const out: string[] = [];
  for (const [name, type] of Object.entries(a.inputs))
    if (fits(p, { input: type })) out.push(`$${name}`);
  const fan = a.fan[step];
  if (fan && fits(p, { item: fan })) out.push(`${fan}.item`);
  for (const id of order(doc)) {
    const n = a.nodes[id];
    if (id === step || !a.dominates(id, step)) continue;
    if (n.kind === "step" && fits(p, { takes: (n.seeds ?? 1) * a.branches(id) }))
      out.push(`${id}.takes`);
    if ((n.kind === "gate" || n.kind === "judge") && fits(p, { takes: n.keep ?? 1 }))
      out.push(`${id}.picked`);
    if (n.kind === "custom")
      for (const f of customOutputs(reg[n.node])) {
        const x = sourceOf(a, `${id}.${f}`, reg);
        if (x && fits(p, x)) out.push(`${id}.${f}`);
      }
  }
  return out;
}

/** A custom node's output fields a source may name (strings and take-id lists). */
export const customOutputs = (c: Custom | undefined) =>
  Object.entries((c?.output as JSchema | undefined)?.properties ?? {})
    .filter(([, f]) => fieldKind(f))
    .map(([k]) => k);

/** The text a write, decide, agent or memory write may read (its `from`): Text inputs, then the reasons of judges, the
 * notes of memory reads and the text fields of custom nodes every row passes before it. */
export function textCandidates(doc: PDoc, node: string, custom: Custom[] = []): string[] {
  const a = analyse(doc);
  const reg = Object.fromEntries(custom.map((c) => [c.name, c]));
  const out = Object.entries(a.inputs)
    .filter(([, t]) => t === "Text")
    .map(([name]) => `$${name}`);
  for (const id of order(doc)) {
    const n = a.nodes[id];
    if (id === node || !a.dominates(id, node)) continue;
    if (n.kind === "judge") out.push(`${id}.reasons`);
    if (n.kind === "memory" && n.op === "read") out.push(`${id}.notes`);
    if (n.kind === "custom")
      for (const f of customOutputs(reg[n.node]))
        if (fieldKind((reg[n.node].output as JSchema).properties?.[f]) === "text")
          out.push(`${id}.${f}`);
  }
  return out;
}

/** The list producers (a write or agent with a list) a step may fan out over: each one every row passes first. */
export const fanCandidates = (doc: PDoc, step: string) => {
  const a = analyse(doc);
  return order(doc).filter((id) => id in a.lists && a.dominates(id, step));
};

/** The memory read nodes a judge may learn from (each one every row passes before it). */
export const memoryCandidates = (doc: PDoc, judge: string) => {
  const a = analyse(doc);
  return order(doc).filter((id) => {
    const n = a.nodes[id];
    return n.kind === "memory" && n.op === "read" && a.dominates(id, judge);
  });
};

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
    if ((k !== "step" && k !== "gate" && k !== "judge") || !a.reach(id).has(out)) return [];
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
  if (n.kind === "custom") return titles[`custom:${n.node}`] ?? n.node;
  if (n.kind === "agent" && n.director) return "Director";
  return KIND_NAMES[n.kind];
}

export const KIND_NAMES: Record<Kind, string> = {
  inputs: "Inputs",
  step: "Step",
  gate: "Gate",
  output: "Output",
  judge: "Judge",
  write: "Write",
  decide: "Decide",
  memory: "Memory",
  agent: "Agent",
  custom: "Custom",
};
/** Whether a node is an agent node (the mock's `.pl.ag`). */
export const isAgent = (k: Kind) =>
  k === "judge" || k === "write" || k === "decide" || k === "memory" || k === "agent";

/** The pipeline's director agent, if any. */
export const directorOf = (doc: PDoc) =>
  Object.keys(doc.nodes).find((id) => {
    const n = doc.nodes[id];
    return n.kind === "agent" && n.director;
  }) ?? null;

/** Steps numbered in row order: "1 · Qwen Image". */
export function stepNumbers(doc: PDoc): Record<string, number> {
  const steps = order(doc).filter((id) => doc.nodes[id].kind === "step");
  return Object.fromEntries(steps.map((id, i) => [id, i + 1]));
}

/** A pipeline's card tags (docs/mock/Pipelines.dc.html): its agent nodes ("No agent nodes"), loops, chat, fan-out,
 * remote calls. */
export function facts(doc: PDoc): string[] {
  const ns = Object.values(doc.nodes);
  const s = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
  const agents = ns.filter((n) => isAgent(n.kind)).length;
  const out = [agents ? s(agents, "agent node") : "No agent nodes"];
  for (const [, to] of doc.edges)
    if (typeof to !== "string") out.push(`retry loop · cap ${to.max}`);
  if (directorOf(doc)) out.push("chat");
  const fans = ns.flatMap((n) => {
    const p = n.kind === "step" && n.each ? doc.nodes[n.each.split(".")[0]] : undefined;
    return p && (p.kind === "write" || p.kind === "agent") && p.list ? [p.list.max] : [];
  });
  if (fans.length) out.push(`fan-out · max ${Math.max(...fans)}`);
  if (doc.remote?.enabled) out.push("remote model on");
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

/** A write's instruction when none is given (README, write). */
export const WRITE_INSTRUCTION = "Fix what the reviewer found; keep everything else.";
/** A new agent's instruction. */
export const AGENT_INSTRUCTION =
  "Turn the script into a shot list, one prompt per shot, using the @elements in it.";

/** The tools an agent may be given (README, Agents: tools), with what each lets it do. */
export type AgentTool = NonNullable<S<"PipelineAgentNode">["tools"]>[number];
export const AGENT_TOOLS: [AgentTool, string][] = [
  ["gallery.search", "Search the gallery"],
  ["library.read", "Read the library (elements, looks)"],
  ["memory.read", "Read memory"],
  ["memory.write", "Write memory"],
  ["rows.add", "Add rows to the batch"],
  ["rows.edit", "Change rows not yet started (director only)"],
];

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
  opts: { workflow?: string; selected?: string | null; custom?: Custom } = {},
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
  else if (kind === "gate" || kind === "judge") {
    const takes = sel && doc.nodes[sel].kind === "step" ? sel : (steps.at(-1) ?? "");
    node =
      kind === "gate"
        ? { kind, takes, keep: 1, position }
        : {
            kind,
            takes,
            keep: 1,
            mode: "suggest",
            criteria: { must: ["No extra or missing limbs", "No text or watermarks"], nice: [] },
            position,
          };
  } else if (kind === "write" || kind === "decide" || kind === "memory") {
    const draft = {
      ...doc,
      nodes: { ...doc.nodes, [id]: { kind: "memory", op: "read" } as PNode },
    };
    const text = textCandidates(draft, id);
    const prompt = text.find((x) => x === "$prompt") ?? text.find((x) => x.startsWith("$"));
    const reasons = text.find((x) => x.endsWith(".reasons"));
    const from = [prompt, reasons].filter((x): x is string => !!x);
    if (kind === "write")
      node = { kind, from, to: prompt ?? "$prompt", instruction: WRITE_INSTRUCTION, position };
    else if (kind === "decide")
      node = {
        kind,
        question: "Are the takes good enough to go on?",
        from,
        routes: { retry: "the takes miss the brief", accept: "good enough to go on" },
        position,
      };
    else node = { kind, op: "read", limit: 8, position };
  } else if (kind === "agent") {
    const draft = {
      ...doc,
      nodes: { ...doc.nodes, [id]: { kind: "memory", op: "read" } as PNode },
    };
    const prompt = textCandidates(draft, id).find((x) => x.startsWith("$"));
    node = {
      kind,
      instruction: AGENT_INSTRUCTION,
      from: prompt ? [prompt] : [],
      tools: ["library.read"],
      position,
    };
  } else if (kind === "custom") {
    const c = opts.custom;
    const draft = {
      ...doc,
      nodes: { ...doc.nodes, [id]: { kind: "memory", op: "read" } as PNode },
    };
    const text = textCandidates(draft, id);
    // each required string field reads the first Text input; the rest is the user's
    const bind: Record<string, string> = {};
    const ins = (c?.input ?? {}) as JSchema;
    for (const f of ins.required ?? [])
      if (fieldKind(ins.properties?.[f]) === "text" && text[0] && !Object.keys(bind).length)
        bind[f] = text[0];
    node = { kind, node: c?.name ?? "", bind, position };
  } else {
    const from =
      sel && (doc.nodes[sel].kind === "gate" || doc.nodes[sel].kind === "judge")
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

/** How a node's out-edge is connected: `next` (a plain edge or a branch's pass), `fail`, `then`, or a decide's
 * route `r:<label>`; `examples` is a memory read node's wire into a judge (its `memory`, not an edge). */
export type Via = "next" | "fail" | "then" | "examples" | `r:${string}`;

/** A connection from node `from` (its port `via`) to `to`: `next` replaces the node's out-edge (keeping a branch's
 * fail and then; a step wired into a judge or gate is also what it judges: its `takes`); `fail` turns the plain edge
 * into a branch (cap 2; a gate's then: the first output, else the pass node; a judge's none: Ask me); `then` sets a
 * branch's then; `examples` from a memory read node sets the judge's memory. A fail or then needs a pass edge first
 * (the editor offers it only then). */
export function connectNodes(doc: PDoc, from: string, via: Via, to: string): PDoc {
  const target = doc.nodes[to];
  if (via === "examples")
    return target?.kind === "judge" ? setNode(doc, to, { ...target, memory: from }) : doc;
  if (
    via === "next" &&
    doc.nodes[from]?.kind === "step" &&
    (target?.kind === "judge" || target?.kind === "gate")
  )
    doc = setNode(doc, to, { ...target, takes: from });
  const i = doc.edges.findIndex((e) => e[0] === from);
  const cur = i >= 0 ? doc.edges[i][1] : undefined;
  const n = doc.nodes[from];
  let next: PEdge[1];
  const own = routes(n);
  if (own) {
    // a decide (or an agent with routes) has only its routes edge: every label, the first one's node until each is set
    const label = via.startsWith("r:") ? via.slice(2) : Object.keys(own)[0];
    const old = typeof cur === "object" && "routes" in cur ? cur : null;
    const labels = Object.keys(own);
    const routes = Object.fromEntries(
      labels.map((l) => [l, l === label ? to : (old?.routes[l] ?? to)]),
    );
    const then = old && labels.includes(old.then) ? old.then : labels.at(-1)!;
    next = { routes, max: old?.max ?? 2, then };
  } else if (via.startsWith("r:")) return doc;
  else if (via === "next")
    next = typeof cur === "object" && !("routes" in cur) ? { ...cur, pass: to } : to;
  else if (cur === undefined) return doc;
  else if (typeof cur === "object" && "routes" in cur) return doc;
  else if (via === "fail") {
    const pass = typeof cur === "string" ? cur : cur.pass;
    const out = order(doc).find((x) => doc.nodes[x].kind === "output");
    next =
      typeof cur === "object"
        ? { ...cur, fail: to }
        : n.kind === "judge"
          ? { pass, fail: to, max: 2 }
          : { pass, fail: to, max: 2, then: out ?? pass };
  } else next = typeof cur === "string" ? cur : { ...cur, then: to };
  const edges = doc.edges.slice();
  if (i >= 0) edges[i] = [from, next];
  else edges.push([from, next]);
  return { ...doc, edges };
}

/** A connection removed: `next` drops the whole out-edge; `fail` or `then` collapses a branch to its pass edge,
 * except a judge's `then`, which goes back to Ask me. */
export function disconnectNodes(doc: PDoc, from: string, via: Via): PDoc {
  const i = doc.edges.findIndex((e) => e[0] === from);
  if (i < 0) return doc;
  const cur = doc.edges[i][1];
  const edges = doc.edges.slice();
  if (via === "next" || typeof cur === "string" || "routes" in cur) edges.splice(i, 1);
  else if (via === "then" && doc.nodes[from]?.kind === "judge") {
    const { then: _drop, ...rest } = cur; // eslint-disable-line @typescript-eslint/no-unused-vars
    edges[i] = [from, rest];
  } else edges[i] = [from, cur.pass];
  return { ...doc, edges };
}

/** A judge's "On retry: Rewrite prompt" (owner's Q6): a Write node titled so, reading the prompt its step binds
 * and the judge's reasons, writing that prompt back, wired into the step; the judge's fail edge goes to it. */
export function addRewrite(doc: PDoc, judge: string): { doc: PDoc; id: string } {
  const j = doc.nodes[judge] as S<"PipelineJudgeNode">;
  const step = doc.nodes[j.takes];
  const text = new Set(
    Object.values(doc.nodes).flatMap((n) =>
      n.kind === "inputs" ? n.inputs.filter((i) => i.type === "Text").map((i) => "$" + i.name) : [],
    ),
  );
  const bound =
    step?.kind === "step" ? Object.values(step.bind ?? {}).find((v) => text.has(v)) : undefined;
  const to = bound ?? [...text][0] ?? "$prompt";
  const id = newNodeId(doc, "rewrite");
  const at = j.position ?? { x: 0, y: 0 };
  const node: PNode = {
    kind: "write",
    title: "Rewrite prompt",
    from: [to, judge + ".reasons"],
    to,
    instruction: WRITE_INSTRUCTION,
    position: { x: at.x, y: at.y + 220 },
  };
  let d: PDoc = { ...doc, nodes: { ...doc.nodes, [id]: node } };
  if (step) d = connectNodes(d, id, "next", j.takes);
  return { doc: connectNodes(d, judge, "fail", id), id };
}

/** A judge's memory wire removed: it learns from nothing ("Memory: Off"). */
export function forgetMemory(doc: PDoc, judge: string): PDoc {
  const n = doc.nodes[judge];
  if (n?.kind !== "judge" || !n.memory) return doc;
  const { memory: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
  return setNode(doc, judge, rest);
}

/** A copy of a node (its settings and what it reads; no edge out), a little below and right of it. */
export function duplicatePipelineNode(doc: PDoc, id: string): { doc: PDoc; id: string } {
  const n = doc.nodes[id];
  const copy = newNodeId(doc, n.kind);
  const node: PNode = structuredClone(n);
  if (n.position) node.position = { x: n.position.x + 40, y: n.position.y + 40 };
  return { doc: { ...doc, nodes: { ...doc.nodes, [copy]: node } }, id: copy };
}

/** A node's title on its card set (blank: the derived one). */
export function renameNode(doc: PDoc, id: string, title: string): PDoc {
  const n = doc.nodes[id];
  if (!n) return doc;
  const { title: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
  const t = title.trim().slice(0, 60);
  return setNode(doc, id, (t ? { ...rest, title: t } : rest) as PNode);
}

/** A decide's routes changed (labels → meanings): its edge keeps the routes still there; a new label goes where the
 * first route goes. */
export function setRoutes(doc: PDoc, id: string, routes: Record<string, string>): PDoc {
  const n = doc.nodes[id] as S<"PipelineDecideNode"> | S<"PipelineAgentNode">;
  const d = setNode(doc, id, { ...n, routes });
  const e = edgeOut(doc, id)?.[1];
  if (typeof e !== "object" || !("routes" in e)) return d;
  const first = Object.values(e.routes)[0];
  const labels = Object.keys(routes);
  const next = {
    routes: Object.fromEntries(labels.map((l) => [l, e.routes[l] ?? first])),
    max: e.max,
    then: labels.includes(e.then) ? e.then : labels.at(-1)!,
  };
  return { ...d, edges: d.edges.map((x) => (x[0] === id ? [id, next] : x)) };
}

/** A decide's `then` route (the one the cap forces) chosen. */
export const setThen = (doc: PDoc, id: string, then: string): PDoc => ({
  ...doc,
  edges: doc.edges.map((e) =>
    e[0] === id && typeof e[1] === "object" && "routes" in e[1] ? [id, { ...e[1], then }] : e,
  ),
});

/** A branch's (or a decide's) cap changed (1..10). */
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
  const kept = (v: string) => v.startsWith("$") || !gone.has(v.split(".")[0]);
  for (const [id, n] of Object.entries(doc.nodes)) {
    if (gone.has(id)) continue;
    if (n.kind === "step") {
      const bind = Object.fromEntries(Object.entries(n.bind ?? {}).filter(([, v]) => kept(v)));
      const { each, ...rest } = n;
      nodes[id] = { ...rest, ...(n.bind && { bind }), ...(each && kept(each) && { each }) };
    } else if (n.kind === "custom") {
      const bind = Object.fromEntries(Object.entries(n.bind ?? {}).filter(([, v]) => kept(v)));
      nodes[id] = { ...n, bind };
    } else if (n.kind === "agent") nodes[id] = { ...n, from: (n.from ?? []).filter(kept) };
    else if (n.kind === "write" || n.kind === "decide" || (n.kind === "memory" && n.op === "write"))
      nodes[id] = { ...n, from: n.from.filter(kept) };
    else if (n.kind === "judge" && n.memory && gone.has(n.memory)) {
      const { memory: _drop, ...rest } = n; // eslint-disable-line @typescript-eslint/no-unused-vars
      nodes[id] = rest;
    } else nodes[id] = n;
  }
  const edges: PEdge[] = [];
  for (const [from, to] of doc.edges) {
    if (gone.has(from)) continue;
    if (typeof to === "string") {
      if (!gone.has(to)) edges.push([from, to]);
    } else if ("routes" in to) {
      if (!Object.values(to.routes).some((t) => gone.has(t))) edges.push([from, to]);
    } else if (!gone.has(to.pass))
      edges.push(
        gone.has(to.fail) || (to.then && gone.has(to.then)) ? [from, to.pass] : [from, to],
      );
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

/** A pipeline's inputs as the batch form's fields: `<inputs node>.<name>`, typed as the param it fills. */
export const pipelineFields = (p: S<"PipelineDoc">): Field[] =>
  pipelineColumns(p).map((c) => {
    const param = inputParam(c.input);
    return {
      key: c.key,
      node: c.node,
      nodeTitle: param.label ?? c.input.name,
      param,
      initial: param.default ?? null,
      canvas: false,
      model: false,
    };
  });
