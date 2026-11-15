// The graph rules (contracts/README.md "Graph rules (normative)"), as contracts/check.py implements them: problems,
// type compatibility, auto-wiring and wire-drop search. Tested against contracts/fixtures/graphs.
import type { S } from "@/lib/api";
import { frameError } from "@/lib/media";

export type Doc = S<"WorkflowDoc">;
export type DocNode = S<"EditorNode">;
export type PaletteNode = S<"PaletteNode">;
/** kind → its schema and the envs that publish it (the live kinds) */
export type Palette = Record<string, PaletteNode>;
export type Problem = S<"Problem">;
type Code = S<"ProblemCode">;
type Param = S<"ParamSchema">;
type Value = S<"ParamValue">;

export const HANDLES = new Set(["Model", "Conditioning", "Latent"]);
/** a value param's port type: values are both parameter and port (bool, enum and rows take no wire) */
export const WIREABLE: Record<string, string> = {
  int: "Int",
  float: "Float",
  seed: "Seed",
  string: "Text",
  text: "Text",
  image: "Image",
};
const RUN_ONLY = new Set<Code>([
  "unknown_kind",
  "env_mismatch",
  "required_input",
  "no_output",
  "too_many_outputs",
  "too_many_seeds",
  "lora_unknown",
  "lora_family",
]);
/** a LoRA id: a shared one's SHA-256, an own one's UUID */
const LORA_ID = /^([0-9a-f]{64}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;

/** A rows param's Lora field (the stack's `lora`), else undefined. */
const loraField = (p: Param) =>
  p.type === "rows" ? Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Lora") : undefined;
export const MAX_NODES = 64;
export const MAX_BYTES = 262144;
/** the most output nodes a run may have: its work budget */
export const MAX_OUTPUTS = 4;

/** A workflow as an editable document. */
export const docOf = (w: S<"Workflow">): Doc => ({
  title: w.title,
  family: w.family,
  env: w.env,
  nodes: w.nodes as Doc["nodes"],
  ...(w.studio && { studio: w.studio }),
});

/** The palette as a map, from GET /v1/studio/nodes. */
export const paletteOf = (p: S<"NodePalette">): Palette =>
  Object.fromEntries(p.groups.flatMap((g) => g.nodes.map((n) => [n.kind, n])));

/** A stand-in palette from a workflow's own node schemas (when /nodes can't answer): each kind in its env. */
export const paletteFrom = (w: S<"Workflow">): Palette =>
  Object.fromEntries(Object.values(w.node_schemas).map((s) => [s.kind, { ...s, envs: [w.env] }]));

/** What an input of type Media accepts (README, Graph rules: fits). */
const MEDIA = new Set(["Image", "Video"]);
/** Whether a wire of type `got` fits a key of type `want`: equal, or `want` is Media and `got` an Image or Video. */
export const fits = (got: string | null | undefined, want: string | null | undefined) =>
  !!got && !!want && (got === want || (want === "Media" && MEDIA.has(got)));

/** The part of a kind before "/", null for a core kind. */
export const family = (kind: string) => (kind.includes("/") ? kind.split("/")[0] : null);

/** The input each output passes through when bypassed (the first input of its type), or null: not bypassable. */
export function passthrough(s: S<"NodeSchema">): string[] | null {
  const through: string[] = [];
  for (const o of s.outputs) {
    const i = s.inputs.find((x) => x.type === o.type);
    if (!i) return null;
    if (!through.includes(i.name)) through.push(i.name);
  }
  return through.length ? through : null;
}

/** The type a wire into `name` must carry: an input's type, or a wireable value param's port type. */
export function portType(s: S<"NodeSchema">, name: string): string | null {
  const i = s.inputs.find((x) => x.name === name);
  if (i) return i.type;
  const p = s.params.find((x) => x.name === name);
  return (p && WIREABLE[p.type]) ?? null;
}

/** The type of a node's output, or null. */
export const outType = (s: S<"NodeSchema"> | undefined, out: string) =>
  s?.outputs.find((o) => o.name === out)?.type ?? null;

/** Splits a wire `<node>.<out>`. */
export const splitRef = (ref: string) => {
  const i = ref.indexOf(".");
  return [ref.slice(0, i), ref.slice(i + 1)] as const;
};

const isInt = (v: unknown): v is number => typeof v === "number" && Number.isInteger(v);
const codePoints = (s: string) => [...s].length;

/** Why a value breaks the run rules for its param (type, min/max, enum, rows; a workflow holds no take), or null. */
export function valueError(
  p: Param | { type: string; [k: string]: unknown },
  v: unknown,
): string | null {
  const t = p.type;
  const min = p.min as number | undefined;
  const max = p.max as number | undefined;
  if (t === "seed" && v === null) return null;
  if (t === "image") return v === null ? null : "a workflow cannot fill an image param";
  if (t === "rows") {
    if (!Array.isArray(v)) return "not a list";
    if (v.length < (min ?? 0) || (max != null && v.length > max))
      return `${v.length} items is outside ${min ?? 0}..${max}`;
    const item = (p.item ?? {}) as Record<string, unknown>;
    for (let i = 0; i < v.length; i++) {
      const row = v[i];
      if (!row || typeof row !== "object" || Array.isArray(row))
        return `item ${i + 1}: not a record`;
      if (Object.keys(row).some((k) => !(k in item))) return `item ${i + 1}: unknown field`;
      for (const [f, spec] of Object.entries(item)) {
        if (spec === "Image") return `item ${i + 1}.${f}: a workflow cannot fill an image`;
        if (spec === "Lora") {
          const l = (row as Record<string, unknown>)[f] as Record<string, unknown> | undefined;
          const ok =
            !!l &&
            typeof l === "object" &&
            Object.keys(l).length === 1 &&
            LORA_ID.test(String(l.lora_id));
          if (!ok) return `item ${i + 1}.${f}: a LoRA is required`;
          continue;
        }
        const err =
          f in row ? valueError(spec as Param, (row as Record<string, unknown>)[f]) : null;
        if (err) return `item ${i + 1}.${f}: ${err}`;
      }
    }
    return null;
  }
  if (t === "frames") return frameError(p as Param, v);
  if ((t === "int" || t === "seed") && !isInt(v)) return "not an integer";
  if (t === "float" && typeof v !== "number") return "not a number";
  if (t === "bool" && typeof v !== "boolean") return "not a bool";
  if ((t === "string" || t === "text" || t === "enum") && typeof v !== "string")
    return "not a string";
  if (t === "enum")
    return (p.enum as string[]).includes(v as string) ? null : "not one of the options";
  if (t === "bool") return null;
  const n = t === "string" || t === "text" ? codePoints(v as string) : (v as number);
  if ((min != null && n < min) || (max != null && n > max))
    return `outside ${min ?? "…"}..${max ?? "…"}`;
  return null;
}

/** The compact JSON size in UTF-8 bytes (too_large). */
export const docBytes = (doc: Doc) => new TextEncoder().encode(JSON.stringify(doc)).length;

/** Node ids that `from` feeds, directly. */
function successors(doc: Doc): Record<string, Set<string>> {
  const succ: Record<string, Set<string>> = {};
  for (const id of Object.keys(doc.nodes)) succ[id] = new Set();
  for (const [id, n] of Object.entries(doc.nodes))
    for (const ref of Object.values(n.in ?? {})) {
      const src = splitRef(ref)[0];
      if (src in succ) succ[src].add(id);
    }
  return succ;
}

/** Whether `to` can be reached from `from` through wires. */
function reaches(succ: Record<string, Set<string>>, from: string, to: string): boolean {
  const seen = new Set<string>();
  const todo = [...(succ[from] ?? [])];
  while (todo.length) {
    const x = todo.pop()!;
    if (x === to) return true;
    if (!seen.has(x)) {
      seen.add(x);
      todo.push(...(succ[x] ?? []));
    }
  }
  return false;
}

/** Every problem of a document against the palette, sorted as the API sorts them (by node, whole-workflow ones
 * first, then code, port, param). `loras` is the caller's usable LoRAs (id → detected family); without it (the
 * library not known yet) lora_unknown and lora_family are not looked for. */
export function graphProblems(
  doc: Doc,
  palette: Palette,
  loras?: Record<string, string>,
): Problem[] {
  const out = new Map<string, Problem>();
  const add = (code: Code, message: string, node?: string, port?: string, param?: string) => {
    const key = [code, node, port, param].join("\u0000");
    if (out.has(key)) return;
    const p: Problem = { code, message, blocks: RUN_ONLY.has(code) ? "run" : "save" };
    if (node !== undefined) p.node = node;
    if (port !== undefined) p.port = port;
    if (param !== undefined) p.param = param;
    out.set(key, p);
  };
  const nodes = doc.nodes;
  if (Object.keys(nodes).length > MAX_NODES) add("too_many_nodes", `more than ${MAX_NODES} nodes`);
  if (docBytes(doc) > MAX_BYTES) add("too_large", "the workflow is larger than 256 KB");
  if (out.size) return sortProblems([...out.values()]); // nothing else is checked: the work stays bounded
  const outputs = Object.values(nodes).filter((n) => n.kind === "output").length;
  if (outputs > MAX_OUTPUTS)
    add("too_many_outputs", `${outputs} Output nodes; a run may have at most ${MAX_OUTPUTS}`);
  const seeds = Object.values(nodes).flatMap((n) =>
    (palette[n.kind]?.params ?? []).filter((p) => p.type === "seed" && !(p.name in (n.in ?? {}))),
  );
  if (seeds.length > 1)
    add("too_many_seeds", `${seeds.length} unwired seeds; wire one Seed into every stage`);
  for (const [id, n] of Object.entries(nodes)) {
    const wires = n.in ?? {};
    const f = family(n.kind);
    if (f && f !== doc.family) add("wrong_family", `${id}: ${n.kind} is not of ${doc.family}`, id);
    const s = palette[n.kind];
    if (!s) {
      add("unknown_kind", `${id}: no runner offers ${n.kind} now`, id);
      continue;
    }
    if (!s.envs.includes(doc.env))
      add("env_mismatch", `${id}: ${n.kind} does not run on ${doc.env}`, id);
    for (const [k, v] of Object.entries(n.params ?? {})) {
      const p = s.params.find((x) => x.name === k);
      if (!p) add("unknown_param", `${id}.${k}: not a param of ${n.kind}`, id, undefined, k);
      else {
        const err = valueError(p, v);
        const lf = loraField(p);
        if (err) add("param_invalid", `${id}.${k}: ${err}`, id, undefined, k);
        else if (lf) {
          const ids = (v as Record<string, { lora_id: string }>[]).map((r) => r[lf].lora_id);
          const twice = ids.findIndex((x, i) => ids.indexOf(x) < i);
          if (twice >= 0)
            add(
              "lora_duplicate",
              `${id}.${k}: row ${twice + 1} has the LoRA of row ${ids.indexOf(ids[twice]) + 1}`,
              id,
              undefined,
              k,
            );
          if (loras && !n.bypass) {
            const f = family(n.kind);
            const gone = ids.findIndex((x) => !(x in loras));
            const off = ids.findIndex((x) => x in loras && loras[x] !== f);
            if (gone >= 0)
              add("lora_unknown", `${id}.${k}: row ${gone + 1}: LoRA not found`, id, undefined, k);
            if (off >= 0)
              add(
                "lora_family",
                `${id}.${k}: row ${off + 1}: the LoRA does not fit ${f}`,
                id,
                undefined,
                k,
              );
          }
        }
      }
    }
    for (const [port, ref] of Object.entries(wires)) {
      const want = portType(s, port);
      if (!want) {
        add("unknown_input", `${id}.${port}: not an input of ${n.kind}`, id, port);
        continue;
      }
      const [srcId, outName] = splitRef(ref);
      const src = nodes[srcId];
      const ss = src ? palette[src.kind] : undefined;
      if (src && !ss) continue; // an unknown kind's outputs are not checked
      const got = outType(ss, outName);
      if (!got)
        add("unknown_source", `${id}.${port}: ${ref} is not an output in this graph`, id, port);
      else if (!fits(got, want) || (HANDLES.has(want) && family(src!.kind) !== family(n.kind)))
        add("type_mismatch", `${id}.${port}: expects ${want}, got ${got} from ${ref}`, id, port);
    }
    if (n.bypass) {
      const through = passthrough(s);
      if (!through) add("not_bypassable", `${id}: ${s.title} cannot be bypassed`, id);
      for (const i of through ?? [])
        if (!(i in wires))
          add("required_input", `${id}.${i}: connect it to pass it through`, id, i);
    } else {
      for (const i of s.inputs)
        if (!i.optional && !(i.name in wires))
          add("required_input", `${id}.${i.name}: needs a ${i.type} wire`, id, i.name);
    }
  }
  const succ = successors(doc);
  for (const id of Object.keys(nodes))
    if (reaches(succ, id, id)) add("cycle", `${id}: its wires loop back to it`, id);
  if (!Object.values(nodes).some((n) => n.kind === "output"))
    add("no_output", "the workflow has no Output node");

  const badPath = (path: string) => {
    const [id, param] = splitRef(path);
    const n = nodes[id];
    if (!n) return true;
    const s = palette[n.kind];
    if (!s) return false; // an unknown kind's params are not checked
    if (param === "bypass") return passthrough(s) === null;
    return !s.params.some((p) => p.name === param) || param in (n.in ?? {});
  };
  const studio = doc.studio ?? {};
  const placed = [
    ...(studio.composer ?? []),
    ...(studio.quick ?? []),
    ...(studio.inspector ?? []).flatMap((sec) => sec.items),
  ];
  // studio.quick's library tiles (README, Looks) are not paths
  const tile = (p: string) =>
    p === "elements" || ["film", "camera", "colour", "lighting"].some((g) => p === `look:${g}`);
  for (const path of [...placed, ...Object.keys(studio.labels ?? {})])
    if (!tile(path) && badPath(path)) {
      const [id, param] = splitRef(path);
      add(
        "studio_unknown_path",
        `${path}: not a settable param of this graph`,
        id,
        undefined,
        param,
      );
    }
  for (const path of placed)
    if (placed.indexOf(path) !== placed.lastIndexOf(path)) {
      const [id, param] = tile(path) ? [undefined, path] : splitRef(path);
      add("studio_duplicate", `${path}: placed more than once`, id, undefined, param);
    }
  for (const [name, values] of Object.entries(studio.presets ?? {}))
    for (const [path, v] of Object.entries(values)) {
      const [id, param] = splitRef(path);
      const msg = `preset ${name}: ${path} is not valid`;
      if (badPath(path)) add("preset_invalid", msg, id, undefined, param);
      else if (nodes[id] && palette[nodes[id].kind]) {
        const spec =
          param === "bypass"
            ? { type: "bool" }
            : palette[nodes[id].kind].params.find((p) => p.name === param)!;
        if (valueError(spec, v)) add("preset_invalid", msg, id, undefined, param);
      }
    }
  return sortProblems([...out.values()]);
}

/** As the API sorts problems: by node (whole-workflow ones first), then code, port, param. */
export function sortProblems(ps: Problem[]): Problem[] {
  const k = (p: Problem) => [p.node ?? "", p.code, p.port ?? "", p.param ?? ""];
  return ps.slice().sort((a, b) => {
    const x = k(a);
    const y = k(b);
    for (let i = 0; i < x.length; i++) if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1;
    return 0;
  });
}

/** The problems that `b` adds to `a`, by (code, node, port, param). */
export function mergeProblems(a: Problem[], b: Problem[]): Problem[] {
  const key = (p: Problem) => [p.code, p.node, p.port, p.param].join("\u0000");
  const seen = new Set(a.map(key));
  return sortProblems([...a, ...b.filter((p) => !seen.has(key(p)))]);
}

/** Whether a wire from `src`'s output `out` may go into `dst`'s key: the types match (a handle within one family),
 * not into itself, and it makes no cycle. */
export function canConnect(
  doc: Doc,
  palette: Palette,
  src: string,
  out: string,
  dst: string,
  key: string,
): boolean {
  if (src === dst || !doc.nodes[src] || !doc.nodes[dst]) return false;
  const ss = palette[doc.nodes[src].kind];
  const ds = palette[doc.nodes[dst].kind];
  if (!ss || !ds) return false;
  const t = outType(ss, out);
  if (!t || !fits(t, portType(ds, key))) return false;
  if (HANDLES.has(t) && family(ss.kind) !== family(ds.kind)) return false;
  return !reaches(successors(doc), dst, src);
}

/** A new node's id: its kind's last segment ("-" → "_"), then `_2`, `_3`, … for the first free one. */
export function newId(doc: Doc, kind: string): string {
  const base = kind.split("/").pop()!.replace(/-/g, "_").slice(0, 60);
  if (!(base in doc.nodes)) return base;
  for (let i = 2; ; i++) if (!(`${base}_${i}` in doc.nodes)) return `${base}_${i}`;
}

/** The `in` of a node `id` of `kind` just added (README, Auto-wiring): each required input wired when the selected
 * node, else the whole graph, offers exactly one output of its type (a handle: of the kind's family), never from a
 * node the new one already feeds. */
export function autowire(
  doc: Doc,
  palette: Palette,
  selected: string | null,
  id: string,
  kind: string,
): Record<string, string> {
  const wired: Record<string, string> = {};
  // what the new node already feeds (a wire-drop from an input) is never its source: that would be a cycle
  const succ = successors(doc);
  const fed = (m: string) => reaches(succ, id, m);
  const candidates = (ids: string[], t: string) =>
    ids.flatMap((m) => {
      const s = m !== id && doc.nodes[m] && !fed(m) ? palette[doc.nodes[m].kind] : undefined;
      if (!s) return [];
      if (HANDLES.has(t) && family(s.kind) !== family(kind)) return [];
      return s.outputs.filter((o) => fits(o.type, t)).map((o) => `${m}.${o.name}`);
    });
  for (const i of palette[kind].inputs) {
    if (i.optional) continue;
    for (const scope of [...(selected ? [[selected]] : []), Object.keys(doc.nodes)]) {
      const c = candidates(scope, i.type);
      if (c.length === 1) {
        wired[i.name] = c[0];
        break;
      }
    }
  }
  return wired;
}

/** Where a wire was dragged from: a node's output, or its input (or wireable param). */
export type WireEnd = { node: string; port: string; side: "output" | "input" };

/** The type a dragged wire carries. */
export function endType(doc: Doc, palette: Palette, from: WireEnd): string | null {
  const s = palette[doc.nodes[from.node]?.kind];
  if (!s) return null;
  return from.side === "output" ? outType(s, from.port) : portType(s, from.port);
}

/** The kinds offered when a wire is dropped on empty canvas (README, Wire-drop search), in order: the workflow's
 * family first, then core kinds, each by title, then kind. */
export function wireDrop(doc: Doc, palette: Palette, from: WireEnd): string[] {
  const srcKind = doc.nodes[from.node].kind;
  const t = endType(doc, palette, from);
  if (!t) return [];
  const hits: [number, string, string][] = [];
  for (const [k, ks] of Object.entries(palette)) {
    if (!ks.envs.includes(doc.env)) continue;
    const f = family(k);
    if (f !== null && f !== doc.family) continue;
    if (HANDLES.has(t) && f !== family(srcKind)) continue;
    const hit =
      from.side === "input"
        ? ks.outputs.some((o) => fits(o.type, t))
        : [...ks.inputs.map((i) => i.type), ...ks.params.map((p) => WIREABLE[p.type])].some((w) =>
            fits(t, w),
          );
    if (hit) hits.push([f === doc.family ? 0 : 1, ks.title, k]);
  }
  const cmp = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
  hits.sort((a, b) => a[0] - b[0] || cmp(a[1], b[1]) || cmp(a[2], b[2]));
  return hits.map((h) => h[2]);
}

/** The new node's port a dropped wire connects to: for an output dragged, its first input (then wireable param) of
 * the type; for an input dragged, its first output of the type. */
export function dropPort(s: S<"NodeSchema">, side: WireEnd["side"], t: string): string | null {
  if (side === "input") return s.outputs.find((o) => fits(o.type, t))?.name ?? null;
  return (
    s.inputs.find((i) => fits(t, i.type))?.name ??
    s.params.find((p) => fits(t, WIREABLE[p.type]))?.name ??
    null
  );
}

export type { Value as ParamValue };
